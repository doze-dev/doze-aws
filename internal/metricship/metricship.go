// Package metricship carries metrics from a service to the CloudWatch
// service off the caller's path: Lambda invocations, API Gateway requests,
// Step Functions executions and CloudWatch Logs metric filters all publish
// through one Shipper.
//
// It is logship's sibling and deliberately its near-twin — batched per
// namespace, sent from a worker goroutine, disabled after one line when the
// target service is absent — because the failure it exists to prevent is the
// same one. A metric published on every Lambda invocation must not make
// Invoke wait for CloudWatch, and a stack without CloudWatch enabled must not
// pay for the metrics it is not collecting.
//
// # Why not just reuse logship
//
// The batching key differs. Logs batch per (group, stream) because
// PutLogEvents takes one stream; metrics batch per namespace because
// PutMetricData takes one namespace and many differently-dimensioned data.
// Sharing the type would mean a key struct with two unused fields on each
// side, which is more confusing than a second small worker.
//
// # Who produces, and who deliberately does not
//
// Four producers, chosen because each one's metric is what a stack's first
// alarm watches and none of them can be faked by the developer:
//
//	AWS/Lambda      Invocations, Errors, Throttles, Duration   by FunctionName
//	AWS/ApiGateway  Count, 4XXError/5XXError (4xx/5xx on an
//	                HTTP API), Latency                         by ApiName+Stage
//	                                                           (ApiId+Stage on v2)
//	AWS/States      ExecutionsStarted/Succeeded/Failed/
//	                TimedOut/Aborted, ExecutionTime            by StateMachineArn
//	custom          whatever EMF or a metric filter names      as named
//
// SQS, SNS, DynamoDB and Kinesis publish nothing, and that is a decision
// rather than an omission. Their built-in metrics are all measurements of
// scale — ApproximateNumberOfMessagesVisible, ConsumedReadCapacityUnits,
// IncomingRecords, GetRecords.IteratorAgeMilliseconds — and locally the
// numbers are whatever the developer's own test just put there. An alarm
// written against them would fire on a fixture rather than on a condition,
// which is worse than having no local datapoint: a silent metric reads as
// INSUFFICIENT_DATA, and an alarm that says so is telling the truth.
//
// Two settings that would turn such metrics on are stored and echoed but
// enable nothing, and say so where they are defined: Kinesis's
// EnableEnhancedMonitoring and DynamoDB's Contributor Insights.
package metricship

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/doze-dev/doze-aws/internal/bg"
	"github.com/doze-dev/doze-aws/internal/peercall"
	"github.com/doze-dev/doze-aws/peers"
)

// Datum is one observation: peercall.MetricDatum under a shorter name.
type Datum = peercall.MetricDatum

// flushAfter bounds how long an observation waits before it is published.
// Longer than logship's, because a metric is read at period granularity —
// nobody is watching for a datapoint within 250ms — and a wider window means
// fewer, larger PutMetricData calls.
const flushAfter = time.Second

// maxBatch is AWS's ceiling on MetricData per call, and a sensible one to
// respect locally: it bounds what a burst can put in a single request.
const maxBatch = 1000

// Shipper batches and publishes metrics for one producer.
type Shipper struct {
	name  string
	peers peers.Directory
	logf  func(string, ...any)

	mu       sync.Mutex
	pending  map[string][]Datum // namespace -> data
	timer    *time.Timer
	disabled bool
	closed   bool
	// dead is set when the worker panicked: a caller's Put then reports a
	// drop instead of buffering into a queue nobody drains.
	dead bool
	work chan map[string][]Datum
	// quit tells the worker to drain and stop. The work channel is never
	// closed, because Flush runs on a timer goroutine that can be between
	// its unlock and its send at the moment Close is called — closing the
	// channel underneath it is a panic, and a flag cannot prevent it.
	quit chan struct{}
	done chan struct{}
	once sync.Once
}

// New starts a shipper. name appears in the one line said when CloudWatch is
// absent, so a reader knows which producer went quiet.
func New(name string, dir peers.Directory, logf func(string, ...any)) *Shipper {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	s := &Shipper{
		name: name, peers: dir, logf: logf,
		pending: map[string][]Datum{},
		work:    make(chan map[string][]Datum, 64),
		quit:    make(chan struct{}), done: make(chan struct{}),
	}
	go s.worker()
	return s
}

// Put queues observations; they are published within flushAfter, or sooner on
// Flush. Safe from any goroutine, and cheap when CloudWatch is not enabled.
func (s *Shipper) Put(namespace string, data ...Datum) {
	if len(data) == 0 {
		return
	}
	s.mu.Lock()
	if s.dead {
		s.mu.Unlock()
		// Reported, unlike a deliberate close: a shipper that died is not
		// something the caller asked for, and the observations are gone.
		s.logf("%s: the metric shipper is not running; dropped %d observation(s)",
			s.name, len(data))
		return
	}
	defer s.mu.Unlock()
	if s.disabled || s.closed {
		return
	}
	s.pending[namespace] = append(s.pending[namespace], data...)
	if s.timer == nil {
		s.timer = time.AfterFunc(flushAfter, s.Flush)
	}
}

// die marks the shipper dead after a panic, so Put starts reporting drops.
func (s *Shipper) die() {
	s.mu.Lock()
	s.closed, s.dead = true, true
	s.mu.Unlock()
}

// Count is the common case: one observation of a counter, dimensioned.
func (s *Shipper) Count(namespace, metric string, dims map[string]string, n float64) {
	s.Put(namespace, Datum{MetricName: metric, Value: n, Unit: "Count", Dimensions: dims})
}

// Duration records a millisecond measurement, which is what every latency
// metric here is.
func (s *Shipper) Duration(namespace, metric string, dims map[string]string, d time.Duration) {
	s.Put(namespace, Datum{MetricName: metric,
		Value: float64(d.Nanoseconds()) / 1e6, Unit: "Milliseconds", Dimensions: dims})
}

// Flush hands everything pending to the worker.
func (s *Shipper) Flush() {
	s.send(s.take())
}

// take detaches what is pending and disarms the timer.
func (s *Shipper) take() map[string][]Datum {
	s.mu.Lock()
	defer s.mu.Unlock()
	batch := s.pending
	s.pending = map[string][]Datum{}
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	return batch
}

// send queues a batch, giving up if the worker has already stopped — which is
// what a Flush racing Close does, and it costs at most the metrics that were
// pending at the instant of shutdown.
func (s *Shipper) send(batch map[string][]Datum) {
	if len(batch) == 0 {
		return
	}
	select {
	case s.work <- batch:
	case <-s.done:
	}
}

// Close flushes and stops the worker, waiting briefly for what was pending so
// a test can read back what it published.
func (s *Shipper) Close() {
	s.once.Do(func() {
		batch := s.take()
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
		s.send(batch)
		close(s.quit)
		select {
		case <-s.done:
		case <-time.After(2 * time.Second):
		}
	})
}

func (s *Shipper) worker() {
	// Registered after close(s.done) so it runs before it: the shipper must be
	// marked dead before Close is told the worker finished.
	defer close(s.done)
	defer bg.Recover(s.logf, s.name+": shipper", s.die)
	for {
		select {
		case batch := <-s.work:
			s.publishAll(batch)
		case <-s.quit:
			// Everything already queued still goes out: Close waits for this,
			// and a test that publishes then reads back depends on it.
			for {
				select {
				case batch := <-s.work:
					s.publishAll(batch)
				default:
					return
				}
			}
		}
	}
}

func (s *Shipper) publishAll(batch map[string][]Datum) {
	for ns, data := range batch {
		s.publish(ns, data)
	}
}

func (s *Shipper) publish(namespace string, data []Datum) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for len(data) > 0 {
		n := min(len(data), maxBatch)
		if err := peercall.PutMetrics(ctx, s.peers, namespace, data[:n]); err != nil {
			s.fail(err)
			return
		}
		data = data[n:]
	}
}

// fail records a publishing failure: an absent CloudWatch turns the shipper
// off after one line; anything else is one line per failure.
func (s *Shipper) fail(err error) {
	if errors.Is(err, peercall.ErrNoCloudWatch) {
		s.mu.Lock()
		already := s.disabled
		s.disabled = true
		s.pending = map[string][]Datum{}
		s.mu.Unlock()
		if !already {
			s.logf("%s: the cloudwatch service is not enabled; metrics are dropped", s.name)
		}
		return
	}
	s.logf("%s: publishing metrics: %v", s.name, err)
}
