package iampolicy

import "testing"

func TestValidateResourcePolicy(t *testing.T) {
	good := `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"AWS":"arn:aws:iam::000000000000:root"},"Action":"sqs:SendMessage","Resource":"*"}]}`
	cases := []struct {
		name string
		raw  string
		kind ProblemKind // "" is valid
		stmt int
	}{
		{"valid", good, "", 0},
		{"valid, one statement as an object", `{"Statement":{"Effect":"Deny","Principal":"*","NotAction":"s3:Get*","Resource":"*"}}`, "", 0},
		{"valid, 2008 version", `{"Version":"2008-10-17","Statement":[{"Effect":"Allow","Principal":"*","Action":"*","Resource":"*"}]}`, "", 0},
		{"empty", ``, NotAPolicy, -1},
		{"not json", `not json`, NotAPolicy, -1},
		{"json that is not a policy", `{"hello":1}`, NoStatement, -1},
		{"an array", `[1,2]`, NotAPolicy, -1},
		{"no statement", `{"Version":"2012-10-17","Statement":[]}`, NoStatement, -1},
		{"unknown version", `{"Version":"2020-01-01","Statement":[{"Effect":"Allow","Principal":"*","Action":"*","Resource":"*"}]}`, BadVersion, -1},
		{"bad effect", `{"Statement":[{"Effect":"Maybe","Principal":"*","Action":"*","Resource":"*"}]}`, BadEffect, 0},
		{"no action", `{"Statement":[{"Effect":"Allow","Principal":"*","Resource":"*"}]}`, NoAction, 0},
		{"no principal", `{"Statement":[{"Effect":"Allow","Action":"*","Resource":"*"}]}`, NoPrincipal, 0},
		{"no resource, second statement", `{"Statement":[{"Effect":"Allow","Principal":"*","Action":"*","Resource":"*"},{"Effect":"Allow","Principal":"*","Action":"*"}]}`, NoResource, 1},
		{"Action and NotAction", `{"Statement":[{"Effect":"Allow","Principal":"*","Action":"a:b","NotAction":"c:d","Resource":"*"}]}`, BothOfAPair, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := ValidateResourcePolicy(tc.raw)
			switch {
			case tc.kind == "" && p != nil:
				t.Fatalf("a valid policy was refused: %v", p)
			case tc.kind != "" && p == nil:
				t.Fatalf("want %s, got nil", tc.kind)
			case p != nil && (p.Kind != tc.kind || p.Statement != tc.stmt):
				t.Fatalf("got %s at statement %d (%q), want %s at %d", p.Kind, p.Statement, p.Detail, tc.kind, tc.stmt)
			}
		})
	}
}
