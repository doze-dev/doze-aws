package apigateway

// The AWS/ApiGateway metrics a served request produces.
//
// AWS publishes these for every stage without being asked, and they are what
// an API's first alarm watches — a 5XX rate, or a latency budget. Producing
// them locally is what lets that alarm be tested before it is deployed.
//
// # REST and HTTP APIs do not share metric names
//
// A REST API reports `4XXError`/`5XXError` dimensioned by ApiName and Stage;
// an HTTP API reports `4xx`/`5xx` dimensioned by ApiId and Stage. That is
// AWS's split, not a local one, and copying either shape onto the other would
// mean an alarm that finds nothing — which is the failure this whole batch
// exists to prevent.

import "github.com/doze-dev/doze-aws/internal/metricship"

// gatewayNamespace is AWS's, not doze-aws's: an alarm written against the
// cloud names this namespace, and it has to match to find anything locally.
const gatewayNamespace = "AWS/ApiGateway"

// recordRequest publishes the metrics for one served request.
//
// Latency is the whole time the gateway held the request — AWS's `Latency`,
// which includes the integration rather than excluding it (`IntegrationLatency`
// is the separate metric for the inner half, and doze-aws does not time the
// integration separately, so it is not published rather than published wrong).
func (s *Server) recordRequest(api *RestAPI, stage string, rl *requestLog) {
	if s.metrics == nil {
		return
	}
	status := rl.status
	if status == 0 {
		// A handler that wrote neither a header nor a body: net/http sends
		// 200, so counting it as anything else would invent an error.
		status = 200
	}
	var dims map[string]string
	var name4xx, name5xx string
	if api.Protocol == "HTTP" {
		dims = map[string]string{"ApiId": api.ID, "Stage": stage}
		name4xx, name5xx = "4xx", "5xx"
	} else {
		dims = map[string]string{"ApiName": api.Name, "Stage": stage}
		name4xx, name5xx = "4XXError", "5XXError"
	}

	s.metrics.Count(gatewayNamespace, "Count", dims, 1)
	s.metrics.Duration(gatewayNamespace, "Latency", dims, s.now().Sub(rl.started))
	switch {
	case status >= 500:
		s.metrics.Count(gatewayNamespace, name5xx, dims, 1)
	case status >= 400:
		s.metrics.Count(gatewayNamespace, name4xx, dims, 1)
	}
}

// closeMetrics flushes anything the shipper is still holding, so a stack shut
// down straight after a request does not lose that request's metrics.
func (s *Server) closeMetrics() {
	if s.metrics != nil {
		s.metrics.Close()
	}
}

// newMetrics is separated so New reads as wiring rather than construction.
func newMetrics(s *Server) *metricship.Shipper {
	return metricship.New("apigateway", s.peers, s.logf)
}
