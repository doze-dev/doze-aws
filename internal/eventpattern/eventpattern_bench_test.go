package eventpattern

// Pattern matching is the one place in EventBridge where cost scales with
// something the user controls: every event published is matched against every
// rule on the bus, so a stack with thirty rules pays this thirty times per
// PutEvents.
//
// Parse and Match are separated on purpose. Parse happens once per rule when
// the rule is created or its cache is invalidated; Match happens per event
// per rule. The one that matters is Match.

import "testing"

const orderEvent = `{
  "version": "0",
  "id": "c1a2b3d4-0000-1111-2222-333344445555",
  "detail-type": "order.placed",
  "source": "shop.orders",
  "account": "000000000000",
  "time": "2026-09-10T12:00:00Z",
  "region": "us-east-1",
  "resources": ["arn:aws:sqs:us-east-1:000000000000:orders"],
  "detail": {
    "orderId": "o-1029",
    "total": 149.5,
    "currency": "GBP",
    "customer": {"id": "c-77", "tier": "gold"},
    "items": [{"sku": "a-1", "qty": 2}, {"sku": "b-2", "qty": 1}]
  }
}`

// Three patterns of rising cost, all shapes real rules take.
var patterns = map[string]string{
	// The common one: match on source, which fails fast for most events.
	"source": `{"source": ["shop.orders"]}`,
	// A content filter: numeric comparison plus a nested field.
	"numeric": `{"source": ["shop.orders"],
	              "detail": {"total": [{"numeric": [">", 100]}],
	                         "customer": {"tier": ["gold", "platinum"]}}}`,
	// The expensive shape: prefix and anything-but over several fields.
	"complex": `{"source": [{"prefix": "shop."}],
	             "detail-type": ["order.placed", "order.updated"],
	             "detail": {"currency": [{"anything-but": ["USD"]}],
	                        "orderId": [{"prefix": "o-"}],
	                        "total": [{"numeric": [">=", 10, "<", 10000]}]}}`,
}

func BenchmarkMatch(b *testing.B) {
	event := []byte(orderEvent)
	for name, src := range patterns {
		p, err := Parse([]byte(src))
		if err != nil {
			b.Fatalf("%s: %v", name, err)
		}
		b.Run(name, func(b *testing.B) {
			b.SetBytes(int64(len(event)))
			b.ReportAllocs()
			for b.Loop() {
				ok, err := p.Match(event)
				if err != nil || !ok {
					b.Fatalf("pattern %s did not match the fixture (%v)", name, err)
				}
			}
		})
	}
}

// A non-matching event is the common case on a busy bus — most rules reject
// most events — so it is worth knowing it is cheaper than a match rather than
// assuming it.
func BenchmarkMatchMiss(b *testing.B) {
	p, err := Parse([]byte(patterns["numeric"]))
	if err != nil {
		b.Fatal(err)
	}
	event := []byte(`{"source":"shop.shipping","detail":{"total":5}}`)
	b.SetBytes(int64(len(event)))
	b.ReportAllocs()
	for b.Loop() {
		if ok, _ := p.Match(event); ok {
			b.Fatal("should not match")
		}
	}
}

func BenchmarkParse(b *testing.B) {
	src := []byte(patterns["complex"])
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Parse(src); err != nil {
			b.Fatal(err)
		}
	}
}
