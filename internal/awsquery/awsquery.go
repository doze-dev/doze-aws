// Package awsquery implements the AWS Query protocol: form-encoded requests
// carrying an Action parameter, XML responses in a per-action envelope. STS and
// SNS speak it exclusively; SQS speaks it for legacy (pre-2023 SDK) clients.
//
// The response envelope is uniform across Query services:
//
//	<{Action}Response xmlns="...">
//	  <{Action}Result> ... </{Action}Result>      (omitted for result-less actions)
//	  <ResponseMetadata><RequestId>..</RequestId></ResponseMetadata>
//	</{Action}Response>
//
// and so is the error envelope:
//
//	<ErrorResponse xmlns="...">
//	  <Error><Type>Sender</Type><Code>..</Code><Message>..</Message></Error>
//	  <RequestId>..</RequestId>
//	</ErrorResponse>
package awsquery

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"strconv"

	"github.com/doze-dev/doze-aws/internal/awshttp"
)

// maxFormBytes bounds how much of a request body Params will read. Query
// requests are metadata-sized; anything bigger is malformed or hostile.
const maxFormBytes = 10 << 20

// Params merges the URL query and the form-encoded body into one set of
// parameters, the way AWS Query services accept them (GET with query params or
// POST with an x-www-form-urlencoded body).
func Params(r *http.Request) (url.Values, error) {
	vals := url.Values{}
	maps.Copy(vals, r.URL.Query())
	if r.Body != nil && r.Method == http.MethodPost {
		r.Body = http.MaxBytesReader(nil, r.Body, maxFormBytes)
		if err := r.ParseForm(); err != nil {
			return nil, awshttp.Errf(400, "InvalidParameterValue", "malformed form body: %v", err)
		}
		for k, vs := range r.PostForm {
			vals[k] = append(vals[k], vs...)
		}
	}
	return vals, nil
}

// Members collects the values of a numbered-list parameter in order:
// prefix.member.1, prefix.member.2, ... (the `member` level is what most Query
// APIs use; pass memberless=true for APIs that number directly: prefix.1, ...).
func Members(vals url.Values, prefix string, memberless bool) []string {
	var out []string
	for i := 1; ; i++ {
		var key string
		if memberless {
			key = prefix + "." + strconv.Itoa(i)
		} else {
			key = prefix + ".member." + strconv.Itoa(i)
		}
		v, ok := vals[key]
		if !ok {
			return out
		}
		out = append(out, v[0])
	}
}

// API renders responses for one Query service (its xmlns is per-service).
type API struct {
	XMLNS string
}

// WriteResult writes the standard success envelope. result must be a struct
// whose fields marshal into the {Action}Result element's children; pass nil
// for actions that have no result element.
func (a API) WriteResult(w http.ResponseWriter, action string, result any) {
	reqID := awshttp.RequestID()
	body, err := a.renderResult(action, result, reqID)
	if err != nil {
		a.WriteError(w, awshttp.AsAPIError(fmt.Errorf("marshal %s result: %w", action, err)))
		return
	}
	w.Header().Set("Content-Type", "text/xml")
	w.Header().Set("x-amzn-RequestId", reqID)
	w.WriteHeader(http.StatusOK)
	w.Write(body)
}

func (a API) renderResult(action string, result any, reqID string) ([]byte, error) {
	buf := []byte(xml.Header)
	buf = fmt.Appendf(buf, "<%sResponse xmlns=%q>", action, a.XMLNS)
	if result != nil {
		// Encode the struct AS the {Action}Result element (replacing its Go
		// type name), so its fields land exactly where the SDKs look.
		var inner bytes.Buffer
		enc := xml.NewEncoder(&inner)
		enc.Indent("  ", "  ")
		if err := enc.EncodeElement(result, xml.StartElement{Name: xml.Name{Local: action + "Result"}}); err != nil {
			return nil, err
		}
		if err := enc.Close(); err != nil {
			return nil, err
		}
		buf = fmt.Appendf(buf, "\n%s", inner.Bytes())
	}
	buf = fmt.Appendf(buf, "\n  <ResponseMetadata><RequestId>%s</RequestId></ResponseMetadata>\n</%sResponse>\n", reqID, action)
	return buf, nil
}

// WriteError writes the standard Query error envelope with e's HTTP status.
func (a API) WriteError(w http.ResponseWriter, e *awshttp.APIError) {
	fault := "Receiver"
	if e.SenderFault {
		fault = "Sender"
	}
	reqID := awshttp.RequestID()
	w.Header().Set("Content-Type", "text/xml")
	w.Header().Set("x-amzn-RequestId", reqID)
	w.WriteHeader(e.Status)
	fmt.Fprintf(w, "%s<ErrorResponse xmlns=%q>\n  <Error>\n    <Type>%s</Type>\n    <Code>%s</Code>\n    <Message>%s</Message>\n  </Error>\n  <RequestId>%s</RequestId>\n</ErrorResponse>\n",
		xml.Header, a.XMLNS, fault, xmlEscape(e.Code), xmlEscape(e.Message), reqID)
}

func xmlEscape(s string) string {
	var buf []byte
	if err := xml.EscapeText(&byteWriter{&buf}, []byte(s)); err != nil {
		return ""
	}
	return string(buf)
}

type byteWriter struct{ b *[]byte }

func (w *byteWriter) Write(p []byte) (int, error) {
	*w.b = append(*w.b, p...)
	return len(p), nil
}
