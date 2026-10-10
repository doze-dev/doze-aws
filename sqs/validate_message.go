package sqs

// What AWS states about a message's own content, which SQS's service model
// does not: the characters a body may hold, the size that counts the
// attributes, the shape of an attribute, and the identifiers FIFO adds.
//
// doc: the SQS developer guide, "Amazon SQS message attributes" and "Amazon SQS
// quotas"; the SendMessage and SendMessageBatch API reference pages (the body's
// allowed characters, MessageGroupId, MessageDeduplicationId, DelaySeconds on a
// FIFO queue).

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	maxMessageAttributes = 10
	maxAttrNameLen       = 256
	maxFIFOIDLen         = 128
	maxBatchBytes        = 1048576 // the sum of a batch's message sizes (doc: SendMessageBatch)
)

// validMessageRune is the character set of a body and of a String attribute's
// value: #x9 | #xA | #xD | #x20 to #xD7FF | #xE000 to #xFFFD | #x10000 to #x10FFFF.
func validMessageRune(r rune) bool {
	switch {
	case r == 0x9 || r == 0xA || r == 0xD:
		return true
	case r >= 0x20 && r <= 0xD7FF:
		return true
	case r >= 0xE000 && r <= 0xFFFD:
		return true
	case r >= 0x10000 && r <= 0x10FFFF:
		return true
	}
	return false
}

func validMessageText(s string) bool {
	if !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if !validMessageRune(r) {
			return false
		}
	}
	return true
}

func validateBody(body string) *apiError {
	if body == "" {
		return errInvalid("The request must contain the parameter MessageBody.")
	}
	if !validMessageText(body) {
		// doc: "rejects the message and returns an InvalidMessageContents error".
		return &apiError{Code: "InvalidMessageContents", Status: 400, SenderFault: true,
			Message: "Invalid characters found. Valid unicode characters are #x9 | #xA | #xD | #x20 to #xD7FF | #xE000 to #xFFFD | #x10000 to #x10FFFF"}
	}
	return nil
}

// messageSize is what counts against a queue's MaximumMessageSize: the body,
// and for every attribute its name, its data type and its value.
func messageSize(body string, attrs map[string]attr) int {
	n := len(body)
	for name, a := range attrs {
		n += len(name) + len(a.DataType) + len(a.StringValue) + len(a.BinaryValue)
	}
	return n
}

func validateMessageAttrs(attrs map[string]attr) *apiError {
	if len(attrs) > maxMessageAttributes {
		return errInvalid("Number of message attributes [" + strconv.Itoa(len(attrs)) + "] exceeds the allowed maximum [10].")
	}
	for name, a := range attrs {
		if aerr := validateAttrName(name); aerr != nil {
			return aerr
		}
		base, custom, hasCustom := strings.Cut(a.DataType, ".")
		switch base {
		case "String", "Number", "Binary":
		default:
			return errInvalid("The message attribute '" + name + "' has an invalid message attribute type, the set of supported type prefixes is Binary, Number, and String.")
		}
		if hasCustom && custom == "" || len(a.DataType) > maxAttrNameLen {
			return errInvalid("The message attribute '" + name + "' has an invalid custom data type suffix.")
		}
		switch base {
		case "String":
			if a.StringValue == "" {
				return errInvalid("Message attribute '" + name + "' must contain a non-empty value of type 'String'.")
			}
			if !validMessageText(a.StringValue) {
				return errInvalid("Invalid non-alphanumeric character was found in the message attribute '" + name + "' value.")
			}
		case "Number":
			v, err := strconv.ParseFloat(a.StringValue, 64)
			if err != nil || v != v || v > 1e126 || v < -1e128 {
				return errInvalid("Could not parse message attribute '" + name + "' value as a number.")
			}
		case "Binary":
			if len(a.BinaryValue) == 0 {
				return errInvalid("Message attribute '" + name + "' must contain a non-empty value of type 'Binary'.")
			}
		}
	}
	return nil
}

func validateAttrName(name string) *apiError {
	lower := strings.ToLower(name)
	switch {
	case name == "" || len(name) > maxAttrNameLen:
		return errInvalid("Invalid message attribute name '" + name + "': a name is 1 to 256 characters.")
	case strings.HasPrefix(lower, "aws.") || strings.HasPrefix(lower, "amazon."):
		return errInvalid("You can't use message attribute names beginning with 'AWS.' or 'Amazon.'. These strings are reserved for internal use.")
	case strings.HasPrefix(name, ".") || strings.HasSuffix(name, ".") || strings.Contains(name, ".."):
		return errInvalid("Invalid message attribute name '" + name + "': it cannot start or end with a period or hold two in a row.")
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' || r == '.') {
			return errInvalid("Invalid non-alphanumeric character was found in the message attribute name '" + name + "'. Only alphanumeric characters, hyphens, underscores, and periods are allowed.")
		}
	}
	return nil
}

// validFIFOID is a MessageGroupId or MessageDeduplicationId: 1 to 128
// alphanumeric characters and punctuation (!"#$%&'()*+,-./:;<=>?@[\]^_`{|}~).
func validFIFOID(s string) bool {
	if s == "" || len(s) > maxFIFOIDLen {
		return false
	}
	for _, r := range s {
		if r > 0x7E || r < 0x21 { // printable ASCII without the space
			return false
		}
	}
	return true
}
