package stepfunctions

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/doze-dev/doze-aws/internal/asl"
	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/awsjson"
)

// Activities: Task states whose Resource is an activity ARN. The engine calls
// nothing — it mints a token, parks the frame exactly as .waitForTaskToken
// does, and queues {token, input} for a worker to collect with
// GetActivityTask. The result comes back through SendTaskSuccess/Failure and
// the existing token path, so Retry, Catch, ResultSelector, ResultPath,
// TimeoutSeconds and HeartbeatSeconds all behave as they do for any other
// parked task; only the history event family (Activity*) is its own.
//
// The long-poll is served on the request goroutine, never the driver's: the
// handler claims from the bbolt queue and, when it is empty, waits on the
// activity's doorbell until the driver rings it, the poll deadline passes,
// the request is cancelled, or the engine closes.

// activityPollTimeout is how long GetActivityTask waits for a task before
// answering an empty response — AWS holds the connection for 60 seconds and
// then returns a 200 with a null taskToken. Tests shorten it.
var activityPollTimeout = 60 * time.Second

// activityHub is the doorbell: one broadcast channel per activity name,
// closed and replaced when a task is queued. A poller fetches the channel
// BEFORE it checks the queue, so a signal between its empty claim and its
// wait still wakes it — the same generation trick as a broadcast Cond, but
// selectable against a context and the engine's stop.
type activityHub struct {
	mu   sync.Mutex
	gens map[string]chan struct{}
}

func (h *activityHub) wait(name string) <-chan struct{} {
	h.mu.Lock()
	defer h.mu.Unlock()
	ch, ok := h.gens[name]
	if !ok {
		ch = make(chan struct{})
		h.gens[name] = ch
	}
	return ch
}

// signal wakes every poller of name. An activity nobody is polling has no
// channel and nothing to do, so the map only ever holds live waits.
func (h *activityHub) signal(name string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if ch, ok := h.gens[name]; ok {
		close(ch)
		delete(h.gens, name)
	}
}

// activityNameOf pulls the name out of an activity ARN.
func activityNameOf(resource string) string {
	_, name, _ := strings.Cut(resource, ":activity:")
	return name
}

// scheduleActivity handles EffCallTask for an activity resource, on the
// driver. The token row and the queue entry are written by the same
// SaveTransition that persists the PARKED frame, so a restart can never find
// a parked frame whose task no worker will ever see.
func (g *engine) scheduleActivity(r *run, call asl.EffCallTask) {
	f := r.e.Exec.Frame(call.Frame)
	name := activityNameOf(call.Resource)
	a, aerr := g.srv.store.GetActivity(name)
	if aerr != nil || a == nil {
		// AWS records ActivityScheduleFailed and fails the state; the frame
		// is already PARKED with a token, and Deliver unwinds that the same
		// way a timeout does. activityFailureEvent maps this failure onto
		// the ScheduleFailed event.
		g.deliver(r, f, failResult(asl.ErrRuntime,
			fmt.Sprintf("the activity %s does not exist", call.Resource)))
		return
	}
	details := map[string]any{
		"resource":     call.Resource,
		"input":        string(call.Input),
		"inputDetails": notTruncated(),
	}
	if s := r.stateOf(f, f.State); s != nil {
		if s.TimeoutSecondsState != nil && *s.TimeoutSecondsState > 0 {
			details["timeoutInSeconds"] = int64(*s.TimeoutSecondsState)
		}
		if s.HeartbeatSeconds != nil && *s.HeartbeatSeconds > 0 {
			details["heartbeatInSeconds"] = int64(*s.HeartbeatSeconds)
		}
	}
	g.event(r, f, "ActivityScheduled", "activityScheduledEventDetails", details)
	now := g.srv.store.now()
	r.tokens = append(r.tokens, tokenOp{
		Token: call.Token,
		Ref:   &TokenRef{ExecKey: r.key, Frame: call.Frame, IssuedAt: now},
		Task: &ActivityTask{
			Activity: name, Token: call.Token, Input: string(call.Input),
			ExecKey: r.key, Frame: call.Frame, ScheduledAt: now,
		},
	})
	g.persist(r)
	g.activities.signal(name)
}

// activityStarted records the worker's claim, on the driver. The heartbeat
// clock is deliberately NOT reset: AWS counts HeartbeatSeconds from
// ActivityScheduled, so a task nobody picks up in time dies of it.
func (g *engine) activityStarted(r *run, frame int, worker string) {
	f := r.e.Exec.Frame(frame)
	if f == nil || f.Status != asl.FrameParked {
		return
	}
	details := map[string]any{}
	if worker != "" {
		details["workerName"] = worker
	}
	g.event(r, f, "ActivityStarted", "activityStartedEventDetails", details)
	g.persist(r)
}

func (g *engine) activitySucceededEvent(r *run, f *asl.Frame, output []byte) {
	g.event(r, f, "ActivitySucceeded", "activitySucceededEventDetails", map[string]any{
		"output": string(output), "outputDetails": notTruncated(),
	})
}

// activityFailureEvent picks the Activity* event for a failure. States.Runtime
// can only reach an activity frame from scheduling — a worker reports its own
// errors by name — so it is the ScheduleFailed event.
func (g *engine) activityFailureEvent(r *run, f *asl.Frame, errName, cause string) {
	typ, key := "ActivityFailed", "activityFailedEventDetails"
	switch errName {
	case asl.ErrTimeout, asl.ErrHeartbeatTimeout:
		typ, key = "ActivityTimedOut", "activityTimedOutEventDetails"
	case asl.ErrRuntime:
		typ, key = "ActivityScheduleFailed", "activityScheduleFailedEventDetails"
	}
	g.event(r, f, typ, key, map[string]any{"error": errName, "cause": cause})
}

// awaitActivityTask claims the next task of name, long-polling until one is
// queued or the deadline, the request or the engine ends. nil, nil is the
// empty poll.
func (g *engine) awaitActivityTask(ctx context.Context, name string) (*ActivityTask, error) {
	timer := time.NewTimer(activityPollTimeout)
	defer timer.Stop()
	for {
		select {
		case <-g.stop:
			// Close is under way; the store is about to go with it.
			return nil, nil
		default:
		}
		wake := g.activities.wait(name)
		task, err := g.srv.store.ClaimActivityTask(name)
		if err != nil || task != nil {
			return task, err
		}
		select {
		case <-wake:
		case <-timer.C:
			return nil, nil
		case <-ctx.Done():
			return nil, nil
		case <-g.stop:
			return nil, nil
		}
	}
}

// getActivityTask is GetActivityTask: FIFO per activity, at most one worker
// receives each task, and an empty poll answers an empty structure — the
// SDK's typed output then has a nil TaskToken, which is what workers loop on.
func (s *Server) getActivityTask(ctx context.Context, p map[string]any) (any, *awshttp.APIError) {
	arn := awsjson.Str(p, "activityArn")
	if arn == "" {
		return nil, errValidation("1 validation error detected: Value at 'activityArn' failed to satisfy constraint: Member must not be null")
	}
	name := nameFromARN(arn, "activity")
	if name == "" {
		return nil, errInvalidARN(arn)
	}
	worker := awsjson.Str(p, "workerName")
	if len(worker) > 80 {
		return nil, errValidation("1 validation error detected: Value at 'workerName' failed to satisfy constraint: Member must have length less than or equal to 80")
	}
	a, aerr := s.store.GetActivity(name)
	if aerr != nil {
		return nil, aerr
	}
	if a == nil {
		return nil, errActivityNotFound(arn)
	}
	task, err := s.engine.awaitActivityTask(ctx, name)
	if err != nil {
		return nil, asAPIError(err)
	}
	if task == nil {
		return map[string]any{}, nil
	}
	s.engine.deliverActivityStarted(task, worker)
	return map[string]any{"taskToken": task.Token, "input": task.Input}, nil
}

// deliverActivityStarted tells the driver a worker took the task;
// fire-and-forget, like a heartbeat. It is queued before anything the worker
// can send back, so ActivityStarted always precedes ActivitySucceeded.
func (g *engine) deliverActivityStarted(task *ActivityTask, worker string) {
	select {
	case g.deliveries <- delivery{key: task.ExecKey, frame: task.Frame, kind: dlvActivityStarted, worker: worker}:
	case <-g.stop:
	}
}
