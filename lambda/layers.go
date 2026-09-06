package lambda

// Lambda layers: publish, list, get and delete layer versions, plus the layer
// version resource policy.
//
// Layers are genuinely emulatable — a layer version is a name, a version
// number, some metadata and a content blob — and templates reference them
// routinely, so they are implemented rather than stubbed. A layer is unpacked
// when published, and a function that lists it is launched with the layer's
// python/, nodejs/, ruby/, bin/ and lib/ directories on the search paths its
// runtime honours — the effect /opt has on AWS, without a mount.

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	bolt "go.etcd.io/bbolt"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/peercall"
)

var layerBucket = []byte("layers")

// LayerVersion is one published version of a layer.
type LayerVersion struct {
	LayerName          string   `json:"layer_name"`
	Version            int64    `json:"version"`
	Description        string   `json:"description,omitempty"`
	CreatedDate        string   `json:"created"`
	CompatibleRuntimes []string `json:"compatible_runtimes,omitempty"`
	CompatibleArchs    []string `json:"compatible_architectures,omitempty"`
	LicenseInfo        string   `json:"license_info,omitempty"`
	CodeSize           int64    `json:"code_size"`
	CodeSHA256         string   `json:"code_sha256,omitempty"`
	// ContentPath is where the layer archive was written under the data dir.
	ContentPath string `json:"content_path,omitempty"`
	// ExtractedDir is the archive unpacked — the tree a function is launched
	// with on its search paths.
	ExtractedDir string `json:"extracted_dir,omitempty"`
	// Policy is the layer version's resource policy statements.
	Policy []PolicyStatement `json:"policy,omitempty"`
}

// ARN returns the versioned layer ARN.
func (l *LayerVersion) ARN() string {
	return awsident.ARN("lambda", fmt.Sprintf("layer:%s:%d", l.LayerName, l.Version))
}

// LayerARN returns the unversioned layer ARN.
func (l *LayerVersion) LayerARN() string {
	return awsident.ARN("lambda", "layer:"+l.LayerName)
}

// layerKey orders versions of one layer contiguously and numerically.
func layerKey(name string, version int64) string {
	return fmt.Sprintf("%s\x00%020d", name, version)
}

// ---- store ----

func (s *Store) PutLayerVersion(l *LayerVersion) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists(layerBucket)
		if err != nil {
			return err
		}
		raw, err := json.Marshal(l)
		if err != nil {
			return err
		}
		return b.Put([]byte(layerKey(l.LayerName, l.Version)), raw)
	})
}

func (s *Store) GetLayerVersion(name string, version int64) (*LayerVersion, error) {
	var out *LayerVersion
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(layerBucket)
		if b == nil {
			return errNoLayer(name, version)
		}
		raw := b.Get([]byte(layerKey(name, version)))
		if raw == nil {
			return errNoLayer(name, version)
		}
		var l LayerVersion
		if err := json.Unmarshal(raw, &l); err != nil {
			return err
		}
		out = &l
		return nil
	})
	return out, err
}

func (s *Store) UpdateLayerVersion(name string, version int64, fn func(*LayerVersion) error) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(layerBucket)
		if b == nil {
			return errNoLayer(name, version)
		}
		key := []byte(layerKey(name, version))
		raw := b.Get(key)
		if raw == nil {
			return errNoLayer(name, version)
		}
		var l LayerVersion
		if err := json.Unmarshal(raw, &l); err != nil {
			return err
		}
		if err := fn(&l); err != nil {
			return err
		}
		updated, err := json.Marshal(&l)
		if err != nil {
			return err
		}
		return b.Put(key, updated)
	})
}

func (s *Store) DeleteLayerVersion(name string, version int64) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(layerBucket)
		if b == nil {
			return nil // AWS's DeleteLayerVersion is idempotent
		}
		return b.Delete([]byte(layerKey(name, version)))
	})
}

// ListLayerVersions returns every version of one layer, newest first.
func (s *Store) ListLayerVersions(name string) ([]LayerVersion, error) {
	var out []LayerVersion
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(layerBucket)
		if b == nil {
			return nil
		}
		prefix := []byte(name + "\x00")
		c := b.Cursor()
		for k, v := c.Seek(prefix); k != nil && strings.HasPrefix(string(k), string(prefix)); k, v = c.Next() {
			var l LayerVersion
			if json.Unmarshal(v, &l) == nil {
				out = append(out, l)
			}
		}
		return nil
	})
	// Newest first, as AWS returns them.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, err
}

// ListLayers returns the latest version of each distinct layer.
func (s *Store) ListLayers() ([]LayerVersion, error) {
	latest := map[string]LayerVersion{}
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(layerBucket)
		if b == nil {
			return nil
		}
		return b.ForEach(func(_, v []byte) error {
			var l LayerVersion
			if json.Unmarshal(v, &l) != nil {
				return nil
			}
			if cur, ok := latest[l.LayerName]; !ok || l.Version > cur.Version {
				latest[l.LayerName] = l
			}
			return nil
		})
	})
	out := make([]LayerVersion, 0, len(latest))
	for _, l := range latest {
		out = append(out, l)
	}
	sortLayers(out)
	return out, err
}

func sortLayers(ls []LayerVersion) {
	for i := 1; i < len(ls); i++ {
		for j := i; j > 0 && ls[j].LayerName < ls[j-1].LayerName; j-- {
			ls[j], ls[j-1] = ls[j-1], ls[j]
		}
	}
}

// nextLayerVersion returns one past the highest version of a layer.
func (s *Store) nextLayerVersion(name string) int64 {
	versions, _ := s.ListLayerVersions(name)
	var high int64
	for _, l := range versions {
		if l.Version > high {
			high = l.Version
		}
	}
	return high + 1
}

func errNoLayer(name string, version int64) *awshttp.APIError {
	return awshttp.Errf(404, "ResourceNotFoundException",
		"Layer version %s:%d does not exist.", name, version)
}

// ---- routing ----

// routeLayers dispatches the /layers family:
//
//	/layers                                          list
//	/layers/{name}/versions                          publish, list versions
//	/layers/{name}/versions/{v}                      get, delete
//	/layers/{name}/versions/{v}/policy[/{sid}]       layer version policy
func (s *Server) routeLayers(w http.ResponseWriter, r *http.Request, segs []string) *awshttp.APIError {
	if len(segs) == 2 {
		if r.Method != http.MethodGet {
			return awshttp.Errf(405, "MethodNotAllowed", "unsupported method on layers")
		}
		// GetLayerVersionByArn is GET /layers?find=LayerVersion&Arn=... — the
		// one operation in the family addressed by query rather than path.
		if arn := r.URL.Query().Get("Arn"); arn != "" {
			layerName, version, ok := parseLayerARN(arn)
			if !ok {
				return awshttp.Errf(400, "InvalidParameterValueException", "malformed layer ARN %q", arn)
			}
			return s.getLayerVersion(w, layerName, version)
		}
		return s.listLayers(w)
	}
	name := segs[2]

	// Some clients spell it /layers/{arn} instead; resolve that too.
	if strings.HasPrefix(name, "arn:") {
		layerName, version, ok := parseLayerARN(name)
		if !ok {
			return awshttp.Errf(400, "InvalidParameterValueException", "malformed layer ARN %q", name)
		}
		return s.getLayerVersion(w, layerName, version)
	}

	if len(segs) >= 4 && segs[3] == "versions" {
		// /layers/{name}/versions
		if len(segs) == 4 {
			switch r.Method {
			case http.MethodPost:
				return s.publishLayerVersion(w, r, name)
			case http.MethodGet:
				return s.listLayerVersions(w, name)
			}
			return awshttp.Errf(405, "MethodNotAllowed", "unsupported method on layer versions")
		}
		version, err := strconv.ParseInt(segs[4], 10, 64)
		if err != nil {
			return awshttp.Errf(400, "InvalidParameterValueException", "layer version must be a number")
		}
		// /layers/{name}/versions/{v}/policy
		if len(segs) >= 6 && segs[5] == "policy" {
			return s.routeLayerPolicy(w, r, name, version, segs)
		}
		// /layers/{name}/versions/{v}
		switch r.Method {
		case http.MethodGet:
			return s.getLayerVersion(w, name, version)
		case http.MethodDelete:
			if err := s.store.DeleteLayerVersion(name, version); err != nil {
				return awshttp.AsAPIError(err)
			}
			w.WriteHeader(204)
			return nil
		}
	}
	return awshttp.Errf(404, "ResourceNotFoundException", "unknown layers subresource")
}

// parseLayerARN splits arn:aws:lambda:region:account:layer:{name}:{version}.
func parseLayerARN(arn string) (string, int64, bool) {
	i := strings.Index(arn, ":layer:")
	if i < 0 {
		return "", 0, false
	}
	rest := arn[i+len(":layer:"):]
	name, versionStr, ok := strings.Cut(rest, ":")
	if !ok {
		return "", 0, false
	}
	version, err := strconv.ParseInt(versionStr, 10, 64)
	if err != nil {
		return "", 0, false
	}
	return name, version, true
}

func (s *Server) publishLayerVersion(w http.ResponseWriter, r *http.Request, name string) *awshttp.APIError {
	var req struct {
		Description string `json:"Description"`
		Content     struct {
			ZipFile   []byte `json:"ZipFile"`
			S3Bucket  string `json:"S3Bucket"`
			S3Key     string `json:"S3Key"`
			S3Version string `json:"S3ObjectVersion"`
		} `json:"Content"`
		CompatibleRuntimes      []string `json:"CompatibleRuntimes"`
		CompatibleArchitectures []string `json:"CompatibleArchitectures"`
		LicenseInfo             string   `json:"LicenseInfo"`
	}
	if aerr := decode(r, &req); aerr != nil {
		return aerr
	}

	version := s.store.nextLayerVersion(name)
	l := &LayerVersion{
		LayerName:          name,
		Version:            version,
		Description:        req.Description,
		CreatedDate:        awshttp.ISO8601(s.now()),
		CompatibleRuntimes: req.CompatibleRuntimes,
		CompatibleArchs:    req.CompatibleArchitectures,
		LicenseInfo:        req.LicenseInfo,
	}

	// The archive is written to the data dir so GetLayerVersion can hand back
	// a location that resolves, and unpacked beside it so a function can be
	// launched with the layer's python/, nodejs/, ruby/, bin/ and lib/ on its
	// search paths — which is what /opt is for on AWS.
	dir := filepath.Join(s.dataDir, "layers", name)
	archive := req.Content.ZipFile
	switch {
	case len(archive) > 0:
	case req.Content.S3Bucket == "_local_" || (req.Content.S3Bucket == "" && req.Content.S3Key != ""):
		// The _local_ convention: the key names a path already on disk — a
		// directory laid out like an unpacked layer, or a zip.
		info, err := os.Stat(req.Content.S3Key)
		if err != nil {
			return awshttp.Errf(400, "InvalidParameterValueException", "local layer path does not exist: %s", req.Content.S3Key)
		}
		l.ContentPath = req.Content.S3Key
		l.CodeSize = info.Size()
		if info.IsDir() {
			l.ExtractedDir = req.Content.S3Key
		} else if raw, err := os.ReadFile(req.Content.S3Key); err == nil {
			archive = raw
		}
	case req.Content.S3Bucket != "" && req.Content.S3Key != "":
		// A deploy tool staged the layer in S3, as `cdk deploy` does.
		fetched, err := peercall.S3Get(context.Background(), s.peers, req.Content.S3Bucket, req.Content.S3Key)
		if err != nil {
			return awshttp.Errf(400, "InvalidParameterValueException",
				"cannot read layer content from s3://%s/%s: %v", req.Content.S3Bucket, req.Content.S3Key, err)
		}
		archive = fetched
	}
	if len(archive) > 0 {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return awshttp.AsAPIError(err)
		}
		path := filepath.Join(dir, fmt.Sprintf("%d.zip", version))
		if err := os.WriteFile(path, archive, 0o644); err != nil {
			return awshttp.AsAPIError(err)
		}
		extracted := filepath.Join(dir, fmt.Sprintf("%d", version))
		if err := unzip(archive, extracted); err != nil {
			return awshttp.Errf(400, "InvalidParameterValueException", "layer content is not a valid zip: %v", err)
		}
		// A layer's bin/ is meant to be run; a zip written without mode bits
		// would leave it unrunnable, which is never what was meant.
		if entries, err := os.ReadDir(filepath.Join(extracted, "bin")); err == nil {
			for _, e := range entries {
				if !e.IsDir() {
					_ = os.Chmod(filepath.Join(extracted, "bin", e.Name()), 0o755)
				}
			}
		}
		l.ContentPath = path
		l.ExtractedDir = extracted
		l.CodeSize = int64(len(archive))
		l.CodeSHA256 = sha256Base64(archive)
	}

	if err := s.store.PutLayerVersion(l); err != nil {
		return awshttp.AsAPIError(err)
	}
	writeJSON(w, 201, layerVersionView(l, true))
	return nil
}

func (s *Server) getLayerVersion(w http.ResponseWriter, name string, version int64) *awshttp.APIError {
	l, err := s.store.GetLayerVersion(name, version)
	if err != nil {
		return awshttp.AsAPIError(err)
	}
	writeJSON(w, 200, layerVersionView(l, true))
	return nil
}

func (s *Server) listLayerVersions(w http.ResponseWriter, name string) *awshttp.APIError {
	versions, err := s.store.ListLayerVersions(name)
	if err != nil {
		return awshttp.AsAPIError(err)
	}
	views := make([]any, 0, len(versions))
	for i := range versions {
		views = append(views, layerVersionView(&versions[i], false))
	}
	writeJSON(w, 200, map[string]any{"LayerVersions": views})
	return nil
}

func (s *Server) listLayers(w http.ResponseWriter) *awshttp.APIError {
	layers, err := s.store.ListLayers()
	if err != nil {
		return awshttp.AsAPIError(err)
	}
	views := make([]any, 0, len(layers))
	for i := range layers {
		l := &layers[i]
		views = append(views, map[string]any{
			"LayerName":             l.LayerName,
			"LayerArn":              l.LayerARN(),
			"LatestMatchingVersion": layerVersionView(l, false),
		})
	}
	writeJSON(w, 200, map[string]any{"Layers": views})
	return nil
}

// layerVersionView shapes a layer version. withContent adds the Content block,
// which list responses omit.
func layerVersionView(l *LayerVersion, withContent bool) map[string]any {
	v := map[string]any{
		"LayerVersionArn": l.ARN(),
		"LayerArn":        l.LayerARN(),
		"Version":         l.Version,
		"Description":     l.Description,
		"CreatedDate":     l.CreatedDate,
		"LicenseInfo":     l.LicenseInfo,
	}
	if len(l.CompatibleRuntimes) > 0 {
		v["CompatibleRuntimes"] = l.CompatibleRuntimes
	}
	if len(l.CompatibleArchs) > 0 {
		v["CompatibleArchitectures"] = l.CompatibleArchs
	}
	if withContent {
		v["Content"] = map[string]any{
			"CodeSize":   l.CodeSize,
			"CodeSha256": l.CodeSHA256,
			// There is no presigned download locally; the path is the honest
			// answer and it is one a local tool can actually open.
			"Location": l.ContentPath,
		}
	}
	return v
}

// ---- layer version policy ----

func (s *Server) routeLayerPolicy(w http.ResponseWriter, r *http.Request, name string, version int64, segs []string) *awshttp.APIError {
	switch r.Method {
	case http.MethodPost:
		var req struct {
			StatementId    string `json:"StatementId"`
			Action         string `json:"Action"`
			Principal      string `json:"Principal"`
			OrganizationId string `json:"OrganizationId"`
		}
		if aerr := decode(r, &req); aerr != nil {
			return aerr
		}
		if req.StatementId == "" || req.Action == "" || req.Principal == "" {
			return awshttp.Errf(400, "InvalidParameterValueException",
				"StatementId, Action and Principal are all required")
		}
		l, err := s.store.GetLayerVersion(name, version)
		if err != nil {
			return awshttp.AsAPIError(err)
		}
		stmt := PolicyStatement{
			Sid: req.StatementId, Effect: "Allow",
			Principal: map[string]string{"AWS": req.Principal},
			Action:    req.Action, Resource: l.ARN(),
		}
		if err := s.store.UpdateLayerVersion(name, version, func(l *LayerVersion) error {
			for _, existing := range l.Policy {
				if existing.Sid == req.StatementId {
					return awshttp.Errf(409, "ResourceConflictException",
						"The statement id (%s) provided already exists.", req.StatementId)
				}
			}
			l.Policy = append(l.Policy, stmt)
			return nil
		}); err != nil {
			return awshttp.AsAPIError(err)
		}
		raw, _ := json.Marshal(stmt)
		writeJSON(w, 201, map[string]any{"Statement": string(raw), "RevisionId": "1"})
		return nil

	case http.MethodGet:
		l, err := s.store.GetLayerVersion(name, version)
		if err != nil {
			return awshttp.AsAPIError(err)
		}
		if len(l.Policy) == 0 {
			return awshttp.Errf(404, "ResourceNotFoundException", "The resource you requested does not exist.")
		}
		raw, _ := json.Marshal(policyDoc{Version: "2012-10-17", Id: "default", Statement: l.Policy})
		writeJSON(w, 200, map[string]any{"Policy": string(raw), "RevisionId": "1"})
		return nil

	case http.MethodDelete:
		sid := ""
		if len(segs) >= 7 {
			sid = segs[6]
		}
		if sid == "" {
			sid = r.URL.Query().Get("StatementId")
		}
		if err := s.store.UpdateLayerVersion(name, version, func(l *LayerVersion) error {
			for i, stmt := range l.Policy {
				if stmt.Sid == sid {
					l.Policy = append(l.Policy[:i], l.Policy[i+1:]...)
					return nil
				}
			}
			return awshttp.Errf(404, "ResourceNotFoundException", "The resource you requested does not exist.")
		}); err != nil {
			return awshttp.AsAPIError(err)
		}
		w.WriteHeader(204)
		return nil
	}
	return awshttp.Errf(405, "MethodNotAllowed", "unsupported method on layer policy")
}

// sha256Base64 is the CodeSha256 encoding AWS uses for layer and function code.
func sha256Base64(b []byte) string {
	sum := sha256.Sum256(b)
	return base64.StdEncoding.EncodeToString(sum[:])
}

// accountSettings answers GetAccountSettings with live counts. There are no
// account quotas locally, so the limits are nominal and the usage is real.
func (s *Server) accountSettings(w http.ResponseWriter, r *http.Request) *awshttp.APIError {
	if r.Method != http.MethodGet {
		return awshttp.Errf(405, "MethodNotAllowed", "unsupported method on account-settings")
	}
	fns, err := s.store.ListFunctions()
	if err != nil {
		return awshttp.AsAPIError(err)
	}
	var codeSize int64
	for i := range fns {
		if fi, statErr := os.Stat(fns[i].CodeDir); statErr == nil {
			codeSize += fi.Size()
		}
	}
	writeJSON(w, 200, map[string]any{
		"AccountLimit": map[string]any{
			"TotalCodeSize":                  80530636800,
			"CodeSizeUnzipped":               262144000,
			"CodeSizeZipped":                 52428800,
			"ConcurrentExecutions":           1000,
			"UnreservedConcurrentExecutions": 1000,
		},
		"AccountUsage": map[string]any{
			"TotalCodeSize": codeSize,
			"FunctionCount": len(fns),
		},
	})
	return nil
}

// checkLayers refuses a function that lists a layer version nothing
// published, as AWS does at create time — a missing layer is a deploy error,
// not a first-invoke surprise.
func (s *Server) checkLayers(arns []string) *awshttp.APIError {
	for _, arn := range arns {
		name, version, ok := parseLayerARN(arn)
		if !ok {
			return awshttp.Errf(400, "InvalidParameterValueException", "Layer ARN %q is malformed: expected arn:aws:lambda:<region>:<account>:layer:<name>:<version>", arn)
		}
		if _, err := s.store.GetLayerVersion(name, version); err != nil {
			return awshttp.Errf(400, "InvalidParameterValueException", "Layer version %s does not exist", arn)
		}
	}
	return nil
}

// layerDirs resolves a function's layers to their unpacked directories, in
// order. A layer published before unpacking existed, or a _local_ zip, has
// no directory and contributes nothing.
func (s *Server) layerDirs(f *Function) []string {
	var dirs []string
	for _, arn := range f.Layers {
		name, version, ok := parseLayerARN(arn)
		if !ok {
			continue
		}
		l, err := s.store.GetLayerVersion(name, version)
		if err != nil || l.ExtractedDir == "" {
			continue
		}
		dirs = append(dirs, l.ExtractedDir)
	}
	return dirs
}
