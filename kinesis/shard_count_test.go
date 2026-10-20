package kinesis_test

// ShardCount is a PositiveIntegerObject: the model gives it a range of 1 and up
// (CreateStream.ShardCount; the generated table carries it as `Min: 1`), and the
// member text says "you must specify the number of shards for the data stream"
// of a provisioned stream. A request that said 0 was treated as not having said
// it, and made a one-shard stream; the zero value of the Go decoder is also a
// 0 sent on the wire, and the two have to be told apart. Direction before: more
// forgiving. The wire is awsJson1.1 (this service has no other); the code is the
// one the generated constraint answers.

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awskinesis "github.com/aws/aws-sdk-go-v2/service/kinesis"
)

func TestAStreamOfZeroShardsIsRefused(t *testing.T) {
	ctx := context.Background()
	c := client(t)
	for _, n := range []int32{0, -1} {
		_, err := c.CreateStream(ctx, &awskinesis.CreateStreamInput{StreamName: aws.String("empty"), ShardCount: aws.Int32(n)})
		if err == nil {
			t.Errorf("ShardCount %d was accepted", n)
			continue
		}
		assertCode(t, err, "ValidationException")
		if !strings.Contains(err.Error(), "ShardCount") && !strings.Contains(strings.ToLower(err.Error()), "shardcount") {
			t.Errorf("ShardCount %d: the refusal does not name the member: %v", n, err)
		}
	}
	if out, err := c.ListStreams(ctx, &awskinesis.ListStreamsInput{}); err != nil || len(out.StreamNames) != 0 {
		t.Errorf("a refused create left a stream behind: %v, %v", out, err)
	}
	// One is the smallest stream.
	if _, err := c.CreateStream(ctx, &awskinesis.CreateStreamInput{StreamName: aws.String("small"), ShardCount: aws.Int32(1)}); err != nil {
		t.Errorf("one shard is valid: %v", err)
	}
}
