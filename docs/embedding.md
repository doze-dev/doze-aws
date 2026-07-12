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
service-to-service features (SNS→SQS, S3 notifications, EventBridge targets,
Lambda event-source mappings) work with zero configuration.

One case does need configuration: if your **Lambda handler code** calls a sibling
service through an AWS SDK, set `StackConfig.Endpoint` to the stack's
externally-reachable base URL (e.g. `"http://127.0.0.1:4566"`). It is injected
into function processes as `AWS_ENDPOINT_URL`. The `doze-aws` binary derives this
from `--listen` automatically; embedders serving the handler over HTTP should
pass it explicitly.

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

## A complete example

A self-contained program: embed a stack, serve it, and drive it with the real
AWS SDK — several services through one endpoint, no Docker, no separate process.

```go
package main

import (
	"context"
	"fmt"
	"log"
	"net/http/httptest"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"

	"github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/awsident"
)

func main() {
	ctx := context.Background()

	// 1. Embed the stack (S3 + SQS here) and serve it on one endpoint.
	stack, err := dozeaws.NewStack(dozeaws.StackConfig{
		DataDir:  "./data",
		Services: []string{"s3", "sqs"}, // nil = every implemented service
	})
	if err != nil {
		log.Fatal(err)
	}
	defer stack.Close()

	ts := httptest.NewServer(stack.Handler())
	defer ts.Close()

	// 2. Point ordinary AWS SDK clients at it. Any credentials work locally.
	cfg := aws.Config{
		Region:      awsident.Region, // us-east-1
		Credentials: credentials.NewStaticCredentialsProvider(awsident.AccessKeyID, awsident.SecretAccessKey, ""),
	}
	s3c := awss3.NewFromConfig(cfg, func(o *awss3.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
		o.UsePathStyle = true
	})
	sqsc := awssqs.NewFromConfig(cfg, func(o *awssqs.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})

	// 3. Use them exactly as you would against real AWS.
	if _, err := s3c.CreateBucket(ctx, &awss3.CreateBucketInput{Bucket: aws.String("uploads")}); err != nil {
		log.Fatal(err)
	}
	if _, err := s3c.PutObject(ctx, &awss3.PutObjectInput{
		Bucket: aws.String("uploads"),
		Key:    aws.String("hello.txt"),
		Body:   strings.NewReader("hello world"), // any io.Reader
	}); err != nil {
		log.Fatal(err)
	}

	q, err := sqsc.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("jobs")})
	if err != nil {
		log.Fatal(err)
	}
	if _, err := sqsc.SendMessage(ctx, &awssqs.SendMessageInput{
		QueueUrl:    q.QueueUrl,
		MessageBody: aws.String("uploads/hello.txt"),
	}); err != nil {
		log.Fatal(err)
	}

	out, _ := sqsc.ReceiveMessage(ctx, &awssqs.ReceiveMessageInput{QueueUrl: q.QueueUrl})
	fmt.Println("received:", aws.ToString(out.Messages[0].Body)) // uploads/hello.txt
}
```

Both clients hit the one `ts.URL`; the stack's gateway routes each request to the
right service by its wire signals. Swap `httptest.NewServer` for
`http.ListenAndServe("127.0.0.1:4566", stack.Handler())` to keep it up as a
long-running local endpoint (4566 matches LocalStack, so existing
`AWS_ENDPOINT_URL` setups work unchanged).

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
