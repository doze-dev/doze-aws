package console

// Exporting the running stack as a CloudFormation template.
//
// `doze-aws export` has always been able to write what is running as a
// template, and until now that was CLI-only: the console's own create-stack
// page opened an empty box and a three-line placeholder, which is the hardest
// starting point in the console — you have to know the YAML dialect AND which
// resource types this emulator supports before you can type anything true.
//
// The work was already done and tested (provision.Export walks the live
// services, cloudformation.Emit writes the template, and emit_roundtrip_test
// proves Export -> Emit -> Parse -> Transpile survives the trip). All that was
// missing was a way to reach it from a browser.

import (
	"context"
	"io"
	"net/http"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/cloudformation"
	"github.com/doze-dev/doze-aws/provision"
)

// gatewayHandler presents the console's service fanout as the http.Handler
// provision.Export expects.
//
// Export crawls every service by making ordinary AWS calls, so it wants
// something that looks like the gateway. The console does not have one — it has
// a client whose transport routes each request to the owning service — so this
// turns that inside out. Going through the same client means an export sees
// exactly the services the console can see, in either topology, rather than
// needing its own endpoint configuration.
type gatewayHandler struct{ b *backend }

func (g gatewayHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	out, err := http.NewRequestWithContext(r.Context(), r.Method, g.b.base+r.URL.RequestURI(), r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	out.Header = r.Header.Clone()
	resp, err := g.b.c.Do(out)
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
	io.Copy(w, resp.Body)
}

// ExportTemplate renders everything currently running as a CloudFormation
// template, the same bytes `doze-aws export` writes to stdout.
func (b *backend) ExportTemplate(ctx context.Context) ([]byte, error) {
	s, err := provision.Export(ctx, gatewayHandler{b}, awsident.Default())
	if err != nil {
		return nil, err
	}
	return cloudformation.Emit(s)
}
