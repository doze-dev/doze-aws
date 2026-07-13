package s3store

import (
	"encoding/json"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/doze-dev/doze-aws/internal/awshttp"
)

// PutVersion commits a fully-written blob as the new version of a key,
// honoring the bucket's versioning state. Returns the stored version.
//
// Conditional writes: ifNoneMatch ("*" — fail if the key exists) and ifMatch
// (fail unless the current ETag matches) implement S3's conditional PUT.
func (s *Store) PutVersion(bucket string, v ObjectVersion, ifNoneMatch, ifMatch string) (*ObjectVersion, error) {
	var replaced string // blob to delete after commit (unversioned overwrite)
	err := s.db.Update(func(tx *bolt.Tx) error {
		bk, err := getBucket(tx, bucket)
		if err != nil {
			return err
		}
		cur, _ := tx.CreateBucketIfNotExists(curBucket(bucket))
		vb, _ := tx.CreateBucketIfNotExists(verBucket(bucket))

		current := currentVersion(tx, bucket, v.Key)
		if ifNoneMatch == "*" && current != nil && !current.DeleteMarker {
			return awshttp.Errf(412, "PreconditionFailed", "object %q already exists", v.Key)
		}
		if ifMatch != "" && (current == nil || trimETag(ifMatch) != current.ETag) {
			return awshttp.Errf(412, "PreconditionFailed", "ETag precondition failed for %q", v.Key)
		}

		v.LastModified = s.now().Unix()
		seq, _ := vb.NextSequence()
		switch bk.Versioning {
		case "Enabled":
			v.VersionID = newVersionID()
		default:
			// Unversioned ("" ) and Suspended both write the "null" version,
			// replacing any existing "null" version in place.
			v.VersionID = "null"
			if old := findVersion(tx, bucket, v.Key, "null"); old != nil {
				replaced = old.Blob
				_ = vb.Delete(old.seqKey)
			}
		}
		key := verKey(v.Key, seq)
		raw, merr := marshalJSON(v)
		if merr != nil {
			return merr
		}
		if err := vb.Put(key, raw); err != nil {
			return err
		}
		v.seqKey = key
		return cur.Put([]byte(v.Key), key)
	})
	if err != nil {
		return nil, err
	}
	if replaced != "" && replaced != v.Blob {
		s.DeleteBlob(replaced)
	}
	return &v, nil
}

// currentVersion returns the newest version record for a key (delete markers
// included), or nil.
func currentVersion(tx *bolt.Tx, bucket, key string) *ObjectVersion {
	vb := tx.Bucket(verBucket(bucket))
	if vb == nil {
		return nil
	}
	c := vb.Cursor()
	prefix := append([]byte(key), 0)
	k, raw := c.Seek(prefix)
	if k == nil || len(k) < len(prefix) || string(k[:len(prefix)]) != string(prefix) {
		return nil
	}
	var v ObjectVersion
	if json.Unmarshal(raw, &v) != nil {
		return nil
	}
	v.seqKey = append([]byte(nil), k...)
	return &v
}

// findVersion locates a specific version of a key, or nil.
func findVersion(tx *bolt.Tx, bucket, key, versionID string) *ObjectVersion {
	vb := tx.Bucket(verBucket(bucket))
	if vb == nil {
		return nil
	}
	c := vb.Cursor()
	prefix := append([]byte(key), 0)
	for k, raw := c.Seek(prefix); k != nil && hasPrefix(k, prefix); k, raw = c.Next() {
		var v ObjectVersion
		if json.Unmarshal(raw, &v) != nil {
			continue
		}
		if v.VersionID == versionID {
			v.seqKey = append([]byte(nil), k...)
			return &v
		}
	}
	return nil
}

func hasPrefix(k, prefix []byte) bool {
	return len(k) >= len(prefix) && string(k[:len(prefix)]) == string(prefix)
}

// GetVersion fetches a key's current version, or a specific one. Delete
// markers surface as (version, MethodNotAllowed-flavored 404) the way S3
// reports them.
func (s *Store) GetVersion(bucket, key, versionID string) (*ObjectVersion, error) {
	var out *ObjectVersion
	err := s.db.View(func(tx *bolt.Tx) error {
		if _, err := getBucket(tx, bucket); err != nil {
			return err
		}
		var v *ObjectVersion
		if versionID == "" {
			v = currentVersion(tx, bucket, key)
		} else {
			v = findVersion(tx, bucket, key, versionID)
			if v == nil {
				return awshttp.Errf(404, "NoSuchVersion", "version %s of %q does not exist", versionID, key)
			}
		}
		if v == nil {
			return ErrNoSuchKey(key)
		}
		out = v
		return nil
	})
	return out, err
}

// DeleteObject implements DELETE semantics:
//   - versionID given: remove that exact version (and its blob);
//   - no versionID, versioning Enabled: insert a delete marker;
//   - no versionID, otherwise: remove the "null"/current version.
//
// Returns (deleteMarkerCreated, versionIDAffected).
func (s *Store) DeleteObject(bucket, key, versionID string, bypassGovernance bool) (bool, string, error) {
	var marker bool
	var affected string
	var dropBlobs []string
	err := s.db.Update(func(tx *bolt.Tx) error {
		bk, err := getBucket(tx, bucket)
		if err != nil {
			return err
		}
		vb, _ := tx.CreateBucketIfNotExists(verBucket(bucket))
		cur, _ := tx.CreateBucketIfNotExists(curBucket(bucket))

		if versionID != "" {
			v := findVersion(tx, bucket, key, versionID)
			if v == nil {
				return nil // deleting a missing version is a no-op, like S3
			}
			if aerr := s.lockBlocksDeletion(v, bypassGovernance); aerr != nil {
				return aerr
			}
			_ = vb.Delete(v.seqKey)
			if !v.DeleteMarker {
				dropBlobs = append(dropBlobs, v.Blob)
			}
			affected = versionID
			s.recomputeCurrent(tx, bucket, key, cur)
			return nil
		}

		if bk.Versioning == "Enabled" {
			seq, _ := vb.NextSequence()
			dm := ObjectVersion{
				Key: key, VersionID: newVersionID(), DeleteMarker: true,
				LastModified: s.now().Unix(),
			}
			raw, merr := marshalJSON(dm)
			if merr != nil {
				return merr
			}
			if err := vb.Put(verKey(key, seq), raw); err != nil {
				return err
			}
			_ = cur.Delete([]byte(key)) // key is now invisible to plain lists
			marker, affected = true, dm.VersionID
			return nil
		}

		if bk.Versioning == "Suspended" {
			// Suspended: insert a "null" delete marker, replacing any existing
			// "null" version. Non-null versions written while versioning was
			// Enabled are preserved (real S3 does not destroy them).
			if old := findVersion(tx, bucket, key, "null"); old != nil {
				if aerr := s.lockBlocksDeletion(old, bypassGovernance); aerr != nil {
					return aerr
				}
				if !old.DeleteMarker {
					dropBlobs = append(dropBlobs, old.Blob)
				}
				_ = vb.Delete(old.seqKey)
			}
			seq, _ := vb.NextSequence()
			dm := ObjectVersion{
				Key: key, VersionID: "null", DeleteMarker: true,
				LastModified: s.now().Unix(),
			}
			raw, merr := marshalJSON(dm)
			if merr != nil {
				return merr
			}
			if err := vb.Put(verKey(key, seq), raw); err != nil {
				return err
			}
			_ = cur.Delete([]byte(key))
			marker, affected = true, "null"
			return nil
		}

		// Truly unversioned bucket: drop the current version outright.
		if v := currentVersion(tx, bucket, key); v != nil {
			if aerr := s.lockBlocksDeletion(v, bypassGovernance); aerr != nil {
				return aerr
			}
			_ = vb.Delete(v.seqKey)
			if !v.DeleteMarker {
				dropBlobs = append(dropBlobs, v.Blob)
			}
			s.recomputeCurrent(tx, bucket, key, cur)
		}
		return nil
	})
	for _, b := range dropBlobs {
		s.DeleteBlob(b)
	}
	return marker, affected, err
}

// lockBlocksDeletion enforces object lock: legal holds always block;
// COMPLIANCE retention always blocks until expiry; GOVERNANCE blocks unless
// bypassed.
func (s *Store) lockBlocksDeletion(v *ObjectVersion, bypassGovernance bool) *awshttp.APIError {
	if v.LegalHold {
		return awshttp.Errf(403, "AccessDenied", "object version %s is under legal hold", v.VersionID)
	}
	if v.RetainUntil > s.now().Unix() {
		if v.RetainMode == "GOVERNANCE" && bypassGovernance {
			return nil
		}
		return awshttp.Errf(403, "AccessDenied",
			"object version %s is locked (%s retention until %s)",
			v.VersionID, v.RetainMode, time.Unix(v.RetainUntil, 0).UTC().Format(time.RFC3339))
	}
	return nil
}

// recomputeCurrent repoints cur:<bucket>[key] at the newest surviving
// non-delete-marker version, or clears it.
func (s *Store) recomputeCurrent(tx *bolt.Tx, bucket, key string, cur *bolt.Bucket) {
	v := currentVersion(tx, bucket, key)
	if v == nil || v.DeleteMarker {
		_ = cur.Delete([]byte(key))
		return
	}
	_ = cur.Put([]byte(key), v.seqKey)
}

// UpdateVersion persists a mutation (tagging, lock fields) to an existing
// version record.
func (s *Store) UpdateVersion(bucket string, v *ObjectVersion, fn func(*ObjectVersion) error) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		vb := tx.Bucket(verBucket(bucket))
		if vb == nil {
			return ErrNoSuchKey(v.Key)
		}
		raw := vb.Get(v.seqKey)
		if raw == nil {
			return ErrNoSuchKey(v.Key)
		}
		var fresh ObjectVersion
		if err := json.Unmarshal(raw, &fresh); err != nil {
			return err
		}
		fresh.seqKey = v.seqKey
		if err := fn(&fresh); err != nil {
			return err
		}
		*v = fresh
		raw, err := marshalJSON(fresh)
		if err != nil {
			return err
		}
		return vb.Put(v.seqKey, raw)
	})
}

func trimETag(etag string) string {
	if len(etag) >= 2 && etag[0] == '"' && etag[len(etag)-1] == '"' {
		return etag[1 : len(etag)-1]
	}
	return etag
}
