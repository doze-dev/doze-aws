// Package kms is doze-aws's local Key Management Service with real crypto for
// all three key families:
//
//   - SYMMETRIC_DEFAULT keys encrypt/decrypt via AES-256-GCM with the
//     encryption context bound as authenticated data; the ciphertext blob
//     embeds the key id (like real KMS), so Decrypt needs no KeyId.
//   - RSA_2048/3072/4096 and ECC_NIST_P256/P384/P521 keys really sign/verify
//     (RSASSA_PKCS1_V1_5, RSASSA_PSS, ECDSA) and RSA keys really
//     encrypt/decrypt (RSAES_OAEP_SHA_1/SHA_256); GetPublicKey returns the
//     genuine DER SPKI, so signatures verify outside KMS too.
//   - HMAC_224/256/384/512 keys really GenerateMac/VerifyMac.
//
// Application crypto code works unmodified. ECC_SECG_P256K1 (secp256k1) is
// the one spec that answers UnsupportedOperationException — Go's standard
// library has no secp256k1, and doze-aws does not take crypto dependencies.
//
// See docs/api-support/kms.md for the operation-by-operation support table.
package kms

import (
	"net/http"
	"os"
	"path/filepath"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/awsjson"
	"github.com/doze-dev/doze-aws/peers"
)

// Options configures the service.
type Options struct {
	// DataDir holds the bbolt store (kms.bolt). Required.
	DataDir string
	// Peers is accepted for constructor uniformity; KMS calls no siblings.
	Peers peers.Directory
	// Logf receives log lines; nil discards.
	Logf func(format string, args ...any)
	// Clock overrides time.Now in tests.
	Clock func() time.Time
}

// Server is the KMS service: an http.Handler speaking AWS JSON 1.1, and an
// io.Closer that stops the janitor and closes the store.
type Server struct {
	store *Store
	logf  func(format string, args ...any)
	api   awsjson.API
	stop  chan struct{}
}

// New opens the bbolt store under DataDir and starts the deletion janitor.
func New(opts Options) (*Server, error) {
	if err := os.MkdirAll(opts.DataDir, 0o755); err != nil {
		return nil, err
	}
	db, err := bolt.Open(filepath.Join(opts.DataDir, "kms.bolt"), 0o600, nil)
	if err != nil {
		return nil, err
	}
	logf := opts.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	s := &Server{
		store: newStore(db),
		logf:  logf,
		api:   awsjson.API{TargetPrefix: "TrentService", JSONVersion: "1.1"},
		stop:  make(chan struct{}),
	}
	if opts.Clock != nil {
		s.store.clock = opts.Clock
	}
	go s.janitor()
	return s, nil
}

// Close stops the janitor and closes the bbolt DB.
func (s *Server) Close() error {
	close(s.stop)
	return s.store.db.Close()
}

// janitor finalizes scheduled key deletions whose waiting period has passed.
func (s *Server) janitor() {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-t.C:
			s.store.SweepDeletions()
		}
	}
}

// handler is one KMS action.
type handler func(s *Server, p map[string]any) (any, *awshttp.APIError)

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	action, aerr := s.api.Action(r)
	if aerr != nil {
		s.api.WriteError(w, aerr)
		return
	}
	var params map[string]any
	if aerr := awsjson.DecodeBody(r, &params); aerr != nil {
		s.api.WriteError(w, aerr)
		return
	}
	h, ok := handlers[action]
	if !ok {
		s.api.WriteError(w, awshttp.Errf(400, "InvalidAction", "unknown KMS action %q", action))
		return
	}
	result, aerr := h(s, params)
	if aerr != nil {
		s.logf("kms: %s -> %s", action, aerr.Code)
		s.api.WriteError(w, aerr)
		return
	}
	s.logf("kms: %s ok", action)
	s.api.Write(w, result)
}
