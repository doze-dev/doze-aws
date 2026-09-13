package dozeaws

import "github.com/doze-dev/doze-aws/internal/awshttp"

// FaultSinkForTest exposes a stack's fault sink to the external test package.
//
// Forcing a genuine 5xx through a service means contriving a store failure,
// which is fiddly and couples the test to whichever service was easiest to
// break. The thing under test here is the ROUTING of a fault to the right
// stack, so the test hands a fault to the sink directly and asserts where it
// lands.
func (s *Stack) FaultSinkForTest() func(string, *awshttp.APIError) { return s.recordFault }
