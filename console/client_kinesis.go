package console

// Kinesis console client (AWS JSON 1.1).
//
// The console shows what makes a local Kinesis legible: which shards a stream
// has, what hash range each covers, and what is actually sitting in them. The
// shard view carries lineage because that is the part people get wrong — a
// closed parent with two children is the shape of a reshard, and seeing it
// explains why a consumer is reading where it is.

import (
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Stream is one Kinesis stream as the list pane shows it.
type Stream struct {
	Name      string
	ARN       string
	Status    string
	Mode      string
	Shards    int // open shards
	Retention int // hours
	Created   string
	Tags      map[string]string
	// Encryption, KeyID and Metrics are stored by the service but have no
	// local effect — there is no envelope over the bbolt file and no
	// CloudWatch to publish to. They round-trip so SDK paths that set them
	// keep working, and the console says as much rather than implying more.
	Encryption string
	KeyID      string
	Metrics    []string
}

// Shard is one shard, with the lineage that explains a reshard.
type Shard struct {
	ID       string
	Parent   string
	Adjacent string
	StartKey string
	EndKey   string
	StartSeq string
	EndSeq   string // "" while open
	Closed   bool
	// Share is the fraction of the 128-bit hash space this shard covers,
	// which is the only intuitive way to read those enormous key bounds.
	Share float64
}

// ShardCount is a shard's depth, fetched lazily. Kinesis has no count API —
// the only way to know is to read — so this is capped and says so rather than
// pretending a big shard is exactly as deep as the cap.
type ShardCount struct {
	N      int
	Capped bool
}

// KRecord is one record in the browser.
type KRecord struct {
	Seq       string
	SeqNum    uint64 // the stream-global sequence, for merge order and cursors
	Shard     string
	Partition string
	Arrived   string
	ArrivedAt time.Time
	Data      string
	Truncated bool // payload clipped for the listing; the row can be expanded
	Binary    bool
	Bytes     int
}

func (b *backend) kinesis(ctx context.Context, action string, in any) ([]byte, error) {
	return b.json11(ctx, "Kinesis_20131202", action, in)
}

func (b *backend) ListStreams(ctx context.Context) ([]Stream, error) {
	body, err := b.kinesis(ctx, "ListStreams", map[string]any{})
	if err != nil {
		return nil, err
	}
	var out struct {
		StreamNames []string `json:"StreamNames"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	streams := make([]Stream, 0, len(out.StreamNames))
	for _, name := range out.StreamNames {
		s, err := b.StreamSummary(ctx, name)
		if err != nil {
			// A stream that vanished between list and describe is not an error
			// worth failing the whole page for.
			continue
		}
		streams = append(streams, *s)
	}
	sort.Slice(streams, func(i, j int) bool { return streams[i].Name < streams[j].Name })
	return streams, nil
}

// CountStreams is the cheap cardinality probe for the nav counts.
func (b *backend) CountStreams(ctx context.Context) (int, error) {
	body, err := b.kinesis(ctx, "ListStreams", map[string]any{})
	if err != nil {
		return 0, err
	}
	var out struct {
		StreamNames []string `json:"StreamNames"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return 0, err
	}
	return len(out.StreamNames), nil
}

func (b *backend) StreamSummary(ctx context.Context, name string) (*Stream, error) {
	body, err := b.kinesis(ctx, "DescribeStreamSummary", map[string]any{"StreamName": name})
	if err != nil {
		return nil, err
	}
	var out struct {
		D struct {
			StreamName        string `json:"StreamName"`
			StreamARN         string `json:"StreamARN"`
			StreamStatus      string `json:"StreamStatus"`
			StreamModeDetails struct {
				StreamMode string `json:"StreamMode"`
			} `json:"StreamModeDetails"`
			RetentionPeriodHours    int     `json:"RetentionPeriodHours"`
			OpenShardCount          int     `json:"OpenShardCount"`
			StreamCreationTimestamp float64 `json:"StreamCreationTimestamp"`
			EncryptionType          string  `json:"EncryptionType"`
			KeyID                   string  `json:"KeyId"`
			EnhancedMonitoring      []struct {
				ShardLevelMetrics []string `json:"ShardLevelMetrics"`
			} `json:"EnhancedMonitoring"`
		} `json:"StreamDescriptionSummary"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	d := out.D
	return &Stream{
		Name: d.StreamName, ARN: d.StreamARN, Status: d.StreamStatus,
		Mode: d.StreamModeDetails.StreamMode, Shards: d.OpenShardCount,
		Retention:  d.RetentionPeriodHours,
		Created:    time.Unix(int64(d.StreamCreationTimestamp), 0).UTC().Format("2006-01-02 15:04:05"),
		Encryption: d.EncryptionType, KeyID: d.KeyID,
		Metrics: firstMetrics(d.EnhancedMonitoring),
	}, nil
}

// maxHash is the top of the partition-key hash space, used to express a
// shard's coverage as a readable percentage.
var maxHash = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 128), big.NewInt(1))

func (b *backend) ListShards(ctx context.Context, stream string) ([]Shard, error) {
	body, err := b.kinesis(ctx, "ListShards", map[string]any{"StreamName": stream})
	if err != nil {
		return nil, err
	}
	var out struct {
		Shards []struct {
			ShardID      string `json:"ShardId"`
			Parent       string `json:"ParentShardId"`
			Adjacent     string `json:"AdjacentParentShardId"`
			HashKeyRange struct {
				Starting string `json:"StartingHashKey"`
				Ending   string `json:"EndingHashKey"`
			} `json:"HashKeyRange"`
			SeqRange struct {
				Starting string `json:"StartingSequenceNumber"`
				Ending   string `json:"EndingSequenceNumber"`
			} `json:"SequenceNumberRange"`
		} `json:"Shards"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	shards := make([]Shard, 0, len(out.Shards))
	for _, s := range out.Shards {
		sh := Shard{
			ID: s.ShardID, Parent: s.Parent, Adjacent: s.Adjacent,
			StartKey: s.HashKeyRange.Starting, EndKey: s.HashKeyRange.Ending,
			StartSeq: s.SeqRange.Starting, EndSeq: s.SeqRange.Ending,
			Closed: s.SeqRange.Ending != "",
			Share:  hashShare(s.HashKeyRange.Starting, s.HashKeyRange.Ending),
		}
		shards = append(shards, sh)
	}
	sort.Slice(shards, func(i, j int) bool { return shards[i].ID < shards[j].ID })
	return shards, nil
}

// hashShare converts a shard's hash bounds into a fraction of the key space.
func hashShare(start, end string) float64 {
	lo, ok1 := new(big.Int).SetString(start, 10)
	hi, ok2 := new(big.Int).SetString(end, 10)
	if !ok1 || !ok2 {
		return 0
	}
	span := new(big.Int).Sub(hi, lo)
	span.Add(span, big.NewInt(1))
	spanF := new(big.Float).SetInt(span)
	totalF := new(big.Float).SetInt(new(big.Int).Add(maxHash, big.NewInt(1)))
	share, _ := new(big.Float).Quo(spanF, totalF).Float64()
	return share
}

// ---- reading records ----

// maxPayloadPreview caps what a record contributes to a listing. Records run to
// 1 MiB and a page can carry many of them, so the browser is never handed the
// whole payload; the single-record view re-reads the one record that is asked
// for.
const maxPayloadPreview = 400

// countCap bounds the lazy per-shard depth probe. Counting means reading, so
// this trades an exact number for a bounded cost and reports itself as capped.
const countCap = 1000

// startAt names where a read begins. These four are the only things Kinesis can
// push down; every other control the explorer offers filters what came back.
type startAt struct {
	Mode  string // "horizon" | "time" | "seq" | "latest"
	Since time.Time
	Seq   string
}

func (s startAt) iteratorInput(stream, shard string) map[string]any {
	in := map[string]any{"StreamName": stream, "ShardId": shard}
	switch s.Mode {
	case "time":
		in["ShardIteratorType"] = "AT_TIMESTAMP"
		in["Timestamp"] = float64(s.Since.UnixNano()) / 1e9
	case "seq":
		// Exclusive: resuming a page must not repeat its last row.
		in["ShardIteratorType"] = "AFTER_SEQUENCE_NUMBER"
		in["StartingSequenceNumber"] = s.Seq
	case "latest":
		in["ShardIteratorType"] = "LATEST"
	default:
		in["ShardIteratorType"] = "TRIM_HORIZON"
	}
	return in
}

// RecordQuery is one pass of the explorer: where to start, how much to read,
// and how to narrow the result.
type RecordQuery struct {
	Stream string
	Shard  string // "" reads every open shard and merges them
	Start  startAt
	Limit  int

	// Post-filters. These run over the window that was read, which is why the
	// page reports how much it scanned.
	Partition string
	Contains  string
	Until     time.Time

	// StartOpt is the raw start control, kept so a re-rendered filter bar shows
	// the option that was actually run.
	StartOpt string
	// UntilOpt is the raw datetime-local value, kept for the same reason.
	UntilOpt string
	// Routed records that Partition picked the shard, so the page can say the
	// search was narrowed by routing rather than by scanning.
	Routed bool
	// Follow keeps the view polling forward from the cursor.
	Follow bool
}

// RecordPage is what one read produced, alongside enough about the read itself
// that the page can be honest about what it did and did not look at.
type RecordPage struct {
	Records  []KRecord
	Cursor   string // resume point; "" when every shard drained
	Scanned  int    // records read before post-filtering
	Shards   []string
	Drained  bool // nothing further to read right now
	Filtered bool // a post-filter was applied, so Scanned != len(Records)
}

// ReadRecords runs one query. It is a browser, not a consumer: it never
// checkpoints, so looking at a stream in the console cannot disturb an
// application reading it.
//
// Sequence numbers are allocated per stream rather than per shard, so a single
// cursor resumes correctly across a merged multi-shard read and the merge order
// is a true arrival order rather than an approximation.
func (b *backend) ReadRecords(ctx context.Context, q RecordQuery) (*RecordPage, error) {
	if q.Limit <= 0 {
		q.Limit = 50
	}
	shards := []string{q.Shard}
	if q.Shard == "" {
		all, err := b.ListShards(ctx, q.Stream)
		if err != nil {
			return nil, err
		}
		shards = shards[:0]
		for _, sh := range all {
			// A closed shard still holds readable records until retention
			// reclaims them, so it belongs in the merge.
			shards = append(shards, sh.ID)
		}
	}

	page := &RecordPage{Shards: shards, Drained: true}
	var all []KRecord
	for _, shard := range shards {
		recs, drained, err := b.readOneShard(ctx, q, shard)
		if err != nil {
			return nil, err
		}
		if !drained {
			page.Drained = false
		}
		all = append(all, recs...)
	}
	page.Scanned = len(all)

	// Merge on the stream-global sequence, which is arrival order.
	sort.Slice(all, func(i, j int) bool { return all[i].SeqNum < all[j].SeqNum })

	kept := make([]KRecord, 0, len(all))
	for _, rec := range all {
		if !q.Until.IsZero() && rec.ArrivedAt.After(q.Until) {
			// Past the requested window: later records are too, but the read
			// still counts as scanned.
			continue
		}
		if q.Partition != "" && rec.Partition != q.Partition {
			continue
		}
		if q.Contains != "" && !strings.Contains(rec.Data, q.Contains) {
			continue
		}
		kept = append(kept, rec)
	}
	page.Filtered = len(kept) != len(all)

	// The cursor tracks the read, not the filtered result: paging must resume
	// past everything examined or a filtered page would loop on the same rows.
	if len(all) > 0 {
		page.Cursor = all[len(all)-1].Seq
	} else if q.Start.Mode == "seq" {
		// Nothing new this time. Hold the cursor where it was, or a follow poll
		// would fall back to its start control and re-read the whole shard.
		page.Cursor = q.Start.Seq
	}
	if len(kept) > q.Limit {
		kept = kept[:q.Limit]
		// Truncating the merge means there is more to show without re-reading.
		page.Cursor = kept[len(kept)-1].Seq
		page.Drained = false
	}
	for i := range kept {
		kept[i].Data, kept[i].Truncated = truncatePayload(kept[i].Data)
	}
	page.Records = kept
	return page, nil
}

// readOneShard reads a single shard once, returning whether it had nothing
// further to give.
func (b *backend) readOneShard(ctx context.Context, q RecordQuery, shard string) ([]KRecord, bool, error) {
	body, err := b.kinesis(ctx, "GetShardIterator", q.Start.iteratorInput(q.Stream, shard))
	if err != nil {
		return nil, true, err
	}
	var it struct {
		ShardIterator string `json:"ShardIterator"`
	}
	if err := json.Unmarshal(body, &it); err != nil {
		return nil, true, err
	}
	body, err = b.kinesis(ctx, "GetRecords", map[string]any{
		"ShardIterator": it.ShardIterator, "Limit": q.Limit,
	})
	if err != nil {
		return nil, true, err
	}
	var out struct {
		Records []struct {
			SequenceNumber string  `json:"SequenceNumber"`
			PartitionKey   string  `json:"PartitionKey"`
			Data           string  `json:"Data"`
			Arrived        float64 `json:"ApproximateArrivalTimestamp"`
		} `json:"Records"`
		// A closed, drained shard returns a null iterator; that null is how a
		// reader learns the shard has ended.
		Next *string `json:"NextShardIterator"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, true, err
	}
	recs := make([]KRecord, 0, len(out.Records))
	for _, r := range out.Records {
		recs = append(recs, decodeRecord(shard, r.SequenceNumber, r.PartitionKey, r.Data, r.Arrived))
	}
	// A short read means the shard is caught up for now; a full one means there
	// is more behind it.
	drained := out.Next == nil || len(out.Records) < q.Limit
	return recs, drained, nil
}

// decodeRecord turns one wire record into the shape the table renders.
func decodeRecord(shard, seq, partition, data string, arrived float64) KRecord {
	raw, derr := base64.StdEncoding.DecodeString(data)
	if derr != nil {
		raw = []byte(data)
	}
	at := time.Unix(0, int64(arrived*1e9)).UTC()
	num, _ := strconv.ParseUint(trimSeq(seq), 10, 64)
	rec := KRecord{
		Seq: trimSeq(seq), SeqNum: num, Shard: shard, Partition: partition,
		Arrived: at.Format("15:04:05"), ArrivedAt: at, Bytes: len(raw),
	}
	if isPrintable(raw) {
		rec.Data = string(raw)
	} else {
		rec.Binary = true
		rec.Data = base64.StdEncoding.EncodeToString(raw)
	}
	return rec
}

// truncatePayload bounds what one row contributes to a listing.
func truncatePayload(s string) (string, bool) {
	if len(s) <= maxPayloadPreview {
		return s, false
	}
	return s[:maxPayloadPreview], true
}

// ReadOne fetches a single record whole, for the row that asked to be expanded.
func (b *backend) ReadOne(ctx context.Context, stream, shard, seq string) (*KRecord, error) {
	body, err := b.kinesis(ctx, "GetShardIterator", map[string]any{
		"StreamName": stream, "ShardId": shard,
		"ShardIteratorType": "AT_SEQUENCE_NUMBER", "StartingSequenceNumber": seq,
	})
	if err != nil {
		return nil, err
	}
	var it struct {
		ShardIterator string `json:"ShardIterator"`
	}
	if err := json.Unmarshal(body, &it); err != nil {
		return nil, err
	}
	body, err = b.kinesis(ctx, "GetRecords", map[string]any{
		"ShardIterator": it.ShardIterator, "Limit": 1,
	})
	if err != nil {
		return nil, err
	}
	var out struct {
		Records []struct {
			SequenceNumber string  `json:"SequenceNumber"`
			PartitionKey   string  `json:"PartitionKey"`
			Data           string  `json:"Data"`
			Arrived        float64 `json:"ApproximateArrivalTimestamp"`
		} `json:"Records"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	if len(out.Records) == 0 {
		return nil, fmt.Errorf("record %s is no longer in %s — retention may have reclaimed it", seq, shard)
	}
	r := out.Records[0]
	rec := decodeRecord(shard, r.SequenceNumber, r.PartitionKey, r.Data, r.Arrived)
	return &rec, nil
}

// CountShard probes how deep a shard is. There is no count API in Kinesis, so
// this reads, and is capped.
func (b *backend) CountShard(ctx context.Context, stream, shard string) (ShardCount, error) {
	recs, _, err := b.readOneShard(ctx, RecordQuery{
		Stream: stream, Start: startAt{Mode: "horizon"}, Limit: countCap,
	}, shard)
	if err != nil {
		return ShardCount{}, err
	}
	return ShardCount{N: len(recs), Capped: len(recs) >= countCap}, nil
}

// trimSeq drops the zero padding a sequence number carries on the wire, since
// the padding is only there to make string and numeric ordering agree.
func trimSeq(s string) string {
	t := strings.TrimLeft(s, "0")
	if t == "" {
		return "0"
	}
	return t
}

// isPrintable reports whether a record body should be shown as text.
func isPrintable(b []byte) bool {
	if len(b) == 0 {
		return true
	}
	for _, c := range b {
		if c == '\n' || c == '\r' || c == '\t' {
			continue
		}
		if c < 0x20 || c == 0x7f {
			return false
		}
	}
	return true
}

// ---- mutations ----

func (b *backend) CreateStream(ctx context.Context, name string, shards int) error {
	if shards < 1 {
		shards = 1
	}
	_, err := b.kinesis(ctx, "CreateStream", map[string]any{
		"StreamName": name, "ShardCount": shards,
	})
	return err
}

func (b *backend) DeleteStream(ctx context.Context, name string) error {
	_, err := b.kinesis(ctx, "DeleteStream", map[string]any{"StreamName": name})
	return err
}

func (b *backend) PutRecord(ctx context.Context, stream, partitionKey, data string) (string, error) {
	body, err := b.kinesis(ctx, "PutRecord", map[string]any{
		"StreamName": stream, "PartitionKey": partitionKey,
		"Data": base64.StdEncoding.EncodeToString([]byte(data)),
	})
	if err != nil {
		return "", err
	}
	var out struct {
		ShardID string `json:"ShardId"`
	}
	json.Unmarshal(body, &out)
	return out.ShardID, nil
}

func (b *backend) SplitShard(ctx context.Context, stream, shard, at string) error {
	_, err := b.kinesis(ctx, "SplitShard", map[string]any{
		"StreamName": stream, "ShardToSplit": shard, "NewStartingHashKey": at,
	})
	return err
}

// MidpointOf returns the hash key halfway through a shard's range, which is
// what a split without an explicit key should use.
func MidpointOf(startKey, endKey string) string {
	lo, ok1 := new(big.Int).SetString(startKey, 10)
	hi, ok2 := new(big.Int).SetString(endKey, 10)
	if !ok1 || !ok2 {
		return ""
	}
	mid := new(big.Int).Add(lo, hi)
	mid.Div(mid, big.NewInt(2))
	// The split point must be strictly inside the parent, so a one-key shard
	// cannot be split at all.
	if mid.Cmp(lo) <= 0 {
		return ""
	}
	return mid.String()
}

func (b *backend) SetRetention(ctx context.Context, stream string, hours int) error {
	cur, err := b.StreamSummary(ctx, stream)
	if err != nil {
		return err
	}
	action := "IncreaseStreamRetentionPeriod"
	if hours < cur.Retention {
		action = "DecreaseStreamRetentionPeriod"
	}
	if hours == cur.Retention {
		return nil
	}
	_, err = b.kinesis(ctx, action, map[string]any{
		"StreamName": stream, "RetentionPeriodHours": hours,
	})
	return err
}

func (b *backend) StreamTags(ctx context.Context, stream string) (map[string]string, error) {
	body, err := b.kinesis(ctx, "ListTagsForStream", map[string]any{"StreamName": stream})
	if err != nil {
		return nil, err
	}
	var out struct {
		Tags []struct{ Key, Value string } `json:"Tags"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	tags := map[string]string{}
	for _, t := range out.Tags {
		tags[t.Key] = t.Value
	}
	return tags, nil
}

// pct renders a hash-space share the way the shard table shows it.
func pct(f float64) string {
	return strconv.FormatFloat(f*100, 'f', 1, 64) + "%"
}

// ShardForKey returns the shard a partition key routes to. Kinesis hashes the
// key with MD5 and places it in the shard whose hash range contains it, so
// this is not a guess: naming a key tells the explorer exactly which shard to
// read instead of scanning all of them for a match that can only be in one.
func ShardForKey(shards []Shard, key string) string {
	sum := md5.Sum([]byte(key))
	h := new(big.Int).SetBytes(sum[:])
	for _, sh := range shards {
		if sh.Closed {
			continue
		}
		lo, ok1 := new(big.Int).SetString(sh.StartKey, 10)
		hi, ok2 := new(big.Int).SetString(sh.EndKey, 10)
		if !ok1 || !ok2 {
			continue
		}
		if h.Cmp(lo) >= 0 && h.Cmp(hi) <= 0 {
			return sh.ID
		}
	}
	return ""
}

// jsonVals renders the hx-vals payload a paging or polling trigger carries so
// the next read repeats the same query.
func jsonVals(m map[string]string) string {
	for k, v := range m {
		if v == "" {
			delete(m, k)
		}
	}
	raw, _ := json.Marshal(m)
	return string(raw)
}

// ---- shard layout and stream configuration ----

// MergePair is one legal merge: two open shards whose hash ranges touch.
type MergePair struct {
	Left, Right Shard
	// Share is what the merged shard would cover, which is the only reason to
	// prefer one pair over another.
	Share float64
}

// MergeablePairs returns the merges the service would actually accept. Kinesis
// will only merge two open shards whose ranges are exactly contiguous — a gap
// would silently drop part of the key space — so the console works out the
// legal pairs rather than asking anyone to name two shards and find out.
func MergeablePairs(shards []Shard) []MergePair {
	open := make([]Shard, 0, len(shards))
	for _, sh := range shards {
		if !sh.Closed {
			open = append(open, sh)
		}
	}
	sort.Slice(open, func(i, j int) bool {
		a, _ := new(big.Int).SetString(open[i].StartKey, 10)
		b, _ := new(big.Int).SetString(open[j].StartKey, 10)
		return a.Cmp(b) < 0
	})
	var pairs []MergePair
	for i := 0; i+1 < len(open); i++ {
		lHi, ok1 := new(big.Int).SetString(open[i].EndKey, 10)
		rLo, ok2 := new(big.Int).SetString(open[i+1].StartKey, 10)
		if !ok1 || !ok2 {
			continue
		}
		if new(big.Int).Add(lHi, big.NewInt(1)).Cmp(rLo) != 0 {
			continue
		}
		pairs = append(pairs, MergePair{
			Left: open[i], Right: open[i+1],
			Share: open[i].Share + open[i+1].Share,
		})
	}
	return pairs
}

func (b *backend) MergeShards(ctx context.Context, stream, left, right string) error {
	_, err := b.kinesis(ctx, "MergeShards", map[string]any{
		"StreamName": stream, "ShardToMerge": left, "AdjacentShardToMerge": right,
	})
	return err
}

// UpdateShardCount rescales a provisioned stream. The service refuses this on
// an ON_DEMAND stream, where the shard count is not ours to set.
func (b *backend) UpdateShardCount(ctx context.Context, stream string, target int) error {
	_, err := b.kinesis(ctx, "UpdateShardCount", map[string]any{
		"StreamName": stream, "TargetShardCount": target,
		"ScalingType": "UNIFORM_SCALING",
	})
	return err
}

func (b *backend) UpdateStreamMode(ctx context.Context, arn, mode string) error {
	_, err := b.kinesis(ctx, "UpdateStreamMode", map[string]any{
		"StreamARN":         arn,
		"StreamModeDetails": map[string]any{"StreamMode": mode},
	})
	return err
}

// ---- encryption, monitoring, consumers, policy ----
//
// The service stores these faithfully but none of them does anything locally:
// there is no KMS envelope over the bbolt file, no CloudWatch to publish shard
// metrics to, and no evaluation of a stream's resource policy. They are worth
// exposing because SDK and template code sets them and expects to read them
// back — but the console labels them for what they are rather than implying a
// local guarantee it cannot make.

// ShardMetrics are the shard-level metrics Kinesis can be asked to publish.
var ShardMetrics = []string{
	"IncomingBytes", "IncomingRecords", "OutgoingBytes", "OutgoingRecords",
	"WriteProvisionedThroughputExceeded", "ReadProvisionedThroughputExceeded",
	"IteratorAgeMilliseconds",
}

// firstMetrics pulls the metric list out of the wire's array-of-one shape.
func firstMetrics(in []struct {
	ShardLevelMetrics []string `json:"ShardLevelMetrics"`
}) []string {
	if len(in) == 0 {
		return nil
	}
	return in[0].ShardLevelMetrics
}

// Consumer is an enhanced fan-out registration.
type Consumer struct {
	Name    string
	ARN     string
	Status  string
	Created string
}

func (b *backend) StartEncryption(ctx context.Context, stream, keyID string) error {
	_, err := b.kinesis(ctx, "StartStreamEncryption", map[string]any{
		"StreamName": stream, "EncryptionType": "KMS", "KeyId": keyID,
	})
	return err
}

func (b *backend) StopEncryption(ctx context.Context, stream string) error {
	_, err := b.kinesis(ctx, "StopStreamEncryption", map[string]any{
		"StreamName": stream, "EncryptionType": "KMS",
	})
	return err
}

// SetMetrics moves a stream to exactly the requested metric set. The API only
// offers enable and disable, so the difference against what is already there
// is worked out here rather than making someone toggle each one.
func (b *backend) SetMetrics(ctx context.Context, stream string, desired []string) error {
	cur, err := b.StreamSummary(ctx, stream)
	if err != nil {
		return err
	}
	have := map[string]bool{}
	for _, m := range cur.Metrics {
		have[m] = true
	}
	want := map[string]bool{}
	for _, m := range desired {
		want[m] = true
	}
	var enable, disable []string
	for m := range want {
		if !have[m] {
			enable = append(enable, m)
		}
	}
	for m := range have {
		if !want[m] {
			disable = append(disable, m)
		}
	}
	sort.Strings(enable)
	sort.Strings(disable)
	if len(disable) > 0 {
		if _, err := b.kinesis(ctx, "DisableEnhancedMonitoring", map[string]any{
			"StreamName": stream, "ShardLevelMetrics": disable,
		}); err != nil {
			return err
		}
	}
	if len(enable) > 0 {
		if _, err := b.kinesis(ctx, "EnableEnhancedMonitoring", map[string]any{
			"StreamName": stream, "ShardLevelMetrics": enable,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (b *backend) ListConsumers(ctx context.Context, arn string) ([]Consumer, error) {
	body, err := b.kinesis(ctx, "ListStreamConsumers", map[string]any{"StreamARN": arn})
	if err != nil {
		return nil, err
	}
	var out struct {
		Consumers []struct {
			ConsumerName              string  `json:"ConsumerName"`
			ConsumerARN               string  `json:"ConsumerARN"`
			ConsumerStatus            string  `json:"ConsumerStatus"`
			ConsumerCreationTimestamp float64 `json:"ConsumerCreationTimestamp"`
		} `json:"Consumers"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	cs := make([]Consumer, 0, len(out.Consumers))
	for _, c := range out.Consumers {
		cs = append(cs, Consumer{
			Name: c.ConsumerName, ARN: c.ConsumerARN, Status: c.ConsumerStatus,
			Created: time.Unix(int64(c.ConsumerCreationTimestamp), 0).UTC().Format("2006-01-02 15:04:05"),
		})
	}
	sort.Slice(cs, func(i, j int) bool { return cs[i].Name < cs[j].Name })
	return cs, nil
}

func (b *backend) RegisterConsumer(ctx context.Context, arn, name string) error {
	_, err := b.kinesis(ctx, "RegisterStreamConsumer", map[string]any{
		"StreamARN": arn, "ConsumerName": name,
	})
	return err
}

func (b *backend) DeregisterConsumer(ctx context.Context, arn, name string) error {
	_, err := b.kinesis(ctx, "DeregisterStreamConsumer", map[string]any{
		"StreamARN": arn, "ConsumerName": name,
	})
	return err
}

// StreamPolicy returns the stream's resource policy, or "" when it has none.
// A stream without one answers ResourceNotFound, which is an absence rather
// than a failure.
func (b *backend) StreamPolicy(ctx context.Context, arn string) (string, error) {
	body, err := b.kinesis(ctx, "GetResourcePolicy", map[string]any{"ResourceARN": arn})
	if err != nil {
		return "", nil
	}
	var out struct {
		Policy string `json:"Policy"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", err
	}
	return out.Policy, nil
}

func (b *backend) PutStreamPolicy(ctx context.Context, arn, policy string) error {
	_, err := b.kinesis(ctx, "PutResourcePolicy", map[string]any{
		"ResourceARN": arn, "Policy": policy,
	})
	return err
}

func (b *backend) DeleteStreamPolicy(ctx context.Context, arn string) error {
	_, err := b.kinesis(ctx, "DeleteResourcePolicy", map[string]any{"ResourceARN": arn})
	return err
}

// KinesisLimits is the account-wide picture. Every field here is read-only:
// UpdateAccountSettings and UpdateMaxRecordSize are accepted and discarded, so
// offering them as controls would promise something that does not happen.
type KinesisLimits struct {
	ShardLimit          int
	OpenShardCount      int
	OnDemandStreams     int
	OnDemandStreamLimit int
	MaxRecordSize       int
	DefaultMode         string
}

func (b *backend) KinesisLimits(ctx context.Context) (*KinesisLimits, error) {
	body, err := b.kinesis(ctx, "DescribeLimits", map[string]any{})
	if err != nil {
		return nil, err
	}
	var lim struct {
		ShardLimit               int `json:"ShardLimit"`
		OpenShardCount           int `json:"OpenShardCount"`
		OnDemandStreamCount      int `json:"OnDemandStreamCount"`
		OnDemandStreamCountLimit int `json:"OnDemandStreamCountLimit"`
	}
	if err := json.Unmarshal(body, &lim); err != nil {
		return nil, err
	}
	out := &KinesisLimits{
		ShardLimit: lim.ShardLimit, OpenShardCount: lim.OpenShardCount,
		OnDemandStreams: lim.OnDemandStreamCount, OnDemandStreamLimit: lim.OnDemandStreamCountLimit,
	}
	body, err = b.kinesis(ctx, "DescribeAccountSettings", map[string]any{})
	if err != nil {
		return out, nil
	}
	var acct struct {
		MaxRecordSize            int `json:"MaxRecordSize"`
		DefaultStreamModeDetails struct {
			StreamMode string `json:"StreamMode"`
		} `json:"DefaultStreamModeDetails"`
	}
	if err := json.Unmarshal(body, &acct); err == nil {
		out.MaxRecordSize = acct.MaxRecordSize
		out.DefaultMode = acct.DefaultStreamModeDetails.StreamMode
	}
	return out, nil
}
