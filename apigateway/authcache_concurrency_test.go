package apigateway

// The authorizer cache under concurrent load.
//
// This is the one piece of per-request in-memory state in this service, it is
// keyed by something a CLIENT chooses (the Authorization header), and it was
// unbounded until recently. Its locking and its eviction were both correct by
// inspection and neither had ever run in parallel in a test — and the race
// detector reports only what actually runs in parallel, which is how two
// shutdown races got through this session.
//
// Two shapes, because they stress different halves:
//
//	one shared token   every request hits the SAME key, so get/put contend on
//	                   one entry and the cache must actually serve hits.
//	unique tokens      every request mints a NEW key, which is the load that
//	                   made this cache grow without bound. Here it must stay
//	                   under maxCachedAuth while being written from many
//	                   goroutines at once — the eviction path, which is the
//	                   part that mutates the map most.

import (
	"encoding/json"
	"fmt"
	"github.com/doze-dev/doze-aws/internal/dozetest"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/doze-dev/doze-aws/peers"
)

// anyTokenLambda allows every token, so a test can mint as many distinct cache
// keys as it likes. fakeLambda only knows three fixed tokens.
type anyTokenLambda struct {
	calls atomic.Int32
}

func (f *anyTokenLambda) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var ev map[string]any
	json.Unmarshal(body, &ev)
	if strings.Contains(r.URL.Path, "/functions/auth/") {
		f.calls.Add(1)
		arn, _ := ev["methodArn"].(string)
		json.NewEncoder(w).Encode(map[string]any{
			"principalId": "user-1",
			"policyDocument": map[string]any{"Version": "2012-10-17", "Statement": []any{
				map[string]any{"Effect": "Allow", "Action": "execute-api:Invoke",
					"Resource": strings.SplitN(arn, "/", 2)[0] + "/*/GET/*"},
			}},
		})
		return
	}
	json.NewEncoder(w).Encode(map[string]any{"statusCode": 200, "body": "ok"})
}

// size reports how many entries the cache holds.
func (c *authCache) size() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

func authCacheServer(t *testing.T) (*Server, *httptest.Server, *anyTokenLambda, string) {
	t.Helper()
	fake := &anyTokenLambda{}
	peer := httptest.NewServer(fake)
	t.Cleanup(peer.Close)
	s, err := New(Options{DataDir: t.TempDir(), Logf: dozetest.Quiet(t),
		Peers: peers.Static{"lambda": peers.Endpoint{Client: peer.Client(), BaseURL: peer.URL}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	ts := httptest.NewServer(s)
	t.Cleanup(ts.Close)
	return s, ts, fake, authorizedAPI(t, s, 300)
}

// hammer runs fn from many goroutines and reports the first error.
func hammer(t *testing.T, workers, iters int, fn func(w, i int) error) {
	t.Helper()
	var wg sync.WaitGroup
	errCh := make(chan error, workers)
	for w := range workers {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := range iters {
				if err := fn(w, i); err != nil {
					select {
					case errCh <- err:
					default:
					}
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(errCh)
	if err := <-errCh; err != nil {
		t.Fatal(err)
	}
}

func TestAuthCacheUnderOneSharedToken(t *testing.T) {
	_, ts, fake, apiID := authCacheServer(t)
	const workers, iters = 16, 20

	hammer(t, workers, iters, func(w, i int) error {
		req, _ := http.NewRequest("GET", ts.URL+ExecutePrefix+apiID+"/v1/items", nil)
		req.Header.Set("Authorization", "allow")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return fmt.Errorf("worker %d iter %d: %w", w, i, err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			return fmt.Errorf("worker %d iter %d: status %d", w, i, resp.StatusCode)
		}
		return nil
	})

	// The cache must actually be serving. There is no single-flight, so the
	// first concurrent wave all miss together and the count is not a fixed
	// number — hence a loose bound, which still fails if the cache is bypassed.
	calls, total := fake.calls.Load(), int32(workers*iters)
	switch {
	case calls == 0:
		t.Error("authorizer never invoked — the API was not actually exercised")
	case calls >= total:
		t.Errorf("authorizer invoked %d times for %d requests — cache served nothing",
			calls, total)
	}
}

func TestAuthCacheStaysBoundedUnderUniqueTokens(t *testing.T) {
	s, ts, _, apiID := authCacheServer(t)
	// Comfortably past maxCachedAuth (512) so eviction runs many times, and
	// from enough goroutines that it runs concurrently with reads.
	const workers, iters = 16, 60

	hammer(t, workers, iters, func(w, i int) error {
		req, _ := http.NewRequest("GET", ts.URL+ExecutePrefix+apiID+"/v1/items", nil)
		req.Header.Set("Authorization", fmt.Sprintf("tok-%d-%d", w, i))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return fmt.Errorf("worker %d iter %d: %w", w, i, err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			return fmt.Errorf("worker %d iter %d: status %d", w, i, resp.StatusCode)
		}
		return nil
	})

	if n := s.authCache.size(); n > maxCachedAuth {
		t.Errorf("cache holds %d entries, cap is %d — the bound does not hold under "+
			"concurrent eviction", n, maxCachedAuth)
	}
	if s.authCache.size() == 0 {
		t.Error("cache is empty — the authorizer path was not exercised")
	}
}

// A TTL expiry racing a concurrent read is the other way this map is mutated:
// get() deletes the entry it just found expired.
func TestAuthCacheExpiryRacesReads(t *testing.T) {
	c := newAuthCache()
	now := time.Now()
	for i := range 64 {
		c.put(fmt.Sprintf("k%d", i), &authorizerResponse{PrincipalID: "p"}, now.Add(time.Millisecond))
	}
	hammer(t, 16, 64, func(w, i int) error {
		c.get(fmt.Sprintf("k%d", i%64), time.Now().Add(time.Second))
		c.put(fmt.Sprintf("k%d", i%64), &authorizerResponse{PrincipalID: "p"}, time.Now().Add(time.Second))
		return nil
	})
}
