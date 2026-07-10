package peercall

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/doze-dev/doze-aws/peers"
)

// recordingPeer returns a peers.Directory pointing sqs/lambda/sns at a test
// server that records the last request and answers each call type.
func recordingPeer(t *testing.T) (peers.Directory, *lastReq) {
	t.Helper()
	last := &lastReq{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		last.target = r.Header.Get("X-Amz-Target")
		last.path = r.URL.Path
		last.body = string(body)
		switch {
		case last.target == "AmazonSQS.ReceiveMessage":
			io.WriteString(w, `{"Messages":[{"Body":"hi","ReceiptHandle":"rh-1"}]}`)
		case strings.Contains(r.URL.Path, "/invocations"):
			w.Write(body) // echo the payload back (RequestResponse)
		case strings.Contains(last.body, "Action=Publish"):
			io.WriteString(w, `<PublishResponse><PublishResult><MessageId>m1</MessageId></PublishResult></PublishResponse>`)
		default:
			io.WriteString(w, `{}`)
		}
	}))
	t.Cleanup(srv.Close)
	ep := peers.Endpoint{Client: srv.Client(), BaseURL: srv.URL}
	return peers.Static{"sqs": ep, "lambda": ep, "sns": ep}, last
}

type lastReq struct{ target, path, body string }

func TestSQSSendReceiveDelete(t *testing.T) {
	dir, last := recordingPeer(t)
	if err := SQSSend(dir, "jobs", "payload", map[string]string{"k": "v"}); err != nil {
		t.Fatalf("SQSSend: %v", err)
	}
	if last.target != "AmazonSQS.SendMessage" || !strings.Contains(last.body, "payload") {
		t.Fatalf("send req = %+v", last)
	}
	msgs, err := SQSReceive(dir, "jobs", 1, 0)
	if err != nil || len(msgs) != 1 || msgs[0].Body != "hi" {
		t.Fatalf("SQSReceive = %+v err=%v", msgs, err)
	}
	if err := SQSDelete(dir, "jobs", msgs[0].ReceiptHandle); err != nil {
		t.Fatalf("SQSDelete: %v", err)
	}
	if last.target != "AmazonSQS.DeleteMessage" || !strings.Contains(last.body, "rh-1") {
		t.Fatalf("delete req = %+v", last)
	}
}

func TestLambdaInvokeSyncAndAsync(t *testing.T) {
	dir, last := recordingPeer(t)
	out, err := LambdaInvoke(dir, "fn", []byte(`{"x":1}`))
	if err != nil || string(out) != `{"x":1}` {
		t.Fatalf("LambdaInvoke = %q err=%v", out, err)
	}
	if !strings.Contains(last.path, "/functions/fn/invocations") {
		t.Fatalf("invoke path = %q", last.path)
	}
	if err := LambdaInvokeAsync(dir, "fn", []byte(`{}`)); err != nil {
		t.Fatalf("LambdaInvokeAsync: %v", err)
	}
}

func TestSNSPublish(t *testing.T) {
	dir, last := recordingPeer(t)
	if err := SNSPublish(dir, "arn:aws:sns:us-east-1:000000000000:topic", "hello"); err != nil {
		t.Fatalf("SNSPublish: %v", err)
	}
	if !strings.Contains(last.body, "Action=Publish") {
		t.Fatalf("publish body = %q", last.body)
	}
}

func TestNoPeerWiredErrors(t *testing.T) {
	none := peers.None()
	if err := SQSSend(none, "q", "b", nil); err == nil {
		t.Fatal("SQSSend with no peer should error")
	}
	if _, err := LambdaInvoke(none, "fn", nil); err == nil {
		t.Fatal("LambdaInvoke with no peer should error")
	}
	if err := SNSPublish(none, "arn", "m"); err == nil {
		t.Fatal("SNSPublish with no peer should error")
	}
}
