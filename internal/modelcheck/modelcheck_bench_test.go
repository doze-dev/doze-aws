package modelcheck

// ValidateMap runs on every request of every service, walking that
// operation's whole constraint table before the handler sees the body. It is
// the one piece of code on every hot path in the tree, so it is the one worth
// measuring first.
//
// The tables are not small: DynamoDB's largest is 277 constraints, Lambda's
// 464. These benchmarks use tables of realistic shape rather than a token
// one, because the cost is per-constraint and a three-constraint table would
// measure nothing.

import (
	"strconv"
	"testing"
)

// table builds a constraint table of n entries with the mix the real ones
// have: mostly lengths, some patterns and enums, a few ranges.
func table(n int) []Constraint {
	pat := Pattern(`^[A-Za-z0-9_.-]+$`)
	enum := []string{"STANDARD", "INFREQUENT_ACCESS", "DELIVERY"}
	out := make([]Constraint, 0, n)
	for i := range n {
		field := "field" + strconv.Itoa(i)
		switch i % 5 {
		case 0:
			out = append(out, Constraint{Path: field, Kind: KindRequired})
		case 1, 2:
			out = append(out, Constraint{Path: field, Kind: KindLength, Min: 1, Max: 255})
		case 3:
			out = append(out, Constraint{Path: field, Kind: KindPattern, Pat: pat})
		default:
			out = append(out, Constraint{Path: field, Kind: KindEnum, Enum: enum})
		}
	}
	return out
}

// body is a request carrying a value for every constraint, which is the
// expensive case: a missing field short-circuits, a present one is checked.
func body(n int) map[string]any {
	out := make(map[string]any, n)
	for i := range n {
		field := "field" + strconv.Itoa(i)
		if i%5 == 4 {
			out[field] = "STANDARD"
		} else {
			out[field] = "a-plausible-value"
		}
	}
	return out
}

func BenchmarkValidateMap(b *testing.B) {
	for _, n := range []int{16, 64, 277, 464} {
		b.Run("constraints="+strconv.Itoa(n), func(b *testing.B) {
			t, raw := table(n), body(n)
			b.ReportAllocs()
			for b.Loop() {
				if aerr := ValidateMap(raw, t); aerr != nil {
					b.Fatalf("the benchmark body should be valid: %v", aerr)
				}
			}
		})
	}
}

// The refusal path, which is what a wrong request costs. Worth its own
// number: it builds an error and formats a message, where the happy path
// allocates nothing it did not already have.
func BenchmarkValidateMapRefusal(b *testing.B) {
	t := table(277)
	raw := body(277)
	raw["field3"] = "not a valid pattern!!" // trips the KindPattern entry
	b.ReportAllocs()
	for b.Loop() {
		if aerr := ValidateMap(raw, t); aerr == nil {
			b.Fatal("the benchmark body should be refused")
		}
	}
}

// FromQuery rebuilds a flattened Query form into the shape the table walks.
// Every request from a v1-era SDK pays it.
func BenchmarkFromQuery(b *testing.B) {
	vals := map[string][]string{
		"Action":  {"PutMetricData"},
		"Version": {"2010-08-01"},
	}
	for i := range 20 {
		n := strconv.Itoa(i + 1)
		vals["MetricData.member."+n+".MetricName"] = []string{"Probe"}
		vals["MetricData.member."+n+".Value"] = []string{"1.5"}
		vals["MetricData.member."+n+".Dimensions.member.1.Name"] = []string{"Stage"}
		vals["MetricData.member."+n+".Dimensions.member.1.Value"] = []string{"prod"}
	}
	b.ReportAllocs()
	for b.Loop() {
		if got := FromQuery(vals); len(got) == 0 {
			b.Fatal("empty result")
		}
	}
}
