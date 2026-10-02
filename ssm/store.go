package ssm

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"os"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/doze-dev/doze-aws/internal/lazybolt"
	bolt "go.etcd.io/bbolt"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/awshttp"
)

var paramsBucket = []byte("params")

// parameter is one parameter with its full version history.
type parameter struct {
	Name        string            `json:"name"`
	Type        string            `json:"type"` // String | StringList | SecureString
	KeyID       string            `json:"key_id,omitempty"`
	Description string            `json:"description,omitempty"`
	DataType    string            `json:"data_type,omitempty"`  // default "text"
	Tier        string            `json:"tier,omitempty"`       // Standard | Advanced (cosmetic)
	Policies    string            `json:"policies,omitempty"`   // raw policy JSON round-trip
	ExpiresAt   int64             `json:"expires_at,omitempty"` // parsed Expiration policy, unix seconds
	Tags        map[string]string `json:"tags,omitempty"`
	Versions    []paramVersion    `json:"versions"` // ascending version order
}

// paramVersion is one parameter version.
type paramVersion struct {
	Value   []byte   `json:"value"` // encrypted for SecureString
	Version int64    `json:"version"`
	Labels  []string `json:"labels,omitempty"`
	Created int64    `json:"created"` // unix seconds
}

// Latest returns the newest version.
func (p *parameter) Latest() *paramVersion { return &p.Versions[len(p.Versions)-1] }

// store is the bbolt-backed parameter store plus the SecureString sealer.
type store struct {
	db    *lazybolt.DB
	gcm   cipher.AEAD
	clock func() time.Time
}

// newStore opens the store and loads (or mints) the per-data-dir SecureString
// key at keyPath.
func newStore(db *lazybolt.DB, keyPath string) (*store, error) {
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
	return &store{db: db, gcm: gcm, clock: time.Now}, nil
}

func (s *store) now() time.Time { return s.clock() }

// What a parameter may be called. None of it is in the service model, which
// gives Name a length and nothing else, so none of it was enforced: a name
// with a space in it, or one in the namespace AWS keeps for itself, made a
// parameter here that no deployed stack could ever hold.
var (
	paramSegment = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
	reservedName = regexp.MustCompile(`(?i)^(aws|ssm)`)
)

const (
	paramNameRule = `Parameter name: can't be prefixed with "aws" or "ssm" (case-insensitive). ` +
		`If formed as a path, it can consist of sub-paths divided by slash symbol; each sub-path can be ` +
		`formed as a mix of letters, numbers and the following 3 symbols .-_`
	paramPathRule = `The parameter doesn't meet the parameter name requirements. The parameter name must ` +
		`begin with a forward slash "/". It can't be prefixed with "aws" or "ssm" (case-insensitive). ` +
		`It must use only letters, numbers, or the following symbols: . (period), - (hyphen), _ (underscore). ` +
		`Special characters are not allowed. All sub-paths, if specified, must use the forward slash symbol "/". ` +
		`Valid example: /get/parameters2-/by1./path0_.`
	maxParamLevels = 15
)

// validParameterName holds a name being WRITTEN to SSM's rules. Reads are not
// held to the reserved prefixes: /aws/service/... is where the public
// parameters live, and reading them is the point of them.
func validParameterName(name string) *awshttp.APIError {
	bad := awshttp.Errf(400, "ValidationException", "%s", paramNameRule)
	segs := strings.Split(strings.TrimPrefix(name, "/"), "/")
	if len(segs) > maxParamLevels {
		return bad
	}
	for _, seg := range segs {
		if !paramSegment.MatchString(seg) {
			return bad
		}
	}
	// A plain name may not start with the reserved words at all; a path may
	// not live under /aws or /ssm.
	if strings.HasPrefix(name, "/") {
		if first := strings.ToLower(segs[0]); first == "aws" || first == "ssm" {
			return bad
		}
	} else if reservedName.MatchString(name) {
		return bad
	}
	return nil
}

func errParamNotFound(name string) *awshttp.APIError {
	return awshttp.Errf(400, "ParameterNotFound", "parameter %q does not exist", name)
}

// seal encrypts a SecureString value.
func (s *store) seal(plaintext string) []byte {
	nonce := make([]byte, s.gcm.NonceSize())
	rand.Read(nonce)
	return append(nonce, s.gcm.Seal(nil, nonce, []byte(plaintext), nil)...)
}

// open decrypts a SecureString value.
func (s *store) open(sealed []byte) (string, error) {
	if len(sealed) < s.gcm.NonceSize() {
		return "", errors.New("sealed value too short")
	}
	pt, err := s.gcm.Open(nil, sealed[:s.gcm.NonceSize()], sealed[s.gcm.NonceSize():], nil)
	return string(pt), err
}

// Put creates or overwrites a parameter, bumping the version.
func (s *store) Put(name, ptype, value, keyID, description, dataType, tier, policies string, expiresAt int64, tags map[string]string, overwrite bool) (int64, *awshttp.APIError) {
	if name == "" {
		return 0, awshttp.Errf(400, "ValidationException", "Name is required")
	}
	if aerr := validParameterName(name); aerr != nil {
		return 0, aerr
	}
	// Whether the caller explicitly supplied a Type. On overwrite an omitted
	// Type must inherit the existing parameter's type — defaulting it to
	// "String" here would silently downgrade a SecureString and store its new
	// value in plaintext.
	typeGiven := ptype != ""
	switch ptype {
	case "String", "StringList", "SecureString", "":
	default:
		return 0, awshttp.Errf(400, "ValidationException", "Type must be String, StringList or SecureString, got %q", ptype)
	}

	var version int64
	err := s.db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists(paramsBucket)
		if err != nil {
			return err
		}
		var p parameter
		if raw := b.Get([]byte(name)); raw != nil {
			if err := json.Unmarshal(raw, &p); err != nil {
				return err
			}
			if !overwrite {
				return awshttp.Errf(400, "ParameterAlreadyExists", "parameter %q already exists; pass Overwrite to update it", name)
			}
			// A parameter's type is immutable; real SSM rejects a mismatched
			// Type and silently keeps the existing type when it is omitted.
			if typeGiven && ptype != p.Type {
				return awshttp.Errf(400, "ValidationException", "cannot change type of parameter %q from %s to %s", name, p.Type, ptype)
			}
		} else {
			// A new parameter has to say what it is. Defaulting to String
			// meant a put with the Type forgotten worked here and was refused
			// on AWS — and a SecureString written that way sat in plaintext.
			if !typeGiven {
				return awshttp.Errf(400, "ValidationException", "A parameter type is required when you create a parameter.")
			}
			p = parameter{Name: name, Type: ptype, DataType: "text"}
		}
		// Seal according to the effective (stored) type, not the request type.
		if p.Type == "SecureString" && p.KeyID == "" && keyID == "" {
			keyID = "alias/aws/ssm"
		}
		stored := []byte(value)
		if p.Type == "SecureString" {
			stored = s.seal(value)
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
		p.Versions = append(p.Versions, paramVersion{
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
func (s *store) Get(selector string, decrypt bool) (*parameter, *paramVersion, string, *awshttp.APIError) {
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
	var p parameter
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

	var v *paramVersion
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
func (s *store) render(p *parameter, v *paramVersion, decrypt bool) (string, *awshttp.APIError) {
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
func (s *store) expired(p *parameter) bool {
	return p.ExpiresAt > 0 && p.ExpiresAt <= s.now().Unix()
}

// Delete removes a parameter.
func (s *store) Delete(name string) *awshttp.APIError {
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
func (s *store) List() ([]parameter, error) {
	var out []parameter
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(paramsBucket)
		if b == nil {
			return nil
		}
		return b.ForEach(func(_, raw []byte) error {
			var p parameter
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
func (s *store) ByPath(path string, recursive bool) ([]parameter, error) {
	if path == "" {
		path = "/"
	}
	// A path is a path: "app/db" used to be read as "/app/db" would be, and
	// AWS refuses it.
	if !strings.HasPrefix(path, "/") {
		return nil, awshttp.Errf(400, "ValidationException", "%s", paramPathRule)
	}
	prefix := strings.TrimSuffix(path, "/") + "/"
	all, err := s.List()
	if err != nil {
		return nil, err
	}
	var out []parameter
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
func (s *store) Label(name string, version int64, labels []string) (attached []string, aerr *awshttp.APIError) {
	err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(paramsBucket)
		if b == nil {
			return errParamNotFound(name)
		}
		raw := b.Get([]byte(name))
		if raw == nil {
			return errParamNotFound(name)
		}
		var p parameter
		if err := json.Unmarshal(raw, &p); err != nil {
			return err
		}
		if version == 0 {
			version = p.Latest().Version
		}
		var target *paramVersion
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
func (s *store) Unlabel(name string, version int64, labels []string) (removed []string, aerr *awshttp.APIError) {
	err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(paramsBucket)
		if b == nil {
			return errParamNotFound(name)
		}
		raw := b.Get([]byte(name))
		if raw == nil {
			return errParamNotFound(name)
		}
		var p parameter
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
func (s *store) UpdateTags(name string, add map[string]string, removeKeys []string) *awshttp.APIError {
	err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(paramsBucket)
		if b == nil {
			return errParamNotFound(name)
		}
		raw := b.Get([]byte(name))
		if raw == nil {
			return errParamNotFound(name)
		}
		var p parameter
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
func (s *store) Tags(name string) (map[string]string, *awshttp.APIError) {
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
		var p parameter
		if err := json.Unmarshal(raw, &p); err != nil {
			return err
		}
		out = p.Tags
		return nil
	})
	return out, awshttp.AsAPIErrorOrNil(err)
}

// SweepExpired deletes parameters whose Expiration policy has passed.
func (s *store) SweepExpired() {
	now := s.now().Unix()
	_ = s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(paramsBucket)
		if b == nil {
			return nil
		}
		var doomed [][]byte
		_ = b.ForEach(func(k, raw []byte) error {
			var p parameter
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

// paramARN returns a parameter's ARN under the given identity. It takes one
// rather than reading a package constant because an ARN belongs to the instance
// that minted it.
func paramARN(id awsident.Identity, name string) string {
	return id.ARN("ssm", "parameter"+ensureSlash(name))
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
