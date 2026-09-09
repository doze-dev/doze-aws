package apigateway

// API keys: the control plane. A key is a value a client sends in
// x-api-key; a method that requires one is served only when the key exists,
// is enabled, and belongs to a usage plan covering the API's stage
// (apikeycheck.go). Throttle and quota settings are stored and reported,
// not enforced: nothing is metered locally, which is also why GetUsage is
// refused by name.

import (
	"crypto/rand"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"

	bolt "go.etcd.io/bbolt"

	"github.com/doze-dev/doze-aws/internal/awshttp"
)

var apiKeyBucket = []byte("apikeys")

// APIKey is one key.
type APIKey struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Value       string            `json:"value"`
	Enabled     bool              `json:"enabled"`
	CustomerID  string            `json:"customer_id,omitempty"`
	Created     int64             `json:"created"`
	Updated     int64             `json:"updated"`
	StageKeys   []string          `json:"stage_keys,omitempty"` // "<apiId>/<stage>"
	Tags        map[string]string `json:"tags,omitempty"`
}

func viewAPIKey(k *APIKey, withValue bool) map[string]any {
	v := map[string]any{
		"id": k.ID, "name": k.Name, "enabled": k.Enabled,
		"createdDate": k.Created, "lastUpdatedDate": k.Updated,
		"stageKeys": orEmptyList(k.StageKeys), "tags": orEmptyMap(k.Tags),
	}
	putIfStr(v, "description", k.Description)
	putIfStr(v, "customerId", k.CustomerID)
	if withValue {
		v["value"] = k.Value
	}
	return v
}

// newKeyValue is the 40-character alphanumeric value API Gateway mints.
func newKeyValue() string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	var b [40]byte
	rand.Read(b[:])
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b[:])
}

// ---- store ----

func (s *Store) PutAPIKey(k *APIKey) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists(apiKeyBucket)
		if err != nil {
			return err
		}
		raw, _ := json.Marshal(k)
		return b.Put([]byte(k.ID), raw)
	})
}

func (s *Store) GetAPIKey(id string) (*APIKey, error) {
	var out *APIKey
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(apiKeyBucket)
		if b == nil || b.Get([]byte(id)) == nil {
			return errNotFound("Invalid API Key identifier specified")
		}
		var k APIKey
		if err := json.Unmarshal(b.Get([]byte(id)), &k); err != nil {
			return err
		}
		out = &k
		return nil
	})
	return out, err
}

func (s *Store) DeleteAPIKey(id string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(apiKeyBucket)
		if b == nil || b.Get([]byte(id)) == nil {
			return errNotFound("Invalid API Key identifier specified")
		}
		if err := b.Delete([]byte(id)); err != nil {
			return err
		}
		// The key leaves every plan it was attached to.
		pb := tx.Bucket(usagePlanBucket)
		if pb == nil {
			return nil
		}
		// Collected first: bbolt forbids writing a bucket from inside its
		// own ForEach.
		var rewrite [][2][]byte
		pb.ForEach(func(pk, raw []byte) error {
			var p UsagePlan
			if json.Unmarshal(raw, &p) != nil || !p.hasKey(id) {
				return nil
			}
			p.removeKey(id)
			out, _ := json.Marshal(p)
			rewrite = append(rewrite, [2][]byte{append([]byte(nil), pk...), out})
			return nil
		})
		for _, kv := range rewrite {
			if err := pb.Put(kv[0], kv[1]); err != nil {
				return err
			}
		}
		return nil
	})
}

// ListAPIKeys answers every key, sorted by name.
func (s *Store) ListAPIKeys() ([]*APIKey, error) {
	var out []*APIKey
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(apiKeyBucket)
		if b == nil {
			return nil
		}
		return b.ForEach(func(_, raw []byte) error {
			var k APIKey
			if err := json.Unmarshal(raw, &k); err != nil {
				return err
			}
			out = append(out, &k)
			return nil
		})
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, err
}

// FindAPIKeyByValue is the data plane's lookup.
func (s *Store) FindAPIKeyByValue(value string) *APIKey {
	keys, _ := s.ListAPIKeys()
	for _, k := range keys {
		if k.Value == value {
			return k
		}
	}
	return nil
}

// ---- handlers ----

// routeAPIKeys serves /apikeys[/{id}].
func (s *Server) routeAPIKeys(w http.ResponseWriter, r *http.Request, segs []string) *awshttp.APIError {
	if len(segs) == 1 {
		switch r.Method {
		case http.MethodPost:
			if r.URL.Query().Get("mode") == "import" {
				return awshttp.Errf(501, "NotImplemented", "ImportApiKeys reads a CSV of keys; create them one at a time with CreateApiKey")
			}
			return s.createAPIKey(w, r)
		case http.MethodGet:
			return s.listAPIKeys(w, r)
		}
		return awshttp.Errf(405, "MethodNotAllowed", "unsupported method on /apikeys")
	}
	if len(segs) == 2 {
		id := segs[1]
		switch r.Method {
		case http.MethodGet:
			k, err := s.store.GetAPIKey(id)
			if err != nil {
				return awshttp.AsAPIError(err)
			}
			writeJSON(w, 200, viewAPIKey(k, r.URL.Query().Get("includeValue") == "true"))
			return nil
		case http.MethodPatch:
			return s.patchAPIKey(w, r, id)
		case http.MethodDelete:
			if err := s.store.DeleteAPIKey(id); err != nil {
				return awshttp.AsAPIError(err)
			}
			w.WriteHeader(202)
			return nil
		}
		return awshttp.Errf(405, "MethodNotAllowed", "unsupported method on an API key")
	}
	return errNotFound("unknown API key path")
}

func (s *Server) createAPIKey(w http.ResponseWriter, r *http.Request) *awshttp.APIError {
	var req struct {
		Name        string            `json:"name"`
		Description string            `json:"description"`
		Enabled     *bool             `json:"enabled"`
		Value       string            `json:"value"`
		CustomerID  string            `json:"customerId"`
		Tags        map[string]string `json:"tags"`
		StageKeys   []struct {
			RestAPIID string `json:"restApiId"`
			StageName string `json:"stageName"`
		} `json:"stageKeys"`
	}
	if aerr := decode(r, &req); aerr != nil {
		return aerr
	}
	if req.Value != "" && (len(req.Value) < 20 || len(req.Value) > 128) {
		return errBadRequest("API Key value should be at least 20 characters")
	}
	now := s.now().Unix()
	k := &APIKey{
		ID: s.store.newID(), Name: req.Name, Description: req.Description, Value: req.Value,
		// A key is disabled unless asked for, as on AWS (the CDK says
		// enabled: true for exactly that reason).
		Enabled: false, CustomerID: req.CustomerID, Created: now, Updated: now, Tags: req.Tags,
	}
	if req.Enabled != nil {
		k.Enabled = *req.Enabled
	}
	if k.Value == "" {
		k.Value = newKeyValue()
	} else if s.store.FindAPIKeyByValue(k.Value) != nil {
		return errConflict("API Key value already exists")
	}
	for _, sk := range req.StageKeys {
		k.StageKeys = append(k.StageKeys, sk.RestAPIID+"/"+sk.StageName)
	}
	if err := s.store.PutAPIKey(k); err != nil {
		return awshttp.AsAPIError(err)
	}
	writeJSON(w, 201, viewAPIKey(k, true))
	return nil
}

func (s *Server) listAPIKeys(w http.ResponseWriter, r *http.Request) *awshttp.APIError {
	keys, err := s.store.ListAPIKeys()
	if err != nil {
		return awshttp.AsAPIError(err)
	}
	q := r.URL.Query()
	withValues := q.Get("includeValues") == "true"
	items := []any{}
	for _, k := range keys {
		if prefix := q.Get("name"); prefix != "" && !strings.HasPrefix(k.Name, prefix) {
			continue
		}
		if cust := q.Get("customerId"); cust != "" && k.CustomerID != cust {
			continue
		}
		items = append(items, viewAPIKey(k, withValues))
	}
	writeJSON(w, 200, map[string]any{"item": items})
	return nil
}

func (s *Server) patchAPIKey(w http.ResponseWriter, r *http.Request, id string) *awshttp.APIError {
	ops, aerr := decodePatch(r)
	if aerr != nil {
		return aerr
	}
	k, err := s.store.GetAPIKey(id)
	if err != nil {
		return awshttp.AsAPIError(err)
	}
	for _, op := range ops {
		if op.Op != "replace" && op.Op != "add" {
			return errBadRequest("Invalid patch operation %s on %s", op.Op, op.Path)
		}
		switch op.Path {
		case "/name":
			k.Name = op.Value
		case "/description":
			k.Description = op.Value
		case "/enabled":
			k.Enabled, _ = strconv.ParseBool(op.Value)
		case "/customerId":
			k.CustomerID = op.Value
		default:
			return errBadRequest("Invalid patch path %s", op.Path)
		}
	}
	k.Updated = s.now().Unix()
	if err := s.store.PutAPIKey(k); err != nil {
		return awshttp.AsAPIError(err)
	}
	writeJSON(w, 200, viewAPIKey(k, false))
	return nil
}
