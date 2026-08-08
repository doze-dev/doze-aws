package cloudformation

import (
	"strings"
	"testing"
)

func newScope() *Scope {
	return &Scope{
		StackName:  "test-stack",
		Parameters: map[string]any{"Env": "prod", "Count": "3"},
		Mappings: map[string]any{
			"RegionMap": map[string]any{
				"us-east-1": map[string]any{"bucket": "east-bucket", "size": float64(10)},
			},
		},
		Conditions: map[string]bool{"IsProd": true, "IsDev": false},
		Refs:       map[string]string{"MyQueue": "orders", "MyBucket": "uploads"},
		Atts: map[string]map[string]string{
			"MyQueue":  {"Arn": "arn:aws:sqs:us-east-1:000000000000:orders", "QueueName": "orders"},
			"MyBucket": {"Arn": "arn:aws:s3:::uploads"},
		},
	}
}

func evalOK(t *testing.T, s *Scope, v any) any {
	t.Helper()
	got, err := s.Eval(v)
	if err != nil {
		t.Fatalf("Eval(%v): %v", v, err)
	}
	return got
}

func evalErr(t *testing.T, s *Scope, v any, wantSubstr string) {
	t.Helper()
	_, err := s.Eval(v)
	if err == nil {
		t.Fatalf("Eval(%v) should have failed", v)
	}
	if !strings.Contains(err.Error(), wantSubstr) {
		t.Fatalf("error %q should mention %q", err, wantSubstr)
	}
}

func TestRefResolution(t *testing.T) {
	s := newScope()
	if got := evalOK(t, s, map[string]any{"Ref": "Env"}); got != "prod" {
		t.Errorf("Ref parameter = %v", got)
	}
	if got := evalOK(t, s, map[string]any{"Ref": "MyQueue"}); got != "orders" {
		t.Errorf("Ref resource = %v", got)
	}
	if got := evalOK(t, s, map[string]any{"Ref": "AWS::Region"}); got != "us-east-1" {
		t.Errorf("Ref pseudo = %v", got)
	}
	if got := evalOK(t, s, map[string]any{"Ref": "AWS::AccountId"}); got != "000000000000" {
		t.Errorf("Ref account = %v", got)
	}
	if got := evalOK(t, s, map[string]any{"Ref": "AWS::StackName"}); got != "test-stack" {
		t.Errorf("Ref stack name = %v", got)
	}
	// An unresolvable Ref must fail loudly rather than yield "".
	evalErr(t, s, map[string]any{"Ref": "Nope"}, "no such parameter or resource")
}

func TestRefNoValueDropsProperty(t *testing.T) {
	s := newScope()
	got := evalOK(t, s, map[string]any{
		"Keep": "yes",
		"Drop": map[string]any{"Ref": "AWS::NoValue"},
	})
	m := got.(map[string]any)
	if _, present := m["Drop"]; present {
		t.Fatal("AWS::NoValue should remove the property entirely")
	}
	if m["Keep"] != "yes" {
		t.Fatal("sibling properties should survive")
	}
}

func TestGetAtt(t *testing.T) {
	s := newScope()
	want := "arn:aws:sqs:us-east-1:000000000000:orders"
	if got := evalOK(t, s, map[string]any{"Fn::GetAtt": []any{"MyQueue", "Arn"}}); got != want {
		t.Errorf("GetAtt list form = %v", got)
	}
	if got := evalOK(t, s, map[string]any{"Fn::GetAtt": "MyQueue.Arn"}); got != want {
		t.Errorf("GetAtt dotted form = %v", got)
	}
	evalErr(t, s, map[string]any{"Fn::GetAtt": []any{"Ghost", "Arn"}}, "no such resource")
	// An unmodelled attribute names what IS available, so the message is useful.
	evalErr(t, s, map[string]any{"Fn::GetAtt": []any{"MyBucket", "DomainName"}}, "does not model that attribute")
}

func TestSub(t *testing.T) {
	s := newScope()
	cases := []struct{ in, want string }{
		{"plain text", "plain text"},
		{"${Env}-app", "prod-app"},
		{"${AWS::Region}/${AWS::AccountId}", "us-east-1/000000000000"},
		{"${MyQueue}", "orders"},
		{"${MyQueue.Arn}", "arn:aws:sqs:us-east-1:000000000000:orders"},
		{"${!NotSubstituted}", "${NotSubstituted}"},
		{"a${Env}b${Env}c", "aprodbprodc"},
		{"trailing ${Env}", "trailing prod"},
	}
	for _, c := range cases {
		if got := evalOK(t, s, map[string]any{"Fn::Sub": c.in}); got != c.want {
			t.Errorf("Sub(%q) = %v, want %q", c.in, got, c.want)
		}
	}

	// The [template, vars] form.
	got := evalOK(t, s, map[string]any{"Fn::Sub": []any{
		"${Greeting} ${Name}",
		map[string]any{"Greeting": "hello", "Name": map[string]any{"Ref": "Env"}},
	}})
	if got != "hello prod" {
		t.Errorf("Sub with vars = %v", got)
	}

	evalErr(t, s, map[string]any{"Fn::Sub": "${Missing}"}, "no such parameter or resource")
}

func TestJoinSelectSplit(t *testing.T) {
	s := newScope()
	if got := evalOK(t, s, map[string]any{"Fn::Join": []any{"-", []any{"a", "b", "c"}}}); got != "a-b-c" {
		t.Errorf("Join = %v", got)
	}
	// Join over a list containing intrinsics.
	if got := evalOK(t, s, map[string]any{"Fn::Join": []any{
		":", []any{map[string]any{"Ref": "Env"}, map[string]any{"Ref": "MyQueue"}},
	}}); got != "prod:orders" {
		t.Errorf("Join with refs = %v", got)
	}
	if got := evalOK(t, s, map[string]any{"Fn::Select": []any{float64(1), []any{"a", "b", "c"}}}); got != "b" {
		t.Errorf("Select = %v", got)
	}
	// Select with a string index, which templates commonly use.
	if got := evalOK(t, s, map[string]any{"Fn::Select": []any{"0", []any{"a", "b"}}}); got != "a" {
		t.Errorf("Select string index = %v", got)
	}
	evalErr(t, s, map[string]any{"Fn::Select": []any{float64(9), []any{"a"}}}, "out of range")

	split := evalOK(t, s, map[string]any{"Fn::Split": []any{",", "x,y,z"}}).([]any)
	if len(split) != 3 || split[2] != "z" {
		t.Errorf("Split = %v", split)
	}
	// The composition templates actually use: Select over Split.
	if got := evalOK(t, s, map[string]any{"Fn::Select": []any{
		float64(1), map[string]any{"Fn::Split": []any{"/", "a/b/c"}},
	}}); got != "b" {
		t.Errorf("Select over Split = %v", got)
	}
}

func TestFindInMap(t *testing.T) {
	s := newScope()
	got := evalOK(t, s, map[string]any{"Fn::FindInMap": []any{"RegionMap", "us-east-1", "bucket"}})
	if got != "east-bucket" {
		t.Errorf("FindInMap = %v", got)
	}
	// Keys may themselves be intrinsics.
	got = evalOK(t, s, map[string]any{"Fn::FindInMap": []any{
		"RegionMap", map[string]any{"Ref": "AWS::Region"}, "bucket",
	}})
	if got != "east-bucket" {
		t.Errorf("FindInMap with Ref key = %v", got)
	}
	evalErr(t, s, map[string]any{"Fn::FindInMap": []any{"Nope", "a", "b"}}, "no mapping named")
	evalErr(t, s, map[string]any{"Fn::FindInMap": []any{"RegionMap", "us-east-1", "ghost"}}, "has no key")
}

func TestConditionFunctions(t *testing.T) {
	s := newScope()
	if got := evalOK(t, s, map[string]any{"Fn::Equals": []any{"a", "a"}}); got != true {
		t.Error("Equals same should be true")
	}
	// CloudFormation compares stringified values.
	if got := evalOK(t, s, map[string]any{"Fn::Equals": []any{float64(1), "1"}}); got != true {
		t.Error("Equals should compare stringified values")
	}
	if got := evalOK(t, s, map[string]any{"Fn::Not": []any{map[string]any{"Fn::Equals": []any{"a", "b"}}}}); got != true {
		t.Error("Not(Equals(a,b)) should be true")
	}
	if got := evalOK(t, s, map[string]any{"Fn::And": []any{true, true}}); got != true {
		t.Error("And(true,true)")
	}
	if got := evalOK(t, s, map[string]any{"Fn::And": []any{true, false}}); got != false {
		t.Error("And(true,false)")
	}
	if got := evalOK(t, s, map[string]any{"Fn::Or": []any{false, true}}); got != true {
		t.Error("Or(false,true)")
	}
	// Fn::If picks a branch and evaluates only that branch.
	if got := evalOK(t, s, map[string]any{"Fn::If": []any{"IsProd", "yes", "no"}}); got != "yes" {
		t.Error("If(IsProd) should take the true branch")
	}
	if got := evalOK(t, s, map[string]any{"Fn::If": []any{"IsDev", "yes", "no"}}); got != "no" {
		t.Error("If(IsDev) should take the false branch")
	}
	// The unselected branch is never evaluated, so a broken Ref there is fine.
	if got := evalOK(t, s, map[string]any{"Fn::If": []any{
		"IsProd", "safe", map[string]any{"Ref": "DoesNotExist"},
	}}); got != "safe" {
		t.Error("If should not evaluate the unselected branch")
	}
	evalErr(t, s, map[string]any{"Fn::If": []any{"Unknown", "a", "b"}}, "undefined condition")
}

// TestEvalConditionsOutOfOrder covers the case that breaks naive
// implementations: a condition defined before the one it depends on.
func TestEvalConditionsOutOfOrder(t *testing.T) {
	s := &Scope{Parameters: map[string]any{"Env": "prod"}}
	err := s.EvalConditions(map[string]any{
		// AlsoProd depends on IsProd, and sorts before it alphabetically.
		"AlsoProd": map[string]any{"Fn::Not": []any{map[string]any{"Condition": "IsNotProd"}}},
		"IsNotProd": map[string]any{"Fn::Not": []any{
			map[string]any{"Fn::Equals": []any{map[string]any{"Ref": "Env"}, "prod"}},
		}},
	})
	if err != nil {
		t.Fatalf("EvalConditions: %v", err)
	}
	if !s.Conditions["AlsoProd"] || s.Conditions["IsNotProd"] {
		t.Fatalf("conditions = %v", s.Conditions)
	}
}

func TestEvalConditionsReportsUnresolvable(t *testing.T) {
	s := &Scope{}
	err := s.EvalConditions(map[string]any{
		"Broken": map[string]any{"Condition": "NeverDefined"},
	})
	if err == nil {
		t.Fatal("an unresolvable condition should error, not loop forever")
	}
}

func TestBase64AndImportValue(t *testing.T) {
	s := newScope()
	if got := evalOK(t, s, map[string]any{"Fn::Base64": "hello"}); got != "aGVsbG8=" {
		t.Errorf("Base64 = %v", got)
	}
	// Without an export registry, ImportValue must fail rather than yield "".
	evalErr(t, s, map[string]any{"Fn::ImportValue": "other-stack-queue"}, "no such export")

	s.Exports = map[string]string{"other-stack-queue": "shared"}
	if got := evalOK(t, s, map[string]any{"Fn::ImportValue": "other-stack-queue"}); got != "shared" {
		t.Errorf("ImportValue with registry = %v", got)
	}
}

func TestNestedIntrinsics(t *testing.T) {
	s := newScope()
	// The shape real templates produce: Sub inside Join inside a property.
	got := evalOK(t, s, map[string]any{
		"Policy": map[string]any{
			"Statement": []any{
				map[string]any{
					"Resource": map[string]any{"Fn::Join": []any{"", []any{
						map[string]any{"Fn::GetAtt": []any{"MyBucket", "Arn"}},
						"/*",
					}}},
					"Sid": map[string]any{"Fn::Sub": "${Env}Access"},
				},
			},
		},
	})
	stmt := got.(map[string]any)["Policy"].(map[string]any)["Statement"].([]any)[0].(map[string]any)
	if stmt["Resource"] != "arn:aws:s3:::uploads/*" {
		t.Errorf("nested Join/GetAtt = %v", stmt["Resource"])
	}
	if stmt["Sid"] != "prodAccess" {
		t.Errorf("nested Sub = %v", stmt["Sid"])
	}
}
