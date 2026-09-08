package eventbridge

import (
	"context"
	"encoding/json"
	"net/url"
	"sort"
	"strings"

	bolt "go.etcd.io/bbolt"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/awsjson"
)

// API destinations: an HTTP endpoint a rule can target, authenticated by a
// connection. Locally the invocation is a real HTTP request (apidest_deliver.go).

var apiDestinationsBucket = []byte("api_destinations")

// ApiDestination is one stored destination.
type ApiDestination struct {
	Name          string `json:"name"`
	ID            string `json:"id"`
	Desc          string `json:"description,omitempty"`
	ConnectionARN string `json:"connection_arn"`
	Endpoint      string `json:"endpoint"` // may contain * path parameters
	Method        string `json:"method"`
	RateLimit     int    `json:"rate_limit,omitempty"` // stored and reported; not enforced
	State         string `json:"state"`                // ACTIVE | INACTIVE
	CreatedMs     int64  `json:"created_ms"`
	ModifiedMs    int64  `json:"modified_ms"`
}

func (d *ApiDestination) ARN() string {
	return awsident.ARN("events", "api-destination/"+d.Name+"/"+d.ID)
}

var httpMethods = map[string]bool{"POST": true, "GET": true, "HEAD": true, "OPTIONS": true, "PUT": true, "PATCH": true, "DELETE": true}

func destinationView(d *ApiDestination, full bool) map[string]any {
	v := map[string]any{
		"Name": d.Name, "ApiDestinationArn": d.ARN(), "ApiDestinationState": d.State,
		"ConnectionArn": d.ConnectionARN, "InvocationEndpoint": d.Endpoint, "HttpMethod": d.Method,
		"CreationTime": float64(d.CreatedMs) / 1000, "LastModifiedTime": float64(d.ModifiedMs) / 1000,
	}
	if d.RateLimit > 0 {
		v["InvocationRateLimitPerSecond"] = d.RateLimit
	}
	if full && d.Desc != "" {
		v["Description"] = d.Desc
	}
	return v
}

// destinationFromARN parses arn:...:api-destination/<name>/<id>; the id
// may be absent (a template names a destination before its id exists).
func destinationFromARN(arn string) (name, id string, ok bool) {
	_, rest, found := strings.Cut(arn, ":api-destination/")
	if !found {
		return "", "", false
	}
	name, id, _ = strings.Cut(rest, "/")
	return name, id, name != ""
}

// parseTargetHTTPParameters reads a target's HttpParameters block.
func parseTargetHTTPParameters(hp map[string]any) *HTTPParameters {
	out := &HTTPParameters{}
	if vals, ok := hp["PathParameterValues"].([]any); ok {
		for _, v := range vals {
			if s, ok := v.(string); ok {
				out.PathParameterValues = append(out.PathParameterValues, s)
			}
		}
	}
	strMap := func(key string) map[string]string {
		m, _ := hp[key].(map[string]any)
		if len(m) == 0 {
			return nil
		}
		out := map[string]string{}
		for k, v := range m {
			if s, ok := v.(string); ok {
				out[k] = s
			}
		}
		return out
	}
	out.HeaderParameters = strMap("HeaderParameters")
	out.QueryStringParameters = strMap("QueryStringParameters")
	return out
}

// targetHTTPParametersView renders the block as ListTargetsByRule does.
func targetHTTPParametersView(hp *HTTPParameters) map[string]any {
	v := map[string]any{}
	if len(hp.PathParameterValues) > 0 {
		v["PathParameterValues"] = hp.PathParameterValues
	}
	if len(hp.HeaderParameters) > 0 {
		v["HeaderParameters"] = hp.HeaderParameters
	}
	if len(hp.QueryStringParameters) > 0 {
		v["QueryStringParameters"] = hp.QueryStringParameters
	}
	return v
}

// ---- handlers ----

func (s *Server) createApiDestination(ctx context.Context, p map[string]any) (any, *awshttp.APIError) {
	d := ApiDestination{
		Name: awsjson.Str(p, "Name"), ID: newID(), Desc: awsjson.Str(p, "Description"),
		ConnectionARN: awsjson.Str(p, "ConnectionArn"), Endpoint: awsjson.Str(p, "InvocationEndpoint"),
		Method: strings.ToUpper(awsjson.Str(p, "HttpMethod")), RateLimit: awsjson.Int(p, "InvocationRateLimitPerSecond", 0),
		State: "ACTIVE", CreatedMs: s.now().UnixMilli(), ModifiedMs: s.now().UnixMilli(),
	}
	if aerr := s.checkDestination(&d); aerr != nil {
		return nil, aerr
	}
	if err := s.store.CreateApiDestination(d); err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	return map[string]any{
		"ApiDestinationArn": d.ARN(), "ApiDestinationState": d.State,
		"CreationTime": float64(d.CreatedMs) / 1000, "LastModifiedTime": float64(d.ModifiedMs) / 1000,
	}, nil
}

// checkDestination validates the endpoint, method and connection. http://
// is accepted, unlike AWS, so a local test server can be a destination.
func (s *Server) checkDestination(d *ApiDestination) *awshttp.APIError {
	if !httpMethods[d.Method] {
		return awshttp.Errf(400, "ValidationException", "HttpMethod must be one of POST, GET, HEAD, OPTIONS, PUT, PATCH, DELETE")
	}
	u, err := url.Parse(d.Endpoint)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return awshttp.Errf(400, "ValidationException", "InvocationEndpoint must be an http or https URL")
	}
	name, _, ok := destinationConnection(d.ConnectionARN)
	if !ok {
		return awshttp.Errf(400, "ValidationException", "ConnectionArn must be a connection ARN")
	}
	if _, err := s.store.GetConnection(name); err != nil {
		return awshttp.AsAPIError(err)
	}
	return nil
}

// destinationConnection parses arn:...:connection/<name>/<id>.
func destinationConnection(arn string) (name, id string, ok bool) {
	_, rest, found := strings.Cut(arn, ":connection/")
	if !found {
		return "", "", false
	}
	name, id, _ = strings.Cut(rest, "/")
	return name, id, name != ""
}

func (s *Server) updateApiDestination(ctx context.Context, p map[string]any) (any, *awshttp.APIError) {
	var check *awshttp.APIError
	d, err := s.store.UpdateApiDestination(awsjson.Str(p, "Name"), func(d *ApiDestination) error {
		if v, ok := p["Description"].(string); ok {
			d.Desc = v
		}
		if v, ok := p["ConnectionArn"].(string); ok {
			d.ConnectionARN = v
		}
		if v, ok := p["InvocationEndpoint"].(string); ok {
			d.Endpoint = v
		}
		if v, ok := p["HttpMethod"].(string); ok {
			d.Method = strings.ToUpper(v)
		}
		if v := awsjson.Int(p, "InvocationRateLimitPerSecond", 0); v > 0 {
			d.RateLimit = v
		}
		d.State = "ACTIVE"
		d.ModifiedMs = s.now().UnixMilli()
		check = s.checkDestination(d)
		if check != nil {
			return check
		}
		return nil
	})
	if check != nil {
		return nil, check
	}
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	return map[string]any{
		"ApiDestinationArn": d.ARN(), "ApiDestinationState": d.State,
		"CreationTime": float64(d.CreatedMs) / 1000, "LastModifiedTime": float64(d.ModifiedMs) / 1000,
	}, nil
}

func (s *Server) deleteApiDestination(ctx context.Context, p map[string]any) (any, *awshttp.APIError) {
	if err := s.store.DeleteApiDestination(awsjson.Str(p, "Name")); err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	return map[string]any{}, nil
}

func (s *Server) describeApiDestination(ctx context.Context, p map[string]any) (any, *awshttp.APIError) {
	d, err := s.store.GetApiDestination(awsjson.Str(p, "Name"))
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	return destinationView(d, true), nil
}

func (s *Server) listApiDestinations(ctx context.Context, p map[string]any) (any, *awshttp.APIError) {
	ds, err := s.store.ListApiDestinations(awsjson.Str(p, "NamePrefix"), awsjson.Str(p, "ConnectionArn"))
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	views := []any{}
	for i := range ds {
		views = append(views, destinationView(&ds[i], false))
	}
	return map[string]any{"ApiDestinations": views}, nil
}

// ---- store ----

func errDestinationNotFound(name string) *awshttp.APIError {
	return awshttp.Errf(400, "ResourceNotFoundException", "An api-destination '%s' does not exist.", name)
}

func (s *Store) CreateApiDestination(d ApiDestination) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists(apiDestinationsBucket)
		if err != nil {
			return err
		}
		if b.Get([]byte(d.Name)) != nil {
			return awshttp.Errf(400, "ResourceAlreadyExistsException", "An api-destination '%s' already exists.", d.Name)
		}
		raw, _ := json.Marshal(d)
		return b.Put([]byte(d.Name), raw)
	})
}

func (s *Store) GetApiDestination(name string) (*ApiDestination, error) {
	var out *ApiDestination
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(apiDestinationsBucket)
		if b == nil {
			return errDestinationNotFound(name)
		}
		raw := b.Get([]byte(name))
		if raw == nil {
			return errDestinationNotFound(name)
		}
		var d ApiDestination
		if err := json.Unmarshal(raw, &d); err != nil {
			return err
		}
		out = &d
		return nil
	})
	return out, err
}

func (s *Store) UpdateApiDestination(name string, fn func(*ApiDestination) error) (*ApiDestination, error) {
	var out *ApiDestination
	err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(apiDestinationsBucket)
		if b == nil || b.Get([]byte(name)) == nil {
			return errDestinationNotFound(name)
		}
		var d ApiDestination
		if err := json.Unmarshal(b.Get([]byte(name)), &d); err != nil {
			return err
		}
		if err := fn(&d); err != nil {
			return err
		}
		raw, _ := json.Marshal(d)
		out = &d
		return b.Put([]byte(name), raw)
	})
	return out, err
}

func (s *Store) DeleteApiDestination(name string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(apiDestinationsBucket)
		if b == nil || b.Get([]byte(name)) == nil {
			return errDestinationNotFound(name)
		}
		return b.Delete([]byte(name))
	})
}

// ListApiDestinations filters by name prefix and connection ARN, sorted.
func (s *Store) ListApiDestinations(prefix, connARN string) ([]ApiDestination, error) {
	var out []ApiDestination
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(apiDestinationsBucket)
		if b == nil {
			return nil
		}
		return b.ForEach(func(_, raw []byte) error {
			var d ApiDestination
			if err := json.Unmarshal(raw, &d); err != nil {
				return err
			}
			if (prefix == "" || strings.HasPrefix(d.Name, prefix)) && (connARN == "" || d.ConnectionARN == connARN) {
				out = append(out, d)
			}
			return nil
		})
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, err
}

// markDestinationsInactive flips every destination on a deleted connection.
func (s *Store) markDestinationsInactive(connARN string) {
	ds, _ := s.ListApiDestinations("", connARN)
	for _, d := range ds {
		s.UpdateApiDestination(d.Name, func(d *ApiDestination) error {
			d.State = "INACTIVE"
			return nil
		})
	}
}
