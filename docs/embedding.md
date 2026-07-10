# Embedding doze-aws

doze-aws is a library first. Every service is a plain Go package exporting an
`http.Handler`, and `dozeaws.NewStack` assembles any subset behind one gateway.
The binary is a thin wrapper around exactly that API.

## The whole stack behind one handler

```go
import "github.com/doze-dev/doze-aws"

stack, err := dozeaws.NewStack(dozeaws.StackConfig{DataDir: "./data"})
if err != nil {
	log.Fatal(err)
}
defer stack.Close()

http.ListenAndServe("127.0.0.1:4566", stack.Handler())
```

`StackConfig` selects services (`Services: []string{"s3", "sqs"}`; nil = all
implemented), the data root, an S3 virtual-hosted base host, and a `Logf`.
Services inside a stack are wired to each other in-process — no sockets — so
cross-service features (SNS→SQS, S3 notifications, EventBridge targets, Lambda)
work with zero configuration.

## A single service

Skip the stack when you want one service under your own supervision:

```go
import "github.com/doze-dev/doze-aws/s3"

srv, err := s3.New(s3.Options{DataDir: "./data/s3"})
if err != nil {
	log.Fatal(err)
}
defer srv.Close()

http.ListenAndServe("127.0.0.1:9000", srv) // *s3.Server is an http.Handler
```

Every service package follows the same shape:

```go
type Options struct {
	DataDir string          // service owns this directory
	Peers   peers.Directory // cross-service reach; nil disables it
	Logf    func(string, ...any)
	Clock   func() time.Time // tests
}

func New(Options) (*Server, error) // *Server implements http.Handler + io.Closer
```

## Wiring services across processes

When services run in separate processes (as doze does, one child per service),
give each a `peers.Directory` so it can reach its siblings:

```go
import "github.com/doze-dev/doze-aws/peers"

// From the environment: DOZE_<SVC>_SOCKET, then AWS_ENDPOINT_URL_<SVC>,
// then AWS_ENDPOINT_URL.
sns.New(sns.Options{DataDir: dir, Peers: peers.FromEnv()})

// Explicit unix sockets, one per service.
peers.UnixSockets(map[string]string{"sqs": "/run/sqs.sock"})
```

`peers.InProcess` (what `NewStack` uses) dispatches straight into sibling
handlers with no network at all.

## Testing

Construct a service or stack, wrap it in `httptest.NewServer`, and point a real
AWS SDK client at the URL with `BaseEndpoint`. That is exactly how doze-aws's
own contract tests run — with both `aws-sdk-go-v2` and the legacy
`aws-sdk-go` — so your tests exercise the same wire path production does.
