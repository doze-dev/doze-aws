package cloudformation

// A stack never takes over a resource it did not make.
//
// provision.Apply converges: a queue that already exists under a name the
// template uses is adopted and brought in line. A stackfile wants that.
// CloudFormation does not — it owns what it creates, deletes it with the
// stack, and rolls it back on failure — so AWS fails the create of a named
// resource whose name is taken, and the stack rolls back without touching the
// one that was there. Adopting it here would let a template pass locally that
// fails on the first real deploy, and a later delete-stack would destroy
// something the stack never owned.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/doze-dev/doze-aws/internal/cfn"
	"github.com/doze-dev/doze-aws/internal/provision"
)

// errTaken is the create a name collision fails, worded as CloudFormation
// words it for every resource type its registry handlers own.
type errTaken struct {
	typ, name string
}

func (e *errTaken) Error() string {
	return fmt.Sprintf("Resource of type '%s' with identifier '%s' already exists.", e.typ, e.name)
}

// refuseTaken returns the first resource sf would create whose name is
// already in use outside this stack, or nil. What the stack already holds —
// its recorded resources, and everything its previous template made — is its
// own to update, never a collision.
func (s *Server) refuseTaken(ctx context.Context, sf *provision.Stack, prev *stackRecord, prevIR *provision.Stack, rep *cfn.Report) error {
	owned := map[string]bool{}
	if prev != nil && prevIR != nil {
		for _, r := range prev.Resources {
			owned[r.PhysicalID] = true
		}
		for n := range provision.Names(prevIR) {
			owned[n[strings.Index(n, "/")+1:]] = true
		}
	}
	fresh := provision.Subset(sf, func(_, name string) bool { return !owned[name] })
	taken, err := provision.Existing(ctx, s.gateway, s.id, fresh)
	if err != nil {
		return err
	}
	if len(taken) == 0 {
		return nil
	}
	name := taken[0][strings.Index(taken[0], "/")+1:]
	typ := "AWS::CloudFormation::Resource"
	for _, e := range rep.Entries {
		if e.Kind == cfn.Mapped && e.Name == name {
			typ = e.Type
			break
		}
	}
	return &errTaken{typ: typ, name: name}
}

// takenName is the physical name a collision names, or "".
func takenName(err error) string {
	var t *errTaken
	if errors.As(err, &t) {
		return t.name
	}
	return ""
}
