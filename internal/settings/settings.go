// Package settings is what the console knows about a service's settings, in one
// place, so the page that teaches a setting and the service that enforces it
// cannot say different things.
//
// A form field that takes a number is shown with its range, its unit, its
// default and a line about what changing it does. Each of those used to be
// written where it was shown: a min and a max on an <input>, a default in a
// placeholder, a unit in a label. They agreed on the day they were typed, and
// nothing kept them agreeing, and several of the services' ranges live in no
// machine-readable source at all (SQS's model states almost none), so there was
// nothing to check a page against.
//
// A Setting is a record of one such value. It is not generated from a model:
// AWS's models carry ranges for some services and almost none for others, so
// each record says where its facts come from (Source), using the repository's
// evidence vocabulary, and a record whose source is only memory says so and
// is not asserted against.
//
// Each service registers a builder; nothing is built until a page asks, so a
// stack that never opens the console pays for a map entry per service and
// nothing else (internal/lightness holds that).
package settings

import (
	"sort"
	"sync"
)

// Source is where a setting's facts came from, in the evidence vocabulary every
// claim in this repository uses. Nothing is asserted against "memory".
type Source string

const (
	// Model: read from the service's own published model.
	Model Source = "model"
	// Recorded: observed against the real service.
	Recorded Source = "recorded"
	// Doc: stated in AWS's documentation.
	Doc Source = "doc"
	// Memory: written from recall and not yet checked.
	Memory Source = "memory"
)

// Setting is one value a service lets a caller configure.
type Setting struct {
	Service string
	// Name is what the API calls it: VisibilityTimeout, not "Visibility".
	Name string
	// Unit is what the number counts: "seconds", "bytes", "messages". Empty for
	// a value that is not a quantity.
	Unit string
	// Min and Max bound a numeric setting, inclusive. Nil when the setting has
	// no stated bound on that side.
	Min, Max *int64
	// Enum lists the accepted values of a setting that takes one of a few.
	Enum []string
	// Default is the value a resource has when none was given, as AWS states it.
	// Empty means there is no default to teach.
	Default string
	// Tier is how real the setting is here, per operation that takes it: F it
	// changes behaviour, C it is accepted and returned and does nothing, S it
	// is refused. A setting can differ by operation (a DynamoDB stream
	// specification is functional on create and update alike, but other
	// settings are create-only), which is why this is a map and not a letter.
	Tier map[string]string
	// EnforcedBy names where the service checks it, as a path and a symbol, so
	// a reader can find the rule the page is quoting.
	EnforcedBy string
	Source     Source
}

// Int64 is a literal's address, for Min and Max.
func Int64(n int64) *int64 { return &n }

var (
	mu       sync.Mutex
	builders = map[string]func() []Setting{}
	built    = map[string][]Setting{}
)

// Register installs a service's builder. It is called from the service's
// package; the builder runs on the first lookup and never before.
func Register(service string, build func() []Setting) {
	mu.Lock()
	defer mu.Unlock()
	builders[service] = build
}

// For returns a service's settings, sorted by name, building them on first use.
// A service that registered nothing has none, and says so with a nil slice.
func For(service string) []Setting {
	mu.Lock()
	defer mu.Unlock()
	if s, ok := built[service]; ok {
		return s
	}
	build, ok := builders[service]
	if !ok {
		return nil
	}
	out := build()
	for i := range out {
		out[i].Service = service
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	built[service] = out
	return out
}

// Lookup finds one setting by its API name.
func Lookup(service, name string) (Setting, bool) {
	for _, s := range For(service) {
		if s.Name == name {
			return s, true
		}
	}
	return Setting{}, false
}

// Built reports whether a service's builder has run, for the test that holds
// the "nothing is built until asked" promise.
func Built(service string) bool {
	mu.Lock()
	defer mu.Unlock()
	_, ok := built[service]
	return ok
}
