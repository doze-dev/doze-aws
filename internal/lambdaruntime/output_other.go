//go:build !unix

package lambdaruntime

import "os/exec"

// Where the pipe cannot be read without blocking, output goes through os/exec
// as it always did, and drain has nothing to do. See output_unix.go for what
// that costs: a function's last lines may trail its END.
type outputPump struct{}

func newOutputPump(cmd *exec.Cmd, out *lineSplitter) (*outputPump, error) {
	cmd.Stdout, cmd.Stderr = out, out
	return &outputPump{}, nil
}

func (p *outputPump) started() {}
func (p *outputPump) drain()   {}
func (p *outputPump) close()   {}
