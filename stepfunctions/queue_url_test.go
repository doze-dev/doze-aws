package stepfunctions

import "testing"

// SQS refuses a queue URL that names another account (SQS-23), so a task that
// sends to one must not deliver to the queue of the same name in this account:
// it names no queue here, and the task fails as it would on AWS for a queue
// the caller cannot reach. A URL with no account (a sibling spelling) or this
// account's is the queue here.
func TestAQueueURLInAnotherAccountIsNoQueueHere(t *testing.T) {
	for _, c := range []struct {
		url, own, name string
		ok             bool
	}{
		{"https://sqs.eu-west-1.amazonaws.com/111122223333/jobs", "111122223333", "jobs", true},
		{"http://127.0.0.1:4566/111122223333/jobs", "111122223333", "jobs", true},
		{"http://peer.invalid/jobs", "111122223333", "jobs", true},
		{"http://127.0.0.1:4566/000000000000/jobs", "111122223333", "", false},
		{"http://127.0.0.1:4566/999999999999/extra/jobs", "111122223333", "", false},
	} {
		name, ok := queueNameIn(c.url, c.own)
		if ok != c.ok || name != c.name {
			t.Errorf("queueNameIn(%q, %q) = %q, %v; want %q, %v", c.url, c.own, name, ok, c.name, c.ok)
		}
	}
}
