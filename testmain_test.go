package dozeaws_test

import (
	"testing"

	"github.com/doze-dev/doze-aws/internal/dozetest"
)

// Nothing this package started may still be running when its tests finish.
//
// See internal/dozetest for why: nine of the thirteen bugs a full audit of this
// tree turned up were lifecycle failures, and not one of the 1,045 tests that
// already existed could see them — every one boots a stack, does its work, and
// stops looking. This makes them all look.
func TestMain(m *testing.M) { dozetest.Main(m) }
