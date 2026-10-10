package aws

import (
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/provision"

	"github.com/doze-dev/doze-aws/module/awslocal"
	"github.com/doze-dev/doze-sdk/engine"
)

// Converge implements engine.Converger: hand the declared stack to doze-aws's
// own convergence (provision.Apply — the same engine `doze-aws apply` uses),
// speaking real wire protocols against the running instance. Idempotent, and
// it never stomps live secret/parameter values without force.
func (Driver) Converge(ctx context.Context, inst engine.Instance, _ engine.Toolchain, ep engine.Endpoint) error {
	cfg, ok := inst.Spec.(*Config)
	if !ok || cfg == nil || cfg.Stack == nil {
		return nil
	}
	gw := clientHandler{c: awslocal.UnixHTTPClient(ep.Backend)}
	// The identity provision.Apply mints ARNs with — integration URIs, activity
	// ARNs — which are then SENT to the instance, so it has to be the identity
	// that instance runs under.
	//
	// awsident.Default() is not a guess here: serveFactory builds this module's
	// stack with a zero-value StackConfig.Identity, which resolves to exactly
	// this, and there is no way to configure otherwise. If the module ever
	// gains a configurable region or account, this and serve.go change together
	// — which is why serve.go asks the stack rather than restating a constant.
	if _, err := provision.Apply(ctx, gw, cfg.Stack, awsident.Default()); err != nil {
		return fmt.Errorf("converging aws stack: %w", err)
	}
	return nil
}

// clientHandler adapts the instance's backend socket to the http.Handler
// provision.Apply drives, so convergence goes through the same gateway the
// SDKs use.
type clientHandler struct{ c *http.Client }

func (h clientHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	out := r.Clone(r.Context())
	out.RequestURI = ""
	out.URL.Scheme = "http"
	if out.URL.Host == "" {
		out.URL.Host = "aws.doze.internal"
	}
	resp, err := h.c.Do(out)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}
