package sns

// A request is validated in every element of its lists (B13).
//
// The rebuilt request kept one element of a list, chosen by the order a map
// iterated in, so a TagResource with a valid first tag and an invalid second
// was accepted most of the time. The violation is never in element 1 here, and
// each request is sent twenty times: before the fix every one of these was
// accepted on some of them.

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/doze-dev/doze-aws/internal/dozetest"
)

func tag(k, v string) map[string]any { return map[string]any{"Key": k, "Value": v} }

func TestSNSValidatesEveryTagOfARequest(t *testing.T) {
	ts := snsServer(t)
	setUpFixture(t, ts)
	longKey, longValue := strings.Repeat("k", 129), strings.Repeat("v", 257)

	// The control: three valid tags are accepted, so what follows is refused
	// for the element named and nothing else.
	if code, resp := call(t, ts, "TagResource", map[string]any{"ResourceArn": auditTopic,
		"Tags": []any{tag("env", "dev"), tag("team", "core"), tag("cost", "x")}}); code != http.StatusOK {
		t.Fatalf("three valid tags: %d %s", code, resp)
	}
	tagResource := func(tags ...any) func(int) (int, string) {
		return func(int) (int, string) {
			return call(t, ts, "TagResource", map[string]any{"ResourceArn": auditTopic, "Tags": tags})
		}
	}
	dozetest.RefusedEveryTime(t, "TagResource, second key over 128", "tags.2.key",
		tagResource(tag("env", "dev"), tag(longKey, "x")))
	dozetest.RefusedEveryTime(t, "TagResource, third key over 128", "tags.3.key",
		tagResource(tag("env", "dev"), tag("team", "core"), tag(longKey, "x")))
	dozetest.RefusedEveryTime(t, "TagResource, second value over 256", "tags.2.value",
		tagResource(tag("env", "dev"), tag("team", longValue)))
	// The elements used to be merged, so a tag with no key had its neighbour's.
	dozetest.RefusedEveryTime(t, "TagResource, second tag has no key", "tags.2.key",
		tagResource(tag("env", "dev"), map[string]any{"Value": "x"}))

	if code, resp := call(t, ts, "CreateTopic", map[string]any{"Name": "whole-control",
		"Tags": []any{tag("env", "dev"), tag("team", "core"), tag("cost", "x")}}); code != http.StatusOK {
		t.Fatalf("CreateTopic with three valid tags: %d %s", code, resp)
	}
	createTopic := func(tags ...any) func(int) (int, string) {
		return func(run int) (int, string) {
			return call(t, ts, "CreateTopic", map[string]any{"Name": "whole-" + strconv.Itoa(run), "Tags": tags})
		}
	}
	dozetest.RefusedEveryTime(t, "CreateTopic, second key over 128", "tags.2.key",
		createTopic(tag("env", "dev"), tag(longKey, "x")))
	dozetest.RefusedEveryTime(t, "CreateTopic, third value over 256", "tags.3.value",
		createTopic(tag("env", "dev"), tag("team", "core"), tag("cost", longValue)))
}
