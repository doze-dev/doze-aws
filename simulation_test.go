package dozeaws_test

// A generated sequence of operations, checked against a model of what should
// exist, with the stack restarted and its siblings broken underneath it.
//
// # Why generated rather than written
//
// The audit that prompted all of this found thirteen bugs, and nine of them
// only appear after a stack has been running or while it is shutting down. A
// hand-written test is one sequence somebody thought of; those nine were in
// sequences nobody thought of. A generator plus an invariant is unbounded
// coverage for bounded effort, which is the only shape that works when one
// person maintains seventeen services.
//
// # The model
//
// Deliberately thin: which resources should exist, and what should be readable.
// It is NOT a second implementation of DynamoDB — a model that models
// everything is a second system to get wrong, and the bugs being hunted here
// are lifecycle and persistence, not query semantics. "I created it, so listing
// must show it, and restarting must not lose it" is enough to catch those and
// cheap enough to be obviously correct.
//
// # Replay
//
// rapid prints the seed of a failing run and shrinks the sequence before
// reporting it, so a failure arrives as the three steps that matter rather than
// the two hundred that happened. Reproduce with:
//
//	go test -run TestSimulation -rapid.seed=<seed> -rapid.steps=<n> .
//
// That is the property that makes randomised testing usable by one person
// rather than maddening.

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsddb "github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	"pgregory.net/rapid"

	dozeaws "github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/dozetest"
	"github.com/doze-dev/doze-aws/peers"
)

// simServices is the set under simulation. Three rather than seventeen because
// a stack is booted per generated sequence and the boot dominates the run; these
// three cover the three storage shapes (a queue, a table, an object store) and
// so the three ways persistence can go wrong.
var simServices = []string{"sqs", "dynamodb", "s3"}

// Names come from a small fixed pool on purpose. Unique names would mean every
// operation touches a fresh resource and the interesting cases — create over a
// delete, send to a queue another step just removed, put to a bucket twice —
// would never be generated.
var (
	simQueues  = []string{"alpha", "beta", "gamma"}
	simTables  = []string{"items", "orders"}
	simBuckets = []string{"one", "two"}
	simKeys    = []string{"a", "b", "c"}
)

// world is one generated run: a stack, its clients, and the model.
type world struct {
	t      *testing.T
	dir    string
	cfg    aws.Config
	faulty bool // cross-service faults injected for this run

	stack *dozeaws.Stack
	ts    *httptest.Server
	sqs   *awssqs.Client
	ddb   *awsddb.Client
	s3    *awss3.Client

	// The model: what should exist. A value of false means "deleted", which is
	// a different claim from "never created" and is worth keeping separate —
	// the bug being looked for is a delete that does not take.
	queues  map[string]bool
	tables  map[string]bool
	buckets map[string]bool
	objects map[string]string // "bucket/key" -> body
	items   map[string]string // "table/pk" -> pk
}

func newWorld(t *testing.T, rt *rapid.T) *world {
	t.Helper()
	dir, err := os.MkdirTemp("", "doze-sim-")
	if err != nil {
		t.Fatal(err)
	}
	w := &world{
		t: t, dir: dir,
		// Faults on roughly half the runs. The operations driven here are
		// direct rather than cascades, so this is asserting something specific:
		// a sibling being down must not disturb the request path or make the
		// stack answer a 5xx of its own.
		faulty:  rapid.Bool().Draw(rt, "faults"),
		queues:  map[string]bool{},
		tables:  map[string]bool{},
		buckets: map[string]bool{},
		objects: map[string]string{},
		items:   map[string]string{},
	}
	w.boot()
	return w
}

func (w *world) boot() {
	w.t.Helper()
	cfg := dozeaws.StackConfig{
		DataDir:  w.dir,
		Services: simServices,
		// Quiet rather than a discard: the generated sequence is long and its
		// log would bury the failure, but the panic watch still has to be armed
		// — a contained background panic is exactly the class being hunted.
		Logf: dozetest.Quiet(w.t),
	}
	if w.faulty {
		cfg.Peers = func(d peers.Directory) peers.Directory {
			return peers.WithFaults(d, func(string, *http.Request) peers.Fault {
				return peers.Fault{Status: 500}
			})
		}
	}
	st, err := dozeaws.NewStack(cfg)
	if err != nil {
		w.t.Fatalf("booting the stack: %v", err)
	}
	w.stack = st
	w.ts = httptest.NewServer(st.Handler())
	w.cfg = aws.Config{
		Region: awsident.Region,
		Credentials: credentials.NewStaticCredentialsProvider(
			awsident.AccessKeyID, awsident.SecretAccessKey, ""),
	}
	ep := aws.String(w.ts.URL)
	w.sqs = awssqs.NewFromConfig(w.cfg, func(o *awssqs.Options) { o.BaseEndpoint = ep })
	w.ddb = awsddb.NewFromConfig(w.cfg, func(o *awsddb.Options) { o.BaseEndpoint = ep })
	w.s3 = awss3.NewFromConfig(w.cfg, func(o *awss3.Options) { o.BaseEndpoint = ep; o.UsePathStyle = true })
}

func (w *world) close() {
	if w.ts != nil {
		w.ts.Close()
	}
	if w.stack != nil {
		w.stack.Close()
	}
	os.RemoveAll(w.dir) //nolint:errcheck // a temp dir
}

func (w *world) ctx() context.Context { return context.Background() }

// ---- actions ----

func (w *world) createQueue(rt *rapid.T) {
	name := rapid.SampledFrom(simQueues).Draw(rt, "queue")
	if _, err := w.sqs.CreateQueue(w.ctx(), &awssqs.CreateQueueInput{
		QueueName: aws.String(name)}); err != nil {
		rt.Fatalf("CreateQueue(%s): %v", name, err)
	}
	w.queues[name] = true
}

func (w *world) deleteQueue(rt *rapid.T) {
	name := rapid.SampledFrom(simQueues).Draw(rt, "queue")
	if !w.queues[name] {
		rt.Skip("not created")
	}
	url, err := w.sqs.GetQueueUrl(w.ctx(), &awssqs.GetQueueUrlInput{QueueName: aws.String(name)})
	if err != nil {
		rt.Fatalf("GetQueueUrl(%s) for a queue the model says exists: %v", name, err)
	}
	if _, err := w.sqs.DeleteQueue(w.ctx(), &awssqs.DeleteQueueInput{QueueUrl: url.QueueUrl}); err != nil {
		rt.Fatalf("DeleteQueue(%s): %v", name, err)
	}
	w.queues[name] = false
}

func (w *world) sendMessage(rt *rapid.T) {
	name := rapid.SampledFrom(simQueues).Draw(rt, "queue")
	if !w.queues[name] {
		rt.Skip("not created")
	}
	url, err := w.sqs.GetQueueUrl(w.ctx(), &awssqs.GetQueueUrlInput{QueueName: aws.String(name)})
	if err != nil {
		rt.Fatalf("GetQueueUrl(%s): %v", name, err)
	}
	if _, err := w.sqs.SendMessage(w.ctx(), &awssqs.SendMessageInput{
		QueueUrl: url.QueueUrl, MessageBody: aws.String("m")}); err != nil {
		rt.Fatalf("SendMessage(%s): %v", name, err)
	}
}

func (w *world) createTable(rt *rapid.T) {
	name := rapid.SampledFrom(simTables).Draw(rt, "table")
	_, err := w.ddb.CreateTable(w.ctx(), &awsddb.CreateTableInput{
		TableName:            aws.String(name),
		AttributeDefinitions: []ddbtypes.AttributeDefinition{{AttributeName: aws.String("pk"), AttributeType: ddbtypes.ScalarAttributeTypeS}},
		KeySchema:            []ddbtypes.KeySchemaElement{{AttributeName: aws.String("pk"), KeyType: ddbtypes.KeyTypeHash}},
	})
	if err != nil {
		if w.tables[name] {
			return // re-creating an existing table is refused, as AWS does
		}
		rt.Fatalf("CreateTable(%s): %v", name, err)
	}
	w.tables[name] = true
}

func (w *world) putItem(rt *rapid.T) {
	table := rapid.SampledFrom(simTables).Draw(rt, "table")
	pk := rapid.SampledFrom(simKeys).Draw(rt, "pk")
	if !w.tables[table] {
		rt.Skip("no table")
	}
	if _, err := w.ddb.PutItem(w.ctx(), &awsddb.PutItemInput{
		TableName: aws.String(table),
		Item:      map[string]ddbtypes.AttributeValue{"pk": &ddbtypes.AttributeValueMemberS{Value: pk}},
	}); err != nil {
		rt.Fatalf("PutItem(%s/%s): %v", table, pk, err)
	}
	w.items[table+"/"+pk] = pk
}

func (w *world) createBucket(rt *rapid.T) {
	name := rapid.SampledFrom(simBuckets).Draw(rt, "bucket")
	_, err := w.s3.CreateBucket(w.ctx(), &awss3.CreateBucketInput{Bucket: aws.String(name)})
	if err != nil && !w.buckets[name] {
		rt.Fatalf("CreateBucket(%s): %v", name, err)
	}
	w.buckets[name] = true
}

func (w *world) putObject(rt *rapid.T) {
	bucket := rapid.SampledFrom(simBuckets).Draw(rt, "bucket")
	key := rapid.SampledFrom(simKeys).Draw(rt, "key")
	if !w.buckets[bucket] {
		rt.Skip("no bucket")
	}
	body := fmt.Sprintf("%s/%s", bucket, key)
	if _, err := w.s3.PutObject(w.ctx(), &awss3.PutObjectInput{
		Bucket: aws.String(bucket), Key: aws.String(key),
		Body: strings.NewReader(body)}); err != nil {
		rt.Fatalf("PutObject(%s/%s): %v", bucket, key, err)
	}
	w.objects[bucket+"/"+key] = body
}

func (w *world) deleteObject(rt *rapid.T) {
	bucket := rapid.SampledFrom(simBuckets).Draw(rt, "bucket")
	key := rapid.SampledFrom(simKeys).Draw(rt, "key")
	if _, ok := w.objects[bucket+"/"+key]; !ok {
		rt.Skip("no object")
	}
	if _, err := w.s3.DeleteObject(w.ctx(), &awss3.DeleteObjectInput{
		Bucket: aws.String(bucket), Key: aws.String(key)}); err != nil {
		rt.Fatalf("DeleteObject(%s/%s): %v", bucket, key, err)
	}
	delete(w.objects, bucket+"/"+key)
}

// restart is the action that makes this a persistence test rather than a
// request test: close everything and reopen over the same directory. The
// invariant that runs next has to find the world exactly as it was.
func (w *world) restart(rt *rapid.T) {
	w.ts.Close()
	if err := w.stack.Close(); err != nil {
		rt.Fatalf("closing the stack: %v", err)
	}
	w.boot()
}

// ---- invariants ----

// invariants run before and after every action. Everything asserted here must
// hold at every point in any sequence, which is what makes them worth checking
// automatically rather than at a place a person chose.
func (w *world) invariants(rt *rapid.T) {
	// No server fault, ever. A 5xx during a sequence of valid operations is a
	// bug by definition, and this is the assertion that costs nothing and
	// covers everything the generator can reach.
	if f := w.stack.Faults(); len(f) != 0 {
		rt.Fatalf("the stack answered %d server fault(s): %+v", len(f), f)
	}

	// What was created is listed, and what was deleted is not.
	gotQ, err := w.sqs.ListQueues(w.ctx(), &awssqs.ListQueuesInput{})
	if err != nil {
		rt.Fatalf("ListQueues: %v", err)
	}
	assertSet(rt, "queues", w.expected(w.queues), queueNames(gotQ.QueueUrls))

	gotT, err := w.ddb.ListTables(w.ctx(), &awsddb.ListTablesInput{})
	if err != nil {
		rt.Fatalf("ListTables: %v", err)
	}
	assertSet(rt, "tables", w.expected(w.tables), gotT.TableNames)

	gotB, err := w.s3.ListBuckets(w.ctx(), &awss3.ListBucketsInput{})
	if err != nil {
		rt.Fatalf("ListBuckets: %v", err)
	}
	var buckets []string
	for _, b := range gotB.Buckets {
		buckets = append(buckets, aws.ToString(b.Name))
	}
	assertSet(rt, "buckets", w.expected(w.buckets), buckets)

	// Everything written is readable, with the bytes it was written with.
	for path, want := range w.objects {
		bucket, key := splitTwo(path)
		out, err := w.s3.GetObject(w.ctx(), &awss3.GetObjectInput{
			Bucket: aws.String(bucket), Key: aws.String(key)})
		if err != nil {
			rt.Fatalf("GetObject(%s) for an object the model says exists: %v", path, err)
		}
		raw, rerr := io.ReadAll(out.Body)
		out.Body.Close()
		if rerr != nil {
			rt.Fatalf("reading %s: %v", path, rerr)
		}
		if got := string(raw); got != want {
			rt.Fatalf("GetObject(%s) = %q, want %q", path, got, want)
		}
	}
	for path, pk := range w.items {
		table, _ := splitTwo(path)
		if !w.tables[table] {
			continue // the table was dropped; its items went with it
		}
		out, err := w.ddb.GetItem(w.ctx(), &awsddb.GetItemInput{
			TableName: aws.String(table),
			Key:       map[string]ddbtypes.AttributeValue{"pk": &ddbtypes.AttributeValueMemberS{Value: pk}},
		})
		if err != nil {
			rt.Fatalf("GetItem(%s): %v", path, err)
		}
		if out.Item == nil {
			rt.Fatalf("GetItem(%s) found nothing for an item the model says exists", path)
		}
	}
}

// expected is the set of names the model says should exist.
func (w *world) expected(m map[string]bool) []string {
	var out []string
	for name, live := range m {
		if live {
			out = append(out, name)
		}
	}
	return out
}

// splitTwo splits a "bucket/key" or "table/pk" model path.
func splitTwo(s string) (string, string) {
	i := strings.Index(s, "/")
	return s[:i], s[i+1:]
}

// queueNames turns queue URLs back into names, which is what the model holds.
func queueNames(urls []string) []string {
	out := make([]string, 0, len(urls))
	for _, u := range urls {
		out = append(out, u[strings.LastIndex(u, "/")+1:])
	}
	return out
}

func assertSet(rt *rapid.T, what string, want, got []string) {
	sort.Strings(want)
	sort.Strings(got)
	if len(want) != len(got) {
		rt.Fatalf("%s: the model says %v, the stack says %v", what, want, got)
	}
	for i := range want {
		if want[i] != got[i] {
			rt.Fatalf("%s: the model says %v, the stack says %v", what, want, got)
		}
	}
}

func TestSimulation(t *testing.T) {
	if testing.Short() {
		t.Skip("boots a stack per generated sequence")
	}
	rapid.Check(t, func(rt *rapid.T) {
		w := newWorld(t, rt)
		defer w.close()
		rt.Repeat(map[string]func(*rapid.T){
			"":             w.invariants,
			"createQueue":  w.createQueue,
			"deleteQueue":  w.deleteQueue,
			"sendMessage":  w.sendMessage,
			"createTable":  w.createTable,
			"putItem":      w.putItem,
			"createBucket": w.createBucket,
			"putObject":    w.putObject,
			"deleteObject": w.deleteObject,
			"restart":      w.restart,
		})
		dozetest.NoFaults(t, w.stack)
	})
}
