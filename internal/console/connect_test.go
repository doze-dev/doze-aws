package console_test

// The Connect page (board Connect): the endpoint written out as the AWS CLI
// profile, the environment, a Terraform provider and the deploy commands, each
// a block whose Copy copies exactly what the block shows, with the region the
// instance runs in and the address the page was reached on, and a check that
// runs STS GetCallerIdentity and says who answered.

import (
	"html"
	"regexp"
	"strings"
	"testing"
)

var (
	cnCode = regexp.MustCompile(`(?s)<pre class="cn-code">(.*?)</pre>`)
	cnCopy = regexp.MustCompile(`(?s)data-copy="(.*?)"`)
)

// cnBlocks is the page's five blocks, as a person reads them, in the order the
// page draws them down its columns: CLI config, CLI credentials, environment,
// Terraform, deploy.
func cnBlocks(t *testing.T, page string) []string {
	t.Helper()
	var out []string
	for _, m := range cnCode.FindAllStringSubmatch(page, -1) {
		out = append(out, html.UnescapeString(m[1]))
	}
	if len(out) != 5 {
		t.Fatalf("the page has %d code blocks, want 5:\n%s", len(out), page)
	}
	return out
}

func wantBlocks(endpoint, region string) []string {
	return []string{
		"[profile doze]\nregion = " + region + "\noutput = json\nendpoint_url = " + endpoint,
		"[doze]\naws_access_key_id = test\naws_secret_access_key = test",
		"export AWS_ENDPOINT_URL=" + endpoint + "\nexport AWS_ACCESS_KEY_ID=test\nexport AWS_SECRET_ACCESS_KEY=test\nexport AWS_REGION=" + region,
		"provider \"aws\" {\n" +
			"  region                      = \"" + region + "\"\n" +
			"  access_key                  = \"test\"\n" +
			"  secret_key                  = \"test\"\n" +
			"  skip_credentials_validation = true\n" +
			"  skip_requesting_account_id  = true\n" +
			"  s3_use_path_style           = true\n" +
			"\n" +
			"  endpoints {\n" +
			"    s3       = \"" + endpoint + "\"\n" +
			"    sqs      = \"" + endpoint + "\"\n" +
			"    sns      = \"" + endpoint + "\"\n" +
			"    dynamodb = \"" + endpoint + "\"\n" +
			"    lambda   = \"" + endpoint + "\"\n" +
			"  }\n" +
			"}",
		"aws cloudformation deploy \\\n  --template-file template.yaml --stack-name shop\nsam deploy --stack-name shop --s3-bucket artifacts\ncdk bootstrap && cdk deploy",
	}
}

func TestConnectDrawsEveryBlockFromTheEndpointAndTheRegion(t *testing.T) {
	h := newConsole(t)
	page := req(t, h, "GET", "/_console/connect", nil).Body.String()
	// httptest's request is addressed to example.com: the page is built from
	// the address it was reached on, not from a default.
	got, want := cnBlocks(t, page), wantBlocks("http://example.com", "us-east-1")
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("block %d:\n got  %q\n want %q", i, got[i], want[i])
		}
	}
	// Copy copies what the block shows.
	var copies []string
	for _, m := range cnCopy.FindAllStringSubmatch(page, -1) {
		copies = append(copies, html.UnescapeString(m[1]))
	}
	if strings.Join(copies, "\x00") != strings.Join(got, "\x00") {
		t.Errorf("the Copy buttons copy %q, the blocks show %q", copies, got)
	}
	for _, name := range []string{"~/.aws/config", "~/.aws/credentials", "shell", "provider.tf", "deploy"} {
		if !strings.Contains(page, `<span>`+name+`</span>`) {
			t.Errorf("no block named %q", name)
		}
	}
}

// The board's three columns hold these headings and sentences, and nothing
// else is drawn that the board does not: no "then" block, no account line.
func TestConnectSaysWhatTheBoardSays(t *testing.T) {
	h := newConsole(t)
	text := flatText(req(t, h, "GET", "/_console/connect", nil).Body.String())
	for _, want := range []string{
		"Point your tooling at this endpoint. Every block is the same fact in a different dialect, built from the address you reached the console on.",
		"AWS CLI profile",
		"A named profile keeps later commands short and leaves your real AWS credentials untouched.",
		"endpoint_url in a profile needs AWS CLI v2.13 or newer. On anything older, use the environment block.",
		"Environment",
		"No files touched. Every AWS SDK reads these.",
		"Terraform",
		"Deploy with what you already have",
		"There is no doze-specific file format. These read the environment and need no further setup.",
		"Check it works",
		"Run get-caller-identity",
		"Runs STS GetCallerIdentity against this stack. It does not appear on the wire — that tail is your app's traffic only.",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the page does not say %q", want)
		}
	}
	for _, not := range []string{"aws --profile doze", "Every block below", "It will not appear"} {
		if strings.Contains(text, not) {
			t.Errorf("the page still says %q", not)
		}
	}
}

func TestConnectVerifyNamesTheCaller(t *testing.T) {
	h := newConsole(t)
	out := flatText(req(t, h, "POST", "/_console/connect/verify", nil).Body.String())
	if !regexp.MustCompile(`^Connected arn:aws:iam::000000000000:user/test$`).MatchString(out) {
		t.Errorf("verify answered %q, want \"Connected arn:aws:iam::000000000000:user/test\"", out)
	}
}

// Another account and region: every block says the instance's region, the
// check says its account, and neither default is written anywhere on the page.
func TestConnectUnderAnotherIdentityWritesNoDefault(t *testing.T) {
	con, _ := consoleInEU(t)
	page := req(t, con, "GET", "/_console/connect", nil).Body.String()
	got, want := cnBlocks(t, page), wantBlocks("http://example.com", euRegion)
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("block %d:\n got  %q\n want %q", i, got[i], want[i])
		}
	}
	if strings.Contains(page, "us-east-1") || strings.Contains(page, "000000000000") {
		t.Errorf("the page carries the default identity:\n%s", page)
	}
	out := flatText(req(t, con, "POST", "/_console/connect/verify", nil).Body.String())
	if want := "Connected arn:aws:iam::" + euAccount + ":user/test"; out != want {
		t.Errorf("verify answered %q, want %q", out, want)
	}
}
