package stepfunctions

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	bolt "go.etcd.io/bbolt"

	"github.com/doze-dev/doze-aws/internal/awshttp"
)

// Version and alias persistence.
//
// bucketVersions holds two kinds of key. `<machine>\x00%010d` is version N's
// snapshot, so a machine's versions are a prefix cursor scan in number order.
// The bare `<machine>` key is the machine's version counter — the highest
// number ever handed out, kept apart from the snapshots because AWS numbers
// versions monotonically: delete version 3 and the next publish is 4, not 3
// again. A name cannot contain "\x00", so the bare key never falls inside
// another machine's prefix.
//
// bucketAliases is `<machine>\x00<alias>` — the same prefix shape, and bbolt's
// key order is the ascending-by-name order ListStateMachineAliases documents.

// Version is a published snapshot of a state machine — what StartExecution
// on a version ARN runs no matter how the machine changes afterwards. It
// carries everything DescribeStateMachine answers for a version ARN, config
// blocks included, so the describe does not have to reach back to the
// (possibly changed) machine.
type Version struct {
	MachineName string `json:"machine_name"`
	Number      int    `json:"number"`
	ARN         string `json:"arn"`
	Definition  string `json:"definition"`
	RoleARN     string `json:"role_arn"`
	Type        string `json:"type"`
	RevisionID  string `json:"revision_id"`
	Description string `json:"description,omitempty"`
	CreatedAt   int64  `json:"created_at"`

	LoggingConfiguration    json.RawMessage `json:"logging,omitempty"`
	TracingConfiguration    json.RawMessage `json:"tracing,omitempty"`
	EncryptionConfiguration json.RawMessage `json:"encryption,omitempty"`
}

// Alias is a named pointer at one or two versions of the same machine, with
// the traffic split StartExecution honours.
type Alias struct {
	MachineName string  `json:"machine_name"`
	Name        string  `json:"name"`
	ARN         string  `json:"arn"`
	Description string  `json:"description,omitempty"`
	Routing     []Route `json:"routing"`
	CreatedAt   int64   `json:"created_at"`
	UpdatedAt   int64   `json:"updated_at"`
}

// Route is one routingConfiguration entry.
type Route struct {
	VersionARN string `json:"version_arn"`
	Weight     int    `json:"weight"`
}

func versionKey(machine string, n int) []byte {
	return []byte(fmt.Sprintf("%s\x00%010d", machine, n))
}

func aliasKey(machine, alias string) []byte { return []byte(machine + "\x00" + alias) }

// versionsIn reads every version of one machine inside tx, ascending.
func versionsIn(tx *bolt.Tx, machine string) ([]Version, error) {
	b := tx.Bucket(bucketVersions)
	if b == nil {
		return nil, nil
	}
	prefix := []byte(machine + "\x00")
	var out []Version
	c := b.Cursor()
	for k, raw := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, raw = c.Next() {
		var v Version
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// aliasesIn reads every alias of one machine inside tx, in name order.
func aliasesIn(tx *bolt.Tx, machine string) ([]Alias, error) {
	b := tx.Bucket(bucketAliases)
	if b == nil {
		return nil, nil
	}
	prefix := []byte(machine + "\x00")
	var out []Alias
	c := b.Cursor()
	for k, raw := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, raw = c.Next() {
		var a Alias
		if err := json.Unmarshal(raw, &a); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

// PublishVersion snapshots m as its next version, or answers the newest
// existing version when that already holds m's revision. AWS documents
// PublishStateMachineVersion as idempotent on the current revision: a CDK
// deploy that changed nothing publishes nothing. Counter bump and snapshot
// land in one transaction so a crash cannot hand out a number twice.
func (s *Store) PublishVersion(m *StateMachine, description string) (*Version, *awshttp.APIError) {
	var out Version
	err := s.db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists(bucketVersions)
		if err != nil {
			return err
		}
		existing, err := versionsIn(tx, m.Name)
		if err != nil {
			return err
		}
		if n := len(existing); n > 0 && existing[n-1].RevisionID == m.RevisionID {
			out = existing[n-1]
			return nil
		}
		next := 1
		if raw := b.Get([]byte(m.Name)); raw != nil {
			var last int
			if err := json.Unmarshal(raw, &last); err != nil {
				return err
			}
			next = last + 1
		}
		out = Version{
			MachineName: m.Name, Number: next, ARN: versionARN(s.id, m.Name, next),
			Definition: m.Definition, RoleARN: m.RoleARN, Type: m.Type,
			RevisionID: m.RevisionID, Description: description, CreatedAt: s.now(),
			LoggingConfiguration:    m.LoggingConfiguration,
			TracingConfiguration:    m.TracingConfiguration,
			EncryptionConfiguration: m.EncryptionConfiguration,
		}
		raw, err := json.Marshal(out)
		if err != nil {
			return err
		}
		if err := b.Put(versionKey(m.Name, next), raw); err != nil {
			return err
		}
		counter, _ := json.Marshal(next)
		return b.Put([]byte(m.Name), counter)
	})
	if err != nil {
		return nil, asAPIError(err)
	}
	return &out, nil
}

// GetVersion reads one version, nil when absent.
func (s *Store) GetVersion(machine string, n int) (*Version, *awshttp.APIError) {
	var v Version
	found, err := s.get(bucketVersions, versionKey(machine, n), &v)
	if err != nil {
		return nil, asAPIError(err)
	}
	if !found {
		return nil, nil
	}
	return &v, nil
}

// ListVersions returns a machine's versions, ascending by number.
func (s *Store) ListVersions(machine string) ([]Version, *awshttp.APIError) {
	var out []Version
	err := s.db.View(func(tx *bolt.Tx) (err error) {
		out, err = versionsIn(tx, machine)
		return err
	})
	if err != nil {
		return nil, asAPIError(err)
	}
	return out, nil
}

// DeleteVersion removes one version, refusing while an alias still routes to
// it — AWS's rule, and the one that stops an alias from pointing into
// nothing. Deleting an absent version succeeds; the operation's model lists
// no not-found error.
func (s *Store) DeleteVersion(machine string, n int) *awshttp.APIError {
	arn := versionARN(s.id, machine, n)
	return asAPIError(s.db.Update(func(tx *bolt.Tx) error {
		aliases, err := aliasesIn(tx, machine)
		if err != nil {
			return err
		}
		var holders []string
		for _, a := range aliases {
			for _, r := range a.Routing {
				if r.VersionARN == arn {
					holders = append(holders, a.ARN)
				}
			}
		}
		if len(holders) > 0 {
			return errConflict("Version to be deleted must not be referenced by an alias. "+
				"Current list of aliases referencing this version: [%s]", strings.Join(holders, ", "))
		}
		b := tx.Bucket(bucketVersions)
		if b == nil {
			return nil
		}
		return b.Delete(versionKey(machine, n))
	}))
}

// PutAlias stores an alias. AWS makes CreateStateMachineAlias idempotent on
// name, description and routing: the same request again answers the
// existing alias, anything else on a taken name is ConflictException.
func (s *Store) PutAlias(a *Alias) (*Alias, *awshttp.APIError) {
	var existing Alias
	found, err := s.get(bucketAliases, aliasKey(a.MachineName, a.Name), &existing)
	if err != nil {
		return nil, asAPIError(err)
	}
	if found {
		if existing.Description == a.Description && sameRouting(existing.Routing, a.Routing) {
			return &existing, nil
		}
		return nil, errConflict("Alias already exists: '%s'", existing.ARN)
	}
	a.CreatedAt = s.now()
	a.UpdatedAt = a.CreatedAt
	if err := s.put(bucketAliases, aliasKey(a.MachineName, a.Name), a); err != nil {
		return nil, asAPIError(err)
	}
	return a, nil
}

func sameRouting(a, b []Route) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// GetAlias reads one alias, nil when absent.
func (s *Store) GetAlias(machine, name string) (*Alias, *awshttp.APIError) {
	var a Alias
	found, err := s.get(bucketAliases, aliasKey(machine, name), &a)
	if err != nil {
		return nil, asAPIError(err)
	}
	if !found {
		return nil, nil
	}
	return &a, nil
}

// UpdateAlias applies UpdateStateMachineAlias: a nil description or routing
// is left as it was. Answers nil for an absent alias.
func (s *Store) UpdateAlias(machine, name string, description *string, routing []Route) (*Alias, *awshttp.APIError) {
	a, aerr := s.GetAlias(machine, name)
	if aerr != nil || a == nil {
		return nil, aerr
	}
	if description != nil {
		a.Description = *description
	}
	if routing != nil {
		a.Routing = routing
	}
	a.UpdatedAt = s.now()
	if err := s.put(bucketAliases, aliasKey(machine, name), a); err != nil {
		return nil, asAPIError(err)
	}
	return a, nil
}

// DeleteAlias removes one alias. found reports whether there was one, so the
// handler can answer ResourceNotFound the way the model lists it.
func (s *Store) DeleteAlias(machine, name string) (found bool, aerr *awshttp.APIError) {
	err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketAliases)
		if b == nil {
			return nil
		}
		key := aliasKey(machine, name)
		if b.Get(key) == nil {
			return nil
		}
		found = true
		return b.Delete(key)
	})
	return found, asAPIError(err)
}

// ListAliases returns a machine's aliases in name order.
func (s *Store) ListAliases(machine string) ([]Alias, *awshttp.APIError) {
	var out []Alias
	err := s.db.View(func(tx *bolt.Tx) (err error) {
		out, err = aliasesIn(tx, machine)
		return err
	})
	if err != nil {
		return nil, asAPIError(err)
	}
	return out, nil
}

// DeleteVersionsAndAliases drops everything a machine owns, for
// DeleteStateMachine — AWS deletes a machine's versions and aliases with it,
// and the counter goes too, so a re-created machine numbers from 1.
func (s *Store) DeleteVersionsAndAliases(machine string) *awshttp.APIError {
	return asAPIError(s.db.Update(func(tx *bolt.Tx) error {
		prefix := []byte(machine + "\x00")
		for _, name := range [][]byte{bucketVersions, bucketAliases} {
			b := tx.Bucket(name)
			if b == nil {
				continue
			}
			// Collect first: deleting under a live cursor shifts what Next
			// lands on, and bbolt documents that as undefined.
			var keys [][]byte
			c := b.Cursor()
			for k, _ := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, _ = c.Next() {
				keys = append(keys, append([]byte(nil), k...))
			}
			for _, k := range keys {
				if err := b.Delete(k); err != nil {
					return err
				}
			}
		}
		if b := tx.Bucket(bucketVersions); b != nil {
			return b.Delete([]byte(machine))
		}
		return nil
	}))
}
