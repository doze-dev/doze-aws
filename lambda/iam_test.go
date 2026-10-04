package lambda

import (
	"net/http/httptest"
	"testing"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/dozetest"
)

// The permission a Lambda request is authorized as is the one AWS's model says,
// and the resource is the function it names. Twenty of these were guessed from
// the path and wrong: a policy that allowed lambda:AddPermission denied
// AddPermission, because the guess was lambda:CreatePermission.
func TestEveryRouteIsAuthorizedAsAWSNamesIt(t *testing.T) {
	s, err := New(Options{DataDir: t.TempDir(), Logf: dozetest.Logf(t)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	fn := awsident.Default().ARN("lambda", "function:worker")

	for _, c := range []struct {
		name             string
		method, path     string
		action, resource string
	}{
		{"invoke", "POST", "/2015-03-31/functions/worker/invocations", "lambda:InvokeFunction", fn},
		{"get a function", "GET", "/2015-03-31/functions/worker", "lambda:GetFunction", fn},
		{"list functions", "GET", "/2015-03-31/functions", "lambda:ListFunctions", ""},
		{"create", "POST", "/2015-03-31/functions", "lambda:CreateFunction", ""},
		{"delete", "DELETE", "/2015-03-31/functions/worker", "lambda:DeleteFunction", fn},
		{"get configuration", "GET", "/2015-03-31/functions/worker/configuration", "lambda:GetFunctionConfiguration", fn},
		{"update configuration", "PUT", "/2015-03-31/functions/worker/configuration", "lambda:UpdateFunctionConfiguration", fn},
		{"update code", "PUT", "/2015-03-31/functions/worker/code", "lambda:UpdateFunctionCode", fn},
		{"publish a version", "POST", "/2015-03-31/functions/worker/versions", "lambda:PublishVersion", fn},
		{"create an alias", "POST", "/2015-03-31/functions/worker/aliases", "lambda:CreateAlias", fn},
		{"get an alias", "GET", "/2015-03-31/functions/worker/aliases/live", "lambda:GetAlias", fn},
		{"delete an alias", "DELETE", "/2015-03-31/functions/worker/aliases/live", "lambda:DeleteAlias", fn},
		{"create a function url", "POST", "/2021-10-31/functions/worker/url", "lambda:CreateFunctionUrlConfig", fn},
		{"event source mappings", "GET", "/2015-03-31/event-source-mappings", "lambda:ListEventSourceMappings", ""},
		{"create an event source mapping", "POST", "/2015-03-31/event-source-mappings", "lambda:CreateEventSourceMapping", ""},
		{"update an event source mapping", "PUT", "/2015-03-31/event-source-mappings/id", "lambda:UpdateEventSourceMapping", ""},
		{"delete an event source mapping", "DELETE", "/2015-03-31/event-source-mappings/id", "lambda:DeleteEventSourceMapping", ""},
		{"a qualifier stays on the ARN", "POST", "/2015-03-31/functions/worker:live/invocations", "lambda:InvokeFunction", awsident.Default().ARN("lambda", "function:worker:live")},

		// What the path-guessing resolver named wrongly.
		{"add permission", "POST", "/2015-03-31/functions/worker/policy", "lambda:AddPermission", fn},
		{"remove permission", "DELETE", "/2015-03-31/functions/worker/policy/sid", "lambda:RemovePermission", fn},
		{"remove permission, query form", "DELETE", "/2015-03-31/functions/worker/policy?StatementId=sid", "lambda:RemovePermission", fn},
		{"get policy", "GET", "/2015-03-31/functions/worker/policy", "lambda:GetPolicy", fn},
		{"list versions", "GET", "/2015-03-31/functions/worker/versions", "lambda:ListVersionsByFunction", fn},
		{"list aliases", "GET", "/2015-03-31/functions/worker/aliases", "lambda:ListAliases", fn},
		{"update an alias", "PUT", "/2015-03-31/functions/worker/aliases/live", "lambda:UpdateAlias", fn},
		{"put concurrency", "PUT", "/2017-10-31/functions/worker/concurrency", "lambda:PutFunctionConcurrency", fn},
		{"get concurrency", "GET", "/2019-09-30/functions/worker/concurrency", "lambda:GetFunctionConcurrency", fn},
		{"delete concurrency", "DELETE", "/2017-10-31/functions/worker/concurrency", "lambda:DeleteFunctionConcurrency", fn},
		{"put event invoke config", "PUT", "/2019-09-25/functions/worker/event-invoke-config", "lambda:PutFunctionEventInvokeConfig", fn},
		{"update event invoke config", "POST", "/2019-09-25/functions/worker/event-invoke-config", "lambda:UpdateFunctionEventInvokeConfig", fn},
		{"list event invoke configs", "GET", "/2019-09-25/functions/worker/event-invoke-config/list", "lambda:ListFunctionEventInvokeConfigs", fn},
		{"get event invoke config", "GET", "/2019-09-25/functions/worker/event-invoke-config", "lambda:GetFunctionEventInvokeConfig", fn},
		{"update a function url", "PUT", "/2021-10-31/functions/worker/url", "lambda:UpdateFunctionUrlConfig", fn},
		{"list function urls", "GET", "/2021-10-31/functions/worker/urls", "lambda:ListFunctionUrlConfigs", fn},
		{"get an event source mapping", "GET", "/2015-03-31/event-source-mappings/id", "lambda:GetEventSourceMapping", ""},
		{"put code signing", "PUT", "/2020-06-30/functions/worker/code-signing-config", "lambda:PutFunctionCodeSigningConfig", fn},
		{"get code signing", "GET", "/2020-06-30/functions/worker/code-signing-config", "lambda:GetFunctionCodeSigningConfig", fn},
		{"delete code signing", "DELETE", "/2020-06-30/functions/worker/code-signing-config", "lambda:DeleteFunctionCodeSigningConfig", fn},
		{"publish a layer version", "POST", "/2018-10-31/layers/util/versions", "lambda:PublishLayerVersion", ""},
		{"list layers", "GET", "/2018-10-31/layers", "lambda:ListLayers", ""},
		{"list layer versions", "GET", "/2018-10-31/layers/util/versions", "lambda:ListLayerVersions", ""},
		{"get a layer version", "GET", "/2018-10-31/layers/util/versions/1", "lambda:GetLayerVersion", ""},
		{"get a layer version by ARN", "GET", "/2018-10-31/layers?find=LayerVersion&Arn=arn:aws:lambda:us-east-1:000000000000:layer:util:1", "lambda:GetLayerVersion", ""},
		{"list tags", "GET", "/2017-03-31/tags/arn", "lambda:ListTags", ""},
		{"tag", "POST", "/2017-03-31/tags/arn", "lambda:TagResource", ""},
		{"untag", "DELETE", "/2017-03-31/tags/arn", "lambda:UntagResource", ""},
		{"account settings", "GET", "/2016-08-19/account-settings", "lambda:GetAccountSettings", ""},

		{"doze's own runtime probe is not evaluated", "GET", "/2015-03-31/functions/worker/doze-runtime", "", ""},
		{"a path that is no route is not evaluated", "GET", "/2015-03-31/nothing", "", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			action, resource := s.resolveIAM(httptest.NewRequest(c.method, c.path, nil))
			if action != c.action || resource != c.resource {
				t.Errorf("%s %s = (%q, %q), want (%q, %q)", c.method, c.path, action, resource, c.action, c.resource)
			}
		})
	}
}

// Every operation the router serves that the model names has an action — a
// route with none would be let through unevaluated.
func TestEveryServedOperationHasAnAction(t *testing.T) {
	for _, rt := range routeSpecs() {
		if rt.Op == "DozeRuntime" || rt.Op == "" {
			continue // doze's own, not AWS's
		}
		if iamActions()[rt.Op] == "" {
			t.Errorf("%s (%s %s) has no IAM action", rt.Op, rt.Method, rt.Pattern)
		}
	}
}
