package dozeaws_test

// What the transport costs, and how much of it a different HTTP server could
// take back.
//
// # Why this exists
//
// request_bench_test.go drives handlers directly and says so: no loopback TCP,
// no SDK client, because those are the parts doze-aws does not control. That is
// right for measuring a handler and wrong for answering "would fasthttp or
// Hertz help", which is asked about every Go service eventually and cannot be
// answered from numbers that exclude the transport. This file is the companion
// that puts the socket back in, so the question has an answer with numbers
// under it rather than an opinion.
//
// # The floor is the point
//
// BenchmarkLoopbackFloor measures net/http answering "ok". Without it the
// loopback figure is uninterpretable: 44 µs looks worth optimising until you
// see that 35 µs of it is what a no-op round trip costs, and that roughly half
// of THAT is the client — which stays net/http regardless, because it is the
// developer's AWS SDK. Whatever a different server framework could win has to
// come out of the server's share of the floor, not out of the 44.
//
// # Two operations, chosen for opposite reasons
//
// STS GetCallerIdentity is the cheapest thing in the tree — stateless, no
// store, no fsync — so the transport's share of it is as LARGE as it can
// possibly be. It is the case most favourable to replacing the server.
//
// SQS SendMessage is the opposite and the common one: a durable write, and
// bbolt fsyncs on commit. Both are measured over the same socket so the
// comparison is arithmetic nobody has to trust.

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// awsRequest builds a request an http.Client can actually send, which
// httptest.NewRequest deliberately does not. Same shape as post()'s: the
// gateway routes on the credential scope, and the signature is not verified.
func awsRequest(url, target, contentType, body, scope string) *http.Request {
	r, err := http.NewRequest(http.MethodPost, url, bytes.NewReader([]byte(body)))
	if err != nil {
		panic(err)
	}
	r.Header.Set("Content-Type", contentType)
	if target != "" {
		r.Header.Set("X-Amz-Target", target)
	}
	r.Header.Set("Authorization",
		"AWS4-HMAC-SHA256 Credential=test/20200101/us-east-1/"+scope+
			"/aws4_request, SignedHeaders=host, Signature=x")
	return r
}

const (
	stsTarget = "" // STS has no X-Amz-Target; it routes on the scope alone
	stsType   = "application/x-www-form-urlencoded"
	stsBody   = "Action=GetCallerIdentity&Version=2011-06-15"
)

// benchClient keeps connections alive the way an SDK client does. Closing idle
// connections on cleanup matters: this package verifies goroutine leaks at
// exit, and a parked keep-alive reader is a live goroutine.
func benchClient(b *testing.B) *http.Client {
	b.Helper()
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.MaxIdleConnsPerHost = 64
	c := &http.Client{Transport: tr}
	b.Cleanup(c.CloseIdleConnections)
	return c
}

// serve puts a handler behind a real socket.
func serve(b *testing.B, h http.Handler) string {
	b.Helper()
	ts := httptest.NewServer(h)
	b.Cleanup(ts.Close)
	return ts.URL
}

func send(b *testing.B, c *http.Client, r *http.Request) {
	b.Helper()
	resp, err := c.Do(r)
	if err != nil {
		b.Error(err)
		return
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		b.Error(err)
		return
	}
	if resp.StatusCode != http.StatusOK {
		b.Errorf("status %d: %s", resp.StatusCode, body)
	}
}

// BenchmarkRequestGetCallerIdentity is the cheapest whole request doze-aws can
// answer, driven the way request_bench_test.go drives its own: no socket. This
// is doze-aws's share — fault recording, IAM header stripping, gateway routing,
// the handler, the encode.
func BenchmarkRequestGetCallerIdentity(b *testing.B) {
	h := benchStack(b, "sts")
	b.ReportAllocs()
	for b.Loop() {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader([]byte(stsBody)))
		r.Header.Set("Content-Type", stsType)
		r.Header.Set("Authorization",
			"AWS4-HMAC-SHA256 Credential=test/20200101/us-east-1/sts"+
				"/aws4_request, SignedHeaders=host, Signature=x")
		h.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			b.Fatalf("%d %s", w.Code, w.Body.String())
		}
	}
}

// BenchmarkRequestGetCallerIdentityOverLoopback is the same request through a
// socket. The difference from the benchmark above is the whole transport, both
// halves of it.
func BenchmarkRequestGetCallerIdentityOverLoopback(b *testing.B) {
	url := serve(b, benchStack(b, "sts"))
	c := benchClient(b)
	b.ReportAllocs()
	for b.Loop() {
		send(b, c, awsRequest(url, stsTarget, stsType, stsBody, "sts"))
	}
}

// BenchmarkRequestSendMessageOverLoopback pairs with
// BenchmarkRequestSendMessage next door, which sends the identical body with
// no socket. The gap between them is what the transport adds to a request that
// has to reach the disk — and it is the ratio that decides whether the HTTP
// layer is worth touching.
func BenchmarkRequestSendMessageOverLoopback(b *testing.B) {
	h := benchStack(b, "sqs")
	url := serve(b, h)
	c := benchClient(b)
	const jsonType = "application/x-amz-json-1.0"
	send(b, c, awsRequest(url, "AmazonSQS.CreateQueue", jsonType, `{"QueueName":"bench"}`, "sqs"))

	body := `{"QueueUrl":"http://127.0.0.1/000000000000/bench","MessageBody":"hello"}`
	b.ReportAllocs()
	for b.Loop() {
		send(b, c, awsRequest(url, "AmazonSQS.SendMessage", jsonType, body, "sqs"))
	}
}

// BenchmarkLoopbackFloor is the control: net/http answering "ok", with none of
// doze-aws in it. Read the two loopback benchmarks against this, not against
// zero.
func BenchmarkLoopbackFloor(b *testing.B) {
	url := serve(b, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.Write([]byte("ok"))
	}))
	c := benchClient(b)
	b.ReportAllocs()
	for b.Loop() {
		send(b, c, awsRequest(url, stsTarget, stsType, stsBody, "sts"))
	}
}

// BenchmarkRequestGetCallerIdentityParallel is throughput, which is the claim
// fasthttp and Hertz actually make. Here so the answer cites the number they
// compete on rather than a single-threaded one.
func BenchmarkRequestGetCallerIdentityParallel(b *testing.B) {
	url := serve(b, benchStack(b, "sts"))
	c := benchClient(b)
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			send(b, c, awsRequest(url, stsTarget, stsType, stsBody, "sts"))
		}
	})
}
