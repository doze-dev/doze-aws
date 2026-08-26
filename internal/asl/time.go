package asl

import "time"

// isRFC3339 reports whether s is an ISO-8601 timestamp of the shape ASL's
// Timestamp comparisons require. AWS is strict here: "2016-08-18" is not a
// valid ASL timestamp even though it is a valid date, because the comparison
// operators need a fully qualified instant to order against.
func isRFC3339(s string) bool {
	_, err := time.Parse(time.RFC3339, s)
	return err == nil
}
