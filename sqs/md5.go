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

func encodeHandle(seqKey []byte) string {
	return base64.StdEncoding.EncodeToString(seqKey)
}

func decodeHandle(h string) ([]byte, error) {
	b, err := base64.StdEncoding.DecodeString(h)
	if err != nil || len(b) != 8 {
		return nil, fmt.Errorf("bad handle")
	}
	return b, nil
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
