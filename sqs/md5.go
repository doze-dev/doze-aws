package sqs

import (
	"crypto/md5"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// queueARN is a method rather than a free function because an ARN now depends
// on which instance minted it, and the store is what carries that.
func (s *store) queueARN(name string) string { return s.id.ARN("sqs", name) }

// encodeHandle builds an opaque receipt handle from the message's sequence key
// (8 bytes), its unique id and the number of times it has been received.
//
// Binding the id into the handle prevents aliasing: after a Purge or
// DeleteQueue+CreateQueue resets bbolt's NextSequence, a stale handle's seq can
// collide with a brand-new message — the id check rejects it so a deferred
// Delete can never target a different message.
//
// Binding the receive count makes a handle the receive's, as on AWS: a handle
// is good for the most recent receive, and a delete with an older one succeeds
// without deleting (doc: the model's DeleteMessage). Nothing is stored for it:
// the count is already in the persisted message.
func encodeHandle(seqKey []byte, id string, receives int) string {
	buf := make([]byte, 0, len(seqKey)+len(id)+8)
	buf = append(buf, seqKey...)
	buf = append(buf, id...)
	buf = append(buf, '#')
	buf = strconv.AppendInt(buf, int64(receives), 10)
	return base64.StdEncoding.EncodeToString(buf)
}

// decodeHandle splits a handle. receives is -1 for a handle minted before
// handles carried one, which is taken to be good for whatever receive is
// current.
func decodeHandle(h string) (seqKey []byte, id string, receives int, err error) {
	b, derr := base64.StdEncoding.DecodeString(h)
	if derr != nil || len(b) < 8 {
		return nil, "", 0, fmt.Errorf("bad handle")
	}
	rest := string(b[8:])
	receives = -1
	if i := strings.LastIndexByte(rest, '#'); i >= 0 {
		if n, err := strconv.Atoi(rest[i+1:]); err == nil {
			rest, receives = rest[:i], n
		}
	}
	return b[:8], rest, receives, nil
}

// md5Attributes computes MD5OfMessageAttributes per the AWS algorithm, which the
// SDKs validate. Returns "" when there are no attributes. Each attribute, sorted
// by name, contributes: len+name, len+dataType, then a transport-type byte
// (1 for String/Number values, 2 for Binary) and len+value bytes.
func md5Attributes(attrs map[string]attr) string {
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

// md5SystemAttributes is the digest of the system attributes a send carried
// (MD5OfMessageSystemAttributes), computed the way the user attributes' is:
// each is a String, which is the only type AWSTraceHeader has (doc: the
// model's SendMessageBatchRequestEntry).
func md5SystemAttributes(sys map[string]string) string {
	if len(sys) == 0 {
		return ""
	}
	as := make(map[string]attr, len(sys))
	for k, v := range sys {
		as[k] = attr{DataType: "String", StringValue: v}
	}
	return md5Attributes(as)
}
