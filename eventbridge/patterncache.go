package eventbridge

// Compiled patterns, kept between events.
//
// Every event published is matched against every rule on the bus. The loop
// that does that used to call eventpattern.Parse for each rule, for each
// event, and then Match — which re-decoded the event from JSON as well. So a
// bus with thirty rules parsed thirty patterns and decoded the same event
// thirty times per PutEvents. Measured before the fix: 4.4 µs to parse and
// 3.2 µs to match, per rule, of which the match was almost entirely the
// decode — matching cost the same whatever the pattern was, which is the
// giveaway.
//
// # Keyed by the pattern text, not the rule name
//
// So there is nothing to invalidate. A rule whose pattern changes produces a
// different key and compiles afresh; a rule that is deleted simply stops
// being looked up. The alternative — keying by rule and clearing on write,
// the way logs/fanout.go does for subscription filters — needs a forget()
// call at every mutation site and goes stale the day someone adds a
// sixteenth one.

import (
	"sync"

	"github.com/doze-dev/doze-aws/internal/eventpattern"
)

// maxCachedPatterns bounds the cache. A local stack has tens of rules, not
// thousands; the cap exists so a pathological loop of rule edits cannot grow
// the map without limit, and dropping everything is fine because the only
// cost of a miss is one parse.
const maxCachedPatterns = 512

type patternCache struct {
	mu sync.Mutex
	m  map[string]*eventpattern.Pattern
}

// compiled returns the compiled form of a pattern, parsing it on first sight.
// An unparseable pattern returns an error every time rather than being cached
// as a negative — the error text names what is wrong with it, and a rule with
// a bad pattern is rare enough that re-reporting it is the right trade.
func (c *patternCache) compiled(src string) (*eventpattern.Pattern, error) {
	c.mu.Lock()
	if p, ok := c.m[src]; ok {
		c.mu.Unlock()
		return p, nil
	}
	c.mu.Unlock()

	p, err := eventpattern.Parse([]byte(src))
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[string]*eventpattern.Pattern{}
	}
	if len(c.m) >= maxCachedPatterns {
		c.m = map[string]*eventpattern.Pattern{}
	}
	c.m[src] = p
	return p, nil
}
