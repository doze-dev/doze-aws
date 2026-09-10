package cloudwatch

// The CloudWatch wire codec: one decoded request over the THREE protocols
// CloudWatch speaks.
//
// Every other service here speaks one wire, or two in SQS's case. CloudWatch
// is mid-migration off Query, and the protocol a caller uses is fixed at SDK
// codegen time per language with no negotiation and no fallback:
//
//	rpcv2Cbor    Go v2, Java, Rust, Swift, Kotlin, C++, .NET v4
//	awsJson1_0   the AWS CLI (v1 and v2), boto3, JavaScript v3, PHP, Ruby, PowerShell
//	awsQuery     any SDK pinned below the versions in AWS's protocol FAQ
//
// So all three are served. The CLI alone would justify JSON, and a
// Query-only CloudWatch — which is what the original plan called for — would
// have worked for almost nobody.
//
// Dispatch is by shape, in the order that makes each unambiguous: an RPC v2
// path, then a JSON target header, then Query's Action parameter.

import (
	"net/http"

	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/awsjson"
	"github.com/doze-dev/doze-aws/internal/awsquery"
	"github.com/doze-dev/doze-aws/internal/modelcheck"
	"github.com/doze-dev/doze-aws/internal/rpcv2cbor"
)

// The service's identity on the wire. "Granite" was CloudWatch's internal
// name and it is still what the protocol addresses: it is the RPC v2 path
// segment and the JSON target prefix, so it is spelled once here.
const (
	serviceName = "GraniteServiceVersion20100801"
	apiVersion  = "2010-08-01"
	xmlNS       = "http://monitoring.amazonaws.com/doc/2010-08-01/"
)

// wire is which protocol a request arrived on.
type wire int

const (
	wireCBOR wire = iota
	wireJSON
	wireQuery
)

func (w wire) String() string {
	switch w {
	case wireCBOR:
		return "rpcv2Cbor"
	case wireJSON:
		return "awsJson1_0"
	}
	return "awsQuery"
}

// qapi and japi render the Query and JSON response envelopes. CBOR's is in
// internal/rpcv2cbor, which owns its own framing.
var (
	qapi = awsquery.API{XMLNS: xmlNS}
	japi = awsjson.API{TargetPrefix: serviceName, JSONVersion: "1.0"}
)

// request is one decoded call. Handlers never learn which wire carried it —
// that is the whole point of normalising into params.
type request struct {
	action     string
	wire       wire
	params     params
	wantsQuery bool // the caller asked for legacy Query error codes
}

// parseRequest decodes whichever protocol this is.
func parseRequest(r *http.Request) (*request, *awshttp.APIError) {
	switch {
	case rpcv2cbor.IsRequest(r):
		return parseCBOR(r)
	case r.Header.Get("X-Amz-Target") != "":
		return parseJSON(r)
	default:
		return parseQuery(r)
	}
}

// maxBody bounds a compressed or uncompressed request. rpcv2cbor bounds what
// a gzip stream may expand to separately.
const maxBody = 8 << 20

func parseCBOR(r *http.Request) (*request, *awshttp.APIError) {
	_, action, ok := rpcv2cbor.ParsePath(r.URL.Path)
	if !ok {
		return nil, awshttp.Errf(404, "UnknownOperationException", "not an rpc-v2-cbor path")
	}
	body, aerr := readBody(r)
	if aerr != nil {
		return nil, aerr
	}
	obj, err := rpcv2cbor.DecodeMap(body)
	if err != nil {
		return nil, awshttp.Errf(400, "SerializationException", "%v", err)
	}
	return &request{action: action, wire: wireCBOR, params: params(obj),
		wantsQuery: rpcv2cbor.WantsQueryErrors(r)}, nil
}

func parseJSON(r *http.Request) (*request, *awshttp.APIError) {
	action, aerr := japi.Action(r)
	if aerr != nil {
		return nil, aerr
	}
	body, aerr := readBody(r)
	if aerr != nil {
		return nil, aerr
	}
	// The same gunzip the CBOR path uses: requestCompression is a property of
	// the operation, so a JSON client may compress too even though the one
	// observed (JavaScript v3) does not.
	raw, err := rpcv2cbor.Gunzip(body)
	if err != nil {
		return nil, awshttp.Errf(400, "SerializationException", "%v", err)
	}
	obj, aerr := decodeJSONBody(raw)
	if aerr != nil {
		return nil, aerr
	}
	return &request{action: action, wire: wireJSON, params: obj,
		wantsQuery: rpcv2cbor.WantsQueryErrors(r)}, nil
}

func parseQuery(r *http.Request) (*request, *awshttp.APIError) {
	vals, err := awsquery.Params(r)
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	action := vals.Get("Action")
	if action == "" {
		return nil, awshttp.Errf(400, "MissingAction", "no Action specified")
	}
	// awsquery.Unflatten, not modelcheck.FromQuery: FromQuery keeps only the
	// first element of every list, which is sound for validation and lossy
	// for a handler. A PutMetricData with two dimensions reached one with a
	// single dimension mixed out of both until this was split apart, and the
	// v1 SDK contract test is what caught it.
	return &request{action: action, wire: wireQuery,
		params: params(awsquery.Unflatten(vals))}, nil
}

func readBody(r *http.Request) ([]byte, *awshttp.APIError) {
	if r.Body == nil {
		return nil, nil
	}
	defer r.Body.Close()
	raw, err := readAll(http.MaxBytesReader(nil, r.Body, maxBody))
	if err != nil {
		return nil, awshttp.Errf(400, "SerializationException", "reading the request body: %v", err)
	}
	return raw, nil
}

// validate runs the model-derived constraint table. One table serves all
// three wires, because all three have already been normalised to the shape
// its paths describe. The error CODE differs: Query services answer
// ValidationError where the JSON family answers ValidationException.
func (req *request) validate() *awshttp.APIError {
	code := modelcheck.CodeJSON
	if req.wire == wireQuery {
		code = modelcheck.CodeQuery
	}
	return modelcheck.ValidateMapAs(req.params, constraintTables[req.action], code)
}

// writeResult renders a handler's return value on the wire it came in on.
func (req *request) writeResult(w http.ResponseWriter, result any) {
	switch req.wire {
	case wireCBOR:
		rpcv2cbor.Write(w, result)
	case wireJSON:
		japi.Write(w, result)
	default:
		qapi.WriteResult(w, req.action, result)
	}
}

// writeError renders an error, which is the one place the three wires
// genuinely disagree rather than merely differing in encoding.
//
// Query answers with the LEGACY code in <Code>, because that is what an
// old client deserialises against and what the awsQueryError trait exists to
// preserve. CBOR and JSON keep the MODERN shape name and carry the legacy one
// in x-amzn-query-error, and only when the caller set x-amzn-query-mode.
func (req *request) writeError(w http.ResponseWriter, e *awshttp.APIError) {
	legacy := legacyCode(e.Code)
	switch req.wire {
	case wireCBOR:
		rpcv2cbor.WriteError(w, e, legacy, req.wantsQuery)
	case wireJSON:
		if req.wantsQuery && legacy != "" {
			fault := "Receiver"
			if e.SenderFault {
				fault = "Sender"
			}
			w.Header().Set(rpcv2cbor.QueryErrorHeader, legacy+";"+fault)
		}
		japi.WriteError(w, e)
	default:
		q := *e
		if legacy != "" {
			q.Code = legacy
		}
		qapi.WriteError(w, &q)
	}
}

// writeParseError answers before a request was understood well enough to know
// its wire. The shape is guessed from what is on the request, which is the
// best available: a malformed request has no reliable protocol.
func writeParseError(w http.ResponseWriter, r *http.Request, e *awshttp.APIError) {
	req := &request{wire: wireQuery, wantsQuery: rpcv2cbor.WantsQueryErrors(r)}
	switch {
	case rpcv2cbor.IsRequest(r):
		req.wire = wireCBOR
	case r.Header.Get("X-Amz-Target") != "":
		req.wire = wireJSON
	}
	req.writeError(w, e)
}
