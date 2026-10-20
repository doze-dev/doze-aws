package console

// Connect: how to point tooling at this endpoint.
//
// It exists because the first question anyone has is "how do I make my CLI /
// SDK / Terraform talk to this", and until now the console answered it
// nowhere — the README did, which is the wrong place when you already have the
// thing running in front of you.
//
// Everything on the page is the same fact in different dialects, rendered from
// the endpoint the request actually arrived on rather than a hardcoded
// 127.0.0.1:4566. Someone reaching the console through a .doze name, a
// forwarded port or a different --listen gets snippets that work for them.

import (
	"net/http"

	"github.com/doze-dev/doze-aws/awsident"
)

func (c *Console) connect(w http.ResponseWriter, r *http.Request) {
	url := "http://" + endpointHost(r)
	region := c.be.id.RegionName()
	key, secret := awsident.AccessKeyID, awsident.SecretAccessKey
	// Each block is written once, here, and the page draws it and hands the same
	// string to Copy: what is copied is what is read (board Connect).
	c.render(w, r, "connect", map[string]any{
		"Title": "Connect",
		"URL":   url,
		"Config": "[profile doze]\n" +
			"region = " + region + "\n" +
			"output = json\n" +
			"endpoint_url = " + url,
		"Credentials": "[doze]\n" +
			"aws_access_key_id = " + key + "\n" +
			"aws_secret_access_key = " + secret,
		"Env": "export AWS_ENDPOINT_URL=" + url + "\n" +
			"export AWS_ACCESS_KEY_ID=" + key + "\n" +
			"export AWS_SECRET_ACCESS_KEY=" + secret + "\n" +
			"export AWS_REGION=" + region,
		"Terraform": "provider \"aws\" {\n" +
			"  region                      = \"" + region + "\"\n" +
			"  access_key                  = \"" + key + "\"\n" +
			"  secret_key                  = \"" + secret + "\"\n" +
			"  skip_credentials_validation = true\n" +
			"  skip_requesting_account_id  = true\n" +
			"  s3_use_path_style           = true\n" +
			"\n" +
			"  endpoints {\n" +
			"    s3       = \"" + url + "\"\n" +
			"    sqs      = \"" + url + "\"\n" +
			"    sns      = \"" + url + "\"\n" +
			"    dynamodb = \"" + url + "\"\n" +
			"    lambda   = \"" + url + "\"\n" +
			"  }\n" +
			"}",
		"Deploy": "aws cloudformation deploy \\\n" +
			"  --template-file template.yaml --stack-name shop\n" +
			"sam deploy --stack-name shop --s3-bucket artifacts\n" +
			"cdk bootstrap && cdk deploy",
	})
}

// connectVerify runs a real STS GetCallerIdentity against this stack and
// reports what came back, so "is my config right" gets settled on the page
// rather than in a terminal.
//
// It does NOT show up on the wire, and that is deliberate rather than an
// oversight: the console's own client dispatches in-process, underneath the
// Recorder, precisely so the tail stays the user's app talking and nothing
// else. Routing this one call through the recorded path to make a nicer demo
// would cost the property that makes the wire worth reading.
func (c *Console) connectVerify(w http.ResponseWriter, r *http.Request) {
	acct, arn, err := c.be.CallerIdentity(r.Context())
	if err != nil {
		c.partial(w, "connect_result", map[string]any{"Err": errText(err)})
		return
	}
	c.partial(w, "connect_result", map[string]any{"Account": acct, "ARN": arn})
}
