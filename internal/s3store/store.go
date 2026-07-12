// Package s3store is the storage engine behind the doze-aws s3 service:
// bucket and object-version metadata in bbolt, object bodies as one file per
// version on disk. Bodies are only ever streamed (temp-file + rename in,
// ReadSeeker out) — steady-state memory is independent of object and bucket
// size, the same flat-memory invariant doze-kafka's log store keeps.
//
// bbolt layout:
//
//	buckets              name        -> Bucket JSON
//	ver:<bucket>         objKey \x00 ^seq (big-endian) -> ObjectVersion JSON (newest first per key)
//	cur:<bucket>         objKey      -> ver: key of the latest version (absent when latest is a delete marker)
//	up:<bucket>          uploadID    -> Upload JSON
//	parts:<bucket>       uploadID \x00 be32(part) -> Part JSON
//
// Blob files live at blobs/<bucket>/<shard>/<blobID>; temp writes under tmp/.
package s3store

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/doze-dev/doze-aws/internal/schemaver"

	"github.com/doze-dev/doze-aws/internal/awshttp"
)

var bucketsBucket = []byte("buckets")

func verBucket(bucket string) []byte   { return []byte("ver:" + bucket) }
func curBucket(bucket string) []byte   { return []byte("cur:" + bucket) }
func upBucket(bucket string) []byte    { return []byte("up:" + bucket) }
func partsBucket(bucket string) []byte { return []byte("parts:" + bucket) }

// Bucket is a bucket's durable definition. Config documents with no local
// behavior are stored raw and returned faithfully (Tier C).
type Bucket struct {
	Name       string            `json:"name"`
	Created    int64             `json:"created"`              // unix seconds
	Versioning string            `json:"versioning,omitempty"` // "" | Enabled | Suspended
	Tags       map[string]string `json:"tags,omitempty"`

	// Functional configs (evaluated locally).
	CORS       string `json:"cors,omitempty"`        // CORS XML document
	Lifecycle  string `json:"lifecycle,omitempty"`   // lifecycle XML document
	Website    string `json:"website,omitempty"`     // website XML document
	ObjectLock string `json:"object_lock,omitempty"` // object-lock XML document

	// Round-trip-only configs.
	Policy       string `json:"policy,omitempty"`
	ACL          string `json:"acl,omitempty"` // canned ACL name
	Encryption   string `json:"encryption,omitempty"`
	Logging      string `json:"logging,omitempty"`
	Notification string `json:"notification,omitempty"`
	Replication  string `json:"replication,omitempty"`
	Accelerate   string `json:"accelerate,omitempty"`
	RequestPays  string `json:"request_pays,omitempty"`
}

// ObjectVersion is one stored version of one object key.
type ObjectVersion struct {
	Key          string            `json:"key"`
	VersionID    string            `json:"version_id"` // "null" on unversioned buckets
	Blob         string            `json:"blob,omitempty"`
	Size         int64             `json:"size"`
	ETag         string            `json:"etag"` // without quotes
	ChecksumAlg  string            `json:"checksum_alg,omitempty"`
	ChecksumVal  string            `json:"checksum_val,omitempty"`  // base64 (composite form for multipart)
	ChecksumType string            `json:"checksum_type,omitempty"` // FULL_OBJECT | COMPOSITE
	ContentType  string            `json:"content_type,omitempty"`
	Meta         map[string]string `json:"meta,omitempty"`    // x-amz-meta-*
	Headers      map[string]string `json:"headers,omitempty"` // Cache-Control, Content-Disposition, ...
	Tags         map[string]string `json:"tags,omitempty"`
	StorageClass string            `json:"storage_class,omitempty"`
	DeleteMarker bool              `json:"delete_marker,omitempty"`
	LastModified int64             `json:"last_modified"` // unix seconds

	// Object lock (enforced).
	RetainUntil int64  `json:"retain_until,omitempty"` // unix seconds
	RetainMode  string `json:"retain_mode,omitempty"`  // GOVERNANCE | COMPLIANCE
	LegalHold   bool   `json:"legal_hold,omitempty"`

	seqKey []byte // ver: bucket key this record was loaded from (not persisted)
}

// SeqKey returns the bbolt key the version was loaded from.
func (v *ObjectVersion) SeqKey() []byte { return v.seqKey }

// Upload is one in-progress multipart upload.
type Upload struct {
	ID          string            `json:"id"`
	Key         string            `json:"key"`
	Initiated   int64             `json:"initiated"`
	ContentType string            `json:"content_type,omitempty"`
	Meta        map[string]string `json:"meta,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
	Tags        map[string]string `json:"tags,omitempty"`
	ChecksumAlg string            `json:"checksum_alg,omitempty"`
	// ChecksumType is FULL_OBJECT or COMPOSITE (how the final checksum is built).
	ChecksumType string `json:"checksum_type,omitempty"`
}

// Part is one uploaded part.
type Part struct {
	Number       int    `json:"number"`
	Blob         string `json:"blob"`
	Size         int64  `json:"size"`
	ETag         string `json:"etag"`
	ChecksumVal  string `json:"checksum_val,omitempty"`
	LastModified int64  `json:"last_modified"`
}

// Store is the S3 storage engine.
type Store struct {
	db    *bolt.DB
	root  string // data dir: blobs/, tmp/ live under it
	clock func() time.Time
	Logf  func(format string, args ...any)
}

// Open opens (or initializes) the store under dataDir.
func Open(dataDir string) (*Store, error) {
	for _, dir := range []string{dataDir, filepath.Join(dataDir, "blobs"), filepath.Join(dataDir, "tmp")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	// Clear orphaned temp files from a previous crash.
	if entries, err := os.ReadDir(filepath.Join(dataDir, "tmp")); err == nil {
		for _, e := range entries {
			_ = os.Remove(filepath.Join(dataDir, "tmp", e.Name()))
		}
	}
	db, err := bolt.Open(filepath.Join(dataDir, "meta.bolt"), 0o600, nil)
	if err != nil {
		return nil, err
	}
	if err := schemaver.Ensure(db, "s3", schemaver.Current); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db, root: dataDir, clock: time.Now, Logf: func(string, ...any) {}}, nil
}

// Close closes the metadata database.
func (s *Store) Close() error { return s.db.Close() }

// SetClock overrides the clock (tests).
func (s *Store) SetClock(fn func() time.Time) { s.clock = fn }

func (s *Store) now() time.Time { return s.clock() }

// ---- error helpers (S3 error codes) ----

func ErrNoSuchBucket(name string) *awshttp.APIError {
	return &awshttp.APIError{Code: "NoSuchBucket", Status: 404, Message: "The specified bucket does not exist: " + name, SenderFault: true}
}

func ErrNoSuchKey(key string) *awshttp.APIError {
	return &awshttp.APIError{Code: "NoSuchKey", Status: 404, Message: "The specified key does not exist: " + key, SenderFault: true}
}

// ---- buckets ----

// CreateBucket creates a bucket; recreating an existing one succeeds (AWS
// returns 200 for the owner in us-east-1, and local buckets are always yours).
func (s *Store) CreateBucket(name string, objectLock bool) error {
	if !validBucketName(name) {
		return awshttp.Errf(400, "InvalidBucketName", "bucket name %q is not valid", name)
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists(bucketsBucket)
		if err != nil {
			return err
		}
		if b.Get([]byte(name)) != nil {
			return nil
		}
		bk := Bucket{Name: name, Created: s.now().Unix()}
		if objectLock {
			bk.ObjectLock = `<ObjectLockConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><ObjectLockEnabled>Enabled</ObjectLockEnabled></ObjectLockConfiguration>`
			bk.Versioning = "Enabled" // object lock requires versioning
		}
		raw, _ := json.Marshal(bk)
		return b.Put([]byte(name), raw)
	})
}

// GetBucket loads a bucket definition.
func (s *Store) GetBucket(name string) (*Bucket, error) {
	var out *Bucket
	err := s.db.View(func(tx *bolt.Tx) error {
		bk, err := getBucket(tx, name)
		if err != nil {
			return err
		}
		out = bk
		return nil
	})
	return out, err
}

func getBucket(tx *bolt.Tx, name string) (*Bucket, error) {
	b := tx.Bucket(bucketsBucket)
	if b == nil {
		return nil, ErrNoSuchBucket(name)
	}
	raw := b.Get([]byte(name))
	if raw == nil {
		return nil, ErrNoSuchBucket(name)
	}
	var bk Bucket
	if err := json.Unmarshal(raw, &bk); err != nil {
		return nil, err
	}
	return &bk, nil
}

// UpdateBucket applies fn to a bucket and persists it.
func (s *Store) UpdateBucket(name string, fn func(*Bucket) error) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		bk, err := getBucket(tx, name)
		if err != nil {
			return err
		}
		if err := fn(bk); err != nil {
			return err
		}
		raw, _ := json.Marshal(bk)
		return tx.Bucket(bucketsBucket).Put([]byte(name), raw)
	})
}

// DeleteBucket removes an empty bucket.
func (s *Store) DeleteBucket(name string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		if _, err := getBucket(tx, name); err != nil {
			return err
		}
		if vb := tx.Bucket(verBucket(name)); vb != nil {
			if k, _ := vb.Cursor().First(); k != nil {
				return awshttp.Errf(409, "BucketNotEmpty", "the bucket %s is not empty", name)
			}
			_ = tx.DeleteBucket(verBucket(name))
		}
		if ub := tx.Bucket(upBucket(name)); ub != nil {
			if k, _ := ub.Cursor().First(); k != nil {
				return awshttp.Errf(409, "BucketNotEmpty", "the bucket %s has in-progress multipart uploads", name)
			}
			_ = tx.DeleteBucket(upBucket(name))
		}
		_ = tx.DeleteBucket(curBucket(name))
		_ = tx.DeleteBucket(partsBucket(name))
		_ = tx.Bucket(bucketsBucket).Delete([]byte(name))
		// Remove the bucket's blob shard tree.
		_ = os.RemoveAll(filepath.Join(s.root, "blobs", name))
		return nil
	})
}

// ListBuckets returns every bucket, sorted by name (bbolt order).
func (s *Store) ListBuckets() ([]Bucket, error) {
	var out []Bucket
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketsBucket)
		if b == nil {
			return nil
		}
		return b.ForEach(func(_, raw []byte) error {
			var bk Bucket
			if json.Unmarshal(raw, &bk) == nil {
				out = append(out, bk)
			}
			return nil
		})
	})
	return out, err
}

// validBucketName enforces the S3 naming rules that matter locally.
func validBucketName(name string) bool {
	if len(name) < 3 || len(name) > 63 {
		return false
	}
	for i := range len(name) {
		c := name[i]
		ok := c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '.'
		if !ok {
			return false
		}
	}
	return name[0] != '-' && name[0] != '.' && name[len(name)-1] != '-' && name[len(name)-1] != '.'
}

// ---- version keys ----

// verKey builds the ver: bucket key: objKey \x00 ^seq — newest version first
// when cursoring forward within one object key.
func verKey(key string, seq uint64) []byte {
	out := make([]byte, 0, len(key)+9)
	out = append(out, key...)
	out = append(out, 0)
	out = binary.BigEndian.AppendUint64(out, ^seq)
	return out
}

// splitVerKey parses a ver: key back into the object key.
func splitVerKey(k []byte) (key string, ok bool) {
	if len(k) < 9 || k[len(k)-9] != 0 {
		return "", false
	}
	return string(k[:len(k)-9]), true
}

func newVersionID() string {
	var b [16]byte
	rand.Read(b[:])
	return "3z" + hex.EncodeToString(b[:]) // fixed prefix keeps ids distinguishable from "null"
}

func newID() string {
	var b [16]byte
	rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func mustJSON(v any) []byte {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("s3store: marshal %T: %v", v, err))
	}
	return raw
}
