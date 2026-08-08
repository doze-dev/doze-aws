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
	"encoding/base64"
	"encoding/json"
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
	Count int // records currently readable
}

// KRecord is one record in the browser.
type KRecord struct {
	Seq       string
	Partition string
	Arrived   string
	Data      string
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
		} `json:"StreamDescriptionSummary"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	d := out.D
	return &Stream{
		Name: d.StreamName, ARN: d.StreamARN, Status: d.StreamStatus,
		Mode: d.StreamModeDetails.StreamMode, Shards: d.OpenShardCount,
		Retention: d.RetentionPeriodHours,
		Created:   time.Unix(int64(d.StreamCreationTimestamp), 0).UTC().Format("2006-01-02 15:04:05"),
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
		if recs, err := b.ReadShard(ctx, stream, sh.ID, 500); err == nil {
			sh.Count = len(recs)
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

// ReadShard reads a shard from the trim horizon. It is a browser, not a
// consumer: it always starts at the beginning and never checkpoints, so
// looking at a stream in the console cannot disturb an application reading it.
func (b *backend) ReadShard(ctx context.Context, stream, shard string, limit int) ([]KRecord, error) {
	body, err := b.kinesis(ctx, "GetShardIterator", map[string]any{
		"StreamName": stream, "ShardId": shard, "ShardIteratorType": "TRIM_HORIZON",
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
		"ShardIterator": it.ShardIterator, "Limit": limit,
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
	recs := make([]KRecord, 0, len(out.Records))
	for _, r := range out.Records {
		raw, derr := base64.StdEncoding.DecodeString(r.Data)
		if derr != nil {
			raw = []byte(r.Data)
		}
		rec := KRecord{
			Seq: trimSeq(r.SequenceNumber), Partition: r.PartitionKey,
			Arrived: time.Unix(int64(r.Arrived), 0).UTC().Format("15:04:05"),
			Bytes:   len(raw),
		}
		if isPrintable(raw) {
			rec.Data = string(raw)
		} else {
			rec.Binary = true
			rec.Data = base64.StdEncoding.EncodeToString(raw)
		}
		recs = append(recs, rec)
	}
	return recs, nil
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
