package cfn

// What a template requires of its parameters.
//
// A parameter declaration can bound its value — AllowedValues, MinValue and
// MaxValue, MinLength and MaxLength, AllowedPattern — and CloudFormation holds
// a deploy to it before it does anything: the call is refused and no stack is
// made. AllowedValues was parsed here and never looked at; the others were
// not parsed. So a value the template forbade went through to the resource,
// where it either worked — against the template's own word — or failed in the
// middle of a deploy that should never have started.

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"unicode/utf8"
)

// CheckParameters holds the supplied values, and the defaults of anything not
// supplied, to the template's declarations. A parameter with no value at all
// is not this check's business: Transpile reports that.
func (t *Template) CheckParameters(given map[string]string) error {
	names := make([]string, 0, len(t.Parameters))
	for name := range t.Parameters {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		decl := t.Parameters[name]
		value, ok := given[name]
		if !ok {
			if decl.Default == nil {
				continue
			}
			value = fmt.Sprint(decl.Default)
		}
		if err := decl.check(name, value); err != nil {
			return err
		}
	}
	return nil
}

func (p Parameter) check(name, value string) error {
	// A ConstraintDescription is the template author's own words for any of
	// these, and CloudFormation uses them when it has them.
	fail := func(format string, args ...any) error {
		if p.ConstraintDescription != "" {
			return fmt.Errorf("Parameter '%s' failed to satisfy constraint: %s", name, p.ConstraintDescription)
		}
		return fmt.Errorf("Parameter '%s' "+format, append([]any{name}, args...)...)
	}
	if len(p.AllowedValues) > 0 {
		allowed := false
		for _, v := range p.AllowedValues {
			allowed = allowed || fmt.Sprint(v) == value
		}
		if !allowed {
			return fail("must be one of AllowedValues")
		}
	}
	if p.Type == "Number" {
		n, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return fmt.Errorf("Parameter '%s' must be a number", name)
		}
		if p.MinValue != nil && n < *p.MinValue {
			return fail("must be a number not less than %s", trim(*p.MinValue))
		}
		if p.MaxValue != nil && n > *p.MaxValue {
			return fail("must be a number not greater than %s", trim(*p.MaxValue))
		}
	}
	length := utf8.RuneCountInString(value)
	if p.MinLength != nil && length < *p.MinLength {
		return fail("must contain at least %d characters", *p.MinLength)
	}
	if p.MaxLength != nil && length > *p.MaxLength {
		return fail("must contain at most %d characters", *p.MaxLength)
	}
	if p.AllowedPattern != "" {
		// The pattern must match the whole value. One Go cannot compile is
		// CloudFormation's dialect being wider than RE2, not the value being
		// wrong, and is let through.
		if re, err := regexp.Compile("^(?:" + p.AllowedPattern + ")$"); err == nil && !re.MatchString(value) {
			return fail("failed to satisfy constraint: must match pattern %s", p.AllowedPattern)
		}
	}
	return nil
}

// number reads a declaration's numeric bound, written as a number or a string.
func number(v any) *float64 {
	switch n := v.(type) {
	case float64:
		return &n
	case int:
		f := float64(n)
		return &f
	case string:
		if f, err := strconv.ParseFloat(n, 64); err == nil {
			return &f
		}
	}
	return nil
}

func whole(v any) *int {
	if f := number(v); f != nil {
		n := int(*f)
		return &n
	}
	return nil
}

// trim prints a bound the way it was written: 43200, not 43200.000000.
func trim(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }
