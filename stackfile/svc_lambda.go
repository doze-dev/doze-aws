package stackfile

// Lambda apply + export: functions, async destinations, triggers, and tags.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// ---- functions ----

func applyFunctions(ctx context.Context, c *client, s *Stack, rep *Report) error {
	for _, name := range sortedNames(s.Functions) {
		f := s.Functions[name]
		code, err := filepath.Abs(f.Code)
		if err != nil {
			return fmt.Errorf("function %q: %w", name, err)
		}
		if _, err := os.Stat(code); err != nil {
			return fmt.Errorf("function %q: code path %s does not exist", name, code)
		}

		exists := true
		if _, err := c.do(ctx, "GET", "/2015-03-31/functions/"+url.PathEscape(name), nil, nil); err != nil {
			if !notFound(err) {
				return fmt.Errorf("function %q: %w", name, err)
			}
			exists = false
		}

		cfg := map[string]any{
			"FunctionName": name,
			"Runtime":      f.Runtime,
			"Handler":      f.Handler,
			"Role":         "arn:aws:iam::" + "000000000000" + ":role/stackfile",
		}
		if f.Timeout > 0 {
			cfg["Timeout"] = f.Timeout
		}
		if f.Memory > 0 {
			cfg["MemorySize"] = f.Memory
		}
		if len(f.Env) > 0 {
			cfg["Environment"] = map[string]any{"Variables": f.Env}
		}
		if len(f.Command) > 0 {
			cfg["Command"] = f.Command
		}
		if f.DLQ != nil {
			cfg["DeadLetterConfig"] = map[string]string{"TargetArn": f.DLQ.arn()}
		}

		if !exists {
			cfg["Code"] = map[string]string{"S3Bucket": "_local_", "S3Key": code}
			if _, err := c.do(ctx, "POST", "/2015-03-31/functions", map[string]string{"Content-Type": "application/json"}, mustJSON(cfg)); err != nil {
				return fmt.Errorf("function %q: %w", name, err)
			}
			rep.add("created", "function/"+name, "")
		} else {
			delete(cfg, "FunctionName")
			delete(cfg, "Role")
			if _, err := c.do(ctx, "PUT", "/2015-03-31/functions/"+url.PathEscape(name)+"/configuration", map[string]string{"Content-Type": "application/json"}, mustJSON(cfg)); err != nil {
				return fmt.Errorf("function %q config: %w", name, err)
			}
			codeReq := map[string]string{"S3Bucket": "_local_", "S3Key": code}
			if _, err := c.do(ctx, "PUT", "/2015-03-31/functions/"+url.PathEscape(name)+"/code", map[string]string{"Content-Type": "application/json"}, mustJSON(codeReq)); err != nil {
				return fmt.Errorf("function %q code: %w", name, err)
			}
			rep.add("updated", "function/"+name, "configuration + code path")
		}

		// Async destinations and retry policy (upsert).
		if f.OnSuccess != nil || f.OnFailure != nil || f.Retries != nil {
			eic := map[string]any{}
			dc := map[string]any{}
			if f.OnSuccess != nil {
				dc["OnSuccess"] = map[string]string{"Destination": f.OnSuccess.arn()}
			}
			if f.OnFailure != nil {
				dc["OnFailure"] = map[string]string{"Destination": f.OnFailure.arn()}
			}
			if len(dc) > 0 {
				eic["DestinationConfig"] = dc
			}
			if f.Retries != nil {
				eic["MaximumRetryAttempts"] = *f.Retries
			}
			body := mustJSON(eic)
			if _, err := c.do(ctx, "PUT", "/2019-09-25/functions/"+url.PathEscape(name)+"/event-invoke-config", map[string]string{"Content-Type": "application/json"}, body); err != nil {
				return fmt.Errorf("function %q destinations: %w", name, err)
			}
		}

		// Tags merge idempotently.
		if len(f.Tags) > 0 {
			body := mustJSON(map[string]any{"Tags": f.Tags})
			if _, err := c.do(ctx, "POST", "/2017-03-31/tags/"+lambdaARN(name), map[string]string{"Content-Type": "application/json"}, body); err != nil {
				return fmt.Errorf("function %q tags: %w", name, err)
			}
		}

		// Event source mappings: create the missing ones by source ARN and
		// converge an explicit enabled flag on the ones already wired.
		if len(f.Triggers) > 0 {
			existing := map[string]string{} // source ARN → mapping UUID
			if out, err := c.do(ctx, "GET", "/2015-03-31/event-source-mappings?FunctionName="+url.QueryEscape(name), nil, nil); err == nil {
				var lst struct {
					EventSourceMappings []struct {
						UUID           string `json:"UUID"`
						EventSourceArn string `json:"EventSourceArn"`
					} `json:"EventSourceMappings"`
				}
				json.Unmarshal(out, &lst)
				for _, m := range lst.EventSourceMappings {
					existing[m.EventSourceArn] = m.UUID
				}
			}
			for _, tr := range f.Triggers {
				arn := queueARN(tr.Queue)
				if uuid, ok := existing[arn]; ok {
					if tr.Enabled != nil {
						body := mustJSON(map[string]any{"Enabled": *tr.Enabled})
						if _, err := c.do(ctx, "PUT", "/2015-03-31/event-source-mappings/"+uuid, map[string]string{"Content-Type": "application/json"}, body); err != nil {
							return fmt.Errorf("function %q trigger %s: %w", name, tr.Queue, err)
						}
					}
					continue
				}
				in := map[string]any{"FunctionName": name, "EventSourceArn": arn}
				if tr.Batch > 0 {
					in["BatchSize"] = tr.Batch
				}
				if tr.Enabled != nil {
					in["Enabled"] = *tr.Enabled
				}
				if _, err := c.do(ctx, "POST", "/2015-03-31/event-source-mappings", map[string]string{"Content-Type": "application/json"}, mustJSON(in)); err != nil {
					return fmt.Errorf("function %q trigger %s: %w", name, tr.Queue, err)
				}
				rep.add("created", "function/"+name, "trigger "+tr.Queue)
			}
		}
	}
	return nil
}

func (d *Dest) arn() string {
	switch {
	case d.Queue != "":
		return queueARN(d.Queue)
	case d.Topic != "":
		return topicARN(d.Topic)
	case d.Lambda != "":
		return lambdaARN(d.Lambda)
	}
	return ""
}

func exportFunctions(ctx context.Context, c *client, s *Stack) error {
	out, err := c.do(ctx, "GET", "/2015-03-31/functions", nil, nil)
	if err != nil {
		return err
	}
	var lst struct {
		Functions []struct {
			FunctionName string
			Runtime      string
			Handler      string
			Timeout      int
			MemorySize   int
			Environment  struct {
				Variables map[string]string
			}
			DeadLetterConfig struct {
				TargetArn string
			}
		}
	}
	json.Unmarshal(out, &lst)
	if len(lst.Functions) > 0 {
		s.Functions = map[string]Function{}
	}
	for _, fn := range lst.Functions {
		f := Function{
			Runtime: fn.Runtime, Handler: fn.Handler,
			Env:  fn.Environment.Variables,
			Code: "<local code path — set me>", // the wire doesn't echo local dirs
		}
		if fn.Timeout != 0 && fn.Timeout != 3 {
			f.Timeout = fn.Timeout
		}
		if fn.MemorySize != 0 && fn.MemorySize != 512 { // 512 is the server default
			f.Memory = fn.MemorySize
		}
		f.DLQ = destFromARN(fn.DeadLetterConfig.TargetArn)
		// Code location: the _local_ extension reports it via GetFunction.
		if out, err := c.do(ctx, "GET", "/2015-03-31/functions/"+url.PathEscape(fn.FunctionName), nil, nil); err == nil {
			var g struct {
				Code struct {
					Location string
				}
			}
			json.Unmarshal(out, &g)
			if loc := strings.TrimPrefix(g.Code.Location, "local://"); loc != g.Code.Location {
				f.Code = loc
			}
		}
		// Destinations + retry policy.
		if out, err := c.do(ctx, "GET", "/2019-09-25/functions/"+url.PathEscape(fn.FunctionName)+"/event-invoke-config", nil, nil); err == nil {
			var eic struct {
				DestinationConfig struct {
					OnSuccess, OnFailure struct{ Destination string }
				}
				MaximumRetryAttempts *int
			}
			json.Unmarshal(out, &eic)
			f.OnSuccess = destFromARN(eic.DestinationConfig.OnSuccess.Destination)
			f.OnFailure = destFromARN(eic.DestinationConfig.OnFailure.Destination)
			if eic.MaximumRetryAttempts != nil && *eic.MaximumRetryAttempts != 2 { // 2 is the default
				f.Retries = eic.MaximumRetryAttempts
			}
		}
		// Triggers.
		if out, err := c.do(ctx, "GET", "/2015-03-31/event-source-mappings?FunctionName="+url.QueryEscape(fn.FunctionName), nil, nil); err == nil {
			var esm struct {
				EventSourceMappings []struct {
					EventSourceArn string
					BatchSize      int
					State          string
				}
			}
			json.Unmarshal(out, &esm)
			for _, m := range esm.EventSourceMappings {
				tr := Trigger{Queue: arnLeaf(m.EventSourceArn)}
				if m.BatchSize != 0 && m.BatchSize != 10 {
					tr.Batch = m.BatchSize
				}
				if m.State == "Disabled" {
					v := false
					tr.Enabled = &v
				}
				f.Triggers = append(f.Triggers, tr)
			}
		}
		// Tags.
		if out, err := c.do(ctx, "GET", "/2017-03-31/tags/"+lambdaARN(fn.FunctionName), nil, nil); err == nil {
			var lt struct {
				Tags map[string]string
			}
			json.Unmarshal(out, &lt)
			if len(lt.Tags) > 0 {
				f.Tags = lt.Tags
			}
		}
		s.Functions[fn.FunctionName] = f
	}
	return nil
}

func destFromARN(arn string) *Dest {
	if arn == "" {
		return nil
	}
	leaf := arnLeaf(arn)
	switch {
	case strings.Contains(arn, ":sqs:"):
		return &Dest{Queue: leaf}
	case strings.Contains(arn, ":sns:"):
		return &Dest{Topic: leaf}
	case strings.Contains(arn, ":lambda:"):
		return &Dest{Lambda: strings.TrimPrefix(leaf, "function:")}
	}
	return nil
}
