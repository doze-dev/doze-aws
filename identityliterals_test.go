package dozeaws_test

// No identity is written into code.
//
// An instance has an account and a region (--account-id, --region) and every
// ARN it mints, every URL it hands back and every principal it names is meant
// to follow them. A literal "000000000000" or "us-east-1" outside awsident is
// a place that does not, and it does not look wrong: under the defaults it is
// exactly right. It was found only when an instance was given another account
// and a KMS key came out locked against its own root, an EventBridge archive
// captured nothing, and a function saw "us-east-1" in its SQS event.
//
// The check is the cheapest one that finds them: read the source. The sites
// that exist today are frozen in testdata/identity_literals.txt, both ways, so
// a new one fails and a fixed one has to be removed from the list. The list is
// a burn-down and each entry belongs to the service named by its path; it ends
// empty. A site that is a comment, a test, a fixture or the default itself in
// awsident is not code that mints, and is not counted.

import (
	"bufio"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

var identityLiterals = []string{"000000000000", `"us-east-1"`}

// notIdentity are files where the digits are something other than an account.
// Each says what, so an entry is a claim somebody can check.
var notIdentity = map[string]string{
	"cmd/doze-aws/main.go":           "the flag's help text names the default it documents",
	"dynamodb/streams.go":            "the shard id AWS gives a DynamoDB stream shard: twenty zeros then eight",
	"internal/peercall/peercall.go":  "the same shard id, as the peer name for it",
	"internal/lambdaruntime/util.go": "the all-zero UUID returned when the system cannot make a random one",
}

func TestNoIdentityIsWrittenIntoCode(t *testing.T) {
	skipDirs := map[string]bool{
		".git": true, ".claude": true, "node_modules": true, "worktrees": true, "testdata": true,
		"e2e": true, "conformance": true, "demo": true, "plan": true, "docs": true,
		"awsident": true, "scripts": true, ".audit-models": true, "data": true,
	}
	found := map[string]int{}
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDirs[d.Name()] && path != "." {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			line := sc.Text()
			trim := strings.TrimSpace(line)
			if strings.HasPrefix(trim, "//") {
				continue
			}
			code := line
			if i := strings.Index(code, " //"); i >= 0 {
				code = code[:i]
			}
			for _, lit := range identityLiterals {
				if _, ok := notIdentity[filepath.ToSlash(path)]; ok {
					continue
				}
				if strings.Contains(code, lit) {
					found[filepath.ToSlash(path)+" "+lit]++
				}
			}
		}
		return sc.Err()
	})
	if err != nil {
		t.Fatal(err)
	}

	const list = "testdata/identity_literals.txt"
	if os.Getenv("IDENTITY_LITERALS_UPDATE") != "" {
		var lines []string
		for k, n := range found {
			lines = append(lines, k+" "+strconv.Itoa(n))
		}
		sort.Strings(lines)
		if err := os.WriteFile(list, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("rewrote %s with %d sites", list, len(lines))
		return
	}
	raw, err := os.ReadFile(list)
	if err != nil {
		t.Fatalf("%v\n  IDENTITY_LITERALS_UPDATE=1 go test . -run TestNoIdentityIsWrittenIntoCode", err)
	}
	frozen := map[string]int{}
	for _, ln := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if ln == "" {
			continue
		}
		i := strings.LastIndex(ln, " ")
		n, _ := strconv.Atoi(ln[i+1:])
		frozen[ln[:i]] = n
	}
	for k, n := range found {
		switch was, ok := frozen[k]; {
		case !ok:
			t.Errorf("%s: a new identity literal in code (%d). Mint it from the instance's awsident.Identity", k, n)
		case n > was:
			t.Errorf("%s: %d literals, was %d. Mint it from the instance's awsident.Identity", k, n, was)
		case n < was:
			t.Errorf("%s: %d literals, was %d. Good. Record it: IDENTITY_LITERALS_UPDATE=1", k, n, was)
		}
	}
	for k := range frozen {
		if _, ok := found[k]; !ok {
			t.Errorf("%s: gone. Good. Record it: IDENTITY_LITERALS_UPDATE=1", k)
		}
	}
}
