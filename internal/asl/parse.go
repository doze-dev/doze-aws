package asl

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Parsing a definition into the AST.
//
// The parser's job is shape, not sense. It refuses malformed JSON and a field
// of the wrong JSON type, and it accepts a Wait state carrying a Resource or a
// Task with no Resource at all — because `analyse.go` is what reports those,
// and it can report *all* of them at once, naming each. A parser that failed on
// the first misplaced field would turn a definition with four mistakes into
// four edit-and-retry cycles.
//
// Document order is preserved. Go randomises map iteration, and a validation
// report whose errors move between runs is one nobody can diff.

// Parse reads a state machine definition.
func Parse(raw []byte) (*Definition, error) {
	var probe any
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, fmt.Errorf("the definition is not valid JSON: %w", err)
	}
	return parseDefinition(raw, "")
}

// parseDefinition parses a machine or a sub-machine. at names the enclosing
// state for error messages ("in Map state Process"), empty at the top level.
func parseDefinition(raw []byte, at string) (*Definition, error) {
	return parseSubDefinition(raw, at, "")
}

// parseSubDefinition is parseDefinition for a Parallel branch or Map
// ItemProcessor, which inherits the enclosing machine's QueryLanguage when it
// declares none of its own — the way AWS resolves the dialect for states inside
// a branch. Recording the inherited value here means every Definition answers
// Lang(s) on its own, with no walk back up to the root.
func parseSubDefinition(raw []byte, at string, inherit QueryLanguage) (*Definition, error) {
	var doc struct {
		Comment        string                     `json:"Comment"`
		StartAt        string                     `json:"StartAt"`
		States         map[string]json.RawMessage `json:"States"`
		TimeoutSeconds *float64                   `json:"TimeoutSeconds"`
		Version        string                     `json:"Version"`
		QueryLanguage  string                     `json:"QueryLanguage"`
		// ProcessorConfig is only meaningful on an ItemProcessor; the CDK
		// writes {"Mode":"INLINE"} on every Map it synthesises.
		ProcessorConfig struct {
			Mode          string `json:"Mode"`
			ExecutionType string `json:"ExecutionType"`
		} `json:"ProcessorConfig"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, wrap(at, err)
	}

	d := &Definition{
		Comment:                doc.Comment,
		StartAt:                doc.StartAt,
		TimeoutSeconds:         doc.TimeoutSeconds,
		Version:                doc.Version,
		QueryLanguage:          QueryLanguage(doc.QueryLanguage),
		States:                 make(map[string]*State, len(doc.States)),
		ProcessorMode:          doc.ProcessorConfig.Mode,
		ProcessorExecutionType: doc.ProcessorConfig.ExecutionType,
	}
	if d.QueryLanguage == "" {
		d.QueryLanguage = inherit
	}

	order, err := objectKeys(raw, "States")
	if err != nil {
		return nil, wrap(at, err)
	}
	d.Order = order

	for _, name := range order {
		s, err := parseState(doc.States[name], name, d.QueryLanguage)
		if err != nil {
			return nil, err
		}
		d.States[name] = s
	}
	return d, nil
}

func parseState(raw []byte, name string, inherit QueryLanguage) (*State, error) {
	var doc struct {
		Type          string `json:"Type"`
		Comment       string `json:"Comment"`
		Next          string `json:"Next"`
		End           bool   `json:"End"`
		QueryLanguage string `json:"QueryLanguage"`

		InputPath      *string         `json:"InputPath"`
		OutputPath     *string         `json:"OutputPath"`
		Parameters     json.RawMessage `json:"Parameters"`
		ResultSelector json.RawMessage `json:"ResultSelector"`
		ResultPath     *string         `json:"ResultPath"`

		Arguments json.RawMessage `json:"Arguments"`
		Output    json.RawMessage `json:"Output"`
		Assign    json.RawMessage `json:"Assign"`

		Result json.RawMessage `json:"Result"`

		Resource             string          `json:"Resource"`
		Credentials          json.RawMessage `json:"Credentials"`
		TimeoutSeconds       json.RawMessage `json:"TimeoutSeconds"`
		TimeoutSecondsPath   string          `json:"TimeoutSecondsPath"`
		HeartbeatSeconds     json.RawMessage `json:"HeartbeatSeconds"`
		HeartbeatSecondsPath string          `json:"HeartbeatSecondsPath"`

		Choices []json.RawMessage `json:"Choices"`
		Default string            `json:"Default"`

		Seconds       json.RawMessage `json:"Seconds"`
		SecondsPath   string          `json:"SecondsPath"`
		Timestamp     string          `json:"Timestamp"`
		TimestampPath string          `json:"TimestampPath"`

		Error     string `json:"Error"`
		ErrorPath string `json:"ErrorPath"`
		Cause     string `json:"Cause"`
		CausePath string `json:"CausePath"`

		Branches []json.RawMessage `json:"Branches"`

		ItemProcessor              json.RawMessage `json:"ItemProcessor"`
		Iterator                   json.RawMessage `json:"Iterator"`
		ItemsPath                  string          `json:"ItemsPath"`
		Items                      json.RawMessage `json:"Items"`
		ItemSelector               json.RawMessage `json:"ItemSelector"`
		MaxConcurrency             json.RawMessage `json:"MaxConcurrency"`
		MaxConcurrencyPath         string          `json:"MaxConcurrencyPath"`
		ItemReader                 json.RawMessage `json:"ItemReader"`
		ItemBatcher                json.RawMessage `json:"ItemBatcher"`
		ResultWriter               json.RawMessage `json:"ResultWriter"`
		Label                      string          `json:"Label"`
		ToleratedFailureCount      *float64        `json:"ToleratedFailureCount"`
		ToleratedFailurePercentage *float64        `json:"ToleratedFailurePercentage"`

		Retry []json.RawMessage `json:"Retry"`
		Catch []json.RawMessage `json:"Catch"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("state %q: %w", name, err)
	}

	// An explicit null is meaningful for the three document-shaping paths —
	// `"InputPath": null` discards the document where an absent InputPath means
	// "$" — but encoding/json leaves the pointer nil either way. A pointer to
	// the empty string is the in-AST spelling of "the key was null": no valid
	// path is empty, so the two cannot collide, and checkPaths already treats
	// it as legal.
	var presence map[string]json.RawMessage
	if json.Unmarshal(raw, &presence) == nil {
		nullPath := func(key string, dst **string) {
			if v, ok := presence[key]; ok && *dst == nil && bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
				empty := ""
				*dst = &empty
			}
		}
		nullPath("InputPath", &doc.InputPath)
		nullPath("OutputPath", &doc.OutputPath)
		nullPath("ResultPath", &doc.ResultPath)
	}

	s := &State{
		Name: name, Type: StateType(doc.Type), Comment: doc.Comment,
		Next: doc.Next, End: doc.End, QueryLanguage: QueryLanguage(doc.QueryLanguage),
		InputPath: doc.InputPath, OutputPath: doc.OutputPath,
		Parameters: doc.Parameters, ResultSelector: doc.ResultSelector, ResultPath: doc.ResultPath,
		Arguments: doc.Arguments, Output: doc.Output, Assign: doc.Assign,
		Result:   doc.Result,
		Resource: doc.Resource, Credentials: doc.Credentials,
		TimeoutSecondsPath: doc.TimeoutSecondsPath, HeartbeatSecondsPath: doc.HeartbeatSecondsPath,
		Default:     doc.Default,
		SecondsPath: doc.SecondsPath,
		Timestamp:   doc.Timestamp, TimestampPath: doc.TimestampPath,
		Error: doc.Error, ErrorPath: doc.ErrorPath,
		Cause: doc.Cause, CausePath: doc.CausePath,
		ItemsPath: doc.ItemsPath, Items: doc.Items, ItemSelector: doc.ItemSelector,
		MaxConcurrencyPath: doc.MaxConcurrencyPath,
		ItemReader:         doc.ItemReader, ItemBatcher: doc.ItemBatcher, ResultWriter: doc.ResultWriter, Label: doc.Label,
		ToleratedFailureCount:      doc.ToleratedFailureCount,
		ToleratedFailurePercentage: doc.ToleratedFailurePercentage,
	}
	s.dialect = s.Lang(inherit)

	// The seconds-shaped fields take a number in both dialects and a `{% … %}`
	// string in JSONata. Split here, so the JSONPath interpreter keeps its
	// *float64 and the analyser can refuse a string on a JSONPath state.
	for _, f := range []struct {
		key  string
		raw  json.RawMessage
		num  **float64
		expr *string
	}{
		{"TimeoutSeconds", doc.TimeoutSeconds, &s.TimeoutSecondsState, &s.TimeoutSecondsExpr},
		{"HeartbeatSeconds", doc.HeartbeatSeconds, &s.HeartbeatSeconds, &s.HeartbeatSecondsExpr},
		{"Seconds", doc.Seconds, &s.Seconds, &s.SecondsExpr},
		{"MaxConcurrency", doc.MaxConcurrency, &s.MaxConcurrency, &s.MaxConcurrencyExpr},
	} {
		if err := numberOrExpr(f.raw, f.num, f.expr); err != nil {
			return nil, fmt.Errorf("state %q: %s must be a number or a JSONata expression string", name, f.key)
		}
	}

	for i, rawRule := range doc.Choices {
		rule, err := parseRule(rawRule, fmt.Sprintf("state %q choice %d", name, i))
		if err != nil {
			return nil, err
		}
		s.Choices = append(s.Choices, rule)
	}
	for i, rawBranch := range doc.Branches {
		b, err := parseSubDefinition(rawBranch, fmt.Sprintf("state %q branch %d", name, i), s.Lang(inherit))
		if err != nil {
			return nil, err
		}
		s.Branches = append(s.Branches, b)
	}
	if len(doc.ItemProcessor) > 0 {
		p, err := parseSubDefinition(doc.ItemProcessor, fmt.Sprintf("state %q ItemProcessor", name), s.Lang(inherit))
		if err != nil {
			return nil, err
		}
		s.ItemProcessor = p
	}
	if len(doc.Iterator) > 0 {
		p, err := parseSubDefinition(doc.Iterator, fmt.Sprintf("state %q Iterator", name), s.Lang(inherit))
		if err != nil {
			return nil, err
		}
		s.Iterator = p
	}
	for i, rawR := range doc.Retry {
		var r Retrier
		if err := json.Unmarshal(rawR, &r); err != nil {
			return nil, fmt.Errorf("state %q Retry[%d]: %w", name, i, err)
		}
		s.Retry = append(s.Retry, &r)
	}
	for i, rawC := range doc.Catch {
		var c Catcher
		if err := json.Unmarshal(rawC, &c); err != nil {
			return nil, fmt.Errorf("state %q Catch[%d]: %w", name, i, err)
		}
		s.Catch = append(s.Catch, &c)
	}
	return s, nil
}

// parseRule reads one choice rule, which is either a comparison or a boolean
// combinator. The operator is whichever key is not structural, found by
// consulting the table rather than by trying forty struct fields.
func parseRule(raw []byte, at string) (*ChoiceRule, error) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", at, err)
	}
	r := &ChoiceRule{}
	if err := jsonField(doc, "Variable", &r.Variable, at); err != nil {
		return nil, err
	}
	if err := jsonField(doc, "Next", &r.Next, at); err != nil {
		return nil, err
	}
	r.Condition = doc["Condition"]
	r.Assign = doc["Assign"]

	for _, combinator := range []struct {
		key string
		dst *[]*ChoiceRule
	}{{"And", &r.And}, {"Or", &r.Or}} {
		if rawList, ok := doc[combinator.key]; ok {
			var list []json.RawMessage
			if err := json.Unmarshal(rawList, &list); err != nil {
				return nil, fmt.Errorf("%s: %s: %w", at, combinator.key, err)
			}
			for i, sub := range list {
				parsed, err := parseRule(sub, fmt.Sprintf("%s %s[%d]", at, combinator.key, i))
				if err != nil {
					return nil, err
				}
				*combinator.dst = append(*combinator.dst, parsed)
			}
		}
	}
	if rawNot, ok := doc["Not"]; ok {
		parsed, err := parseRule(rawNot, at+" Not")
		if err != nil {
			return nil, err
		}
		r.Not = parsed
	}

	for key, operand := range doc {
		if ruleStructuralKeys[key] {
			continue
		}
		base, isPath, _, ok := LookupOperator(key)
		if !ok {
			// Not refused here: analyse reports it, alongside everything else
			// wrong with the definition, naming the key.
			continue
		}
		if r.Comparison != nil {
			return nil, fmt.Errorf("%s: two comparison operators (%s and %s); a rule may have only one",
				at, r.Comparison.Op, base)
		}
		r.Comparison = &Comparison{Op: base, Operand: operand, IsPath: isPath}
	}
	return r, nil
}

// jsonField decodes one optional string field, reporting a type mismatch
// against the field name rather than against the whole rule.
func jsonField(doc map[string]json.RawMessage, key string, dst *string, at string) error {
	raw, ok := doc[key]
	if !ok {
		return nil
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return fmt.Errorf("%s: %s must be a string", at, key)
	}
	return nil
}

// objectKeys returns the keys of a nested object in the order the document
// listed them, by re-scanning with a token decoder. encoding/json gives no
// other way to recover order, and order is what makes reports diffable.
func objectKeys(raw []byte, field string) ([]string, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	// Walk to the opening brace of the top-level object.
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, _ := keyTok.(string)
		if key != field {
			var skip json.RawMessage
			if err := dec.Decode(&skip); err != nil {
				return nil, err
			}
			continue
		}
		// The value of States: read its keys in order.
		if _, err := dec.Token(); err != nil { // opening brace
			return nil, err
		}
		var keys []string
		for dec.More() {
			nameTok, err := dec.Token()
			if err != nil {
				return nil, err
			}
			name, _ := nameTok.(string)
			keys = append(keys, name)
			var skip json.RawMessage
			if err := dec.Decode(&skip); err != nil {
				return nil, err
			}
		}
		return keys, nil
	}
	return nil, nil
}

func wrap(at string, err error) error {
	if at == "" {
		return err
	}
	return fmt.Errorf("%s: %w", at, err)
}

// numberOrExpr reads a field that is a number in JSONPath and may be a
// `{% … %}` string in JSONata. null and absent both leave the pair empty.
func numberOrExpr(raw json.RawMessage, num **float64, expr *string) error {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil
	}
	if raw[0] == '"' {
		return json.Unmarshal(raw, expr)
	}
	var n float64
	if err := json.Unmarshal(raw, &n); err != nil {
		return err
	}
	*num = &n
	return nil
}
