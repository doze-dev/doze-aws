// Package lambdaruntime runs Lambda functions as supervised local processes
// that speak the AWS Lambda Runtime API. Each function gets one child process
// (serial invocations in this phase) started with AWS_LAMBDA_RUNTIME_API
// pointing at a per-function loopback listener serving the four runtime routes:
//
//	GET  /2018-06-01/runtime/invocation/next
//	POST /2018-06-01/runtime/invocation/{id}/response
//	POST /2018-06-01/runtime/invocation/{id}/error
//	POST /2018-06-01/runtime/init/error
//
// The official runtime interface clients (provided.al2 bootstrap, awslambdaric
// for Python/Node) speak this protocol unmodified — so real handlers run with
// no Docker.
package lambdaruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Spec describes how to run one function.
type Spec struct {
	Name       string
	Handler    string
	Runtime    string            // provided.*, go, python3.x, nodejs*
	Command    []string          // explicit command (doze extension) — wins over Runtime mapping
	Dir        string            // working directory
	Env        map[string]string // function environment
	Timeout    time.Duration
	MemorySize int               // MB, as configured; 0 falls back to Lambda's own default
	Endpoints  map[string]string // AWS_ENDPOINT_URL_* injected so handlers reach sibling services
}

// Result is one invocation's outcome.
type Result struct {
	Payload     []byte
	FunctionErr string // non-empty on a handler error (X-Amz-Function-Error)
	Logs        []byte // tail of stdout/stderr

	// Init is how long the function took to become ready, and is only set on a
	// cold start — a warm runner reports zero, the way AWS omits Init Duration
	// from a warm invocation's REPORT line.
	//
	// Splitting it out is the visible half of not charging cold start to the
	// function's timeout: the console can say "412ms of that was starting the
	// process, 18ms was your handler", which is the distinction people actually
	// need when a local invoke feels slow.
	Init time.Duration
	// Exec is time from the function fetching the work to it answering — the
	// part a timeout applies to.
	Exec time.Duration
}

// invocation is queued work for the runtime loop.
type invocation struct {
	id      string
	payload []byte
	// deadline is set when the function FETCHES the work, not when it was
	// queued — see handleNext.
	deadline time.Time
	done     chan Result
	// dispatched closes when handleNext hands this invocation to the function,
	// which is the moment the timeout starts applying.
	dispatched chan struct{}
	// cold records whether this invocation was the one that waited for a
	// process to come up, so Init is reported for it and not for the warm
	// invocations that follow.
	cold bool
	// queuedAt and startedAt bracket the two phases.
	queuedAt  time.Time
	startedAt time.Time
}

// initBudget bounds how long a function may take to come up and fetch its
// first invocation. AWS gives init its own allowance (10s) separate from the
// function timeout, and this is the same idea: a cold start is not the
// function running slowly, so it must not be charged to the function's clock —
// but it cannot be unbounded either, or a handler that hangs before ever
// polling would block the caller forever.
//
// A process that DIES during init does not wait this out: reap fails the
// queued invocations immediately with the exit error, which is a far more
// useful message than a timeout.
const initBudget = 10 * time.Second

// defaultTimeout matches Lambda's own default when a function does not set one.
const defaultTimeout = 3 * time.Second

// defaultMemoryMB matches Lambda's default allocation.
const defaultMemoryMB = 128

// graceAfterDeadline lets the child report its own timeout — which carries the
// function's stack — before Invoke gives up and reports a generic one.
const graceAfterDeadline = time.Second

// MaxWait is the longest Invoke can take for a function with this timeout: a
// cold start, then the function's own budget and the grace.
//
// A caller that imposes its own deadline must not set it shorter than this. A
// shorter one races the runtime and wins for the wrong reason: the caller sees
// "context deadline exceeded" and reports a 500 transport error, where AWS
// reports a 200 with a function-timeout payload. That is a worse answer AND a
// different one, and it was the second cause of the flake in
// TestDeployedAPIInvokesLambda — fixing only the runtime's own clock left this
// one still measuring from before the process had started.
func MaxWait(timeout time.Duration) time.Duration {
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	return initBudget + timeout + graceAfterDeadline
}

// Runner supervises one function's process and Runtime API listener.
type Runner struct {
	spec Spec
	logf func(string, ...any)

	mu      sync.Mutex
	ln      net.Listener
	cmd     *exec.Cmd
	queue   chan *invocation
	current *invocation
	pending map[string]*invocation
	logTail *ringBuffer
	started bool
	stopped bool
}

// NewRunner builds a runner (the process starts on first Invoke).
func NewRunner(spec Spec, logf func(string, ...any)) *Runner {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if spec.Timeout <= 0 {
		spec.Timeout = defaultTimeout
	}
	return &Runner{
		spec:    spec,
		logf:    logf,
		queue:   make(chan *invocation, 64),
		pending: map[string]*invocation{},
		logTail: newRingBuffer(16 << 10),
	}
}

// Invoke runs the function synchronously (serial: one in flight at a time).
func (r *Runner) Invoke(ctx context.Context, payload []byte) (Result, error) {
	// Warm or cold has to be decided BEFORE ensureStarted, because that is the
	// call that spawns the process — after it, every invocation looks started.
	r.mu.Lock()
	cold := !r.started
	r.mu.Unlock()

	if err := r.ensureStarted(); err != nil {
		return Result{}, err
	}
	inv := &invocation{
		id:         newID(),
		payload:    payload,
		done:       make(chan Result, 1),
		dispatched: make(chan struct{}),
		cold:       cold,
		queuedAt:   time.Now(),
	}
	select {
	case r.queue <- inv:
	case <-ctx.Done():
		return Result{}, ctx.Err()
	}

	// Waiting happens in two phases, because the function's timeout must not be
	// spent on getting the function ready.
	//
	// A single timer started here charges the child's cold start — process
	// spawn, interpreter boot, the runtime client connecting back — and any
	// wait behind another invocation to the function's own budget. On a loaded
	// machine that alone can exceed a default 3s timeout, which is what made
	// TestDeployedAPIInvokesLambda fail under a full test run and pass on its
	// own. AWS does not bill or bound init that way, and neither should this.
	select {
	case res := <-inv.done:
		return res, nil // finished, or reap failed it, before we even waited
	case <-inv.dispatched:
		// The function has the work. Now its clock is the one that matters.
	case <-time.After(initBudget):
		return Result{FunctionErr: "Unhandled",
			Payload: []byte(`{"errorMessage":"Task timed out during init"}`)}, nil
	case <-ctx.Done():
		return Result{}, ctx.Err()
	}

	// The grace second lets the child report its own timeout first, which
	// carries the function's stack rather than this generic message.
	select {
	case res := <-inv.done:
		return r.withTiming(res, inv), nil
	case <-time.After(r.spec.Timeout + graceAfterDeadline):
		return r.withTiming(Result{FunctionErr: "Unhandled",
			Payload: []byte(`{"errorMessage":"Task timed out"}`)}, inv), nil
	case <-ctx.Done():
		return Result{}, ctx.Err()
	}
}

// withTiming fills in the init/exec split. It is only ever called after
// inv.dispatched has been observed closed, so startedAt is published.
func (r *Runner) withTiming(res Result, inv *invocation) Result {
	if inv.startedAt.IsZero() {
		return res // never dispatched: nothing meaningful to split
	}
	res.Exec = time.Since(inv.startedAt)
	if inv.cold {
		res.Init = inv.startedAt.Sub(inv.queuedAt)
	}
	r.report(res, inv)
	res.Logs = r.logTail.snapshot()
	return res
}

// report closes the invocation in the log stream the way AWS does.
//
// Max Memory Used is deliberately absent rather than invented: the runner does
// not measure the child's RSS, and a plausible-looking number nobody computed
// is worse than a missing field — someone would size a function from it.
func (r *Runner) report(res Result, inv *invocation) {
	ms := float64(res.Exec) / float64(time.Millisecond)
	billed := int64(math.Ceil(ms))
	var b strings.Builder
	fmt.Fprintf(&b, "END RequestId: %s\n", inv.id)
	fmt.Fprintf(&b, "REPORT RequestId: %s\tDuration: %.2f ms\tBilled Duration: %d ms\tMemory Size: %d MB",
		inv.id, ms, billed, r.memoryMB())
	if res.Init > 0 {
		fmt.Fprintf(&b, "\tInit Duration: %.2f ms", float64(res.Init)/float64(time.Millisecond))
	}
	b.WriteString("\n")
	_, _ = r.logTail.Write([]byte(b.String()))
}

// memoryMB is the function's configured size, or Lambda's default when unset.
func (r *Runner) memoryMB() int {
	if r.spec.MemorySize > 0 {
		return r.spec.MemorySize
	}
	return defaultMemoryMB
}

// ensureStarted lazily binds the listener and spawns the child.
func (r *Runner) ensureStarted() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	// A stopped runner must not respawn — otherwise a Stop that races an Invoke
	// leaves an orphaned process nothing will ever reap.
	if r.stopped {
		return ErrPoolClosed
	}
	if r.started {
		return nil
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	r.ln = ln
	go http.Serve(ln, r.routes()) //nolint:errcheck // stops when ln closes

	cmd, err := r.buildCommand(ln.Addr().String())
	if err != nil {
		ln.Close()
		return err
	}
	r.cmd = cmd
	if err := cmd.Start(); err != nil {
		ln.Close()
		return fmt.Errorf("start function process: %w", err)
	}
	r.started = true
	go r.reap()
	return nil
}

// buildCommand resolves the runtime into an exec.Cmd with the Lambda env.
func (r *Runner) buildCommand(runtimeAPI string) (*exec.Cmd, error) {
	// Copy the command: pooled Runners share one Spec, and the argv[0] rewrite
	// below must not mutate that shared slice's backing array.
	argv := append([]string(nil), r.spec.Command...)
	if len(argv) == 0 {
		mapped, err := runtimeCommand(r.spec.Runtime, r.spec.Handler)
		if err != nil {
			return nil, err
		}
		argv = mapped
	}
	// Resolve a relative bootstrap/binary against the code dir so the child's
	// working directory can't affect whether it's found.
	if r.spec.Dir != "" && (strings.HasPrefix(argv[0], "./") || !strings.ContainsRune(argv[0], os.PathSeparator) && fileExists(filepath.Join(r.spec.Dir, argv[0]))) {
		argv[0] = filepath.Join(r.spec.Dir, strings.TrimPrefix(argv[0], "./"))
	}
	// ...and then make it ABSOLUTE, because cmd.Dir is set below and Go
	// resolves a relative argv[0] against cmd.Dir rather than the parent's
	// working directory — which would look for the binary inside its own
	// directory twice over. This bites whenever the data dir is relative (the
	// default is ./data) and the code came from a zip rather than an in-place
	// path, i.e. every function deployed by `sam deploy` or `cdk deploy`.
	if !filepath.IsAbs(argv[0]) && strings.ContainsRune(argv[0], os.PathSeparator) {
		if abs, err := filepath.Abs(argv[0]); err == nil {
			argv[0] = abs
		}
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = r.spec.Dir
	env := os.Environ()
	env = append(env,
		"AWS_LAMBDA_RUNTIME_API="+runtimeAPI,
		"_HANDLER="+r.spec.Handler,
		"AWS_LAMBDA_FUNCTION_NAME="+r.spec.Name,
		"AWS_LAMBDA_FUNCTION_VERSION=$LATEST",
		"AWS_LAMBDA_FUNCTION_MEMORY_SIZE="+strconv.Itoa(r.memoryMB()),
		"AWS_REGION=us-east-1",
		"AWS_DEFAULT_REGION=us-east-1",
		"AWS_ACCESS_KEY_ID=test",
		"AWS_SECRET_ACCESS_KEY=test",
	)
	for k, v := range r.spec.Endpoints {
		env = append(env, k+"="+v)
	}
	for k, v := range r.spec.Env {
		env = append(env, k+"="+v)
	}
	cmd.Env = env
	cmd.Stdout = r.logTail
	cmd.Stderr = r.logTail
	return cmd, nil
}

// runtimeCommand maps a runtime identifier to a launch command.
func runtimeCommand(runtime, handler string) ([]string, error) {
	switch {
	case runtime == "" || runtime == "go" || strings.HasPrefix(runtime, "provided"):
		// provided.* and Go run a self-contained bootstrap/binary.
		bin := handler
		if bin == "" {
			bin = "bootstrap"
		}
		return []string{"./" + strings.TrimPrefix(bin, "./")}, nil
	case strings.HasPrefix(runtime, "python"):
		return []string{"python3", "-m", "awslambdaric", handler}, nil
	case strings.HasPrefix(runtime, "nodejs"):
		return []string{"npx", "--yes", "aws-lambda-ric", handler}, nil
	case strings.HasPrefix(runtime, "java"):
		// aws-lambda-java-runtime-interface-client: entrypoint class reads the
		// handler ("package.Class::method") from argv.
		return []string{"java", "-cp", "./*:.", "com.amazonaws.services.lambda.runtime.api.client.AWSLambda", handler}, nil
	case strings.HasPrefix(runtime, "ruby"):
		return []string{"aws_lambda_ric", handler}, nil
	case strings.HasPrefix(runtime, "dotnet"):
		// .NET RIC (Amazon.Lambda.RuntimeSupport) reads "Assembly::Type::Method".
		return []string{"dotnet", "exec", "/opt/aws-lambda-ric.dll", handler}, nil
	}
	return nil, fmt.Errorf("unsupported runtime %q (use provided.*, go, python3.x, nodejs*, java*, ruby*, dotnet*, or set an explicit command)", runtime)
}

// routes serves the Runtime API.
func (r *Runner) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/2018-06-01/runtime/invocation/next", r.handleNext)
	mux.HandleFunc("/2018-06-01/runtime/invocation/", r.handleInvocationResult)
	mux.HandleFunc("/2018-06-01/runtime/init/error", r.handleInitError)
	return mux
}

// handleNext blocks until an invocation is queued, then hands it to the runtime.
func (r *Runner) handleNext(w http.ResponseWriter, req *http.Request) {
	var inv *invocation
	select {
	case inv = <-r.queue:
	case <-req.Context().Done():
		// The child process's long-poll connection dropped (it was stopped) —
		// return instead of blocking this goroutine forever on the queue.
		return
	}
	r.mu.Lock()
	r.current = inv
	r.pending[inv.id] = inv
	// The timeout clock starts HERE — the moment the function fetches the work
	// — not when Invoke queued it. What came before is cold start and queue
	// wait, which are the platform's time, not the function's. Setting it here
	// also means the deadline the child computes from the header below is the
	// same one Invoke enforces, instead of one already partly spent.
	inv.deadline = time.Now().Add(r.spec.Timeout)
	inv.startedAt = inv.deadline.Add(-r.spec.Timeout)
	deadline := inv.deadline
	r.mu.Unlock()
	// Closing after the write publishes startedAt to whoever observes the
	// close — that happens-before is what makes reading it in Invoke safe.
	close(inv.dispatched)

	// Real Lambda brackets every invocation in its log stream. doze-aws emitted
	// none of it, so `aws lambda invoke --log-type Tail` came back with only
	// whatever the handler printed, and anything parsing REPORT found nothing.
	fmt.Fprintf(r.logTail, "START RequestId: %s Version: $LATEST\n", inv.id)

	w.Header().Set("Lambda-Runtime-Aws-Request-Id", inv.id)
	w.Header().Set("Lambda-Runtime-Deadline-Ms", fmt.Sprintf("%d", deadline.UnixMilli()))
	w.Header().Set("Lambda-Runtime-Invoked-Function-Arn",
		"arn:aws:lambda:us-east-1:000000000000:function:"+r.spec.Name)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)
	w.Write(inv.payload)
}

// handleInvocationResult routes /{id}/response and /{id}/error.
func (r *Runner) handleInvocationResult(w http.ResponseWriter, req *http.Request) {
	path := strings.TrimPrefix(req.URL.Path, "/2018-06-01/runtime/invocation/")
	id, kind, ok := strings.Cut(path, "/")
	if !ok {
		w.WriteHeader(400)
		return
	}
	body, _ := io.ReadAll(io.LimitReader(req.Body, 8<<20))
	r.mu.Lock()
	inv := r.pending[id]
	delete(r.pending, id)
	r.mu.Unlock()
	if inv == nil {
		w.WriteHeader(400)
		return
	}
	res := Result{Payload: body, Logs: r.logTail.snapshot()}
	if kind == "error" {
		res.FunctionErr = "Unhandled"
	}
	inv.done <- res
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(202)
	w.Write([]byte(`{"status":"OK"}`))
}

func (r *Runner) handleInitError(w http.ResponseWriter, req *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(req.Body, 1<<20))
	r.logf("lambda %s: init error: %s", r.spec.Name, body)
	w.WriteHeader(202)
	w.Write([]byte(`{"status":"OK"}`))
}

// reap waits for the process to exit and fails any in-flight invocation.
func (r *Runner) reap() {
	err := r.cmd.Wait()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.started = false
	if r.stopped {
		return
	}
	msg := "the function process exited"
	if err != nil {
		msg = fmt.Sprintf("the function process exited: %v", err)
	}
	r.logf("lambda %s: %s", r.spec.Name, msg)
	// A process that dies before it ever fetches work — a missing runtime
	// interface client, a handler that will not import — leaves the invocation
	// sitting in the QUEUE rather than in pending. Failing only the pending set
	// meant those callers waited out the whole timeout and were told "Task
	// timed out", which hides the actual cause. Fail both.
	fail := Result{
		FunctionErr: "Unhandled",
		Payload:     mustJSON(map[string]string{"errorMessage": msg, "errorType": "Runtime.ExitError"}),
		Logs:        r.logTail.snapshot(),
	}
	for id, inv := range r.pending {
		inv.done <- fail
		delete(r.pending, id)
	}
	for {
		select {
		case inv := <-r.queue:
			inv.done <- fail
		default:
			return
		}
	}
}

// Stop terminates the process and listener.
func (r *Runner) Stop() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stopped = true
	if r.cmd != nil && r.cmd.Process != nil {
		_ = r.cmd.Process.Kill()
	}
	if r.ln != nil {
		_ = r.ln.Close()
	}
}

func mustJSON(v any) []byte {
	raw, _ := json.Marshal(v)
	return raw
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
