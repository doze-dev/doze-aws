package lambda

import (
	"encoding/json"
	"net/http"
	"sort"

	bolt "go.etcd.io/bbolt"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/awshttp"
)

var (
	funcsBucket    = []byte("functions")
	mappingsBucket = []byte("mappings")
)

// Function is a stored function definition.
type Function struct {
	Name        string            `json:"name"`
	Runtime     string            `json:"runtime"`
	Handler     string            `json:"handler"`
	Role        string            `json:"role,omitempty"`
	Description string            `json:"description,omitempty"`
	Timeout     int               `json:"timeout"` // seconds
	MemorySize  int               `json:"memory_size"`
	Env         map[string]string `json:"env,omitempty"`
	Command     []string          `json:"command,omitempty"` // doze extension

	// Code location: either an unpacked dir under the data dir, or an
	// in-place absolute path (the _local_ bucket convention).
	CodeDir    string `json:"code_dir"`
	CodeSHA256 string `json:"code_sha256,omitempty"`
	Version    string `json:"version"` // "$LATEST" or a published number

	DeadLetterArn       string            `json:"dead_letter_arn,omitempty"`
	Destinations        json.RawMessage   `json:"destinations,omitempty"` // DestinationConfig round-trip
	ReservedConcurrency *int              `json:"reserved_concurrency,omitempty"`
	Tags                map[string]string `json:"tags,omitempty"`
	Layers              []string          `json:"layers,omitempty"`

	// EventInvokeConfig (async): set via PutFunctionEventInvokeConfig; the
	// presence of any of these means an event-invoke config exists.
	MaxRetryAttempts   *int `json:"max_retry_attempts,omitempty"`
	MaxEventAgeSeconds *int `json:"max_event_age_seconds,omitempty"`
	HasEventInvokeCfg  bool `json:"has_event_invoke_cfg,omitempty"`

	Aliases  map[string]string `json:"aliases,omitempty"` // alias -> version
	LastMod  int64             `json:"last_mod"`
	Revision string            `json:"revision"`

	FunctionURL string `json:"function_url,omitempty"` // when a URL config exists
}

// ARN returns the function ARN.
func (f *Function) ARN() string { return awsident.ARN("lambda", "function:"+f.Name) }

// EventSourceMapping is one SQS→function poller definition.
type EventSourceMapping struct {
	UUID           string `json:"uuid"`
	FunctionName   string `json:"function_name"`
	EventSourceArn string `json:"event_source_arn"`
	BatchSize      int    `json:"batch_size"`
	Enabled        bool   `json:"enabled"`
	State          string `json:"state"` // Enabled | Disabled
}

// Store is the bbolt-backed Lambda control-plane state.
type Store struct {
	db *bolt.DB
}

func newStore(db *bolt.DB) *Store { return &Store{db: db} }

func errFuncNotFound(name string) *awshttp.APIError {
	return &awshttp.APIError{Code: "ResourceNotFoundException", Status: 404,
		Message: "Function not found: " + name, SenderFault: true}
}

// PutFunction stores a function.
func (s *Store) PutFunction(f *Function) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists(funcsBucket)
		if err != nil {
			return err
		}
		raw, _ := json.Marshal(f)
		return b.Put([]byte(f.Name), raw)
	})
}

// GetFunction loads a function by name.
func (s *Store) GetFunction(name string) (*Function, error) {
	var out *Function
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(funcsBucket)
		if b == nil {
			return errFuncNotFound(name)
		}
		raw := b.Get([]byte(name))
		if raw == nil {
			return errFuncNotFound(name)
		}
		var f Function
		if err := json.Unmarshal(raw, &f); err != nil {
			return err
		}
		out = &f
		return nil
	})
	return out, err
}

// Update applies fn to a function.
func (s *Store) Update(name string, fn func(*Function) error) (*Function, error) {
	var out *Function
	err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(funcsBucket)
		if b == nil {
			return errFuncNotFound(name)
		}
		raw := b.Get([]byte(name))
		if raw == nil {
			return errFuncNotFound(name)
		}
		var f Function
		if err := json.Unmarshal(raw, &f); err != nil {
			return err
		}
		if err := fn(&f); err != nil {
			return err
		}
		nraw, _ := json.Marshal(f)
		out = &f
		return b.Put([]byte(name), nraw)
	})
	return out, err
}

// DeleteFunction removes a function.
func (s *Store) DeleteFunction(name string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(funcsBucket)
		if b == nil || b.Get([]byte(name)) == nil {
			return errFuncNotFound(name)
		}
		return b.Delete([]byte(name))
	})
}

// ListFunctions returns all functions, sorted by name.
func (s *Store) ListFunctions() ([]Function, error) {
	var out []Function
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(funcsBucket)
		if b == nil {
			return nil
		}
		return b.ForEach(func(_, raw []byte) error {
			var f Function
			if json.Unmarshal(raw, &f) == nil {
				out = append(out, f)
			}
			return nil
		})
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, err
}

// ---- event source mappings ----

func (s *Store) PutMapping(m *EventSourceMapping) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists(mappingsBucket)
		if err != nil {
			return err
		}
		raw, _ := json.Marshal(m)
		return b.Put([]byte(m.UUID), raw)
	})
}

func (s *Store) GetMapping(uuid string) (*EventSourceMapping, error) {
	var out *EventSourceMapping
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(mappingsBucket)
		if b == nil {
			return errMappingNotFound(uuid)
		}
		raw := b.Get([]byte(uuid))
		if raw == nil {
			return errMappingNotFound(uuid)
		}
		var m EventSourceMapping
		if err := json.Unmarshal(raw, &m); err != nil {
			return err
		}
		out = &m
		return nil
	})
	return out, err
}

func (s *Store) DeleteMapping(uuid string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		if b := tx.Bucket(mappingsBucket); b != nil {
			_ = b.Delete([]byte(uuid))
		}
		return nil
	})
}

func (s *Store) ListMappings() ([]EventSourceMapping, error) {
	var out []EventSourceMapping
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(mappingsBucket)
		if b == nil {
			return nil
		}
		return b.ForEach(func(_, raw []byte) error {
			var m EventSourceMapping
			if json.Unmarshal(raw, &m) == nil {
				out = append(out, m)
			}
			return nil
		})
	})
	return out, err
}

func errMappingNotFound(uuid string) *awshttp.APIError {
	return &awshttp.APIError{Code: "ResourceNotFoundException", Status: 404,
		Message: "event source mapping " + uuid + " does not exist", SenderFault: true}
}

// writeError renders Lambda's REST-JSON error shape.
func writeError(w http.ResponseWriter, e *awshttp.APIError) {
	body, _ := json.Marshal(map[string]string{"Type": "User", "message": e.Message, "Message": e.Message})
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("x-amzn-errortype", e.Code)
	w.WriteHeader(e.Status)
	w.Write(body)
}
