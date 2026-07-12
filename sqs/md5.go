package sqs

import (
	"crypto/md5"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"sort"

	"github.com/doze-dev/doze-aws/awsident"
)

func queueARN(name string) string { return awsident.ARN("sqs", name) }

// encodeHandle builds an opaque receipt handle from the message's sequence key
// (8 bytes) and its unique id. Binding the id into the handle prevents aliasing:
// after a Purge or DeleteQueue+CreateQueue resets bbolt's NextSequence, a stale
// handle's seq can collide with a brand-new message — the id check rejects it so
// a deferred Delete can never target a different message.
func encodeHandle(seqKey []byte, id string) string {
	buf := make([]byte, 0, len(seqKey)+len(id))
	buf = append(buf, seqKey...)
	buf = append(buf, id...)
	return base64.StdEncoding.EncodeToString(buf)
}

func decodeHandle(h string) (seqKey []byte, id string, err error) {
	b, derr := base64.StdEncoding.DecodeString(h)
	if derr != nil || len(b) < 8 {
		return nil, "", fmt.Errorf("bad handle")
	}
	return b[:8], string(b[8:]), nil
}

// md5Attributes computes MD5OfMessageAttributes per the AWS algorithm, which the
// SDKs validate. Returns "" when there are no attributes. Each attribute, sorted
// by name, contributes: len+name, len+dataType, then a transport-type byte
// (1 for String/Number values, 2 for Binary) and len+value bytes.
func md5Attributes(attrs map[string]Attr) string {
	if len(attrs) == 0 {
		return ""
	}
	names := make([]string, 0, len(attrs))
	for n := range attrs {
		names = append(names, n)
	}
	sort.Strings(names)

	h := md5.New()
	writeField := func(b []byte) {
		var l [4]byte
		binary.BigEndian.PutUint32(l[:], uint32(len(b)))
		h.Write(l[:])
		h.Write(b)
	}
	for _, name := range names {
		a := attrs[name]
		writeField([]byte(name))
		writeField([]byte(a.DataType))
		if len(a.BinaryValue) > 0 {
			h.Write([]byte{2})
			writeField(a.BinaryValue)
		} else {
			h.Write([]byte{1})
			writeField([]byte(a.StringValue))
		}
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}
