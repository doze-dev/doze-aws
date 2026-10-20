package console

// The console's three short-lived caches (the wiring graph, the rail's "a
// dead-letter queue holds a message", the wire's "Needs a look") are dropped by
// bustGraph when the console itself changes something, so the page the user
// lands on shows the change at once. Each is filled by a read that can take a
// while, and a read that STARTED before the mutation used to finish after the
// drop and store what it had read, with a fresh timestamp: the next page then
// showed the world as it was before the user's own action, for the whole TTL.
//
// This was found as a flaky browser spec (an event source mapping made through
// the console was missing from the queue's Configuration form for five
// seconds when another page's crawl was in flight). The tests below hold the
// ordering deterministically: the read is parked mid-flight, the mutation
// happens, the read is let go, and the next ask must see the mutation.

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/peers"
)

// bucketService answers ListBuckets with whatever the bucket list is when the
// request ARRIVES, and parks the first request (after reading, before
// answering) until released: a slow crawl that read the old state.
type bucketService struct {
	mu       sync.Mutex
	names    []string
	parked   bool
	entered  chan struct{}
	release  chan struct{}
	requests int
}

func newBucketService(names ...string) *bucketService {
	return &bucketService{names: names, entered: make(chan struct{}), release: make(chan struct{})}
}

func (s *bucketService) set(names ...string) {
	s.mu.Lock()
	s.names = names
	s.mu.Unlock()
}

func (s *bucketService) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.requests++
	seen := append([]string(nil), s.names...)
	park := !s.parked
	s.parked = true
	s.mu.Unlock()
	if park {
		close(s.entered)
		<-s.release
	}
	var b strings.Builder
	b.WriteString(`<ListAllMyBucketsResult><Buckets>`)
	for _, n := range seen {
		b.WriteString(`<Bucket><Name>`)
		xml.EscapeText(&b, []byte(n))
		b.WriteString(`</Name></Bucket>`)
	}
	b.WriteString(`</Buckets></ListAllMyBucketsResult>`)
	w.Header().Set("Content-Type", "application/xml")
	w.Write([]byte(b.String()))
}

func backendOver(s3 http.Handler) *backend {
	return newBackend(peers.InProcess(func(svc string) http.Handler {
		if svc == "s3" {
			return s3
		}
		return nil
	}), awsident.Default(), "")
}

func hasNode(g FlowGraph, id string) bool {
	for _, n := range g.Nodes {
		if n.ID == id {
			return true
		}
	}
	for _, n := range g.Unwired {
		if n.ID == id {
			return true
		}
	}
	return false
}

func TestAGraphCrawledBeforeAMutationIsNotKeptAfterIt(t *testing.T) {
	svc := newBucketService("before")
	b := backendOver(svc)
	ctx := context.Background()

	crawl := make(chan FlowGraph, 1)
	go func() { crawl <- b.graphCached(ctx) }()
	<-svc.entered // the crawl has read the world as it is now and is on its way back

	svc.set("before", "made-by-the-console")
	b.bustGraph() // the console's own mutation, a moment after the crawl read
	close(svc.release)

	old := <-crawl
	if hasNode(old, "s3:made-by-the-console") {
		t.Fatalf("the parked crawl cannot have seen the bucket made after it read: the test is not holding the order")
	}
	if g := b.graphCached(ctx); !hasNode(g, "s3:made-by-the-console") {
		t.Errorf("the page after the mutation shows the graph the crawl read before it (nodes %v): a stale crawl was cached over the drop", nodeIDs(g))
	}
}

func nodeIDs(g FlowGraph) []string {
	var out []string
	for _, n := range g.Nodes {
		out = append(out, n.ID)
	}
	return out
}

// A mutation with no crawl in flight still drops the cache, as it always did,
// and a crawl that no mutation overlapped is kept for its TTL (the cache is
// still a cache).
func TestAGraphNoMutationOverlappedIsStillCached(t *testing.T) {
	svc := newBucketService("one")
	close(svc.release) // never parked for long: the release is already given
	b := backendOver(svc)
	ctx := context.Background()
	b.graphCached(ctx)
	svc.mu.Lock()
	first := svc.requests
	svc.mu.Unlock()
	b.graphCached(ctx)
	svc.mu.Lock()
	second := svc.requests
	svc.mu.Unlock()
	if second != first {
		t.Errorf("a second ask inside the TTL crawled again (%d requests, then %d)", first, second)
	}
	b.bustGraph()
	b.graphCached(ctx)
	svc.mu.Lock()
	third := svc.requests
	svc.mu.Unlock()
	if third == second {
		t.Errorf("an ask after a mutation did not crawl again")
	}
}

// queueService is an SQS with one source queue dead-lettering into one
// dead-letter queue, whose depth is whatever it is when a request ARRIVES; the
// first ListQueues is parked after it has read and before it answers.
type queueService struct {
	mu      sync.Mutex
	held    int
	parked  bool
	entered chan struct{}
	release chan struct{}
}

func (s *queueService) setHeld(n int) {
	s.mu.Lock()
	s.held = n
	s.mu.Unlock()
}

func (s *queueService) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/x-amz-json-1.0")
	s.mu.Lock()
	held := s.held
	s.mu.Unlock()
	switch strings.TrimPrefix(r.Header.Get("X-Amz-Target"), "AmazonSQS.") {
	case "ListQueues":
		w.Write([]byte(`{"QueueUrls":["http://x/000000000000/dead","http://x/000000000000/source"]}`))
	case "GetQueueAttributes":
		var in struct{ QueueUrl string }
		json.NewDecoder(r.Body).Decode(&in)
		if strings.HasSuffix(in.QueueUrl, "/source") {
			w.Write([]byte(`{"Attributes":{"RedrivePolicy":"{\"deadLetterTargetArn\":\"arn:aws:sqs:us-east-1:000000000000:dead\",\"maxReceiveCount\":3}"}}`))
			return
		}
		// The dead-letter queue's depth is read on arrival (held above) and the
		// first look is then parked before it is answered: a read that has seen
		// the world as it was and is on its way back with it.
		s.mu.Lock()
		park := !s.parked
		s.parked = true
		s.mu.Unlock()
		if park {
			close(s.entered)
			<-s.release
		}
		w.Write([]byte(`{"Attributes":{"ApproximateNumberOfMessages":"` + strconv.Itoa(held) + `"}}`))
	default:
		w.WriteHeader(400)
	}
}

func queueBackend() (*backend, *queueService) {
	s := &queueService{entered: make(chan struct{}), release: make(chan struct{})}
	return newBackend(peers.InProcess(func(svc string) http.Handler {
		if svc == "sqs" {
			return s
		}
		return nil
	}), awsident.Default(), ""), s
}

func TestTheRailsDeadLetterReadStartedBeforeAMutationIsNotKeptAfterIt(t *testing.T) {
	b, svc := queueBackend()
	ctx := context.Background()
	got := make(chan bool, 1)
	go func() { got <- b.dlqHolds(ctx) }()
	<-svc.entered
	svc.setHeld(1) // the console sends a message to the dead-letter queue...
	b.bustGraph()  // ...and drops what it has cached, while the read is in flight
	close(svc.release)
	if <-got {
		t.Fatal("the parked read cannot have seen the message sent after it read: the test is not holding the order")
	}
	if !b.dlqHolds(ctx) {
		t.Error("the rail, after the console's own send, says no dead-letter queue holds a message: the read in flight when the mutation landed was cached over the drop")
	}
}

func TestTheWiresNeedsReadStartedBeforeAMutationIsNotKeptAfterIt(t *testing.T) {
	b, svc := queueBackend()
	c := &Console{be: b}
	ctx := context.Background()
	got := make(chan int, 1)
	go func() { d, _ := c.productNeeds(ctx); got <- len(d) }()
	<-svc.entered
	svc.setHeld(1)
	b.bustGraph()
	close(svc.release)
	if n := <-got; n != 0 {
		t.Fatalf("the parked read cannot have seen the message sent after it read: the test is not holding the order (%d)", n)
	}
	if d, _ := c.productNeeds(ctx); len(d) != 1 {
		t.Errorf("the wire, after the console's own send, names %d dead-letter queues holding messages, want 1: the read in flight when the mutation landed was cached over the drop", len(d))
	}
}
