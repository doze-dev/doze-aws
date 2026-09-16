package cloudformation

import (
	"testing"

	"github.com/doze-dev/doze-aws/internal/dozetest"
)

// Nothing this package started may still be running when its tests finish.
// See internal/dozetest for why this is a standing assertion rather than a test.
func TestMain(m *testing.M) { dozetest.Main(m) }
