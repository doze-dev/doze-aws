package console

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/doze-dev/doze-aws/awsident"
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
		return awsident.ARN("sns", id)
	case "ddb":
		return awsident.ARN("dynamodb", "table/"+id)
	case "lambda":
		return awsident.ARN("lambda", "function:"+id)
	case "eb":
		return awsident.ARN("events", "rule/"+id)
	default:
		return id
	}
}

// ResourceTags reads a resource's tags as a sorted key/value list.
func (b *backend) ResourceTags(ctx context.Context, svc, id string) ([]KV, error) {
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
	default: // sns, eb — Query/XML ListTagsForResource
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

// SetResourceTag sets (or overwrites) one tag.
func (b *backend) SetResourceTag(ctx context.Context, svc, id, key, value string) error {
	switch svc {
	case "sqs":
		_, err := b.sqs(ctx, "TagQueue", map[string]any{"QueueUrl": b.queueURL(id), "Tags": map[string]string{key: value}})
		return err
	case "ddb":
		_, err := b.ddbCall(ctx, "TagResource", map[string]any{
			"ResourceArn": b.tagARN(svc, id), "Tags": []map[string]string{{"Key": key, "Value": value}},
		})
		return err
	case "kms":
		_, err := b.json11(ctx, "TrentService", "TagResource", map[string]any{
			"KeyId": id, "Tags": []map[string]string{{"TagKey": key, "TagValue": value}},
		})
		return err
	case "sm":
		_, err := b.json11(ctx, "secretsmanager", "TagResource", map[string]any{
			"SecretId": id, "Tags": []map[string]string{{"Key": key, "Value": value}},
		})
		return err
	case "lambda":
		return b.lambdaTagsSet(ctx, b.tagARN(svc, id), map[string]string{key: value})
	default: // sns, eb
		v := url.Values{"Action": {"TagResource"}, "ResourceArn": {b.tagARN(svc, id)}}
		v.Set("Tags.member.1.Key", key)
		v.Set("Tags.member.1.Value", value)
		_, err := b.queryXML(ctx, v)
		return err
	}
}

// RemoveResourceTag deletes one tag by key.
func (b *backend) RemoveResourceTag(ctx context.Context, svc, id, key string) error {
	switch svc {
	case "sqs":
		_, err := b.sqs(ctx, "UntagQueue", map[string]any{"QueueUrl": b.queueURL(id), "TagKeys": []string{key}})
		return err
	case "ddb":
		_, err := b.ddbCall(ctx, "UntagResource", map[string]any{"ResourceArn": b.tagARN(svc, id), "TagKeys": []string{key}})
		return err
	case "kms":
		_, err := b.json11(ctx, "TrentService", "UntagResource", map[string]any{"KeyId": id, "TagKeys": []string{key}})
		return err
	case "sm":
		_, err := b.json11(ctx, "secretsmanager", "UntagResource", map[string]any{"SecretId": id, "TagKeys": []string{key}})
		return err
	case "lambda":
		return b.lambdaTagsRemove(ctx, b.tagARN(svc, id), key)
	default: // sns, eb
		v := url.Values{"Action": {"UntagResource"}, "ResourceArn": {b.tagARN(svc, id)}}
		v.Set("TagKeys.member.1", key)
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

func (b *backend) lambdaTagsRemove(ctx context.Context, arn, key string) error {
	req, _ := http.NewRequestWithContext(ctx, "DELETE",
		b.base+"/2017-03-31/tags/"+url.PathEscape(arn)+"?tagKeys="+url.QueryEscape(key), nil)
	_, err := b.do(req)
	return err
}
