package console_test

import (
	"encoding/json"
	"html"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// One Save is one write per direction, so a refusal cannot leave a random
// subset of the draft on the resource. The handler used to write one tag per
// call and stop at the first refusal, which left whichever keys the map
// iteration reached first and an editor still showing the old draft.

// saveTags submits the tag editor for a resource with exactly these rows.
func saveTags(t *testing.T, h http.Handler, svc, id string, rows [][2]string) string {
	t.Helper()
	form := url.Values{"svc": {svc}, "id": {id}}
	for _, kv := range rows {
		form.Add("tag_key", kv[0])
		form.Add("tag_val", kv[1])
	}
	rec := req(t, h, "POST", "/_console/tags/save", form)
	if rec.Code != http.StatusOK {
		t.Fatalf("%s: save answered %d, want 200 (a refusal is drawn in the editor)\n%s", svc, rec.Code, rec.Body)
	}
	return rec.Body.String()
}

// tagsOf reads what the resource holds, through the same view the page lazy-loads.
func tagsOf(t *testing.T, h http.Handler, svc, id string) map[string]string {
	t.Helper()
	body := req(t, h, "GET", "/_console/tags/view?svc="+svc+"&id="+url.QueryEscape(id), nil).Body.String()
	// The editor is seeded from a JSON array in its x-data attribute.
	_, rest, ok := strings.Cut(body, "dozeTags(")
	if !ok {
		t.Fatalf("%s: no tag editor in the view:\n%s", svc, body)
	}
	seed, _, _ := strings.Cut(rest, ")\"")
	var rows []struct{ K, V string }
	if err := json.Unmarshal([]byte(html.UnescapeString(seed)), &rows); err != nil {
		t.Fatalf("%s: tag seed is not JSON (%v): %s", svc, err, seed)
	}
	out := map[string]string{}
	for _, r := range rows {
		out[r.K] = r.V
	}
	return out
}

func TestTagSaveWritesTheWholeDraftAndOnlyTheDifference(t *testing.T) {
	h := newConsole(t)
	create(t, h, "/_console/sqs/create", url.Values{"name": {"jobs"}, "dlq_mode": {"none"}})
	create(t, h, "/_console/sns/create", url.Values{"name": {"alerts"}})
	create(t, h, "/_console/sm/create", url.Values{"name": {"db"}, "value": {"s3cret"}})
	create(t, h, "/_console/kinesis/create", url.Values{"name": {"telemetry"}, "shards": {"1"}})
	create(t, h, "/_console/ddb/create", url.Values{"name": {"users"}, "hash_key": {"id"}, "hash_type": {"S"}})
	req(t, h, "POST", "/_console/eb/default/create-rule", url.Values{"name": {"r"}, "pattern": {`{"source":["x"]}`}})
	create(t, h, "/_console/ssm/create", url.Values{"name": {"/app/p"}, "type": {"String"}, "value": {"v"}})
	create(t, h, "/_console/logs/create", url.Values{"name": {"/app/g"}})
	create(t, h, "/_console/iam/create", url.Values{"kind": {"user"}, "name": {"bob"}})
	create(t, h, "/_console/sfn/create", url.Values{"name": {"machine"}, "role": {sfnTestRole},
		"definition": {`{"StartAt":"W","States":{"W":{"Type":"Wait","Seconds":1,"End":true}}}`}})
	// The id of a thing the service names itself is the last path segment of
	// where its create form redirects.
	tail := func(loc string) string {
		if i := strings.IndexByte(loc, '?'); i >= 0 {
			loc = loc[:i]
		}
		return loc[strings.LastIndexByte(loc, '/')+1:]
	}
	keyID := tail(create(t, h, "/_console/kms/create", url.Values{"spec": {"SYMMETRIC_DEFAULT"}}))
	restID := tail(create(t, h, "/_console/apigw/create", url.Values{"name": {"rest"}}))
	httpID := tail(create(t, h, "/_console/apigw/create", url.Values{"name": {"http"}, "protocol": {"HTTP"}}))
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "bootstrap"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	create(t, h, "/_console/lambda/create", url.Values{"name": {"fn"}, "runtime": {"provided.al2"}, "handler": {"bootstrap"}, "code": {dir}})

	create(t, h, "/_console/cw/create-alarm", url.Values{
		"name": {"low"}, "metric": {"Custom|Hits"}, "statistic": {"Sum"}, "operator": {"LessThanThreshold"},
		"threshold": {"3"}, "period": {"60"}, "evaluation": {"1"}, "missing": {"notBreaching"},
	})

	// Every service whose tags the editor writes, each with its own wire shape
	// for a batch: a JSON map, a list of Key/Value under three spellings, form
	// members numbered from one, and keys repeated in a query string.
	for _, c := range []struct{ svc, id string }{
		{"sqs", "jobs"}, {"sns", "alerts"}, {"sm", "db"}, {"kinesis", "telemetry"}, {"ddb", "users"}, {"eb", "r"},
		{"ssm", "/app/p"}, {"logs", "/app/g"}, {"iam-user", "bob"}, {"sfn", "machine"}, {"kms", keyID},
		{"apigw", restID}, {"apigw-http", httpID}, {"lambda", "fn"}, {"cw", "low"},
	} {
		t.Run(c.svc, func(t *testing.T) {
			// Eleven tags in one save: past Kinesis's ten per call, so the
			// chunking is exercised where the service has a per-call limit.
			var first [][2]string
			for _, k := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k"} {
				first = append(first, [2]string{k, "1"})
			}
			saveTags(t, h, c.svc, c.id, first)
			if got := tagsOf(t, h, c.svc, c.id); len(got) != 11 || got["k"] != "1" {
				t.Fatalf("after the first save: %v", got)
			}

			// Change one, drop one, add one, leave the rest.
			var second [][2]string
			for _, kv := range first {
				switch kv[0] {
				case "b":
					second = append(second, [2]string{"b", "2"})
				case "c":
					// dropped
				default:
					second = append(second, kv)
				}
			}
			second = append(second, [2]string{"z", "9"})
			saveTags(t, h, c.svc, c.id, second)
			got := tagsOf(t, h, c.svc, c.id)
			if got["b"] != "2" || got["z"] != "9" || got["a"] != "1" {
				t.Errorf("changed/added/kept wrong: %v", got)
			}
			if _, still := got["c"]; still {
				t.Errorf("c should have been removed: %v", got)
			}
			if len(got) != 11 {
				t.Errorf("want 11 tags, have %d: %v", len(got), got)
			}

			// Nine keys leave in one save: the removal is a batch too, and each
			// service spells a list of keys its own way (a JSON array, members
			// numbered from one, a key repeated in the query string).
			saveTags(t, h, c.svc, c.id, [][2]string{{"a", "1"}, {"z", "9"}})
			got = tagsOf(t, h, c.svc, c.id)
			if len(got) != 2 || got["a"] != "1" || got["z"] != "9" {
				t.Errorf("after removing nine keys at once: %v", got)
			}
		})
	}
}

func TestARefusedTagSaveLeavesNothingHalfWritten(t *testing.T) {
	h := newConsole(t)
	create(t, h, "/_console/sm/create", url.Values{"name": {"db"}, "value": {"s3cret"}})
	saveTags(t, h, "sm", "db", [][2]string{{"env", "prod"}})

	// A key over 128 characters is refused by the service's own model (Secrets
	// Manager's Tag.Key is 1 to 128), and so is the whole set it travels in. "ok" is a perfectly good tag and must not be
	// written on the strength of arriving earlier in the form.
	long := strings.Repeat("x", 129)
	body := saveTags(t, h, "sm", "db", [][2]string{{"env", "prod"}, {"ok", "1"}, {long, "v"}})

	if got := tagsOf(t, h, "sm", "db"); len(got) != 1 || got["env"] != "prod" {
		t.Fatalf("a refused save changed the resource: %v", got)
	}
	for _, want := range []string{"Refused", "Nothing was changed", `&#34;ok&#34;`} {
		if !strings.Contains(body, want) {
			t.Errorf("the editor does not say %q after a refusal:\n%s", want, body)
		}
	}
}

func TestARefusedSetAfterAnAppliedRemovalSaysSo(t *testing.T) {
	h := newConsole(t)
	create(t, h, "/_console/sm/create", url.Values{"name": {"db"}, "value": {"s3cret"}})
	saveTags(t, h, "sm", "db", [][2]string{{"env", "prod"}})

	// env leaves and an invalid key arrives: two calls, and the second is refused.
	body := saveTags(t, h, "sm", "db", [][2]string{{strings.Repeat("x", 129), "v"}})

	if got := tagsOf(t, h, "sm", "db"); len(got) != 0 {
		t.Fatalf("the removal should have been applied and nothing else: %v", got)
	}
	if !strings.Contains(body, "was applied") {
		t.Errorf("the editor must say the removal went through:\n%s", body)
	}
}
