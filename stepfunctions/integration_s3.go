package stepfunctions

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/doze-dev/doze-aws/internal/asl"
	"github.com/doze-dev/doze-aws/internal/peercall"
)

// aws-sdk:s3, the one REST-protocol service the generic integration
// reaches. The SDK's input members map onto a path-style request — Bucket
// and Key to the path, ContentType and Metadata to headers, the list
// options to the query string — and the response's headers and XML map back
// onto the SDK's output members, PascalCase, so a machine reads
// $.ContentLength or $.Contents[0].Key exactly as it would on AWS. Errors
// follow the aws-sdk rule: S3.NoSuchKeyException, suffix added.
//
// The action set is fixed here (getObject, putObject, deleteObject,
// headObject, listObjectsV2) because each needs its own mapping; anything
// else is refused at create time by parseSDKResource.

// callS3 maps the five object actions onto path-style requests. Body is a
// string on the way in and out — base64 when the object is not valid UTF-8,
// since a state's data is JSON.
func (s *Server) callS3(ctx context.Context, action string, input []byte) asl.TaskResult {
	var in struct {
		Bucket, Key, Body, ContentType, VersionID, Prefix, Delimiter, ContinuationToken, StartAfter string
		Metadata                                                                                    map[string]string
		MaxKeys                                                                                     int
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return failResult(asl.ErrTaskFailed, "s3:"+action+": "+err.Error())
	}
	if in.Bucket == "" || (action != "listObjectsV2" && in.Key == "") {
		return failResult(asl.ErrTaskFailed, "s3:"+action+" needs Bucket and Key parameters")
	}
	pascal := upperFirst(action)
	query := url.Values{}
	if in.VersionID != "" {
		query.Set("versionId", in.VersionID)
	}
	headers := map[string]string{}
	var method, key string
	var body []byte
	switch action {
	case "getObject":
		method, key = "GET", in.Key
	case "headObject":
		method, key = "HEAD", in.Key
	case "deleteObject":
		method, key = "DELETE", in.Key
	case "putObject":
		method, key, body = "PUT", in.Key, []byte(in.Body)
		if in.ContentType != "" {
			headers["Content-Type"] = in.ContentType
		}
		for k, v := range in.Metadata {
			headers["x-amz-meta-"+k] = v
		}
	case "listObjectsV2":
		method = "GET"
		query.Set("list-type", "2")
		for k, v := range map[string]string{"prefix": in.Prefix, "delimiter": in.Delimiter,
			"continuation-token": in.ContinuationToken, "start-after": in.StartAfter} {
			if v != "" {
				query.Set(k, v)
			}
		}
		if in.MaxKeys > 0 {
			query.Set("max-keys", fmt.Sprint(in.MaxKeys))
		}
	}
	resp, err := peercall.S3Call(ctx, s.peers, pascal, method, in.Bucket, key, query, headers, body)
	if err != nil {
		return failFor(err, "S3", true)
	}
	out, _ := json.Marshal(s3Result(action, resp))
	return asl.TaskResult{Output: out}
}

// s3Result shapes the response the way the SDK's output structure does.
func s3Result(action string, resp *peercall.RESTResponse) map[string]any {
	h := resp.Header
	out := map[string]any{}
	set := func(member, header string) {
		if v := h.Get(header); v != "" {
			out[member] = v
		}
	}
	switch action {
	case "putObject":
		set("ETag", "ETag")
		set("VersionId", "x-amz-version-id")
	case "deleteObject":
		set("VersionId", "x-amz-version-id")
		if h.Get("x-amz-delete-marker") == "true" {
			out["DeleteMarker"] = true
		}
	case "getObject", "headObject":
		set("ETag", "ETag")
		set("ContentType", "Content-Type")
		set("LastModified", "Last-Modified")
		set("VersionId", "x-amz-version-id")
		out["ContentLength"] = len(resp.Body)
		if action == "headObject" {
			if v := h.Get("Content-Length"); v != "" {
				var n int
				fmt.Sscan(v, &n)
				out["ContentLength"] = n
			}
		}
		meta := map[string]string{}
		for k := range h {
			if rest, ok := strings.CutPrefix(strings.ToLower(k), "x-amz-meta-"); ok {
				meta[rest] = h.Get(k)
			}
		}
		out["Metadata"] = meta
		if action == "getObject" {
			out["Body"] = bodyString(resp.Body)
		}
	case "listObjectsV2":
		out = s3ListResult(resp.Body)
	}
	return out
}

// s3ListResult decodes ListBucketResult into the SDK's ListObjectsV2Output
// members, typed: a Choice on $.KeyCount or $.IsTruncated must see a number
// and a boolean, not the XML's text.
func s3ListResult(body []byte) map[string]any {
	var doc struct {
		KeyCount              int    `xml:"KeyCount"`
		MaxKeys               int    `xml:"MaxKeys"`
		IsTruncated           bool   `xml:"IsTruncated"`
		Name                  string `xml:"Name"`
		Prefix                string `xml:"Prefix"`
		Delimiter             string `xml:"Delimiter"`
		ContinuationToken     string `xml:"ContinuationToken"`
		NextContinuationToken string `xml:"NextContinuationToken"`
		Contents              []struct {
			Key, ETag, LastModified, StorageClass string
			Size                                  int64
		} `xml:"Contents"`
		CommonPrefixes []struct{ Prefix string } `xml:"CommonPrefixes"`
	}
	_ = xml.Unmarshal(body, &doc)
	contents := make([]any, 0, len(doc.Contents))
	for _, c := range doc.Contents {
		contents = append(contents, map[string]any{
			"Key": c.Key, "ETag": c.ETag, "LastModified": c.LastModified,
			"Size": c.Size, "StorageClass": c.StorageClass,
		})
	}
	out := map[string]any{
		"Name": doc.Name, "Prefix": doc.Prefix, "KeyCount": doc.KeyCount, "MaxKeys": doc.MaxKeys,
		"IsTruncated": doc.IsTruncated, "Contents": contents,
	}
	for k, v := range map[string]string{"Delimiter": doc.Delimiter,
		"ContinuationToken": doc.ContinuationToken, "NextContinuationToken": doc.NextContinuationToken} {
		if v != "" {
			out[k] = v
		}
	}
	if len(doc.CommonPrefixes) > 0 {
		prefixes := make([]any, 0, len(doc.CommonPrefixes))
		for _, p := range doc.CommonPrefixes {
			prefixes = append(prefixes, map[string]any{"Prefix": p.Prefix})
		}
		out["CommonPrefixes"] = prefixes
	}
	return out
}

// bodyString is the object body as a JSON-safe string: itself when UTF-8,
// otherwise standard base64.
func bodyString(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	return base64.StdEncoding.EncodeToString(b)
}
