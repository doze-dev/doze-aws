package provision

// CloudWatch Logs apply: log groups. A group a template declares exists
// before the function that writes to it, with the retention the template
// set; a group nobody declares is still created by Lambda on the first line.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

func applyLogGroups(ctx context.Context, c *client, s *Stack, rep *Report) error {
	for _, name := range sortedNames(s.LogGroups) {
		g := s.LogGroups[name]
		in := map[string]any{"logGroupName": name}
		if len(g.Tags) > 0 {
			in["tags"] = g.Tags
		}
		_, err := c.json11(ctx, "Logs_20140328", "CreateLogGroup", in)
		switch {
		case err == nil:
			rep.add("created", "loggroup/"+name, "")
		case strings.Contains(err.Error(), "ResourceAlreadyExistsException"):
			rep.add("skipped", "loggroup/"+name, "exists")
		default:
			return fmt.Errorf("log group %q: %w", name, err)
		}
		if g.RetentionDays > 0 {
			if _, err := c.json11(ctx, "Logs_20140328", "PutRetentionPolicy", map[string]any{
				"logGroupName": name, "retentionInDays": g.RetentionDays,
			}); err != nil {
				return fmt.Errorf("log group %q retention: %w", name, err)
			}
		}
	}
	return nil
}

func exportLogGroups(ctx context.Context, c *client, s *Stack) error {
	out, err := c.json11(ctx, "Logs_20140328", "DescribeLogGroups", map[string]any{})
	if err != nil {
		return fmt.Errorf("describe log groups: %w", err)
	}
	var list struct {
		LogGroups []struct {
			Name      string `json:"logGroupName"`
			Retention int    `json:"retentionInDays"`
		} `json:"logGroups"`
	}
	json.Unmarshal(out, &list)
	for _, g := range list.LogGroups {
		// Lambda's own groups follow the function; exporting them would
		// double-declare what the function's presence already implies.
		if strings.HasPrefix(g.Name, "/aws/lambda/") {
			continue
		}
		if s.LogGroups == nil {
			s.LogGroups = map[string]LogGroup{}
		}
		s.LogGroups[g.Name] = LogGroup{RetentionDays: g.Retention}
	}
	return nil
}

func destroyLogGroups(ctx context.Context, c *client, s *Stack, rep *DestroyReport) error {
	for _, name := range sortedNames(s.LogGroups) {
		_, err := c.json11(ctx, "Logs_20140328", "DeleteLogGroup", map[string]any{"logGroupName": name})
		record(rep, "loggroup/"+name, err)
	}
	return nil
}
