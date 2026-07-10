package s3store

import (
	"io"
	"strings"
	"testing"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func putObj(t *testing.T, s *Store, bucket, key, body string) *ObjectVersion {
	t.Helper()
	blob, size, err := s.WriteBlob(bucket, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	v, err := s.PutVersion(bucket, ObjectVersion{Key: key, Blob: blob, Size: size, ETag: "etag-" + body}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func readVersion(t *testing.T, s *Store, bucket, key, versionID string) string {
	t.Helper()
	v, err := s.GetVersion(bucket, key, versionID)
	if err != nil {
		t.Fatal(err)
	}
	if v.DeleteMarker {
		return "<delete marker>"
	}
	f, err := s.OpenBlob(v.Blob)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	b, _ := io.ReadAll(f)
	return string(b)
}

func TestUnversionedOverwriteReplacesBlob(t *testing.T) {
	s := testStore(t)
	if err := s.CreateBucket("bkt", false); err != nil {
		t.Fatal(err)
	}
	putObj(t, s, "bkt", "k", "one")
	first, _ := s.GetVersion("bkt", "k", "")
	putObj(t, s, "bkt", "k", "two")

	if got := readVersion(t, s, "bkt", "k", ""); got != "two" {
		t.Errorf("current = %q", got)
	}
	// The replaced version's blob is gone and only one version remains.
	if _, err := s.OpenBlob(first.Blob); err == nil {
		t.Error("old blob still exists after unversioned overwrite")
	}
	res, err := s.ListVersions("bkt", "", "", "", "", 100)
	if err != nil || len(res.Versions) != 1 {
		t.Fatalf("versions = %d, err = %v", len(res.Versions), err)
	}
	if res.Versions[0].VersionID != "null" {
		t.Errorf("unversioned VersionID = %q", res.Versions[0].VersionID)
	}
}

func TestVersioningLifecycle(t *testing.T) {
	s := testStore(t)
	if err := s.CreateBucket("bkt", false); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateBucket("bkt", func(bk *Bucket) error { bk.Versioning = "Enabled"; return nil }); err != nil {
		t.Fatal(err)
	}
	v1 := putObj(t, s, "bkt", "k", "one")
	v2 := putObj(t, s, "bkt", "k", "two")
	if v1.VersionID == v2.VersionID || v1.VersionID == "null" {
		t.Fatalf("version ids: %q %q", v1.VersionID, v2.VersionID)
	}

	// Current is v2; v1 retrievable by id.
	if got := readVersion(t, s, "bkt", "k", ""); got != "two" {
		t.Errorf("current = %q", got)
	}
	if got := readVersion(t, s, "bkt", "k", v1.VersionID); got != "one" {
		t.Errorf("v1 = %q", got)
	}

	// Plain delete inserts a marker; key vanishes from lists but versions remain.
	marker, markerID, err := s.DeleteObject("bkt", "k", "", false)
	if err != nil || !marker || markerID == "" {
		t.Fatalf("delete: marker=%v id=%q err=%v", marker, markerID, err)
	}
	if v, err := s.GetVersion("bkt", "k", ""); err != nil || !v.DeleteMarker {
		t.Errorf("current after delete should be the marker: %+v, %v", v, err)
	}
	list, _ := s.ListObjects("bkt", "", "", "", 100)
	if len(list.Entries) != 0 {
		t.Errorf("listed %d entries after delete marker", len(list.Entries))
	}
	vers, _ := s.ListVersions("bkt", "", "", "", "", 100)
	if len(vers.Versions) != 3 { // v1, v2, marker
		t.Fatalf("versions = %d", len(vers.Versions))
	}
	if !vers.Versions[0].IsLatest || !vers.Versions[0].DeleteMarker {
		t.Errorf("newest version should be the latest delete marker: %+v", vers.Versions[0])
	}

	// Deleting the marker by id restores the key.
	if _, _, err := s.DeleteObject("bkt", "k", markerID, false); err != nil {
		t.Fatal(err)
	}
	if got := readVersion(t, s, "bkt", "k", ""); got != "two" {
		t.Errorf("after marker removal, current = %q", got)
	}
}

func TestConditionalPut(t *testing.T) {
	s := testStore(t)
	s.CreateBucket("bkt", false)
	putObj(t, s, "bkt", "k", "one")

	// If-None-Match: * fails on existing keys.
	blob, size, _ := s.WriteBlob("bkt", strings.NewReader("x"))
	_, err := s.PutVersion("bkt", ObjectVersion{Key: "k", Blob: blob, Size: size, ETag: "e"}, "*", "")
	if err == nil {
		t.Fatal("If-None-Match:* PUT over existing key succeeded")
	}
	// If-Match with the wrong etag fails.
	blob2, size2, _ := s.WriteBlob("bkt", strings.NewReader("y"))
	if _, err := s.PutVersion("bkt", ObjectVersion{Key: "k", Blob: blob2, Size: size2, ETag: "e"}, "", "wrong"); err == nil {
		t.Fatal("If-Match with wrong etag succeeded")
	}
	// If-Match with the right etag succeeds.
	blob3, size3, _ := s.WriteBlob("bkt", strings.NewReader("z"))
	if _, err := s.PutVersion("bkt", ObjectVersion{Key: "k", Blob: blob3, Size: size3, ETag: "e2"}, "", `"etag-one"`); err != nil {
		t.Fatalf("If-Match with right etag failed: %v", err)
	}
}

func TestListDelimiterAndPaging(t *testing.T) {
	s := testStore(t)
	s.CreateBucket("bkt", false)
	for _, k := range []string{"a.txt", "dir/one", "dir/two", "dir2/x", "z.txt"} {
		putObj(t, s, "bkt", k, "v")
	}

	res, err := s.ListObjects("bkt", "", "/", "", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != 2 || res.Entries[0].Key != "a.txt" || res.Entries[1].Key != "z.txt" {
		t.Errorf("entries = %+v", res.Entries)
	}
	if len(res.CommonPrefixes) != 2 || res.CommonPrefixes[0] != "dir/" || res.CommonPrefixes[1] != "dir2/" {
		t.Errorf("prefixes = %v", res.CommonPrefixes)
	}

	// Paging: 2 at a time across all 5 keys.
	var got []string
	after := ""
	for range 10 {
		res, err := s.ListObjects("bkt", "", "", after, 2)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range res.Entries {
			got = append(got, e.Key)
		}
		if !res.IsTruncated {
			break
		}
		after = res.NextToken
	}
	if len(got) != 5 {
		t.Errorf("paged keys = %v", got)
	}
}

func TestMultipartAssembly(t *testing.T) {
	s := testStore(t)
	s.CreateBucket("bkt", false)
	up, err := s.CreateUpload("bkt", Upload{Key: "big", ContentType: "text/plain"})
	if err != nil {
		t.Fatal(err)
	}
	// Two parts; part 1 must be >= 5 MiB.
	part1 := strings.Repeat("a", minPartSize)
	part2 := "the-tail"
	var declared []CompletedPart
	for i, body := range []string{part1, part2} {
		blob, size, err := s.WriteBlob("bkt", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		etag := md5hex(body)
		if err := s.PutPart("bkt", up.ID, Part{Number: i + 1, Blob: blob, Size: size, ETag: etag}); err != nil {
			t.Fatal(err)
		}
		declared = append(declared, CompletedPart{Number: i + 1, ETag: etag})
	}
	v, err := s.CompleteUpload("bkt", up.ID, declared, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if v.Size != int64(len(part1)+len(part2)) {
		t.Errorf("size = %d", v.Size)
	}
	if !strings.HasSuffix(v.ETag, "-2") {
		t.Errorf("multipart etag = %q", v.ETag)
	}
	if got := readVersion(t, s, "bkt", "big", ""); got != part1+part2 {
		t.Errorf("assembled content wrong (len %d)", len(got))
	}
	// Upload is gone.
	if _, err := s.GetUpload("bkt", up.ID); err == nil {
		t.Error("upload still exists after completion")
	}

	// Out-of-order part lists are rejected.
	up2, _ := s.CreateUpload("bkt", Upload{Key: "big2"})
	blob, size, _ := s.WriteBlob("bkt", strings.NewReader("x"))
	s.PutPart("bkt", up2.ID, Part{Number: 1, Blob: blob, Size: size, ETag: md5hex("x")})
	_, err = s.CompleteUpload("bkt", up2.ID, []CompletedPart{{Number: 2, ETag: "e"}, {Number: 1, ETag: "e"}}, "", "")
	if err == nil {
		t.Error("descending part order accepted")
	}
}

func TestObjectLockBlocksDeletion(t *testing.T) {
	s := testStore(t)
	s.CreateBucket("bkt", true) // object lock on => versioning on
	v := putObj(t, s, "bkt", "locked", "data")

	future := s.now().Unix() + 3600
	if err := s.UpdateVersion("bkt", v, func(ov *ObjectVersion) error {
		ov.RetainMode, ov.RetainUntil = "COMPLIANCE", future
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.DeleteObject("bkt", "locked", v.VersionID, false); err == nil {
		t.Fatal("COMPLIANCE-locked version deleted")
	}
	// GOVERNANCE yields to the bypass flag.
	if err := s.UpdateVersion("bkt", v, func(ov *ObjectVersion) error {
		ov.RetainMode = "GOVERNANCE"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.DeleteObject("bkt", "locked", v.VersionID, false); err == nil {
		t.Fatal("GOVERNANCE lock ignored without bypass")
	}
	if _, _, err := s.DeleteObject("bkt", "locked", v.VersionID, true); err != nil {
		t.Fatalf("governance bypass failed: %v", err)
	}
}

func md5hex(s string) string {
	h, _ := hexSum(s)
	return h
}
