package provision

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"io"
	"strings"
	"testing"
)

// Code.ZipFile is the handler's source; CloudFormation writes it to index.py or
// index.js and zips it, which is why an inline function's handler is always
// index.handler.
func TestInlineCodeIsPackagedAsIndex(t *testing.T) {
	for runtime, file := range map[string]string{"python3.12": "index.py", "nodejs22.x": "index.js"} {
		block, err := inlineCode("fn", runtime, "SOURCE")
		if err != nil {
			t.Fatalf("%s: %v", runtime, err)
		}
		raw, err := base64.StdEncoding.DecodeString(block["ZipFile"])
		if err != nil {
			t.Fatal(err)
		}
		zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
		if err != nil || len(zr.File) != 1 || zr.File[0].Name != file {
			t.Fatalf("%s: zip = %v %v, want one file %s", runtime, zr, err, file)
		}
		rc, _ := zr.File[0].Open()
		body, _ := io.ReadAll(rc)
		if string(body) != "SOURCE" {
			t.Errorf("%s: contents = %q", runtime, body)
		}
	}
	if _, err := inlineCode("fn", "ruby3.3", "x"); err == nil || !strings.Contains(err.Error(), "Node.js and Python") {
		t.Errorf("a Ruby inline function: err = %v, want a refusal naming the runtimes", err)
	}
}
