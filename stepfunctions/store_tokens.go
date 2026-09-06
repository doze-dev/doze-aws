package stepfunctions

// Task tokens: one row per outstanding token, written and deleted in the
// same transaction as the execution record (SaveTransition), because the two
// travel together — a PARKED frame with no redeemable token is a hung
// execution, and a redeemable token whose result was already applied is a
// double delivery.

var bucketTokens = []byte("tokens")

// TokenRef locates the frame a token redeems.
type TokenRef struct {
	ExecKey  string `json:"exec_key"`
	Frame    int    `json:"frame"`
	IssuedAt int64  `json:"issued_at"`
	// Queue is the activityqueue key holding this token's unclaimed task,
	// "" for a .waitForTaskToken task or once a worker has claimed it. It
	// is what lets a timeout that kills the token also drop the queue entry
	// in the same transaction, so a worker never receives a dead task.
	Queue string `json:"queue,omitempty"`
	// TimedOut, when set, makes the row a tombstone: the task this token
	// belonged to hit its TimeoutSeconds or HeartbeatSeconds, and a late
	// SendTask* is answered TaskTimedOut rather than TaskDoesNotExist — the
	// distinction AWS keeps, and the one a worker's error handling branches
	// on. Tombstones are swept after tokenTombstoneTTL.
	TimedOut int64 `json:"timed_out,omitempty"`
}

// tokenOp is one buffered token write: Ref nil means delete. Task, when set,
// also enqueues the activity task the token belongs to. TimedOut turns the
// delete into a tombstone.
type tokenOp struct {
	Token    string
	Ref      *TokenRef
	Task     *ActivityTask
	TimedOut bool
}

// tokenTombstoneTTL is how long a timed-out token keeps answering
// TaskTimedOut. A day covers any worker that comes back late from a real
// outage; after that the row is gone like any other spent token.
const tokenTombstoneTTL = int64(24 * 60 * 60 * 1000)

// GetToken resolves a token, nil when unknown (never issued, already
// redeemed, or timed out).
func (s *Store) GetToken(token string) (*TokenRef, error) {
	var ref TokenRef
	found, err := s.get(bucketTokens, []byte(token), &ref)
	if err != nil || !found {
		return nil, err
	}
	return &ref, nil
}
