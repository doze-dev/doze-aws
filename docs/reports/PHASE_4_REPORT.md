# Phase 4 report — S3 from scratch

Date: 2026-07-11

## Scope delivered

The gofakes3 replacement: a complete REST-XML S3 implementation over a new
storage engine, fixing every known gap that motivated the rewrite (no
versioning, no aws-chunked, no trailer checksums).

- **internal/awschunk**: streaming aws-chunked decoder — signed and unsigned
  chunk framing, trailer capture, malformed-input errors. Fuzzed (decoder can
  never emit more bytes than it consumed).
- **internal/checksum**: CRC32, CRC32C, SHA1, SHA256 from stdlib plus an
  in-house table-driven CRC64NVME (verified against the standard check value
  0xAE8B14860A799888); composite "-N" multipart form.
- **internal/s3store**: bbolt metadata + one blob file per object version
  (temp-file + fsync + rename; streamed only — flat-memory invariant).
  Version records keyed `objKey \x00 ^seq` so newest-first cursors are free;
  `cur:` pointers make visible listings O(keys). Versioning state machine
  (Enabled/Suspended/null versions, delete markers, current recomputation),
  conditional puts, multipart with stream-concat assembly, object-lock
  enforcement, list paging with exclusive-resume markers.
- **s3 service**: both addressing styles; method+query router; the full
  upload matrix through one ingest pipeline (chunked decode → tee MD5 +
  checksum hash → blob → trailer/header verification); conditional
  reads/writes; ranges; CopyObject + UploadPartCopy with source ranges;
  batch delete; tagging; GetObjectAttributes; CORS preflight evaluation;
  bucket-website index/error serving; lifecycle janitor (current, noncurrent,
  stale-upload rules); object lock end to end; presigned expiry enforced at
  the service as well as the gateway. Tier C configs stored and returned.

## Bugs caught by tests before they shipped

- List pagination markers pointed at the first UNdelivered key while resume
  treated markers as exclusive — keys were silently skipped across pages
  (caught by white-box paging test; fixed to last-delivered-key markers).
- PutVersion returned versions without their store key, breaking follow-up
  UpdateVersion calls (caught by the object-lock test).
- Website index serving was unreachable: directory keys never resolve, so the
  error-document branch always won (caught by the website feature test).
- Two test-authoring traps worth remembering: bucket names under 3 chars are
  rejected (twice!), and CRC32("Hello, world!") is 0xEBE6C6E6 — verify test
  vectors externally.

## Test evidence

- **aws-sdk-go-v2 contract tests**: PutObject over STREAMING-UNSIGNED-PAYLOAD-
  TRAILER (the SDK default that broke gofakes3), explicit SHA256 checksums
  verified server-side and validated client-side on GET, ranges, versioning
  lifecycle (markers, get-by-version, marker removal), multipart (5 MiB parts,
  "-N" ETag), V1+V2 listings with delimiter and paging, CopyObject, batch
  delete, conditional writes, virtual-hosted addressing.
- **aws-sdk-go v1 contract tests**: signed-payload round trip, V2 listing,
  coded XML errors, presigned PUT + GET through a plain HTTP client, expired
  presigned URLs refused (403).
- **Feature tests**: CORS preflight allow/deny by origin and method with
  max-age; website index + error documents; lifecycle expiration under an
  injected clock via SweepLifecycleNow.
- **White-box**: store versioning/current-pointer machinery, conditional
  puts, delimiter+paging, multipart assembly and validation, object lock;
  awschunk (all three framings + malformed inputs, fuzz), checksum vectors.
- Full suite `-race` clean.
