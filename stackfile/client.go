package stackfile

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/doze-dev/doze-aws/awsident"
)

// client speaks the real wire protocols against the gateway handler
// in-process — the same honesty rule as the console: apply exercises exactly
// the API an SDK user would.
type client struct {
	h    http.Handler
	base string
}

func newClient(gateway http.Handler) *client {
	return &client{h: gateway, base: "http://stackfile.doze-aws.internal"}
}

// apiErr carries a non-2xx wire response.
type apiErr struct {
	status int
	body   string
}

func (e *apiErr) Error() string { return fmt.Sprintf("aws %d: %s", e.status, e.body) }

// notFound reports whether an error is a 4xx "does not exist" — the signal
// that a resource needs creating rather than a real failure.
func notFound(err error) bool {
	var ae *apiErr
	if !asAPIErr(err, &ae) {
		return false
	}
	if ae.status == 404 {
		return true
	}
	b := ae.body
	for _, marker := range []string{"NonExistent", "DoesNotExist", "NotFound", "ResourceNotFoundException", "NoSuchBucket", "ParameterNotFound"} {
		if strings.Contains(b, marker) {
			return true
		}
	}
	return false
}

func asAPIErr(err error, target **apiErr) bool {
	for err != nil {
		if ae, ok := err.(*apiErr); ok {
			*target = ae
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

func (c *client) do(ctx context.Context, method, path string, headers map[string]string, body []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := &respRec{header: http.Header{}, body: &bytes.Buffer{}}
	c.h.ServeHTTP(rec, req)
	code := rec.code
	if code == 0 {
		code = 200
	}
	out, _ := io.ReadAll(rec.body)
	if code/100 != 2 {
		return nil, &apiErr{status: code, body: string(out)}
	}
	return out, nil
}

type respRec struct {
	code   int
	header http.Header
	body   *bytes.Buffer
}

func (r *respRec) Header() http.Header         { return r.header }
func (r *respRec) Write(b []byte) (int, error) { return r.body.Write(b) }
func (r *respRec) WriteHeader(code int)        { r.code = code }

// jsonTarget posts an X-Amz-Target routed JSON request (SQS 1.0, DDB 1.0,
// KMS/SSM/SM/EB 1.1 — the content type just needs the right family).
func (c *client) jsonTarget(ctx context.Context, target, contentType string, in any) ([]byte, error) {
	body, _ := json.Marshal(in)
	return c.do(ctx, "POST", "/", map[string]string{
		"Content-Type": contentType, "X-Amz-Target": target,
	}, body)
}

func (c *client) sqs(ctx context.Context, action string, in any) ([]byte, error) {
	return c.jsonTarget(ctx, "AmazonSQS."+action, "application/x-amz-json-1.0", in)
}

func (c *client) ddb(ctx context.Context, action string, in any) ([]byte, error) {
	return c.jsonTarget(ctx, "DynamoDB_20120810."+action, "application/x-amz-json-1.0", in)
}

func (c *client) json11(ctx context.Context, prefix, action string, in any) ([]byte, error) {
	return c.jsonTarget(ctx, prefix+"."+action, "application/x-amz-json-1.1", in)
}

// query posts an SNS/STS Query-protocol request (Action in the query string).
func (c *client) query(ctx context.Context, v url.Values) ([]byte, error) {
	return c.do(ctx, "POST", "/?"+v.Encode(), nil, nil)
}

func queueURL(name string) string {
	return "http://stackfile.doze-aws.internal/" + awsident.AccountID + "/" + name
}

func queueARN(name string) string { return awsident.ARN("sqs", name) }
func topicARN(name string) string { return awsident.ARN("sns", name) }

func lambdaARN(name string) string {
	return "arn:aws:lambda:" + awsident.Region + ":" + awsident.AccountID + ":function:" + name
}
