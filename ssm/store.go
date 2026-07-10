package ssm

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/awshttp"
)

var paramsBucket = []byte("params")

// Parameter is one parameter with its full version history.
type Parameter struct {
	Name        string            `json:"name"`
	Type        string            `json:"type"` // String | StringList | SecureString
	KeyID       string            `json:"key_id,omitempty"`
	Description string            `json:"description,omitempty"`
	DataType    string            `json:"data_type,omitempty"`  // default "text"
	Tier        string            `json:"tier,omitempty"`       // Standard | Advanced (cosmetic)
	Policies    string            `json:"policies,omitempty"`   // raw policy JSON round-trip
	ExpiresAt   int64             `json:"expires_at,omitempty"` // parsed Expiration policy, unix seconds
	Tags        map[string]string `json:"tags,omitempty"`
	Versions    []Version         `json:"versions"` // ascending version order
}

// Version is one parameter version.
type Version struct {
	Value   []byte   `json:"value"` // encrypted for SecureString
	Version int64    `json:"version"`
	Labels  []string `json:"labels,omitempty"`
	Created int64    `json:"created"` // unix seconds
}

// Latest returns the newest version.
func (p *Parameter) Latest() *Version { return &p.Versions[len(p.Versions)-1] }

// Store is the bbolt-backed parameter store plus the SecureString sealer.
type Store struct {
	db    *bolt.DB
	gcm   cipher.AEAD
	clock func() time.Time
}

// newStore opens the store and loads (or mints) the per-data-dir SecureString
// key at keyPath.
func newStore(db *bolt.DB, keyPath string) (*Store, error) {
	key, err := os.ReadFile(keyPath)
	if errors.Is(err, os.ErrNotExist) {
		key = make([]byte, 32)
		rand.Read(key)
		if err := os.WriteFile(keyPath, key, 0o600); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Store{db: db, gcm: gcm, clock: time.Now}, nil
}

func (s *Store) now() time.Time { return s.clock() }

func errParamNotFound(name string) *awshttp.APIError {
	return awshttp.Errf(400, "ParameterNotFound", "parameter %q does not exist", name)
}

// seal encrypts a SecureString value.
func (s *Store) seal(plaintext string) []byte {
	nonce := make([]byte, s.gcm.NonceSize())
	rand.Read(nonce)
	return append(nonce, s.gcm.Seal(nil, nonce, []byte(plaintext), nil)...)
}

// open decrypts a SecureString value.
func (s *Store) open(sealed []byte) (string, error) {
	if len(sealed) < s.gcm.NonceSize() {
		return "", errors.New("sealed value too short")
	}
	pt, err := s.gcm.Open(nil, sealed[:s.gcm.NonceSize()], sealed[s.gcm.NonceSize():], nil)
	return string(pt), err
}

// Put creates or overwrites a parameter, bumping the version.
func (s *Store) Put(name, ptype, value, keyID, description, dataType, tier, policies string, expiresAt int64, tags map[string]string, overwrite bool) (int64, *awshttp.APIError) {
	if name == "" {
		return 0, awshttp.Errf(400, "ValidationException", "Name is required")
	}
	if strings.Contains(name, "//") || strings.HasSuffix(name, "/") {
		return 0, awshttp.Errf(400, "ValidationException", "parameter name %q is malformed", name)
	}
	switch ptype {
	case "String", "StringList", "SecureString":
	case "":
		ptype = "String"
	default:
		return 0, awshttp.Errf(400, "ValidationException", "Type must be String, StringList or SecureString, got %q", ptype)
	}
	if keyID == "" && ptype == "SecureString" {
		keyID = "alias/aws/ssm"
	}
	stored := []byte(value)
	if ptype == "SecureString" {
		stored = s.seal(value)
	}

	var version int64
	err := s.db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists(paramsBucket)
		if err != nil {
			return err
		}
		var p Parameter
		if raw := b.Get([]byte(name)); raw != nil {
			if err := json.Unmarshal(raw, &p); err != nil {
				return err
			}
			if !overwrite {
				return awshttp.Errf(400, "ParameterAlreadyExists", "parameter %q already exists; pass Overwrite to update it", name)
			}
			if p.Type != ptype && ptype != "" {
				p.Type = ptype
			}
		} else {
			p = Parameter{Name: name, Type: ptype, DataType: "text"}
		}
		if keyID != "" {
			p.KeyID = keyID
		}
		if description != "" {
			p.Description = description
		}
		if dataType != "" {
			p.DataType = dataType
		}
		if tier != "" {
			p.Tier = tier
		}
		if policies != "" {
			p.Policies = policies
			p.ExpiresAt = expiresAt
		}
		for k, v := range tags {
			if p.Tags == nil {
				p.Tags = map[string]string{}
			}
			p.Tags[k] = v
		}
		version = int64(len(p.Versions)) + 1
		p.Versions = append(p.Versions, Version{
			Value: stored, Version: version, Created: s.now().Unix(),
		})
		raw, _ := json.Marshal(p)
		return b.Put([]byte(name), raw)
	})
	if err != nil {
		return 0, awshttp.AsAPIError(err)
	}
	return version, nil
}

// Get resolves a selector: "name", "name:version", or "name:label".
func (s *Store) Get(selector string, decrypt bool) (*Parameter, *Version, string, *awshttp.APIError) {
	name, qualifier := selector, ""
	// ARN form: arn:aws:ssm:region:acct:parameter/<name>.
	if strings.HasPrefix(name, "arn:") {
		if i := strings.Index(name, ":parameter"); i >= 0 {
			name = name[i+len(":parameter"):]
		}
	}
	if i := strings.LastIndex(name, ":"); i > 0 {
		name, qualifier = name[:i], name[i+1:]
	}
	var p Parameter
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(paramsBucket)
		if b == nil {
			return errParamNotFound(name)
		}
		raw := b.Get([]byte(name))
		if raw == nil {
			return errParamNotFound(name)
		}
		return json.Unmarshal(raw, &p)
	})
	if err != nil {
		return nil, nil, "", awshttp.AsAPIError(err)
	}
	if s.expired(&p) {
		return nil, nil, "", errParamNotFound(name)
	}

	var v *Version
	if qualifier == "" {
		v = p.Latest()
	} else if n, nerr := strconv.ParseInt(qualifier, 10, 64); nerr == nil {
		for i := range p.Versions {
			if p.Versions[i].Version == n {
				v = &p.Versions[i]
				break
			}
		}
		if v == nil {
			return nil, nil, "", awshttp.Errf(400, "ParameterVersionNotFound", "parameter %q has no version %d", name, n)
		}
	} else {
		for i := range p.Versions {
			for _, l := range p.Versions[i].Labels {
				if l == qualifier {
					v = &p.Versions[i]
				}
			}
		}
		if v == nil {
			return nil, nil, "", awshttp.Errf(400, "ParameterVersionNotFound", "parameter %q has no label %q", name, qualifier)
		}
	}

	value, aerr := s.render(&p, v, decrypt)
	if aerr != nil {
		return nil, nil, "", aerr
	}
	return &p, v, value, nil
}

// render produces the API-visible value for a version.
func (s *Store) render(p *Parameter, v *Version, decrypt bool) (string, *awshttp.APIError) {
	if p.Type != "SecureString" {
		return string(v.Value), nil
	}
	if !decrypt {
		// Real SSM returns the raw ciphertext; ours is binary, so base64 it.
		return "AQIC" + strconv.Itoa(int(v.Version)) + ":" + base64of(v.Value), nil
	}
	value, err := s.open(v.Value)
	if err != nil {
		return "", awshttp.Errf(500, "InternalServerError", "stored SecureString is corrupt")
	}
	return value, nil
}

// expired reports whether the parameter's Expiration policy has passed.
func (s *Store) expired(p *Parameter) bool {
	return p.ExpiresAt > 0 && p.ExpiresAt <= s.now().Unix()
}

// Delete removes a parameter.
func (s *Store) Delete(name string) *awshttp.APIError {
	err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(paramsBucket)
		if b == nil || b.Get([]byte(name)) == nil {
			return errParamNotFound(name)
		}
		return b.Delete([]byte(name))
	})
	return awshttp.AsAPIErrorOrNil(err)
}

// List returns all live parameters, sorted by name.
func (s *Store) List() ([]Parameter, error) {
	var out []Parameter
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(paramsBucket)
		if b == nil {
			return nil
		}
		return b.ForEach(func(_, raw []byte) error {
			var p Parameter
			if json.Unmarshal(raw, &p) == nil && !s.expired(&p) {
				out = append(out, p)
			}
			return nil
		})
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, err
}

// ByPath returns parameters under a path, optionally recursive.
func (s *Store) ByPath(path string, recursive bool) ([]Parameter, error) {
	if path == "" {
		path = "/"
	}
	prefix := strings.TrimSuffix(path, "/") + "/"
	all, err := s.List()
	if err != nil {
		return nil, err
	}
	var out []Parameter
	for _, p := range all {
		if !strings.HasPrefix(p.Name, prefix) {
			continue
		}
		if !recursive && strings.Contains(p.Name[len(prefix):], "/") {
			continue
		}
		out = append(out, p)
	}
	return out, nil
}

// Label attaches labels to a version, moving each label from any version that
// had it (SSM semantics: a label names at most one version).
func (s *Store) Label(name string, version int64, labels []string) (attached []string, aerr *awshttp.APIError) {
	err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(paramsBucket)
		if b == nil {
			return errParamNotFound(name)
		}
		raw := b.Get([]byte(name))
		if raw == nil {
			return errParamNotFound(name)
		}
		var p Parameter
		if err := json.Unmarshal(raw, &p); err != nil {
			return err
		}
		if version == 0 {
			version = p.Latest().Version
		}
		var target *Version
		for i := range p.Versions {
			if p.Versions[i].Version == version {
				target = &p.Versions[i]
			}
		}
		if target == nil {
			return awshttp.Errf(400, "ParameterVersionNotFound", "parameter %q has no version %d", name, version)
		}
		for _, label := range labels {
			for i := range p.Versions {
				p.Versions[i].Labels = remove(p.Versions[i].Labels, label)
			}
			target.Labels = append(target.Labels, label)
			attached = append(attached, label)
		}
		nraw, _ := json.Marshal(p)
		return b.Put([]byte(name), nraw)
	})
	return attached, awshttp.AsAPIErrorOrNil(err)
}

// Unlabel removes labels from a version.
func (s *Store) Unlabel(name string, version int64, labels []string) (removed []string, aerr *awshttp.APIError) {
	err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(paramsBucket)
		if b == nil {
			return errParamNotFound(name)
		}
		raw := b.Get([]byte(name))
		if raw == nil {
			return errParamNotFound(name)
		}
		var p Parameter
		if err := json.Unmarshal(raw, &p); err != nil {
			return err
		}
		for i := range p.Versions {
			if p.Versions[i].Version != version {
				continue
			}
			for _, label := range labels {
				if contains(p.Versions[i].Labels, label) {
					p.Versions[i].Labels = remove(p.Versions[i].Labels, label)
					removed = append(removed, label)
				}
			}
		}
		nraw, _ := json.Marshal(p)
		return b.Put([]byte(name), nraw)
	})
	return removed, awshttp.AsAPIErrorOrNil(err)
}

// UpdateTags mutates a parameter's tags.
func (s *Store) UpdateTags(name string, add map[string]string, removeKeys []string) *awshttp.APIError {
	err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(paramsBucket)
		if b == nil {
			return errParamNotFound(name)
		}
		raw := b.Get([]byte(name))
		if raw == nil {
			return errParamNotFound(name)
		}
		var p Parameter
		if err := json.Unmarshal(raw, &p); err != nil {
			return err
		}
		for k, v := range add {
			if p.Tags == nil {
				p.Tags = map[string]string{}
			}
			p.Tags[k] = v
		}
		for _, k := range removeKeys {
			delete(p.Tags, k)
		}
		nraw, _ := json.Marshal(p)
		return b.Put([]byte(name), nraw)
	})
	return awshttp.AsAPIErrorOrNil(err)
}

// Tags returns a parameter's tags.
func (s *Store) Tags(name string) (map[string]string, *awshttp.APIError) {
	var out map[string]string
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(paramsBucket)
		if b == nil {
			return errParamNotFound(name)
		}
		raw := b.Get([]byte(name))
		if raw == nil {
			return errParamNotFound(name)
		}
		var p Parameter
		if err := json.Unmarshal(raw, &p); err != nil {
			return err
		}
		out = p.Tags
		return nil
	})
	return out, awshttp.AsAPIErrorOrNil(err)
}

// SweepExpired deletes parameters whose Expiration policy has passed.
func (s *Store) SweepExpired() {
	now := s.now().Unix()
	_ = s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(paramsBucket)
		if b == nil {
			return nil
		}
		var doomed [][]byte
		_ = b.ForEach(func(k, raw []byte) error {
			var p Parameter
			if json.Unmarshal(raw, &p) == nil && p.ExpiresAt > 0 && p.ExpiresAt <= now {
				doomed = append(doomed, append([]byte(nil), k...))
			}
			return nil
		})
		for _, k := range doomed {
			_ = b.Delete(k)
		}
		return nil
	})
}

// ARN returns a parameter's ARN.
func paramARN(name string) string {
	return awsident.ARN("ssm", "parameter"+ensureSlash(name))
}

func ensureSlash(name string) string {
	if strings.HasPrefix(name, "/") {
		return name
	}
	return "/" + name
}

func contains(list []string, v string) bool {
	return slices.Contains(list, v)
}

func remove(list []string, v string) []string {
	out := list[:0]
	for _, s := range list {
		if s != v {
			out = append(out, s)
		}
	}
	return out
}
