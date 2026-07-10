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
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Spec describes how to run one function.
type Spec struct {
	Name      string
	Handler   string
	Runtime   string            // provided.*, go, python3.x, nodejs*
	Command   []string          // explicit command (doze extension) — wins over Runtime mapping
	Dir       string            // working directory
	Env       map[string]string // function environment
	Timeout   time.Duration
	Endpoints map[string]string // AWS_ENDPOINT_URL_* injected so handlers reach sibling services
}

// Result is one invocation's outcome.
type Result struct {
	Payload     []byte
	FunctionErr string // non-empty on a handler error (X-Amz-Function-Error)
	Logs        []byte // tail of stdout/stderr
}

// invocation is queued work for the runtime loop.
type invocation struct {
	id       string
	payload  []byte
	deadline time.Time
	done     chan Result
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
		spec.Timeout = 3 * time.Second
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
	if err := r.ensureStarted(); err != nil {
		return Result{}, err
	}
	inv := &invocation{
		id:       newID(),
		payload:  payload,
		deadline: time.Now().Add(r.spec.Timeout),
		done:     make(chan Result, 1),
	}
	select {
	case r.queue <- inv:
	case <-ctx.Done():
		return Result{}, ctx.Err()
	}
	select {
	case res := <-inv.done:
		return res, nil
	case <-time.After(r.spec.Timeout + time.Second):
		return Result{FunctionErr: "Unhandled", Payload: []byte(`{"errorMessage":"Task timed out"}`)}, nil
	case <-ctx.Done():
		return Result{}, ctx.Err()
	}
}

// ensureStarted lazily binds the listener and spawns the child.
func (r *Runner) ensureStarted() error {
	r.mu.Lock()
	defer r.mu.Unlock()
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
	argv := r.spec.Command
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
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = r.spec.Dir
	env := os.Environ()
	env = append(env,
		"AWS_LAMBDA_RUNTIME_API="+runtimeAPI,
		"_HANDLER="+r.spec.Handler,
		"AWS_LAMBDA_FUNCTION_NAME="+r.spec.Name,
		"AWS_LAMBDA_FUNCTION_VERSION=$LATEST",
		"AWS_LAMBDA_FUNCTION_MEMORY_SIZE=512",
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
	}
	return nil, fmt.Errorf("unsupported runtime %q (use provided.*, python3.x, nodejs*, or set an explicit command)", runtime)
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
	inv := <-r.queue
	r.mu.Lock()
	r.current = inv
	r.pending[inv.id] = inv
	r.mu.Unlock()

	w.Header().Set("Lambda-Runtime-Aws-Request-Id", inv.id)
	w.Header().Set("Lambda-Runtime-Deadline-Ms", fmt.Sprintf("%d", inv.deadline.UnixMilli()))
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
	// Fail any invocation the dead process was handling.
	for id, inv := range r.pending {
		inv.done <- Result{
			FunctionErr: "Unhandled",
			Payload:     mustJSON(map[string]string{"errorMessage": msg, "errorType": "Runtime.ExitError"}),
			Logs:        r.logTail.snapshot(),
		}
		delete(r.pending, id)
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
