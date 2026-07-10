package secretsmanager

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/awshttp"
)

var secretsBucket = []byte("secrets")

// Secret is one secret with its version map.
type Secret struct {
	ARN         string             `json:"arn"`
	Name        string             `json:"name"`
	Description string             `json:"description,omitempty"`
	KMSKeyID    string             `json:"kms_key_id,omitempty"`
	Tags        map[string]string  `json:"tags,omitempty"`
	Policy      string             `json:"policy,omitempty"` // resource policy round-trip
	Versions    map[string]Version `json:"versions"`
	Created     int64              `json:"created"`
	LastChanged int64              `json:"last_changed"`
	DeletedAt   int64              `json:"deleted_at,omitempty"` // scheduled-deletion time set
	PurgeAt     int64              `json:"purge_at,omitempty"`   // when the janitor removes it
}

// Version is one secret version. Exactly one of String/Binary was set by the
// caller; both are sealed at rest.
type Version struct {
	String  []byte   `json:"string,omitempty"` // sealed
	Binary  []byte   `json:"binary,omitempty"` // sealed
	Stages  []string `json:"stages,omitempty"`
	Created int64    `json:"created"`
}

// Store is the bbolt-backed secret store plus the value sealer.
type Store struct {
	db    *bolt.DB
	gcm   cipher.AEAD
	clock func() time.Time
}

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

func errSecretNotFound(id string) *awshttp.APIError {
	return awshttp.Errf(400, "ResourceNotFoundException", "Secrets Manager can't find the specified secret: %s", id)
}

func (s *Store) seal(plaintext []byte) []byte {
	if plaintext == nil {
		return nil
	}
	nonce := make([]byte, s.gcm.NonceSize())
	rand.Read(nonce)
	return append(nonce, s.gcm.Seal(nil, nonce, plaintext, nil)...)
}

func (s *Store) open(sealed []byte) ([]byte, error) {
	if sealed == nil {
		return nil, nil
	}
	if len(sealed) < s.gcm.NonceSize() {
		return nil, errors.New("sealed value too short")
	}
	return s.gcm.Open(nil, sealed[:s.gcm.NonceSize()], sealed[s.gcm.NonceSize():], nil)
}

// nameFromID extracts the secret name from a SecretId (name or ARN; ARNs carry
// a -6char suffix after the name).
func nameFromID(id string) string {
	if !strings.HasPrefix(id, "arn:") {
		return id
	}
	// arn:aws:secretsmanager:region:acct:secret:<name>-XXXXXX
	parts := strings.SplitN(id, ":", 7)
	if len(parts) != 7 {
		return id
	}
	name := parts[6]
	if i := strings.LastIndex(name, "-"); i > 0 && len(name)-i == 7 {
		name = name[:i]
	}
	return name
}

// get loads a secret inside a transaction.
func (s *Store) get(tx *bolt.Tx, id string) (*Secret, error) {
	name := nameFromID(id)
	b := tx.Bucket(secretsBucket)
	if b == nil {
		return nil, errSecretNotFound(id)
	}
	raw := b.Get([]byte(name))
	if raw == nil {
		return nil, errSecretNotFound(id)
	}
	var sec Secret
	if err := json.Unmarshal(raw, &sec); err != nil {
		return nil, err
	}
	return &sec, nil
}

func (s *Store) put(tx *bolt.Tx, sec *Secret) error {
	b, err := tx.CreateBucketIfNotExists(secretsBucket)
	if err != nil {
		return err
	}
	raw, _ := json.Marshal(sec)
	return b.Put([]byte(sec.Name), raw)
}

// Get loads a secret by name or ARN.
func (s *Store) Get(id string) (*Secret, error) {
	var out *Secret
	err := s.db.View(func(tx *bolt.Tx) error {
		sec, err := s.get(tx, id)
		if err != nil {
			return err
		}
		out = sec
		return nil
	})
	return out, err
}

// Mutate applies fn to a secret and persists it.
func (s *Store) Mutate(id string, fn func(*Secret) error) (*Secret, error) {
	var out *Secret
	err := s.db.Update(func(tx *bolt.Tx) error {
		sec, err := s.get(tx, id)
		if err != nil {
			return err
		}
		if err := fn(sec); err != nil {
			return err
		}
		sec.LastChanged = s.now().Unix()
		out = sec
		return s.put(tx, sec)
	})
	return out, err
}

// Create makes a new secret with an initial version, or fails if it exists.
func (s *Store) Create(name, description, kmsKeyID, token string, str, bin []byte, tags map[string]string) (*Secret, string, error) {
	if name == "" {
		return nil, "", awshttp.Errf(400, "ValidationException", "Name is required")
	}
	if token == "" {
		token = newUUID()
	}
	now := s.now().Unix()
	sec := &Secret{
		ARN:         secretARN(name),
		Name:        name,
		Description: description,
		KMSKeyID:    kmsKeyID,
		Tags:        tags,
		Versions:    map[string]Version{},
		Created:     now,
		LastChanged: now,
	}
	err := s.db.Update(func(tx *bolt.Tx) error {
		if existing, err := s.get(tx, name); err == nil {
			if existing.DeletedAt > 0 {
				return awshttp.Errf(400, "InvalidRequestException",
					"secret %s is scheduled for deletion; restore it or wait for the purge", name)
			}
			// Idempotent when the same ClientRequestToken already made it.
			if _, ok := existing.Versions[token]; ok {
				*sec = *existing
				return nil
			}
			return awshttp.Errf(400, "ResourceExistsException", "the secret %s already exists", name)
		}
		if str != nil || bin != nil {
			sec.Versions[token] = Version{
				String: s.seal(str), Binary: s.seal(bin),
				Stages: []string{"AWSCURRENT"}, Created: now,
			}
		}
		return s.put(tx, sec)
	})
	if err != nil {
		return nil, "", err
	}
	return sec, token, nil
}

// AddVersion appends a version and moves AWSCURRENT (old current becomes
// AWSPREVIOUS), returning the new version id.
func (s *Store) AddVersion(id, token string, str, bin []byte, stages []string) (*Secret, string, error) {
	if token == "" {
		token = newUUID()
	}
	if len(stages) == 0 {
		stages = []string{"AWSCURRENT"}
	}
	sec, err := s.Mutate(id, func(sec *Secret) error {
		if sec.DeletedAt > 0 {
			return errDeleted(sec.Name)
		}
		if _, ok := sec.Versions[token]; ok {
			return nil // idempotent replay
		}
		if contains(stages, "AWSCURRENT") {
			for vid, v := range sec.Versions {
				if contains(v.Stages, "AWSCURRENT") {
					v.Stages = replace(v.Stages, "AWSCURRENT", "AWSPREVIOUS")
					sec.Versions[vid] = v
				} else if contains(v.Stages, "AWSPREVIOUS") {
					v.Stages = remove(v.Stages, "AWSPREVIOUS")
					sec.Versions[vid] = v
				}
			}
		}
		sec.Versions[token] = Version{
			String: s.seal(str), Binary: s.seal(bin),
			Stages: stages, Created: s.now().Unix(),
		}
		return nil
	})
	if err != nil {
		return nil, "", err
	}
	return sec, token, nil
}

func errDeleted(name string) *awshttp.APIError {
	return awshttp.Errf(400, "InvalidRequestException",
		"secret %s is scheduled for deletion (restore it to use it)", name)
}

// Resolve picks a version by id or stage (default AWSCURRENT).
func (sec *Secret) Resolve(versionID, stage string) (string, *Version, *awshttp.APIError) {
	if versionID != "" {
		v, ok := sec.Versions[versionID]
		if !ok {
			return "", nil, errSecretNotFound(sec.Name + " version " + versionID)
		}
		return versionID, &v, nil
	}
	if stage == "" {
		stage = "AWSCURRENT"
	}
	for vid, v := range sec.Versions {
		if contains(v.Stages, stage) {
			return vid, &v, nil
		}
	}
	return "", nil, errSecretNotFound(sec.Name + " with staging label " + stage)
}

// List returns all secrets, sorted by name.
func (s *Store) List() ([]Secret, error) {
	var out []Secret
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(secretsBucket)
		if b == nil {
			return nil
		}
		return b.ForEach(func(_, raw []byte) error {
			var sec Secret
			if json.Unmarshal(raw, &sec) == nil {
				out = append(out, sec)
			}
			return nil
		})
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, err
}

// Delete schedules (or forces) deletion.
func (s *Store) Delete(id string, recoveryDays int, force bool) (*Secret, error) {
	if force {
		var out *Secret
		err := s.db.Update(func(tx *bolt.Tx) error {
			sec, err := s.get(tx, id)
			if err != nil {
				return err
			}
			out = sec
			return tx.Bucket(secretsBucket).Delete([]byte(sec.Name))
		})
		if out != nil {
			out.DeletedAt = s.now().Unix()
		}
		return out, err
	}
	if recoveryDays == 0 {
		recoveryDays = 30
	}
	if recoveryDays < 7 || recoveryDays > 30 {
		return nil, awshttp.Errf(400, "InvalidParameterException", "RecoveryWindowInDays must be between 7 and 30, got %d", recoveryDays)
	}
	return s.Mutate(id, func(sec *Secret) error {
		if sec.DeletedAt > 0 {
			return errDeleted(sec.Name)
		}
		sec.DeletedAt = s.now().Unix()
		sec.PurgeAt = s.now().Add(time.Duration(recoveryDays) * 24 * time.Hour).Unix()
		return nil
	})
}

// Restore cancels a scheduled deletion.
func (s *Store) Restore(id string) (*Secret, error) {
	return s.Mutate(id, func(sec *Secret) error {
		sec.DeletedAt, sec.PurgeAt = 0, 0
		return nil
	})
}

// SweepDeleted purges secrets whose recovery window has passed.
func (s *Store) SweepDeleted() {
	now := s.now().Unix()
	_ = s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(secretsBucket)
		if b == nil {
			return nil
		}
		var doomed [][]byte
		_ = b.ForEach(func(k, raw []byte) error {
			var sec Secret
			if json.Unmarshal(raw, &sec) == nil && sec.PurgeAt > 0 && sec.PurgeAt <= now {
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

func secretARN(name string) string {
	return awsident.ARN("secretsmanager", "secret:"+name+"-"+randSuffix())
}

// randSuffix mimics the 6-character ARN suffix real Secrets Manager appends.
func randSuffix() string {
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	var b [6]byte
	rand.Read(b[:])
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b[:])
}

func newUUID() string {
	var b [16]byte
	rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
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

func replace(list []string, from, to string) []string {
	for i, s := range list {
		if s == from {
			list[i] = to
		}
	}
	return list
}
