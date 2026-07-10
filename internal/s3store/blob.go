package s3store

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// WriteBlob streams r into a new blob file for the bucket (temp file + fsync +
// rename, so a crash never leaves a partial blob visible) and returns the
// blob's store-relative path and size.
func (s *Store) WriteBlob(bucket string, r io.Reader) (blob string, size int64, err error) {
	id := newID()
	rel := filepath.Join("blobs", bucket, id[:2], id)
	abs := filepath.Join(s.root, rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return "", 0, err
	}
	tmp, err := os.CreateTemp(filepath.Join(s.root, "tmp"), "put-*")
	if err != nil {
		return "", 0, err
	}
	defer func() {
		tmp.Close()
		os.Remove(tmp.Name()) // no-op after a successful rename
	}()
	size, err = io.Copy(tmp, r)
	if err != nil {
		return "", 0, err
	}
	if err := tmp.Sync(); err != nil {
		return "", 0, err
	}
	if err := tmp.Close(); err != nil {
		return "", 0, err
	}
	if err := os.Rename(tmp.Name(), abs); err != nil {
		return "", 0, err
	}
	return rel, size, nil
}

// OpenBlob opens a blob for streaming reads (supports ranges via Seek).
func (s *Store) OpenBlob(blob string) (*os.File, error) {
	if blob == "" {
		return nil, fmt.Errorf("s3store: version has no blob")
	}
	return os.Open(filepath.Join(s.root, blob))
}

// DeleteBlob removes a blob file; missing files are fine (idempotent).
func (s *Store) DeleteBlob(blob string) {
	if blob == "" {
		return
	}
	_ = os.Remove(filepath.Join(s.root, blob))
}

// ConcatBlobs streams the given blobs, in order, into one new blob —
// CompleteMultipartUpload's assembly step. Constant memory.
func (s *Store) ConcatBlobs(bucket string, blobs []string) (blob string, size int64, err error) {
	readers := make([]io.Reader, 0, len(blobs))
	files := make([]*os.File, 0, len(blobs))
	defer func() {
		for _, f := range files {
			f.Close()
		}
	}()
	for _, b := range blobs {
		f, err := s.OpenBlob(b)
		if err != nil {
			return "", 0, err
		}
		files = append(files, f)
		readers = append(readers, f)
	}
	return s.WriteBlob(bucket, io.MultiReader(readers...))
}
