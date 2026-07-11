package eventbridge

import (
	"encoding/json"
	"sort"
	"strings"

	bolt "go.etcd.io/bbolt"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/awshttp"
)

var (
	busesBucket = []byte("buses")
	rulesBucket = []byte("rules")
)

// DefaultBus is the implicit bus every account has.
const DefaultBus = "default"

// Bus is a custom event bus.
type Bus struct {
	Name string            `json:"name"`
	Tags map[string]string `json:"tags,omitempty"`
}

// Target is one rule target.
type Target struct {
	ID               string            `json:"id"`
	ARN              string            `json:"arn"`
	Input            string            `json:"input,omitempty"`      // literal input override
	InputPath        string            `json:"input_path,omitempty"` // $.path extraction
	InputTransformer *InputTransformer `json:"input_transformer,omitempty"`
}

// InputTransformer maps event paths into a template.
type InputTransformer struct {
	PathsMap map[string]string `json:"paths_map,omitempty"`
	Template string            `json:"template"`
}

// Rule is one rule on a bus.
type Rule struct {
	Bus      string            `json:"bus"`
	Name     string            `json:"name"`
	Pattern  string            `json:"pattern,omitempty"`  // event pattern JSON
	Schedule string            `json:"schedule,omitempty"` // rate(...) driven by ticker; cron(...) stored only
	State    string            `json:"state"`              // ENABLED | DISABLED
	Desc     string            `json:"desc,omitempty"`
	Targets  []Target          `json:"targets,omitempty"`
	Tags     map[string]string `json:"tags,omitempty"`
}

// ARN returns the rule ARN.
func (r *Rule) ARN() string {
	if r.Bus == DefaultBus {
		return awsident.ARN("events", "rule/"+r.Name)
	}
	return awsident.ARN("events", "rule/"+r.Bus+"/"+r.Name)
}

func busARN(name string) string {
	return awsident.ARN("events", "event-bus/"+name)
}

func ruleKey(bus, name string) []byte {
	return []byte(bus + "\x00" + name)
}

// Store is the bbolt-backed EventBridge state.
type Store struct {
	db *bolt.DB
}

func newStore(db *bolt.DB) *Store { return &Store{db: db} }

func errRuleNotFound(name string) *awshttp.APIError {
	return awshttp.Errf(400, "ResourceNotFoundException", "Rule %s does not exist", name)
}

// busExists treats the default bus as always present.
func (s *Store) busExists(tx *bolt.Tx, name string) bool {
	if name == DefaultBus {
		return true
	}
	b := tx.Bucket(busesBucket)
	return b != nil && b.Get([]byte(name)) != nil
}

// CreateBus registers a custom bus.
func (s *Store) CreateBus(name string, tags map[string]string) error {
	if name == "" || name == DefaultBus || strings.ContainsAny(name, "/\\ ") {
		return awshttp.Errf(400, "ValidationException", "invalid event bus name %q", name)
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists(busesBucket)
		if err != nil {
			return err
		}
		if b.Get([]byte(name)) != nil {
			return awshttp.Errf(400, "ResourceAlreadyExistsException", "event bus %s already exists", name)
		}
		raw, _ := json.Marshal(Bus{Name: name, Tags: tags})
		return b.Put([]byte(name), raw)
	})
}

// DeleteBus removes a custom bus and its rules.
func (s *Store) DeleteBus(name string) error {
	if name == DefaultBus {
		return awshttp.Errf(400, "ValidationException", "the default event bus cannot be deleted")
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		if b := tx.Bucket(busesBucket); b != nil {
			_ = b.Delete([]byte(name))
		}
		if rb := tx.Bucket(rulesBucket); rb != nil {
			prefix := []byte(name + "\x00")
			c := rb.Cursor()
			var stale [][]byte
			for k, _ := c.Seek(prefix); k != nil && strings.HasPrefix(string(k), string(prefix)); k, _ = c.Next() {
				stale = append(stale, append([]byte(nil), k...))
			}
			for _, k := range stale {
				_ = rb.Delete(k)
			}
		}
		return nil
	})
}

// ListBuses returns the default bus plus custom buses, sorted.
func (s *Store) ListBuses() ([]Bus, error) {
	out := []Bus{{Name: DefaultBus}}
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(busesBucket)
		if b == nil {
			return nil
		}
		return b.ForEach(func(_, raw []byte) error {
			var bus Bus
			if json.Unmarshal(raw, &bus) == nil {
				out = append(out, bus)
			}
			return nil
		})
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, err
}

// PutRule creates or updates a rule.
func (s *Store) PutRule(r Rule) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		if !s.busExists(tx, r.Bus) {
			return awshttp.Errf(400, "ResourceNotFoundException", "event bus %s does not exist", r.Bus)
		}
		b, err := tx.CreateBucketIfNotExists(rulesBucket)
		if err != nil {
			return err
		}
		// Preserve existing targets across PutRule updates.
		if raw := b.Get(ruleKey(r.Bus, r.Name)); raw != nil {
			var old Rule
			if json.Unmarshal(raw, &old) == nil {
				r.Targets = old.Targets
				if len(old.Tags) > 0 && r.Tags == nil {
					r.Tags = old.Tags
				}
			}
		}
		raw, _ := json.Marshal(r)
		return b.Put(ruleKey(r.Bus, r.Name), raw)
	})
}

// GetRule loads one rule.
func (s *Store) GetRule(bus, name string) (*Rule, error) {
	var out *Rule
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(rulesBucket)
		if b == nil {
			return errRuleNotFound(name)
		}
		raw := b.Get(ruleKey(bus, name))
		if raw == nil {
			return errRuleNotFound(name)
		}
		var r Rule
		if err := json.Unmarshal(raw, &r); err != nil {
			return err
		}
		out = &r
		return nil
	})
	return out, err
}

// UpdateRule applies fn to a rule.
func (s *Store) UpdateRule(bus, name string, fn func(*Rule) error) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(rulesBucket)
		if b == nil {
			return errRuleNotFound(name)
		}
		raw := b.Get(ruleKey(bus, name))
		if raw == nil {
			return errRuleNotFound(name)
		}
		var r Rule
		if err := json.Unmarshal(raw, &r); err != nil {
			return err
		}
		if err := fn(&r); err != nil {
			return err
		}
		nraw, _ := json.Marshal(r)
		return b.Put(ruleKey(bus, name), nraw)
	})
}

// DeleteRule removes a rule (Force semantics: targets go with it).
func (s *Store) DeleteRule(bus, name string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		if b := tx.Bucket(rulesBucket); b != nil {
			_ = b.Delete(ruleKey(bus, name))
		}
		return nil
	})
}

// Rules lists a bus's rules, optionally by name prefix.
func (s *Store) Rules(bus, prefix string) ([]Rule, error) {
	var out []Rule
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(rulesBucket)
		if b == nil {
			return nil
		}
		busPrefix := []byte(bus + "\x00" + prefix)
		c := b.Cursor()
		for k, raw := c.Seek(busPrefix); k != nil && strings.HasPrefix(string(k), string(busPrefix)); k, raw = c.Next() {
			var r Rule
			if json.Unmarshal(raw, &r) == nil {
				out = append(out, r)
			}
		}
		return nil
	})
	return out, err
}
