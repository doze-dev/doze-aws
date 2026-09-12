package main

// `doze-aws env` — the shell block that points an AWS SDK at this instance.
//
// # Why a command rather than a line in the README
//
// The values depend on the instance: its address, its region, its account, and
// whether it has a DNS suffix. A README can only show one combination, so it
// shows the default and everyone else works theirs out. This prints the right
// one for the configuration in front of you, and `eval` makes it one step.
//
// # Why the per-service variables
//
// AWS_ENDPOINT_URL alone is enough to make everything work: every service
// arrives on one address and the gateway tells them apart. The per-service
// AWS_ENDPOINT_URL_<SERVICE> variables buy something else — the URLs you get
// BACK are AWS-shaped, because doze-aws mints them from the Host a request
// arrived on. Ask through sqs.<region>.<suffix> and the queue URL says
// sqs.<region>.<suffix>.
//
// They are only printed when a suffix is configured, since without one there
// is nothing for them to point at that AWS_ENDPOINT_URL does not already
// cover.
//
// One limit worth stating, because it is not obvious and it is not fixable
// here: a custom endpoint is a fixed string. The SDKs do not template a region
// into it, so AWS_ENDPOINT_URL_SQS can only name ONE region. A client built
// for another region still reaches that host and still works — doze-aws routes
// on the credential scope — but the hostname it used will not match the region
// it asked for.

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	dozeaws "github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/awsident"
)

// signingNames maps a doze-aws service to the label AWS puts in its hostname
// and the suffix of its AWS_ENDPOINT_URL_<X> variable.
//
// Three disagree with the service's own name, which is the whole reason this
// is a table: EventBridge addresses as "events", Step Functions as "states",
// and CloudWatch's metrics endpoint as "monitoring".
var signingNames = map[string]string{
	"s3": "s3", "sqs": "sqs", "sns": "sns", "sts": "sts", "iam": "iam",
	"dynamodb": "dynamodb", "kms": "kms", "ssm": "ssm",
	"secretsmanager": "secretsmanager", "lambda": "lambda", "kinesis": "kinesis",
	"cloudformation": "cloudformation", "apigateway": "apigateway", "logs": "logs",
	"eventbridge": "events", "stepfunctions": "states", "cloudwatch": "monitoring",
}

// envVarNames are the SDK's spelling of the per-service variable, which is the
// signing name upper-cased with dashes dropped.
func envVarName(signing string) string {
	return "AWS_ENDPOINT_URL_" + strings.ToUpper(strings.ReplaceAll(signing, "-", ""))
}

func runEnv(args []string) int { return envTo(os.Stdout, args) }

// envTo is runEnv with the destination named, so a test can read what it would
// print without capturing os.Stdout.
func envTo(w io.Writer, args []string) int {
	st, err := loadConfig(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	cfg := st.cfg
	id := cfg.Identity()

	base := reachableEndpoint(cfg.ListenAddr)
	fmt.Fprintf(w, "export AWS_ENDPOINT_URL=%s\n", base)
	fmt.Fprintf(w, "export AWS_REGION=%s\n", id.RegionName())
	fmt.Fprintf(w, "export AWS_DEFAULT_REGION=%s\n", id.RegionName())
	fmt.Fprintf(w, "export AWS_ACCESS_KEY_ID=%s\n", awsident.AccessKeyID)
	fmt.Fprintf(w, "export AWS_SECRET_ACCESS_KEY=%s\n", awsident.SecretAccessKey)

	if cfg.Suffix == "" {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "# Everything reaches one address and the gateway tells the services apart.")
		fmt.Fprintln(w, "# Configure a DNS suffix (--suffix) to get AWS-shaped hostnames, and this")
		fmt.Fprintln(w, "# command will print the per-service variables that use them.")
		return 0
	}

	fmt.Fprintln(w)
	fmt.Fprintf(w, "# AWS-shaped hostnames under %s. Setting these means the URLs doze-aws\n", cfg.Suffix)
	fmt.Fprintln(w, "# hands back — queue URLs, invoke URLs — are AWS-shaped too, because it")
	fmt.Fprintln(w, "# mints them from the host a request arrived on.")
	fmt.Fprintf(w, "# Each names %s; a client built for another region still works (routing\n", id.RegionName())
	fmt.Fprintln(w, "# is by credential scope) but its hostname will not match the region.")

	enabled := cfg.Services
	if enabled == nil {
		enabled = dozeaws.Implemented
	}
	lines := make([]string, 0, len(enabled))
	for _, svc := range enabled {
		signing, ok := signingNames[svc]
		if !ok {
			continue
		}
		host := signing + "." + id.RegionName() + "." + cfg.Suffix
		if dozeaws.Global[svc] {
			// IAM and STS have no region, and their hostnames say so.
			host = signing + "." + cfg.Suffix
		}
		lines = append(lines, fmt.Sprintf("export %s=http://%s", envVarName(signing), host))
	}
	sort.Strings(lines)
	for _, l := range lines {
		fmt.Fprintln(w, l)
	}
	return 0
}

// envUsage is the one-line description in the command table.
const envUsage = "print the shell block that points an AWS SDK at this instance"
