package console_test

// Every line in copy.go can be reached, and each service is in the case it is
// declared to be.
//
// copy.go held a "nothing selected" line per service. Most services never
// showed it, because they open their first resource whenever one exists, or
// land on a page of their own. The lines were written, reviewed and never on
// screen, and several had gone stale (IAM "is off by default"; API Gateway and
// CloudFormation "have no create form" while both have one). A line nobody can
// reach is a line nobody notices is wrong, so this drives every service into
// the states and asks what actually appears.

import (
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/doze-dev/doze-aws/internal/console"
)

func TestEveryLineOfEmptyCopyIsReachable(t *testing.T) {
	// Empty stack first: what a person with nothing sees.
	h := newConsole(t)
	get := func(path string) string {
		t.Helper()
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/_console"+path, nil))
		if w.Code != http.StatusOK {
			t.Fatalf("GET %s = %d", path, w.Code)
		}
		return w.Body.String()
	}
	shown := func(body, line string) bool { return strings.Contains(body, html.EscapeString(line)) }

	empty := map[string]string{}
	for _, e := range console.RegisteredServices() {
		empty[e.Key] = get(e.Path)
	}

	// Then one resource each, so the second half asks what a person with
	// something sees. The names are distinct so a page can be asked "is this
	// the one you opened?".
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "bootstrap"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	seeds := []struct {
		svc, path, name string
		form            url.Values
	}{
		{"s3", "/s3/create", "bucket-one", url.Values{"name": {"bucket-one"}}},
		{"ddb", "/ddb/create", "tbl-one", url.Values{"name": {"tbl-one"}, "hash_key": {"id"}, "hash_type": {"S"}}},
		{"sqs", "/sqs/create", "queue-one", url.Values{"name": {"queue-one"}, "dlq_mode": {"none"}}},
		{"sns", "/sns/create", "topic-one", url.Values{"name": {"topic-one"}}},
		{"kinesis", "/kinesis/create", "stream-one", url.Values{"name": {"stream-one"}, "shards": {"1"}}},
		{"logs", "/logs/create", "/app/group-one", url.Values{"name": {"/app/group-one"}}},
		{"sm", "/sm/create", "secret-one", url.Values{"name": {"secret-one"}, "value": {"x"}}},
		{"ssm", "/ssm/create", "/app/param-one", url.Values{"name": {"/app/param-one"}, "type": {"String"}, "value": {"v"}}},
		{"apigw", "/apigw/create", "api-one", url.Values{"name": {"api-one"}}},
		{"iam", "/iam/create", "user-one", url.Values{"kind": {"user"}, "name": {"user-one"}}},
		{"sfn", "/sfn/create", "machine-one", url.Values{"name": {"machine-one"}, "role": {sfnTestRole},
			"definition": {`{"StartAt":"W","States":{"W":{"Type":"Wait","Seconds":1,"End":true}}}`}}},
		{"lambda", "/lambda/create", "fn-one", url.Values{"name": {"fn-one"}, "runtime": {"provided.al2"},
			"handler": {"bootstrap"}, "code": {dir}}},
		{"cfn", "/cfn/create", "stack-one", url.Values{"name": {"stack-one"},
			// Named so that it sorts after queue-one: a stack's own queue must not
			// become the one the SQS page opens.
			"template": {`{"Resources":{"Q":{"Type":"AWS::SQS::Queue","Properties":{"QueueName":"zz-from-stack"}}}}`}}},
		{"kms", "/kms/create", "alias-one", url.Values{"spec": {"SYMMETRIC_DEFAULT"}, "alias": {"alias-one"}}},
		{"cw", "/cw/create-alarm", "alarm-one", url.Values{"name": {"alarm-one"}, "metric": {"Custom|Hits"}, "statistic": {"Sum"},
			"operator": {"LessThanThreshold"}, "threshold": {"3"}, "period": {"60"}, "evaluation": {"1"}, "missing": {"notBreaching"}}},
	}
	// EventBridge needs no seed: the default bus is always there.
	named := map[string]string{"eb": "default"}
	for _, s := range seeds {
		named[s.svc] = s.name
		if rec := req(t, h, "POST", "/_console"+s.path, s.form); rec.Code != http.StatusSeeOther {
			t.Fatalf("seeding %s: %d\n%s", s.svc, rec.Code, rec.Body)
		}
	}

	for _, e := range console.RegisteredServices() {
		t.Run(e.Key, func(t *testing.T) {
			c, ok := console.EmptyCopyOf(e.Key)
			if !ok {
				t.Fatal("no copy held")
			}
			if c.Case != "unselected" && c.Case != "first" && c.Case != "home" {
				t.Fatalf("case %q is not one of unselected, first, home", c.Case)
			}

			// 1. Nothing exists: every held Short and None line is on screen.
			//    A service with none held shows none (EventBridge always has a
			//    bus, IAM always has the access log).
			for name, line := range map[string]string{"Short": c.Short, "None": c.None} {
				if line != "" && !shown(empty[e.Key], line) {
					t.Errorf("%s is held but the empty page does not show it: %q", name, line)
				}
			}
			if c.CTA != "" && !strings.Contains(empty[e.Key], c.CTAPath) {
				t.Errorf("the CTA %q points at %s, which the empty page does not link", c.CTA, c.CTAPath)
			}
			if c.CTAPath != "" {
				assertRenders(t, h, "/_console"+c.CTAPath)
			}

			// 2. Something exists.
			body := get(e.Path)
			title := ""
			if i := strings.Index(body, `class="det-title"`); i >= 0 {
				title = body[i:min(len(body), i+400)]
			}
			// KMS names a key by its id, not the alias it was given.
			selectedByDefault := title != "" && named[e.Key] != "" && (strings.Contains(title, named[e.Key]) || e.Key == "kms")

			switch c.Case {
			case "unselected":
				if c.Unsel == "" {
					t.Fatal("unselected, but no Unsel line is held")
				}
				if !shown(body, c.Unsel) {
					t.Errorf("Unsel is held and never shown with a resource present: %q", c.Unsel)
				}
				if selectedByDefault {
					t.Errorf("declared unselected, but the page opened %s", named[e.Key])
				}
			case "first":
				if c.Unsel != "" {
					t.Errorf("opens its first resource, so Unsel can never show: %q", c.Unsel)
				}
				if !selectedByDefault {
					t.Errorf("declared first-resource, but with %s present the page did not open it:\n%s", named[e.Key], title)
				}
			case "home":
				if c.Unsel != "" {
					t.Errorf("lands on a page of its own, so Unsel can never show: %q", c.Unsel)
				}
				if selectedByDefault {
					t.Errorf("declared home, but the page opens %s", named[e.Key])
				}
			}
		})
	}
}
