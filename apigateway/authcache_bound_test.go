package apigateway

import (
	"fmt"
	"testing"
	"time"
)

// The authorizer cache is keyed by authorizer id plus the caller's identity
// source — for a TOKEN authorizer, the Authorization header. A client sending
// per-request JWTs therefore mints a new key on every request.
//
// Entries were removed only by a `get` for that same key, which for a
// per-request token never comes again. So a load test against a REST API behind
// a TOKEN authorizer left one entry per request — each holding a whole policy
// document — resident for the life of the process, long after its TTL.
//
// eventbridge's patternCache already caps itself at 512 and explains why. This
// is the same argument with a worse input: that one is keyed by rule patterns
// an operator writes, this one by whatever a caller sends.
func TestTheAuthCacheIsBounded(t *testing.T) {
	c := newAuthCache()
	future := time.Now().Add(time.Hour)

	// Ten times the cap, all live, all distinct — a client with per-request
	// tokens and none of them expiring.
	for i := range maxCachedAuth * 10 {
		c.put(fmt.Sprintf("auth1\x00jwt-%d", i), &authorizerResponse{}, future)
	}

	c.mu.Lock()
	n := len(c.entries)
	c.mu.Unlock()
	if n > maxCachedAuth {
		t.Errorf("cache holds %d entries, cap is %d — unbounded in client input", n, maxCachedAuth)
	}
}

// Expired entries are swept before anything live is dropped, so a busy but
// bounded workload keeps its cache instead of being cleared by churn beside it.
func TestExpiredEntriesGoBeforeLiveOnes(t *testing.T) {
	c := newAuthCache()
	now := time.Now()

	// Fill to the cap with entries that are already dead.
	for i := range maxCachedAuth {
		c.put(fmt.Sprintf("auth1\x00expired-%d", i), &authorizerResponse{}, now.Add(-time.Minute))
	}
	// One more live entry triggers the sweep.
	c.put("auth1\x00live", &authorizerResponse{}, now.Add(time.Hour))

	if _, ok := c.get("auth1\x00live", now); !ok {
		t.Error("the live entry was dropped by a sweep that should have taken the dead ones")
	}
	c.mu.Lock()
	n := len(c.entries)
	c.mu.Unlock()
	if n > maxCachedAuth {
		t.Errorf("still %d entries after a sweep", n)
	}
}

// The cache must still cache: a repeated identity is answered without a second
// authorizer invocation, which is the entire point of it existing.
func TestTheCacheStillCaches(t *testing.T) {
	c := newAuthCache()
	now := time.Now()
	want := &authorizerResponse{}

	c.put("auth1\x00token", want, now.Add(time.Hour))
	got, ok := c.get("auth1\x00token", now)
	if !ok || got != want {
		t.Fatalf("a live entry was not returned: %v %v", got, ok)
	}
	// ...and an expired one is not.
	if _, ok := c.get("auth1\x00token", now.Add(2*time.Hour)); ok {
		t.Error("an expired entry was served")
	}
}
