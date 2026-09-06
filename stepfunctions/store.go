package stepfunctions

import (
	"encoding/json"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/doze-dev/doze-aws/internal/awshttp"
)

// The bbolt store: buckets, keys, and the records they hold.
//
// Split from the per-aggregate accessors (store_machines.go and, in later
// stages, store_executions.go and store_tokens.go) so this file stays the one
// place the schema is described. Anyone asking "what is on disk" reads this and
// nothing else.

var (
	bucketMachines   = []byte("machines")
	bucketActivities = []byte("activities")
	bucketTags       = []byte("tags")
	// Versions and aliases: layout documented in store_versions.go.
	bucketVersions = []byte("versions")
	bucketAliases  = []byte("aliases")
)

// StateMachine is a stored state machine.
//
// Definition is kept as the exact text the caller supplied, not a re-serialised
// AST. DescribeStateMachine must return what was sent — CDK and Terraform both
// diff the definition they get back against the one they hold, and a
// round-trip that reorders keys or drops a comment shows as permanent drift.
type StateMachine struct {
	Name       string `json:"name"`
	ARN        string `json:"arn"`
	Definition string `json:"definition"`
	RoleARN    string `json:"role_arn"`
	Type       string `json:"type"` // STANDARD | EXPRESS
	Status     string `json:"status"`
	CreatedAt  int64  `json:"created_at"`
	RevisionID string `json:"revision_id"`

	// Logging, Tracing and Encryption are stored and returned verbatim without
	// having any local effect. There is no CloudWatch Logs or X-Ray here, but
	// Terraform's aws_sfn_state_machine tracks all three, so dropping them
	// would show as drift on every plan. Recorded as Tier C in the docs rather
	// than silently discarded.
	LoggingConfiguration    json.RawMessage `json:"logging,omitempty"`
	TracingConfiguration    json.RawMessage `json:"tracing,omitempty"`
	EncryptionConfiguration json.RawMessage `json:"encryption,omitempty"`
}

// Activity is a stored activity — a task an external worker polls for.
type Activity struct {
	Name      string `json:"name"`
	ARN       string `json:"arn"`
	CreatedAt int64  `json:"created_at"`
}

// Store is the persistence layer.
type Store struct {
	db    *bolt.DB
	clock func() time.Time
	// vol holds Express and TestState executions, which never reach bbolt.
	vol *volatile
}

func newStore(db *bolt.DB) *Store {
	return &Store{db: db, clock: time.Now, vol: newVolatile()}
}

func (s *Store) now() int64 { return s.clock().UnixMilli() }

// get reads one JSON record. found is false when the key is absent, which the
// caller turns into the service's own not-found error — the store does not know
// which of StateMachineDoesNotExist or ActivityDoesNotExist applies.
func (s *Store) get(bucket, key []byte, dst any) (found bool, err error) {
	err = s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucket)
		if b == nil {
			return nil
		}
		raw := b.Get(key)
		if raw == nil {
			return nil
		}
		found = true
		return json.Unmarshal(raw, dst)
	})
	return found, err
}

func (s *Store) put(bucket, key []byte, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists(bucket)
		if err != nil {
			return err
		}
		return b.Put(key, raw)
	})
}

func (s *Store) delete(bucket, key []byte) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucket)
		if b == nil {
			return nil
		}
		return b.Delete(key)
	})
}

// each walks a bucket in key order. bbolt cursors are already sorted, which is
// what makes the List* operations return a stable order for free.
func (s *Store) each(bucket []byte, fn func(key, raw []byte) error) error {
	return s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucket)
		if b == nil {
			return nil
		}
		return b.ForEach(fn)
	})
}

// Tags are keyed by resource ARN and shared by every taggable resource type,
// because ListTagsForResource takes an ARN and does not care what it points at.
func (s *Store) tags(arn string) (map[string]string, error) {
	out := map[string]string{}
	if _, err := s.get(bucketTags, []byte(arn), &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) setTags(arn string, tags map[string]string) error {
	return s.put(bucketTags, []byte(arn), tags)
}

// asAPIError coerces a store error, so a raw bbolt failure surfaces as an
// opaque InternalFailure rather than leaking the storage layer.
func asAPIError(err error) *awshttp.APIError {
	return awshttp.AsAPIErrorOrNil(err)
}
