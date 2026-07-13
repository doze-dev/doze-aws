package dynamodb

// DynamoDB Streams API (DynamoDBStreams_20120810): ListStreams, DescribeStream,
// GetShardIterator, GetRecords. Each table with streaming enabled exposes a
// single open shard; records are read from the store's per-table change log. A
// shard iterator is an opaque "<table>|<afterSeq>" cursor.

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/ddb/store"
)

// The single shard every doze-aws stream exposes.
const streamShardID = "shardId-00000000000000000000-00000000"

var streamHandlers = map[string]func(*Server, []byte) (any, *awshttp.APIError){
	"ListStreams":      (*Server).listStreams,
	"DescribeStream":   (*Server).describeStream,
	"GetShardIterator": (*Server).getShardIterator,
	"GetRecords":       (*Server).getRecords,
}

// tableFromStreamARN extracts the table name from arn:...:table/<name>/stream/<label>.
func tableFromStreamARN(arn string) string {
	i := strings.Index(arn, "table/")
	if i < 0 {
		return ""
	}
	rest := arn[i+len("table/"):]
	if j := strings.Index(rest, "/stream/"); j >= 0 {
		return rest[:j]
	}
	return rest
}

func (s *Server) listStreams(body []byte) (any, *awshttp.APIError) {
	var req struct {
		TableName string `json:"TableName"`
	}
	json.Unmarshal(body, &req)

	var streams []map[string]any
	add := func(name string) {
		t, err := s.store.GetTable(name)
		if err != nil {
			return
		}
		if _, ok := t.StreamViewType(); !ok {
			return
		}
		streams = append(streams, map[string]any{
			"StreamArn": t.StreamARN(), "TableName": t.Name, "StreamLabel": t.StreamLabel(),
		})
	}
	if req.TableName != "" {
		add(req.TableName)
	} else {
		names, _ := s.store.ListTables()
		for _, n := range names {
			add(n)
		}
	}
	return map[string]any{"Streams": streams}, nil
}

func (s *Server) describeStream(body []byte) (any, *awshttp.APIError) {
	var req struct {
		StreamArn string `json:"StreamArn"`
	}
	json.Unmarshal(body, &req)
	table := tableFromStreamARN(req.StreamArn)
	t, err := s.store.GetTable(table)
	if err != nil {
		return nil, awshttp.Errf(400, "ResourceNotFoundException", "stream %s not found", req.StreamArn)
	}
	viewType, ok := t.StreamViewType()
	if !ok {
		return nil, awshttp.Errf(400, "ResourceNotFoundException", "table %s has no stream", table)
	}
	keySchema := []map[string]any{{"AttributeName": t.Hash.Name, "KeyType": "HASH"}}
	if t.Range != nil {
		keySchema = append(keySchema, map[string]any{"AttributeName": t.Range.Name, "KeyType": "RANGE"})
	}
	return map[string]any{
		"StreamDescription": map[string]any{
			"StreamArn":               t.StreamARN(),
			"StreamLabel":             t.StreamLabel(),
			"StreamStatus":            "ENABLED",
			"StreamViewType":          viewType,
			"TableName":               t.Name,
			"KeySchema":               keySchema,
			"CreationRequestDateTime": float64(t.Created),
			"Shards": []map[string]any{{
				"ShardId": streamShardID,
				// An open shard: a starting sequence, no ending one.
				"SequenceNumberRange": map[string]any{"StartingSequenceNumber": "0"},
			}},
		},
	}, nil
}

func (s *Server) getShardIterator(body []byte) (any, *awshttp.APIError) {
	var req struct {
		StreamArn         string `json:"StreamArn"`
		ShardId           string `json:"ShardId"`
		ShardIteratorType string `json:"ShardIteratorType"`
		SequenceNumber    string `json:"SequenceNumber"`
	}
	json.Unmarshal(body, &req)
	table := tableFromStreamARN(req.StreamArn)
	if _, ok := s.store.StreamViewType(table); !ok {
		return nil, awshttp.Errf(400, "ResourceNotFoundException", "stream %s not found", req.StreamArn)
	}
	var after uint64
	switch req.ShardIteratorType {
	case "TRIM_HORIZON":
		after = 0
	case "LATEST":
		after = s.store.LatestStreamSeq(table)
	case "AT_SEQUENCE_NUMBER":
		n, _ := strconv.ParseUint(req.SequenceNumber, 10, 64)
		if n > 0 {
			after = n - 1 // include the named record
		}
	case "AFTER_SEQUENCE_NUMBER":
		after, _ = strconv.ParseUint(req.SequenceNumber, 10, 64)
	default:
		return nil, awshttp.Errf(400, "ValidationException", "invalid ShardIteratorType %q", req.ShardIteratorType)
	}
	return map[string]any{"ShardIterator": encodeIterator(table, after)}, nil
}

func (s *Server) getRecords(body []byte) (any, *awshttp.APIError) {
	var req struct {
		ShardIterator string `json:"ShardIterator"`
		Limit         int    `json:"Limit"`
	}
	json.Unmarshal(body, &req)
	table, after, ok := decodeIterator(req.ShardIterator)
	if !ok {
		return nil, awshttp.Errf(400, "ValidationException", "invalid ShardIterator")
	}
	viewType, enabled := s.store.StreamViewType(table)
	if !enabled {
		return nil, awshttp.Errf(400, "ResourceNotFoundException", "stream for %s not found", table)
	}
	recs, _, err := s.store.StreamRecords(table, after, req.Limit)
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	next := after
	out := make([]map[string]any, 0, len(recs))
	for _, rec := range recs {
		out = append(out, streamRecordWire(rec, viewType))
		next = rec.Seq
	}
	return map[string]any{
		"Records": out,
		// A single open shard always returns a continuation iterator.
		"NextShardIterator": encodeIterator(table, next),
	}, nil
}

// streamRecordWire shapes one stored record into the Streams wire format,
// applying the stream view type.
func streamRecordWire(rec store.StreamRecord, viewType string) map[string]any {
	d := map[string]any{
		"Keys":                        json.RawMessage(rec.Keys),
		"SequenceNumber":              strconv.FormatUint(rec.Seq, 10),
		"SizeBytes":                   rec.SizeBytes,
		"StreamViewType":              viewType,
		"ApproximateCreationDateTime": float64(rec.CreatedNs) / 1e9,
	}
	switch viewType {
	case "NEW_IMAGE":
		if rec.New != nil {
			d["NewImage"] = json.RawMessage(rec.New)
		}
	case "OLD_IMAGE":
		if rec.Old != nil {
			d["OldImage"] = json.RawMessage(rec.Old)
		}
	case "NEW_AND_OLD_IMAGES":
		if rec.New != nil {
			d["NewImage"] = json.RawMessage(rec.New)
		}
		if rec.Old != nil {
			d["OldImage"] = json.RawMessage(rec.Old)
		}
	}
	return map[string]any{
		"eventID":      strconv.FormatUint(rec.Seq, 10),
		"eventName":    rec.EventName,
		"eventVersion": "1.1",
		"eventSource":  "aws:dynamodb",
		"awsRegion":    awsident.Region,
		"dynamodb":     d,
	}
}

func encodeIterator(table string, after uint64) string {
	return base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf("%s|%d", table, after)))
}

func decodeIterator(tok string) (table string, after uint64, ok bool) {
	raw, err := base64.RawURLEncoding.DecodeString(tok)
	if err != nil {
		return "", 0, false
	}
	tbl, seq, found := strings.Cut(string(raw), "|")
	if !found {
		return "", 0, false
	}
	n, err := strconv.ParseUint(seq, 10, 64)
	if err != nil {
		return "", 0, false
	}
	return tbl, n, true
}
