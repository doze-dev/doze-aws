package console_test

// The access-policy cluster: six console surfaces that write a resource
// policy, and not one test anywhere asserted what they wrote.
//
// They are driven by console/mutation_routes_test.go, which sweeps every POST
// route with a generic form and asserts exactly two things — no 5xx, and if a
// redirect happens the target renders. That makes these handlers LOOK covered
// (55-75% each) while nothing checks the document reaches the service or
// comes back. The sweep is a good guard against a broken route; it is not
// evidence about a policy.
//
// Which matters more here than for most panels, because these documents are
// evaluated for real under IAM soft and enforce. A panel that silently drops
// what you typed is a panel that tells you an access rule is in place when it
// is not.

import (
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// policyDoc is a document distinctive enough that finding it proves it came
// back rather than being regenerated.
func accessDoc(sid, action, resource string) string {
	return `{"Version":"2012-10-17","Statement":[{"Sid":"` + sid + `","Effect":"Allow",` +
		`"Principal":{"AWS":"arn:aws:iam::000000000000:user/reader"},"Action":"` + action +
		`","Resource":"` + resource + `"}]}`
}

// TestConsoleKeyPolicyRoundTrips: KMS always has a key policy, so this one
// replaces rather than adds — and the panel must show what it replaced it
// with.
func TestConsoleKeyPolicyRoundTrips(t *testing.T) {
	h := newConsole(t)
	loc := create(t, h, "/_console/kms/create", url.Values{"alias": {"app"}, "description": {"k"}})
	keyID := regexp.MustCompile(`/kms/([^/?]+)`).FindStringSubmatch(loc)[1]

	doc := accessDoc("ConsoleWrote", "kms:Decrypt", "*")
	rec := req(t, h, "POST", "/_console/kms/"+keyID+"/policy", url.Values{"document": {doc}})
	if rec.Code >= 400 {
		t.Fatalf("save key policy: %d\n%s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "ConsoleWrote") {
		t.Errorf("the partial should render the saved policy:\n%s", truncateBody(rec.Body.String()))
	}
	// Read it back on a fresh render, not just in the response to the write.
	page := req(t, h, "GET", "/_console/kms/"+keyID, nil).Body.String()
	if !strings.Contains(page, "ConsoleWrote") {
		t.Errorf("the key page does not show the saved policy:\n%s", truncateBody(page))
	}

	// An empty document is refused: KMS always has one, and accepting the
	// empty string would lock the key.
	if bad := req(t, h, "POST", "/_console/kms/"+keyID+"/policy", url.Values{"document": {"  "}}); bad.Code < 400 {
		t.Errorf("an empty key policy must be refused, got %d", bad.Code)
	}
	after := req(t, h, "GET", "/_console/kms/"+keyID, nil).Body.String()
	if !strings.Contains(after, "ConsoleWrote") {
		t.Error("the refused save must leave the previous policy in place")
	}
}

// TestConsoleSecretPolicyRoundTripsAndRemoves: the secret panel both writes
// and clears, and the badge follows.
func TestConsoleSecretPolicyRoundTripsAndRemoves(t *testing.T) {
	h := newConsole(t)
	create(t, h, "/_console/sm/create", url.Values{"name": {"db"}, "value": {"s3cret"}})

	doc := accessDoc("SecretRead", "secretsmanager:GetSecretValue", "*")
	rec := req(t, h, "POST", "/_console/sm/policy", url.Values{"name": {"db"}, "document": {doc}})
	if rec.Code >= 400 {
		t.Fatalf("save secret policy: %d\n%s", rec.Code, rec.Body)
	}
	page := req(t, h, "GET", "/_console/sm/secret?name=db", nil).Body.String()
	if !strings.Contains(page, "SecretRead") {
		t.Errorf("the secret page does not show the saved policy:\n%s", truncateBody(page))
	}

	if rm := req(t, h, "POST", "/_console/sm/policy",
		url.Values{"name": {"db"}, "remove": {"1"}}); rm.Code >= 400 {
		t.Fatalf("remove secret policy: %d\n%s", rm.Code, rm.Body)
	}
	gone := req(t, h, "GET", "/_console/sm/secret?name=db", nil).Body.String()
	if strings.Contains(gone, "SecretRead") {
		t.Errorf("the policy survived removal:\n%s", truncateBody(gone))
	}
}

// TestConsoleStreamPolicyRoundTripsAndRemoves: same shape for Kinesis, where
// an empty document means delete rather than "store the empty string".
func TestConsoleStreamPolicyRoundTripsAndRemoves(t *testing.T) {
	h := newConsole(t)
	create(t, h, "/_console/kinesis/create", url.Values{"name": {"telemetry"}, "shards": {"1"}})

	doc := accessDoc("StreamRead", "kinesis:GetRecords", "*")
	rec := req(t, h, "POST", "/_console/kinesis/telemetry/policy", url.Values{"policy": {doc}})
	if rec.Code >= 400 {
		t.Fatalf("save stream policy: %d\n%s", rec.Code, rec.Body)
	}
	// The policy panel lives on the details tab, which is the handler that
	// fetches it.
	page := req(t, h, "GET", "/_console/kinesis/telemetry/details", nil).Body.String()
	if !strings.Contains(page, "StreamRead") {
		t.Errorf("the stream details tab does not show the saved policy:\n%s", truncateBody(page))
	}

	if rm := req(t, h, "POST", "/_console/kinesis/telemetry/policy",
		url.Values{"policy": {""}}); rm.Code >= 400 {
		t.Fatalf("remove stream policy: %d\n%s", rm.Code, rm.Body)
	}
	gone := req(t, h, "GET", "/_console/kinesis/telemetry/details", nil).Body.String()
	if strings.Contains(gone, "StreamRead") {
		t.Errorf("the policy survived removal:\n%s", truncateBody(gone))
	}
}

// TestConsoleQueuePermissionGrantsAndRevokes: AddPermission synthesises a
// statement and RemovePermission finds it again by label. The sweep drives
// both with a fixture label and never looks at the queue's policy.
func TestConsoleQueuePermissionGrantsAndRevokes(t *testing.T) {
	h := newConsole(t)
	create(t, h, "/_console/sqs/create", url.Values{"name": {"orders"}})

	if rec := req(t, h, "POST", "/_console/sqs/orders/permission",
		url.Values{"label": {"partner-send"}, "account": {"000000000000"}, "action": {"SendMessage"}}); rec.Code >= 400 {
		t.Fatalf("add permission: %d\n%s", rec.Code, rec.Body)
	}
	page := req(t, h, "GET", "/_console/sqs/orders?tab=config", nil).Body.String()
	if !strings.Contains(page, "partner-send") {
		t.Fatalf("the queue page does not show the granted permission:\n%s", truncateBody(page))
	}
	if !strings.Contains(page, "SendMessage") {
		t.Errorf("the statement should name the action it granted:\n%s", truncateBody(page))
	}

	if rec := req(t, h, "POST", "/_console/sqs/orders/permission/delete",
		url.Values{"label": {"partner-send"}}); rec.Code >= 400 {
		t.Fatalf("remove permission: %d\n%s", rec.Code, rec.Body)
	}
	after := req(t, h, "GET", "/_console/sqs/orders?tab=config", nil).Body.String()
	if strings.Contains(after, "partner-send") {
		t.Errorf("the permission survived removal:\n%s", truncateBody(after))
	}

	// A permission with no label is refused: the label is how RemovePermission
	// finds it again, so an unlabelled one could never be taken back.
	if bad := req(t, h, "POST", "/_console/sqs/orders/permission",
		url.Values{"label": {""}, "action": {"SendMessage"}}); bad.Code < 400 {
		t.Errorf("an unlabelled permission must be refused, got %d", bad.Code)
	}
}

// TestConsoleTopicPermissionGrantsAndRevokes: the same for SNS.
func TestConsoleTopicPermissionGrantsAndRevokes(t *testing.T) {
	h := newConsole(t)
	create(t, h, "/_console/sns/create", url.Values{"name": {"alerts"}})

	if rec := req(t, h, "POST", "/_console/sns/alerts/permission",
		url.Values{"label": {"partner-publish"}, "account": {"000000000000"}, "action": {"Publish"}}); rec.Code >= 400 {
		t.Fatalf("add permission: %d\n%s", rec.Code, rec.Body)
	}
	page := req(t, h, "GET", "/_console/sns/alerts?tab=details", nil).Body.String()
	if !strings.Contains(page, "partner-publish") {
		t.Fatalf("the topic page does not show the granted permission:\n%s", truncateBody(page))
	}

	if rec := req(t, h, "POST", "/_console/sns/alerts/permission/delete",
		url.Values{"label": {"partner-publish"}}); rec.Code >= 400 {
		t.Fatalf("remove permission: %d\n%s", rec.Code, rec.Body)
	}
	after := req(t, h, "GET", "/_console/sns/alerts?tab=details", nil).Body.String()
	if strings.Contains(after, "partner-publish") {
		t.Errorf("the permission survived removal:\n%s", truncateBody(after))
	}

	if bad := req(t, h, "POST", "/_console/sns/alerts/permission",
		url.Values{"label": {""}}); bad.Code < 400 {
		t.Errorf("an unlabelled permission must be refused, got %d", bad.Code)
	}
}

// TestConsoleTopicPolicyFullDocument: the topic's Policy attribute, set as a
// whole document rather than through the grant form.
func TestConsoleTopicPolicyFullDocument(t *testing.T) {
	h := newConsole(t)
	create(t, h, "/_console/sns/create", url.Values{"name": {"broadcast"}})

	doc := accessDoc("TopicWide", "sns:Publish", "*")
	rec := req(t, h, "POST", "/_console/sns/broadcast/attribute",
		url.Values{"name": {"Policy"}, "value": {doc}})
	if rec.Code >= 400 {
		t.Fatalf("set topic policy: %d\n%s", rec.Code, rec.Body)
	}
	page := req(t, h, "GET", "/_console/sns/broadcast?tab=details", nil).Body.String()
	if !strings.Contains(page, "TopicWide") {
		t.Errorf("the topic page does not show the policy that was set:\n%s", truncateBody(page))
	}
}
