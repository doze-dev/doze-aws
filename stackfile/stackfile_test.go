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
  audit: {}

tables:
  sessions:
    key: sessionId:S
    ttl: expiresAt
    gsis:
      by-user:
        key: userId:S createdAt:N

buckets:
  uploads:
    versioning: true
    notify:
      - events: ["s3:ObjectCreated:*"]
        queue: audit

topics:
  order-events:
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
    on_failure: {queue: audit}
    triggers:
      - queue: audit
        batch: 5

rules:
  on-order:
    pattern: {source: [shop.orders]}
    targets: [queue:audit, lambda:resize]

keys:
  app-key:
    rotation: true

secrets:
  app/config:
    value: '{"apiKey":"local"}'

parameters:
  /app/db/host: localhost
  /app/db/port:
    value: "5432"
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
	if got := exp.Tables["sessions"]; got.Key != "sessionId:S" || got.TTL != "expiresAt" {
		t.Errorf("export table mismatch: %+v", got)
	}
	if got := exp.Tables["sessions"].GSIs["by-user"]; got.Key != "userId:S createdAt:N" {
		t.Errorf("export GSI mismatch: %+v", got)
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
	if len(exp.Rules["on-order"].Targets) != 2 {
		t.Errorf("export rule targets: %+v", exp.Rules["on-order"])
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
