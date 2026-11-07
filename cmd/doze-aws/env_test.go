package main

import (
	"strings"
	"testing"
)

// captureEnv runs `doze-aws env` with the given flags and returns what it
// would print.
func captureEnv(t *testing.T, args ...string) string {
	t.Helper()
	var b strings.Builder
	if code := envTo(&b, append([]string{"--data-dir", t.TempDir()}, args...)); code != 0 {
		t.Fatalf("env exited %d", code)
	}
	return b.String()
}

// The signing name is not the service name for three of them, and getting one
// wrong produces a hostname no SDK will ever ask for — so the variable is set,
// looks right, and does nothing.
func TestSigningNamesMatchAWS(t *testing.T) {
	for svc, want := range map[string]string{
		"eventbridge":   "events",     // EventBridge signs and addresses as events
		"stepfunctions": "states",     // Step Functions as states
		"cloudwatch":    "monitoring", // the metrics endpoint is monitoring
		"sqs":           "sqs",
		"s3":            "s3",
	} {
		if got := signingNames[svc]; got != want {
			t.Errorf("signingNames[%q] = %q, want %q", svc, got, want)
		}
	}
	// Every implemented service needs one, or `env` silently omits it.
	for svc := range signingNames {
		if signingNames[svc] == "" {
			t.Errorf("%s has no signing name", svc)
		}
	}
}

func TestEnvVarName(t *testing.T) {
	for in, want := range map[string]string{
		"sqs":            "AWS_ENDPOINT_URL_SQS",
		"events":         "AWS_ENDPOINT_URL_EVENTS",
		"secretsmanager": "AWS_ENDPOINT_URL_SECRETSMANAGER",
		"monitoring":     "AWS_ENDPOINT_URL_MONITORING",
	} {
		if got := envVarName(in); got != want {
			t.Errorf("envVarName(%q) = %q, want %q", in, got, want)
		}
	}
}

// A region-less service must not carry a region in its hostname. IAM in
// ap-south-1 is the same IAM as everywhere else, and iam.ap-south-1.<suffix>
// is a name AWS does not use and doze-aws does not serve.
func TestGlobalServicesHaveNoRegionInTheirHost(t *testing.T) {
	out := captureEnv(t, "--suffix", "aws.harbour.doze", "--region", "ap-south-1")
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "export AWS_ENDPOINT_URL_IAM="),
			strings.HasPrefix(line, "export AWS_ENDPOINT_URL_STS="):
			if strings.Contains(line, "ap-south-1") {
				t.Errorf("a region-less service carries a region: %s", line)
			}
		case strings.HasPrefix(line, "export AWS_ENDPOINT_URL_SQS="):
			if !strings.Contains(line, "sqs.ap-south-1.aws.harbour.doze") {
				t.Errorf("regional service is missing its region: %s", line)
			}
		}
	}
}

// With no flags, env points an SDK at the address doze-aws listens on by
// default, 127.0.0.1:4566, and names nothing under .doze: the standalone binary
// claims no name (names are doze's, docs/endpoints.md).
func TestEnvPointsAtTheDefaultAddress(t *testing.T) {
	out := captureEnv(t)
	if !strings.Contains(out, "export AWS_ENDPOINT_URL=http://127.0.0.1:4566\n") {
		t.Errorf("AWS_ENDPOINT_URL is not the default address:\n%s", out)
	}
	if strings.Contains(out, ".doze") {
		t.Errorf("the standalone binary claims no .doze name:\n%s", out)
	}
}

// With --suffix (a proxy that owns a name in front of doze-aws) the
// per-service hostnames sit under it.
func TestEnvWithASuffixNamesEachService(t *testing.T) {
	out := captureEnv(t, "--suffix", "aws.example.test")
	if !strings.Contains(out, "AWS_ENDPOINT_URL_SQS=http://sqs.us-east-1.aws.example.test") {
		t.Errorf("the per-service block is missing or wrong:\n%s", out)
	}
	// The three that sign under another name — the variable follows the signing
	// name, not the service name.
	for _, want := range []string{
		"AWS_ENDPOINT_URL_EVENTS=http://events.",
		"AWS_ENDPOINT_URL_STATES=http://states.",
		"AWS_ENDPOINT_URL_MONITORING=http://monitoring.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
}

// Under --listen, the address given, and no per-service hostnames without a
// suffix to put them under.
func TestEnvUnderListenUsesTheAddress(t *testing.T) {
	out := captureEnv(t, "--listen", "127.0.0.1:4577")

	if !strings.Contains(out, "export AWS_ENDPOINT_URL=http://127.0.0.1:4577") {
		t.Errorf("AWS_ENDPOINT_URL is not the listen address:\n%s", out)
	}
	if strings.Contains(out, "AWS_ENDPOINT_URL_") {
		t.Errorf("per-service hostnames need a suffix:\n%s", out)
	}
	if strings.Contains(out, ".doze") {
		t.Errorf("no .doze name:\n%s", out)
	}
	for _, want := range []string{"AWS_ENDPOINT_URL=", "AWS_REGION=", "AWS_ACCESS_KEY_ID="} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s in:\n%s", want, out)
		}
	}
}
