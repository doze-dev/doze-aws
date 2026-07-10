package kms

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/awshttp"
)

var (
	keysBucket    = []byte("keys")
	aliasesBucket = []byte("aliases")
)

// Key states.
const (
	stateEnabled         = "Enabled"
	stateDisabled        = "Disabled"
	statePendingDeletion = "PendingDeletion"
)

// Key is one customer master key.
type Key struct {
	ID           string            `json:"id"` // UUID
	Material     []byte            `json:"material"`
	OldMaterials [][]byte          `json:"old_materials,omitempty"` // superseded backing keys, newest first, kept so pre-rotation ciphertexts still decrypt
	Rotations    []int64           `json:"rotations,omitempty"`     // unix-seconds timestamps of each rotation, newest first
	State        string            `json:"state"`
	Description  string            `json:"description"`
	Created      int64             `json:"created"`               // unix seconds
	DeletionAt   int64             `json:"deletion_at,omitempty"` // unix seconds, PendingDeletion only
	RotationOn   bool              `json:"rotation_on"`           // automatic-rotation flag
	Policy       string            `json:"policy,omitempty"`      // round-trip only
	Tags         map[string]string `json:"tags,omitempty"`
	KeySpec      string            `json:"key_spec"`  // SYMMETRIC_DEFAULT
	KeyUsage     string            `json:"key_usage"` // ENCRYPT_DECRYPT
	MultiRegion  bool              `json:"multi_region"`
}

// ARN returns the key's ARN.
func (k *Key) ARN() string { return awsident.ARN("kms", "key/"+k.ID) }

// Store is the bbolt-backed KMS state.
type Store struct {
	db    *bolt.DB
	clock func() time.Time
}

func newStore(db *bolt.DB) *Store { return &Store{db: db, clock: time.Now} }

func (s *Store) now() time.Time { return s.clock() }

func errNotFound(keyID string) *awshttp.APIError {
	return awshttp.Errf(400, "NotFoundException", "key %q does not exist", keyID)
}

// CreateKey mints a new key of the given spec. Material is the AES key
// (symmetric), the HMAC secret, or the PKCS#8 DER private key (RSA/ECC).
func (s *Store) CreateKey(spec, usage, description, policy string, tags map[string]string) (*Key, error) {
	if spec == "" {
		spec = "SYMMETRIC_DEFAULT"
	}
	if usage == "" {
		usage = defaultUsageFor(spec)
	}
	if aerr := validateSpecUsage(spec, usage); aerr != nil {
		return nil, aerr
	}
	material, err := generateMaterial(spec)
	if err != nil {
		return nil, err
	}
	k := &Key{
		ID:          newUUID(),
		Material:    material,
		State:       stateEnabled,
		Description: description,
		Created:     s.now().Unix(),
		Policy:      policy,
		Tags:        tags,
		KeySpec:     spec,
		KeyUsage:    usage,
	}
	err = s.db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists(keysBucket)
		if err != nil {
			return err
		}
		raw, _ := json.Marshal(k)
		return b.Put([]byte(k.ID), raw)
	})
	return k, err
}

// Resolve maps any accepted key identifier — key id, key ARN, alias name,
// alias ARN — to the key.
func (s *Store) Resolve(ident string) (*Key, error) {
	var out *Key
	err := s.db.View(func(tx *bolt.Tx) error {
		k, err := s.resolve(tx, ident)
		if err != nil {
			return err
		}
		out = k
		return nil
	})
	return out, err
}

func (s *Store) resolve(tx *bolt.Tx, ident string) (*Key, error) {
	if ident == "" {
		return nil, awshttp.Errf(400, "ValidationException", "KeyId is required")
	}
	// ARN forms: arn:aws:kms:region:acct:key/<id> or :alias/<name>.
	if strings.HasPrefix(ident, "arn:") {
		if i := strings.Index(ident, ":key/"); i >= 0 {
			ident = ident[i+len(":key/"):]
		} else if i := strings.Index(ident, ":alias/"); i >= 0 {
			ident = "alias/" + ident[i+len(":alias/"):]
		}
	}
	if name, ok := strings.CutPrefix(ident, "alias/"); ok {
		ab := tx.Bucket(aliasesBucket)
		if ab == nil {
			return nil, errNotFound("alias/" + name)
		}
		id := ab.Get([]byte(name))
		if id == nil {
			return nil, errNotFound("alias/" + name)
		}
		ident = string(id)
	}
	b := tx.Bucket(keysBucket)
	if b == nil {
		return nil, errNotFound(ident)
	}
	raw := b.Get([]byte(ident))
	if raw == nil {
		return nil, errNotFound(ident)
	}
	var k Key
	if err := json.Unmarshal(raw, &k); err != nil {
		return nil, err
	}
	return &k, nil
}

// Update applies fn to a key resolved by ident and persists it.
func (s *Store) Update(ident string, fn func(*Key) *awshttp.APIError) (*Key, error) {
	var out *Key
	err := s.db.Update(func(tx *bolt.Tx) error {
		k, err := s.resolve(tx, ident)
		if err != nil {
			return err
		}
		if aerr := fn(k); aerr != nil {
			return aerr
		}
		raw, _ := json.Marshal(k)
		out = k
		return tx.Bucket(keysBucket).Put([]byte(k.ID), raw)
	})
	return out, err
}

// List returns all keys, sorted by id.
func (s *Store) List() ([]Key, error) {
	var out []Key
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(keysBucket)
		if b == nil {
			return nil
		}
		return b.ForEach(func(_, raw []byte) error {
			var k Key
			if json.Unmarshal(raw, &k) == nil {
				out = append(out, k)
			}
			return nil
		})
	})
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, err
}

// ---- aliases ----

// SetAlias points an alias at a key (create or update).
func (s *Store) SetAlias(name, keyIdent string, mustExist, mustNotExist bool) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		k, err := s.resolve(tx, keyIdent)
		if err != nil {
			return err
		}
		b, err := tx.CreateBucketIfNotExists(aliasesBucket)
		if err != nil {
			return err
		}
		existing := b.Get([]byte(name))
		if mustNotExist && existing != nil {
			return awshttp.Errf(400, "AlreadyExistsException", "alias/%s already exists", name)
		}
		if mustExist && existing == nil {
			return errNotFound("alias/" + name)
		}
		return b.Put([]byte(name), []byte(k.ID))
	})
}

// DeleteAlias removes an alias.
func (s *Store) DeleteAlias(name string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(aliasesBucket)
		if b == nil || b.Get([]byte(name)) == nil {
			return errNotFound("alias/" + name)
		}
		return b.Delete([]byte(name))
	})
}

// Aliases returns name→keyID, sorted by name.
func (s *Store) Aliases() ([][2]string, error) {
	var out [][2]string
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(aliasesBucket)
		if b == nil {
			return nil
		}
		return b.ForEach(func(k, v []byte) error {
			out = append(out, [2]string{string(k), string(v)})
			return nil
		})
	})
	sort.Slice(out, func(i, j int) bool { return out[i][0] < out[j][0] })
	return out, err
}

// SweepDeletions finalizes PendingDeletion keys whose date has passed.
func (s *Store) SweepDeletions() {
	now := s.now().Unix()
	_ = s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(keysBucket)
		if b == nil {
			return nil
		}
		var doomed []string
		_ = b.ForEach(func(id, raw []byte) error {
			var k Key
			if json.Unmarshal(raw, &k) == nil && k.State == statePendingDeletion && k.DeletionAt > 0 && k.DeletionAt <= now {
				doomed = append(doomed, string(id))
			}
			return nil
		})
		for _, id := range doomed {
			_ = b.Delete([]byte(id))
			// Drop aliases pointing at the deleted key.
			if ab := tx.Bucket(aliasesBucket); ab != nil {
				var stale [][]byte
				_ = ab.ForEach(func(name, keyID []byte) error {
					if string(keyID) == id {
						stale = append(stale, append([]byte(nil), name...))
					}
					return nil
				})
				for _, name := range stale {
					_ = ab.Delete(name)
				}
			}
		}
		return nil
	})
}

// ---- crypto ----

// Ciphertext blob layout: "DZKMS" | version(1) | idLen(1) | keyID | nonce | sealed.
const blobMagic = "DZKMS"

// usable rejects operations on keys that are not in the Enabled state, with
// the error codes real KMS uses.
func usable(k *Key) *awshttp.APIError {
	switch k.State {
	case stateEnabled:
		return nil
	case stateDisabled:
		return awshttp.Errf(400, "DisabledException", "key %s is disabled", k.ID)
	default:
		return awshttp.Errf(400, "KMSInvalidStateException", "key %s is pending deletion", k.ID)
	}
}

// seal encrypts plaintext under the key with the encryption context bound as
// additional authenticated data.
func seal(k *Key, plaintext []byte, context map[string]string) ([]byte, error) {
	block, err := aes.NewCipher(k.Material)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	rand.Read(nonce)
	sealed := gcm.Seal(nil, nonce, plaintext, canonicalContext(context))

	blob := make([]byte, 0, len(blobMagic)+2+len(k.ID)+len(nonce)+len(sealed))
	blob = append(blob, blobMagic...)
	blob = append(blob, 1, byte(len(k.ID)))
	blob = append(blob, k.ID...)
	blob = append(blob, nonce...)
	blob = append(blob, sealed...)
	return blob, nil
}

// openBlob parses a ciphertext blob and returns the embedded key id and the
// function that finishes decryption once the key is loaded.
func openBlob(blob []byte) (keyID string, unseal func(*Key, map[string]string) ([]byte, error), err *awshttp.APIError) {
	bad := awshttp.Errf(400, "InvalidCiphertextException", "ciphertext is malformed or was not produced by this KMS")
	if len(blob) < len(blobMagic)+2 || string(blob[:len(blobMagic)]) != blobMagic || blob[len(blobMagic)] != 1 {
		return "", nil, bad
	}
	idLen := int(blob[len(blobMagic)+1])
	rest := blob[len(blobMagic)+2:]
	if len(rest) < idLen {
		return "", nil, bad
	}
	keyID = string(rest[:idLen])
	rest = rest[idLen:]
	return keyID, func(k *Key, context map[string]string) ([]byte, error) {
		// Try the current backing key, then any superseded ones (rotation keeps
		// old material so ciphertexts predating a rotation still decrypt).
		aad := canonicalContext(context)
		for _, mat := range append([][]byte{k.Material}, k.OldMaterials...) {
			block, err := aes.NewCipher(mat)
			if err != nil {
				continue
			}
			gcm, err := cipher.NewGCM(block)
			if err != nil {
				continue
			}
			if len(rest) < gcm.NonceSize() {
				return nil, bad
			}
			if pt, err := gcm.Open(nil, rest[:gcm.NonceSize()], rest[gcm.NonceSize():], aad); err == nil {
				return pt, nil
			}
		}
		return nil, awshttp.Errf(400, "InvalidCiphertextException", "decryption failed (wrong key or encryption context)")
	}, nil
}

// canonicalContext renders an encryption context deterministically (sorted
// key=value lines) for use as authenticated data.
func canonicalContext(context map[string]string) []byte {
	if len(context) == 0 {
		return nil
	}
	keys := make([]string, 0, len(context))
	for k := range context {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b []byte
	for _, k := range keys {
		b = binary.BigEndian.AppendUint32(b, uint32(len(k)))
		b = append(b, k...)
		b = binary.BigEndian.AppendUint32(b, uint32(len(context[k])))
		b = append(b, context[k]...)
	}
	return b
}

func newUUID() string {
	var b [16]byte
	rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
