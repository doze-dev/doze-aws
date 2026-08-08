package kinesis

// Shard iterators, ARNs and sequence-number formatting — the identity and
// cursor plumbing shared by the control and data planes.

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/doze-dev/doze-aws/awsident"
)

const accountID = awsident.AccountID

// iteratorTTL matches AWS: a shard iterator goes stale after five minutes, and
// consumers are expected to handle ExpiredIteratorException by asking for a new
// one. Enforcing it locally means code that ignores the contract fails here
// rather than in production.
const iteratorTTL = 5 * time.Minute

func streamARN(name string) string { return awsident.ARN("kinesis", "stream/"+name) }

// shardID renders a shard number the way Kinesis does: shardId- and twelve
// zero-padded digits.
func shardID(n int) string { return fmt.Sprintf("shardId-%012d", n) }

// formatSeq renders an internal counter as a wire sequence number. Kinesis
// sequence numbers are opaque decimal strings that consumers (notably the KCL)
// compare as big integers, so a zero-padded decimal both sorts correctly as a
// string and compares correctly as a number.
func formatSeq(seq uint64) string { return fmt.Sprintf("%021d", seq) }

// parseSeq reads a wire sequence number back. Leading zeros are fine; anything
// non-numeric is rejected so a garbled cursor fails loudly.
func parseSeq(s string) (uint64, bool) {
	if s == "" {
		return 0, false
	}
	n, err := strconv.ParseUint(strings.TrimLeft(s, "0"), 10, 64)
	if err != nil {
		if strings.Trim(s, "0") == "" {
			return 0, true // all zeros is sequence 0
		}
		return 0, false
	}
	return n, true
}

// cursor is the decoded form of a shard iterator: which shard, and the
// sequence already consumed. Iterators are exclusive — the next read starts
// after After.
type cursor struct {
	Stream   string
	Shard    string
	After    uint64
	IssuedNs int64
}

// encodeIterator renders a cursor as the opaque base64 token AWS hands out.
// The token is not signed: this is a local emulator, and a readable cursor is
// worth more when debugging than tamper-resistance is.
func encodeIterator(c cursor) string {
	raw := fmt.Sprintf("%s|%s|%d|%d", c.Stream, c.Shard, c.After, c.IssuedNs)
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// decodeIterator parses a shard iterator, rejecting malformed tokens and ones
// past their TTL.
func decodeIterator(tok string, now time.Time) (cursor, *apiError) {
	var c cursor
	raw, err := base64.RawURLEncoding.DecodeString(tok)
	if err != nil {
		return c, errInvalid("invalid ShardIterator")
	}
	parts := strings.Split(string(raw), "|")
	if len(parts) != 4 {
		return c, errInvalid("invalid ShardIterator")
	}
	after, err1 := strconv.ParseUint(parts[2], 10, 64)
	issued, err2 := strconv.ParseInt(parts[3], 10, 64)
	if err1 != nil || err2 != nil {
		return c, errInvalid("invalid ShardIterator")
	}
	c = cursor{Stream: parts[0], Shard: parts[1], After: after, IssuedNs: issued}
	if now.Sub(time.Unix(0, issued)) > iteratorTTL {
		return c, errExpiredIterator(
			"Iterator expired. The iterator was created at %s and was expired after %s.",
			time.Unix(0, issued).UTC().Format(time.RFC3339), iteratorTTL)
	}
	return c, nil
}
