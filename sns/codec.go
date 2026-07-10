package sns

import (
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/url"
)

const snsXMLNS = "http://sns.amazonaws.com/doc/2010-03-31/"

// Attr is an SNS message attribute.
type Attr struct {
	DataType    string
	StringValue string
	BinaryValue []byte
}

// messageAttributes parses Publish MessageAttributes.entry.N.* form params.
func messageAttributes(form url.Values) map[string]Attr {
	out := map[string]Attr{}
	for i := 1; ; i++ {
		base := fmt.Sprintf("MessageAttributes.entry.%d.", i)
		name := form.Get(base + "Name")
		if name == "" {
			break
		}
		a := Attr{
			DataType:    form.Get(base + "Value.DataType"),
			StringValue: form.Get(base + "Value.StringValue"),
		}
		if bv := form.Get(base + "Value.BinaryValue"); bv != "" {
			a.BinaryValue, _ = base64.StdEncoding.DecodeString(bv)
		}
		out[name] = a
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// subscribeAttributes parses Subscribe Attributes.entry.N.key/value form params.
func subscribeAttributes(form url.Values) map[string]string {
	out := map[string]string{}
	for i := 1; ; i++ {
		base := fmt.Sprintf("Attributes.entry.%d.", i)
		k := form.Get(base + "key")
		if k == "" {
			break
		}
		out[k] = form.Get(base + "value")
	}
	return out
}

type respMeta struct {
	RequestID string `xml:"RequestId"`
}

// writeResult renders an SNS Query/XML response. result may be nil — real SNS
// still emits an empty {Action}Result element, and some SDK deserializers
// (e.g. TagResource in aws-sdk-go-v2) require the node to be present.
func writeResult(w http.ResponseWriter, action string, result any) {
	w.Header().Set("Content-Type", "text/xml")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(xml.Header))
	enc := xml.NewEncoder(w)
	resp := xml.StartElement{
		Name: xml.Name{Local: action + "Response"},
		Attr: []xml.Attr{{Name: xml.Name{Local: "xmlns"}, Value: snsXMLNS}},
	}
	_ = enc.EncodeToken(resp)
	resultEl := xml.StartElement{Name: xml.Name{Local: action + "Result"}}
	if result != nil {
		_ = enc.EncodeElement(result, resultEl)
	} else {
		_ = enc.EncodeToken(resultEl)
		_ = enc.EncodeToken(resultEl.End())
	}
	_ = enc.EncodeElement(respMeta{RequestID: newID()}, xml.StartElement{Name: xml.Name{Local: "ResponseMetadata"}})
	_ = enc.EncodeToken(resp.End())
	_ = enc.Flush()
}

func writeError(w http.ResponseWriter, err *apiError) {
	w.Header().Set("Content-Type", "text/xml")
	w.WriteHeader(err.Status)
	_, _ = w.Write([]byte(xml.Header))
	type xmlErr struct {
		XMLName   xml.Name `xml:"ErrorResponse"`
		Type      string   `xml:"Error>Type"`
		Code      string   `xml:"Error>Code"`
		Message   string   `xml:"Error>Message"`
		RequestID string   `xml:"RequestId"`
	}
	_ = xml.NewEncoder(w).Encode(xmlErr{Type: "Sender", Code: err.Code, Message: err.Msg, RequestID: newID()})
}
