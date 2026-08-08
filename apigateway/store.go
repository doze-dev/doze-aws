package apigateway

// The bbolt-backed store and the REST API model.
//
// An API Gateway REST API is a tree: an API owns resources, a resource owns
// methods, a method owns one integration and its responses. The whole tree is
// stored as a single record per API — a local API has tens of resources at
// most, and keeping it together makes every mutation one atomic write and
// every request-time lookup a single read.

import (
	"encoding/json"
	"sort"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"
)

var apiBucket = []byte("restapis")

// RestAPI is one REST API and everything under it.
type RestAPI struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Version     string            `json:"version,omitempty"`
	Created     int64             `json:"created"`
	Tags        map[string]string `json:"tags,omitempty"`

	// Resources are keyed by resource id. The root resource ("/") is created
	// with the API and cannot be deleted.
	Resources map[string]*Resource `json:"resources"`
	// Deployments and Stages are keyed by id and stage name.
	Deployments map[string]*Deployment `json:"deployments,omitempty"`
	Stages      map[string]*Stage      `json:"stages,omitempty"`

	// Round-tripped configuration with no local effect.
	APIKeySource           string   `json:"api_key_source,omitempty"`
	BinaryMediaTypes       []string `json:"binary_media_types,omitempty"`
	MinimumCompressionSize *int     `json:"minimum_compression_size,omitempty"`
	EndpointTypes          []string `json:"endpoint_types,omitempty"`
	Policy                 string   `json:"policy,omitempty"`
	DisableExecuteAPI      bool     `json:"disable_execute_api,omitempty"`
}

// Resource is one node of the API's path tree.
type Resource struct {
	ID       string `json:"id"`
	ParentID string `json:"parent_id,omitempty"`
	PathPart string `json:"path_part,omitempty"`
	// Path is the full path from the root, recomputed on every mutation so a
	// request-time lookup never has to walk parents.
	Path string `json:"path"`
	// Methods are keyed by uppercase HTTP method, plus "ANY".
	Methods map[string]*Method `json:"methods,omitempty"`
}

// Method is one HTTP method on a resource.
type Method struct {
	HTTPMethod         string            `json:"http_method"`
	AuthorizationType  string            `json:"authorization_type,omitempty"`
	AuthorizerID       string            `json:"authorizer_id,omitempty"`
	APIKeyRequired     bool              `json:"api_key_required,omitempty"`
	OperationName      string            `json:"operation_name,omitempty"`
	RequestParameters  map[string]bool   `json:"request_parameters,omitempty"`
	RequestModels      map[string]string `json:"request_models,omitempty"`
	RequestValidatorID string            `json:"request_validator_id,omitempty"`

	Integration *Integration               `json:"integration,omitempty"`
	Responses   map[string]*MethodResponse `json:"responses,omitempty"`
}

// Integration is how a method reaches a backend.
type Integration struct {
	// Type is AWS_PROXY, AWS, HTTP, HTTP_PROXY or MOCK.
	Type                string            `json:"type"`
	HTTPMethod          string            `json:"http_method,omitempty"`
	URI                 string            `json:"uri,omitempty"`
	ConnectionType      string            `json:"connection_type,omitempty"`
	Credentials         string            `json:"credentials,omitempty"`
	PassthroughBehavior string            `json:"passthrough_behavior,omitempty"`
	TimeoutInMillis     int               `json:"timeout_in_millis,omitempty"`
	RequestTemplates    map[string]string `json:"request_templates,omitempty"`
	RequestParameters   map[string]string `json:"request_parameters,omitempty"`
	ContentHandling     string            `json:"content_handling,omitempty"`
	CacheKeyParameters  []string          `json:"cache_key_parameters,omitempty"`
	CacheNamespace      string            `json:"cache_namespace,omitempty"`

	Responses map[string]*IntegrationResponse `json:"integration_responses,omitempty"`
}

// MethodResponse declares a status code the method can return.
type MethodResponse struct {
	StatusCode         string            `json:"status_code"`
	ResponseModels     map[string]string `json:"response_models,omitempty"`
	ResponseParameters map[string]bool   `json:"response_parameters,omitempty"`
}

// IntegrationResponse maps a backend result onto a method response.
type IntegrationResponse struct {
	StatusCode         string            `json:"status_code"`
	SelectionPattern   string            `json:"selection_pattern,omitempty"`
	ResponseTemplates  map[string]string `json:"response_templates,omitempty"`
	ResponseParameters map[string]string `json:"response_parameters,omitempty"`
	ContentHandling    string            `json:"content_handling,omitempty"`
}

// Deployment is a point-in-time release of the API.
//
// Real API Gateway snapshots the API into a deployment. doze-aws serves the
// LIVE API instead: locally you want an edit to take effect without
// redeploying, and a stale snapshot is a debugging trap rather than a feature.
// The deployment record exists so the control plane and CloudFormation behave.
type Deployment struct {
	ID          string `json:"id"`
	Description string `json:"description,omitempty"`
	Created     int64  `json:"created"`
}

// Stage is a named, addressable release.
type Stage struct {
	Name           string            `json:"name"`
	DeploymentID   string            `json:"deployment_id,omitempty"`
	Description    string            `json:"description,omitempty"`
	Variables      map[string]string `json:"variables,omitempty"`
	Created        int64             `json:"created"`
	Updated        int64             `json:"updated"`
	Tags           map[string]string `json:"tags,omitempty"`
	TracingEnabled bool              `json:"tracing_enabled,omitempty"`
}

// Store is the bbolt-backed API Gateway state.
type Store struct {
	db    *bolt.DB
	clock func() time.Time
}

func newStore(db *bolt.DB) *Store { return &Store{db: db, clock: time.Now} }

func (s *Store) now() time.Time { return s.clock() }

// ---- API lifecycle ----

func (s *Store) Create(name, description, version string, tags map[string]string) (*RestAPI, error) {
	if name == "" {
		return nil, errBadRequest("Name is required")
	}
	api := &RestAPI{
		ID: s.newID(), Name: name, Description: description, Version: version,
		Created: s.now().Unix(), Tags: tags,
		Resources:    map[string]*Resource{},
		Deployments:  map[string]*Deployment{},
		Stages:       map[string]*Stage{},
		APIKeySource: "HEADER",
	}
	// Every API is born with a root resource; it has no path part and cannot
	// be removed.
	root := &Resource{ID: s.newID(), Path: "/"}
	api.Resources[root.ID] = root
	return api, s.Put(api)
}

func (s *Store) Get(id string) (*RestAPI, error) {
	var out *RestAPI
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(apiBucket)
		if b == nil {
			return errNotFound("Invalid API identifier specified")
		}
		raw := b.Get([]byte(id))
		if raw == nil {
			return errNotFound("Invalid API identifier specified")
		}
		var api RestAPI
		if err := json.Unmarshal(raw, &api); err != nil {
			return err
		}
		out = &api
		return nil
	})
	return out, err
}

func (s *Store) Put(api *RestAPI) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists(apiBucket)
		if err != nil {
			return err
		}
		raw, err := json.Marshal(api)
		if err != nil {
			return err
		}
		return b.Put([]byte(api.ID), raw)
	})
}

// Update applies fn inside a write transaction.
func (s *Store) Update(id string, fn func(*RestAPI) error) (*RestAPI, error) {
	api, err := s.Get(id)
	if err != nil {
		return nil, err
	}
	if err := fn(api); err != nil {
		return nil, err
	}
	// Path strings are derived, so recompute them after any tree change.
	rebuildPaths(api)
	return api, s.Put(api)
}

func (s *Store) Delete(id string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(apiBucket)
		if b == nil {
			return nil
		}
		return b.Delete([]byte(id))
	})
}

func (s *Store) List() ([]RestAPI, error) {
	var out []RestAPI
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(apiBucket)
		if b == nil {
			return nil
		}
		return b.ForEach(func(_, raw []byte) error {
			var api RestAPI
			if json.Unmarshal(raw, &api) == nil {
				out = append(out, api)
			}
			return nil
		})
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Created < out[j].Created })
	return out, err
}

// ---- tree helpers ----

// Root returns the API's root resource.
func (api *RestAPI) Root() *Resource {
	for _, r := range api.Resources {
		if r.ParentID == "" {
			return r
		}
	}
	return nil
}

// Children returns a resource's direct children, in a stable order.
func (api *RestAPI) Children(parentID string) []*Resource {
	var out []*Resource
	for _, r := range api.Resources {
		if r.ParentID == parentID {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PathPart < out[j].PathPart })
	return out
}

// SortedResources returns every resource ordered by path, so listings are
// deterministic.
func (api *RestAPI) SortedResources() []*Resource {
	out := make([]*Resource, 0, len(api.Resources))
	for _, r := range api.Resources {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// rebuildPaths recomputes every resource's full Path from the tree.
func rebuildPaths(api *RestAPI) {
	root := api.Root()
	if root == nil {
		return
	}
	root.Path = "/"
	var walk func(parent *Resource)
	walk = func(parent *Resource) {
		for _, child := range api.Children(parent.ID) {
			if parent.Path == "/" {
				child.Path = "/" + child.PathPart
			} else {
				child.Path = parent.Path + "/" + child.PathPart
			}
			walk(child)
		}
	}
	walk(root)
}

// FindByPath returns the resource with an exact path, if any.
func (api *RestAPI) FindByPath(path string) *Resource {
	for _, r := range api.Resources {
		if r.Path == path {
			return r
		}
	}
	return nil
}

// ---- ids ----

// newID generates the 10-character lowercase alphanumeric id API Gateway uses.
func (s *Store) newID() string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	n := s.now().UnixNano()
	var b strings.Builder
	for range 10 {
		b.WriteByte(alphabet[n%int64(len(alphabet))])
		n /= int64(len(alphabet))
		if n == 0 {
			n = s.now().UnixNano()
		}
	}
	return b.String()
}
