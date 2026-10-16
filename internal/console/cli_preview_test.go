package console

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// A form's "cli" preview is the command the form is about to make. It is held
// to two things, both mechanical:
//
//  1. every $field it names is an input the form has, so a rename cannot leave
//     a preview rendering an empty argument; and
//  2. every input the form has is either in the preview or listed below with
//     the reason it is not.
//
// The second is the one that was failing. DynamoDB's create form took indexes
// and a TTL attribute and previewed neither, so running what the preview showed
// made a different table. The omissions that remain are named, so an input
// added to a form without a matching preview fails here and does not sit
// quietly beside a preview that is wrong about it.
//
// The key is the template file and the preview's first command.
var cliOmits = map[string]map[string]string{
	// A selector that chooses which of two previews is shown is not an argument.
	"create.html:apigateway create-rest-api": {
		"protocol": "selects the REST or the HTTP preview",
	},
	"create.html:cloudformation create-stack": {
		"review": "selects the create-stack or the create-change-set preview",
	},
	"iam.html:sts assume-role": {
		"mode": "selects which of the six previews is shown",
	},
	// Rows the CLI takes as one document a flat template cannot build.
	"sns.html:sns publish": {
		"attrs": "repeating rows: --message-attributes takes one JSON document",
		"count": "repeats the publish n times",
	},
	// Console conveniences that repeat or switch the call.
	"kinesis.html:kinesis put-record": {
		"count": "above 1 the form calls PutRecords, which is a different command",
	},
	"sfn_more.html:stepfunctions test-state": {
		"mock":        "TestState's mocked-integration member is not modelled: with a mock set this is the unmocked call (how the AWS CLI spells the mock is not established here)",
		"mock_result": "see mock",
		"mock_error":  "see mock",
		"mock_cause":  "see mock",
	},
}

var (
	// "cli" is the toggle; "cli_panel" is the same command drawn as the create
	// page's panel (D4). Both are held to their form.
	cliCall      = regexp.MustCompile(`\{\{template "cli(?:_panel)?" (?:\(printf )?"((?:[^"\\]|\\.)*)"`)
	cliField     = regexp.MustCompile(`\$([A-Za-z_][\w-]*)`)
	formSpan     = regexp.MustCompile(`(?s)<form\b.*?</form>`)
	builderField = regexp.MustCompile(`"Field" "([A-Za-z_][\w-]*)"`)
	inputRef     = regexp.MustCompile(`<(?:input|select|textarea)\b[^>]*\bname="([A-Za-z_][\w-]*)"[^>]*>`)
)

func TestEveryCLIPreviewMatchesItsForm(t *testing.T) {
	files, err := filepath.Glob("templates/*.html")
	if err != nil || len(files) == 0 {
		t.Fatalf("no templates read: %v", err)
	}
	used := map[string]bool{}
	texts := map[string]string{}
	var all strings.Builder
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		texts[f] = string(raw)
		all.Write(raw)
	}
	anywhere := func(n string) bool {
		return strings.Contains(all.String(), `name="`+n+`"`) || strings.Contains(all.String(), `"Field" "`+n+`"`)
	}
	// A form's inputs include those of the templates it includes: the SQS
	// composer's message attributes are an attr_editor, and reading only the
	// form's own text left them out of the check altogether.
	//
	// Each define's body runs to the next define. (The regexp that read these
	// used the next `{{define "` as its terminator, which consumed it, so the
	// define after every define was never seen: adding one template to a file
	// moved which includes were expanded, and sns's `attrs` stopped being found.)
	defined := map[string]string{}
	src := all.String()
	starts := regexp.MustCompile(`\{\{define "([\w-]+)"\}\}`).FindAllStringSubmatchIndex(src, -1)
	for i, m := range starts {
		end := len(src)
		if i+1 < len(starts) {
			end = starts[i+1][0]
		}
		defined[src[m[2]:m[3]]] = src[m[1]:end]
	}
	include := regexp.MustCompile(`\{\{template "([\w-]+)"`)
	expand := func(span string) string {
		out := span
		for _, m := range include.FindAllStringSubmatch(span, -1) {
			if m[1] != "cli" {
				out += defined[m[1]]
			}
		}
		return out
	}
	for _, f := range files {
		text := texts[f]
		base := filepath.Base(f)

		for _, raw := range formSpan.FindAllString(text, -1) {
			calls := cliCall.FindAllStringSubmatch(raw, -1)
			if len(calls) == 0 {
				continue
			}
			span := expand(raw)
			// 1. Every $field a preview names is an input of THIS form. A form
			//    that includes controls from elsewhere (hx-include) may name
			//    theirs. Looking across every template let a preview name a
			//    field that only another branch, or another page, has.
			includes := strings.Contains(raw[:strings.Index(raw, ">")], "hx-include")
			for _, c := range calls {
				for _, fm := range cliField.FindAllStringSubmatch(c[1], -1) {
					n := fm[1]
					here := strings.Contains(span, `name="`+n+`"`) || strings.Contains(span, `"Field" "`+n+`"`)
					if !here && !(includes && anywhere(n)) {
						t.Errorf("%s: the preview %q names $%s, which this form has no input for", base, firstCommand(c[1]), n)
					}
				}
			}

			// 2. Every input of a form with a preview is in it, or is omitted on purpose.
			referenced := map[string]bool{}
			for _, c := range calls {
				for _, fm := range cliField.FindAllStringSubmatch(c[1], -1) {
					referenced[fm[1]] = true
				}
			}
			names := map[string]bool{}
			for _, m := range inputRef.FindAllStringSubmatch(span, -1) {
				// A hidden input with a fixed value is plumbing. One BOUND to
				// the page's state (:value, x-model) carries what the user
				// entered, like the message-attribute rows, and counts.
				if strings.Contains(m[0], `type="hidden"`) && !strings.Contains(m[0], `:value=`) && !strings.Contains(m[0], "x-model") {
					continue
				}
				names[m[1]] = true
			}
			for _, m := range builderField.FindAllStringSubmatch(span, -1) {
				names[m[1]] = true
			}
			var missing []string
			for n := range names {
				if referenced[n] {
					continue
				}
				key := base + ":" + firstCommand(calls[0][1])
				if _, ok := cliOmits[key][n]; ok {
					used[key+"/"+n] = true
					continue
				}
				missing = append(missing, n)
			}
			sort.Strings(missing)
			if len(missing) > 0 {
				t.Errorf("%s: the preview %q leaves out %s: show them in the preview, or list them in cliOmits with why",
					base, firstCommand(calls[0][1]), strings.Join(missing, ", "))
			}
		}
	}
	// An omission that is no longer one must leave the list.
	for key, fields := range cliOmits {
		for n := range fields {
			if !used[key+"/"+n] {
				t.Errorf("cliOmits has %s / %s, which the form no longer omits: remove it", key, n)
			}
		}
	}
}

func firstCommand(tmpl string) string {
	line, _, _ := strings.Cut(strings.ReplaceAll(tmpl, `\n`, "\n"), "\n")
	words := strings.Fields(line)
	if len(words) > 2 {
		words = words[:2]
	}
	return strings.Join(words, " ")
}
