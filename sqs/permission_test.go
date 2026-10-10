package sqs

// AddPermission and RemovePermission, on both wire protocols.
//
// doc: the AddPermission and RemovePermission API reference pages. Label is at
// most 80 characters of letters, digits, hyphens and underscores; the actions
// are "the name of any action or *"; the principals are AWS account numbers. The exact
// message text is `memory` and is not asserted — only that it is a 400.

import (
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

func TestAddPermissionRules(t *testing.T) {
	ts := testServer(t)
	jsonBody(t, ts.URL, "CreateQueue", `{"QueueName":"perm"}`)
	qurl := ts.URL + "/000000000000/perm"

	add := func(protocol, label string, accounts, actions []string) int {
		if protocol == "json" {
			a, _ := json.Marshal(accounts)
			b, _ := json.Marshal(actions)
			code, _ := jsonBody(t, ts.URL, "AddPermission",
				`{"QueueUrl":"`+qurl+`","Label":`+strconv.Quote(label)+`,"AWSAccountIds":`+string(a)+`,"Actions":`+string(b)+`}`)
			return code
		}
		form := url.Values{"Action": {"AddPermission"}, "QueueUrl": {qurl}, "Label": {label}}
		for i, a := range accounts {
			form.Set("AWSAccountId."+strconv.Itoa(i+1), a)
		}
		for i, a := range actions {
			form.Set("ActionName."+strconv.Itoa(i+1), a)
		}
		code, _ := formPost(t, ts.URL, form)
		return code
	}
	const acct = "111122223333"

	for _, protocol := range []string{"json", "query"} {
		for _, c := range []struct {
			name     string
			label    string
			accounts []string
			actions  []string
			ok       bool
		}{
			{"a plain label", "p-" + protocol + "-1", []string{acct}, []string{"SendMessage"}, true},
			{"a label of 80 characters", protocol + strings.Repeat("a", 80-len(protocol)), []string{acct}, []string{"SendMessage"}, true},
			{"a label of 81 characters", strings.Repeat("a", 81), []string{acct}, []string{"SendMessage"}, false},
			{"a label with a space", "has space", []string{acct}, []string{"SendMessage"}, false},
			{"a label with a dot", "has.dot", []string{acct}, []string{"SendMessage"}, false},
			{"underscore and hyphen", "a_b-c" + protocol, []string{acct}, []string{"SendMessage"}, true},
			{"a handful of actions", "all-" + protocol, []string{acct},
				[]string{"ChangeMessageVisibility", "DeleteMessage", "GetQueueAttributes", "GetQueueUrl",
					"ListDeadLetterSourceQueues", "ListQueues", "PurgeQueue", "ReceiveMessage", "SendMessage"}, true},
			{"the wildcard", "star-" + protocol, []string{acct}, []string{"*"}, true},
			{"an action SQS has no such thing as", "bad-act-" + protocol, []string{acct}, []string{"DoEverything"}, false},
			{"an action of another service", "other-" + protocol, []string{acct}, []string{"s3:GetObject"}, false},
			{"an account number that is not twelve digits", "short-" + protocol, []string{"1234"}, []string{"SendMessage"}, false},
			{"an account number with a letter", "letter-" + protocol, []string{"11112222333x"}, []string{"SendMessage"}, false},
		} {
			if got := add(protocol, c.label, c.accounts, c.actions); c.ok && got != 200 || !c.ok && got != 400 {
				t.Errorf("%s AddPermission, %s: answered %d, ok=%v", protocol, c.name, got, c.ok)
			}
		}
	}

	// A label already in use is refused, whatever else the request carries.
	if got := add("json", "dup", []string{acct}, []string{"SendMessage"}); got != 200 {
		t.Fatalf("first AddPermission answered %d", got)
	}
	for _, protocol := range []string{"json", "query"} {
		if got := add(protocol, "dup", []string{"999988887777"}, []string{"ReceiveMessage"}); got != 400 {
			t.Errorf("%s AddPermission with a label in use answered %d, want 400", protocol, got)
		}
	}
}

func TestAddPermissionWritesTheStatement(t *testing.T) {
	ts := testServer(t)
	jsonBody(t, ts.URL, "CreateQueue", `{"QueueName":"stmt"}`)
	qurl := ts.URL + "/000000000000/stmt"
	if code, body := jsonBody(t, ts.URL, "AddPermission",
		`{"QueueUrl":"`+qurl+`","Label":"from-a","AWSAccountIds":["111122223333"],"Actions":["SendMessage","ReceiveMessage"]}`); code != 200 {
		t.Fatalf("AddPermission: %d %s", code, body)
	}
	_, body := jsonBody(t, ts.URL, "GetQueueAttributes", `{"QueueUrl":"`+qurl+`","AttributeNames":["Policy"]}`)
	var got struct {
		Attributes map[string]string
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("%v: %s", err, body)
	}
	var doc struct {
		Statement []map[string]any
	}
	if err := json.Unmarshal([]byte(got.Attributes["Policy"]), &doc); err != nil {
		t.Fatalf("the policy is not JSON: %v: %s", err, got.Attributes["Policy"])
	}
	if len(doc.Statement) != 1 {
		t.Fatalf("want one statement, got %d: %s", len(doc.Statement), got.Attributes["Policy"])
	}
	st := doc.Statement[0]
	if st["Sid"] != "from-a" || st["Effect"] != "Allow" {
		t.Errorf("Sid/Effect = %v/%v", st["Sid"], st["Effect"])
	}
	if r, _ := st["Resource"].(string); !strings.HasSuffix(r, ":000000000000:stmt") {
		t.Errorf("Resource = %v, want the queue's ARN", st["Resource"])
	}
	if !strings.Contains(got.Attributes["Policy"], "arn:aws:iam::111122223333:root") {
		t.Errorf("the principal is not the account's root ARN: %s", got.Attributes["Policy"])
	}
	for _, a := range []string{"SQS:SendMessage", "SQS:ReceiveMessage"} {
		if !strings.Contains(got.Attributes["Policy"], a) {
			t.Errorf("the policy lacks %s: %s", a, got.Attributes["Policy"])
		}
	}
}

func TestRemovePermissionRules(t *testing.T) {
	ts := testServer(t)
	jsonBody(t, ts.URL, "CreateQueue", `{"QueueName":"unperm"}`)
	qurl := ts.URL + "/000000000000/unperm"
	remove := func(protocol, label string) int {
		if protocol == "json" {
			code, _ := jsonBody(t, ts.URL, "RemovePermission", `{"QueueUrl":"`+qurl+`","Label":`+strconv.Quote(label)+`}`)
			return code
		}
		code, _ := formPost(t, ts.URL, url.Values{"Action": {"RemovePermission"}, "QueueUrl": {qurl}, "Label": {label}})
		return code
	}

	for _, protocol := range []string{"json", "query"} {
		if got := remove(protocol, "never-added"); got != 400 {
			t.Errorf("%s RemovePermission of a label that is not there answered %d, want 400", protocol, got)
		}
	}
	for _, label := range []string{"one", "two"} {
		if code, body := jsonBody(t, ts.URL, "AddPermission",
			`{"QueueUrl":"`+qurl+`","Label":"`+label+`","AWSAccountIds":["111122223333"],"Actions":["SendMessage"]}`); code != 200 {
			t.Fatalf("AddPermission %s: %d %s", label, code, body)
		}
	}
	if got := remove("json", "one"); got != 200 {
		t.Fatalf("RemovePermission of a label that is there answered %d", got)
	}
	if got := remove("query", "one"); got != 400 {
		t.Errorf("removing the same label twice answered %d, want 400", got)
	}
	// Only that statement went; the other stays.
	_, body := jsonBody(t, ts.URL, "GetQueueAttributes", `{"QueueUrl":"`+qurl+`","AttributeNames":["Policy"]}`)
	if !strings.Contains(body, `\"two\"`) || strings.Contains(body, `\"one\"`) {
		t.Errorf("after removing one, the policy reads: %s", body)
	}
	// Removing the last leaves no policy at all, not an empty Statement list,
	// which the policy validator would refuse to store.
	if got := remove("json", "two"); got != 200 {
		t.Fatalf("RemovePermission of the last label answered %d", got)
	}
	_, body = jsonBody(t, ts.URL, "GetQueueAttributes", `{"QueueUrl":"`+qurl+`","AttributeNames":["Policy"]}`)
	if strings.Contains(body, "Statement") {
		t.Errorf("a queue with its last permission removed still has a policy: %s", body)
	}
	// And the label is free to use again.
	if code, _ := jsonBody(t, ts.URL, "AddPermission",
		`{"QueueUrl":"`+qurl+`","Label":"one","AWSAccountIds":["111122223333"],"Actions":["SendMessage"]}`); code != 200 {
		t.Errorf("a removed label could not be reused: %d", code)
	}
}
