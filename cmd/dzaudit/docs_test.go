package main

import (
	"bytes"
	"strings"
	"testing"
)

// The text the first pass of SQS did not read is the text this prints.
func TestDocsPrintsWhatTheModelSaysInProse(t *testing.T) {
	m, err := load("../../.audit-models/sqs.json", "")
	if err != nil {
		t.Skipf("no vendored SQS model: %v", err)
	}
	var out bytes.Buffer
	if err := emitDocs(&out, m, ""); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"== SetQueueAttributes",
		"You can't change it for an existing queue.", // a rule that is only prose
		"1,048,576 bytes (1 MiB)",                    // a limit that is only prose
		"ReceiptHandleIsInvalid · query code ReceiptHandleIsInvalid, status 404 · httpError 404", // an error's code and status
		"xmlName Attribute; xmlFlattened", // a wire name
		"Tags (map; xmlName Tag; xmlFlattened)",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the output lacks %q", want)
		}
	}
	if strings.Contains(out.String(), "<p>") || strings.Contains(out.String(), "&#") {
		t.Error("markup was left in the text")
	}

	out.Reset()
	if err := emitDocs(&out, m, "purgequeue"); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.String(), "== PurgeQueue\n") || strings.Count(out.String(), "\n== ") != 0 {
		t.Errorf("-op did not restrict the output to one operation:\n%.200s", out.String())
	}
	if err := emitDocs(&out, m, "NoSuchOperation"); err == nil {
		t.Error("an operation that is not in the model was not an error")
	}
}
