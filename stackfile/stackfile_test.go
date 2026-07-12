package stackfile

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	dozeaws "github.com/doze-dev/doze-aws"
)

const sample = `
queues:
  orders.fifo:
    fifo: true
    content_dedup: true
    dlq: auto
    max_receives: 4
    tags: {team: shop}
  audit:
    receive_wait: 5
    max_size: 65536

tables:
  sessions:
    key: sessionId:S userId:S
    ttl: expiresAt
    deletion_protection: true
    tags: {team: shop}
    gsis:
      by-user:
        key: userId:S createdAt:N
      by-email:
        key: email:S
        projection: INCLUDE
        include: [displayName]
    lsis:
      by-created:
        key: createdAt:N
        projection: KEYS_ONLY

buckets:
  uploads:
    versioning: true
    tags: {team: shop}
    cors:
      - origins: ["https://app.local"]
        methods: [GET, PUT]
        headers: ["*"]
        expose: [ETag]
        max_age: 3600
    lifecycle:
      - prefix: tmp/
        expire_days: 7
        abort_uploads_days: 2
    website:
      index: index.html
      error: 404.html
    notify:
      - events: ["s3:ObjectCreated:*"]
        queue: audit

topics:
  order-events:
    tags: {team: shop}
    subscriptions:
      - queue: audit
        filter: {kind: [click]}
      - lambda: resize

functions:
  resize:
    runtime: provided.al2
    handler: bootstrap
    code: CODEDIR
    env: {LOG_LEVEL: debug}
    retries: 1
    dlq: {queue: audit}
    on_failure: {queue: audit}
    tags: {team: shop}
    triggers:
      - queue: audit
        batch: 5

rules:
  on-order:
    pattern: {source: [shop.orders]}
    targets:
      - queue:audit
      - lambda: resize
        input_path: $.detail
      - topic: order-events
        template: '{"msg": <msg>}'
        paths: {msg: $.detail.message}
  nightly:
    schedule: rate(1 day)
    enabled: false
    targets: [queue:audit]

keys:
  app-key:
    rotation: true
    description: app data key
    tags: {team: shop}
  rsa-crypt:
    spec: RSA_2048
    usage: ENCRYPT_DECRYPT

secrets:
  app/config:
    value: '{"apiKey":"local"}'
    description: app config blob
    tags: {team: shop}
  app/blob:
    binary: aGVsbG8=

parameters:
  /app/db/host: localhost
  /app/db/port:
    value: "5432"
    description: local pg port
    tags: {team: shop}
`

func testStack(t *testing.T) *dozeaws.Stack {
	t.Helper()
	if testing.Short() {
		t.Skip("boots a Stack")
	}
	st, err := dozeaws.NewStack(dozeaws.StackConfig{DataDir: t.TempDir(), Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func sampleWithCode(t *testing.T) []byte {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "bootstrap"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return []byte(strings.ReplaceAll(sample, "CODEDIR", dir))
}

func TestParseValidates(t *testing.T) {
	if _, err := Parse(sampleWithCode(t)); err != nil {
		t.Fatalf("sample should parse: %v", err)
	}
	bad := []struct{ name, yaml, want string }{
		{"fifo suffix", "queues:\n  orders:\n    fifo: true\n", ".fifo suffix"},
		{"unknown field", "queues:\n  a:\n    color: red\n", "field color not found"},
		{"dangling ref", "topics:\n  t:\n    subscriptions:\n      - queue: nope\n", "not declared"},
		{"bad key", "tables:\n  t:\n    key: pk\n", "attr:TYPE"},
		{"two dests", "topics:\n  t:\n    subscriptions:\n      - {queue: a, lambda: b}\n", "exactly one"},
		{"bad target", "functions: {}\nrules:\n  r:\n    pattern: {a: [b]}\n    targets: [nope]\n", "queue:name"},
		{"bad projection", "tables:\n  t:\n    key: pk:S\n    gsis:\n      g: {key: x:S, projection: SOME}\n", "projection"},
		{"include sans INCLUDE", "tables:\n  t:\n    key: pk:S\n    gsis:\n      g: {key: x:S, include: [a]}\n", "INCLUDE"},
		{"lsi two keys", "tables:\n  t:\n    key: pk:S\n    lsis:\n      l: {key: 'pk:S sk:N'}\n", "sort key only"},
		{"cors sans methods", "buckets:\n  b:\n    cors:\n      - origins: ['*']\n", "methods"},
		{"empty lifecycle", "buckets:\n  b:\n    lifecycle:\n      - prefix: x/\n", "expire_days"},
		{"paths sans template", "queues:\n  q: {}\nrules:\n  r:\n    pattern: {a: [b]}\n    targets:\n      - queue: q\n        paths: {x: $.y}\n", "template"},
		{"value and binary", "secrets:\n  s:\n    value: x\n    binary: eA==\n", "mutually exclusive"},
		{"bad usage", "keys:\n  k:\n    usage: SIGNING\n", "usage"},
		{"bad retries", "functions:\n  f:\n    code: /tmp\n    retries: 9\n", "retries"},
	}
	for _, c := range bad {
		if _, err := Parse([]byte(c.yaml)); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: want error containing %q, got %v", c.name, c.want, err)
		}
	}
}

// TestApplyConverges is the core contract: apply once creates everything;
// apply twice changes nothing it shouldn't (no duplicate subscriptions,
// triggers, or DLQs — and secret/parameter values stay untouched).
func TestApplyConverges(t *testing.T) {
	st := testStack(t)
	ctx := context.Background()
	s, err := Parse(sampleWithCode(t))
	if err != nil {
		t.Fatal(err)
	}

	rep, err := Apply(ctx, st.Handler(), s)
	if err != nil {
		t.Fatalf("first apply: %v", err)
	}
	created, _, _ := rep.Counts()
	if created < 10 { // queues(3 incl auto-dlq) + table + bucket + fn + topic subs + key + secret + params
		t.Fatalf("first apply created only %d resources:\n%+v", created, rep.Actions)
	}

	// Second apply must not create anything new.
	rep2, err := Apply(ctx, st.Handler(), s)
	if err != nil {
		t.Fatalf("second apply: %v", err)
	}
	for _, a := range rep2.Actions {
		if a.Op == "created" {
			t.Errorf("second apply created %s (%s) — not convergent", a.Resource, a.Detail)
		}
	}

	// The wired graph is queryable: export sees what apply built.
	exp, err := Export(ctx, st.Handler())
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if _, ok := exp.Queues["orders.fifo"]; !ok {
		t.Fatalf("export missing orders.fifo: %+v", exp.Queues)
	}
	if exp.Queues["orders.fifo"].DLQ != "orders-dlq.fifo" {
		t.Errorf("export lost the redrive wiring: %+v", exp.Queues["orders.fifo"])
	}
	if got := exp.Tables["sessions"]; got.Key != "sessionId:S userId:S" || got.TTL != "expiresAt" {
		t.Errorf("export table mismatch: %+v", got)
	}
	if got := exp.Tables["sessions"].GSIs["by-user"]; got.Key != "userId:S createdAt:N" {
		t.Errorf("export GSI mismatch: %+v", got)
	}
	if got := exp.Tables["sessions"].GSIs["by-email"]; got.Projection != "INCLUDE" || len(got.Include) != 1 {
		t.Errorf("export GSI projection mismatch: %+v", got)
	}
	if got := exp.Tables["sessions"].LSIs["by-created"]; got.Key != "createdAt:N" || got.Projection != "KEYS_ONLY" {
		t.Errorf("export LSI mismatch: %+v", got)
	}
	if got := exp.Tables["sessions"]; got.DeletionProtection == nil || !*got.DeletionProtection || got.Tags["team"] != "shop" {
		t.Errorf("export table protection/tags mismatch: %+v", got)
	}
	if got := exp.Queues["orders.fifo"]; got.Tags["team"] != "shop" {
		t.Errorf("export queue tags mismatch: %+v", got)
	}
	if got := exp.Queues["audit"]; got.ReceiveWait != 5 || got.MaxSize != 65536 {
		t.Errorf("export queue receive_wait/max_size mismatch: %+v", got)
	}
	if got := exp.Buckets["uploads"]; len(got.CORS) != 1 || got.CORS[0].MaxAge != 3600 ||
		len(got.Lifecycle) != 1 || got.Lifecycle[0].ExpireDays != 7 ||
		got.Website == nil || got.Website.Index != "index.html" || got.Tags["team"] != "shop" {
		t.Errorf("export bucket configs mismatch: %+v", got)
	}
	if got := exp.Topics["order-events"]; got.Tags["team"] != "shop" {
		t.Errorf("export topic tags mismatch: %+v", got)
	}
	if got := exp.Functions["resize"]; got.DLQ == nil || got.DLQ.Queue != "audit" ||
		got.Retries == nil || *got.Retries != 1 || got.Tags["team"] != "shop" {
		t.Errorf("export function dlq/retries/tags mismatch: %+v", got)
	}
	if got := exp.Rules["nightly"]; got.Enabled == nil || *got.Enabled {
		t.Errorf("export rule enabled mismatch: %+v", got)
	}
	if got := exp.Keys["app-key"]; got.Description != "app data key" || got.Tags["team"] != "shop" {
		t.Errorf("export key description/tags mismatch: %+v", got)
	}
	if got := exp.Keys["rsa-crypt"]; got.Spec != "RSA_2048" || got.Usage != "ENCRYPT_DECRYPT" {
		t.Errorf("export key usage mismatch: %+v", got)
	}
	if got := exp.Secrets["app/config"]; got.Description != "app config blob" || got.Tags["team"] != "shop" {
		t.Errorf("export secret description/tags mismatch: %+v", got)
	}
	if got := exp.Parameters["/app/db/port"]; got.Description != "local pg port" || got.Tags["team"] != "shop" {
		t.Errorf("export parameter description/tags mismatch: %+v", got)
	}
	if len(exp.Topics["order-events"].Subscriptions) != 2 {
		t.Errorf("export subscriptions: %+v", exp.Topics["order-events"])
	}
	if len(exp.Buckets["uploads"].Notify) != 1 || exp.Buckets["uploads"].Notify[0].Queue != "audit" {
		t.Errorf("export bucket notify: %+v", exp.Buckets["uploads"].Notify)
	}
	if len(exp.Functions["resize"].Triggers) != 1 || exp.Functions["resize"].Triggers[0].Queue != "audit" {
		t.Errorf("export triggers: %+v", exp.Functions["resize"])
	}
	if exp.Functions["resize"].OnFailure == nil || exp.Functions["resize"].OnFailure.Queue != "audit" {
		t.Errorf("export on_failure: %+v", exp.Functions["resize"].OnFailure)
	}
	if got := exp.Rules["on-order"].Targets; len(got) != 3 {
		t.Errorf("export rule targets: %+v", got)
	} else {
		byKind := map[string]Target{}
		for _, tgt := range got {
			switch {
			case tgt.Queue != "":
				byKind["queue"] = tgt
			case tgt.Topic != "":
				byKind["topic"] = tgt
			case tgt.Lambda != "":
				byKind["lambda"] = tgt
			}
		}
		if byKind["lambda"].InputPath != "$.detail" {
			t.Errorf("export target input_path: %+v", byKind["lambda"])
		}
		if byKind["topic"].Template == "" || byKind["topic"].Paths["msg"] != "$.detail.message" {
			t.Errorf("export target transformer: %+v", byKind["topic"])
		}
	}
	if !exp.Keys["app-key"].Rotation {
		t.Errorf("export key rotation: %+v", exp.Keys["app-key"])
	}
	if _, ok := exp.Secrets["app/config"]; !ok {
		t.Errorf("export secrets: %+v", exp.Secrets)
	}
	if exp.Secrets["app/config"].Value != "" {
		t.Errorf("secret VALUE leaked into export: %+v", exp.Secrets["app/config"])
	}
	if exp.Parameters["/app/db/host"].Value != "localhost" {
		t.Errorf("export parameter value: %+v", exp.Parameters["/app/db/host"])
	}

	// Round-trip: the exported stack re-applies as a no-op (functions report
	// "updated" for config/code refresh; nothing may be CREATED).
	// The exported function code path is real (the _local_ extension echoes it).
	yml, err := Marshal(exp)
	if err != nil {
		t.Fatal(err)
	}
	reparsed, err := Parse(yml)
	if err != nil {
		t.Fatalf("exported yaml does not reparse: %v\n%s", err, yml)
	}
	rep3, err := Apply(ctx, st.Handler(), reparsed)
	if err != nil {
		t.Fatalf("apply(export()): %v", err)
	}
	for _, a := range rep3.Actions {
		if a.Op == "created" {
			t.Errorf("apply(export()) created %s — round trip is not stable", a.Resource)
		}
	}
}

// TestApplyConvergesExistingTable: a table declared with more GSIs / a TTL
// than it has live gets the missing pieces via UpdateTable, not a skip.
func TestApplyConvergesExistingTable(t *testing.T) {
	st := testStack(t)
	ctx := context.Background()

	v1, err := Parse([]byte("tables:\n  events:\n    key: id:S\n"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(ctx, st.Handler(), v1); err != nil {
		t.Fatal(err)
	}

	v2, err := Parse([]byte("tables:\n  events:\n    key: id:S\n    ttl: expiresAt\n    gsis:\n      by-kind:\n        key: kind:S\n"))
	if err != nil {
		t.Fatal(err)
	}
	rep, err := Apply(ctx, st.Handler(), v2)
	if err != nil {
		t.Fatal(err)
	}
	var updated bool
	for _, a := range rep.Actions {
		if a.Resource == "table/events" && a.Op == "updated" {
			updated = true
			if !strings.Contains(a.Detail, "gsi by-kind") || !strings.Contains(a.Detail, "ttl") {
				t.Errorf("update detail = %q, want gsi + ttl", a.Detail)
			}
		}
	}
	if !updated {
		t.Fatalf("existing table was not converged: %+v", rep.Actions)
	}

	exp, err := Export(ctx, st.Handler())
	if err != nil {
		t.Fatal(err)
	}
	if got := exp.Tables["events"]; got.TTL != "expiresAt" || got.GSIs["by-kind"].Key != "kind:S" {
		t.Errorf("converged table not visible in export: %+v", got)
	}

	// Third apply: nothing left to converge.
	rep2, err := Apply(ctx, st.Handler(), v2)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range rep2.Actions {
		if a.Op != "skipped" {
			t.Errorf("third apply still reports %s %s (%s)", a.Op, a.Resource, a.Detail)
		}
	}
}

// TestApplyNeverStompsValues: a live-edited secret/parameter survives apply.
func TestApplyNeverStompsValues(t *testing.T) {
	st := testStack(t)
	ctx := context.Background()
	c := newClient(st.Handler())

	s, err := Parse([]byte("secrets:\n  app/token:\n    value: from-file\nparameters:\n  /app/x: from-file\n"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(ctx, st.Handler(), s); err != nil {
		t.Fatal(err)
	}
	// A human changes both live.
	if _, err := c.json11(ctx, "secretsmanager", "PutSecretValue", map[string]any{
		"SecretId": "app/token", "SecretString": "live-edit",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.json11(ctx, "AmazonSSM", "PutParameter", map[string]any{
		"Name": "/app/x", "Value": "live-edit", "Type": "String", "Overwrite": true,
	}); err != nil {
		t.Fatal(err)
	}
	// Re-apply must not stomp.
	if _, err := Apply(ctx, st.Handler(), s); err != nil {
		t.Fatal(err)
	}
	out, err := c.json11(ctx, "secretsmanager", "GetSecretValue", map[string]any{"SecretId": "app/token"})
	if err != nil || !strings.Contains(string(out), "live-edit") {
		t.Errorf("apply stomped the live secret: %s (%v)", out, err)
	}
	out, err = c.json11(ctx, "AmazonSSM", "GetParameter", map[string]any{"Name": "/app/x"})
	if err != nil || !strings.Contains(string(out), "live-edit") {
		t.Errorf("apply stomped the live parameter: %s (%v)", out, err)
	}
}
