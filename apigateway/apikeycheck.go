package apigateway

// The API key gate, after the authorizer and before the integration. A
// method with apiKeyRequired is served only when the request carries a key
// (x-api-key, or the authorizer's usageIdentifierKey when the API's key
// source is AUTHORIZER) that exists, is enabled, and is attached to a usage
// plan covering this API stage. Anything else is 403 {"message":
// "Forbidden"}, as on AWS. Throttle and quota are not enforced.

import "net/http"

// checkAPIKey returns the key the request presented, or nil when the method
// needs none; denied is set when the method needs one and the request did
// not qualify.
func (s *Server) checkAPIKey(api *RestAPI, stage string, m *Method, r *http.Request, cc *callCtx) (key *APIKey, denied *authDenial) {
	if !m.APIKeyRequired {
		return nil, nil
	}
	value := r.Header.Get("x-api-key")
	if api.APIKeySource == "AUTHORIZER" {
		value = ""
		if cc != nil {
			value = cc.UsageKey
		}
	}
	if value == "" {
		return nil, &authDenial{403, "Forbidden"}
	}
	k := s.store.FindAPIKeyByValue(value)
	if k == nil || !k.Enabled {
		return nil, &authDenial{403, "Forbidden"}
	}
	for _, p := range s.store.PlansCovering(api.ID, stage) {
		if p.hasKey(k.ID) {
			return k, nil
		}
	}
	return nil, &authDenial{403, "Forbidden"}
}
