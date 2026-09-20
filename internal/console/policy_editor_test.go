package console_test

import (
	"net/url"
	"strings"
	"testing"
)

// Every field holding an IAM policy document gets the policy builder, and every
// field holding an event pattern gets the pattern builder. Both are
// service-agnostic partials, and for a long time each was wired into exactly
// one of the several places it fits: the bucket policy had rows while the queue
// policy beside it was a bare textarea, and an SNS filter policy got the builder
// once the subscription existed but not while you were creating it — the one
// moment you know least about the shape.
//
// Asserted per page rather than by reading the templates, because what matters
// is that the builder reaches the rendered page: a partial that silently stops
// being included still leaves the template file looking correct.
func TestPolicyAndPatternFieldsGetTheirBuilders(t *testing.T) {
	h := newConsole(t)
	create(t, h, "/_console/sqs/create", url.Values{"name": {"polq"}})
	create(t, h, "/_console/sns/create", url.Values{"name": {"poltopic"}})
	create(t, h, "/_console/kinesis/create", url.Values{"name": {"polstream"}})
	create(t, h, "/_console/s3/create", url.Values{"name": {"polbucket"}})

	for _, tc := range []struct {
		what    string
		path    string
		builder string
	}{
		{"the queue access policy", "/_console/sqs/polq?tab=config", "policyBuilder"},
		{"the queue access policy on create", "/_console/sqs/create", "policyBuilder"},
		{"the stream resource policy", "/_console/kinesis/polstream/details", "policyBuilder"},
		{"the bucket policy", "/_console/s3/polbucket?tab=properties", "policyBuilder"},
		{"a new subscription's filter policy", "/_console/sns/poltopic", "patternBuilder"},
		{"an EventBridge rule pattern", "/_console/eb/default/create-rule", "patternBuilder"},
	} {
		t.Run(tc.what, func(t *testing.T) {
			rec := req(t, h, "GET", tc.path, nil)
			if rec.Code != 200 {
				t.Fatalf("%s: status %d", tc.path, rec.Code)
			}
			if !strings.Contains(rec.Body.String(), tc.builder) {
				t.Errorf("%s has no %s — it is a bare textarea again", tc.what, tc.builder)
			}
		})
	}
}

// The IAM access checker's draft box opens with a document to edit. It is the
// one policy field on that page deliberately NOT switched to the builder: the
// simulate handler picks SimulateCustomPolicy whenever document is non-empty,
// and the textarea's disabled binding is what keeps a prefilled draft from
// submitting — and hijacking the simulation — while you are checking a
// principal instead.
func TestIAMDraftPolicyOpensWithADocument(t *testing.T) {
	h := newConsole(t)
	page := req(t, h, "GET", "/_console/iam", nil).Body.String()

	i := strings.Index(page, `name="document"`)
	if i < 0 {
		t.Fatal("no draft policy document field on the IAM page")
	}
	box := page[i:]
	if end := strings.Index(box, "</textarea>"); end >= 0 {
		box = box[:end]
	}
	for _, want := range []string{"Version", "Statement", "Effect"} {
		if !strings.Contains(box, want) {
			t.Errorf("the draft box does not open with a policy to edit; missing %q", want)
		}
	}
	if !strings.Contains(box, `x-bind:disabled="!draft"`) {
		t.Error("the draft box lost its disabled binding: a prefilled document that " +
			"submits in principal mode silently simulates the starter instead of the principal")
	}
}
