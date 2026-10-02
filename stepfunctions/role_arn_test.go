package stepfunctions

import "testing"

// A role ARN is a role's ARN. The model gives roleArn a length and nothing
// else, so "not-an-arn" used to make a state machine.
func TestRoleARNIsHeldToItsShape(t *testing.T) {
	for _, arn := range []string{
		"arn:aws:iam::000000000000:role/states",
		"arn:aws:iam::123456789012:role/service-role/StepFunctions-x",
		"arn:aws-us-gov:iam::123456789012:role/r",
	} {
		if aerr := validRoleARN(arn); aerr != nil {
			t.Errorf("%s refused: %v", arn, aerr)
		}
	}
	for _, arn := range []string{"", "not-an-arn", "arn:aws:iam::000000000000:user/u",
		"arn:aws:iam::12345:role/r", "arn:aws:s3:::bucket"} {
		if aerr := validRoleARN(arn); aerr == nil || aerr.Code != "InvalidArn" {
			t.Errorf("%q: got %v, want InvalidArn", arn, aerr)
		}
	}
}
