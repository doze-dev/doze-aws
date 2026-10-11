package sqs

import (
	"testing"
	"time"

	"github.com/doze-dev/doze-aws/internal/dozetest"
)

// Nothing this package started may still be running when its tests finish.
// See internal/dozetest for why this is a standing assertion rather than a test.
func TestMain(m *testing.M) {
	// A move task starts after half a second and moves at 500 a second, which
	// is right for a person and slow for a suite. The tests that are about the
	// pacing set it back (see move_pacing_test.go).
	moveStartDelay, moveDefaultRate, moveTick = 0, 100000, 5*time.Millisecond
	dozetest.Main(m)
}
