package stepfunctions

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/doze-dev/doze-aws/internal/asl"
	"github.com/doze-dev/doze-aws/internal/bg"
	"github.com/doze-dev/doze-aws/internal/trace"
)

// The S3 halves of a Map Run: an ItemReader fetches the items, a
// ResultWriter stores the outcomes. Both are peer calls on a worker, handed
// back to the driver as deliveries, so the driver never waits on a bucket.

// readItems fetches the ItemReader's items on a worker and delivers them.
func (g *engine) readItems(r *run, f *asl.Frame, cfg asl.MapRunConfig) {
	key, frame, header := r.key, f.ID, r.e.TraceHeader
	reader := cfg.ItemReader
	input := f.Input
	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		defer bg.Recover(g.srv.logf, "stepfunctions: Map Run item reader")
		ctx := trace.Continue(g.workerCtx, g.srv.sink, header)
		items, fail := g.srv.fetchItems(ctx, reader, input)
		d := delivery{key: key, frame: frame, kind: dlvMapItems, items: items}
		if fail != nil {
			d.result = asl.TaskResult{Failure: fail}
		}
		select {
		case g.deliveries <- d:
		case <-g.stop:
		}
	}()
}

// fetchItems runs the ItemReader: s3:getObject with a JSON, JSONL or CSV
// body, or s3:listObjectsV2 whose listing is the item set. Parameters are
// the reader's, evaluated against the state's input like a Task's.
func (s *Server) fetchItems(ctx context.Context, reader json.RawMessage, input json.RawMessage) ([]json.RawMessage, *asl.Failure) {
	var rd struct {
		Resource     string          `json:"Resource"`
		Parameters   json.RawMessage `json:"Parameters"`
		ReaderConfig struct {
			InputType         string   `json:"InputType"`
			CSVHeaderLocation string   `json:"CSVHeaderLocation"`
			CSVHeaders        []string `json:"CSVHeaders"`
			MaxItems          int      `json:"MaxItems"`
		} `json:"ReaderConfig"`
	}
	if err := json.Unmarshal(reader, &rd); err != nil {
		return nil, asl.Failf(asl.ErrItemReaderFailed, "ItemReader: %v", err)
	}
	params, fail := asl.EvalParameters(rd.Parameters, input)
	if fail != nil {
		return nil, fail
	}
	action := strings.TrimPrefix(rd.Resource, "arn:aws:states:::s3:")
	res := s.callS3(ctx, action, params)
	if res.Failure != nil {
		return nil, &asl.Failure{Name: asl.ErrItemReaderFailed, Cause: res.Failure.Name + ": " + res.Failure.Cause}
	}
	var items []json.RawMessage
	switch action {
	case "listObjectsV2":
		var out struct {
			Contents []json.RawMessage `json:"Contents"`
		}
		json.Unmarshal(res.Output, &out)
		items = out.Contents
	case "getObject":
		var out struct {
			Body string `json:"Body"`
		}
		json.Unmarshal(res.Output, &out)
		var err error
		items, err = parseItems(rd.ReaderConfig.InputType, rd.ReaderConfig.CSVHeaderLocation, rd.ReaderConfig.CSVHeaders, []byte(out.Body))
		if err != nil {
			return nil, asl.Failf(asl.ErrItemReaderFailed, "%v", err)
		}
	default:
		return nil, asl.Failf(asl.ErrItemReaderFailed, "ItemReader resource %q is not s3:getObject or s3:listObjectsV2", rd.Resource)
	}
	if rd.ReaderConfig.MaxItems > 0 && len(items) > rd.ReaderConfig.MaxItems {
		items = items[:rd.ReaderConfig.MaxItems]
	}
	return items, nil
}

// parseItems decodes an object body as JSON (an array), JSONL, or CSV with
// headers from the first row or given.
func parseItems(inputType, headerLoc string, headers []string, body []byte) ([]json.RawMessage, error) {
	switch strings.ToUpper(inputType) {
	case "", "JSON":
		var arr []json.RawMessage
		if err := json.Unmarshal(body, &arr); err != nil {
			return nil, fmt.Errorf("the object is not a JSON array: %v", err)
		}
		return arr, nil
	case "JSONL":
		var out []json.RawMessage
		for _, line := range bytes.Split(body, []byte("\n")) {
			line = bytes.TrimSpace(line)
			if len(line) == 0 {
				continue
			}
			if !json.Valid(line) {
				return nil, fmt.Errorf("a JSONL line is not JSON: %s", line)
			}
			out = append(out, json.RawMessage(line))
		}
		return out, nil
	case "CSV":
		rows, err := csv.NewReader(bytes.NewReader(body)).ReadAll()
		if err != nil {
			return nil, fmt.Errorf("CSV: %v", err)
		}
		if strings.ToUpper(headerLoc) != "GIVEN" {
			if len(rows) == 0 {
				return nil, nil
			}
			headers, rows = rows[0], rows[1:]
		}
		var out []json.RawMessage
		for _, row := range rows {
			obj := map[string]string{}
			for i, h := range headers {
				if i < len(row) {
					obj[h] = row[i]
				} else {
					obj[h] = ""
				}
			}
			raw, _ := json.Marshal(obj)
			out = append(out, raw)
		}
		return out, nil
	case "MANIFEST":
		return nil, fmt.Errorf("S3 inventory manifests are not supported locally; use JSON, JSONL or CSV")
	}
	return nil, fmt.Errorf("unknown ItemReader InputType %q", inputType)
}

// writeResults stores the outcomes through the ResultWriter on a worker:
// manifest.json, SUCCEEDED_0.json and FAILED_0.json under the prefix, the
// layout AWS documents, then delivers the pointer as the state's result.
func (g *engine) writeResults(r *run, f *asl.Frame, mr *MapRun, items []*MapItem) {
	key, frame, header := r.key, f.ID, r.e.TraceHeader
	writer := mr.ResultWriter
	arn := mr.ARN
	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		defer bg.Recover(g.srv.logf, "stepfunctions: Map Run result writer")
		ctx := trace.Continue(g.workerCtx, g.srv.sink, header)
		res := g.srv.storeResults(ctx, writer, arn, items)
		select {
		case g.deliveries <- delivery{key: key, frame: frame, kind: dlvMapResults, result: res}:
		case <-g.stop:
		}
	}()
}

func (s *Server) storeResults(ctx context.Context, writer json.RawMessage, arn string, items []*MapItem) asl.TaskResult {
	var w struct {
		Resource   string `json:"Resource"`
		Parameters struct {
			Bucket string `json:"Bucket"`
			Prefix string `json:"Prefix"`
		} `json:"Parameters"`
	}
	if err := json.Unmarshal(writer, &w); err != nil || w.Resource != "arn:aws:states:::s3:putObject" || w.Parameters.Bucket == "" {
		return failResult(asl.ErrResultWriterFailed, "ResultWriter must be arn:aws:states:::s3:putObject with Bucket and Prefix parameters")
	}
	id := arn[strings.LastIndex(arn, ":")+1:]
	prefix := strings.TrimSuffix(w.Parameters.Prefix, "/") + "/" + id + "/"
	var ok, bad []map[string]any
	for _, it := range items {
		row := map[string]any{"Input": string(it.Input), "Status": it.Status}
		if it.Status == "SUCCEEDED" {
			row["Output"] = string(it.Output)
			ok = append(ok, row)
		} else {
			row["Error"], row["Cause"] = it.Error, it.Cause
			bad = append(bad, row)
		}
	}
	files := map[string]any{}
	if len(ok) > 0 {
		files["SUCCEEDED_0.json"] = ok
	}
	if len(bad) > 0 {
		files["FAILED_0.json"] = bad
	}
	manifest := map[string]any{"DestinationBucket": w.Parameters.Bucket, "MapRunArn": arn, "ResultFiles": map[string]any{}}
	rf := manifest["ResultFiles"].(map[string]any)
	for name, content := range files {
		raw, _ := json.Marshal(content)
		if res := s.putS3(ctx, w.Parameters.Bucket, prefix+name, raw); res.Failure != nil {
			return failResult(asl.ErrResultWriterFailed, res.Failure.Cause)
		}
		kind := strings.TrimSuffix(name, "_0.json")
		rf[kind] = []map[string]any{{"Key": prefix + name, "Size": len(raw)}}
	}
	raw, _ := json.Marshal(manifest)
	if res := s.putS3(ctx, w.Parameters.Bucket, prefix+"manifest.json", raw); res.Failure != nil {
		return failResult(asl.ErrResultWriterFailed, res.Failure.Cause)
	}
	out, _ := json.Marshal(map[string]any{
		"MapRunArn":           arn,
		"ResultWriterDetails": map[string]any{"Bucket": w.Parameters.Bucket, "Key": prefix + "manifest.json"},
	})
	return asl.TaskResult{Output: out}
}

func (s *Server) putS3(ctx context.Context, bucket, key string, body []byte) asl.TaskResult {
	in, _ := json.Marshal(map[string]any{"Bucket": bucket, "Key": key, "Body": string(body), "ContentType": "application/json"})
	return s.callS3(ctx, "putObject", in)
}
