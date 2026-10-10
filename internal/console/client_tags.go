package console

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

// One tag surface, many wire protocols. Each service tags a different
// identifier (queue URL, ARN, key id, secret name) with its own action names,
// so ResourceTags / SetResourceTag / RemoveResourceTag dispatch on svc. The
// console always passes the natural id it already uses for that service (a name
// or key id); we build the ARN here where the API wants one.

// tagARN maps a console id to the ARN (or id) a service's tag API expects.
func (b *backend) tagARN(svc, id string) string {
	switch svc {
	case "sns":
		return b.id.ARN("sns", id)
	case "ddb":
		return b.id.ARN("dynamodb", "table/"+id)
	case "lambda":
		return b.id.ARN("lambda", "function:"+id)
	case "eb":
		return b.id.ARN("events", "rule/"+id)
	case "sfn":
		return b.id.ARN("states", "stateMachine:"+id)
	case "logs":
		return b.id.ARN("logs", "log-group:"+id)
	case "cw":
		return b.id.ARN("cloudwatch", "alarm:"+id)
	case "apigw":
		// API Gateway ARNs carry no account and a path-shaped resource.
		return "arn:aws:apigateway:" + b.id.RegionName() + "::/restapis/" + id
	case "apigw-http":
		// An HTTP API is /apis/, and the service refuses the other shape.
		return "arn:aws:apigateway:" + b.id.RegionName() + "::/apis/" + id
	default:
		return id
	}
}

// ResourceTags reads a resource's tags as a sorted key/value list.
func (b *backend) ResourceTags(ctx context.Context, svc, id string) ([]KV, error) {
	// IAM entities carry their kind in the svc key ("iam-user" etc.) and go
	// through the ListXxxTags family directly.
	if kind, ok := strings.CutPrefix(svc, "iam-"); ok {
		return b.IAMTags(ctx, kind, id)
	}
	m := map[string]string{}
	var err error
	switch svc {
	case "sqs":
		var out struct {
			Tags map[string]string `json:"Tags"`
		}
		body, e := b.sqs(ctx, "ListQueueTags", map[string]any{"QueueUrl": b.queueURL(id)})
		if e == nil {
			json.Unmarshal(body, &out)
			m = out.Tags
		}
		err = e
	case "ddb":
		var out struct {
			Tags []struct{ Key, Value string } `json:"Tags"`
		}
		body, e := b.ddbCall(ctx, "ListTagsOfResource", map[string]any{"ResourceArn": b.tagARN(svc, id)})
		if e == nil {
			json.Unmarshal(body, &out)
			for _, t := range out.Tags {
				m[t.Key] = t.Value
			}
		}
		err = e
	case "kinesis":
		var out struct {
			Tags []struct{ Key, Value string } `json:"Tags"`
		}
		body, e := b.kinesis(ctx, "ListTagsForStream", map[string]any{"StreamName": id})
		if e == nil {
			json.Unmarshal(body, &out)
			for _, t := range out.Tags {
				m[t.Key] = t.Value
			}
		}
		err = e
	case "kms":
		var out struct {
			Tags []struct{ TagKey, TagValue string } `json:"Tags"`
		}
		body, e := b.json11(ctx, "TrentService", "ListResourceTags", map[string]any{"KeyId": id})
		if e == nil {
			json.Unmarshal(body, &out)
			for _, t := range out.Tags {
				m[t.TagKey] = t.TagValue
			}
		}
		err = e
	case "ssm":
		var out struct {
			TagList []struct{ Key, Value string } `json:"TagList"`
		}
		body, e := b.json11(ctx, "AmazonSSM", "ListTagsForResource", map[string]any{
			"ResourceType": "Parameter", "ResourceId": id,
		})
		if e == nil {
			json.Unmarshal(body, &out)
			for _, t := range out.TagList {
				m[t.Key] = t.Value
			}
		}
		err = e
	case "sm":
		var out struct {
			Tags []struct{ Key, Value string } `json:"Tags"`
		}
		body, e := b.json11(ctx, "secretsmanager", "DescribeSecret", map[string]any{"SecretId": id})
		if e == nil {
			json.Unmarshal(body, &out)
			for _, t := range out.Tags {
				m[t.Key] = t.Value
			}
		}
		err = e
	case "lambda":
		var out struct {
			Tags map[string]string `json:"Tags"`
		}
		body, e := b.lambdaTagsGet(ctx, b.tagARN(svc, id))
		if e == nil {
			json.Unmarshal(body, &out)
			m = out.Tags
		}
		err = e
	case "apigw", "apigw-http":
		// GetTags — the control plane's tag read, addressed by ARN in the path.
		var out struct {
			Tags map[string]string `json:"tags"`
		}
		err = b.apigwGet(ctx, "/tags/"+url.PathEscape(b.tagARN(svc, id)), &out)
		m = out.Tags
	case "logs":
		var out struct {
			Tags map[string]string `json:"tags"`
		}
		body, e := b.json11(ctx, "Logs_20140328", "ListTagsForResource", map[string]any{"resourceArn": b.tagARN(svc, id)})
		if e == nil {
			json.Unmarshal(body, &out)
			m = out.Tags
		}
		err = e
	case "cw":
		// CloudWatch answers a LIST of Key/Value, not the map CloudWatch Logs
		// uses — one service, two tag shapes, and the ARN prefixes differ too.
		var out struct {
			Tags []struct{ Key, Value string }
		}
		body, e := b.cw(ctx, "ListTagsForResource", map[string]any{"ResourceARN": b.tagARN(svc, id)})
		if e == nil {
			json.Unmarshal(body, &out)
			m = map[string]string{}
			for _, t := range out.Tags {
				m[t.Key] = t.Value
			}
		}
		err = e
	case "eb":
		var out struct {
			Tags []struct{ Key, Value string } `json:"Tags"`
		}
		body, e := b.json11(ctx, "AWSEvents", "ListTagsForResource", map[string]any{"ResourceARN": b.tagARN(svc, id)})
		if e == nil {
			json.Unmarshal(body, &out)
			for _, t := range out.Tags {
				m[t.Key] = t.Value
			}
		}
		err = e
	case "sfn":
		// Lowercase members, and a [{key,value}] list rather than a map.
		var out struct {
			Tags []struct {
				Key   string `json:"key"`
				Value string `json:"value"`
			} `json:"tags"`
		}
		body, e := b.sfnCall(ctx, "ListTagsForResource", map[string]any{"resourceArn": b.tagARN(svc, id)})
		if e == nil {
			json.Unmarshal(body, &out)
			for _, t := range out.Tags {
				m[t.Key] = t.Value
			}
		}
		err = e
	default: // sns — Query/XML ListTagsForResource
		m, err = b.queryTags(ctx, b.tagARN(svc, id))
	}
	if err != nil {
		return nil, err
	}
	kvs := make([]KV, 0, len(m))
	for k, v := range m {
		kvs = append(kvs, KV{K: k, V: v})
	}
	sort.Slice(kvs, func(i, j int) bool { return kvs[i].K < kvs[j].K })
	return kvs, nil
}

// tagWritePartial is a batch that was written in pieces and refused part-way:
// the keys before the refusal are on the resource. Only a service that takes
// fewer tags per call than a save can hold (Kinesis, ten) can produce it, and
// the handler says so rather than claiming the save changed nothing.
type tagWritePartial struct {
	Applied []string
	Err     error
}

func (e *tagWritePartial) Error() string { return e.Err.Error() }
func (e *tagWritePartial) Unwrap() error { return e.Err }

// SetResourceTags sets (or overwrites) a batch of tags in one call. Every
// service's tag API takes a set, so one Save is one TagResource: the service
// accepts the whole set or refuses the whole set, and the editor never has to
// say which half of its own draft it managed to write. Kinesis is the one
// exception, which takes at most ten per call, so it is chunked here and says
// so in the doc rather than leaking into the handler.
func (b *backend) SetResourceTags(ctx context.Context, svc, id string, tags map[string]string) error {
	if len(tags) == 0 {
		return nil
	}
	// A stable order, so a chunked save writes the same keys first every time.
	keys := make([]string, 0, len(tags))
	for k := range tags {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	list := func(keyName, valName string) []map[string]string {
		out := make([]map[string]string, 0, len(keys))
		for _, k := range keys {
			out = append(out, map[string]string{keyName: k, valName: tags[k]})
		}
		return out
	}

	if kind, ok := strings.CutPrefix(svc, "iam-"); ok {
		return b.SetIAMTags(ctx, kind, id, keys, tags)
	}
	switch svc {
	case "kinesis":
		const perCall = 10 // AddTagsToStream's limit
		for len(keys) > 0 {
			n := min(perCall, len(keys))
			chunk := make(map[string]string, n)
			for _, k := range keys[:n] {
				chunk[k] = tags[k]
			}
			if _, err := b.kinesis(ctx, "AddTagsToStream", map[string]any{"StreamName": id, "Tags": chunk}); err != nil {
				if done := len(tags) - len(keys); done > 0 {
					all := make([]string, 0, len(tags))
					for k := range tags {
						all = append(all, k)
					}
					sort.Strings(all)
					return &tagWritePartial{Applied: all[:done], Err: err}
				}
				return err
			}
			keys = keys[n:]
		}
		return nil
	case "sqs":
		_, err := b.sqs(ctx, "TagQueue", map[string]any{"QueueUrl": b.queueURL(id), "Tags": tags})
		return err
	case "ddb":
		_, err := b.ddbCall(ctx, "TagResource", map[string]any{"ResourceArn": b.tagARN(svc, id), "Tags": list("Key", "Value")})
		return err
	case "kms":
		_, err := b.json11(ctx, "TrentService", "TagResource", map[string]any{"KeyId": id, "Tags": list("TagKey", "TagValue")})
		return err
	case "sm":
		_, err := b.json11(ctx, "secretsmanager", "TagResource", map[string]any{"SecretId": id, "Tags": list("Key", "Value")})
		return err
	case "ssm":
		_, err := b.json11(ctx, "AmazonSSM", "AddTagsToResource", map[string]any{
			"ResourceType": "Parameter", "ResourceId": id, "Tags": list("Key", "Value"),
		})
		return err
	case "lambda":
		return b.lambdaTagsSet(ctx, b.tagARN(svc, id), tags)
	case "apigw", "apigw-http":
		_, err := b.apigwJSON(ctx, "PUT", "/tags/"+url.PathEscape(b.tagARN(svc, id)), map[string]any{"tags": tags})
		return err
	case "eb":
		_, err := b.json11(ctx, "AWSEvents", "TagResource", map[string]any{"ResourceARN": b.tagARN(svc, id), "Tags": list("Key", "Value")})
		return err
	case "sfn":
		_, err := b.sfnCall(ctx, "TagResource", map[string]any{"resourceArn": b.tagARN(svc, id), "tags": list("key", "value")})
		return err
	case "logs":
		_, err := b.json11(ctx, "Logs_20140328", "TagResource", map[string]any{"resourceArn": b.tagARN(svc, id), "tags": tags})
		return err
	case "cw":
		_, err := b.cw(ctx, "TagResource", map[string]any{"ResourceARN": b.tagARN(svc, id), "Tags": list("Key", "Value")})
		return err
	default: // sns
		v := url.Values{"Action": {"TagResource"}, "ResourceArn": {b.tagARN(svc, id)}}
		for i, k := range keys {
			v.Set(fmt.Sprintf("Tags.member.%d.Key", i+1), k)
			v.Set(fmt.Sprintf("Tags.member.%d.Value", i+1), tags[k])
		}
		_, err := b.queryXML(ctx, v)
		return err
	}
}

// RemoveResourceTags deletes a batch of tags by key in one call.
func (b *backend) RemoveResourceTags(ctx context.Context, svc, id string, keys []string) error {
	if len(keys) == 0 {
		return nil
	}
	keys = append([]string(nil), keys...)
	sort.Strings(keys)
	if kind, ok := strings.CutPrefix(svc, "iam-"); ok {
		return b.RemoveIAMTags(ctx, kind, id, keys)
	}
	switch svc {
	case "kinesis":
		_, err := b.kinesis(ctx, "RemoveTagsFromStream", map[string]any{"StreamName": id, "TagKeys": keys})
		return err
	case "sqs":
		_, err := b.sqs(ctx, "UntagQueue", map[string]any{"QueueUrl": b.queueURL(id), "TagKeys": keys})
		return err
	case "ddb":
		_, err := b.ddbCall(ctx, "UntagResource", map[string]any{"ResourceArn": b.tagARN(svc, id), "TagKeys": keys})
		return err
	case "kms":
		_, err := b.json11(ctx, "TrentService", "UntagResource", map[string]any{"KeyId": id, "TagKeys": keys})
		return err
	case "sm":
		_, err := b.json11(ctx, "secretsmanager", "UntagResource", map[string]any{"SecretId": id, "TagKeys": keys})
		return err
	case "ssm":
		_, err := b.json11(ctx, "AmazonSSM", "RemoveTagsFromResource", map[string]any{
			"ResourceType": "Parameter", "ResourceId": id, "TagKeys": keys,
		})
		return err
	case "lambda":
		return b.lambdaTagsRemove(ctx, b.tagARN(svc, id), keys)
	case "apigw", "apigw-http":
		q := url.Values{"tagKeys": keys}.Encode()
		_, err := b.apigwJSON(ctx, "DELETE", "/tags/"+url.PathEscape(b.tagARN(svc, id))+"?"+q, nil)
		return err
	case "eb":
		_, err := b.json11(ctx, "AWSEvents", "UntagResource", map[string]any{"ResourceARN": b.tagARN(svc, id), "TagKeys": keys})
		return err
	case "sfn":
		_, err := b.sfnCall(ctx, "UntagResource", map[string]any{"resourceArn": b.tagARN(svc, id), "tagKeys": keys})
		return err
	case "logs":
		_, err := b.json11(ctx, "Logs_20140328", "UntagResource", map[string]any{"resourceArn": b.tagARN(svc, id), "tagKeys": keys})
		return err
	case "cw":
		_, err := b.cw(ctx, "UntagResource", map[string]any{"ResourceARN": b.tagARN(svc, id), "TagKeys": keys})
		return err
	default: // sns
		v := url.Values{"Action": {"UntagResource"}, "ResourceArn": {b.tagARN(svc, id)}}
		for i, k := range keys {
			v.Set(fmt.Sprintf("TagKeys.member.%d", i+1), k)
		}
		_, err := b.queryXML(ctx, v)
		return err
	}
}

// queryTags reads Query/XML tags (SNS, EventBridge share the shape).
func (b *backend) queryTags(ctx context.Context, arn string) (map[string]string, error) {
	body, err := b.queryXML(ctx, url.Values{"Action": {"ListTagsForResource"}, "ResourceArn": {arn}})
	if err != nil {
		return nil, err
	}
	var out struct {
		Members []struct {
			Key   string `xml:"Key"`
			Value string `xml:"Value"`
		} `xml:"ListTagsForResourceResult>Tags>member"`
	}
	if err := xml.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	m := map[string]string{}
	for _, t := range out.Members {
		m[t.Key] = t.Value
	}
	return m, nil
}

// ---- Lambda REST tag calls (/2017-03-31/tags/{arn}) ----

func (b *backend) lambdaTagsGet(ctx context.Context, arn string) ([]byte, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", b.base+"/2017-03-31/tags/"+url.PathEscape(arn), nil)
	return b.do(req)
}

func (b *backend) lambdaTagsSet(ctx context.Context, arn string, tags map[string]string) error {
	buf, _ := json.Marshal(map[string]any{"Tags": tags})
	req, _ := http.NewRequestWithContext(ctx, "POST", b.base+"/2017-03-31/tags/"+url.PathEscape(arn), strings.NewReader(string(buf)))
	req.Header.Set("Content-Type", "application/json")
	_, err := b.do(req)
	return err
}

func (b *backend) lambdaTagsRemove(ctx context.Context, arn string, keys []string) error {
	req, _ := http.NewRequestWithContext(ctx, "DELETE",
		b.base+"/2017-03-31/tags/"+url.PathEscape(arn)+"?"+url.Values{"tagKeys": keys}.Encode(), nil)
	_, err := b.do(req)
	return err
}
