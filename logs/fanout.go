package logs

// Fan-out from PutLogEvents to a group's subscription filters. The batch is
// handed to one worker goroutine so a Lambda writing its own lines never
// waits on the delivery; the worker matches each event against the filter,
// wraps the matches in the envelope AWS sends — gzip-compressed JSON,
// base64 under `awslogs.data` for Lambda, the raw gzip bytes as a Kinesis
// record — and calls the peer. A failure is logged with the group, the
// filter and the destination; there is no retry queue, as there is none on
// AWS for a Lambda destination either.

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/peercall"
	"github.com/doze-dev/doze-aws/internal/trace"
	"github.com/doze-dev/doze-aws/peers"
)

// fanBuffer bounds how many batches wait for the worker before PutLogEvents
// drops one with a log line rather than blocking the writer.
const fanBuffer = 1024

type fanBatch struct {
	ctx    context.Context
	group  string
	stream string
	events []Stored
}

// compiledSub is a subscription with its pattern compiled once.
type compiledSub struct {
	Subscription
	match matcher
}

type fanout struct {
	store *Store
	peers peers.Directory
	logf  func(string, ...any)
	in    chan fanBatch
	done  chan struct{}

	mu    sync.Mutex
	cache map[string][]compiledSub // group → its filters, compiled
}

func newFanout(store *Store, dir peers.Directory, logf func(string, ...any)) *fanout {
	f := &fanout{store: store, peers: dir, logf: logf, in: make(chan fanBatch, fanBuffer), done: make(chan struct{}), cache: map[string][]compiledSub{}}
	go f.run()
	return f
}

// enqueue hands a stored batch to the worker. The request context is
// detached so the delivery outlives the PutLogEvents call that caused it.
func (f *fanout) enqueue(ctx context.Context, group, stream string, events []Stored) {
	if len(events) == 0 {
		return
	}
	select {
	case f.in <- fanBatch{ctx: context.WithoutCancel(ctx), group: group, stream: stream, events: events}:
	default:
		f.logf("logs: subscription fan-out for %s is %d batches behind; dropped one", group, fanBuffer)
	}
}

// forget drops a group's compiled filters after they changed.
func (f *fanout) forget(group string) {
	f.mu.Lock()
	delete(f.cache, group)
	f.mu.Unlock()
}

// close stops the worker after it drains what is queued.
func (f *fanout) close() {
	close(f.in)
	<-f.done
}

func (f *fanout) run() {
	defer close(f.done)
	for b := range f.in {
		f.deliver(b)
	}
}

// subs returns a group's filters, compiled, from the cache or the store.
func (f *fanout) subs(group string) []compiledSub {
	f.mu.Lock()
	defer f.mu.Unlock()
	if cs, ok := f.cache[group]; ok {
		return cs
	}
	list, err := f.store.Subscriptions(group)
	if err != nil {
		f.logf("logs: subscriptions(%s): %v", group, err)
		return nil
	}
	cs := make([]compiledSub, 0, len(list))
	for _, sub := range list {
		m, err := compile(sub.Pattern)
		if err != nil {
			f.logf("logs: subscription %s/%s has an unusable pattern: %v", group, sub.Name, err)
			continue
		}
		cs = append(cs, compiledSub{Subscription: sub, match: m})
	}
	f.cache[group] = cs
	return cs
}

func (f *fanout) deliver(b fanBatch) {
	for _, sub := range f.subs(b.group) {
		var matched []Stored
		for _, ev := range b.events {
			if sub.match(ev.Msg) {
				matched = append(matched, ev)
			}
		}
		if len(matched) == 0 {
			continue
		}
		data, err := envelope(b.group, b.stream, sub.Name, matched)
		if err != nil {
			f.logf("logs: subscription %s/%s: %v", b.group, sub.Name, err)
			continue
		}
		// The delivery is Logs' own call, on behalf of the group.
		ctx := peers.WithPrincipal(b.ctx, "logs", awsident.ARN("logs", "log-group:"+b.group+":*"))
		err = trace.Step(ctx, trace.Event{Service: "logs", Action: "SubscriptionFilter", Resource: b.group + "/" + sub.Name, Via: "logs:PutLogEvents"},
			func(ctx context.Context) error {
				switch {
				case strings.Contains(sub.Destination, ":lambda:"):
					fn := sub.Destination[strings.LastIndex(sub.Destination, ":")+1:]
					payload, _ := json.Marshal(map[string]any{"awslogs": map[string]any{"data": base64.StdEncoding.EncodeToString(data)}})
					return peercall.LambdaInvokeAsync(ctx, f.peers, fn, payload)
				case strings.Contains(sub.Destination, ":kinesis:"):
					stream := sub.Destination[strings.LastIndex(sub.Destination, "/")+1:]
					key := b.stream
					if sub.Distribution != "ByLogStream" {
						key = randomKey()
					}
					return peercall.KinesisPutRecord(ctx, f.peers, stream, key, data)
				}
				return nil
			})
		if err != nil {
			f.logf("logs: subscription %s/%s -> %s: %v", b.group, sub.Name, sub.Destination, err)
		}
	}
}

// envelope is the CloudWatch Logs subscription message, gzip-compressed.
func envelope(group, stream, filter string, events []Stored) ([]byte, error) {
	items := make([]map[string]any, 0, len(events))
	for _, ev := range events {
		items = append(items, map[string]any{"id": eventID(ev.Seq), "timestamp": ev.TS, "message": ev.Msg})
	}
	doc := map[string]any{
		"messageType":         "DATA_MESSAGE",
		"owner":               awsident.AccountID,
		"logGroup":            group,
		"logStream":           stream,
		"subscriptionFilters": []string{filter},
		"logEvents":           items,
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	zw.Write(raw)
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// eventID renders a sequence as the 56-digit decimal id AWS uses, so a
// consumer that parses ids as big integers is not surprised.
func eventID(seq int64) string {
	return fmt.Sprintf("%056d", seq)
}

func randomKey() string {
	var b [8]byte
	rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
