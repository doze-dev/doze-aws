package sqs

import (
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"sort"
)

// kvAttrs is a name→value attribute map that renders as a JSON object but as
// repeated <Attribute><Name/><Value/></Attribute> elements in XML (the SQS
// Query shape). Used for queue attributes and message system attributes.
type kvAttrs map[string]string

func (m kvAttrs) MarshalJSON() ([]byte, error) { return json.Marshal(map[string]string(m)) }

func (m kvAttrs) MarshalXML(e *xml.Encoder, _ xml.StartElement) error {
	type kv struct {
		Name  string
		Value string
	}
	for _, k := range sortedKeys(m) {
		if err := e.EncodeElement(kv{k, m[k]}, xml.StartElement{Name: xml.Name{Local: "Attribute"}}); err != nil {
			return err
		}
	}
	return nil
}

// msgAttrs is the custom message-attribute map: a JSON object of typed values,
// or repeated <MessageAttribute> elements in XML.
type msgAttrs map[string]Attr

func (m msgAttrs) MarshalJSON() ([]byte, error) {
	type jsonAttr struct {
		DataType    string `json:"DataType"`
		StringValue string `json:"StringValue,omitempty"`
		BinaryValue string `json:"BinaryValue,omitempty"`
	}
	out := map[string]jsonAttr{}
	for k, a := range m {
		ja := jsonAttr{DataType: a.DataType, StringValue: a.StringValue}
		if len(a.BinaryValue) > 0 {
			ja.BinaryValue = base64.StdEncoding.EncodeToString(a.BinaryValue)
		}
		out[k] = ja
	}
	return json.Marshal(out)
}

func (m msgAttrs) MarshalXML(e *xml.Encoder, _ xml.StartElement) error {
	type valXML struct {
		DataType    string `xml:"DataType"`
		StringValue string `xml:"StringValue,omitempty"`
		BinaryValue string `xml:"BinaryValue,omitempty"`
	}
	type maXML struct {
		Name  string `xml:"Name"`
		Value valXML `xml:"Value"`
	}
	for _, k := range sortedKeys2(m) {
		a := m[k]
		v := valXML{DataType: a.DataType, StringValue: a.StringValue}
		if len(a.BinaryValue) > 0 {
			v.BinaryValue = base64.StdEncoding.EncodeToString(a.BinaryValue)
		}
		if err := e.EncodeElement(maXML{Name: k, Value: v}, xml.StartElement{Name: xml.Name{Local: "MessageAttribute"}}); err != nil {
			return err
		}
	}
	return nil
}

// msgView is one message in a ReceiveMessage result. JSON uses struct tags; XML
// is hand-rolled to match the SQS Query shape (repeated Attribute /
// MessageAttribute elements).
type msgView struct {
	MessageID              string   `json:"MessageId"`
	ReceiptHandle          string   `json:"ReceiptHandle"`
	MD5OfBody              string   `json:"MD5OfBody"`
	Body                   string   `json:"Body"`
	Attributes             kvAttrs  `json:"Attributes,omitempty"`
	MD5OfMessageAttributes string   `json:"MD5OfMessageAttributes,omitempty"`
	MessageAttributes      msgAttrs `json:"MessageAttributes,omitempty"`
}

func (m msgView) MarshalXML(e *xml.Encoder, start xml.StartElement) error {
	start.Name = xml.Name{Local: "Message"}
	if err := e.EncodeToken(start); err != nil {
		return err
	}
	simple := func(name, val string) error {
		return e.EncodeElement(val, xml.StartElement{Name: xml.Name{Local: name}})
	}
	if err := simple("MessageId", m.MessageID); err != nil {
		return err
	}
	if err := simple("ReceiptHandle", m.ReceiptHandle); err != nil {
		return err
	}
	if err := simple("MD5OfBody", m.MD5OfBody); err != nil {
		return err
	}
	if err := simple("Body", m.Body); err != nil {
		return err
	}
	if len(m.Attributes) > 0 {
		if err := m.Attributes.MarshalXML(e, xml.StartElement{}); err != nil {
			return err
		}
	}
	if m.MD5OfMessageAttributes != "" {
		if err := simple("MD5OfMessageAttributes", m.MD5OfMessageAttributes); err != nil {
			return err
		}
	}
	if len(m.MessageAttributes) > 0 {
		if err := m.MessageAttributes.MarshalXML(e, xml.StartElement{}); err != nil {
			return err
		}
	}
	return e.EncodeToken(start.End())
}

// ---- per-action result structs ----

type queueURLResult struct {
	QueueURL string `json:"QueueUrl" xml:"QueueUrl"`
}

type listQueuesResult struct {
	QueueURLs []string `json:"QueueUrls,omitempty" xml:"QueueUrl"`
}

type getAttrsResult struct {
	Attributes kvAttrs `json:"Attributes,omitempty" xml:"Attribute"`
}

type sendResult struct {
	MessageID  string `json:"MessageId" xml:"MessageId"`
	MD5OfBody  string `json:"MD5OfMessageBody" xml:"MD5OfMessageBody"`
	MD5OfAttrs string `json:"MD5OfMessageAttributes,omitempty" xml:"MD5OfMessageAttributes,omitempty"`
}

type receiveResult struct {
	Messages []msgView `json:"Messages,omitempty" xml:"Message"`
}

// batch entries
type sendBatchOK struct {
	ID         string `json:"Id" xml:"Id"`
	MessageID  string `json:"MessageId" xml:"MessageId"`
	MD5OfBody  string `json:"MD5OfMessageBody" xml:"MD5OfMessageBody"`
	MD5OfAttrs string `json:"MD5OfMessageAttributes,omitempty" xml:"MD5OfMessageAttributes,omitempty"`
}

type batchErr struct {
	ID          string `json:"Id" xml:"Id"`
	Code        string `json:"Code" xml:"Code"`
	Message     string `json:"Message,omitempty" xml:"Message,omitempty"`
	SenderFault bool   `json:"SenderFault" xml:"SenderFault"`
}

type sendBatchResult struct {
	Successful []sendBatchOK `json:"Successful,omitempty" xml:"SendMessageBatchResultEntry"`
	Failed     []batchErr    `json:"Failed,omitempty" xml:"BatchResultErrorEntry"`
}

type deleteBatchOK struct {
	ID string `json:"Id" xml:"Id"`
}

type deleteBatchResult struct {
	Successful []deleteBatchOK `json:"Successful,omitempty" xml:"DeleteMessageBatchResultEntry"`
	Failed     []batchErr      `json:"Failed,omitempty" xml:"BatchResultErrorEntry"`
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedKeys2(m map[string]Attr) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
