package cloudformation

// The stack store: stacks, change sets and cross-stack exports.
//
// The insight that makes this small is that `sam deploy` and `cdk deploy` do
// not need CloudFormation's semantics — no drift detection, no resource-level
// rollback, no asynchronous convergence. They need its API surface and a stack
// that reaches a terminal status. So every operation here is synchronous:
// CreateStack transpiles, applies, records what happened and returns
// CREATE_COMPLETE. The events those tools poll for are synthesized afterwards
// from what apply actually did, which makes them true rather than theatrical.

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/doze-dev/doze-aws/awsident"
)

var (
	stackBucket     = []byte("stacks")
	changeSetBucket = []byte("changesets")
)

// Stack lifecycle statuses. Only terminal ones are ever stored: nothing here
// is asynchronous, so an IN_PROGRESS stack would be a lie.
const (
	StatusCreateComplete   = "CREATE_COMPLETE"
	StatusUpdateComplete   = "UPDATE_COMPLETE"
	StatusCreateFailed     = "CREATE_FAILED"
	StatusUpdateFailed     = "UPDATE_ROLLBACK_COMPLETE"
	StatusDeleteComplete   = "DELETE_COMPLETE"
	StatusRollbackComplete = "ROLLBACK_COMPLETE"
	// StatusReviewInProgress is the one non-terminal status that must exist:
	// real CloudFormation materialises a stack the moment a CREATE change set
	// is made, and deploy tools poll its events before executing. Without it,
	// `sam deploy` fails on DescribeStackEvents between the two calls.
	StatusReviewInProgress = "REVIEW_IN_PROGRESS"
)

// StackRecord is a deployed stack.
type StackRecord struct {
	Name         string            `json:"name"`
	ID           string            `json:"id"`
	Status       string            `json:"status"`
	StatusReason string            `json:"status_reason,omitempty"`
	TemplateBody string            `json:"template"`
	Parameters   map[string]string `json:"parameters,omitempty"`
	Outputs      []StackOutput     `json:"outputs,omitempty"`
	Resources    []StackResource   `json:"resources,omitempty"`
	Events       []StackEvent      `json:"events,omitempty"`
	Tags         map[string]string `json:"tags,omitempty"`
	Created      int64             `json:"created"`
	Updated      int64             `json:"updated"`
	// TerminationProtection and Policy round-trip; nothing local enforces them.
	TerminationProtection bool   `json:"termination_protection,omitempty"`
	Policy                string `json:"policy,omitempty"`
	// Capabilities, NotificationARNs, RollbackConfiguration and DisableRollback
	// are declared on every real deploy — sam and cdk always pass capabilities,
	// and Terraform tracks all four on aws_cloudformation_stack. Nothing local
	// acts on them, but a stack that accepts them and then describes itself
	// without them is a stack Terraform will keep planning to change.
	Capabilities     []string        `json:"capabilities,omitempty"`
	NotificationARNs []string        `json:"notification_arns,omitempty"`
	Rollback         *RollbackConfig `json:"rollback,omitempty"`
	DisableRollback  bool            `json:"disable_rollback,omitempty"`
}

// RollbackConfig is the alarm-watch configuration a stack declares. Nothing
// local watches alarms; it is kept so a describe reports what was set.
type RollbackConfig struct {
	MonitoringTimeInMinutes *int              `json:"monitoring_minutes,omitempty"`
	Triggers                []RollbackTrigger `json:"triggers,omitempty"`
}

// RollbackTrigger is one alarm a rollback configuration watches.
type RollbackTrigger struct {
	Arn  string `json:"arn"`
	Type string `json:"type"`
}

// StackOutput is one output, with its export name when it declares one.
type StackOutput struct {
	Key         string `json:"key"`
	Value       string `json:"value"`
	Description string `json:"description,omitempty"`
	ExportName  string `json:"export,omitempty"`
}

// StackResource is one resource a stack owns. PhysicalID is the name doze-aws
// gave it, which is what DeleteStack needs to reclaim it.
type StackResource struct {
	LogicalID  string `json:"logical_id"`
	Type       string `json:"type"`
	PhysicalID string `json:"physical_id"`
	Status     string `json:"status"`
	Reason     string `json:"reason,omitempty"`
}

// StackEvent is one synthesized progress event. Deploy tools poll these until
// a terminal stack-level status appears, so the final event must always be one.
type StackEvent struct {
	ID         string `json:"id"`
	Timestamp  int64  `json:"ts"`
	LogicalID  string `json:"logical_id"`
	Type       string `json:"type"`
	PhysicalID string `json:"physical_id,omitempty"`
	Status     string `json:"status"`
	Reason     string `json:"reason,omitempty"`
}

// ChangeSetRecord is a pending change set.
type ChangeSetRecord struct {
	Name            string            `json:"name"`
	ID              string            `json:"id"`
	StackName       string            `json:"stack_name"`
	Status          string            `json:"status"`
	StatusReason    string            `json:"status_reason,omitempty"`
	ExecutionStatus string            `json:"execution_status"`
	TemplateBody    string            `json:"template"`
	Parameters      map[string]string `json:"parameters,omitempty"`
	Type            string            `json:"type"` // CREATE | UPDATE
	Changes         []Change          `json:"changes,omitempty"`
	Tags            map[string]string `json:"tags,omitempty"`
	Created         int64             `json:"created"`
}

// Change is one entry of a change set.
type Change struct {
	Action      string `json:"action"` // Add | Modify | Remove
	LogicalID   string `json:"logical_id"`
	Type        string `json:"type"`
	PhysicalID  string `json:"physical_id,omitempty"`
	Replacement string `json:"replacement,omitempty"`
}

// Store is the bbolt-backed CloudFormation state.
type Store struct {
	db    *bolt.DB
	clock func() time.Time
}

func newStore(db *bolt.DB) *Store { return &Store{db: db, clock: time.Now} }

func (s *Store) now() time.Time { return s.clock() }

// StackARN builds the ARN a stack reports as its StackId.
func StackARN(name, id string) string {
	return awsident.ARN("cloudformation", "stack/"+name+"/"+id)
}

// ---- stacks ----

// GetStack resolves a stack by name or by StackId.
//
// The distinction matters after deletion. Real CloudFormation keeps a deleted
// stack queryable BY ID with status DELETE_COMPLETE, while a lookup by NAME
// reports it gone — and deploy tools depend on exactly that: `cdk destroy`
// polls DescribeStacks by id and fails unless it sees DELETE_COMPLETE.
func (s *Store) GetStack(name string) (*StackRecord, error) {
	byID := false
	if i := strings.Index(name, ":stack/"); i >= 0 {
		byID = true
		rest := name[i+len(":stack/"):]
		name, _, _ = strings.Cut(rest, "/")
	}
	var out *StackRecord
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(stackBucket)
		if b == nil {
			return errStackNotFound(name)
		}
		raw := b.Get([]byte(name))
		if raw == nil {
			return errStackNotFound(name)
		}
		var st StackRecord
		if err := json.Unmarshal(raw, &st); err != nil {
			return err
		}
		if st.Status == StatusDeleteComplete && !byID {
			return errStackNotFound(name)
		}
		out = &st
		return nil
	})
	return out, err
}

func (s *Store) PutStack(st *StackRecord) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists(stackBucket)
		if err != nil {
			return err
		}
		raw, err := json.Marshal(st)
		if err != nil {
			return err
		}
		return b.Put([]byte(st.Name), raw)
	})
}

func (s *Store) DeleteStack(name string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(stackBucket)
		if b == nil {
			return nil
		}
		return b.Delete([]byte(name))
	})
}

func (s *Store) ListStacks() ([]StackRecord, error) {
	var out []StackRecord
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(stackBucket)
		if b == nil {
			return nil
		}
		return b.ForEach(func(_, raw []byte) error {
			var st StackRecord
			if json.Unmarshal(raw, &st) == nil {
				out = append(out, st)
			}
			return nil
		})
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, err
}

// Exports collects every exported output across all stacks — the registry
// Fn::ImportValue resolves against, which is what makes a multi-stack CDK app
// work rather than failing on the first cross-stack reference.
func (s *Store) Exports() (map[string]string, error) {
	out := map[string]string{}
	stacks, err := s.ListStacks()
	if err != nil {
		return nil, err
	}
	for _, st := range stacks {
		for _, o := range st.Outputs {
			if o.ExportName != "" {
				out[o.ExportName] = o.Value
			}
		}
	}
	return out, nil
}

// ExportOwner reports which stack exports a name, for the conflict check on
// create and the dependency check on delete.
func (s *Store) ExportOwner(export string) (string, bool) {
	stacks, _ := s.ListStacks()
	for _, st := range stacks {
		for _, o := range st.Outputs {
			if o.ExportName == export {
				return st.Name, true
			}
		}
	}
	return "", false
}

// ---- change sets ----

func changeSetKey(stack, name string) string { return stack + "\x00" + name }

func (s *Store) PutChangeSet(cs *ChangeSetRecord) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists(changeSetBucket)
		if err != nil {
			return err
		}
		raw, err := json.Marshal(cs)
		if err != nil {
			return err
		}
		return b.Put([]byte(changeSetKey(cs.StackName, cs.Name)), raw)
	})
}

// GetChangeSet resolves a change set by (stack, name) or by its ARN-shaped id,
// both of which deploy tools use interchangeably.
func (s *Store) GetChangeSet(stack, nameOrID string) (*ChangeSetRecord, error) {
	var out *ChangeSetRecord
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(changeSetBucket)
		if b == nil {
			return errChangeSetNotFound(nameOrID)
		}
		if stack != "" {
			if raw := b.Get([]byte(changeSetKey(stack, nameOrID))); raw != nil {
				var cs ChangeSetRecord
				if err := json.Unmarshal(raw, &cs); err != nil {
					return err
				}
				out = &cs
				return nil
			}
		}
		// Fall back to an id scan.
		return b.ForEach(func(_, raw []byte) error {
			var cs ChangeSetRecord
			if json.Unmarshal(raw, &cs) != nil {
				return nil
			}
			if cs.ID == nameOrID || (stack == "" && cs.Name == nameOrID) {
				out = &cs
			}
			return nil
		})
	})
	if err == nil && out == nil {
		return nil, errChangeSetNotFound(nameOrID)
	}
	return out, err
}

func (s *Store) DeleteChangeSet(stack, name string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(changeSetBucket)
		if b == nil {
			return nil
		}
		return b.Delete([]byte(changeSetKey(stack, name)))
	})
}

func (s *Store) ListChangeSets(stack string) ([]ChangeSetRecord, error) {
	var out []ChangeSetRecord
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(changeSetBucket)
		if b == nil {
			return nil
		}
		prefix := []byte(stack + "\x00")
		c := b.Cursor()
		for k, v := c.Seek(prefix); k != nil && strings.HasPrefix(string(k), string(prefix)); k, v = c.Next() {
			var cs ChangeSetRecord
			if json.Unmarshal(v, &cs) == nil {
				out = append(out, cs)
			}
		}
		return nil
	})
	return out, err
}

// DeleteStackChangeSets drops every change set belonging to a stack.
func (s *Store) DeleteStackChangeSets(stack string) error {
	sets, err := s.ListChangeSets(stack)
	if err != nil {
		return err
	}
	for _, cs := range sets {
		if err := s.DeleteChangeSet(stack, cs.Name); err != nil {
			return err
		}
	}
	return nil
}

// ---- ids ----

// newID generates the UUID-shaped identifier CloudFormation puts in stack and
// change-set ARNs.
func (s *Store) newID() string {
	// A monotonic clock-derived id keeps ordering stable and avoids pulling in
	// randomness that would make tests non-deterministic.
	n := s.now().UnixNano()
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		uint32(n>>32), uint16(n>>16), uint16(n), uint16(n>>48), uint64(n)&0xffffffffffff)
}
