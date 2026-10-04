package lambda

// Lambda's control plane, routed by the table generated from AWS's model.
//
// routes (validate.go) says which operation each method and path is; this file
// turns it into a chi router, adds the handful of paths the model does not
// list, and says which handler serves each operation. A request is matched
// once: validation, the IAM guard and the handler all read the operation off
// its context, where each used to work it out again from the path.

import (
	"net/http"
	"strings"
	"sync"

	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/restroute"
)

// routeSpecs is every route Lambda serves, without handlers: the model's
// table, then what it leaves out. Operations sharing a method and path are
// tried in order, so the one with a Pick comes first.
var routeSpecs = sync.OnceValue(func() []restroute.Route {
	out := []restroute.Route{
		// GetLayerVersionByArn is GET /layers?find=LayerVersion&Arn=… — the one
		// operation in the family addressed by query rather than path. It shares
		// ListLayers' method and path, so it has to be asked first.
		{Op: "GetLayerVersionByArn", Method: "GET", Pattern: "/2018-10-31/layers",
			Pick: func(r *http.Request) bool { return r.URL.Query().Get("Arn") != "" }},
	}
	for _, rt := range routes {
		out = append(out, restroute.Route{
			Op: rt.Op, Method: rt.Method, Pattern: restroute.Pattern(rt.Segs, rt.Labels, false),
		})
	}
	// Served, but not in the model's table (which lists 47 of Lambda's 85
	// operations): the layer-version policy, account settings, the spelling of
	// RemovePermission that carries its statement in the query, some clients'
	// /layers/{arn}, and doze's own runtime probe.
	return append(out,
		restroute.Route{Op: "GetLayerVersionByArn", Method: "GET", Pattern: "/2018-10-31/layers/{LayerName}"},
		restroute.Route{Op: "AddLayerVersionPermission", Method: "POST", Pattern: "/2018-10-31/layers/{LayerName}/versions/{VersionNumber}/policy"},
		restroute.Route{Op: "GetLayerVersionPolicy", Method: "GET", Pattern: "/2018-10-31/layers/{LayerName}/versions/{VersionNumber}/policy"},
		restroute.Route{Op: "RemoveLayerVersionPermission", Method: "DELETE", Pattern: "/2018-10-31/layers/{LayerName}/versions/{VersionNumber}/policy/{StatementId}"},
		restroute.Route{Op: "RemoveLayerVersionPermission", Method: "DELETE", Pattern: "/2018-10-31/layers/{LayerName}/versions/{VersionNumber}/policy"},
		restroute.Route{Op: "RemovePermission", Method: "DELETE", Pattern: "/2015-03-31/functions/{FunctionName}/policy"},
		restroute.Route{Op: "GetAccountSettings", Method: "GET", Pattern: "/2016-08-19/account-settings"},
		restroute.Route{Op: "DozeRuntime", Method: "GET", Pattern: "/2015-03-31/functions/{FunctionName}/doze-runtime"},
	)
})

// handlers says which handler serves each operation. A handler reads its path
// labels with restroute.Param; fnRef and fnName fold the qualifier a function
// reference may carry.
func (s *Server) handlers() map[string]restroute.Handler {
	version := func(h func(w http.ResponseWriter, r *http.Request, layer string, v int64) *awshttp.APIError) restroute.Handler {
		return func(w http.ResponseWriter, r *http.Request) *awshttp.APIError {
			v, aerr := layerVersionNumber(r)
			if aerr != nil {
				return aerr
			}
			return h(w, r, restroute.Param(r, "LayerName"), v)
		}
	}
	named := func(h func(w http.ResponseWriter, name string) *awshttp.APIError) restroute.Handler {
		return func(w http.ResponseWriter, r *http.Request) *awshttp.APIError { return h(w, fnName(r)) }
	}
	namedReq := func(h func(w http.ResponseWriter, r *http.Request, name string) *awshttp.APIError) restroute.Handler {
		return func(w http.ResponseWriter, r *http.Request) *awshttp.APIError { return h(w, r, fnName(r)) }
	}
	// latestOnly refuses a qualifier on an operation that only ever changes
	// $LATEST, as AWS does.
	latestOnly := func(h func(w http.ResponseWriter, r *http.Request, name string) *awshttp.APIError) restroute.Handler {
		return func(w http.ResponseWriter, r *http.Request) *awshttp.APIError {
			name, qualifier := fnRef(r)
			if aerr := onlyLatest(qualifier); aerr != nil {
				return aerr
			}
			return h(w, r, name)
		}
	}
	alias := func(h func(w http.ResponseWriter, name, alias string) *awshttp.APIError) restroute.Handler {
		return func(w http.ResponseWriter, r *http.Request) *awshttp.APIError {
			return h(w, fnName(r), restroute.Param(r, "Name"))
		}
	}
	mapping := func(h func(w http.ResponseWriter, uuid string) *awshttp.APIError) restroute.Handler {
		return func(w http.ResponseWriter, r *http.Request) *awshttp.APIError {
			return h(w, restroute.Param(r, "UUID"))
		}
	}
	return map[string]restroute.Handler{
		// functions
		"CreateFunction": s.createFunction,
		"ListFunctions":  func(w http.ResponseWriter, r *http.Request) *awshttp.APIError { return s.listFunctions(w) },
		"GetFunction": func(w http.ResponseWriter, r *http.Request) *awshttp.APIError {
			name, q := fnRef(r)
			return s.getFunction(w, name, q)
		},
		"DeleteFunction": func(w http.ResponseWriter, r *http.Request) *awshttp.APIError {
			name, q := fnRef(r)
			return s.deleteFunction(w, name, q)
		},
		"Invoke": func(w http.ResponseWriter, r *http.Request) *awshttp.APIError {
			name, q := fnRef(r)
			return s.invoke(w, r, name, q)
		},
		"GetFunctionConfiguration": func(w http.ResponseWriter, r *http.Request) *awshttp.APIError {
			name, q := fnRef(r)
			return s.getConfiguration(w, name, q)
		},
		"UpdateFunctionConfiguration": latestOnly(s.updateConfiguration),
		"UpdateFunctionCode":          latestOnly(s.updateCode),
		"PublishVersion":              namedReq(s.publishVersion),
		"ListVersionsByFunction":      named(s.listVersions),
		"DozeRuntime":                 named(s.dozeRuntime),

		// aliases
		"CreateAlias": namedReq(s.createAlias),
		"ListAliases": named(s.listAliases),
		"GetAlias":    alias(s.getAlias),
		"UpdateAlias": func(w http.ResponseWriter, r *http.Request) *awshttp.APIError {
			return s.updateAlias(w, r, fnName(r), restroute.Param(r, "Name"))
		},
		"DeleteAlias": alias(s.deleteAlias),

		// the function's resource policy
		"AddPermission": namedReq(s.addPermission),
		"GetPolicy":     named(s.getPolicy),
		"RemovePermission": func(w http.ResponseWriter, r *http.Request) *awshttp.APIError {
			return s.removePermission(w, fnName(r), statementID(r))
		},

		// concurrency, code signing, async config, function URLs
		"PutFunctionConcurrency":    namedReq(s.putConcurrency),
		"GetFunctionConcurrency":    named(s.getConcurrency),
		"DeleteFunctionConcurrency": named(s.deleteConcurrency),

		"GetFunctionCodeSigningConfig":    named(s.getCodeSigning),
		"PutFunctionCodeSigningConfig":    namedReq(s.putCodeSigning),
		"DeleteFunctionCodeSigningConfig": named(s.deleteCodeSigning),

		"ListFunctionEventInvokeConfigs":  named(s.listEventInvokeConfigs),
		"GetFunctionEventInvokeConfig":    named(s.getEventInvokeConfig),
		"DeleteFunctionEventInvokeConfig": named(s.deleteEventInvokeConfig),
		"PutFunctionEventInvokeConfig": func(w http.ResponseWriter, r *http.Request) *awshttp.APIError {
			return s.putEventInvokeConfig(w, r, fnName(r), true)
		},
		"UpdateFunctionEventInvokeConfig": func(w http.ResponseWriter, r *http.Request) *awshttp.APIError {
			return s.putEventInvokeConfig(w, r, fnName(r), false)
		},

		"CreateFunctionUrlConfig": func(w http.ResponseWriter, r *http.Request) *awshttp.APIError {
			return s.putFunctionURL(w, r, fnName(r), 201)
		},
		"UpdateFunctionUrlConfig": func(w http.ResponseWriter, r *http.Request) *awshttp.APIError {
			return s.putFunctionURL(w, r, fnName(r), 200)
		},
		"GetFunctionUrlConfig":    namedReq(s.getFunctionURL),
		"ListFunctionUrlConfigs":  namedReq(s.listFunctionURLs),
		"DeleteFunctionUrlConfig": named(s.deleteFunctionURL),

		// event source mappings
		"CreateEventSourceMapping": s.createMapping,
		"ListEventSourceMappings":  s.listMappings,
		"GetEventSourceMapping":    mapping(s.getMapping),
		"DeleteEventSourceMapping": mapping(s.deleteMapping),
		"UpdateEventSourceMapping": func(w http.ResponseWriter, r *http.Request) *awshttp.APIError {
			return s.updateMapping(w, r, restroute.Param(r, "UUID"))
		},

		// tags
		"ListTags":      s.listTags,
		"TagResource":   s.tagResource,
		"UntagResource": s.untagResource,

		// layers
		"ListLayers": func(w http.ResponseWriter, r *http.Request) *awshttp.APIError { return s.listLayers(w) },
		"GetLayerVersionByArn": func(w http.ResponseWriter, r *http.Request) *awshttp.APIError {
			// The query form carries the ARN in ?Arn=; the path form in the label.
			// Anything else under /layers/{x} is not a layer ARN and is no route.
			arn := r.URL.Query().Get("Arn")
			if arn == "" {
				arn = restroute.Param(r, "LayerName")
				if !strings.HasPrefix(arn, "arn:") {
					return awshttp.Errf(404, "ResourceNotFoundException", "unknown layers subresource")
				}
			}
			return s.getLayerVersionByARN(w, arn)
		},
		"ListLayerVersions": func(w http.ResponseWriter, r *http.Request) *awshttp.APIError {
			return s.listLayerVersions(w, restroute.Param(r, "LayerName"))
		},
		"PublishLayerVersion": func(w http.ResponseWriter, r *http.Request) *awshttp.APIError {
			return s.publishLayerVersion(w, r, restroute.Param(r, "LayerName"))
		},
		"GetLayerVersion": version(func(w http.ResponseWriter, _ *http.Request, layer string, v int64) *awshttp.APIError {
			return s.getLayerVersion(w, layer, v)
		}),
		"DeleteLayerVersion": version(func(w http.ResponseWriter, _ *http.Request, layer string, v int64) *awshttp.APIError {
			return s.deleteLayerVersion(w, layer, v)
		}),
		"AddLayerVersionPermission": version(s.addLayerVersionPermission),
		"GetLayerVersionPolicy": version(func(w http.ResponseWriter, _ *http.Request, layer string, v int64) *awshttp.APIError {
			return s.getLayerVersionPolicy(w, layer, v)
		}),
		"RemoveLayerVersionPermission": version(func(w http.ResponseWriter, r *http.Request, layer string, v int64) *awshttp.APIError {
			return s.removeLayerVersionPermission(w, layer, v, statementID(r))
		}),

		"GetAccountSettings": func(w http.ResponseWriter, r *http.Request) *awshttp.APIError { return s.accountSettings(w) },
	}
}

// refuse logs and writes an error the way every Lambda refusal is.
func (s *Server) refuse(w http.ResponseWriter, r *http.Request, aerr *awshttp.APIError) {
	s.logf("lambda: %s %s -> %s", r.Method, r.URL.Path, aerr.Code)
	writeError(w, aerr)
}

// notFound and notAllowed are the router's own refusals.
func notFound(r *http.Request) *awshttp.APIError {
	return awshttp.Errf(404, "ResourceNotFoundException", "unknown path %q", r.URL.Path)
}

func notAllowed(r *http.Request) *awshttp.APIError {
	return awshttp.Errf(405, "MethodNotAllowed", "unsupported method %s on %s", r.Method, r.URL.Path)
}

func (s *Server) buildRouter() *restroute.Router {
	h := s.handlers()
	specs := append([]restroute.Route(nil), routeSpecs()...)
	for i := range specs {
		specs[i].Handler = h[specs[i].Op]
	}
	return restroute.Build(specs, restroute.Options{
		OnError:          s.refuse,
		Tolerant:         true,
		NotFound:         notFound,
		MethodNotAllowed: notAllowed,
		// After matching, so the operation is known: validation first, then the
		// IAM guard, as before.
		Use: []func(http.Handler) http.Handler{s.validateMiddleware, s.guardMiddleware},
	})
}

func (s *Server) validateMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if aerr := validateRequest(r); aerr != nil {
			s.refuse(w, r, aerr)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) guardMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if aerr := s.guardRequest(w, r); aerr != nil {
			s.refuse(w, r, aerr)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// opsOnly names operations for callers with no Server — the console's wire
// page. It is the same routes with no handlers behind them.
var opsOnly = sync.OnceValue(func() *restroute.Router {
	return restroute.Build(routeSpecs(), restroute.Options{
		OnError:          func(http.ResponseWriter, *http.Request, *awshttp.APIError) {},
		Tolerant:         true,
		NotFound:         notFound,
		MethodNotAllowed: notAllowed,
	})
})

// OperationFor reports the Lambda operation a request addresses, or "" when no
// route matches. Exported for the console's traffic classifier.
func OperationFor(r *http.Request) string { return opsOnly().Op(r) }
