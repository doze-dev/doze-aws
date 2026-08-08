package cloudformation

// The SAM transform.
//
// SAM is a macro layer: `AWS::Serverless::Function` expands to a Lambda
// function plus a role plus event wiring. doze-aws does not need the role, so
// the transform here is deliberately thin — it normalises SAM's shorthand onto
// the properties the Lambda and DynamoDB mappers already understand, and lets
// the Events block through to be expanded once every resource exists.
//
// The honest limit: SAM's `Api` and `HttpApi` events need API Gateway, which
// doze-aws does not serve yet. Those are refused by name during mapping rather
// than quietly producing a function nothing can reach.

// applySAMTransform normalises serverless resources in place. It runs before
// classification, so afterwards the ordinary registry handles everything.
//
// The Globals section supplies defaults for every function, and is applied
// first so an explicit property on a function always wins.
func applySAMTransform(t *Template) error {
	fnGlobals, _ := t.Globals["Function"].(map[string]any)
	for _, id := range t.Order() {
		r := t.Resources[id]
		if r.Type != "AWS::Serverless::Function" {
			continue
		}
		for k, v := range fnGlobals {
			if _, set := r.Properties[k]; !set {
				r.Properties[k] = v
			}
		}
		// SAM's InlineCode is a source body, and there is no build step
		// locally. Dropping it means apply reports a missing code path rather
		// than creating a function that cannot run.
		delete(r.Properties, "InlineCode")
	}
	return nil
}

// unsupportedSAM names serverless resource types doze-aws cannot model, with
// the reason, so the registry can refuse them precisely.
var unsupportedSAM = map[string]string{
	"AWS::Serverless::StateMachine": "doze-aws has no Step Functions yet",
	"AWS::Serverless::Application":  "nested applications need the Serverless Application Repository",
	"AWS::Serverless::LayerVersion": "layer versions are created through the Lambda API, not the stack file",
}

func samReason(typ string) (string, bool) {
	reason, ok := unsupportedSAM[typ]
	return reason, ok
}
