package apigateway

// Refusals the boto3 conformance suite found missing
// (conformance/tests/test_apigateway.py).

import "testing"

func TestPathPartsAndMethodsAreHeldToWhatAPIGatewayAccepts(t *testing.T) {
	for _, part := range []string{"orders", "v1.2", "a_b-c:d", "{id}", "{proxy+}"} {
		if !pathPart.MatchString(part) {
			t.Errorf("path part %q refused", part)
		}
	}
	for _, part := range []string{"has space", "a/b", "{id", "id}", "{id}x", "{a b}", "{+}", "sla$h"} {
		if pathPart.MatchString(part) {
			t.Errorf("path part %q accepted", part)
		}
	}
	for _, verb := range []string{"GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS", "ANY"} {
		if !restMethods[verb] {
			t.Errorf("method %s refused", verb)
		}
	}
	for _, verb := range []string{"FETCH", "TRACE", "CONNECT", ""} {
		if restMethods[verb] {
			t.Errorf("method %q accepted", verb)
		}
	}
}
