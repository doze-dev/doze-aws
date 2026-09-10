package iampolicy

import "testing"

// The negated and omitted forms. s3/access_public_test.go covers the shapes a
// bucket policy actually takes; these are the ones that reach the other
// branches of grantActions/grantResources and of the deny filter.
func TestIsPublicNegatedAndOmittedMembers(t *testing.T) {
	cases := []struct {
		name   string
		doc    string
		public bool
	}{
		{
			"NotAction allow grants everything else",
			`{"Statement":[{"Effect":"Allow","Principal":"*","NotAction":"s3:DeleteBucket","Resource":"*"}]}`,
			true,
		},
		{
			"NotResource allow grants everything else",
			`{"Statement":[{"Effect":"Allow","Principal":"*","Action":"s3:GetObject","NotResource":"arn:aws:s3:::private/*"}]}`,
			true,
		},
		{
			"a queue-shaped statement with no Resource",
			`{"Statement":[{"Effect":"Allow","Principal":"*","Action":"sqs:SendMessage"}]}`,
			true,
		},
		{
			"a deny using NotAction is not weighed, so the grant stays public",
			`{"Statement":[{"Effect":"Allow","Principal":"*","Action":"s3:GetObject","Resource":"*"},` +
				`{"Effect":"Deny","Principal":"*","NotAction":"s3:PutObject","Resource":"*"}]}`,
			true,
		},
		{
			"a deny with no Resource is not weighed",
			`{"Statement":[{"Effect":"Allow","Principal":"*","Action":"s3:GetObject","Resource":"*"},` +
				`{"Effect":"Deny","Principal":"*","Action":"s3:GetObject"}]}`,
			true,
		},
	}
	for _, c := range cases {
		doc, err := Parse(c.doc)
		if err != nil {
			t.Errorf("%s: parse: %v", c.name, err)
			continue
		}
		if got := IsPublic(doc); got != c.public {
			t.Errorf("%s: IsPublic=%v, want %v", c.name, got, c.public)
		}
	}
}

// Documents Parse will not produce, but IsPublic is exported and takes a
// *Document, so a caller can hand it one built by hand.
func TestIsPublicOnHandBuiltDocuments(t *testing.T) {
	if IsPublic(nil) {
		t.Error("a nil document is not public")
	}
	if IsPublic(&Document{}) {
		t.Error("a document with no statements is not public")
	}
	// No Action and no NotAction: the statement grants no action at all, so
	// there is nothing for a public principal to do.
	noAction := &Document{Statement: []Statement{
		{Effect: "Allow", Principal: principalBlock{Any: true, Present: true}, Resource: stringList{"*"}},
	}}
	if IsPublic(noAction) {
		t.Error("an allow naming no action grants nothing")
	}
	// Parse insists on exact casing; IsPublic folds it, so a hand-built
	// document is read the way the JSON one would be.
	lower := &Document{Statement: []Statement{
		{Effect: "allow", Principal: principalBlock{Any: true, Present: true},
			Action: stringList{"s3:GetObject"}, Resource: stringList{"*"}},
	}}
	if !IsPublic(lower) {
		t.Error("effect casing should fold")
	}
}
