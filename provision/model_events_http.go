package provision

// EventBridge connections and API destinations: the HTTP half of the rule
// target model. See Stack.Connections and Stack.APIDestinations.

// Connection is how an API destination authenticates: exactly one of Basic,
// APIKey or OAuth, plus parameters every invocation carries. Secrets live in
// the stack as written; an export blanks them, since the service never
// reports a secret back.
type Connection struct {
	Description string
	Basic       *BasicAuth
	APIKey      *APIKeyAuth
	OAuth       *OAuthAuth
	Invocation  ConnectionParams
}

type BasicAuth struct{ Username, Password string }

type APIKeyAuth struct{ Name, Value string }

// OAuthAuth is a client-credentials grant against Endpoint.
type OAuthAuth struct {
	ClientID, ClientSecret string
	Endpoint               string
	Method                 string // GET | POST | PUT
	Params                 ConnectionParams
}

// ConnectionParams are header, query and body parameters added to a request.
type ConnectionParams struct {
	Headers []ConnectionParam
	Query   []ConnectionParam
	Body    []ConnectionParam
}

type ConnectionParam struct {
	Key, Value string
	Secret     bool
}

// APIDestination is an HTTP endpoint a rule can target.
type APIDestination struct {
	Description string
	Connection  string // name of a Connection in the stack
	Endpoint    string // may hold `*` segments filled by the target's PathParams
	Method      string // POST | GET | HEAD | OPTIONS | PUT | PATCH | DELETE
	RateLimit   int    // stored and reported; not enforced locally
}
