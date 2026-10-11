package sqs

// Message move tasks, asynchronously.
//
// A task is started by StartMessageMoveTask and answered at once with its
// handle; the messages then move in the background at the rate asked for, the
// task is visible as RUNNING with a count of what has moved so far, and
// CancelMessageMoveTask stops it. That is the AWS shape (doc: the model's
// StartMessageMoveTask: "an asynchronous task"; ListMessageMoveTasks: statuses
// RUNNING, COMPLETED, CANCELLING, CANCELLED and FAILED, and a TaskHandle only
// "for tasks that are in RUNNING status"; "only one active message movement task
// is supported per queue at any given time").
//
// Why it is not instant. A move that finished inside the call let a test assert
// the moved messages straight after the start and pass here, where AWS answers
// RUNNING and the messages are still on their way. So the task waits a moment
// before it begins and then moves at the stated rate. The pacing is the
// emulator's own: AWS "optimizes the rate based on the queue message backlog",
// and says nothing locally checkable, so the defaults below are a choice.
//
// What is remembered. A running task lives in memory (a goroutine and a record)
// and a finished one is written to the store, as before. A restart mid-move
// loses the running task: the messages already moved stay moved and the rest
// stay in the dead-letter queue, so starting it again is safe. Nothing runs
// until a task is started, so an idle process pays nothing for this.

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	bolt "go.etcd.io/bbolt"
)

// The pacing of a move. Variables, not constants, so a test can make it quick.
var (
	// moveStartDelay is how long a task is RUNNING before its first message moves.
	moveStartDelay = 500 * time.Millisecond
	// moveDefaultRate is messages per second when none was asked for. 500 is
	// also the most a task may ask for (doc: the model).
	moveDefaultRate = 500
	// moveTick is how often a running task moves a chunk.
	moveTick = 100 * time.Millisecond
)

const (
	moveRunning   = "RUNNING"
	moveCompleted = "COMPLETED"
	moveFailed    = "FAILED"
	moveCancelled = "CANCELLED"
)

// moveTasks tracks the tasks that are running. The zero value is ready.
type moveTasks struct {
	mu       sync.Mutex
	active   map[string]*runningMove // by source queue name
	byHandle map[string]*runningMove
	closed   bool
	wg       sync.WaitGroup
}

type runningMove struct {
	task      moveTask // Moved and Status are read and written under the tracker's lock
	cancel    chan struct{}
	cancelled bool
	done      chan struct{}
}

// snapshot is the task as of now.
func (t *moveTasks) snapshot(rm *runningMove) moveTask {
	t.mu.Lock()
	defer t.mu.Unlock()
	return rm.task
}

// runningFor is the task running against source, if any.
func (t *moveTasks) runningFor(source string) (moveTask, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if rm := t.active[source]; rm != nil {
		return rm.task, true
	}
	return moveTask{}, false
}

// stop cancels every running task and waits for them, for Close.
func (t *moveTasks) stop() {
	t.mu.Lock()
	t.closed = true
	for _, rm := range t.active {
		if !rm.cancelled {
			rm.cancelled = true
			close(rm.cancel)
		}
	}
	t.mu.Unlock()
	t.wg.Wait()
}

// StartMessageMoveTask starts moving what is stored in source, in the
// background, and returns the task as RUNNING.
//
// With a destination, everything goes there. Without one, each message goes
// back to the queue it was dead-lettered from (its DeadLetterQueueSourceArn),
// and a message with no origin, or whose origin queue is gone, stays where it
// is and fails the task. The source has to be some queue's dead-letter queue.
//
// doc: the StartMessageMoveTask API reference.
func (s *store) StartMessageMoveTask(source, dest string, perSecond int) (*moveTask, error) {
	task := moveTask{
		Handle:       newID(),
		Status:       moveRunning,
		Source:       source,
		Destination:  dest,
		MaxPerSecond: perSecond,
		StartedAt:    s.now().Unix(),
	}
	err := s.db.View(func(tx *bolt.Tx) error {
		if _, err := s.getQueue(tx, source); err != nil {
			return err
		}
		if dest != "" {
			if _, err := s.getQueue(tx, dest); err != nil {
				return err
			}
		}
		if !s.isDeadLetterQueue(tx, source) {
			return errInvalid("Value for parameter SourceArn is invalid. Reason: only the ARN of a dead-letter queue is accepted as the source of a move task.")
		}
		if src := tx.Bucket(msgBucket(source)); src != nil {
			task.ToMove = src.Stats().KeyN
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	t := &s.moves
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil, errInvalid("The service is shutting down.")
	}
	if cur := t.active[source]; cur != nil && cur.task.Status == moveRunning {
		// doc: "Only one active message movement task is supported per queue at
		// any given time." The code AWS refuses with is not established.
		return nil, errInvalid("A message movement task is already running for this queue.")
	}
	if t.active == nil {
		t.active, t.byHandle = map[string]*runningMove{}, map[string]*runningMove{}
	}
	rm := &runningMove{task: task, cancel: make(chan struct{}), done: make(chan struct{})}
	t.active[source] = rm
	t.byHandle[task.Handle] = rm
	t.wg.Add(1)
	go s.runMove(rm)
	return &task, nil
}

// CancelMessageMoveTask stops a running task and returns how many messages it
// had moved. A task that is not running (finished, or never existed) is
// refused: "A message movement can only be cancelled when the current status is
// RUNNING."
func (s *store) CancelMessageMoveTask(handle string) (int, error) {
	t := &s.moves
	t.mu.Lock()
	rm := t.byHandle[handle]
	if rm == nil || rm.task.Status != moveRunning {
		t.mu.Unlock()
		return 0, &apiError{Code: "ResourceNotFoundException", Status: 400, SenderFault: true,
			Message: "The task handle does not name a message movement task that is running."}
	}
	if !rm.cancelled {
		rm.cancelled = true
		close(rm.cancel)
	}
	t.mu.Unlock()
	<-rm.done
	return t.snapshot(rm).Moved, nil
}

// runMove is one task's goroutine.
func (s *store) runMove(rm *runningMove) {
	t := &s.moves
	defer t.wg.Done()
	defer close(rm.done)

	rate := rm.task.MaxPerSecond
	if rate <= 0 {
		rate = moveDefaultRate
	}
	// Credit accrues at the rate and whole messages are spent from it, so a
	// rate of 1 a second moves one a second, not one a tick.
	credit := 0.0

	finish := func(status, reason string) {
		// In this order, so a listing never finds the task in neither place:
		// the status changes (the task is still listed, no longer RUNNING),
		// then the record is written, then the task leaves the running set.
		t.mu.Lock()
		rm.task.Status = status
		rm.task.FailureReason = reason
		final := rm.task
		t.mu.Unlock()
		// A closing store may already be gone; the record is best effort then.
		_ = s.db.Update(func(tx *bolt.Tx) error { return s.recordMoveTask(tx, &final) })
		t.mu.Lock()
		if t.active[final.Source] == rm { // a new task may have taken its place
			delete(t.active, final.Source)
		}
		delete(t.byHandle, final.Handle)
		t.mu.Unlock()
	}

	if moveStartDelay > 0 {
		select {
		case <-time.After(moveStartDelay):
		case <-rm.cancel:
			finish(moveCancelled, "")
			return
		}
	}
	tick := time.NewTicker(moveTick)
	defer tick.Stop()
	for {
		select {
		case <-rm.cancel:
			finish(moveCancelled, "")
			return
		case <-tick.C:
		}
		credit += float64(rate) * moveTick.Seconds()
		perTick := int(credit)
		if perTick < 1 {
			continue
		}
		credit -= float64(perTick)
		moved, unplaced, err := s.moveChunk(rm.task.Source, rm.task.Destination, perTick)
		if err != nil {
			finish(moveFailed, err.Error())
			return
		}
		t.mu.Lock()
		rm.task.Moved += moved
		t.mu.Unlock()
		if moved == 0 {
			// Nothing more can move. What is left, if anything, had nowhere to go.
			if unplaced > 0 {
				finish(moveFailed, fmt.Sprintf("%d message(s) have no source queue to return to; they were left in the dead-letter queue", unplaced))
			} else {
				finish(moveCompleted, "")
			}
			return
		}
	}
}

// moveChunk moves up to limit messages out of source in one transaction and
// returns how many moved, and how many of the ones it passed had nowhere to go.
// Messages that cannot be placed stay where they are, so a final pass that
// moves none is how a task knows it is done.
func (s *store) moveChunk(source, dest string, limit int) (moved, unplaced int, err error) {
	signalled := map[string]bool{}
	err = s.db.Update(func(tx *bolt.Tx) error {
		if _, err := s.getQueue(tx, source); err != nil {
			return fmt.Errorf("the source queue no longer exists")
		}
		if dest != "" {
			if _, err := s.getQueue(tx, dest); err != nil {
				return fmt.Errorf("the destination queue no longer exists")
			}
		}
		src := tx.Bucket(msgBucket(source))
		if src == nil {
			return nil
		}
		type pending struct {
			key    []byte
			target string
			m      message
		}
		var moves []pending
		_ = src.ForEach(func(k, raw []byte) error {
			if len(moves) >= limit {
				return nil
			}
			var m message
			if json.Unmarshal(raw, &m) != nil {
				return nil
			}
			target := dest
			if target == "" {
				target = arnQueueName(m.SourceArn)
			}
			if target == "" {
				unplaced++
				return nil
			}
			if _, err := s.getQueue(tx, target); err != nil {
				unplaced++ // the queue it came from has been deleted
				return nil
			}
			moves = append(moves, pending{key: append([]byte(nil), k...), target: target, m: m})
			return nil
		})
		for _, p := range moves {
			dst, err := tx.CreateBucketIfNotExists(msgBucket(p.target))
			if err != nil {
				return err
			}
			seq, _ := dst.NextSequence()
			m := p.m
			m.Seq = seq
			m.ReceiveCount = 0
			m.VisibleAt = s.now().UnixNano()
			// It is back where it came from: no longer a dead letter.
			m.SourceArn = ""
			if err := putMessage(dst, &m); err != nil {
				return err
			}
			_ = src.Delete(p.key)
			signalled[p.target] = true
		}
		moved = len(moves)
		return nil
	})
	if err == nil {
		for q := range signalled {
			s.notify.signal(q)
		}
	}
	return moved, unplaced, err
}
