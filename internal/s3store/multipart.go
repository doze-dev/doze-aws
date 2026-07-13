package s3store

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"sort"
	"strings"

	bolt "go.etcd.io/bbolt"

	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/checksum"
)

func partKey(uploadID string, num int) []byte {
	out := append([]byte(uploadID), 0)
	return binary.BigEndian.AppendUint32(out, uint32(num))
}

// CreateUpload starts a multipart upload.
func (s *Store) CreateUpload(bucket string, up Upload) (*Upload, error) {
	up.ID = newID() + newID() // long ids look the part
	up.Initiated = s.now().Unix()
	err := s.db.Update(func(tx *bolt.Tx) error {
		if _, err := getBucket(tx, bucket); err != nil {
			return err
		}
		b, err := tx.CreateBucketIfNotExists(upBucket(bucket))
		if err != nil {
			return err
		}
		raw, err := marshalJSON(up)
		if err != nil {
			return err
		}
		return b.Put([]byte(up.ID), raw)
	})
	if err != nil {
		return nil, err
	}
	return &up, nil
}

// GetUpload loads an in-progress upload.
func (s *Store) GetUpload(bucket, uploadID string) (*Upload, error) {
	var out *Upload
	err := s.db.View(func(tx *bolt.Tx) error {
		if _, err := getBucket(tx, bucket); err != nil {
			return err
		}
		b := tx.Bucket(upBucket(bucket))
		if b == nil {
			return errNoSuchUpload(uploadID)
		}
		raw := b.Get([]byte(uploadID))
		if raw == nil {
			return errNoSuchUpload(uploadID)
		}
		var up Upload
		if err := json.Unmarshal(raw, &up); err != nil {
			return err
		}
		out = &up
		return nil
	})
	return out, err
}

func errNoSuchUpload(id string) *awshttp.APIError {
	return &awshttp.APIError{Code: "NoSuchUpload", Status: 404, Message: "upload " + id + " does not exist or was completed/aborted", SenderFault: true}
}

// PutPart records an uploaded part (blob already written). Re-uploading a part
// number replaces it.
func (s *Store) PutPart(bucket, uploadID string, part Part) error {
	var replaced string
	err := s.db.Update(func(tx *bolt.Tx) error {
		if _, err := getBucket(tx, bucket); err != nil {
			return err
		}
		ub := tx.Bucket(upBucket(bucket))
		if ub == nil || ub.Get([]byte(uploadID)) == nil {
			return errNoSuchUpload(uploadID)
		}
		pb, err := tx.CreateBucketIfNotExists(partsBucket(bucket))
		if err != nil {
			return err
		}
		key := partKey(uploadID, part.Number)
		if raw := pb.Get(key); raw != nil {
			var old Part
			if json.Unmarshal(raw, &old) == nil {
				replaced = old.Blob
			}
		}
		part.LastModified = s.now().Unix()
		raw, err := marshalJSON(part)
		if err != nil {
			return err
		}
		return pb.Put(key, raw)
	})
	if err == nil && replaced != "" {
		s.DeleteBlob(replaced)
	}
	return err
}

// Parts lists an upload's parts in part-number order.
func (s *Store) Parts(bucket, uploadID string) ([]Part, error) {
	var out []Part
	err := s.db.View(func(tx *bolt.Tx) error {
		if _, err := getBucket(tx, bucket); err != nil {
			return err
		}
		ub := tx.Bucket(upBucket(bucket))
		if ub == nil || ub.Get([]byte(uploadID)) == nil {
			return errNoSuchUpload(uploadID)
		}
		pb := tx.Bucket(partsBucket(bucket))
		if pb == nil {
			return nil
		}
		prefix := append([]byte(uploadID), 0)
		c := pb.Cursor()
		for k, raw := c.Seek(prefix); k != nil && hasPrefix(k, prefix); k, raw = c.Next() {
			var p Part
			if json.Unmarshal(raw, &p) == nil {
				out = append(out, p)
			}
		}
		return nil
	})
	return out, err
}

// CompletedPart is the client's view of one part in CompleteMultipartUpload.
type CompletedPart struct {
	Number      int
	ETag        string
	ChecksumVal string // optional client-declared checksum
}

// minPartSize is S3's floor for every part but the last.
const minPartSize = 5 << 20

// CompleteUpload validates the part list, assembles the final blob, computes
// the multipart ETag ("md5-of-md5s-N") and composite/full checksum, commits
// the object version, and cleans up the upload. checksumFull computes the
// FULL_OBJECT checksum from the assembled blob when the algorithm needs it.
func (s *Store) CompleteUpload(bucket, uploadID string, declared []CompletedPart, ifNoneMatch, ifMatch string) (*ObjectVersion, error) {
	up, err := s.GetUpload(bucket, uploadID)
	if err != nil {
		return nil, err
	}
	stored, err := s.Parts(bucket, uploadID)
	if err != nil {
		return nil, err
	}
	byNum := map[int]Part{}
	for _, p := range stored {
		byNum[p.Number] = p
	}

	if len(declared) == 0 {
		return nil, awshttp.Errf(400, "MalformedXML", "CompleteMultipartUpload requires at least one part")
	}
	var blobs []string
	var partMD5s [][]byte
	var partSums [][]byte
	var totalSize int64
	last := 0
	for i, d := range declared {
		if d.Number <= last {
			return nil, awshttp.Errf(400, "InvalidPartOrder", "parts must be listed in ascending order")
		}
		last = d.Number
		p, ok := byNum[d.Number]
		if !ok || trimETag(d.ETag) != p.ETag {
			return nil, awshttp.Errf(400, "InvalidPart", "part %d was not uploaded or its ETag does not match", d.Number)
		}
		if i < len(declared)-1 && p.Size < minPartSize {
			return nil, awshttp.Errf(400, "EntityTooSmall", "part %d is %d bytes; every part but the last must be at least %d bytes", d.Number, p.Size, minPartSize)
		}
		blobs = append(blobs, p.Blob)
		totalSize += p.Size
		md5raw, err := hexDecode(p.ETag)
		if err != nil {
			return nil, awshttp.Errf(500, "InternalError", "stored part ETag is corrupt")
		}
		partMD5s = append(partMD5s, md5raw)
		if p.ChecksumVal != "" {
			if sum, err := base64.StdEncoding.DecodeString(p.ChecksumVal); err == nil {
				partSums = append(partSums, sum)
			}
		}
	}

	blob, size, err := s.ConcatBlobs(bucket, blobs)
	if err != nil {
		return nil, err
	}
	if size != totalSize {
		s.DeleteBlob(blob)
		return nil, awshttp.Errf(500, "InternalError", "assembled size mismatch")
	}

	v := ObjectVersion{
		Key:          up.Key,
		Blob:         blob,
		Size:         size,
		ETag:         multipartETag(partMD5s),
		ContentType:  up.ContentType,
		Meta:         up.Meta,
		Headers:      up.Headers,
		Tags:         up.Tags,
		ChecksumAlg:  up.ChecksumAlg,
		ChecksumType: up.ChecksumType,
	}
	if up.ChecksumAlg != "" {
		switch strings.ToUpper(up.ChecksumType) {
		case "FULL_OBJECT":
			// Recompute over the assembled blob (CRC algorithms combine; a
			// full pass is simpler and local disks are fast).
			f, err := s.OpenBlob(blob)
			if err == nil {
				if h, herr := checksum.New(up.ChecksumAlg); herr == nil {
					copyAll(h, f)
					v.ChecksumVal = checksum.Encode(h.Sum(nil))
				}
				f.Close()
			}
		default: // COMPOSITE
			if len(partSums) == len(declared) {
				if comp, err := checksum.Composite(up.ChecksumAlg, partSums); err == nil {
					v.ChecksumVal = comp
					v.ChecksumType = "COMPOSITE"
				}
			}
		}
	}

	out, err := s.PutVersion(bucket, v, ifNoneMatch, ifMatch)
	if err != nil {
		s.DeleteBlob(blob)
		return nil, err
	}
	s.dropUpload(bucket, uploadID, blobs)
	return out, nil
}

// AbortUpload discards an upload and its part blobs.
func (s *Store) AbortUpload(bucket, uploadID string) error {
	if _, err := s.GetUpload(bucket, uploadID); err != nil {
		return err
	}
	parts, _ := s.Parts(bucket, uploadID)
	var blobs []string
	for _, p := range parts {
		blobs = append(blobs, p.Blob)
	}
	s.dropUpload(bucket, uploadID, blobs)
	return nil
}

// dropUpload removes upload metadata, part records, and part blobs.
func (s *Store) dropUpload(bucket, uploadID string, blobs []string) {
	_ = s.db.Update(func(tx *bolt.Tx) error {
		if ub := tx.Bucket(upBucket(bucket)); ub != nil {
			_ = ub.Delete([]byte(uploadID))
		}
		if pb := tx.Bucket(partsBucket(bucket)); pb != nil {
			prefix := append([]byte(uploadID), 0)
			c := pb.Cursor()
			var stale [][]byte
			for k, _ := c.Seek(prefix); k != nil && hasPrefix(k, prefix); k, _ = c.Next() {
				stale = append(stale, append([]byte(nil), k...))
			}
			for _, k := range stale {
				_ = pb.Delete(k)
			}
		}
		return nil
	})
	for _, b := range blobs {
		s.DeleteBlob(b)
	}
}

// Uploads lists in-progress uploads, sorted by key then id.
func (s *Store) Uploads(bucket, prefix string) ([]Upload, error) {
	var out []Upload
	err := s.db.View(func(tx *bolt.Tx) error {
		if _, err := getBucket(tx, bucket); err != nil {
			return err
		}
		b := tx.Bucket(upBucket(bucket))
		if b == nil {
			return nil
		}
		return b.ForEach(func(_, raw []byte) error {
			var up Upload
			if json.Unmarshal(raw, &up) == nil && strings.HasPrefix(up.Key, prefix) {
				out = append(out, up)
			}
			return nil
		})
	})
	sort.Slice(out, func(i, j int) bool {
		if out[i].Key != out[j].Key {
			return out[i].Key < out[j].Key
		}
		return out[i].ID < out[j].ID
	})
	return out, err
}
