package lambda

// The AWS/Lambda metrics a function's invocations produce.
//
// AWS publishes these for every function without being asked, which is why an
// alarm on Errors is the first alarm most stacks have. Producing them locally
// is what lets that alarm be tested before it is deployed.
//
// Four metrics, dimensioned by FunctionName, and each also published
// undimensioned so an account-wide alarm — "any function erroring" — has
// something to watch, which is how AWS reports them too.

import (
	"errors"
	"time"

	"github.com/doze-dev/doze-aws/internal/lambdaruntime"
	"github.com/doze-dev/doze-aws/internal/metricship"
)

// lambdaNamespace is AWS's, not doze-aws's: an alarm written against the
// cloud names this namespace, and it has to match for the alarm to find
// anything locally.
const lambdaNamespace = "AWS/Lambda"

// recordInvoke publishes the metrics for one completed invocation.
//
// Invocations counts every attempt that ran, matching AWS: a retried async
// invocation is several invocations, because each one consumed a runtime.
// Errors counts a handler that reported failure — not a transport problem,
// which is doze-aws's fault rather than the function's. Throttles is its own
// metric because a throttled call never ran, so counting it as an invocation
// would inflate the denominator of every error-rate alarm.
func (s *Server) recordInvoke(name string, res lambdaruntime.Result, err error, took time.Duration) {
	if s.metrics == nil {
		return
	}
	dims := map[string]string{"FunctionName": name}
	switch {
	case errors.Is(err, errThrottled):
		s.metrics.Count(lambdaNamespace, "Throttles", dims, 1)
		s.metrics.Count(lambdaNamespace, "Throttles", nil, 1)
		return
	case err != nil:
		// A transport or pool failure: the invocation did not complete, so it
		// is not an error the function is responsible for. It still counts as
		// an invocation, because a runtime was consumed.
		s.metrics.Count(lambdaNamespace, "Invocations", dims, 1)
		s.metrics.Count(lambdaNamespace, "Invocations", nil, 1)
		return
	}
	s.metrics.Count(lambdaNamespace, "Invocations", dims, 1)
	s.metrics.Count(lambdaNamespace, "Invocations", nil, 1)
	s.metrics.Duration(lambdaNamespace, "Duration", dims, took)
	if res.FunctionErr != "" {
		s.metrics.Count(lambdaNamespace, "Errors", dims, 1)
		s.metrics.Count(lambdaNamespace, "Errors", nil, 1)
	}
}

// putEMF publishes the metrics an Embedded Metric Format log line carries.
// A function that writes EMF is asking for custom metrics without an SDK
// call, and the line reaching CloudWatch Logs is the whole publishing
// mechanism — see internal/emf.
func (s *Server) putEMF(data []metricship.Datum, namespace string) {
	if s.metrics == nil || len(data) == 0 {
		return
	}
	s.metrics.Put(namespace, data...)
}
