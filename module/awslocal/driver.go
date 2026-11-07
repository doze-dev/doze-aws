package awslocal

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"

	"github.com/doze-dev/doze-sdk/engine"
)

// BaseDriver implements the boilerplate shared by every local-AWS engine: there
// is no toolchain to download (the service is built into doze), so Resolve is
// synthetic and Spawn re-executes the doze binary as `doze __serve <Name>`,
// which runs the service (registered via RegisterServer) on a unix socket.
// Concrete engines embed it and add their own ConfigDecoder/Converger.
type BaseDriver struct {
	Name        string // engine type, __serve key, and socket basename: "s3"
	EndpointEnv string // SDK endpoint var, e.g. "AWS_ENDPOINT_URL_S3"
	// ChildEnv, if set, returns extra environment variables for the spawned
	// service process — e.g. SNS passing the backend socket of the SQS instance
	// it fans out to.
	ChildEnv func(inst engine.Instance) []string
}

// Type implements engine.Driver.
func (d BaseDriver) Type() string { return d.Name }

// Versionless implements engine.Versionless: these services ship inside doze, so
// instances need no `version`.
func (d BaseDriver) Versionless() {}

// Resolve implements engine.Driver: the service ships inside doze, so there is
// nothing to fetch — return a synthetic toolchain.
func (d BaseDriver) Resolve(_ context.Context, _ engine.VersionSpec, _ engine.Platform, _ engine.Locker, _ engine.Fetcher) (engine.Toolchain, error) {
	return engine.Toolchain{Engine: d.Name, Full: "builtin"}, nil
}

// Provision implements engine.Driver: just the data directory.
func (d BaseDriver) Provision(_ context.Context, inst engine.Instance, _ engine.Toolchain) error {
	return os.MkdirAll(inst.DataDir, 0o700)
}

// Provisioned implements engine.Driver.
func (d BaseDriver) Provisioned(dataDir string) bool {
	fi, err := os.Stat(dataDir)
	return err == nil && fi.IsDir()
}

// Plan implements engine.Spawner: a one-spec SpawnPlan core supervises, gated on
// the service's unix socket (the listener binds only after the handler is built,
// so an accepting socket means ready). The spawned binary is this process's own
// executable re-run as `__serve <name>` — the doze binary in-tree, or the engine's
// plugin binary out-of-process (which dispatches __serve to awslocal.ServeFromArgs).
func (d BaseDriver) Plan(_ context.Context, inst engine.Instance, _ engine.Toolchain) (engine.SpawnPlan, error) {
	if err := os.MkdirAll(inst.SocketDir, 0o700); err != nil {
		return engine.SpawnPlan{}, fmt.Errorf("creating socket dir: %w", err)
	}
	socket := d.socket(inst.SocketDir)
	_ = os.Remove(socket) // clear any stale socket from a crash
	self, err := os.Executable()
	if err != nil {
		return engine.SpawnPlan{}, fmt.Errorf("locating service binary: %w", err)
	}
	env := os.Environ()
	if d.ChildEnv != nil {
		if extra := d.ChildEnv(inst); len(extra) > 0 {
			env = append(env, extra...)
		}
	}
	args := []string{"__serve", d.Name, "--socket", socket, "--datadir", inst.DataDir}
	// The name doze gave the instance (engine.Networked): the service answers
	// under it and mints its URLs with it.
	if inst.Grant.Named() {
		args = append(args, "--suffix", inst.Grant.Host)
		if l, ok := inst.Grant.Listeners[engine.MainListener]; ok && l.URL != "" {
			args = append(args, "--endpoint", l.URL)
		}
	}
	return engine.SpawnPlan{Specs: []engine.SpawnSpec{{
		Name:  inst.Name,
		Bin:   self,
		Args:  args,
		Env:   env,
		Ready: &engine.Ready{Kind: "socket", Target: socket},
	}}}, nil
}

// BackendSocket implements engine.Driver.
func (d BaseDriver) BackendSocket(socketDir string, _ int) string { return d.socket(socketDir) }

func (d BaseDriver) socket(socketDir string) string {
	return filepath.Join(socketDir, d.Name+".sock")
}

// ConnString implements engine.Driver: the SDK endpoint URL for this service:
// the instance's name without a port when doze gave it one, otherwise the
// doze-owned TCP endpoint.
func (d BaseDriver) ConnString(inst engine.Instance, ep engine.Endpoint) (string, string) {
	if l, ok := inst.Grant.Listeners[engine.MainListener]; ok && l.URL != "" {
		return d.EndpointEnv, l.URL
	}
	return d.EndpointEnv, "http://" + clientHost(ep)
}

// Network implements engine.Networked: one HTTP listener on the declared port,
// answering at the instance's name and every name under it (the AWS-shaped
// hosts: sqs.<region>.<name>, <bucket>.s3.<region>.<name>) and at
// sync-<name>, where every SDK sends Step Functions' StartSyncExecution and
// TestState.
func (d BaseDriver) Network(engine.Instance) engine.Network {
	return engine.Network{Listeners: []engine.Listener{
		{Name: engine.MainListener, Protocol: engine.ProtocolHTTP, Routes: []string{engine.RouteSelf, "sync-@"}},
	}}
}

// clientHost returns the host:port an SDK uses to reach this instance. The AWS
// services need a TCP endpoint (an http URL); a unix-only endpoint can't be
// expressed as one, so fall back to localhost as a best effort.
func clientHost(ep engine.Endpoint) string {
	if ep.TCPAddr != "" {
		return ep.TCPAddr
	}
	return "127.0.0.1"
}

// UnixHTTPClient returns an http.Client that dials the given unix socket for
// every request (the URL host is ignored). Drivers use it to reach a service's
// backend socket — e.g. a Converger creating declared buckets/queues/topics.
func UnixHTTPClient(socket string) *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", socket)
			},
		},
	}
}
