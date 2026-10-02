package cfn

import (
	"strings"
	"testing"
)

func TestCheckParameters(t *testing.T) {
	tmpl, err := Parse([]byte(`{
	  "Parameters": {
	    "Env":     {"Type": "String", "AllowedValues": ["dev", "prod"], "Default": "dev"},
	    "Timeout": {"Type": "Number", "MinValue": 0, "MaxValue": "43200", "Default": 30},
	    "Name":    {"Type": "String", "MinLength": 3, "MaxLength": 8, "AllowedPattern": "[a-z]+"},
	    "Zone":    {"Type": "String", "AllowedPattern": "[a-z]{2}-[0-9]", "ConstraintDescription": "two letters, a dash, a digit"},
	    "Free":    {"Type": "String"}
	  },
	  "Resources": {"Q": {"Type": "AWS::SQS::Queue"}}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	good := map[string]string{"Env": "prod", "Timeout": "45", "Name": "orders", "Zone": "eu-1", "Free": "anything at all"}
	if err := tmpl.CheckParameters(good); err != nil {
		t.Fatalf("refused values that satisfy every constraint: %v", err)
	}
	// Defaults are held to the constraints too, and an absent value with no
	// default is somebody else's error to report.
	if err := tmpl.CheckParameters(map[string]string{"Name": "orders", "Zone": "eu-1"}); err != nil {
		t.Fatalf("defaults: %v", err)
	}
	for _, tc := range []struct{ key, value, want string }{
		{"Env", "staging", "Parameter 'Env' must be one of AllowedValues"},
		{"Timeout", "50000", "Parameter 'Timeout' must be a number not greater than 43200"},
		{"Timeout", "-1", "Parameter 'Timeout' must be a number not less than 0"},
		{"Timeout", "soon", "Parameter 'Timeout' must be a number"},
		{"Name", "ab", "Parameter 'Name' must contain at least 3 characters"},
		{"Name", "abcdefghi", "Parameter 'Name' must contain at most 8 characters"},
		{"Name", "Orders", "Parameter 'Name' failed to satisfy constraint: must match pattern [a-z]+"},
		{"Name", "ord3rs", "must match pattern"}, // the whole value, not a part of it
		{"Zone", "europe", "Parameter 'Zone' failed to satisfy constraint: two letters, a dash, a digit"},
	} {
		given := map[string]string{}
		for k, v := range good {
			given[k] = v
		}
		given[tc.key] = tc.value
		if err := tmpl.CheckParameters(given); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s=%q: got %v, want %q", tc.key, tc.value, err, tc.want)
		}
	}
}
