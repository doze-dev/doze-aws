package console_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"testing"
)

// Every link, hx-get and live region on the SQS pages opens, in each state a
// queue can be in. A 404 on "/sqs%2F<dlq>" was seen once in a link crawl of the
// console and never reproduced (plan/services/sqs.md, SQS-54); this is the crawl,
// kept: it follows what the server renders (the browser-side crawl, which also
// follows what JavaScript writes, is e2e/tests/sqs-links.spec.ts).
//
// The states: a queue with a dead-letter queue made alongside, one with an
// existing dead-letter queue picked, a FIFO queue with its own, messages waiting,
// held and delivered past maxReceiveCount (so a dead-letter queue has rows and a
// "fed by" line), a finished move task, a function reading one queue, a topic
// feeding another, and a name at the edge of the rule (80 characters, hyphens and
// underscores).
func TestEveryLinkOnTheSQSPagesOpens(t *testing.T) {
	h, gateway := newConsoleStack(t)
	sqs := func(op string, body string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest("POST", "/", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/x-amz-json-1.0")
		r.Header.Set("X-Amz-Target", "AmazonSQS."+op)
		rec := httptest.NewRecorder()
		gateway.ServeHTTP(rec, r)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: %d\n%s", op, rec.Code, rec.Body)
		}
		return rec
	}
	post := func(path string, form url.Values) {
		t.Helper()
		if rec := req(t, h, "POST", path, form); rec.Code != http.StatusSeeOther && rec.Code != http.StatusOK {
			t.Fatalf("POST %s: %d\n%s", path, rec.Code, rec.Body)
		}
	}
	queueURL := func(name string) string { return "http://example.test/000000000000/" + name }

	// A queue with a dead-letter queue made alongside, and one picked.
	post("/_console/sqs/create", url.Values{"name": {"orders"}, "dlq_mode": {"new"}, "max_receive": {"2"}, "visibility": {"30"}})
	post("/_console/sqs/create", url.Values{"name": {"audit_log-1"}, "dlq_mode": {"existing"}, "dlq": {"orders-dlq"}, "max_receive": {"3"}})
	post("/_console/sqs/create", url.Values{"name": {"depot"}, "fifo": {"on"}, "dlq_mode": {"new"}})
	post("/_console/sqs/create", url.Values{"name": {strings.Repeat("n", 80)}, "dlq_mode": {"none"}})
	post("/_console/sqs/create", url.Values{"name": {"plain"}, "dlq_mode": {"none"}})

	// Messages: waiting, held, and past maxReceiveCount, so the dead-letter queue has rows.
	for i := 0; i < 3; i++ {
		sqs("SendMessage", `{"QueueUrl":"`+queueURL("orders")+`","MessageBody":"{\"n\":`+string(rune('0'+i))+`}"}`)
	}
	for i := 0; i < 3; i++ {
		sqs("ReceiveMessage", `{"QueueUrl":"`+queueURL("orders")+`","MaxNumberOfMessages":10,"VisibilityTimeout":0}`)
	}
	sqs("SendMessage", `{"QueueUrl":"`+queueURL("orders")+`","MessageBody":"held"}`)
	sqs("ReceiveMessage", `{"QueueUrl":"`+queueURL("orders")+`","MaxNumberOfMessages":1,"VisibilityTimeout":600}`)
	sqs("SendMessage", `{"QueueUrl":"`+queueURL("depot.fifo")+`","MessageBody":"v17","MessageGroupId":"g","MessageDeduplicationId":"d1"}`)

	// A finished move task, so the redrive card has a row and "Open <dest>".
	post("/_console/sqs/create", url.Values{"name": {"returns"}, "dlq_mode": {"none"}})

	// Neighbours: a topic feeding a queue.
	if rec := req(t, gateway, "POST", "/", url.Values{"Action": {"CreateTopic"}, "Name": {"orders-topic"}, "Version": {"2010-03-31"}}); rec.Code != http.StatusOK {
		t.Fatalf("CreateTopic: %d\n%s", rec.Code, rec.Body)
	}
	if rec := req(t, gateway, "POST", "/", url.Values{"Action": {"Subscribe"}, "TopicArn": {"arn:aws:sns:us-east-1:000000000000:orders-topic"},
		"Protocol": {"sqs"}, "Endpoint": {"arn:aws:sqs:us-east-1:000000000000:orders"}, "Version": {"2010-03-31"}}); rec.Code != http.StatusOK {
		t.Fatalf("Subscribe: %d\n%s", rec.Code, rec.Body)
	}

	var start []string
	start = append(start, "/_console/sqs", "/_console/sqs/create")
	for _, q := range []string{"orders", "orders-dlq", "audit_log-1", "depot.fifo", "depot-dlq.fifo", strings.Repeat("n", 80), "plain", "returns"} {
		start = append(start, "/_console/sqs/"+url.PathEscape(q))
		for _, tab := range []string{"consume", "config", "tags"} {
			start = append(start, "/_console/sqs/"+url.PathEscape(q)+"?tab="+tab)
		}
	}
	// "orders" is in the path of most of them, which is what lets the crawl read the
	// links on each page it reaches.
	var all []string
	for _, name := range []string{"orders", "audit", "depot", "plain", "returns", "nnnnnnnn"} {
		seen := crawl(t, h, name, "", start...)
		for target := range seen {
			all = append(all, target)
		}
	}
	sort.Strings(all)
	for _, target := range all {
		if strings.HasPrefix(target, "/_console/sqs%2F") || strings.HasPrefix(target, "/_console/sqs%2f") {
			t.Errorf("a link to %q has the slash after /sqs escaped: it is not the queue's page", target)
		}
	}
	if len(all) < 40 {
		t.Errorf("the crawl reached only %d targets:\n%s", len(all), strings.Join(all, "\n"))
	}
}

// The pages of other services that name a queue link to it: a stack's physical
// resource id is the queue's URL, an SNS subscription and a rule's target are
// its ARN, a dead-letter queue is a redrive policy's target. Each opens the
// queue's page, and none spells the queue as "/sqs%2F<name>".
func TestEveryLinkToAQueueFromAnotherServiceOpens(t *testing.T) {
	h, gateway := newConsoleStack(t)
	post := func(path string, form url.Values) {
		t.Helper()
		if rec := req(t, h, "POST", path, form); rec.Code != http.StatusSeeOther && rec.Code != http.StatusOK {
			t.Fatalf("POST %s: %d\n%s", path, rec.Code, rec.Body)
		}
	}
	call := func(target, ct, body string) {
		t.Helper()
		r := httptest.NewRequest("POST", "/", strings.NewReader(body))
		r.Header.Set("Content-Type", ct)
		if target != "" {
			r.Header.Set("X-Amz-Target", target)
		}
		rec := httptest.NewRecorder()
		gateway.ServeHTTP(rec, r)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: %d\n%s", target, rec.Code, rec.Body)
		}
	}
	const dlqARN = "arn:aws:sqs:us-east-1:000000000000:xs-dlq"
	post("/_console/cfn/create", url.Values{"name": {"xs-stack"}, "template": {`{"Resources":{
		"Main":{"Type":"AWS::SQS::Queue","Properties":{"QueueName":"xs-main","RedrivePolicy":{"deadLetterTargetArn":{"Fn::GetAtt":["Dlq","Arn"]},"maxReceiveCount":3}}},
		"Dlq":{"Type":"AWS::SQS::Queue","Properties":{"QueueName":"xs-dlq"}}}}`}})
	form := func(v url.Values) string { v.Set("Version", "2010-03-31"); return v.Encode() }
	call("", "application/x-www-form-urlencoded", form(url.Values{"Action": {"CreateTopic"}, "Name": {"xs-topic"}}))
	call("", "application/x-www-form-urlencoded", form(url.Values{"Action": {"Subscribe"}, "TopicArn": {"arn:aws:sns:us-east-1:000000000000:xs-topic"},
		"Protocol": {"sqs"}, "Endpoint": {"arn:aws:sqs:us-east-1:000000000000:xs-main"},
		"Attributes.entry.1.key": {"RedrivePolicy"}, "Attributes.entry.1.value": {`{"deadLetterTargetArn":"` + dlqARN + `"}`}}))
	call("AWSEvents.PutRule", "application/x-amz-json-1.1", `{"Name":"xs-rule","EventPattern":"{\"source\":[\"xs\"]}"}`)
	call("AWSEvents.PutTargets", "application/x-amz-json-1.1", `{"Rule":"xs-rule","Targets":[{"Id":"q","Arn":"arn:aws:sqs:us-east-1:000000000000:xs-main","DeadLetterConfig":{"Arn":"`+dlqARN+`"}}]}`)
	call("AmazonSQS.SendMessage", "application/x-amz-json-1.0", `{"QueueUrl":"http://example.test/000000000000/xs-main","MessageBody":"b"}`)

	var start []string
	for _, p := range []string{"cfn", "cfn/xs-stack", "cfn/xs-stack?tab=resources", "sns", "sns/xs-topic", "eb", "eb/default", "traffic", "connect", "lambda", "sqs/xs-main", "sqs/xs-dlq"} {
		start = append(start, "/_console/"+p)
	}
	seen := crawl(t, h, "xs", "", start...)
	var sqsTargets []string
	for target := range seen {
		if strings.HasPrefix(target, "/_console/sqs%2F") || strings.HasPrefix(target, "/_console/sqs%2f") {
			t.Errorf("a link to %q has the slash after /sqs escaped", target)
		}
		if strings.HasPrefix(target, "/_console/sqs/") {
			sqsTargets = append(sqsTargets, target)
		}
	}
	sort.Strings(sqsTargets)
	for _, q := range []string{"xs-main", "xs-dlq"} {
		found := false
		for _, target := range sqsTargets {
			if strings.HasPrefix(target, "/_console/sqs/"+q) {
				found = true
			}
		}
		if !found {
			t.Errorf("no page of another service links to queue %s; the crawl reached:\n%s", q, strings.Join(keysOf(seen), "\n"))
		}
	}
}
