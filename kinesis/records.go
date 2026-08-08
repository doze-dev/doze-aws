package kinesis

// The data plane: PutRecord(s), GetShardIterator, GetRecords and ListShards.

import (
	"time"

	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/awsjson"
)

// resolveStream reads the stream name from a request, accepting either
// StreamName or StreamARN. Every Kinesis operation takes one or the other, and
// modern SDKs increasingly send the ARN.
func resolveStream(p map[string]any) (string, *apiError) {
	if name := awsjson.Str(p, "StreamName"); name != "" {
		return name, nil
	}
	if arn := awsjson.Str(p, "StreamARN"); arn != "" {
		name, err := streamFromARN(arn)
		if err != nil {
			return "", err.(*apiError)
		}
		return name, nil
	}
	return "", errValidation("either StreamName or StreamARN must be supplied")
}

func hPutRecord(s *Server, p map[string]any) (any, *awshttp.APIError) {
	stream, aerr := resolveStream(p)
	if aerr != nil {
		return nil, aerr
	}
	// A stream that writes under a customer key must not accept records it
	// could not have encrypted.
	if aerr := s.requireUsableKey(s.keyForStream(stream)); aerr != nil {
		return nil, aerr
	}
	data, aerr := awsjson.Blob(p, "Data")
	if aerr != nil {
		return nil, aerr
	}
	entry := PutEntry{
		PartitionKey:    awsjson.Str(p, "PartitionKey"),
		ExplicitHashKey: awsjson.Str(p, "ExplicitHashKey"),
		Data:            data,
	}
	res, err := s.store.Put(stream, []PutEntry{entry})
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	return map[string]any{
		"ShardId":        res[0].ShardID,
		"SequenceNumber": formatSeq(res[0].Seq),
		"EncryptionType": encryptionOf(s, stream),
	}, nil
}

func hPutRecords(s *Server, p map[string]any) (any, *awshttp.APIError) {
	stream, aerr := resolveStream(p)
	if aerr != nil {
		return nil, aerr
	}
	raw, ok := p["Records"].([]any)
	if !ok || len(raw) == 0 {
		return nil, errValidation("Records must contain at least one record")
	}
	// A stream that writes under a customer key must not accept records it
	// could not have encrypted.
	if aerr := s.requireUsableKey(s.keyForStream(stream)); aerr != nil {
		return nil, aerr
	}
	entries := make([]PutEntry, 0, len(raw))
	for i, item := range raw {
		rec, ok := item.(map[string]any)
		if !ok {
			return nil, errValidation("Records[%d] is not an object", i)
		}
		data, aerr := awsjson.Blob(rec, "Data")
		if aerr != nil {
			return nil, aerr
		}
		entries = append(entries, PutEntry{
			PartitionKey:    awsjson.Str(rec, "PartitionKey"),
			ExplicitHashKey: awsjson.Str(rec, "ExplicitHashKey"),
			Data:            data,
		})
	}
	res, err := s.store.Put(stream, entries)
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	out := make([]map[string]any, len(res))
	for i, r := range res {
		out[i] = map[string]any{"ShardId": r.ShardID, "SequenceNumber": formatSeq(r.Seq)}
	}
	// The batch is written in one transaction, so it either all lands or the
	// whole call fails — there is no local throttling to cause a partial write.
	return map[string]any{
		"FailedRecordCount": 0,
		"Records":           out,
		"EncryptionType":    encryptionOf(s, stream),
	}, nil
}

func hGetShardIterator(s *Server, p map[string]any) (any, *awshttp.APIError) {
	stream, aerr := resolveStream(p)
	if aerr != nil {
		return nil, aerr
	}
	shard := awsjson.Str(p, "ShardId")
	if shard == "" {
		return nil, errValidation("ShardId is required")
	}
	st, err := s.store.Get(stream)
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	sh, ok := st.shard(shard)
	if !ok {
		return nil, errNoShard(shard, stream)
	}

	var after uint64
	switch typ := awsjson.Str(p, "ShardIteratorType"); typ {
	case "TRIM_HORIZON":
		// Start below the oldest surviving record, not at zero: retention may
		// already have reclaimed the front of the shard.
		if after, err = s.store.TrimHorizonSeq(stream, shard); err != nil {
			return nil, awshttp.AsAPIError(err)
		}
		if sh.StartSeq > 0 && after < sh.StartSeq-1 {
			after = sh.StartSeq - 1
		}
	case "LATEST":
		if after, err = s.store.LatestSeq(stream, shard); err != nil {
			return nil, awshttp.AsAPIError(err)
		}
	case "AT_SEQUENCE_NUMBER", "AFTER_SEQUENCE_NUMBER":
		seq, ok := parseSeq(awsjson.Str(p, "StartingSequenceNumber"))
		if !ok {
			return nil, errInvalid("StartingSequenceNumber is required for %s", typ)
		}
		after = seq
		if typ == "AT_SEQUENCE_NUMBER" && seq > 0 {
			after = seq - 1 // the named record must itself be delivered
		}
	case "AT_TIMESTAMP":
		ts, aerr := timestampOf(p, "Timestamp")
		if aerr != nil {
			return nil, aerr
		}
		if after, err = s.store.SeqAtOrAfter(stream, shard, ts); err != nil {
			return nil, awshttp.AsAPIError(err)
		}
	case "":
		return nil, errValidation("ShardIteratorType is required")
	default:
		return nil, errValidation("invalid ShardIteratorType %q", typ)
	}

	return map[string]any{"ShardIterator": encodeIterator(cursor{
		Stream: stream, Shard: shard, After: after, IssuedNs: s.now().UnixNano(),
	})}, nil
}

func hGetRecords(s *Server, p map[string]any) (any, *awshttp.APIError) {
	tok := awsjson.Str(p, "ShardIterator")
	if tok == "" {
		return nil, errValidation("ShardIterator is required")
	}
	cur, aerr := decodeIterator(tok, s.now())
	if aerr != nil {
		return nil, aerr
	}
	// Reading an encrypted stream decrypts, so an unusable key fails the read
	// the same way it fails a write.
	if aerr := s.requireUsableKey(s.keyForStream(cur.Stream)); aerr != nil {
		return nil, aerr
	}
	limit := awsjson.Int(p, "Limit", 0)
	recs, next, behind, err := s.store.Fetch(cur.Stream, cur.Shard, cur.After, limit)
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}

	st, err := s.store.Get(cur.Stream)
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	sh, ok := st.shard(cur.Shard)
	if !ok {
		return nil, errNoShard(cur.Shard, cur.Stream)
	}

	out := make([]map[string]any, 0, len(recs))
	enc := encryptionOf(s, cur.Stream)
	for _, rec := range recs {
		out = append(out, map[string]any{
			"SequenceNumber":              formatSeq(rec.Seq),
			"ApproximateArrivalTimestamp": float64(rec.ArrivedNs) / 1e9,
			"Data":                        rec.Data,
			"PartitionKey":                rec.PartitionKey,
			"EncryptionType":              enc,
		})
	}

	result := map[string]any{
		"Records":            out,
		"MillisBehindLatest": behind.Milliseconds(),
	}
	// A closed shard that has been drained returns a null iterator and names
	// its children — that null is how a consumer learns the shard has ended
	// and it should move on, so it must not be papered over.
	drained := sh.Closed && (sh.EndSeq == 0 || next >= sh.EndSeq)
	if drained {
		result["NextShardIterator"] = nil
		if kids := childShards(st, sh.ID); len(kids) > 0 {
			result["ChildShards"] = kids
		}
	} else {
		result["NextShardIterator"] = encodeIterator(cursor{
			Stream: cur.Stream, Shard: cur.Shard, After: next, IssuedNs: s.now().UnixNano(),
		})
	}
	return result, nil
}

// childShards lists the shards that name parent as a parent, in the shape
// GetRecords advertises them.
func childShards(st *Stream, parent string) []map[string]any {
	var out []map[string]any
	for _, sh := range st.Shards {
		if sh.ParentID != parent && sh.AdjacentID != parent {
			continue
		}
		parents := []string{sh.ParentID}
		if sh.AdjacentID != "" {
			parents = append(parents, sh.AdjacentID)
		}
		out = append(out, map[string]any{
			"ShardId":      sh.ID,
			"ParentShards": parents,
			"HashKeyRange": hashRange(sh),
		})
	}
	return out
}

func hListShards(s *Server, p map[string]any) (any, *awshttp.APIError) {
	stream, aerr := resolveStream(p)
	if aerr != nil {
		return nil, aerr
	}
	st, err := s.store.Get(stream)
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	start := awsjson.Str(p, "ExclusiveStartShardId")
	var out []map[string]any
	for _, sh := range st.Shards {
		if start != "" && sh.ID <= start {
			continue
		}
		out = append(out, shardWire(sh))
	}
	if out == nil {
		out = []map[string]any{}
	}
	return map[string]any{"Shards": out}, nil
}

// shardWire shapes a shard for DescribeStream and ListShards.
func shardWire(sh Shard) map[string]any {
	seqRange := map[string]any{"StartingSequenceNumber": formatSeq(sh.StartSeq)}
	if sh.Closed && sh.EndSeq != 0 {
		seqRange["EndingSequenceNumber"] = formatSeq(sh.EndSeq)
	}
	m := map[string]any{
		"ShardId":             sh.ID,
		"HashKeyRange":        hashRange(sh),
		"SequenceNumberRange": seqRange,
	}
	if sh.ParentID != "" {
		m["ParentShardId"] = sh.ParentID
	}
	if sh.AdjacentID != "" {
		m["AdjacentParentShardId"] = sh.AdjacentID
	}
	return m
}

func hashRange(sh Shard) map[string]any {
	return map[string]any{"StartingHashKey": sh.StartHash, "EndingHashKey": sh.EndHash}
}

// encryptionOf reports the stream's encryption type for response echoing.
func encryptionOf(s *Server, stream string) string {
	st, err := s.store.Get(stream)
	if err != nil || st.EncryptionType == "" {
		return "NONE"
	}
	return st.EncryptionType
}

// timestampOf reads an AWS JSON timestamp, which travels as epoch seconds
// (possibly fractional) but which some SDKs send as a numeric string.
func timestampOf(p map[string]any, key string) (time.Time, *apiError) {
	switch v := p[key].(type) {
	case float64:
		return time.Unix(0, int64(v*1e9)), nil
	case string:
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			return t, nil
		}
	}
	return time.Time{}, errValidation("%s is required and must be a timestamp", key)
}
