package dozeaws_test

// Every operation in every service's AWS model, driven over the real wire.
//
// # What was missing
//
// Each service's coverage_model_test.go asks a static question: does this
// operation name appear in exactly one dispatch table? That caught operations
// nobody had written down. It cannot see what the caller sees, and what the
// caller sees is the contract.
//
// Writing the per-service contracts for the 1.0 plan found about 120
// operations that pass the static check and still answer something false: an
// "InvalidAction" for an action the model documents, "unknown path" for a
// route the model defines, a bare 404 or 405, a "ValidationError" for a call
// whose input is fine. This test sends every operation and says which kind of
// answer came back.
//
// # What it sends, and why that is enough
//
// An empty request. A JSON or Query call with no members; a REST call with its
// labels filled with a placeholder and no body. Almost every operation refuses
// that, for a missing required member, and that is the point: the question is
// not whether the call succeeds but whether it was **dispatched**. A request
// that reaches a handler and is refused for its input has been understood. A
// request answered "no such action" has not, whatever the dispatch table says.
//
// # The classes
//
//	handled     2xx, or a 4xx that names a missing or invalid input. The
//	            operation reached something that understood it.
//	notbuilt    501 with X-Doze-Not-Built. The agreed refusal (plan decision D5).
//	legacystub  An answer an old refusal table gives: UnsupportedOperation-
//	            Exception, a 501 with no header, or anything stamped
//	            X-Doze-Legacy-Stub. The stamp is for the tables that refuse
//	            under a code for a bad input (CloudFormation's ValidationError,
//	            Secrets Manager's InvalidRequestException), which the code
//	            alone reads as handled. Understood, but not in the agreed
//	            shape. Burns down as services move to notbuilt.
//	unknown     InvalidAction, UnknownOperation, "unknown path", a route
//	            miss. A false statement that the operation does not exist.
//	fault       5xx. A bug.
//
// The baseline is frozen both ways in testdata/modelsweep.json, the same shape
// as the Unreached lists it replaces. The end state is a baseline with only
// handled and notbuilt in it, and then the legacy classes are removed from the
// code that can produce them.
//
// MODELSWEEP_REPORT=1 prints the table; MODELSWEEP_UPDATE=1 rewrites the
// baseline (and says what changed, so the diff is read, not trusted).

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	dozeaws "github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/console"
	"github.com/doze-dev/doze-aws/internal/dozetest"
)

// sweepService is one service's wire shape.
type sweepService struct {
	name    string // the stack's service name
	ops     string // ops_*.json
	routes  string // routes_*.json, REST only
	signing string // the SigV4 credential-scope service name
	style   string // "json" | "query" | "rest"
	target  string // X-Amz-Target prefix (json)
	jsonVer string // "1.0" | "1.1"
	version string // Query API version
}

var sweepServices = []sweepService{
	{name: "sqs", ops: "sqs/testdata/ops_sqs.json", signing: "sqs", style: "json", target: "AmazonSQS", jsonVer: "1.0"},
	{name: "sns", ops: "sns/testdata/ops_sns.json", signing: "sns", style: "query", version: "2010-03-31"},
	{name: "sts", ops: "sts/testdata/ops_sts.json", signing: "sts", style: "query", version: "2011-06-15"},
	{name: "iam", ops: "iam/testdata/ops_iam.json", signing: "iam", style: "query", version: "2010-05-08"},
	{name: "cloudformation", ops: "cloudformation/testdata/ops_cloudformation.json", signing: "cloudformation", style: "query", version: "2010-05-15"},
	{name: "cloudwatch", ops: "cloudwatch/testdata/ops_cloudwatch.json", signing: "monitoring", style: "query", version: "2010-08-01"},
	{name: "dynamodb", ops: "dynamodb/testdata/ops_dynamodb.json", signing: "dynamodb", style: "json", target: "DynamoDB_20120810", jsonVer: "1.0"},
	{name: "kinesis", ops: "kinesis/testdata/ops_kinesis.json", signing: "kinesis", style: "json", target: "Kinesis_20131202", jsonVer: "1.1"},
	{name: "kms", ops: "kms/testdata/ops_kms.json", signing: "kms", style: "json", target: "TrentService", jsonVer: "1.1"},
	{name: "secretsmanager", ops: "secretsmanager/testdata/ops_secrets-manager.json", signing: "secretsmanager", style: "json", target: "secretsmanager", jsonVer: "1.1"},
	{name: "ssm", ops: "ssm/testdata/ops_ssm.json", signing: "ssm", style: "json", target: "AmazonSSM", jsonVer: "1.1"},
	{name: "stepfunctions", ops: "stepfunctions/testdata/ops_sfn.json", signing: "states", style: "json", target: "AWSStepFunctions", jsonVer: "1.0"},
	{name: "eventbridge", ops: "eventbridge/testdata/ops_eventbridge.json", signing: "events", style: "json", target: "AWSEvents", jsonVer: "1.1"},
	{name: "logs", ops: "logs/testdata/ops_cloudwatch-logs.json", signing: "logs", style: "json", target: "Logs_20140328", jsonVer: "1.1"},
	{name: "lambda", ops: "lambda/testdata/ops_lambda.json", routes: "lambda/testdata/modelroutes_lambda.json", signing: "lambda", style: "rest"},
	{name: "s3", ops: "s3/testdata/ops_s3.json", routes: "s3/testdata/modelroutes_s3.json", signing: "s3", style: "rest"},
	{name: "apigateway", ops: "apigateway/testdata/ops_api-gateway.json", routes: "apigateway/testdata/modelroutes_api-gateway.json", signing: "apigateway", style: "rest"},
	{name: "apigatewayv2", ops: "apigateway/testdata/ops_apigatewayv2.json", routes: "apigateway/testdata/modelroutes_apigatewayv2.json", signing: "apigateway", style: "rest"},
}

type restRoute struct {
	Method string `json:"method"`
	URI    string `json:"uri"`
}

var labelRE = regexp.MustCompile(`\{([A-Za-z0-9_]+)\+?\}`)

// request builds the empty request for one operation.
func (s sweepService) request(base, op string, routes map[string]restRoute) (*http.Request, error) {
	scope := "Credential=test/20261010/us-east-1/" + s.signing + "/aws4_request, SignedHeaders=host, Signature=00"
	var r *http.Request
	var err error
	switch s.style {
	case "json":
		r, err = http.NewRequest("POST", base+"/", strings.NewReader("{}"))
		if err != nil {
			return nil, err
		}
		r.Header.Set("Content-Type", "application/x-amz-json-"+s.jsonVer)
		r.Header.Set("X-Amz-Target", s.target+"."+op)
	case "query":
		form := url.Values{"Action": {op}, "Version": {s.version}}
		r, err = http.NewRequest("POST", base+"/", strings.NewReader(form.Encode()))
		if err != nil {
			return nil, err
		}
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	case "rest":
		rt, ok := routes[op]
		if !ok {
			return nil, fmt.Errorf("no route for %s in the model's route table", op)
		}
		uri := labelRE.ReplaceAllString(rt.URI, "sweep")
		body := io.Reader(nil)
		if rt.Method == "POST" || rt.Method == "PUT" || rt.Method == "PATCH" {
			body = strings.NewReader("{}")
		}
		r, err = http.NewRequest(rt.Method, base+uri, body)
		if err != nil {
			return nil, err
		}
		if body != nil {
			r.Header.Set("Content-Type", "application/json")
		}
	}
	r.Header.Set("Authorization", "AWS4-HMAC-SHA256 "+scope)
	r.Header.Set("X-Amz-Date", "20261010T000000Z")
	return r, nil
}

// answer is the part of a response the classifier reads.
type answer struct {
	status  int
	code    string
	message string
	header  string
	// legacy is the X-Doze-Legacy-Stub header: the stub table's own word that
	// this answer came from it.
	legacy string
}

// A stub that refuses under the code for a bad input is a stub, and the sweep
// knows it from the stamp the stub's writer sets and not from the code or from
// how the message is spelled. CloudFormation's 67 stubs answer 400
// ValidationError and Secrets Manager's 3 answer 400 InvalidRequestException;
// by the code both are an input refused, which is "handled", and so nothing
// made them move to the agreed refusal.
func TestTheSweepReadsAStubFromItsStamp(t *testing.T) {
	for _, tc := range []struct {
		what string
		a    answer
		want string
	}{
		{"a CloudFormation stub", answer{status: 400, code: "ValidationError", message: "doze-aws does not implement DetectStackDrift: there is nothing to drift from locally", legacy: "1"}, "legacystub"},
		{"a Secrets Manager stub", answer{status: 400, code: "InvalidRequestException", message: "not supported by doze-aws: there is exactly one region locally", legacy: "1"}, "legacystub"},
		// The same codes with no stamp are what they say: an input refused.
		{"a real ValidationError", answer{status: 400, code: "ValidationError", message: "1 validation error detected"}, "handled"},
		{"a real InvalidRequestException", answer{status: 400, code: "InvalidRequestException", message: "You can't perform this operation on the secret because it was marked for deletion."}, "handled"},
		// A message that reads like a stub's is not evidence of one.
		{"a message that says doze-aws", answer{status: 400, code: "ValidationError", message: "doze-aws does not implement this property"}, "handled"},
		// The agreed refusal outranks the stamp: it is where a stub ends up.
		{"not built", answer{status: 501, code: "NotImplemented", header: "deferred", legacy: "1"}, "notbuilt"},
	} {
		if got := classify(tc.a); got != tc.want {
			t.Errorf("%s: classified %s, want %s", tc.what, got, tc.want)
		}
	}
}

var (
	xmlCodeRE = regexp.MustCompile(`<Code>([^<]*)</Code>`)
	xmlMsgRE  = regexp.MustCompile(`<Message>([^<]*)</Message>`)
)

func readAnswer(resp *http.Response) answer {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	a := answer{status: resp.StatusCode, header: resp.Header.Get(awshttp.NotBuiltHeader),
		legacy: resp.Header.Get(awshttp.LegacyStubHeader)}
	var j map[string]any
	if json.Unmarshal(body, &j) == nil {
		for _, k := range []string{"__type", "code", "Code", "errorType"} {
			if s, ok := j[k].(string); ok && s != "" {
				a.code = s
				break
			}
		}
		for _, k := range []string{"message", "Message", "errorMessage"} {
			if s, ok := j[k].(string); ok && s != "" {
				a.message = s
				break
			}
		}
	}
	if a.code == "" {
		a.code = resp.Header.Get("x-amzn-ErrorType")
	}
	if m := xmlCodeRE.FindSubmatch(body); m != nil && a.code == "" {
		a.code = string(m[1])
	}
	if m := xmlMsgRE.FindSubmatch(body); m != nil && a.message == "" {
		a.message = string(m[1])
	}
	// "Type#Code:uri" and "ns#Code" shapes some services use.
	if i := strings.LastIndex(a.code, "#"); i >= 0 {
		a.code = a.code[i+1:]
	}
	if i := strings.Index(a.code, ":"); i >= 0 {
		a.code = a.code[:i]
	}
	return a
}

// classify is deliberately small and written so that adding a class is a
// visible edit. The unknown and legacystub sets are the shapes the survey found.
func classify(a answer) string {
	switch {
	case a.status >= 200 && a.status < 300:
		return "handled"
	case a.status == 501 && a.header != "":
		return "notbuilt"
	case a.legacy != "":
		// The stub table's own stamp, whatever code the stub borrowed.
		return "legacystub"
	case a.status == 501:
		return "legacystub"
	case a.status >= 500:
		return "fault"
	}
	switch a.code {
	case "InvalidAction", "UnknownOperation", "UnknownOperationException", "MethodNotAllowed":
		return "unknown"
	case "UnsupportedOperation", "NotImplemented":
		// UnsupportedOperationException is not here: it is a real AWS code,
		// and every stub table that borrows it sets the stamp read above.
		return "legacystub"
	}
	m := strings.ToLower(a.message)
	if strings.Contains(m, "unknown path") || strings.Contains(m, "unknown action") ||
		strings.Contains(m, "unknown operation") || strings.Contains(m, "no route") {
		return "unknown"
	}
	// A bare 404 or 405 with nothing identifying it is a route miss.
	if (a.status == 404 || a.status == 405) && a.code == "" {
		return "unknown"
	}
	return "handled"
}

type sweepResult struct {
	Class   string `json:"class"`
	Status  int    `json:"status"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

func TestEveryModelOperationIsAnsweredTruthfully(t *testing.T) {
	if testing.Short() {
		t.Skip("stands up a full stack and sends every operation")
	}
	stack, err := dozeaws.NewStack(dozeaws.StackConfig{DataDir: t.TempDir(), Logf: dozetest.Quiet(t)})
	if err != nil {
		t.Fatal(err)
	}
	defer stack.Close()
	ts := httptest.NewServer(stack.Handler())
	defer ts.Close()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	got := map[string]map[string]sweepResult{}
	for _, s := range sweepServices {
		ops := dozetest.ModelOps(t, s.ops)
		var routes map[string]restRoute
		if s.routes != "" {
			b, err := os.ReadFile(s.routes)
			if err != nil {
				t.Fatalf("%s: %v", s.routes, err)
			}
			if err := json.Unmarshal(b, &routes); err != nil {
				t.Fatalf("%s: %v", s.routes, err)
			}
		}
		got[s.name] = map[string]sweepResult{}
		for _, op := range ops {
			req, err := s.request(ts.URL, op, routes)
			if err != nil {
				got[s.name][op] = sweepResult{Class: "noroute", Message: err.Error()}
				continue
			}
			resp, err := client.Do(req)
			if err != nil {
				t.Fatalf("%s %s: %v", s.name, op, err)
			}
			a := readAnswer(resp)
			resp.Body.Close()
			msg := a.message
			if len(msg) > 90 {
				msg = msg[:90]
			}
			got[s.name][op] = sweepResult{Class: classify(a), Status: a.status, Code: a.code, Message: msg}
		}
	}

	if os.Getenv("MODELSWEEP_REPORT") != "" {
		printSweep(t, got)
	}

	// What is frozen is every operation that is not simply "handled": the
	// default needs no entry, so the file is the burn-down list and nothing else.
	now := map[string]map[string]string{}
	for svc, m := range got {
		for op, r := range m {
			if r.Class == "handled" {
				continue
			}
			if now[svc] == nil {
				now[svc] = map[string]string{}
			}
			now[svc][op] = r.Class
		}
	}

	// Two classes are never acceptable, whatever the baseline says: a 5xx is a
	// bug, and an operation the sweep could not even address is a hole in the
	// fixtures. Neither belongs on a burn-down list.
	for svc, m := range got {
		for op, r := range m {
			if r.Class == "fault" || r.Class == "noroute" {
				t.Errorf("%s %s answered %s (%d %s %s)", svc, op, r.Class, r.Status, r.Code, r.Message)
			}
		}
	}

	const path = "testdata/modelsweep.json"
	if os.Getenv("MODELSWEEP_UPDATE") != "" {
		b, _ := json.MarshalIndent(now, "", " ")
		if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("rewrote %s: read the diff, do not trust it", path)
		return
	}
	var frozen map[string]map[string]string
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v\n  Write the baseline with: MODELSWEEP_UPDATE=1 go test . -run TestEveryModelOperationIsAnsweredTruthfully", err)
	}
	if err := json.Unmarshal(b, &frozen); err != nil {
		t.Fatalf("%s: %v", path, err)
	}

	// A service dropped from the sweep must take its entries with it: otherwise
	// they sit in the baseline unchecked and read as a burn-down still to do.
	for svc := range frozen {
		if _, ok := got[svc]; !ok {
			t.Errorf("%s is in the baseline and is no longer swept", svc)
		}
	}

	// Frozen in BOTH directions, the shape of the Unreached lists this replaces.
	// A worse answer is a regression; a better one means somebody closed a gap
	// and the list should say so, and the failure message says which it is.
	var svcs []string
	for svc := range got {
		svcs = append(svcs, svc)
	}
	sort.Strings(svcs)
	for _, svc := range svcs {
		var ops []string
		for op := range got[svc] {
			ops = append(ops, op)
		}
		sort.Strings(ops)
		for _, op := range ops {
			cur := now[svc][op]
			if cur == "" {
				cur = "handled"
			}
			was := frozen[svc][op]
			if was == "" {
				was = "handled"
			}
			if cur == was {
				continue
			}
			r := got[svc][op]
			if rank(cur) < rank(was) {
				t.Errorf("%s %s improved: %s -> %s. Good. Record it: MODELSWEEP_UPDATE=1", svc, op, was, cur)
			} else {
				t.Errorf("%s %s got worse: %s -> %s (%d %s: %s)", svc, op, was, cur, r.Status, r.Code, r.Message)
			}
		}
		for op := range frozen[svc] {
			if _, ok := got[svc][op]; !ok {
				t.Errorf("%s %s is in the baseline and not in the model", svc, op)
			}
		}
	}
}

// rank orders the classes from best to worst, for saying which way a change went.
func rank(class string) int {
	switch class {
	case "handled", "notbuilt":
		return 0
	case "legacystub":
		return 1
	case "unknown":
		return 2
	}
	return 3
}

func printSweep(t *testing.T, got map[string]map[string]sweepResult) {
	classes := []string{"handled", "notbuilt", "legacystub", "unknown", "fault", "noroute"}
	var names []string
	for n := range got {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	fmt.Fprintf(&b, "\n%-16s %5s", "service", "ops")
	for _, c := range classes {
		fmt.Fprintf(&b, " %10s", c)
	}
	b.WriteString("\n")
	tot := map[string]int{}
	all := 0
	for _, n := range names {
		cnt := map[string]int{}
		for _, r := range got[n] {
			cnt[r.Class]++
			tot[r.Class]++
		}
		all += len(got[n])
		fmt.Fprintf(&b, "%-16s %5d", n, len(got[n]))
		for _, c := range classes {
			fmt.Fprintf(&b, " %10d", cnt[c])
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "%-16s %5d", "TOTAL", all)
	for _, c := range classes {
		fmt.Fprintf(&b, " %10d", tot[c])
	}
	b.WriteString("\n\nanswers by (class, status, code):\n")
	type key struct {
		class, code string
		status      int
	}
	freq := map[key]int{}
	for _, m := range got {
		for _, r := range m {
			if r.Class == "handled" && r.Status >= 200 && r.Status < 300 {
				freq[key{r.Class, "", r.Status}]++
				continue
			}
			freq[key{r.Class, r.Code, r.Status}]++
		}
	}
	var ks []key
	for k := range freq {
		ks = append(ks, k)
	}
	sort.Slice(ks, func(i, j int) bool {
		if ks[i].class != ks[j].class {
			return ks[i].class < ks[j].class
		}
		return freq[ks[i]] > freq[ks[j]]
	})
	for _, k := range ks {
		fmt.Fprintf(&b, "  %-10s %3d  %-40s %d\n", k.class, k.status, k.code, freq[k])
	}
	t.Log(b.String())
}

// The console renders a refusal by reading its code out of the response, and
// there are four places a service can put it. This asks each of the services
// for a real refusal and holds the console's reader to getting a code and a
// message out of it. S3 failed: its error document has Error as the root and
// the reader asked for an Error child, so every S3 refusal in the console was
// raw XML under a bare "Refused". Lambda and API Gateway put the code in a
// header and had a message and no code.
func TestTheConsoleCanReadEveryServicesRefusal(t *testing.T) {
	if testing.Short() {
		t.Skip("stands up a full stack and sends one operation per service")
	}
	stack, err := dozeaws.NewStack(dozeaws.StackConfig{DataDir: t.TempDir(), Logf: dozetest.Quiet(t)})
	if err != nil {
		t.Fatal(err)
	}
	defer stack.Close()
	ts := httptest.NewServer(stack.Handler())
	defer ts.Close()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	for _, s := range sweepServices {
		t.Run(s.name, func(t *testing.T) {
			var routes map[string]restRoute
			if s.routes != "" {
				b, err := os.ReadFile(s.routes)
				if err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(b, &routes); err != nil {
					t.Fatal(err)
				}
			}
			// The first operation (by name) whose empty request is refused for
			// its input: that is a refusal every service can produce.
			for _, op := range dozetest.ModelOps(t, s.ops) {
				req, err := s.request(ts.URL, op, routes)
				if err != nil {
					continue
				}
				resp, err := client.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
				resp.Body.Close()
				a := readAnswer(&http.Response{StatusCode: resp.StatusCode, Header: resp.Header, Body: io.NopCloser(strings.NewReader(string(body)))})
				if classify(a) != "handled" || resp.StatusCode < 400 || resp.StatusCode >= 500 {
					continue
				}
				got := console.TrafficEntry{Status: resp.StatusCode, RespBody: string(body), ErrType: resp.Header.Get("x-amzn-ErrorType")}.Failure()
				if got == nil || got.Code == "" || got.Message == "" {
					t.Fatalf("%s %s answered %d %q and the console reads %+v: a refusal it cannot name",
						s.name, op, resp.StatusCode, string(body), got)
				}
				return
			}
			t.Fatalf("no operation of %s was refused for its input", s.name)
		})
	}
}
