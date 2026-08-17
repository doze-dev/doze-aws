package kinesis

// The control plane: stream lifecycle, retention, tags, encryption,
// consumers and account settings — plus the dispatch table the server routes
// X-Amz-Target through.

import (
	"context"
	"sort"

	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/awsjson"
	"github.com/doze-dev/doze-aws/internal/peercall"
)

var handlers = map[string]handler{
	// Stream lifecycle.
	"CreateStream":          hCreateStream,
	"DeleteStream":          hDeleteStream,
	"ListStreams":           hListStreams,
	"DescribeStream":        hDescribeStream,
	"DescribeStreamSummary": hDescribeStreamSummary,
	"UpdateStreamMode":      hUpdateStreamMode,
	"UpdateMaxRecordSize":   hUpdateMaxRecordSize,

	// Data plane (records.go).
	"PutRecord":        hPutRecord,
	"PutRecords":       hPutRecords,
	"GetRecords":       hGetRecords,
	"GetShardIterator": hGetShardIterator,
	"ListShards":       hListShards,

	// Resharding (reshard.go).
	"SplitShard":       hSplitShard,
	"MergeShards":      hMergeShards,
	"UpdateShardCount": hUpdateShardCount,

	// Retention.
	"IncreaseStreamRetentionPeriod": hIncreaseRetention,
	"DecreaseStreamRetentionPeriod": hDecreaseRetention,

	// Tags.
	"AddTagsToStream":      hAddTags,
	"RemoveTagsFromStream": hRemoveTags,
	"ListTagsForStream":    hListTagsForStream,
	"TagResource":          hTagResource,
	"UntagResource":        hUntagResource,
	"ListTagsForResource":  hListTagsForResource,

	// Consumers.
	"RegisterStreamConsumer":   hRegisterConsumer,
	"DeregisterStreamConsumer": hDeregisterConsumer,
	"DescribeStreamConsumer":   hDescribeConsumer,
	"ListStreamConsumers":      hListConsumers,

	// Configuration round-trips: accepted and echoed, no local effect.
	"StartStreamEncryption":     hStartEncryption,
	"StopStreamEncryption":      hStopEncryption,
	"EnableEnhancedMonitoring":  hEnableMonitoring,
	"DisableEnhancedMonitoring": hDisableMonitoring,
	"DescribeLimits":            hDescribeLimits,
	"DescribeAccountSettings":   hDescribeAccountSettings,
	"UpdateAccountSettings":     hUpdateAccountSettings,
	"PutResourcePolicy":         hPutResourcePolicy,
	"GetResourcePolicy":         hGetResourcePolicy,
	"DeleteResourcePolicy":      hDeleteResourcePolicy,
}

// ---- stream lifecycle ----

func hCreateStream(s *Server, p map[string]any) (any, *awshttp.APIError) {
	mode := ""
	if d, ok := p["StreamModeDetails"].(map[string]any); ok {
		mode = awsjson.Str(d, "StreamMode")
	}
	_, err := s.store.Create(awsjson.Str(p, "StreamName"), awsjson.Int(p, "ShardCount", 1), mode)
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	return nil, nil
}

func hDeleteStream(s *Server, p map[string]any) (any, *awshttp.APIError) {
	stream, aerr := resolveStream(p)
	if aerr != nil {
		return nil, aerr
	}
	if err := s.store.Delete(stream); err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	return nil, nil
}

func hListStreams(s *Server, p map[string]any) (any, *awshttp.APIError) {
	names, more, err := s.store.List(awsjson.Str(p, "ExclusiveStartStreamName"), awsjson.Int(p, "Limit", 0))
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	summaries := make([]map[string]any, 0, len(names))
	for _, n := range names {
		st, err := s.store.Get(n)
		if err != nil {
			continue
		}
		summaries = append(summaries, map[string]any{
			"StreamName":              st.Name,
			"StreamARN":               streamARN(st.Name),
			"StreamStatus":            "ACTIVE",
			"StreamModeDetails":       map[string]any{"StreamMode": st.Mode},
			"StreamCreationTimestamp": float64(st.Created),
		})
	}
	if names == nil {
		names = []string{}
	}
	return map[string]any{
		"StreamNames":     names,
		"HasMoreStreams":  more,
		"StreamSummaries": summaries,
	}, nil
}

func hDescribeStream(s *Server, p map[string]any) (any, *awshttp.APIError) {
	stream, aerr := resolveStream(p)
	if aerr != nil {
		return nil, aerr
	}
	st, err := s.store.Get(stream)
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	start := awsjson.Str(p, "ExclusiveStartShardId")
	shards := make([]map[string]any, 0, len(st.Shards))
	for _, sh := range st.Shards {
		if start != "" && sh.ID <= start {
			continue
		}
		shards = append(shards, shardWire(sh))
	}
	d := map[string]any{
		"StreamName":              st.Name,
		"StreamARN":               streamARN(st.Name),
		"StreamStatus":            "ACTIVE",
		"StreamModeDetails":       map[string]any{"StreamMode": st.Mode},
		"Shards":                  shards,
		"HasMoreShards":           false,
		"RetentionPeriodHours":    st.RetentionHours,
		"StreamCreationTimestamp": float64(st.Created),
		"EnhancedMonitoring":      enhancedWire(st),
		"EncryptionType":          encTypeOf(st),
	}
	if st.KeyID != "" {
		d["KeyId"] = st.KeyID
	}
	return map[string]any{"StreamDescription": d}, nil
}

func hDescribeStreamSummary(s *Server, p map[string]any) (any, *awshttp.APIError) {
	stream, aerr := resolveStream(p)
	if aerr != nil {
		return nil, aerr
	}
	st, err := s.store.Get(stream)
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	consumers, _ := s.store.ListConsumers(streamARN(st.Name))
	d := map[string]any{
		"StreamName":              st.Name,
		"StreamARN":               streamARN(st.Name),
		"StreamStatus":            "ACTIVE",
		"StreamModeDetails":       map[string]any{"StreamMode": st.Mode},
		"RetentionPeriodHours":    st.RetentionHours,
		"StreamCreationTimestamp": float64(st.Created),
		"EnhancedMonitoring":      enhancedWire(st),
		"EncryptionType":          encTypeOf(st),
		"OpenShardCount":          len(st.OpenShards()),
		"ConsumerCount":           len(consumers),
	}
	if st.KeyID != "" {
		d["KeyId"] = st.KeyID
	}
	return map[string]any{"StreamDescriptionSummary": d}, nil
}

func hUpdateStreamMode(s *Server, p map[string]any) (any, *awshttp.APIError) {
	arn := awsjson.Str(p, "StreamARN")
	stream, err := streamFromARN(arn)
	if err != nil {
		return nil, err.(*apiError)
	}
	mode := ""
	if d, ok := p["StreamModeDetails"].(map[string]any); ok {
		mode = awsjson.Str(d, "StreamMode")
	}
	if mode != modeProvisioned && mode != modeOnDemand {
		return nil, errValidation("StreamModeDetails.StreamMode must be %s or %s", modeProvisioned, modeOnDemand)
	}
	if _, err := s.store.Update(stream, func(st *Stream) error {
		st.Mode = mode
		return nil
	}); err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	return nil, nil
}

// hUpdateMaxRecordSize round-trips the setting; the 1 MiB ceiling enforced on
// the put path is the AWS default and is not raised locally.
func hUpdateMaxRecordSize(s *Server, p map[string]any) (any, *awshttp.APIError) {
	if _, aerr := resolveStream(p); aerr != nil {
		return nil, aerr
	}
	return nil, nil
}

// ---- retention ----

func setRetention(s *Server, p map[string]any, increase bool) (any, *awshttp.APIError) {
	stream, aerr := resolveStream(p)
	if aerr != nil {
		return nil, aerr
	}
	hours := awsjson.Int(p, "RetentionPeriodHours", 0)
	if hours < minRetentionHours || hours > maxRetentionHours {
		return nil, errInvalid("RetentionPeriodHours must be between %d and %d", minRetentionHours, maxRetentionHours)
	}
	_, err := s.store.Update(stream, func(st *Stream) error {
		// AWS rejects an "increase" that shortens the window and vice versa —
		// the asymmetry is the whole point of having two operations.
		if increase && hours < st.RetentionHours {
			return errInvalid("requested retention %dh is shorter than the current %dh", hours, st.RetentionHours)
		}
		if !increase && hours > st.RetentionHours {
			return errInvalid("requested retention %dh is longer than the current %dh", hours, st.RetentionHours)
		}
		st.RetentionHours = hours
		return nil
	})
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	return nil, nil
}

func hIncreaseRetention(s *Server, p map[string]any) (any, *awshttp.APIError) {
	return setRetention(s, p, true)
}

func hDecreaseRetention(s *Server, p map[string]any) (any, *awshttp.APIError) {
	return setRetention(s, p, false)
}

// ---- tags ----

func addTags(s *Server, stream string, tags map[string]string) *awshttp.APIError {
	_, err := s.store.Update(stream, func(st *Stream) error {
		if st.Tags == nil {
			st.Tags = map[string]string{}
		}
		for k, v := range tags {
			st.Tags[k] = v
		}
		return nil
	})
	return awshttp.AsAPIErrorOrNil(err)
}

func removeTags(s *Server, stream string, keys []string) *awshttp.APIError {
	_, err := s.store.Update(stream, func(st *Stream) error {
		for _, k := range keys {
			delete(st.Tags, k)
		}
		return nil
	})
	return awshttp.AsAPIErrorOrNil(err)
}

func hAddTags(s *Server, p map[string]any) (any, *awshttp.APIError) {
	stream, aerr := resolveStream(p)
	if aerr != nil {
		return nil, aerr
	}
	return nil, addTags(s, stream, awsjson.StrMap(p, "Tags"))
}

func hRemoveTags(s *Server, p map[string]any) (any, *awshttp.APIError) {
	stream, aerr := resolveStream(p)
	if aerr != nil {
		return nil, aerr
	}
	return nil, removeTags(s, stream, awsjson.Strs(p, "TagKeys"))
}

// hTagResource is the newer tag API; it addresses the stream by ARN and takes
// tags as a list of {Key, Value} pairs rather than a map.
func hTagResource(s *Server, p map[string]any) (any, *awshttp.APIError) {
	stream, err := streamFromARN(awsjson.Str(p, "ResourceARN"))
	if err != nil {
		return nil, err.(*apiError)
	}
	tags := map[string]string{}
	if list, ok := p["Tags"].([]any); ok {
		for _, item := range list {
			if t, ok := item.(map[string]any); ok {
				tags[awsjson.Str(t, "Key")] = awsjson.Str(t, "Value")
			}
		}
	}
	for k, v := range awsjson.StrMap(p, "Tags") {
		tags[k] = v
	}
	return nil, addTags(s, stream, tags)
}

func hUntagResource(s *Server, p map[string]any) (any, *awshttp.APIError) {
	stream, err := streamFromARN(awsjson.Str(p, "ResourceARN"))
	if err != nil {
		return nil, err.(*apiError)
	}
	return nil, removeTags(s, stream, awsjson.Strs(p, "TagKeys"))
}

func hListTagsForStream(s *Server, p map[string]any) (any, *awshttp.APIError) {
	stream, aerr := resolveStream(p)
	if aerr != nil {
		return nil, aerr
	}
	st, err := s.store.Get(stream)
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	return map[string]any{"Tags": tagList(st.Tags), "HasMoreTags": false}, nil
}

func hListTagsForResource(s *Server, p map[string]any) (any, *awshttp.APIError) {
	stream, err := streamFromARN(awsjson.Str(p, "ResourceARN"))
	if err != nil {
		return nil, err.(*apiError)
	}
	st, gerr := s.store.Get(stream)
	if gerr != nil {
		return nil, awshttp.AsAPIError(gerr)
	}
	return map[string]any{"Tags": tagList(st.Tags)}, nil
}

func tagList(tags map[string]string) []map[string]any {
	keys := make([]string, 0, len(tags))
	for k := range tags {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]map[string]any, 0, len(keys))
	for _, k := range keys {
		out = append(out, map[string]any{"Key": k, "Value": tags[k]})
	}
	return out
}

// ---- consumers ----

func hRegisterConsumer(s *Server, p map[string]any) (any, *awshttp.APIError) {
	arn := awsjson.Str(p, "StreamARN")
	if arn == "" {
		return nil, errValidation("StreamARN is required")
	}
	c, err := s.store.RegisterConsumer(arn, awsjson.Str(p, "ConsumerName"))
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	return map[string]any{"Consumer": consumerWire(c)}, nil
}

func hDeregisterConsumer(s *Server, p map[string]any) (any, *awshttp.APIError) {
	c, err := s.store.FindConsumer(awsjson.Str(p, "StreamARN"), awsjson.Str(p, "ConsumerName"), awsjson.Str(p, "ConsumerARN"))
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	if err := s.store.DeleteConsumer(c); err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	return nil, nil
}

func hDescribeConsumer(s *Server, p map[string]any) (any, *awshttp.APIError) {
	c, err := s.store.FindConsumer(awsjson.Str(p, "StreamARN"), awsjson.Str(p, "ConsumerName"), awsjson.Str(p, "ConsumerARN"))
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	d := consumerWire(c)
	d["StreamARN"] = c.StreamARN
	return map[string]any{"ConsumerDescription": d}, nil
}

func hListConsumers(s *Server, p map[string]any) (any, *awshttp.APIError) {
	arn := awsjson.Str(p, "StreamARN")
	if arn == "" {
		return nil, errValidation("StreamARN is required")
	}
	consumers, err := s.store.ListConsumers(arn)
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	out := make([]map[string]any, 0, len(consumers))
	for i := range consumers {
		out = append(out, consumerWire(&consumers[i]))
	}
	return map[string]any{"Consumers": out}, nil
}

func consumerWire(c *Consumer) map[string]any {
	return map[string]any{
		"ConsumerName":              c.Name,
		"ConsumerARN":               c.ARN,
		"ConsumerStatus":            c.Status,
		"ConsumerCreationTimestamp": float64(c.Created),
	}
}

// ---- configuration round-trips ----
//
// These change stream metadata that has no local effect: there is no KMS
// envelope over the bbolt file, no CloudWatch to publish shard metrics to, and
// no IAM evaluation until the IAM service lands. They are stored and echoed
// faithfully rather than refused, because SDK code paths that set them should
// keep working.

func encTypeOf(st *Stream) string {
	if st.EncryptionType == "" {
		return "NONE"
	}
	return st.EncryptionType
}

func enhancedWire(st *Stream) []map[string]any {
	metrics := st.EnhancedMetrics
	if metrics == nil {
		metrics = []string{}
	}
	return []map[string]any{{"ShardLevelMetrics": metrics}}
}

func hStartEncryption(s *Server, p map[string]any) (any, *awshttp.APIError) {
	stream, aerr := resolveStream(p)
	if aerr != nil {
		return nil, aerr
	}
	keyID := awsjson.Str(p, "KeyId")
	if keyID == "" {
		return nil, errValidation("KeyId is required")
	}
	// Turning encryption on against a key that cannot be used is the failure
	// worth catching early: AWS refuses it here rather than at the first put.
	if aerr := s.requireUsableKey(keyID); aerr != nil {
		return nil, aerr
	}
	_, err := s.store.Update(stream, func(st *Stream) error {
		st.EncryptionType, st.KeyID = "KMS", keyID
		return nil
	})
	return nil, awshttp.AsAPIErrorOrNil(err)
}

func hStopEncryption(s *Server, p map[string]any) (any, *awshttp.APIError) {
	stream, aerr := resolveStream(p)
	if aerr != nil {
		return nil, aerr
	}
	_, err := s.store.Update(stream, func(st *Stream) error {
		st.EncryptionType, st.KeyID = "NONE", ""
		return nil
	})
	return nil, awshttp.AsAPIErrorOrNil(err)
}

func setMonitoring(s *Server, p map[string]any, enable bool) (any, *awshttp.APIError) {
	stream, aerr := resolveStream(p)
	if aerr != nil {
		return nil, aerr
	}
	req := awsjson.Strs(p, "ShardLevelMetrics")
	var before, after []string
	_, err := s.store.Update(stream, func(st *Stream) error {
		before = append([]string{}, st.EnhancedMetrics...)
		set := map[string]bool{}
		for _, m := range st.EnhancedMetrics {
			set[m] = true
		}
		for _, m := range req {
			if m == "ALL" {
				set = map[string]bool{"ALL": enable}
				continue
			}
			set[m] = enable
		}
		st.EnhancedMetrics = nil
		for m, on := range set {
			if on {
				st.EnhancedMetrics = append(st.EnhancedMetrics, m)
			}
		}
		sort.Strings(st.EnhancedMetrics)
		after = st.EnhancedMetrics
		return nil
	})
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	if after == nil {
		after = []string{}
	}
	return map[string]any{
		"StreamName":               stream,
		"StreamARN":                streamARN(stream),
		"CurrentShardLevelMetrics": before,
		"DesiredShardLevelMetrics": after,
	}, nil
}

func hEnableMonitoring(s *Server, p map[string]any) (any, *awshttp.APIError) {
	return setMonitoring(s, p, true)
}

func hDisableMonitoring(s *Server, p map[string]any) (any, *awshttp.APIError) {
	return setMonitoring(s, p, false)
}

func hDescribeLimits(s *Server, _ map[string]any) (any, *awshttp.APIError) {
	names, _, err := s.store.List("", 0)
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	open := 0
	for _, n := range names {
		if st, err := s.store.Get(n); err == nil {
			open += len(st.OpenShards())
		}
	}
	// There is no account quota locally; the shard limit is reported as a large
	// constant so SDK code that reads it has a sane number to work with.
	return map[string]any{
		"ShardLimit":               10000,
		"OpenShardCount":           open,
		"OnDemandStreamCount":      0,
		"OnDemandStreamCountLimit": 50,
	}, nil
}

func hDescribeAccountSettings(_ *Server, _ map[string]any) (any, *awshttp.APIError) {
	return map[string]any{
		"MaxRecordSize":            maxRecordSize,
		"TotalStreamCount":         0,
		"DefaultStreamModeDetails": map[string]any{"StreamMode": modeProvisioned},
	}, nil
}

func hUpdateAccountSettings(_ *Server, _ map[string]any) (any, *awshttp.APIError) {
	return map[string]any{}, nil
}

func hPutResourcePolicy(s *Server, p map[string]any) (any, *awshttp.APIError) {
	stream, err := streamFromARN(awsjson.Str(p, "ResourceARN"))
	if err != nil {
		return nil, err.(*apiError)
	}
	policy := awsjson.Str(p, "Policy")
	if policy == "" {
		return nil, errValidation("Policy is required")
	}
	_, uerr := s.store.Update(stream, func(st *Stream) error {
		st.ResourcePolicy = policy
		return nil
	})
	return nil, awshttp.AsAPIErrorOrNil(uerr)
}

func hGetResourcePolicy(s *Server, p map[string]any) (any, *awshttp.APIError) {
	stream, err := streamFromARN(awsjson.Str(p, "ResourceARN"))
	if err != nil {
		return nil, err.(*apiError)
	}
	st, gerr := s.store.Get(stream)
	if gerr != nil {
		return nil, awshttp.AsAPIError(gerr)
	}
	if st.ResourcePolicy == "" {
		return nil, errNoStream(stream)
	}
	return map[string]any{"Policy": st.ResourcePolicy}, nil
}

func hDeleteResourcePolicy(s *Server, p map[string]any) (any, *awshttp.APIError) {
	stream, err := streamFromARN(awsjson.Str(p, "ResourceARN"))
	if err != nil {
		return nil, err.(*apiError)
	}
	_, uerr := s.store.Update(stream, func(st *Stream) error {
		st.ResourcePolicy = ""
		return nil
	})
	return nil, awshttp.AsAPIErrorOrNil(uerr)
}

// requireUsableKey checks the customer key behind an encrypted stream. AWS
// fails the write rather than storing records it could not encrypt, and the
// distinction between a missing, disabled and half-deleted key is what tells
// someone which thing to go and fix.
//
// The check runs once per API call rather than once per record, so a batch put
// costs the same as a single one. A stack assembled without KMS wired skips it
// entirely: the absence of the service is not the same as a bad key.
func (s *Server) requireUsableKey(keyID string) *awshttp.APIError {
	if keyID == "" || s.peers == nil {
		return nil
	}
	state, ok, err := peercall.KMSDescribeKey(context.Background(), s.peers, keyID)
	if err != nil || !ok {
		// KMS unreachable is not the stream's fault; refusing writes here would
		// turn a missing peer into data loss.
		return nil
	}
	if !state.Found {
		return errKMSNotFound(keyID)
	}
	switch state.State {
	case "Enabled":
		return nil
	case "PendingDeletion", "PendingImport", "Unavailable":
		return errKMSInvalidState(keyID, state.State)
	default:
		return errKMSDisabled(keyID)
	}
}

// keyForStream returns the customer key a stream writes under, or "" when it
// is not encrypted.
func (s *Server) keyForStream(stream string) string {
	st, err := s.store.Get(stream)
	if err != nil || st.EncryptionType != "KMS" {
		return ""
	}
	return st.KeyID
}
