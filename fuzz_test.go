package dozeaws_test

// Fuzzing at the layer nothing fuzzed: the gateway, and the stack behind it.
//
// There were thirteen fuzz targets before these two and every one of them was a
// PARSER — a CBOR decoder, an expression grammar, a signature header, a key
// encoder. Those are the right things to fuzz and they are all one layer down.
// Nothing fed a request to the front door and asked whether the whole assembly
// held, so the routing table, the protocol dispatch, the host parsing and every
// service handler behind them had only ever seen input somebody wrote on
// purpose.
//
// Both targets here assert the same two things, which are the only two that can
// be asserted without knowing what the input was supposed to mean:
//
//	it must not panic        — the target body crashing is the fuzzer's verdict
//	it must not answer 5xx   — a server fault is a bug by definition in this
//	                           tree; that is the premise Stack.Faults() and
//	                           dozetest.NoFaults are already built on
//
// Anything else — a 400, a 404, an XML error where JSON was expected — is a
// legitimate answer to nonsense and is not asserted about.

import (
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"

	dozeaws "github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/dozetest"
)

// fuzzWorld is a full stack for the targets here to send requests at.
//
// # Its lifetime depends on what the binary is doing, and it has to
//
// While FUZZING, the target body runs millions of times. Booting seventeen
// services costs about seventy-five milliseconds, so a stack per iteration
// would cap the target at thirteen executions a second and it would never find
// anything; shared, the gateway target measures about fifty thousand. So it is
// shared, and comes down through dozetest.AtExit before the goroutine check —
// which is the entire reason AtExit exists.
//
// While NOT fuzzing — an ordinary `go test`, which is every run of the suite —
// there are about twenty seed inputs and sharing buys nothing. It costs,
// though, and the bill was much larger than it looked. A shared stack is booted
// by the first seed and then sits there for the rest of the package, seventeen
// services' worth of tickers and janitors competing with every other test under
// the race detector: the root package went from 150 seconds to 232, while the
// seed corpora themselves accounted for 1.9 of that. Eighty seconds of
// interference from a fixture busy for two.
//
// So an ordinary run boots one per seed and closes it with t.Cleanup. Twenty-two
// boots cost about seven seconds under race and leave nothing running behind
// them, which puts the package at 163 — thirteen seconds for these two targets
// rather than eighty-two.
//
// # What sharing costs when it IS shared
//
// STATE ACCUMULATES ACROSS ITERATIONS. A queue one input created is still there
// for the next, so a crasher the fuzzer reports may not reproduce from its own
// input alone against a fresh stack — verified, not assumed: an eight-byte
// crasher this file's sabotage check produced passed when re-run on its own.
// That is a real drawback for debugging and none for detection, and it is why
// FuzzOperationSequence prints the decoded sequence on its way down.
type fuzzWorld struct {
	handler http.Handler
	stack   *dozeaws.Stack
	watcher *dozetest.PanicWatcher
	dir     string
}

func bootFuzzWorld() *fuzzWorld {
	dir, err := os.MkdirTemp("", "doze-fuzz-")
	if err != nil {
		panic(err)
	}
	w := dozetest.Watcher()
	st, err := dozeaws.NewStack(dozeaws.StackConfig{DataDir: dir, Logf: w.Logf()})
	if err != nil {
		panic(fmt.Sprintf("booting the fuzz stack: %v", err))
	}
	return &fuzzWorld{handler: st.Handler(), stack: st, watcher: w, dir: dir}
}

func (w *fuzzWorld) close() {
	w.stack.Close()
	os.RemoveAll(w.dir) //nolint:errcheck // a temp dir
}

var sharedFuzzWorld = sync.OnceValue(func() *fuzzWorld {
	w := bootFuzzWorld()
	dozetest.AtExit(w.close)
	return w
})

// stackFor hands back the stack this iteration should use, shared or its own,
// per the reasoning on fuzzWorld.
func stackFor(t *testing.T) *fuzzWorld {
	if fuzzing() {
		return sharedFuzzWorld()
	}
	w := bootFuzzWorld()
	t.Cleanup(w.close)
	return w
}

// fuzzing reports whether this binary was invoked with -fuzz.
//
// There is no exported way to ask, and the alternative — sharing always, or
// never — costs either eighty seconds of every suite run or every fuzz target's
// usefulness. The flag is registered by testing.Init before any test runs, and
// an absent one simply means "not fuzzing", which is the safe reading.
func fuzzing() bool {
	f := flag.Lookup("test.fuzz")
	return f != nil && f.Value.String() != ""
}

// FuzzGatewayRequest sends arbitrary HTTP at the front door.
//
// The six arguments are the fields the gateway actually dispatches on — it
// routes by host, by path, by X-Amz-Target and by the service in the
// Authorization credential scope — so giving the fuzzer those directly reaches
// the routing logic far faster than mutating a serialised request would. The
// body is separate because protocol dispatch reads it.
func FuzzGatewayRequest(f *testing.F) {
	// Seeds are real requests, one per protocol shape the gateway knows, so
	// the fuzzer starts from inputs that reach a handler rather than from ones
	// that bounce off the router.
	const auth = "AWS4-HMAC-SHA256 Credential=test/20260101/us-east-1/%s/aws4_request, " +
		"SignedHeaders=host;x-amz-date, Signature=00"
	for _, s := range []struct {
		method, path, host, target, service, body string
	}{
		// JSON, dispatched on X-Amz-Target.
		{"POST", "/", "localhost", "AmazonSQS.CreateQueue", "sqs", `{"QueueName":"seed"}`},
		{"POST", "/", "localhost", "DynamoDB_20120810.ListTables", "dynamodb", `{}`},
		{"POST", "/", "localhost", "TrentService.ListKeys", "kms", `{}`},
		// Query, dispatched on the credential scope and an Action in the form.
		{"POST", "/", "localhost", "", "sts", "Action=GetCallerIdentity&Version=2011-06-15"},
		{"POST", "/", "localhost", "", "iam", "Action=ListRoles&Version=2010-05-08"},
		// REST, dispatched on path — and on host, for S3's virtual-hosted form.
		{"GET", "/", "localhost", "", "s3", ""},
		{"PUT", "/seed-bucket", "localhost", "", "s3", ""},
		{"GET", "/", "seed-bucket.s3.localhost", "", "s3", ""},
		{"GET", "/2015-03-31/functions/", "localhost", "", "lambda", ""},
		{"GET", "/restapis", "localhost", "", "apigateway", ""},
		// Shapes with no service at all, which must be refused rather than
		// routed somewhere by accident.
		{"GET", "/", "localhost", "", "", ""},
		{"POST", "/", "localhost", "Nonsense.Operation", "", "{}"},
	} {
		a := ""
		if s.service != "" {
			a = fmt.Sprintf(auth, s.service)
		}
		f.Add(s.method, s.path, s.host, s.target, a, s.body)
	}

	f.Fuzz(func(t *testing.T, method, path, host, target, auth, body string) {
		w := stackFor(t)
		code := w.send(method, path, host, target, auth, body)
		if code >= 500 {
			t.Fatalf("%s %s (host %q, target %q) answered %d — "+
				"a server fault is a bug however malformed the request",
				method, path, host, target, code)
		}
		w.watcher.Check(t)
	})
}

// send builds the request by hand rather than through http.NewRequest, which
// validates the method and the URL and would reject exactly the inputs worth
// trying. An unparseable path becomes a URL with that path verbatim, so nothing
// is skipped for being malformed — being malformed is the point.
func (w *fuzzWorld) send(method, path, host, target, auth, body string) int {
	u, err := url.ParseRequestURI(path)
	if err != nil {
		u = &url.URL{Path: path}
	}
	r := &http.Request{
		Method:        method,
		URL:           u,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        http.Header{},
		Host:          host,
		RequestURI:    path,
		Body:          io.NopCloser(strings.NewReader(body)),
		ContentLength: int64(len(body)),
	}
	if target != "" {
		r.Header.Set("X-Amz-Target", target)
		r.Header.Set("Content-Type", "application/x-amz-json-1.0")
	} else if body != "" {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if auth != "" {
		r.Header.Set("Authorization", auth)
	}
	rec := httptest.NewRecorder()
	w.handler.ServeHTTP(rec, r)
	return rec.Code
}

// ---- generated operation sequences ----

// seqOps are well-formed operations, in contrast to the target above. This one
// is not about parsing at all: every request it sends is valid, and what varies
// is the ORDER and which of a handful of names each one names.
//
// That is a different search from the simulation's. rapid samples orderings
// uniformly; the fuzzer is coverage-guided, so it keeps the orderings that
// reached somewhere new and mutates those. Getting an item from a table that a
// previous step deleted, sending to a queue created twice, deleting a bucket
// with an object still in it — those live in orderings, and a uniform sampler
// finds them only by luck.
var seqOps = []struct {
	target, body string
}{
	{"AmazonSQS.CreateQueue", `{"QueueName":"%s"}`},
	{"AmazonSQS.GetQueueUrl", `{"QueueName":"%s"}`},
	{"AmazonSQS.SendMessage", `{"QueueUrl":"` + queueURL + `","MessageBody":"m"}`},
	{"AmazonSQS.ReceiveMessage", `{"QueueUrl":"` + queueURL + `","MaxNumberOfMessages":1}`},
	{"AmazonSQS.PurgeQueue", `{"QueueUrl":"` + queueURL + `"}`},
	{"AmazonSQS.GetQueueAttributes", `{"QueueUrl":"` + queueURL + `","AttributeNames":["All"]}`},
	{"AmazonSQS.DeleteQueue", `{"QueueUrl":"` + queueURL + `"}`},
	{"AmazonSQS.ListQueues", `{}`},

	{"DynamoDB_20120810.CreateTable", `{"TableName":"%s",` +
		`"AttributeDefinitions":[{"AttributeName":"pk","AttributeType":"S"}],` +
		`"KeySchema":[{"AttributeName":"pk","KeyType":"HASH"}],` +
		`"BillingMode":"PAY_PER_REQUEST"}`},
	{"DynamoDB_20120810.PutItem", `{"TableName":"%s","Item":{"pk":{"S":"k"}}}`},
	{"DynamoDB_20120810.GetItem", `{"TableName":"%s","Key":{"pk":{"S":"k"}}}`},
	{"DynamoDB_20120810.DeleteItem", `{"TableName":"%s","Key":{"pk":{"S":"k"}}}`},
	{"DynamoDB_20120810.Scan", `{"TableName":"%s"}`},
	{"DynamoDB_20120810.DescribeTable", `{"TableName":"%s"}`},
	{"DynamoDB_20120810.DeleteTable", `{"TableName":"%s"}`},
	{"DynamoDB_20120810.ListTables", `{}`},

	{"AmazonSSM.PutParameter", `{"Name":"/f/%s","Value":"v","Type":"String","Overwrite":true}`},
	{"AmazonSSM.GetParameter", `{"Name":"/f/%s"}`},
	{"AmazonSSM.DeleteParameter", `{"Name":"/f/%s"}`},

	{"secretsmanager.CreateSecret", `{"Name":"%s","SecretString":"s"}`},
	{"secretsmanager.GetSecretValue", `{"SecretId":"%s"}`},
	{"secretsmanager.DeleteSecret", `{"SecretId":"%s","ForceDeleteWithoutRecovery":true}`},
}

const queueURL = "http://localhost/" + awsident.AccountID + "/%s"

// seqNames is short so that names collide constantly. A fresh name per step
// would mean no step ever met a resource another step had already touched,
// which is where the orderings that matter are.
var seqNames = []string{"a", "b", "c", "d"}

// step packs an operation and a name into the one byte the decoder reads back.
// Four names, so two bits; the operation takes the rest.
func step(op, name int) byte { return byte(op<<2 | name&0x3) }

// describe renders a sequence as the operations it performs.
//
// It matters more here than it would elsewhere because of the shared stack: a
// corpus entry is a sequence of opaque bytes, and one that fails during a fuzz
// run may not fail on its own afterwards, since the state an earlier iteration
// left behind is part of what provoked it. Verified, not assumed — a crasher
// this target found and minimised to eight bytes passed when re-run alone.
// When that happens the decoded sequence is the evidence that remains, so it
// should be legible.
func describe(steps []byte) string {
	if len(steps) == 0 {
		return "the empty sequence"
	}
	parts := make([]string, 0, len(steps))
	for _, b := range steps {
		op := seqOps[int(b>>2)%len(seqOps)]
		name := seqNames[int(b&0x3)%len(seqNames)]
		parts = append(parts, fmt.Sprintf("%s(%s)", op.target, name))
	}
	return "[" + strings.Join(parts, " ") + "]"
}

// FuzzOperationSequence walks the stack through a generated sequence of valid
// operations.
//
// Each byte of the input is one step: the high bits choose the operation, the
// low bits choose which name it acts on. A byte-per-step keeps the mapping from
// input to behaviour direct enough that the fuzzer's minimiser produces a
// sequence a person can read.
func FuzzOperationSequence(f *testing.F) {
	// Seeds are written as (operation, name) pairs and encoded, rather than as
	// the raw bytes — the encoding is an implementation detail of the step
	// decoder and a seed list written in terms of it goes stale silently the
	// moment an operation is inserted in the middle.
	seq := func(pairs ...[2]int) []byte {
		out := make([]byte, 0, len(pairs))
		for _, p := range pairs {
			out = append(out, step(p[0], p[1]))
		}
		return out
	}
	const (
		qCreate, qURL, qSend, qRecv = 0, 1, 2, 3
		qPurge, qAttrs, qDelete     = 4, 5, 6
		tCreate, tPut, tGet         = 8, 9, 10
		tDelItem, tScan, tDesc      = 11, 12, 13
		tDrop                       = 14
		pPut, pGet, pDel            = 16, 17, 18
		sCreate, sGet, sDel         = 19, 20, 21
	)
	f.Add(seq())                                                                               // the empty sequence must be fine too
	f.Add(seq([2]int{qCreate, 0}, [2]int{qSend, 0}, [2]int{qRecv, 0}, [2]int{qDelete, 0}))     // the happy path
	f.Add(seq([2]int{qCreate, 0}, [2]int{qCreate, 0}, [2]int{qDelete, 0}, [2]int{qDelete, 0})) // create and delete twice
	f.Add(seq([2]int{qDelete, 1}, [2]int{qSend, 1}, [2]int{qURL, 1}, [2]int{qAttrs, 1}))       // use a queue never created
	f.Add(seq([2]int{qCreate, 2}, [2]int{qSend, 2}, [2]int{qPurge, 2}, [2]int{qRecv, 2}))      // receive after a purge
	f.Add(seq([2]int{tCreate, 0}, [2]int{tPut, 0}, [2]int{tGet, 0}, [2]int{tDrop, 0}))         // the happy path
	f.Add(seq([2]int{tDrop, 1}, [2]int{tPut, 1}, [2]int{tCreate, 1}, [2]int{tGet, 1}))         // drop before create, then use
	f.Add(seq([2]int{tCreate, 2}, [2]int{tPut, 2}, [2]int{tDrop, 2}, [2]int{tCreate, 2},
		[2]int{tScan, 2}, [2]int{tDesc, 2}, [2]int{tDelItem, 2})) // across a drop and recreate
	f.Add(seq([2]int{pPut, 0}, [2]int{pGet, 0}, [2]int{pDel, 0}, [2]int{pGet, 0}))    // a parameter, used after deletion
	f.Add(seq([2]int{sCreate, 0}, [2]int{sGet, 0}, [2]int{sDel, 0}, [2]int{sGet, 0})) // the same for a secret

	f.Fuzz(func(t *testing.T, steps []byte) {
		w := stackFor(t)
		// Every step here is a real request against bbolt, so length is paid
		// for in executions per second — measured at roughly a hundred and
		// fifty a second against the gateway target's fifty thousand. The cap
		// buys back exploration: the orderings that matter are short, and the
		// minimiser would shorten a long one anyway.
		if len(steps) > 32 {
			t.Skip("sequence longer than the interesting range")
		}
		// A panic takes the process down with a stack trace and nothing about
		// what led there, and the raw bytes in the corpus entry are not
		// readable. Printing the decoded sequence while the panic unwinds puts
		// the two together without recovering — recovering would cost the
		// stack trace, which is the more useful half.
		finished := false
		defer func() {
			if !finished {
				fmt.Fprintf(os.Stderr, "doze fuzz: the sequence was %s\n", describe(steps))
			}
		}()

		for i, b := range steps {
			op := seqOps[int(b>>2)%len(seqOps)]
			name := seqNames[int(b&0x3)%len(seqNames)]
			body := op.body
			if strings.Contains(body, "%s") {
				body = fmt.Sprintf(body, name)
			}
			if code := w.send("POST", "/", "localhost", op.target, "", body); code >= 500 {
				t.Fatalf("step %d of %s answered %d; every request in this "+
					"sequence is well-formed", i, describe(steps), code)
			}
		}
		finished = true
		w.watcher.Check(t)
	})
}
