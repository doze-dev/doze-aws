package peers

// Injecting failure into the path between services.
//
// Every cascade in doze-aws goes through a Directory: an S3 notification
// reaching Lambda, a Step Functions task calling SQS, a subscription filter
// shipping to Kinesis, an alarm firing a topic. Until now there was no way to
// make any of those fail, hang, or vanish — so every one of those paths had
// only ever been exercised on its success case, and "what happens when the
// sibling is down" was answered by reading the code.
//
// This is deliberately in the non-test package. A fault injector that lives in
// a _test.go file is importable by exactly one package, and the paths worth
// breaking are spread across every service.

import (
	"fmt"
	"net/http"
	"time"
)

// Fault describes what should happen to a call instead of it succeeding.
//
// The zero Fault does nothing, so a rule that matches but carries no effect is
// a pass-through rather than a silent black hole.
type Fault struct {
	// Status answers with this HTTP status and an empty body, without the
	// sibling ever seeing the request. Zero means the request goes through.
	Status int
	// Err fails the round-trip outright, the way a refused connection does.
	// This is a DIFFERENT failure from Status: a caller that handles a 500 may
	// still not handle a transport error, and the two take different code
	// paths in every SDK.
	Err error
	// Delay is applied before anything else, including before Status and Err.
	// Use it to push a caller past its own timeout.
	Delay time.Duration
}

// FaultFunc decides what should happen to one call. Returning the zero Fault
// lets it through. It is called on every peer request, so it must be safe for
// concurrent use.
type FaultFunc func(service string, r *http.Request) Fault

// WithFaults returns a Directory that consults fn before each call reaches the
// sibling underneath.
//
// A decorator over a real Directory rather than a standalone fake: the point is
// to break a path that otherwise works, and a fake would be testing itself.
func WithFaults(inner Directory, fn FaultFunc) Directory {
	if fn == nil {
		return inner
	}
	return faultDir{inner: inner, fn: fn}
}

type faultDir struct {
	inner Directory
	fn    FaultFunc
}

func (d faultDir) Endpoint(service string) (Endpoint, bool) {
	ep, ok := d.inner.Endpoint(service)
	if !ok {
		return ep, false
	}
	inner := ep.Client
	if inner == nil {
		inner = http.DefaultClient
	}
	ep.Client = &http.Client{
		Transport: faultTransport{service: service, fn: d.fn, next: inner.Transport},
		Timeout:   inner.Timeout,
	}
	return ep, true
}

// Unreachable is the Directory a service sees when a sibling is simply not
// there. Distinct from a fault: this is the deployment saying "no wiring", and
// callers are expected to degrade rather than error.
func Unreachable(inner Directory, services ...string) Directory {
	gone := make(map[string]bool, len(services))
	for _, s := range services {
		gone[s] = true
	}
	return missingDir{inner: inner, gone: gone}
}

type missingDir struct {
	inner Directory
	gone  map[string]bool
}

func (d missingDir) Endpoint(service string) (Endpoint, bool) {
	if d.gone[service] {
		return Endpoint{}, false
	}
	return d.inner.Endpoint(service)
}

type faultTransport struct {
	service string
	fn      FaultFunc
	next    http.RoundTripper
}

func (t faultTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	f := t.fn(t.service, r)
	if f.Delay > 0 {
		select {
		case <-time.After(f.Delay):
		case <-r.Context().Done():
			// The caller gave up first, which is usually the point of a delay.
			return nil, r.Context().Err()
		}
	}
	if f.Err != nil {
		return nil, f.Err
	}
	if f.Status != 0 {
		return &http.Response{
			StatusCode: f.Status,
			Status:     fmt.Sprintf("%d %s", f.Status, http.StatusText(f.Status)),
			Header:     http.Header{},
			Body:       http.NoBody,
			Request:    r,
		}, nil
	}
	next := t.next
	if next == nil {
		next = http.DefaultTransport
	}
	return next.RoundTrip(r)
}
