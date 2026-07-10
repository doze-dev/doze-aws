package s3

// REST-XML response rendering and shared shapes. S3's XML uses the 2006-03-01
// namespace on top-level response documents; errors use a bare <Error> root.

import (
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/awshttp"
)

const s3NS = "http://s3.amazonaws.com/doc/2006-03-01/"

// writeXML renders an XML document with the standard header and request id.
func writeXML(w http.ResponseWriter, status int, doc any) {
	w.Header().Set("Content-Type", "application/xml")
	w.Header().Set("x-amz-request-id", awshttp.RequestID())
	w.WriteHeader(status)
	io.WriteString(w, xml.Header)
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	_ = enc.Encode(doc)
	_ = enc.Close()
	io.WriteString(w, "\n")
}

// writeS3Error renders S3's error document (root <Error>, no namespace).
func writeS3Error(w http.ResponseWriter, e *awshttp.APIError) {
	type errDoc struct {
		XMLName   xml.Name `xml:"Error"`
		Code      string   `xml:"Code"`
		Message   string   `xml:"Message"`
		RequestID string   `xml:"RequestId"`
	}
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(e.Status)
	io.WriteString(w, xml.Header)
	_ = xml.NewEncoder(w).Encode(errDoc{Code: e.Code, Message: e.Message, RequestID: awshttp.RequestID()})
	io.WriteString(w, "\n")
}

// iso8601 renders the timestamp format S3 list responses use.
func iso8601(unix int64) string {
	return time.Unix(unix, 0).UTC().Format("2006-01-02T15:04:05.000Z")
}

// quoteETag wraps an ETag value in the quotes the wire format requires.
func quoteETag(etag string) string { return `"` + etag + `"` }

// owner is the fixed local bucket/object owner.
type owner struct {
	ID          string `xml:"ID"`
	DisplayName string `xml:"DisplayName"`
}

func localOwner() owner {
	return owner{ID: "doze" + awsident.AccountID, DisplayName: awsident.AccessKeyID}
}

// readBodyXML decodes a request's XML body into dst with a size guard.
func readBodyXML(r *http.Request, dst any) *awshttp.APIError {
	body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		return awshttp.Errf(400, "IncompleteBody", "reading request body: %v", err)
	}
	if err := xml.Unmarshal(body, dst); err != nil {
		return awshttp.Errf(400, "MalformedXML", "request body is not the expected XML: %v", err)
	}
	return nil
}

// readBodyString reads a bounded raw request body (policy documents, configs).
func readBodyString(r *http.Request) (string, *awshttp.APIError) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		return "", awshttp.Errf(400, "IncompleteBody", "reading request body: %v", err)
	}
	return string(body), nil
}

func fmtInt(n int64) string { return fmt.Sprintf("%d", n) }
