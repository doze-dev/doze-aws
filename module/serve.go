package aws

import (
	"io"
	"net/http"

	dozeaws "github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/internal/console"
	"github.com/doze-dev/doze-aws/peers"

	"github.com/doze-dev/doze-aws/module/awslocal"
)

func init() { awslocal.RegisterServer("aws", serveFactory) }

// serveFactory assembles exactly what the standalone doze-aws binary serves:
// the full stack behind its gateway, wrapped in the traffic recorder, with the
// web console mounted at /_console. The console drives its own calls straight
// to each raw service handler (peers.InProcess over the stack), so they never
// appear in the Traffic tail; the "/_console" prefix can never collide with a
// valid S3 bucket name (those forbid underscores).
func serveFactory(datadir string) (http.Handler, io.Closer, error) {
	stack, err := dozeaws.NewStack(dozeaws.StackConfig{DataDir: datadir})
	if err != nil {
		return nil, nil, err
	}
	// The recorder needs the SAME identity the stack mints with: it classifies
	// a queue URL by its account prefix, so a mismatch quietly labels SQS
	// traffic as S3. Asked for rather than restated, so the two cannot drift.
	rec := console.NewRecorder(stack.Handler(), stack.Identity())
	con, err := console.New(console.Options{
		Peers:    peers.InProcess(stack.Service),
		Recorder: rec,
	})
	if err != nil {
		_ = stack.Close()
		return nil, nil, err
	}
	mux := http.NewServeMux()
	mux.Handle("/_console/", con)
	mux.Handle("/_console", http.RedirectHandler("/_console/", http.StatusFound))
	mux.Handle("/", rec)
	return mux, stack, nil
}
