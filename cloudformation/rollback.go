package cloudformation

// What happens to a stack when a resource fails.
//
// CloudFormation accepts the call, works, and on a failure undoes what it did:
// a create that fails deletes what it created and leaves the stack in
// ROLLBACK_COMPLETE; an update that fails puts the previous template back and
// leaves it in UPDATE_ROLLBACK_COMPLETE. Deploy tools are written against
// that. They poll the stack, read its events to say what went wrong, and
// expect to find the account as it was.
//
// This used to do none of it. A failing resource made CreateStack itself
// answer 400, left the stack in CREATE_FAILED with every resource reported
// COMPLETE, and left whatever had been created in place — so a stack that
// half-deployed here would, on AWS, have cleaned up after itself, and one
// that failed an update kept the new template it had failed to apply. An
// UpdateStack that dropped a resource from the template left the resource
// running, owned by nothing.
//
// Stacks are still synchronous: the work is finished before the call returns
// (docs/cloudformation.md). What changes is what "finished" means. The call
// answers 200, the rollback has happened, and the event trail tells it in the
// order CloudFormation would.
//
// # What a rollback may delete
//
// Only what the failed apply itself reported creating. A template can name a
// resource that already exists — apply adopts it rather than failing — and a
// rollback that deleted everything the template names would destroy something
// the stack never made. The same care is the reason a stack in
// ROLLBACK_COMPLETE owns nothing: deleting it removes the record and touches
// no resource.
//
// A resource whose DeletionPolicy is Retain is never deleted, by a rollback,
// by an update that drops it, or by deleting the stack.

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/doze-dev/doze-aws/internal/cfn"
	"github.com/doze-dev/doze-aws/internal/provision"
)

// The statuses a failure can end in, beside the ones store.go declares.
const (
	statusRollbackComplete     = "ROLLBACK_COMPLETE"
	statusRollbackFailed       = "ROLLBACK_FAILED"
	statusUpdateRollbackFailed = "UPDATE_ROLLBACK_FAILED"
	statusUpdateFailedKept     = "UPDATE_FAILED"
)

// errPreviousGone is the one way an update cannot be rolled back: the template
// it would go back to no longer transpiles.
var errPreviousGone = errors.New("the previous template can no longer be read")

// failurePolicy is what the caller asked to happen when a resource fails.
type failurePolicy int

const (
	onFailureRollback failurePolicy = iota // the default: undo it
	onFailureKeep                          // DisableRollback, or OnFailure=DO_NOTHING
	onFailureDelete                        // OnFailure=DELETE: undo it and delete the stack
)

func policyOf(p params) failurePolicy {
	switch {
	case p.bool_("DisableRollback"), p.str("OnFailure") == "DO_NOTHING":
		return onFailureKeep
	case p.str("OnFailure") == "DELETE":
		return onFailureDelete
	}
	return onFailureRollback
}

// failure is everything settleFailure needs to know about an apply that
// did not finish.
type failure struct {
	st     *stackRecord     // the stack as the failed deploy would have left it
	prev   *stackRecord     // the stack before, nil on a create
	ir     *provision.Stack // what was being applied
	prevIR *provision.Stack // what was there before, nil when it cannot be re-derived
	rep    *provision.Report
	err    error
	update bool
	policy failurePolicy
}

// made is the part of the applied stack this apply itself brought into being,
// less anything its template says to retain.
//
// "created" and "published" are certain. A few kinds are only ever upserted —
// a rule, an alarm, a dashboard — and report "updated" whether or not they
// existed; those are taken as made, because they hold configuration rather
// than data and a rule left behind would keep firing for a stack that is gone.
func (f failure) made() *provision.Stack {
	upsertOnly := map[string]bool{"rule": true, "alarm": true, "dashboard": true,
		"connection": true, "apidestination": true, "layer": true}
	did := map[string]string{}
	if f.rep != nil {
		for _, a := range f.rep.Actions {
			did[a.Resource] = a.Op
		}
	}
	retain := retained(f.st.TemplateBody, f.st.Resources, true)
	return provision.Subset(f.ir, func(kind, name string) bool {
		if retain[name] {
			return false
		}
		switch did[kind+"/"+name] {
		case "created", "published":
			return true
		case "updated":
			return upsertOnly[kind]
		}
		return false
	})
}

// settleFailure decides how a failed deploy ends, does the undoing, and
// returns the stack record to store. It never fails the call: the call
// succeeded, and the stack did not.
func (s *Server) settleFailure(ctx context.Context, f failure) *stackRecord {
	st := f.st
	verb := statusVerb(f.update)
	touched := touchedBy(f.rep)
	failed := markFailed(st, f.err, f.update)
	reason := "The following resource(s) failed to " + strings.ToLower(verb) + ": [" + strings.Join(failed, ", ") + "]."
	if len(failed) == 0 {
		// The error names no resource this stack declares; say what it said.
		reason = f.err.Error()
	}

	events := []stackEvent{s.stackEvent(st, verb+"_IN_PROGRESS", "User Initiated")}
	var attempted []stackResource
	for _, r := range st.Resources {
		isFailed := strings.HasSuffix(r.Status, "_FAILED")
		if !isFailed && touched[r.PhysicalID] == "" {
			// Never reached. Nothing was started, so there is nothing to
			// report and nothing to undo.
			continue
		}
		attempted = append(attempted, r)
		events = append(events, s.resourceEvent(r, verb+"_IN_PROGRESS", ""), s.resourceEvent(r, r.Status, r.Reason))
	}

	switch {
	case f.policy == onFailureKeep:
		// Left as it fell: what was made stays, and the stack says it failed.
		st.Resources = attempted
		st.Status = pick(f.update, statusUpdateFailedKept, StatusCreateFailed)
		st.StatusReason = reason
		events = append(events, s.stackEvent(st, st.Status, reason))

	case !f.update:
		events = append(events, s.undoCreate(ctx, f, attempted, reason+" Rollback requested by user.")...)

	default:
		// undoUpdate rewrites the record in place with the previous one.
		events = append(events, s.undoUpdate(ctx, f, attempted, reason)...)
	}
	st.Events = boundEvents(append(st.Events, events...))
	return st
}

// undoCreate deletes what a failed create made. The stack ends
// ROLLBACK_COMPLETE, or DELETE_COMPLETE when the caller asked for OnFailure=DELETE.
func (s *Server) undoCreate(ctx context.Context, f failure, attempted []stackResource, reason string) []stackEvent {
	st := f.st
	st.Status, st.StatusReason = "ROLLBACK_IN_PROGRESS", reason
	events := []stackEvent{s.stackEvent(st, st.Status, reason)}

	left := s.destroy(ctx, f.made())
	st.Outputs = nil
	st.Resources = nil
	for _, r := range attempted {
		if why, ok := left[r.PhysicalID]; ok {
			r.Status, r.Reason = "DELETE_FAILED", why
			events = append(events, s.resourceEvent(r, "DELETE_IN_PROGRESS", ""), s.resourceEvent(r, r.Status, why))
		} else {
			if !strings.HasSuffix(r.Status, "_FAILED") {
				events = append(events, s.resourceEvent(r, "DELETE_IN_PROGRESS", ""))
			}
			r.Status, r.Reason = "DELETE_COMPLETE", ""
			events = append(events, s.resourceEvent(r, r.Status, ""))
		}
		st.Resources = append(st.Resources, r)
	}

	switch {
	case len(left) > 0:
		st.Status = statusRollbackFailed
		st.StatusReason = "The following resource(s) failed to delete: [" + strings.Join(logicalIDs(st.Resources, "DELETE_FAILED"), ", ") + "]."
	case f.policy == onFailureDelete:
		events = append(events, s.stackEvent(st, "DELETE_IN_PROGRESS", reason))
		st.Status, st.StatusReason, st.Resources = StatusDeleteComplete, "", nil
	default:
		st.Status = statusRollbackComplete
		st.StatusReason = ""
	}
	return append(events, s.stackEvent(st, st.Status, st.StatusReason))
}

// undoUpdate puts a stack back the way it was before an update that failed:
// what the update created is deleted, what it changed is re-applied from the
// previous template, and the record is the previous record.
func (s *Server) undoUpdate(ctx context.Context, f failure, attempted []stackResource, reason string) []stackEvent {
	st, prev := f.st, f.prev
	events := []stackEvent{s.stackEvent(st, "UPDATE_ROLLBACK_IN_PROGRESS", reason)}

	// Only what is new to the stack. A resource the previous template also
	// had is about to be put back, not deleted.
	before := map[string]bool{}
	if f.prevIR != nil {
		before = provision.Names(f.prevIR)
	}
	isNew := func(kind, name string) bool { return !before[kind+"/"+name] }
	left := s.destroy(ctx, provision.Subset(f.made(), isNew))

	restoreErr := error(nil)
	if f.prevIR == nil {
		restoreErr = errPreviousGone
	} else if _, err := provision.Apply(ctx, s.gateway, f.prevIR, s.id); err != nil {
		restoreErr = err
	}

	was := map[string]bool{}
	for _, r := range prev.Resources {
		was[r.LogicalID] = true
	}
	for _, r := range attempted {
		switch {
		case !was[r.LogicalID]:
			if why, ok := left[r.PhysicalID]; ok {
				events = append(events, s.resourceEvent(r, "DELETE_FAILED", why))
			} else {
				events = append(events, s.resourceEvent(r, "DELETE_COMPLETE", ""))
			}
		case restoreErr == nil:
			events = append(events, s.resourceEvent(r, "UPDATE_COMPLETE", ""))
		}
	}

	// The record is the previous one: its template, parameters, resources and
	// outputs are what is deployed again. Only the trail and the status are new.
	events0 := st.Events
	id, created := st.ID, st.Created
	*st = *prev
	st.ID, st.Created, st.Events, st.Updated = id, created, events0, s.now().Unix()
	if restoreErr != nil || len(left) > 0 {
		st.Status = statusUpdateRollbackFailed
		if restoreErr != nil {
			st.StatusReason = "The previous template could not be re-applied: " + restoreErr.Error()
		} else {
			st.StatusReason = "Resources the update created could not be deleted."
		}
		return append(events, s.stackEvent(st, st.Status, st.StatusReason))
	}
	st.Status, st.StatusReason = StatusUpdateFailed, ""
	return append(events,
		s.stackEvent(st, "UPDATE_ROLLBACK_COMPLETE_CLEANUP_IN_PROGRESS", ""),
		s.stackEvent(st, st.Status, ""))
}

// removeDropped deletes what an update's new template no longer has, and
// returns the events of doing it. It is the half of UpdateStack that was
// missing: a resource taken out of a template stayed running, with no stack
// to own it and nothing left that would ever delete it.
func (s *Server) removeDropped(ctx context.Context, st, prev *stackRecord, ir, prevIR *provision.Stack) []stackEvent {
	if prev == nil || prevIR == nil {
		return nil
	}
	now := provision.Names(ir)
	retain := retained(prev.TemplateBody, prev.Resources, false)
	dropped := provision.Subset(prevIR, func(kind, name string) bool {
		return !now[kind+"/"+name] && !retain[name]
	})
	if len(provision.Names(dropped)) == 0 {
		return nil
	}
	left := s.destroy(ctx, dropped)

	kept := map[string]bool{}
	for _, r := range st.Resources {
		kept[r.LogicalID+"\x00"+r.PhysicalID] = true
	}
	events := []stackEvent{s.stackEvent(st, "UPDATE_COMPLETE_CLEANUP_IN_PROGRESS", "")}
	for _, r := range prev.Resources {
		if kept[r.LogicalID+"\x00"+r.PhysicalID] || retain[r.PhysicalID] {
			continue
		}
		events = append(events, s.resourceEvent(r, "DELETE_IN_PROGRESS", ""))
		if why, ok := left[r.PhysicalID]; ok {
			events = append(events, s.resourceEvent(r, "DELETE_FAILED", why))
		} else {
			events = append(events, s.resourceEvent(r, "DELETE_COMPLETE", ""))
		}
	}
	return events
}

// destroy removes a set of resources and reports the ones it could not, by
// name, with why.
func (s *Server) destroy(ctx context.Context, what *provision.Stack) map[string]string {
	left := map[string]string{}
	if len(provision.Names(what)) == 0 {
		return left
	}
	rep, err := provision.Destroy(ctx, s.gateway, what, s.id)
	if err != nil {
		s.logf("cloudformation: rollback left resources behind: %v", err)
	}
	if rep != nil {
		for _, a := range rep.Failures() {
			if i := strings.Index(a.Resource, "/"); i >= 0 {
				left[a.Resource[i+1:]] = a.Detail
			}
		}
	}
	return left
}

// retained is the physical names of the resources a template says to keep.
// RetainExceptOnCreate keeps a resource everywhere except in the rollback of
// the create that made it, which is the one place createRollback is true.
func retained(templateBody string, resources []stackResource, createRollback bool) map[string]bool {
	out := map[string]bool{}
	tmpl, err := cfn.Parse([]byte(templateBody))
	if err != nil {
		return out
	}
	for _, r := range resources {
		decl := tmpl.Resources[r.LogicalID]
		if decl == nil {
			continue
		}
		switch decl.DeletionPolicy {
		case "Retain":
			out[r.PhysicalID] = true
		case "RetainExceptOnCreate":
			out[r.PhysicalID] = !createRollback
		}
	}
	return out
}

// touchedBy maps each resource name apply reported on to what it did.
func touchedBy(rep *provision.Report) map[string]string {
	out := map[string]string{}
	if rep == nil {
		return out
	}
	for _, a := range rep.Actions {
		if i := strings.Index(a.Resource, "/"); i >= 0 {
			out[a.Resource[i+1:]] = a.Op
		}
	}
	return out
}

func logicalIDs(resources []stackResource, status string) []string {
	var out []string
	for _, r := range resources {
		if r.Status == status {
			out = append(out, r.LogicalID)
		}
	}
	sort.Strings(out)
	return out
}

func (s *Server) stackEvent(st *stackRecord, status, reason string) stackEvent {
	return stackEvent{
		ID: s.store.newID(), Timestamp: s.now().Unix(), TimeMs: s.now().UnixMilli(), LogicalID: st.Name,
		Type: "AWS::CloudFormation::Stack", PhysicalID: st.ID, Status: status, Reason: reason,
	}
}

func (s *Server) resourceEvent(r stackResource, status, reason string) stackEvent {
	return stackEvent{
		ID: s.store.newID(), Timestamp: s.now().Unix(), TimeMs: s.now().UnixMilli(), LogicalID: r.LogicalID,
		Type: r.Type, PhysicalID: r.PhysicalID, Status: status, Reason: reason,
	}
}

// boundEvents keeps the trail to its most recent entries: a stack redeployed
// in a loop should not grow without limit.
func boundEvents(all []stackEvent) []stackEvent {
	const maxEvents = 500
	if len(all) > maxEvents {
		all = all[len(all)-maxEvents:]
	}
	return all
}
