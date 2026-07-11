package lambda

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/doze-dev/doze-aws/internal/awshttp"
)

// routeFunctions dispatches /functions[/name[/...]] requests.
func (s *Server) routeFunctions(w http.ResponseWriter, r *http.Request, segs []string) *awshttp.APIError {
	// /2015-03-31/functions
	if len(segs) == 2 {
		switch r.Method {
		case http.MethodPost:
			return s.createFunction(w, r)
		case http.MethodGet:
			return s.listFunctions(w)
		}
		return awshttp.Errf(405, "MethodNotAllowed", "unsupported method on functions")
	}
	name := segs[2]
	// /functions/{name}
	if len(segs) == 3 {
		switch r.Method {
		case http.MethodGet:
			return s.getFunction(w, name)
		case http.MethodDelete:
			return s.deleteFunction(w, name)
		}
	}
	// /functions/{name}/invocations
	if len(segs) == 4 && segs[3] == "invocations" {
		return s.invoke(w, r, name)
	}
	// /functions/{name}/configuration
	if len(segs) == 4 && segs[3] == "configuration" && r.Method == http.MethodPut {
		return s.updateConfiguration(w, r, name)
	}
	// /functions/{name}/code
	if len(segs) == 4 && segs[3] == "code" && r.Method == http.MethodPut {
		return s.updateCode(w, r, name)
	}
	// /functions/{name}/versions (PublishVersion)
	if len(segs) == 4 && segs[3] == "versions" && r.Method == http.MethodPost {
		return s.publishVersion(w, name)
	}
	// /functions/{name}/aliases
	if len(segs) >= 4 && segs[3] == "aliases" {
		return s.routeAliases(w, r, name, segs)
	}
	// /functions/{name}/concurrency
	if len(segs) == 4 && segs[3] == "concurrency" {
		return s.routeConcurrency(w, r, name)
	}
	// /functions/{name}/urls or url  (Function URL config)
	if len(segs) >= 4 && (segs[3] == "url" || segs[3] == "urls") {
		return s.routeFunctionURL(w, r, name)
	}
	// /functions/{name}/event-invoke-config[/list]  (async destinations/retries)
	if len(segs) >= 4 && segs[3] == "event-invoke-config" {
		return s.routeEventInvokeConfig(w, r, name, segs)
	}
	return awshttp.Errf(404, "ResourceNotFoundException", "unknown function subresource")
}

// codeWire is the request Code member.
type codeWire struct {
	ZipFile  string `json:"ZipFile"`  // base64 zip
	S3Bucket string `json:"S3Bucket"` // "_local_" for the in-place extension
	S3Key    string `json:"S3Key"`    // absolute path when S3Bucket == "_local_"
}

type createFunctionReq struct {
	FunctionName string   `json:"FunctionName"`
	Runtime      string   `json:"Runtime"`
	Handler      string   `json:"Handler"`
	Role         string   `json:"Role"`
	Description  string   `json:"Description"`
	Timeout      int      `json:"Timeout"`
	MemorySize   int      `json:"MemorySize"`
	Code         codeWire `json:"Code"`
	Environment  struct {
		Variables map[string]string `json:"Variables"`
	} `json:"Environment"`
	DeadLetterConfig struct {
		TargetArn string `json:"TargetArn"`
	} `json:"DeadLetterConfig"`
	DestinationConfig json.RawMessage   `json:"DestinationConfig"`
	Layers            []string          `json:"Layers"`
	Tags              map[string]string `json:"Tags"`
	// doze extension.
	Command []string `json:"Command"`
}

func (s *Server) createFunction(w http.ResponseWriter, r *http.Request) *awshttp.APIError {
	var req createFunctionReq
	if aerr := decode(r, &req); aerr != nil {
		return aerr
	}
	if req.FunctionName == "" {
		return awshttp.Errf(400, "InvalidParameterValueException", "FunctionName is required")
	}
	if _, err := s.store.GetFunction(req.FunctionName); err == nil {
		return awshttp.Errf(409, "ResourceConflictException", "Function already exist: %s", req.FunctionName)
	}
	codeDir, sha, aerr := s.materializeCode(req.FunctionName, req.Code)
	if aerr != nil {
		return aerr
	}
	f := &Function{
		Name: req.FunctionName, Runtime: req.Runtime, Handler: req.Handler,
		Role: req.Role, Description: req.Description,
		Timeout: orInt(req.Timeout, 3), MemorySize: orInt(req.MemorySize, 512),
		Env: req.Environment.Variables, Command: req.Command,
		CodeDir: codeDir, CodeSHA256: sha, Version: "$LATEST",
		DeadLetterArn: req.DeadLetterConfig.TargetArn, Destinations: req.DestinationConfig,
		Layers: req.Layers, Tags: req.Tags,
		LastMod: s.now().Unix(), Revision: newRevision(),
	}
	if err := s.store.PutFunction(f); err != nil {
		return awshttp.AsAPIError(err)
	}
	writeJSON(w, 201, s.configView(f))
	return nil
}

// materializeCode either unpacks a ZipFile under the data dir or records the
// in-place _local_ path.
func (s *Server) materializeCode(name string, code codeWire) (codeDir, sha string, aerr *awshttp.APIError) {
	if code.S3Bucket == "_local_" {
		if code.S3Key == "" {
			return "", "", awshttp.Errf(400, "InvalidParameterValueException", "_local_ code requires S3Key to be an absolute path")
		}
		info, err := os.Stat(code.S3Key)
		if err != nil {
			return "", "", awshttp.Errf(400, "InvalidParameterValueException", "local code path does not exist: %s", code.S3Key)
		}
		dir := code.S3Key
		if !info.IsDir() {
			dir = filepath.Dir(code.S3Key)
		}
		return dir, "local", nil
	}
	if code.ZipFile == "" {
		return "", "", awshttp.Errf(400, "InvalidParameterValueException", "Code.ZipFile or the _local_ extension is required")
	}
	raw, err := base64.StdEncoding.DecodeString(code.ZipFile)
	if err != nil {
		return "", "", awshttp.Errf(400, "InvalidParameterValueException", "Code.ZipFile is not valid base64")
	}
	sum := sha256.Sum256(raw)
	dir := filepath.Join(s.dataDir, "code", name)
	_ = os.RemoveAll(dir)
	if err := unzip(raw, dir); err != nil {
		return "", "", awshttp.Errf(400, "InvalidParameterValueException", "Code.ZipFile is not a valid zip: %v", err)
	}
	return dir, hex.EncodeToString(sum[:]), nil
}

// unzip extracts a zip archive into dir, making bootstrap/handler executable.
func unzip(data []byte, dir string) error {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, f := range zr.File {
		target := filepath.Join(dir, f.Name)
		if !strings.HasPrefix(target, filepath.Clean(dir)+string(os.PathSeparator)) && target != filepath.Clean(dir) {
			return os.ErrInvalid // zip-slip guard
		}
		if f.FileInfo().IsDir() {
			os.MkdirAll(target, 0o755)
			continue
		}
		os.MkdirAll(filepath.Dir(target), 0o755)
		rc, err := f.Open()
		if err != nil {
			return err
		}
		mode := f.Mode()
		if mode == 0 {
			mode = 0o644
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
		if err != nil {
			rc.Close()
			return err
		}
		_, err = io.Copy(out, rc)
		rc.Close()
		out.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) getFunction(w http.ResponseWriter, name string) *awshttp.APIError {
	f, err := s.store.GetFunction(name)
	if err != nil {
		return awshttp.AsAPIError(err)
	}
	writeJSON(w, 200, map[string]any{
		"Configuration": s.configView(f),
		"Code":          map[string]any{"RepositoryType": "S3", "Location": "local://" + f.CodeDir},
		"Tags":          f.Tags,
	})
	return nil
}

func (s *Server) listFunctions(w http.ResponseWriter) *awshttp.APIError {
	fns, err := s.store.ListFunctions()
	if err != nil {
		return awshttp.AsAPIError(err)
	}
	views := []any{}
	for i := range fns {
		views = append(views, s.configView(&fns[i]))
	}
	writeJSON(w, 200, map[string]any{"Functions": views})
	return nil
}

func (s *Server) deleteFunction(w http.ResponseWriter, name string) *awshttp.APIError {
	if err := s.store.DeleteFunction(name); err != nil {
		return awshttp.AsAPIError(err)
	}
	s.mu.Lock()
	if r := s.runners[name]; r != nil {
		r.Stop()
		delete(s.runners, name)
	}
	s.mu.Unlock()
	w.WriteHeader(204)
	return nil
}

func (s *Server) updateConfiguration(w http.ResponseWriter, r *http.Request, name string) *awshttp.APIError {
	var req createFunctionReq
	if aerr := decode(r, &req); aerr != nil {
		return aerr
	}
	f, err := s.store.Update(name, func(f *Function) error {
		if req.Runtime != "" {
			f.Runtime = req.Runtime
		}
		if req.Handler != "" {
			f.Handler = req.Handler
		}
		if req.Timeout > 0 {
			f.Timeout = req.Timeout
		}
		if req.MemorySize > 0 {
			f.MemorySize = req.MemorySize
		}
		if req.Environment.Variables != nil {
			f.Env = req.Environment.Variables
		}
		if req.Command != nil {
			f.Command = req.Command
		}
		if req.DeadLetterConfig.TargetArn != "" {
			f.DeadLetterArn = req.DeadLetterConfig.TargetArn
		}
		if len(req.DestinationConfig) > 0 {
			f.Destinations = req.DestinationConfig
		}
		f.LastMod = s.now().Unix()
		f.Revision = newRevision()
		return nil
	})
	if err != nil {
		return awshttp.AsAPIError(err)
	}
	s.restartRunner(name)
	writeJSON(w, 200, s.configView(f))
	return nil
}

func (s *Server) updateCode(w http.ResponseWriter, r *http.Request, name string) *awshttp.APIError {
	var req struct {
		codeWire
	}
	if aerr := decode(r, &req); aerr != nil {
		return aerr
	}
	codeDir, sha, aerr := s.materializeCode(name, req.codeWire)
	if aerr != nil {
		return aerr
	}
	f, err := s.store.Update(name, func(f *Function) error {
		f.CodeDir, f.CodeSHA256 = codeDir, sha
		f.LastMod = s.now().Unix()
		f.Revision = newRevision()
		return nil
	})
	if err != nil {
		return awshttp.AsAPIError(err)
	}
	s.restartRunner(name)
	writeJSON(w, 200, s.configView(f))
	return nil
}

func (s *Server) publishVersion(w http.ResponseWriter, name string) *awshttp.APIError {
	f, err := s.store.GetFunction(name)
	if err != nil {
		return awshttp.AsAPIError(err)
	}
	// A monotonically increasing published version number, kept in an alias
	// map for simplicity (local versioning is cosmetic beyond the number).
	next := "1"
	if v, ok := f.Aliases["$published"]; ok {
		next = incVersion(v)
	}
	s.store.Update(name, func(f *Function) error {
		if f.Aliases == nil {
			f.Aliases = map[string]string{}
		}
		f.Aliases["$published"] = next
		return nil
	})
	view := s.configView(f)
	view["Version"] = next
	writeJSON(w, 201, view)
	return nil
}

// configView renders a FunctionConfiguration.
func (s *Server) configView(f *Function) map[string]any {
	view := map[string]any{
		"FunctionName":     f.Name,
		"FunctionArn":      f.ARN(),
		"Runtime":          f.Runtime,
		"Handler":          f.Handler,
		"Role":             orStr(f.Role, "arn:aws:iam::000000000000:role/lambda-role"),
		"Description":      f.Description,
		"Timeout":          f.Timeout,
		"MemorySize":       f.MemorySize,
		"CodeSha256":       f.CodeSHA256,
		"Version":          f.Version,
		"LastModified":     awshttp.ISO8601(s.now()),
		"State":            "Active",
		"LastUpdateStatus": "Successful",
		"PackageType":      "Zip",
		"RevisionId":       f.Revision,
		"Architectures":    []string{"x86_64"},
	}
	if len(f.Env) > 0 {
		view["Environment"] = map[string]any{"Variables": f.Env}
	}
	if f.DeadLetterArn != "" {
		view["DeadLetterConfig"] = map[string]any{"TargetArn": f.DeadLetterArn}
	}
	return view
}

// decode reads a JSON body into dst.
func decode(r *http.Request, dst any) *awshttp.APIError {
	body, err := io.ReadAll(io.LimitReader(r.Body, 128<<20))
	if err != nil {
		return awshttp.Errf(400, "InvalidRequestContentException", "read body: %v", err)
	}
	if len(body) == 0 {
		return nil
	}
	if err := json.Unmarshal(body, dst); err != nil {
		return awshttp.Errf(400, "InvalidRequestContentException", "malformed JSON: %v", err)
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	body, _ := json.Marshal(v)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(body)
}

func newRevision() string {
	var b [16]byte
	_, _ = readRand(b[:])
	return hex.EncodeToString(b[:])
}

func orInt(v, def int) int {
	if v == 0 {
		return def
	}
	return v
}

func orStr(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func incVersion(v string) string {
	n := 0
	for _, c := range v {
		if c >= '0' && c <= '9' {
			n = n*10 + int(c-'0')
		}
	}
	return itoa(n + 1)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [12]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
