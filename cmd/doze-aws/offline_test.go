package main

// The binary makes no outbound connections of its own.
//
// docs/not-built.md tells a security reviewer that doze-aws does not phone
// home, check for updates or report usage. That is true, it is the strongest
// trust signal this project has, and until now it was a sentence.
//
// It is the kind of claim that stops being true in one line: a version check
// "just to warn about updates", an error reporter, a docs fetch. None of those
// looks wrong in review, and all of them break the promise.
//
// What this does NOT cover, said plainly: it reads the command layer and the
// config package, which is where such a thing would land. It cannot prove the
// whole binary makes no connections — a service could, and several legitimately
// do reach their own siblings in-process. The claim is about doze-aws itself
// reaching out, and this is the surface where that would be written.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// outbound matches a URL to somewhere that is not this machine. Loopback and
// the .doze zone are the addresses doze-aws serves itself on.
var outbound = regexp.MustCompile(`https?://[a-zA-Z0-9][-a-zA-Z0-9.]*\.[a-zA-Z]{2,}`)

// allowed are the mentions that are not an outbound call: documentation URLs
// printed for a person to read, and the AWS hostnames the docs compare against.
var allowed = map[string]bool{
	"https://github.com":                   true, // the docs link in --help
	"https://raw.githubusercontent.com":    true, // the install one-liner, quoted in comments
	"https://sqs.ap-south-1.amazonaws.com": true, // the AWS-vs-doze URL comparison
}

func TestTheBinaryReachesNothingOnTheInternet(t *testing.T) {
	dirs := []string{".", filepath.Join("..", "..", "internal", "config")}
	for _, dir := range dirs {
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		if len(files) == 0 {
			// An empty glob would make this pass by finding nothing, which is
			// the failure mode three ratchets in this repo already had.
			t.Fatalf("no Go files under %s — this test would pass vacuously", dir)
		}
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			b, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			// The pattern stops at the host, so the match IS the host — the
			// first version sliced it further and mangled every http:// URL,
			// because it trimmed "https://" and then added that length back.
			for _, host := range outbound.FindAllString(string(b), -1) {
				// .doze is the zone doze-aws serves ITSELF on, so a URL under
				// it is this machine talking to this machine — the opposite of
				// what is being looked for here.
				if allowed[host] || strings.HasSuffix(host, ".doze") {
					continue
				}
				t.Errorf("%s reaches %s.\n"+
					"  docs/not-built.md promises a reviewer that doze-aws makes no outbound\n"+
					"  connections of its own. If this is a URL printed for a person to read,\n"+
					"  add it to `allowed`. If it is something the binary FETCHES, the promise\n"+
					"  has to change before the code does.", f, host)
			}
		}
	}
}
