package sqs

import (
	"strconv"

	"github.com/doze-dev/doze-aws/internal/settings"
)

// SQS's settings, for the console's teaching lines and its form constraints.
//
// The numbers are not restated here: the ranges are attrRange, the defaults
// are the def* constants, and this file reads them, so the record and the rule
// that enforces it are the same value. What this file adds is what the
// enforcing code has no place to say: the unit, the tier, and where the claim
// comes from. SQS's published model states almost no ranges, so every range
// here is Doc, taken from the API reference's attribute descriptions.
func init() {
	settings.Register("sqs", func() []settings.Setting {
		span := func(name string) (*int64, *int64) {
			r := attrRange[name]
			return settings.Int64(int64(r[0])), settings.Int64(int64(r[1]))
		}
		functional := map[string]string{"CreateQueue": "F", "SetQueueAttributes": "F"}
		cosmetic := map[string]string{"CreateQueue": "C", "SetQueueAttributes": "C"}

		var out []settings.Setting
		add := func(name, unit, def string) {
			lo, hi := span(name)
			out = append(out, settings.Setting{
				Name: name, Unit: unit, Min: lo, Max: hi, Default: def,
				Tier: functional, EnforcedBy: "sqs/attrs.go attrRange", Source: settings.Doc,
			})
		}
		add("VisibilityTimeout", "seconds", strconv.Itoa(defVisibilityTimeout))
		add("DelaySeconds", "seconds", "0")
		add("MessageRetentionPeriod", "seconds", strconv.Itoa(defRetentionPeriod))
		add("MaximumMessageSize", "bytes", strconv.Itoa(defMaxMessageSize))
		add("ReceiveMessageWaitTimeSeconds", "seconds", "0")

		// Stored attributes. These three change behaviour: a dead-letter
		// queue's RedriveAllowPolicy is checked when a queue names it, an access
		// policy is validated and evaluated, and the deduplication scope decides
		// what a duplicate is. FifoThroughputLimit is the only one with no local
		// effect (nothing here is throttled), and is validated and returned.
		behaves := map[string]string{
			"RedriveAllowPolicy": "sqs/attrs.go redriveAllowed",
			"Policy":             "sqs/attrs.go, sqs/guard.go",
			"DeduplicationScope": "sqs/store.go sendIn",
		}
		for name := range storedAttrs {
			if where, ok := behaves[name]; ok {
				out = append(out, settings.Setting{
					Name: name, Tier: functional, EnforcedBy: where, Source: settings.Doc,
					Enum: map[string][]string{"DeduplicationScope": {"queue", "messageGroup"}}[name],
				})
				continue
			}
			where := "sqs/attrs.go storedAttrs"
			out = append(out, settings.Setting{
				Name: name, Tier: cosmetic, EnforcedBy: where, Source: settings.Doc,
				Enum: map[string][]string{"FifoThroughputLimit": {"perQueue", "perMessageGroupId"}}[name],
			})
		}
		return out
	})
}
