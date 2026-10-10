package peercall

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/peers"
	"github.com/doze-dev/doze-aws/trace"
)

// The untyped clients keep an API error apart from a transport failure, and
// read the code out of every envelope shape the services in this repo (and
// AWS) produce.

func genericPeer(t *testing.T, h http.HandlerFunc) peers.Directory {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	ep := peers.Endpoint{Client: srv.Client(), BaseURL: srv.URL}
	return peers.Static{"dynamodb": ep, "sns": ep, "s3": ep}
}

func TestJSONCallDecodesQualifiedErrorTypes(t *testing.T) {
	var gotTarget, gotCT string
	dir := genericPeer(t, func(w http.ResponseWriter, r *http.Request) {
		gotTarget, gotCT = r.Header.Get("X-Amz-Target"), r.Header.Get("Content-Type")
		switch {
		case strings.HasSuffix(gotTarget, "GetItem"):
			w.Write([]byte(`{"Item":{"pk":{"S":"a"}}}`))
		case strings.HasSuffix(gotTarget, "PutItem"):
			// The namespace-qualified spelling AWS DynamoDB uses.
			w.WriteHeader(400)
			w.Write([]byte(`{"__type":"com.amazonaws.dynamodb.v20120810#ConditionalCheckFailedException","message":"The conditional request failed"}`))
		default:
			// The header spelling, with the ":extra" qualifier some
			// services append.
			w.Header().Set("x-amzn-ErrorType", "ResourceNotFoundException:http://internal.amazon.com/coral/")
			w.WriteHeader(400)
			w.Write([]byte(`{"Message":"Requested resource not found"}`))
		}
	})
	ctx := context.Background()
	out, err := JSONCall(ctx, dir, "dynamodb", "DynamoDB_20120810.GetItem", "application/x-amz-json-1.0", "t", []byte(`{}`))
	if err != nil || !strings.Contains(string(out), `"pk"`) || gotTarget != "DynamoDB_20120810.GetItem" || gotCT != "application/x-amz-json-1.0" {
		t.Fatalf("GetItem: %s %v (target %q ct %q)", out, err, gotTarget, gotCT)
	}
	_, err = JSONCall(ctx, dir, "dynamodb", "DynamoDB_20120810.PutItem", "application/x-amz-json-1.0", "t", []byte(`{}`))
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "ConditionalCheckFailedException" || apiErr.Status != 400 {
		t.Errorf("PutItem error = %v, want the bare ConditionalCheckFailedException", err)
	}
	_, err = JSONCall(ctx, dir, "dynamodb", "DynamoDB_20120810.DeleteItem", "application/x-amz-json-1.0", "t", []byte(`{}`))
	if !errors.As(err, &apiErr) || apiErr.Code != "ResourceNotFoundException" || apiErr.Message != "Requested resource not found" {
		t.Errorf("DeleteItem error = %v, want ResourceNotFoundException from the header", err)
	}
	// No peer at all is not an APIError.
	_, err = JSONCall(ctx, peers.None(), "dynamodb", "DynamoDB_20120810.GetItem", "application/x-amz-json-1.0", "t", []byte(`{}`))
	if err == nil || errors.As(err, &apiErr) {
		t.Errorf("missing peer should be a plain error, got %v", err)
	}
}

func TestQueryCallLiftsResultsAndErrors(t *testing.T) {
	dir := genericPeer(t, func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.Form.Get("TopicArn") == "bad" {
			w.WriteHeader(404)
			w.Write([]byte(`<ErrorResponse><Error><Type>Sender</Type><Code>NotFound</Code><Message>Topic does not exist</Message></Error></ErrorResponse>`))
			return
		}
		w.Write([]byte(`<PublishResponse><PublishResult><MessageId>m-1</MessageId></PublishResult></PublishResponse>`))
	})
	ctx := context.Background()
	out, err := QueryCall(ctx, dir, "sns", "Publish", "t", url.Values{"Action": {"Publish"}, "TopicArn": {"good"}})
	if err != nil || out["MessageId"] != "m-1" {
		t.Fatalf("Publish = %v, %v", out, err)
	}
	_, err = QueryCall(ctx, dir, "sns", "Publish", "t", url.Values{"Action": {"Publish"}, "TopicArn": {"bad"}})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "NotFound" || apiErr.Message != "Topic does not exist" {
		t.Errorf("error = %v, want NotFound", err)
	}
}

func TestS3CallPathsAndErrors(t *testing.T) {
	var gotPath string
	dir := genericPeer(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.RequestURI()
		switch r.Method {
		case "HEAD":
			w.WriteHeader(404) // a HEAD carries no body
		case "GET":
			w.WriteHeader(404)
			w.Write([]byte(`<Error><Code>NoSuchKey</Code><Message>The specified key does not exist.</Message></Error>`))
		default:
			w.Header().Set("ETag", `"e"`)
		}
	})
	ctx := context.Background()
	resp, err := S3Call(ctx, dir, "PutObject", "PUT", "b", "dir/k.txt", nil, map[string]string{"Content-Type": "text/plain"}, []byte("x"))
	if err != nil || resp.Header.Get("ETag") != `"e"` || gotPath != "/b/dir/k.txt" {
		t.Fatalf("put: %v %v path %q", resp, err, gotPath)
	}
	_, err = S3Call(ctx, dir, "ListObjectsV2", "GET", "b", "", url.Values{"list-type": {"2"}, "prefix": {"dir/"}}, nil, nil)
	if gotPath != "/b?list-type=2&prefix=dir%2F" {
		t.Errorf("list path = %q", gotPath)
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "NoSuchKey" {
		t.Errorf("GET error = %v, want NoSuchKey", err)
	}
	_, err = S3Call(ctx, dir, "HeadObject", "HEAD", "b", "k", nil, nil, nil)
	if !errors.As(err, &apiErr) || apiErr.Code != "NotFound" {
		t.Errorf("HEAD 404 = %v, want NotFound", err)
	}
}

// A failed hop carries what its target answered: the status, and the
// X-Doze-Denied fact when a policy decided. Without them the wire showed every
// failed fan-out as a 500, so an alarm whose Publish was denied read as
// doze-aws breaking.
type recordingSink struct{ events []trace.Event }

func (r *recordingSink) ReserveCascade() trace.Cause { return trace.Cause(len(r.events) + 1) }
func (r *recordingSink) EmitCascade(e trace.Event)   { r.events = append(r.events, e) }

func TestAFailedPeerCallReachesTheTraceWithItsStatusAndWhoDeniedIt(t *testing.T) {
	var status int
	var denied string
	dir := genericPeer(t, func(w http.ResponseWriter, r *http.Request) {
		if denied != "" {
			w.Header().Set(awshttp.DeniedHeader, denied)
		}
		w.WriteHeader(status)
		w.Write([]byte(`{"__type":"AccessDenied","message":"no"}`))
	})
	for _, c := range []struct {
		name         string
		status       int
		denied       string
		wantDeniedBy string
	}{
		{"a policy denied it", 403, "iam", "iam"},
		{"the target broke", 503, "", ""},
		{"refused for its input", 400, "", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			status, denied = c.status, c.denied
			sink := &recordingSink{}
			ctx := trace.With(context.Background(), sink, trace.Cause(1))
			// The three shapes of non-2xx answer a peer call turns into an error.
			_, _ = JSONCall(ctx, dir, "dynamodb", "DynamoDB_20120810.GetItem", "application/x-amz-json-1.0", "t", []byte(`{}`))
			_ = SNSPublish(ctx, dir, "arn:aws:sns:us-east-1:000000000000:t", "m")

			if len(sink.events) < 2 {
				t.Fatalf("recorded %d events", len(sink.events))
			}
			for _, e := range sink.events {
				if e.Err == "" {
					t.Errorf("%s/%s: no error recorded", e.Service, e.Action)
				}
				if e.Status != c.status {
					t.Errorf("%s/%s: status %d, want %d", e.Service, e.Action, e.Status, c.status)
				}
				if e.Denied != c.wantDeniedBy {
					t.Errorf("%s/%s: denied %q, want %q", e.Service, e.Action, e.Denied, c.wantDeniedBy)
				}
			}
		})
	}
}
