package peercall

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/doze-dev/doze-aws/internal/awsquery"
	"github.com/doze-dev/doze-aws/internal/trace"
	"github.com/doze-dev/doze-aws/peers"
)

// The untyped clients: one call per wire protocol, for a caller that knows
// the target service, the action and the parameters but not the shape —
// which is what a Step Functions aws-sdk integration is. Each keeps the
// service's own answer apart from a failure to reach it: an API error comes
// back as *APIError with the code the service spoke, a transport failure as
// a plain error. Step Functions needs the two to be different error NAMES.

// APIError is a non-2xx answer from a peer, decoded from whichever error
// envelope its protocol uses.
type APIError struct {
	Code    string
	Message string
	Status  int
}

func (e *APIError) Error() string { return e.Code + ": " + e.Message }

// JSONCall posts one X-Amz-Target request (the awsJson protocols) and returns
// the response document.
func JSONCall(ctx context.Context, dir peers.Directory, service, target, contentType, resource string, params json.RawMessage) ([]byte, error) {
	ep, ok := dir.Endpoint(service)
	if !ok {
		return nil, fmt.Errorf("no %s peer wired", service)
	}
	_, action, _ := strings.Cut(target, ".")
	var out []byte
	err := trace.Step(ctx, trace.Event{Service: service, Action: action, Resource: resource},
		func(ctx context.Context) error {
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, ep.URL("/"), bytes.NewReader(params))
			if err != nil {
				return err
			}
			req.Header.Set("Content-Type", contentType)
			req.Header.Set("X-Amz-Target", target)
			resp, err := ep.Client.Do(req)
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			body, _ := io.ReadAll(io.LimitReader(resp.Body, maxPeerResponse))
			if resp.StatusCode/100 != 2 {
				return jsonAPIError(resp, body)
			}
			out = body
			return nil
		})
	return out, err
}

// jsonAPIError reads the awsJson error shape: the code in x-amzn-ErrorType
// or the body's __type, either of which AWS may qualify (a "prefix#Code" or
// a "Code:extra" form) — the bare code is what a Catch names.
func jsonAPIError(resp *http.Response, body []byte) *APIError {
	code := resp.Header.Get("x-amzn-ErrorType")
	var e struct {
		Type    string `json:"__type"`
		Message string `json:"message"`
		Msg     string `json:"Message"`
	}
	_ = json.Unmarshal(body, &e)
	if code == "" {
		code = e.Type
	}
	if i := strings.LastIndex(code, "#"); i >= 0 {
		code = code[i+1:]
	}
	if i := strings.Index(code, ":"); i >= 0 {
		code = code[:i]
	}
	msg := e.Message
	if msg == "" {
		msg = e.Msg
	}
	if msg == "" {
		msg = string(body)
	}
	if code == "" {
		code = "UnknownError"
	}
	return &APIError{Code: code, Message: msg, Status: resp.StatusCode}
}

// QueryCall posts one form-encoded Action (the Query protocol) and returns
// the {Action}Result element lifted to JSON.
func QueryCall(ctx context.Context, dir peers.Directory, service, action, resource string, form url.Values) (map[string]any, error) {
	ep, ok := dir.Endpoint(service)
	if !ok {
		return nil, fmt.Errorf("no %s peer wired", service)
	}
	var out map[string]any
	err := trace.Step(ctx, trace.Event{Service: service, Action: action, Resource: resource},
		func(ctx context.Context) error {
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, ep.URL("/"), strings.NewReader(form.Encode()))
			if err != nil {
				return err
			}
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			resp, err := ep.Client.Do(req)
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			body, _ := io.ReadAll(io.LimitReader(resp.Body, maxPeerResponse))
			if resp.StatusCode/100 != 2 {
				code, msg, ok := awsquery.Error(body)
				if !ok {
					code, msg = "UnknownError", string(body)
				}
				return &APIError{Code: code, Message: msg, Status: resp.StatusCode}
			}
			out, err = awsquery.Result(action, body)
			return err
		})
	return out, err
}

// RESTResponse is a REST-protocol answer with its headers kept: S3 carries
// half of an object's metadata there.
type RESTResponse struct {
	Status int
	Header http.Header
	Body   []byte
}

// S3Call performs one path-style request against the local S3 and returns
// the response; a non-2xx answer with an S3 <Error> document is an *APIError.
func S3Call(ctx context.Context, dir peers.Directory, action, method, bucket, key string, query url.Values, headers map[string]string, body []byte) (*RESTResponse, error) {
	ep, ok := dir.Endpoint("s3")
	if !ok {
		return nil, fmt.Errorf("no s3 peer wired")
	}
	path := "/" + bucket
	if key != "" {
		path += "/" + strings.TrimPrefix(key, "/")
	}
	if len(query) > 0 {
		path += "?" + query.Encode()
	}
	resource := bucket
	if key != "" {
		resource = bucket + "/" + key
	}
	var out *RESTResponse
	err := trace.Step(ctx, trace.Event{Service: "s3", Action: action, Resource: resource},
		func(ctx context.Context) error {
			req, err := http.NewRequestWithContext(ctx, method, ep.URL(path), bytes.NewReader(body))
			if err != nil {
				return err
			}
			for k, v := range headers {
				req.Header.Set(k, v)
			}
			resp, err := ep.Client.Do(req)
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			data, _ := io.ReadAll(io.LimitReader(resp.Body, maxPeerResponse))
			if resp.StatusCode/100 != 2 {
				return s3APIError(resp, data)
			}
			out = &RESTResponse{Status: resp.StatusCode, Header: resp.Header, Body: data}
			return nil
		})
	return out, err
}

func s3APIError(resp *http.Response, body []byte) *APIError {
	var e struct {
		XMLName xml.Name `xml:"Error"`
		Code    string   `xml:"Code"`
		Message string   `xml:"Message"`
	}
	if xml.Unmarshal(body, &e) != nil || e.Code == "" {
		// HEAD answers carry no body: S3's own codes for the two statuses
		// a HEAD can fail with.
		switch resp.StatusCode {
		case 404:
			e.Code, e.Message = "NotFound", "Not Found"
		case 403:
			e.Code, e.Message = "Forbidden", "Forbidden"
		default:
			e.Code, e.Message = "UnknownError", string(body)
		}
	}
	return &APIError{Code: e.Code, Message: e.Message, Status: resp.StatusCode}
}
