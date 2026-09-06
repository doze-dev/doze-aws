package lambda

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/doze-dev/doze-aws/internal/awshttp"
)

// Versions freeze a function: the code directory is copied under the data
// dir and the configuration is stored as it was, so an alias that routes to
// version 2 keeps running version 2 while $LATEST moves on. Publishing an
// unchanged function answers the version it already has, as AWS does, so a
// deploy tool that publishes on every run does not pile up copies.

// fingerprint is what a version freezes: the code and the configuration
// AWS snapshots. Two functions with the same fingerprint are the same
// version.
func fingerprint(f *Function) string {
	raw, _ := json.Marshal(map[string]any{
		"code": f.CodeSHA256, "runtime": f.Runtime, "handler": f.Handler, "env": f.Env, "timeout": f.Timeout,
		"memory": f.MemorySize, "layers": f.Layers, "role": f.Role, "command": f.Command,
		"logging": string(f.LoggingConfig), "dlq": f.DeadLetterArn, "arch": f.Architectures,
	})
	return string(raw)
}

func (s *Server) publishVersion(w http.ResponseWriter, r *http.Request, name string) *awshttp.APIError {
	f, err := s.store.GetFunction(name)
	if err != nil {
		return awshttp.AsAPIError(err)
	}
	var req struct {
		Description string `json:"Description"`
		RevisionID  string `json:"RevisionId"`
	}
	decode(r, &req)
	if req.RevisionID != "" && req.RevisionID != f.Revision {
		return awshttp.Errf(412, "PreconditionFailedException", "The RevisionId provided does not match the latest RevisionId for the Lambda function or alias. Call the GetFunction or the GetAlias API to retrieve the latest RevisionId for your resource.")
	}
	versions, err := s.store.ListVersions(name)
	if err != nil {
		return awshttp.AsAPIError(err)
	}
	// Code edited in place has no upload to hash; what is on disk now is
	// what this version would freeze.
	if s.isLocal(f.CodeDir) {
		f.CodeSHA256 = treeHash(f.CodeDir)
	}
	if n := len(versions); n > 0 && fingerprint(versions[n-1]) == fingerprint(f) {
		// Nothing changed since the last publish: that version is the answer.
		writeJSON(w, 201, s.configView(versions[n-1]))
		return nil
	}
	next := len(versions) + 1
	if n := len(versions); n > 0 {
		if last, err := strconv.Atoi(versions[n-1].Version); err == nil {
			next = last + 1
		}
	}
	frozen := *f
	frozen.Version = strconv.Itoa(next)
	if req.Description != "" {
		frozen.Description = req.Description
	}
	frozen.CodeDir = filepath.Join(s.dataDir, "versions", name, frozen.Version)
	// A _local_ directory is copied too: freezing is the point of a
	// version, and the live function keeps reading the directory in place.
	if err := copyTree(f.CodeDir, frozen.CodeDir, !s.isLocal(f.CodeDir)); err != nil {
		return awshttp.Errf(500, "ServiceException", "freezing the code for version %s: %v", frozen.Version, err)
	}
	frozen.Aliases = nil
	frozen.FunctionURL = ""
	if err := s.store.PutVersion(&frozen); err != nil {
		return awshttp.AsAPIError(err)
	}
	writeJSON(w, 201, s.configView(&frozen))
	return nil
}

// listVersions implements ListVersionsByFunction: $LATEST first, then the
// published versions oldest first, as AWS returns them.
func (s *Server) listVersions(w http.ResponseWriter, name string) *awshttp.APIError {
	f, err := s.store.GetFunction(name)
	if err != nil {
		return awshttp.AsAPIError(err)
	}
	views := []any{s.configView(f)}
	versions, err := s.store.ListVersions(name)
	if err != nil {
		return awshttp.AsAPIError(err)
	}
	for _, v := range versions {
		views = append(views, s.configView(v))
	}
	writeJSON(w, 200, map[string]any{"Versions": views})
	return nil
}

// splitQualifier separates a function reference — a name, "name:qualifier",
// a partial or full ARN with or without a qualifier — into the bare name
// and the qualifier, if any.
func splitQualifier(ref string) (name, qualifier string) {
	if i := strings.Index(ref, ":function:"); i >= 0 {
		ref = ref[i+len(":function:"):]
	}
	if i := strings.IndexByte(ref, ':'); i >= 0 {
		return ref[:i], ref[i+1:]
	}
	return ref, ""
}

// resolve finds the record an invocation runs: the live function for no
// qualifier or $LATEST, a frozen version for a number, and an alias's
// target otherwise. It answers the qualifier that was used, for the ARN the
// function sees and the X-Amz-Executed-Version header.
func (s *Server) resolve(ref, qualifier string) (*Function, string, *awshttp.APIError) {
	name, inRef := splitQualifier(ref)
	if qualifier == "" {
		qualifier = inRef
	}
	f, err := s.store.GetFunction(name)
	if err != nil {
		return nil, "", awshttp.AsAPIError(err)
	}
	if qualifier == "" || qualifier == "$LATEST" {
		return f, "$LATEST", nil
	}
	version := qualifier
	if _, isNumber := strconv.Atoi(qualifier); isNumber != nil {
		target, ok := f.Aliases[qualifier]
		if !ok || strings.HasPrefix(qualifier, "$") {
			return nil, "", awshttp.Errf(404, "ResourceNotFoundException", "Function not found: %s:%s", f.ARN(), qualifier)
		}
		version = target
		if version == "$LATEST" || version == "" {
			return f, "$LATEST", nil
		}
	}
	n, err := strconv.Atoi(version)
	if err != nil {
		return nil, "", awshttp.Errf(404, "ResourceNotFoundException", "Function not found: %s:%s", f.ARN(), qualifier)
	}
	frozen, err := s.store.GetVersion(name, n)
	if err != nil {
		return nil, "", awshttp.AsAPIError(err)
	}
	// The live record's aliases and URL belong to the name, not the version.
	frozen.Aliases = f.Aliases
	return frozen, version, nil
}

// isLocal reports whether a code directory is one the user owns (the
// _local_ extension) rather than one unpacked under the data dir.
func (s *Server) isLocal(dir string) bool {
	rel, err := filepath.Rel(s.dataDir, dir)
	return err != nil || strings.HasPrefix(rel, "..")
}

// treeHash is the content fingerprint of a directory the user edits in
// place: every file's relative path, size and modification time. A zip has
// a real CodeSha256; a _local_ directory has this, which is what tells a
// publish that the code changed since the last version.
func treeHash(dir string) string {
	h := sha256.New()
	filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		fmt.Fprintf(h, "%s\x00%d\x00%d\n", rel, info.Size(), info.ModTime().UnixNano())
		return nil
	})
	return hex.EncodeToString(h.Sum(nil))
}

// copyTree copies a directory. Files under the data dir are hard-linked
// where the filesystem allows, so a large package is not duplicated; a
// directory the user edits in place is copied for real, because a hard link
// would let an edit reach into the frozen version.
func copyTree(src, dst string, link bool) error {
	src, err := filepath.Abs(src)
	if err != nil {
		return err
	}
	_ = os.RemoveAll(dst)
	return filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		switch {
		case info.IsDir():
			return os.MkdirAll(target, 0o755)
		case info.Mode()&os.ModeSymlink != 0:
			link, err := os.Readlink(p)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		}
		if link {
			if err := os.Link(p, target); err == nil {
				return nil
			}
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode())
		if err != nil {
			return err
		}
		defer out.Close()
		_, err = io.Copy(out, in)
		return err
	})
}
