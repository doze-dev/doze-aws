package kinesis_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awskinesis "github.com/aws/aws-sdk-go-v2/service/kinesis"
	kintypes "github.com/aws/aws-sdk-go-v2/service/kinesis/types"
)

// The sizes that bound a record's Data, and which rule refuses first.
//
// Two rules are in the model, and they are different rules with different
// codes. The Data blob itself is at most 10485760 BYTES (the shape's length);
// it travels as base64, 13,981,016 characters at the bound. And a record,
// "the data blob ... added to the partition key size", may not exceed the
// stream's maximum record size, which a stream here has at 1 MiB (1024 KiB is
// the least MaxRecordSizeInKiB the model allows, and UpdateMaxRecordSize does
// not raise it locally).
//
// So going up: a record of exactly 1 MiB is accepted; one byte more is refused
// by the record-size rule, InvalidArgumentException, and stays so all the way
// to the model's bound on Data; one byte past that bound is refused by the
// shape's length, ValidationException, before the stream is looked at.
const (
	mib           = 1 << 20
	dataModelMax  = 10485760
	boundaryKey   = "pk"
	largestRecord = mib - len(boundaryKey)
)

func TestPutRecordAtTheRecordSizeBoundary(t *testing.T) {
	ctx := context.Background()
	c := client(t)
	if _, err := c.CreateStream(ctx, &awskinesis.CreateStreamInput{StreamName: aws.String("sized"), ShardCount: aws.Int32(1)}); err != nil {
		t.Fatal(err)
	}
	put := func(stream string, n int) error {
		_, err := c.PutRecord(ctx, &awskinesis.PutRecordInput{StreamName: aws.String(stream),
			PartitionKey: aws.String(boundaryKey), Data: bytes.Repeat([]byte{0x5a}, n)})
		return err
	}

	// Past 786,432 bytes the base64 text is itself over 1 MiB: a check that
	// measured the text would refuse here.
	for _, n := range []int{0, 786432, 786433, largestRecord} {
		if err := put("sized", n); err != nil {
			t.Fatalf("a record of %d data bytes and a %d byte key was refused: %v", n, len(boundaryKey), err)
		}
	}
	// The largest one comes back whole.
	it, err := c.GetShardIterator(ctx, &awskinesis.GetShardIteratorInput{StreamName: aws.String("sized"),
		ShardId: aws.String("shardId-000000000000"), ShardIteratorType: kintypes.ShardIteratorTypeTrimHorizon})
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.GetRecords(ctx, &awskinesis.GetRecordsInput{ShardIterator: it.ShardIterator})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Records) != 4 || len(got.Records[3].Data) != largestRecord {
		t.Fatalf("want the four records back with the last of %d bytes, got %d records", largestRecord, len(got.Records))
	}

	// One byte more and the record, data and key together, is over 1 MiB.
	assertCode(t, put("sized", largestRecord+1), "InvalidArgumentException")
	// Data of exactly 1 MiB is over by the size of its key.
	assertCode(t, put("sized", mib), "InvalidArgumentException")
	// At the model's own bound on Data the shape is satisfied, and the record
	// is still ten times what the stream takes.
	assertCode(t, put("sized", dataModelMax), "InvalidArgumentException")

	// One byte past the model's bound is the shape's refusal, and it comes
	// first: the stream named does not exist, and that is not what is reported.
	assertCode(t, put("sized", dataModelMax+1), "ValidationException")
	assertCode(t, put("no-such-stream", dataModelMax+1), "ValidationException")
	assertCode(t, put("no-such-stream", 1), "ResourceNotFoundException")
}

// PutRecords holds each record to the same two rules.
func TestPutRecordsAtTheRecordSizeBoundary(t *testing.T) {
	ctx := context.Background()
	c := client(t)
	if _, err := c.CreateStream(ctx, &awskinesis.CreateStreamInput{StreamName: aws.String("sized"), ShardCount: aws.Int32(1)}); err != nil {
		t.Fatal(err)
	}
	put := func(sizes ...int) error {
		var entries []kintypes.PutRecordsRequestEntry
		for _, n := range sizes {
			entries = append(entries, kintypes.PutRecordsRequestEntry{
				PartitionKey: aws.String(boundaryKey), Data: bytes.Repeat([]byte{0x5a}, n)})
		}
		_, err := c.PutRecords(ctx, &awskinesis.PutRecordsInput{StreamName: aws.String("sized"), Records: entries})
		return err
	}
	if err := put(1, largestRecord); err != nil {
		t.Fatalf("two records, the second exactly 1 MiB with its key, were refused: %v", err)
	}
	// The second record, not the first: every record is measured.
	assertCode(t, put(1, largestRecord+1), "InvalidArgumentException")
	assertCode(t, put(1, dataModelMax), "InvalidArgumentException")
	assertCode(t, put(1, dataModelMax+1), "ValidationException")

	// And the request has a bound of its own, in the operation's words: "up to
	// a limit of 10 MiB for the entire request, including partition keys". Ten
	// records of exactly 1 MiB are exactly that; an eleventh, each of them
	// within the record's own limit, is over.
	ten := make([]int, 10)
	for i := range ten {
		ten[i] = largestRecord
	}
	if err := put(ten...); err != nil {
		t.Fatalf("ten records of 1 MiB each, 10 MiB with their keys, were refused: %v", err)
	}
	assertCode(t, put(append(ten, 1)...), "InvalidArgumentException")
	assertCode(t, put(append(ten, largestRecord)...), "InvalidArgumentException")
}
