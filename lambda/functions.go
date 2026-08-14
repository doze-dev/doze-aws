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
	"strconv"
	"strings"

	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/lambdaruntime"
	"github.com/doze-dev/doze-aws/internal/peercall"
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
	if len(segs) == 4 && segs[3] == "configuration" {
		switch r.Method {
		case http.MethodPut:
			return s.updateConfiguration(w, r, name)
		case http.MethodGet:
			// GetFunctionConfiguration. Terraform and the CLI both read this
			// directly rather than going through GetFunction.
			f, err := s.store.GetFunction(name)
			if err != nil {
				return awshttp.AsAPIError(err)
			}
			writeJSON(w, 200, s.configView(f))
			return nil
		}
	}
	// /functions/{name}/code
	if len(segs) == 4 && segs[3] == "code" && r.Method == http.MethodPut {
		return s.updateCode(w, r, name)
	}
	// /functions/{name}/versions — PublishVersion (POST) and
	// ListVersionsByFunction (GET). Terraform calls the GET form after every
	// create to determine the function's latest version.
	if len(segs) == 4 && segs[3] == "versions" {
		switch r.Method {
		case http.MethodPost:
			return s.publishVersion(w, name)
		case http.MethodGet:
			return s.listVersions(w, name)
		}
	}
	// /functions/{name}/aliases
	if len(segs) >= 4 && segs[3] == "aliases" {
		return s.routeAliases(w, r, name, segs)
	}
	// /functions/{name}/code-signing-config
	//
	// Terraform reads this on every function refresh, so an unrouted path
	// fails the whole resource. Code signing is cloud-only — nothing here
	// verifies a signature — but reporting "no config" is the honest answer
	// for a function that has none.
	if len(segs) == 4 && segs[3] == "code-signing-config" {
		return s.routeCodeSigning(w, r, name)
	}
	// /functions/{name}/policy[/{statementId}]  (AWS::Lambda::Permission)
	if len(segs) >= 4 && segs[3] == "policy" {
		return s.routePolicy(w, r, name, segs)
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
	// /functions/{name}/doze-runtime  (doze extension: warm/idle process state)
	if len(segs) == 4 && segs[3] == "doze-runtime" && r.Method == http.MethodGet {
		return s.dozeRuntime(w, name)
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
	Architectures     []string          `json:"Architectures"`
	EphemeralStorage  struct {
		Size int `json:"Size"`
	} `json:"EphemeralStorage"`
	TracingConfig struct {
		Mode string `json:"Mode"`
	} `json:"TracingConfig"`
	KMSKeyArn         string          `json:"KMSKeyArn"`
	LoggingConfig     json.RawMessage `json:"LoggingConfig"`
	SnapStart         json.RawMessage `json:"SnapStart"`
	VpcConfig         json.RawMessage `json:"VpcConfig"`
	FileSystemConfigs json.RawMessage `json:"FileSystemConfigs"`
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
	if aerr := validSizing(req.MemorySize, req.Timeout); aerr != nil {
		return aerr
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
		Architectures:      req.Architectures,
		EphemeralStorageMB: req.EphemeralStorage.Size,
		TracingMode:        req.TracingConfig.Mode,
		KMSKeyArn:          req.KMSKeyArn,
		LoggingConfig:      req.LoggingConfig,
		SnapStart:          req.SnapStart,
		VpcConfig:          req.VpcConfig,
		FileSystemConfigs:  req.FileSystemConfigs,
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
	// A real bucket and key means a deploy tool staged the code in S3, which
	// is exactly what `sam deploy` and `cdk deploy` do. Fetching and unpacking
	// it is what real Lambda does, so doze-aws does the same.
	var raw []byte
	switch {
	case code.S3Bucket != "" && code.S3Key != "":
		fetched, err := peercall.S3Get(s.peers, code.S3Bucket, code.S3Key)
		if err != nil {
			return "", "", awshttp.Errf(400, "InvalidParameterValueException",
				"cannot read function code from s3://%s/%s: %v", code.S3Bucket, code.S3Key, err)
		}
		raw = fetched
	case code.ZipFile != "":
		decoded, err := base64.StdEncoding.DecodeString(code.ZipFile)
		if err != nil {
			return "", "", awshttp.Errf(400, "InvalidParameterValueException", "Code.ZipFile is not valid base64")
		}
		raw = decoded
	default:
		return "", "", awshttp.Errf(400, "InvalidParameterValueException",
			"Code.ZipFile, Code.S3Bucket/S3Key, or the _local_ extension is required")
	}
	sum := sha256.Sum256(raw)
	dir := filepath.Join(s.dataDir, "code", name)
	_ = os.RemoveAll(dir)
	if err := unzip(raw, dir); err != nil {
		return "", "", awshttp.Errf(400, "InvalidParameterValueException", "function code is not a valid zip: %v", err)
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

// dozeRuntime reports a function's process state — whether it's warm (holding
// one or more child processes) and the idle window after which an idle pool
// scales back to zero. A doze extension: the console surfaces it live so the
// resource cost of a function is visible.
func (s *Server) dozeRuntime(w http.ResponseWriter, name string) *awshttp.APIError {
	if _, err := s.store.GetFunction(name); err != nil {
		return awshttp.AsAPIError(err)
	}
	s.mu.Lock()
	p := s.runners[name]
	s.mu.Unlock()

	runners := 0
	idle := lambdaruntime.DefaultIdleTimeout
	var sleepAt int64
	if p != nil {
		runners = p.Size()
		idle = p.IdleTimeout()
		if dl, counting := p.SleepDeadline(); counting {
			sleepAt = dl.Unix()
		}
	}
	writeJSON(w, 200, map[string]any{
		"FunctionName":       name,
		"Warm":               runners > 0,
		"Runners":            runners,
		"IdleTimeoutSeconds": int(idle.Seconds()),
		"SleepAtUnix":        sleepAt, // 0 unless a countdown is running
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
	if aerr := validSizing(req.MemorySize, req.Timeout); aerr != nil {
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

// routeCodeSigning serves the code-signing config as a faithful round-trip.
func (s *Server) routeCodeSigning(w http.ResponseWriter, r *http.Request, name string) *awshttp.APIError {
	f, err := s.store.GetFunction(name)
	if err != nil {
		return awshttp.AsAPIError(err)
	}
	switch r.Method {
	case http.MethodGet:
		out := map[string]any{"FunctionName": name}
		if f.CodeSigningConfigArn != "" {
			out["CodeSigningConfigArn"] = f.CodeSigningConfigArn
		}
		writeJSON(w, 200, out)
		return nil
	case http.MethodPut:
		var req struct {
			CodeSigningConfigArn string `json:"CodeSigningConfigArn"`
		}
		if aerr := decode(r, &req); aerr != nil {
			return aerr
		}
		if _, err := s.store.Update(name, func(f *Function) error {
			f.CodeSigningConfigArn = req.CodeSigningConfigArn
			return nil
		}); err != nil {
			return awshttp.AsAPIError(err)
		}
		writeJSON(w, 200, map[string]any{
			"FunctionName": name, "CodeSigningConfigArn": req.CodeSigningConfigArn,
		})
		return nil
	case http.MethodDelete:
		if _, err := s.store.Update(name, func(f *Function) error {
			f.CodeSigningConfigArn = ""
			return nil
		}); err != nil {
			return awshttp.AsAPIError(err)
		}
		w.WriteHeader(204)
		return nil
	}
	return awshttp.Errf(405, "MethodNotAllowed", "unsupported code-signing-config request")
}

// listVersions implements ListVersionsByFunction. $LATEST always exists; any
// published versions follow it, oldest first, as AWS returns them.
func (s *Server) listVersions(w http.ResponseWriter, name string) *awshttp.APIError {
	f, err := s.store.GetFunction(name)
	if err != nil {
		return awshttp.AsAPIError(err)
	}
	versions := []any{s.configViewAt(f, "$LATEST")}
	if latest, ok := f.Aliases["$published"]; ok {
		for n := 1; ; n++ {
			v := strconv.Itoa(n)
			versions = append(versions, s.configViewAt(f, v))
			if v == latest {
				break
			}
		}
	}
	writeJSON(w, 200, map[string]any{"Versions": versions})
	return nil
}

// configViewAt renders a function's configuration as it appears at one version.
func (s *Server) configViewAt(f *Function, version string) map[string]any {
	view := s.configView(f)
	view["Version"] = version
	if version != "$LATEST" {
		view["FunctionArn"] = f.ARN() + ":" + version
	}
	return view
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
		"Architectures":    archOf(f),
	}
	if f.EphemeralStorageMB > 0 {
		view["EphemeralStorage"] = map[string]any{"Size": f.EphemeralStorageMB}
	}
	if f.TracingMode != "" {
		view["TracingConfig"] = map[string]any{"Mode": f.TracingMode}
	}
	if f.KMSKeyArn != "" {
		view["KMSKeyArn"] = f.KMSKeyArn
	}
	for key, raw := range map[string]json.RawMessage{
		"LoggingConfig": f.LoggingConfig, "SnapStart": f.SnapStart,
		"VpcConfig": f.VpcConfig, "FileSystemConfigs": f.FileSystemConfigs,
	} {
		if len(raw) > 0 {
			var v any
			if json.Unmarshal(raw, &v) == nil {
				view[key] = v
			}
		}
	}
	if len(f.Env) > 0 {
		view["Environment"] = map[string]any{"Variables": f.Env}
	}
	if f.DeadLetterArn != "" {
		view["DeadLetterConfig"] = map[string]any{"TargetArn": f.DeadLetterArn}
	}
	return view
}

// archOf reports the architectures the function declared, defaulting the way
// AWS does when none was given.
func archOf(f *Function) []string {
	if len(f.Architectures) > 0 {
		return f.Architectures
	}
	return []string{"x86_64"}
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

// Bounds Lambda states for a function's sizing, from its own service model
// (`dzaudit list --op CreateFunction lambda`). Zero means "not supplied" on
// both members — Lambda defaults them, so an absent value is not an
// out-of-range one.
//
// MemorySize was the gap the audit named: doze-aws does not allocate memory
// per function, so the value has no local effect and nothing had ever looked
// at it. A member the emulator ignores still has to be refused when it is
// invalid, or a function that CloudFormation would reject deploys clean here
// and fails in the account.
const (
	memoryMinMB = 128
	memoryMaxMB = 32768
	timeoutMinS = 1
	timeoutMaxS = 5400
)

func validSizing(memoryMB, timeoutS int) *awshttp.APIError {
	if memoryMB != 0 && (memoryMB < memoryMinMB || memoryMB > memoryMaxMB) {
		return awshttp.Errf(400, "InvalidParameterValueException",
			"1 validation error detected: Value '%d' at 'memorySize' failed to satisfy constraint: "+
				"Member must have value greater than or equal to %d and less than or equal to %d",
			memoryMB, memoryMinMB, memoryMaxMB)
	}
	if timeoutS != 0 && (timeoutS < timeoutMinS || timeoutS > timeoutMaxS) {
		return awshttp.Errf(400, "InvalidParameterValueException",
			"1 validation error detected: Value '%d' at 'timeout' failed to satisfy constraint: "+
				"Member must have value greater than or equal to %d and less than or equal to %d",
			timeoutS, timeoutMinS, timeoutMaxS)
	}
	return nil
}
