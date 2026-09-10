package rpcv2cbor

// The HTTP framing around a CBOR body.
//
// RPC v2 addresses an operation by PATH rather than by a target header, which
// is the one structural difference from awsJson: there is no X-Amz-Target to
// dispatch on, and the spec says a request MUST NOT carry one.
//
//	POST {prefix}/service/{ServiceName}/operation/{OperationName}
//	Smithy-Protocol: rpc-v2-cbor
//	Content-Type: application/cbor
//	Accept: application/cbor
//
// The prefix exists because a fronting router may serve the stack under a
// path (aws.demo.doze/cloudwatch), so the pair is matched wherever it appears
// rather than anchored at the root.

import (
	"net/http"
	"strings"

	"github.com/doze-dev/doze-aws/internal/awshttp"
)

// ContentType is the media type both directions use.
const ContentType = "application/cbor"

// ProtocolHeader and ProtocolID are the handshake. The spec requires the
// response to echo the request's value.
const (
	ProtocolHeader = "Smithy-Protocol"
	ProtocolID     = "rpc-v2-cbor"
)

// QueryModeHeader is the awsQueryCompatible opt-in. A client that sends it is
// asking for legacy Query error codes alongside the modern ones.
const QueryModeHeader = "X-Amzn-Query-Mode"

// QueryErrorHeader carries those legacy codes, as `Code;Fault`.
const QueryErrorHeader = "X-Amzn-Query-Error"

// ParsePath pulls the service and operation out of an RPC v2 path. It matches
// the `/service/{s}/operation/{o}` pair anywhere in the path so a routing
// prefix does not defeat it, and reports ok=false for anything else.
//
// The spec forbids a namespaced shape id in the operation segment, so a
// segment containing '#' is refused rather than trimmed: accepting it would
// let two spellings address one operation.
func ParsePath(path string) (service, operation string, ok bool) {
	segs := strings.Split(strings.Trim(path, "/"), "/")
	for i := 0; i+3 < len(segs); i++ {
		if segs[i] != "service" || segs[i+2] != "operation" {
			continue
		}
		service, operation = segs[i+1], segs[i+3]
		if service == "" || operation == "" ||
			strings.Contains(service, "#") || strings.Contains(operation, "#") {
			return "", "", false
		}
		return service, operation, true
	}
	return "", "", false
}

// IsRequest reports whether this looks like an RPC v2 CBOR call. The path is
// the load-bearing signal — it is what makes the request addressable at all —
// and the content type confirms it. The Smithy-Protocol header is checked
// when present but not required, so a hand-rolled client that omits it is
// still understood.
func IsRequest(r *http.Request) bool {
	if _, _, ok := ParsePath(r.URL.Path); !ok {
		return false
	}
	if p := r.Header.Get(ProtocolHeader); p != "" && p != ProtocolID {
		return false
	}
	return strings.HasPrefix(r.Header.Get("Content-Type"), ContentType) ||
		r.Header.Get("Content-Type") == ""
}

// WantsQueryErrors reports whether the caller asked for legacy Query error
// codes. aws-sdk-go-v2 sets this on every CloudWatch request.
func WantsQueryErrors(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get(QueryModeHeader), "true")
}

// Write sends a successful response. A nil result is an empty CBOR map rather
// than an empty body: the operations here return a structure even when it has
// no members, and a client decoding one will not accept nothing.
func Write(w http.ResponseWriter, result any) {
	body := []byte{0xa0}
	if result != nil {
		var err error
		if body, err = Marshal(result); err != nil {
			WriteError(w, awshttp.AsAPIError(err), "", false)
			return
		}
	}
	w.Header().Set("Content-Type", ContentType)
	w.Header().Set(ProtocolHeader, ProtocolID)
	w.Header().Set("x-amzn-RequestId", awshttp.RequestID())
	w.WriteHeader(200)
	_, _ = w.Write(body)
}

// WriteError renders an error.
//
// The body keeps the MODERN shape name in __type, which is what a client on
// this protocol deserialises against. queryCode — the legacy name the
// awsQueryError trait gives the same error — rides in a header instead, and
// only when the caller asked for it. Putting the legacy code in __type would
// break the modern client to serve the old one.
func WriteError(w http.ResponseWriter, e *awshttp.APIError, queryCode string, wantsQuery bool) {
	fault := "Receiver"
	if e.SenderFault {
		fault = "Sender"
	}
	body, err := Marshal(map[string]any{
		"__type":  e.Code,
		"message": e.Message,
	})
	if err != nil {
		// Marshalling two strings cannot fail; if it somehow did, an empty
		// map still carries the status, which is what the spec says a client
		// falls back to.
		body = []byte{0xa0}
	}
	w.Header().Set("Content-Type", ContentType)
	w.Header().Set(ProtocolHeader, ProtocolID)
	w.Header().Set("x-amzn-RequestId", awshttp.RequestID())
	if wantsQuery && queryCode != "" {
		w.Header().Set(QueryErrorHeader, queryCode+";"+fault)
	}
	w.WriteHeader(e.Status)
	_, _ = w.Write(body)
}
