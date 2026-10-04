package provision

// Existing: which of a stack's named resources are already there.
//
// Apply converges: a resource that exists under a name the stack uses is
// adopted and brought in line. That is right for a stackfile, which describes
// the account rather than owning it. CloudFormation owns what it creates, and
// AWS fails a create whose name is taken — the stack never silently takes over
// a queue somebody else made. Existing is the read-only check that lets a
// caller refuse before Apply touches anything.
//
// Only kinds whose name AWS holds unique are probed. A REST API, an API key or
// a usage plan can share its name with another; a layer gains a version; an
// activity's create is idempotent. None of those can collide.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"

	"github.com/doze-dev/doze-aws/awsident"
)

// Existing returns the "kind/name" of every resource in s that already exists,
// sorted, in the spelling Names uses.
func Existing(ctx context.Context, gateway http.Handler, id awsident.Identity, s *Stack) ([]string, error) {
	c := newClient(gateway, id)
	var aliases map[string]bool // read once, on the first key
	var out []string
	var failed error
	Subset(s, func(kind, name string) bool {
		if failed != nil {
			return false
		}
		var there bool
		var err error
		switch kind {
		case "queue":
			there, err = present(c.sqs(ctx, "GetQueueUrl", map[string]any{"QueueName": name}))
		case "topic":
			there, err = present(c.query(ctx, url.Values{
				"Action": {"GetTopicAttributes"}, "TopicArn": {topicARN(id, name)},
			}))
		case "bucket":
			there, err = present(c.do(ctx, "HEAD", "/"+name, nil, nil))
		case "table":
			there, err = present(c.ddb(ctx, "DescribeTable", map[string]any{"TableName": name}))
		case "function":
			there, err = present(c.do(ctx, "GET", "/2015-03-31/functions/"+url.PathEscape(name), nil, nil))
		case "secret":
			there, err = present(c.json11(ctx, "secretsmanager", "DescribeSecret", map[string]any{"SecretId": name}))
		case "parameter":
			there, err = present(c.json11(ctx, "AmazonSSM", "GetParameter", map[string]any{"Name": name}))
		case "statemachine":
			there, err = present(c.sfn(ctx, "DescribeStateMachine", map[string]any{"stateMachineArn": stateMachineARN(id, name)}))
		case "rule":
			in := map[string]any{"Name": name}
			if bus := s.Rules[name].Bus; bus != "" && bus != "default" {
				in["EventBusName"] = bus
			}
			there, err = present(c.json11(ctx, "AWSEvents", "DescribeRule", in))
		case "connection":
			there, err = present(c.json11(ctx, "AWSEvents", "DescribeConnection", map[string]any{"Name": name}))
		case "apidestination":
			there, err = present(c.json11(ctx, "AWSEvents", "DescribeApiDestination", map[string]any{"Name": name}))
		case "dashboard":
			there, err = present(c.cw(ctx, "GetDashboard", map[string]any{"DashboardName": name}))
		case "loggroup":
			there, err = logGroupThere(ctx, c, name)
		case "alarm":
			there, err = alarmThere(ctx, c, name)
		case "key":
			if aliases == nil {
				aliases, err = aliasNames(ctx, c)
			}
			there = aliases["alias/"+name]
		}
		if err != nil {
			failed = fmt.Errorf("%s %q: %w", kind, name, err)
		} else if there {
			out = append(out, kind+"/"+name)
		}
		return false
	})
	if failed != nil {
		return nil, failed
	}
	sort.Strings(out)
	return out, nil
}

// present turns a describe call into yes, no, or "could not tell".
func present(_ []byte, err error) (bool, error) {
	switch {
	case err == nil:
		return true, nil
	case notFound(err):
		return false, nil
	}
	return false, err
}

func logGroupThere(ctx context.Context, c *client, name string) (bool, error) {
	body, err := c.json11(ctx, "Logs_20140328", "DescribeLogGroups", map[string]any{"logGroupNamePrefix": name})
	if err != nil {
		return false, err
	}
	var got struct {
		LogGroups []struct{ LogGroupName string } `json:"logGroups"`
	}
	json.Unmarshal(body, &got)
	for _, g := range got.LogGroups {
		if g.LogGroupName == name {
			return true, nil
		}
	}
	return false, nil
}

func alarmThere(ctx context.Context, c *client, name string) (bool, error) {
	body, err := c.cw(ctx, "DescribeAlarms", map[string]any{"AlarmNames": []string{name}})
	if err != nil {
		return false, err
	}
	var got struct {
		MetricAlarms    []json.RawMessage
		CompositeAlarms []json.RawMessage
	}
	json.Unmarshal(body, &got)
	return len(got.MetricAlarms)+len(got.CompositeAlarms) > 0, nil
}

func aliasNames(ctx context.Context, c *client) (map[string]bool, error) {
	body, err := c.json11(ctx, "TrentService", "ListAliases", map[string]any{})
	if err != nil {
		return nil, err
	}
	var got struct {
		Aliases []struct{ AliasName string }
	}
	json.Unmarshal(body, &got)
	out := map[string]bool{}
	for _, a := range got.Aliases {
		out[a.AliasName] = true
	}
	return out, nil
}
