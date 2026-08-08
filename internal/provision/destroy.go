package provision

// Destroy is the inverse of Apply: it removes the resources a stack describes.
//
// Apply is deliberately create-or-update and never deletes, because a stack
// file is a description of what should exist, not an exhaustive statement of
// what may. Destroy is the explicit opposite — you are naming exactly what to
// take away — so it lives beside Apply rather than being a mode of it.
//
// Two properties matter:
//
//   - It is TOLERANT. A resource that is already gone is not an error; the
//     goal state is "absent" and it is already met.
//   - It does not stop at the first failure. A half-destroyed stack that
//     refuses to continue is worse than one that removes what it can and
//     reports precisely what it could not.
//
// Phases run in reverse dependency order relative to Apply, so a subscription
// goes before its topic and a trigger before its queue.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// DestroyReport is the outcome of a Destroy.
type DestroyReport struct {
	Actions []Action
}

func (r *DestroyReport) add(op, resource, detail string) {
	r.Actions = append(r.Actions, Action{Op: op, Resource: resource, Detail: detail})
}

// Counts summarises the report as deleted/absent/failed.
func (r *DestroyReport) Counts() (deleted, absent, failed int) {
	for _, a := range r.Actions {
		switch a.Op {
		case "deleted":
			deleted++
		case "absent":
			absent++
		default:
			failed++
		}
	}
	return
}

// Failures returns the resources that could not be removed.
func (r *DestroyReport) Failures() []Action {
	var out []Action
	for _, a := range r.Actions {
		if a.Op == "failed" {
			out = append(out, a)
		}
	}
	return out
}

// Destroy removes every resource named in s. It returns a report even when it
// also returns an error, so a caller can always see how far it got.
func Destroy(ctx context.Context, gateway http.Handler, s *Stack) (*DestroyReport, error) {
	c := newClient(gateway)
	rep := &DestroyReport{}

	// The reverse of Apply's order: dependents first.
	phases := []struct {
		name string
		run  func() error
	}{
		{"apis", func() error { return destroyAPIs(ctx, c, s, rep) }},
		{"parameters", func() error { return destroyParameters(ctx, c, s, rep) }},
		{"secrets", func() error { return destroySecrets(ctx, c, s, rep) }},
		{"rules", func() error { return destroyRules(ctx, c, s, rep) }},
		{"topics", func() error { return destroyTopics(ctx, c, s, rep) }},
		{"functions", func() error { return destroyFunctions(ctx, c, s, rep) }},
		{"buckets", func() error { return destroyBuckets(ctx, c, s, rep) }},
		{"keys", func() error { return destroyKeys(ctx, c, s, rep) }},
		{"tables", func() error { return destroyTables(ctx, c, s, rep) }},
		{"queues", func() error { return destroyQueues(ctx, c, s, rep) }},
	}
	for _, p := range phases {
		if err := p.run(); err != nil {
			return rep, fmt.Errorf("stackfile: destroy %s: %w", p.name, err)
		}
	}
	if failed := rep.Failures(); len(failed) > 0 {
		return rep, fmt.Errorf("stackfile: %d resource(s) could not be removed", len(failed))
	}
	return rep, nil
}

// record turns a delete call's outcome into a report line. A "not found" is
// success: the resource is absent, which is what was asked for.
func record(rep *DestroyReport, resource string, err error) {
	switch {
	case err == nil:
		rep.add("deleted", resource, "")
	case notFound(err):
		rep.add("absent", resource, "")
	default:
		rep.add("failed", resource, err.Error())
	}
}

// ---- per-service teardown ----

func destroyQueues(ctx context.Context, c *client, s *Stack, rep *DestroyReport) error {
	for _, name := range sortedNames(s.Queues) {
		q := s.Queues[name]
		// An auto-created dead-letter queue belongs to this stack too.
		if q.DLQ == "auto" {
			dlq := autoDLQName(name, q.FIFO)
			record(rep, "queue/"+dlq, deleteQueue(ctx, c, dlq))
		}
		record(rep, "queue/"+name, deleteQueue(ctx, c, name))
	}
	return nil
}

func deleteQueue(ctx context.Context, c *client, name string) error {
	out, err := c.sqs(ctx, "GetQueueUrl", map[string]any{"QueueName": name})
	if err != nil {
		return err
	}
	var got struct {
		QueueUrl string `json:"QueueUrl"`
	}
	if json.Unmarshal(out, &got) != nil || got.QueueUrl == "" {
		got.QueueUrl = queueURL(name)
	}
	_, err = c.sqs(ctx, "DeleteQueue", map[string]any{"QueueUrl": got.QueueUrl})
	return err
}

func destroyTopics(ctx context.Context, c *client, s *Stack, rep *DestroyReport) error {
	for _, name := range sortedNames(s.Topics) {
		_, err := c.query(ctx, url.Values{
			"Action": {"DeleteTopic"}, "TopicArn": {topicARN(name)}, "Version": {"2010-03-31"},
		})
		record(rep, "topic/"+name, err)
	}
	return nil
}

func destroyBuckets(ctx context.Context, c *client, s *Stack, rep *DestroyReport) error {
	for _, name := range sortedNames(s.Buckets) {
		record(rep, "bucket/"+name, deleteBucket(ctx, c, name))
	}
	return nil
}

// deleteBucket empties a bucket first: S3 refuses to remove one that still has
// objects, and a versioned bucket needs its versions removed too.
func deleteBucket(ctx context.Context, c *client, name string) error {
	if out, err := c.do(ctx, "GET", "/"+name+"?versions", nil, nil); err == nil {
		for _, obj := range parseVersions(string(out)) {
			path := "/" + name + "/" + obj.key
			if obj.version != "" {
				path += "?versionId=" + url.QueryEscape(obj.version)
			}
			_, _ = c.do(ctx, "DELETE", path, nil, nil)
		}
	}
	// An unversioned listing catches anything the versions call missed.
	if out, err := c.do(ctx, "GET", "/"+name+"?list-type=2", nil, nil); err == nil {
		for _, key := range xmlValues(string(out), "Key") {
			_, _ = c.do(ctx, "DELETE", "/"+name+"/"+key, nil, nil)
		}
	}
	_, err := c.do(ctx, "DELETE", "/"+name, nil, nil)
	return err
}

type objectVersion struct{ key, version string }

// parseVersions pairs each <Key> with the <VersionId> that follows it in a
// ListObjectVersions response.
func parseVersions(body string) []objectVersion {
	var out []objectVersion
	rest := body
	for {
		i := strings.Index(rest, "<Key>")
		if i < 0 {
			return out
		}
		rest = rest[i+len("<Key>"):]
		j := strings.Index(rest, "</Key>")
		if j < 0 {
			return out
		}
		key := rest[:j]
		rest = rest[j:]
		version := ""
		// The VersionId belongs to this entry only if it appears before the
		// next Key.
		if v := strings.Index(rest, "<VersionId>"); v >= 0 {
			nextKey := strings.Index(rest, "<Key>")
			if nextKey < 0 || v < nextKey {
				tail := rest[v+len("<VersionId>"):]
				if e := strings.Index(tail, "</VersionId>"); e >= 0 {
					version = tail[:e]
				}
			}
		}
		out = append(out, objectVersion{key, version})
	}
}

func destroyTables(ctx context.Context, c *client, s *Stack, rep *DestroyReport) error {
	for _, name := range sortedNames(s.Tables) {
		_, err := c.ddb(ctx, "DeleteTable", map[string]any{"TableName": name})
		record(rep, "table/"+name, err)
	}
	return nil
}

func destroyFunctions(ctx context.Context, c *client, s *Stack, rep *DestroyReport) error {
	for _, name := range sortedNames(s.Functions) {
		// Event source mappings hold the function open; remove them first.
		if out, err := c.do(ctx, "GET", "/2015-03-31/event-source-mappings?FunctionName="+url.QueryEscape(name), nil, nil); err == nil {
			var listed struct {
				EventSourceMappings []struct {
					UUID string `json:"UUID"`
				} `json:"EventSourceMappings"`
			}
			if json.Unmarshal(out, &listed) == nil {
				for _, m := range listed.EventSourceMappings {
					_, _ = c.do(ctx, "DELETE", "/2015-03-31/event-source-mappings/"+m.UUID, nil, nil)
				}
			}
		}
		_, err := c.do(ctx, "DELETE", "/2015-03-31/functions/"+url.PathEscape(name), nil, nil)
		record(rep, "function/"+name, err)
	}
	return nil
}

func destroyRules(ctx context.Context, c *client, s *Stack, rep *DestroyReport) error {
	for _, name := range sortedNames(s.Rules) {
		r := s.Rules[name]
		bus := r.Bus
		req := map[string]any{"Name": name, "Force": true}
		if bus != "" {
			req["EventBusName"] = bus
		}
		// Targets must be removed before the rule they belong to.
		if ids := targetIDs(len(r.Targets)); len(ids) > 0 {
			remove := map[string]any{"Rule": name, "Ids": ids, "Force": true}
			if bus != "" {
				remove["EventBusName"] = bus
			}
			_, _ = c.json11(ctx, "AWSEvents", "RemoveTargets", remove)
		}
		_, err := c.json11(ctx, "AWSEvents", "DeleteRule", req)
		record(rep, "rule/"+name, err)
	}
	return nil
}

// targetIDs reproduces the ids Apply assigns to a rule's targets ("1", "2", …).
func targetIDs(n int) []string {
	out := make([]string, 0, n)
	for i := 1; i <= n; i++ {
		out = append(out, fmt.Sprint(i))
	}
	return out
}

func destroyKeys(ctx context.Context, c *client, s *Stack, rep *DestroyReport) error {
	for _, name := range sortedNames(s.Keys) {
		// KMS keys are scheduled for deletion, never removed outright — even
		// locally, because code that reads a deleted key should see the same
		// PendingDeletion state it would in the cloud. The alias goes now.
		_, aliasErr := c.json11(ctx, "TrentService", "DeleteAlias",
			map[string]any{"AliasName": "alias/" + name})
		_, err := c.json11(ctx, "TrentService", "ScheduleKeyDeletion",
			map[string]any{"KeyId": "alias/" + name, "PendingWindowInDays": 7})
		if err == nil {
			err = nil // the schedule succeeded; the alias result is incidental
		} else if notFound(aliasErr) && notFound(err) {
			err = aliasErr
		}
		record(rep, "key/"+name, err)
	}
	return nil
}

func destroySecrets(ctx context.Context, c *client, s *Stack, rep *DestroyReport) error {
	for _, name := range sortedNames(s.Secrets) {
		_, err := c.json11(ctx, "secretsmanager", "DeleteSecret", map[string]any{
			"SecretId": name, "ForceDeleteWithoutRecovery": true,
		})
		record(rep, "secret/"+name, err)
	}
	return nil
}

func destroyParameters(ctx context.Context, c *client, s *Stack, rep *DestroyReport) error {
	for _, name := range sortedNames(s.Parameters) {
		_, err := c.json11(ctx, "AmazonSSM", "DeleteParameter", map[string]any{"Name": name})
		record(rep, "parameter/"+name, err)
	}
	return nil
}
