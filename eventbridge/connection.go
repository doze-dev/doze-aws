package eventbridge

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"

	bolt "go.etcd.io/bbolt"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/awsjson"
)

// Connections: how an API destination authenticates.
//
// A connection holds one of three credentials — Basic, an API key header,
// or OAuth client credentials — and optional parameters every invocation
// carries. AWS keeps the secret half in Secrets Manager under
// events!connection/<name>/<uuid>; here it stays in the connection record,
// and SecretArn reports the ARN AWS would mint so a template's GetAtt
// resolves. Describe never returns a secret value, as on AWS.

var connectionsBucket = []byte("connections")

// param is one header, query or body parameter, secret or not.
type param struct {
	Key      string `json:"key"`
	Value    string `json:"value,omitempty"`
	IsSecret bool   `json:"is_secret,omitempty"`
}

// httpParams are the parameters added to every invocation.
type httpParams struct {
	Header []param `json:"header,omitempty"`
	Query  []param `json:"query,omitempty"`
	Body   []param `json:"body,omitempty"`
}

// connection is one stored connection.
type connection struct {
	Name       string      `json:"name"`
	ID         string      `json:"id"` // the uuid segment of the ARN
	Desc       string      `json:"description,omitempty"`
	AuthType   string      `json:"auth_type"` // BASIC | API_KEY | OAUTH_CLIENT_CREDENTIALS
	State      string      `json:"state"`     // AUTHORIZED | DEAUTHORIZED
	Basic      *basicAuth  `json:"basic,omitempty"`
	APIKey     *apiKeyAuth `json:"api_key,omitempty"`
	OAuth      *oauthAuth  `json:"oauth,omitempty"`
	Invocation httpParams  `json:"invocation"`
	CreatedMs  int64       `json:"created_ms"`
	ModifiedMs int64       `json:"modified_ms"`
}

type basicAuth struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type apiKeyAuth struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type oauthAuth struct {
	ClientID     string     `json:"client_id"`
	ClientSecret string     `json:"client_secret"`
	Endpoint     string     `json:"endpoint"`
	Method       string     `json:"method"`
	Params       httpParams `json:"params"`
}

func (c *connection) ARN(id awsident.Identity) string {
	return id.ARN("events", "connection/"+c.Name+"/"+c.ID)
}

func (c *connection) SecretARN(id awsident.Identity) string {
	return id.ARN("secretsmanager", "secret:events!connection/"+c.Name+"/"+c.ID)
}

func newID() string {
	var b [16]byte
	rand.Read(b[:])
	h := hex.EncodeToString(b[:])
	return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

// parseAuthParameters reads AuthParameters (p) as Create/UpdateConnection send
// them; every field of the chosen type is required, as on AWS. top is the
// whole request, for the connectivity block that sits beside AuthParameters.
func parseAuthParameters(authType string, top, p map[string]any) (*basicAuth, *apiKeyAuth, *oauthAuth, httpParams, *awshttp.APIError) {
	var basic *basicAuth
	var key *apiKeyAuth
	var oauth *oauthAuth
	if latticeARN(p, "ConnectivityParameters") != "" || latticeARN(top, "InvocationConnectivityParameters") != "" {
		return nil, nil, nil, httpParams{}, awshttp.Errf(400, "ValidationException",
			"private API destinations through VPC Lattice are cloud infrastructure; doze-aws delivers over the local network")
	}
	switch authType {
	case "BASIC":
		bp, _ := p["BasicAuthParameters"].(map[string]any)
		basic = &basicAuth{Username: awsjson.Str(bp, "Username"), Password: awsjson.Str(bp, "Password")}
		if basic.Username == "" || basic.Password == "" {
			return nil, nil, nil, httpParams{}, awshttp.Errf(400, "ValidationException", "BasicAuthParameters need Username and Password")
		}
	case "API_KEY":
		kp, _ := p["ApiKeyAuthParameters"].(map[string]any)
		key = &apiKeyAuth{Name: awsjson.Str(kp, "ApiKeyName"), Value: awsjson.Str(kp, "ApiKeyValue")}
		if key.Name == "" || key.Value == "" {
			return nil, nil, nil, httpParams{}, awshttp.Errf(400, "ValidationException", "ApiKeyAuthParameters need ApiKeyName and ApiKeyValue")
		}
	case "OAUTH_CLIENT_CREDENTIALS":
		op, _ := p["OAuthParameters"].(map[string]any)
		cp, _ := op["ClientParameters"].(map[string]any)
		oauth = &oauthAuth{
			ClientID: awsjson.Str(cp, "ClientID"), ClientSecret: awsjson.Str(cp, "ClientSecret"),
			Endpoint: awsjson.Str(op, "AuthorizationEndpoint"), Method: awsjson.Str(op, "HttpMethod"),
			Params: parseHTTPParams(op, "OAuthHttpParameters"),
		}
		if oauth.ClientID == "" || oauth.ClientSecret == "" || oauth.Endpoint == "" || oauth.Method == "" {
			return nil, nil, nil, httpParams{}, awshttp.Errf(400, "ValidationException", "OAuthParameters need ClientParameters (ClientID, ClientSecret), AuthorizationEndpoint and HttpMethod")
		}
	default:
		return nil, nil, nil, httpParams{}, awshttp.Errf(400, "ValidationException", "AuthorizationType must be BASIC, API_KEY or OAUTH_CLIENT_CREDENTIALS")
	}
	return basic, key, oauth, parseHTTPParams(p, "InvocationHttpParameters"), nil
}

// latticeARN reads the ResourceConfigurationArn under a connectivity block.
func latticeARN(p map[string]any, key string) string {
	block, _ := p[key].(map[string]any)
	rp, _ := block["ResourceParameters"].(map[string]any)
	return awsjson.Str(rp, "ResourceConfigurationArn")
}

// parseHTTPParams reads a ConnectionHttpParameters block.
func parseHTTPParams(p map[string]any, key string) httpParams {
	block, _ := p[key].(map[string]any)
	read := func(list string) []param {
		var out []param
		items, _ := block[list].([]any)
		for _, it := range items {
			m, ok := it.(map[string]any)
			if !ok {
				continue
			}
			secret, _ := m["IsValueSecret"].(bool)
			out = append(out, param{Key: awsjson.Str(m, "Key"), Value: awsjson.Str(m, "Value"), IsSecret: secret})
		}
		return out
	}
	return httpParams{Header: read("HeaderParameters"), Query: read("QueryStringParameters"), Body: read("BodyParameters")}
}

// httpParamsView renders parameters the way Describe does: a secret's value
// is omitted.
func httpParamsView(h httpParams) map[string]any {
	render := func(ps []param) []any {
		out := []any{}
		for _, p := range ps {
			v := map[string]any{"Key": p.Key, "IsValueSecret": p.IsSecret}
			if !p.IsSecret {
				v["Value"] = p.Value
			}
			out = append(out, v)
		}
		return out
	}
	v := map[string]any{}
	if len(h.Header) > 0 {
		v["HeaderParameters"] = render(h.Header)
	}
	if len(h.Query) > 0 {
		v["QueryStringParameters"] = render(h.Query)
	}
	if len(h.Body) > 0 {
		v["BodyParameters"] = render(h.Body)
	}
	return v
}

// connectionView is the Describe response; the list view is a subset. No
// password, API key value or client secret ever leaves the store.
func connectionView(id awsident.Identity, c *connection, full bool) map[string]any {
	v := map[string]any{
		"Name": c.Name, "ConnectionArn": c.ARN(id), "ConnectionState": c.State,
		"AuthorizationType": c.AuthType,
		"CreationTime":      float64(c.CreatedMs) / 1000, "LastModifiedTime": float64(c.ModifiedMs) / 1000,
		"LastAuthorizedTime": float64(c.ModifiedMs) / 1000,
	}
	if !full {
		return v
	}
	v["SecretArn"] = c.SecretARN(id)
	if c.Desc != "" {
		v["Description"] = c.Desc
	}
	auth := map[string]any{}
	switch {
	case c.Basic != nil:
		auth["BasicAuthParameters"] = map[string]any{"Username": c.Basic.Username}
	case c.APIKey != nil:
		auth["ApiKeyAuthParameters"] = map[string]any{"ApiKeyName": c.APIKey.Name}
	case c.OAuth != nil:
		auth["OAuthParameters"] = map[string]any{
			"ClientParameters":      map[string]any{"ClientID": c.OAuth.ClientID},
			"AuthorizationEndpoint": c.OAuth.Endpoint, "HttpMethod": c.OAuth.Method,
			"OAuthHttpParameters": httpParamsView(c.OAuth.Params),
		}
	}
	if inv := httpParamsView(c.Invocation); len(inv) > 0 {
		auth["InvocationHttpParameters"] = inv
	}
	v["AuthParameters"] = auth
	return v
}

// ---- store ----

func errConnectionNotFound(name string) *awshttp.APIError {
	return awshttp.Errf(400, "ResourceNotFoundException", "Connection %s does not exist", name)
}

func (s *store) CreateConnection(c connection) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists(connectionsBucket)
		if err != nil {
			return err
		}
		if b.Get([]byte(c.Name)) != nil {
			return awshttp.Errf(400, "ResourceAlreadyExistsException", "Connection %s already exists", c.Name)
		}
		raw, _ := json.Marshal(c)
		return b.Put([]byte(c.Name), raw)
	})
}

func (s *store) GetConnection(name string) (*connection, error) {
	var out *connection
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(connectionsBucket)
		if b == nil {
			return errConnectionNotFound(name)
		}
		raw := b.Get([]byte(name))
		if raw == nil {
			return errConnectionNotFound(name)
		}
		var c connection
		if err := json.Unmarshal(raw, &c); err != nil {
			return err
		}
		out = &c
		return nil
	})
	return out, err
}

// UpdateConnection applies fn under the write lock.
func (s *store) UpdateConnection(name string, fn func(*connection) error) (*connection, error) {
	var out *connection
	err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(connectionsBucket)
		if b == nil {
			return errConnectionNotFound(name)
		}
		raw := b.Get([]byte(name))
		if raw == nil {
			return errConnectionNotFound(name)
		}
		var c connection
		if err := json.Unmarshal(raw, &c); err != nil {
			return err
		}
		if err := fn(&c); err != nil {
			return err
		}
		updated, _ := json.Marshal(c)
		out = &c
		return b.Put([]byte(name), updated)
	})
	return out, err
}

func (s *store) DeleteConnection(name string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(connectionsBucket)
		if b == nil || b.Get([]byte(name)) == nil {
			return errConnectionNotFound(name)
		}
		return b.Delete([]byte(name))
	})
}

// ListConnections filters by name prefix and state, sorted by name.
func (s *store) ListConnections(prefix, state string) ([]connection, error) {
	var out []connection
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(connectionsBucket)
		if b == nil {
			return nil
		}
		return b.ForEach(func(_, raw []byte) error {
			var c connection
			if err := json.Unmarshal(raw, &c); err != nil {
				return err
			}
			if (prefix == "" || strings.HasPrefix(c.Name, prefix)) && (state == "" || c.State == state) {
				out = append(out, c)
			}
			return nil
		})
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, err
}
