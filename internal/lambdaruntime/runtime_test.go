package lambdaruntime

import (
	"strings"
	"testing"
)

func TestRuntimeCommand(t *testing.T) {
	cases := []struct {
		runtime string
		handler string
		want    []string
		wantErr bool
	}{
		{"", "", []string{"./bootstrap"}, false},
		{"go", "main", []string{"./main"}, false},
		{"provided.al2", "bootstrap", []string{"./bootstrap"}, false},
		{"python3.12", "app.handler", []string{"python3", "-m", "awslambdaric", "app.handler"}, false},
		{"nodejs20.x", "index.handler", []string{"npx", "--yes", "aws-lambda-ric", "index.handler"}, false},
		{"java21", "example.Handler::run", []string{"java", "-cp", "./*:.", "com.amazonaws.services.lambda.runtime.api.client.AWSLambda", "example.Handler::run"}, false},
		{"ruby3.3", "app.LambdaFunction::Handler.process", []string{"aws_lambda_ric", "app.LambdaFunction::Handler.process"}, false},
		{"dotnet8", "Assembly::Type::Method", []string{"dotnet", "exec", "/opt/aws-lambda-ric.dll", "Assembly::Type::Method"}, false},
		{"cobol", "x", nil, true},
	}
	for _, c := range cases {
		got, err := runtimeCommand(c.runtime, c.handler)
		if c.wantErr {
			if err == nil {
				t.Errorf("runtime %q: expected error, got %v", c.runtime, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("runtime %q: unexpected error: %v", c.runtime, err)
			continue
		}
		if strings.Join(got, " ") != strings.Join(c.want, " ") {
			t.Errorf("runtime %q: got %v, want %v", c.runtime, got, c.want)
		}
	}
}
