package s3store

import (
	"encoding/json"
	"sort"
	"strings"

	bolt "go.etcd.io/bbolt"
)

// ListEntry is one object in a list result.
type ListEntry struct {
	Key          string
	Size         int64
	ETag         string
	LastModified int64
	StorageClass string
	ChecksumAlg  string
}

// ListResult is a paged ListObjects(V2) result.
type ListResult struct {
	Entries        []ListEntry
	CommonPrefixes []string
	IsTruncated    bool
	NextToken      string // next StartAfter/Marker/ContinuationToken value (a key)
}

// ListObjects walks the current (visible) objects of a bucket with
// prefix/delimiter/paging semantics shared by ListObjects and ListObjectsV2.
// after is the exclusive start key (Marker / StartAfter / decoded token).
func (s *Store) ListObjects(bucket, prefix, delimiter, after string, max int) (*ListResult, error) {
	if max <= 0 {
		max = 1000
	}
	res := &ListResult{}
	seenPrefix := map[string]bool{}
	err := s.db.View(func(tx *bolt.Tx) error {
		if _, err := getBucket(tx, bucket); err != nil {
			return err
		}
		cur := tx.Bucket(curBucket(bucket))
		if cur == nil {
			return nil
		}
		vb := tx.Bucket(verBucket(bucket))
		c := cur.Cursor()

		lastDelivered := ""
		k, verRef := c.Seek([]byte(prefix))
		for ; k != nil; k, verRef = c.Next() {
			key := string(k)
			if !strings.HasPrefix(key, prefix) {
				break
			}
			if after != "" && key <= after {
				continue
			}
			// Delimiter grouping: everything sharing the prefix up to the
			// next delimiter collapses into one CommonPrefix.
			if delimiter != "" {
				rest := key[len(prefix):]
				if i := strings.Index(rest, delimiter); i >= 0 {
					cp := prefix + rest[:i+len(delimiter)]
					if !seenPrefix[cp] {
						if len(res.Entries)+len(seenPrefix) >= max {
							res.IsTruncated = true
							res.NextToken = lastDelivered
							return nil
						}
						seenPrefix[cp] = true
					}
					lastDelivered = key
					continue
				}
			}
			if len(res.Entries)+len(seenPrefix) >= max {
				res.IsTruncated = true
				res.NextToken = lastDelivered
				return nil
			}
			lastDelivered = key
			var v ObjectVersion
			if raw := vb.Get(verRef); raw == nil || json.Unmarshal(raw, &v) != nil {
				continue
			}
			res.Entries = append(res.Entries, ListEntry{
				Key: v.Key, Size: v.Size, ETag: v.ETag,
				LastModified: v.LastModified,
				StorageClass: orStandard(v.StorageClass),
				ChecksumAlg:  v.ChecksumAlg,
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for cp := range seenPrefix {
		res.CommonPrefixes = append(res.CommonPrefixes, cp)
	}
	sort.Strings(res.CommonPrefixes)
	return res, nil
}

// VersionEntry is one row of a ListObjectVersions result.
type VersionEntry struct {
	ObjectVersion
	IsLatest bool
}

// VersionsResult is a paged ListObjectVersions result.
type VersionsResult struct {
	Versions        []VersionEntry
	CommonPrefixes  []string
	IsTruncated     bool
	NextKeyMarker   string
	NextVersionMark string
}

// ListVersions walks every version (delete markers included), newest-first
// within each key.
func (s *Store) ListVersions(bucket, prefix, delimiter, keyMarker, versionMarker string, max int) (*VersionsResult, error) {
	if max <= 0 {
		max = 1000
	}
	res := &VersionsResult{}
	seenPrefix := map[string]bool{}
	err := s.db.View(func(tx *bolt.Tx) error {
		if _, err := getBucket(tx, bucket); err != nil {
			return err
		}
		vb := tx.Bucket(verBucket(bucket))
		if vb == nil {
			return nil
		}
		c := vb.Cursor()
		latestSeen := map[string]bool{}
		skipping := keyMarker != "" // resume-from-marker mode

		for k, raw := c.Seek([]byte(prefix)); k != nil; k, raw = c.Next() {
			key, ok := splitVerKey(k)
			if !ok || !strings.HasPrefix(key, prefix) {
				break
			}
			var v ObjectVersion
			if json.Unmarshal(raw, &v) != nil {
				continue
			}
			if skipping {
				switch {
				case key < keyMarker:
					continue
				case key == keyMarker && versionMarker == "":
					continue // no version marker: resume strictly after this key
				case key == keyMarker && v.VersionID != versionMarker:
					continue // still before the marker version
				case key == keyMarker && v.VersionID == versionMarker:
					skipping = false
					continue // the marker pair itself was already delivered
				default:
					skipping = false // first key past the marker
				}
			}
			if delimiter != "" {
				rest := key[len(prefix):]
				if i := strings.Index(rest, delimiter); i >= 0 {
					cp := prefix + rest[:i+len(delimiter)]
					if !seenPrefix[cp] {
						seenPrefix[cp] = true
					}
					continue
				}
			}
			if len(res.Versions) >= max {
				res.IsTruncated = true
				if n := len(res.Versions); n > 0 {
					res.NextKeyMarker = res.Versions[n-1].Key
					res.NextVersionMark = res.Versions[n-1].VersionID
				}
				return nil
			}
			isLatest := !latestSeen[key]
			latestSeen[key] = true
			v.seqKey = append([]byte(nil), k...)
			res.Versions = append(res.Versions, VersionEntry{ObjectVersion: v, IsLatest: isLatest})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for cp := range seenPrefix {
		res.CommonPrefixes = append(res.CommonPrefixes, cp)
	}
	sort.Strings(res.CommonPrefixes)
	return res, nil
}

func orStandard(sc string) string {
	if sc == "" {
		return "STANDARD"
	}
	return sc
}
