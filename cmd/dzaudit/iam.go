package main

// The IAM action each operation is authorized as.
//
// A request is not authorized as the operation it is: Lambda's Invoke is
// lambda:InvokeFunction, DeleteBucketCors is s3:PutBucketCORS, ListObjectVersions
// is s3:ListBucketVersions, and a read of one version of an object is
// s3:GetObjectVersion. Guessing the name from the method and path got twenty of
// Lambda's wrong and about as many of S3's — a policy that allowed
// lambda:AddPermission denied AddPermission, because the guess was
// lambda:CreatePermission, and an access log generated a policy full of actions
// AWS has never heard of. AWS says the truth itself, in two places:
//
//   - the service model's aws.iam#iamAction trait, for the models that carry it
//     (Lambda does): an optional name, and otherwise the operation's own name;
//   - the Service Authorization Reference, for the ones that do not (S3 does
//     not): every API operation and the actions it authorizes.
//
//     dzaudit iam lambda              # {"Actions": {"AddPermission": "lambda:AddPermission", …}}
//     dzaudit iam -go lambda lambda   # the same as a Go table for package lambda
//     dzaudit iam -go s3 s3           # S3's, from the reference
//
// An operation can authorize several actions (GetObject also needs
// s3:GetObjectTagging for a tagged object); the first one in the service is the
// operation's own, and is what the table holds. A request that names a version
// is authorized as the version's action instead — s3:GetObjectVersion — which
// the reference lists as an action of its own, so it is found there rather than
// written down.

import (
	"encoding/json"
	"fmt"
	"go/format"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

// referenceURL is AWS's Service Authorization Reference, one file a service.
const referenceURL = "https://servicereference.us-east-1.amazonaws.com/v1/"

type reference struct {
	Actions []struct {
		Name string `json:"Name"`
	} `json:"Actions"`
	Operations []struct {
		Name              string `json:"Name"`
		AuthorizedActions []struct {
			Name    string `json:"Name"`
			Service string `json:"Service"`
		} `json:"AuthorizedActions"`
	} `json:"Operations"`
}

// iamTable is what the tables hold and where it came from.
type iamTable struct {
	Actions   map[string]string `json:"Actions"`             // operation → action
	Versioned map[string]string `json:"Versioned,omitempty"` // action → the action for a named version
	source    string
}

func buildIAM(m *model, cache string) (*iamTable, error) {
	id, _, ops := m.service()
	var svc struct {
		ARNNamespace string `json:"arnNamespace"`
	}
	raw, ok := m.Shapes[id].Traits["aws.api#service"]
	if !ok || json.Unmarshal(raw, &svc) != nil || svc.ARNNamespace == "" {
		return nil, fmt.Errorf("the model names no arnNamespace, so there is no action prefix")
	}
	prefix := svc.ARNNamespace

	t := &iamTable{Actions: map[string]string{}}
	traited := 0
	for _, opID := range ops {
		name := shortName(opID)
		if raw, ok := m.Shapes[opID].Traits["aws.iam#iamAction"]; ok {
			traited++
			action := name
			var tr struct {
				Name string `json:"name"`
			}
			if json.Unmarshal(raw, &tr) == nil && tr.Name != "" {
				action = tr.Name
			}
			t.Actions[name] = prefix + ":" + action
		}
	}
	if traited > 0 {
		t.source = "the service model's aws.iam#iamAction trait"
		return t, nil
	}

	ref, err := loadReference(prefix, cache)
	if err != nil {
		return nil, err
	}
	known := map[string]bool{}
	for _, a := range ref.Actions {
		known[a.Name] = true
	}
	byOp := map[string]string{}
	for _, op := range ref.Operations {
		var own []string
		for _, a := range op.AuthorizedActions {
			if a.Service == prefix {
				own = append(own, a.Name)
			}
		}
		if len(own) > 0 {
			byOp[op.Name] = primaryAction(op.Name, own)
		}
	}
	for _, opID := range ops {
		name := shortName(opID)
		if action, ok := byOp[name]; ok {
			t.Actions[name] = prefix + ":" + action
		} else {
			fmt.Fprintf(os.Stderr, "dzaudit: %s has no action in the reference; it is left out\n", name)
		}
	}
	// The version's action is the operation's with "Version" after "Object":
	// GetObject → GetObjectVersion, PutObjectTagging → PutObjectVersionTagging.
	// It counts only if the reference lists it as an action of its own.
	t.Versioned = map[string]string{}
	for _, action := range t.Actions {
		bare := strings.TrimPrefix(action, prefix+":")
		if v := strings.Replace(bare, "Object", "ObjectVersion", 1); v != bare && known[v] {
			t.Versioned[action] = prefix + ":" + v
		}
	}
	t.source = "AWS's Service Authorization Reference"
	return t, nil
}

// destinationWrites are the operations that read one object to write another:
// the reference lists the source's read first, and the operation's own action
// is the destination's write. The read is authorized separately, by the guard.
var destinationWrites = map[string]string{"CopyObject": "PutObject", "UploadPartCopy": "PutObject"}

// primaryAction picks, among the actions an operation authorizes, its own: the
// one named like it, or like its singular (DeleteObjects → DeleteObject — the
// reference lists s3:BypassGovernanceRetention first), or else the one whose
// name begins most like it.
func primaryAction(op string, actions []string) string {
	if want, ok := destinationWrites[op]; ok {
		return want
	}
	for _, name := range []string{op, strings.TrimSuffix(op, "s")} {
		for _, a := range actions {
			if a == name {
				return a
			}
		}
	}
	// Neither: the action that shares the most with the operation's name —
	// ListObjects → ListBucket, where the reference lists GetObjectAcl first —
	// and the first of a tie.
	best, bestLen := actions[0], -1
	for _, a := range actions {
		n := 0
		for n < len(a) && n < len(op) && a[n] == op[n] {
			n++
		}
		if n > bestLen {
			best, bestLen = a, n
		}
	}
	return best
}

func loadReference(prefix, cache string) (*reference, error) {
	path := cache + "/" + prefix + ".reference.json"
	if _, err := os.Stat(path); err != nil {
		if err := os.MkdirAll(cache, 0o755); err != nil {
			return nil, err
		}
		c := &http.Client{Timeout: 60 * time.Second}
		resp, err := c.Get(referenceURL + prefix + "/" + prefix + ".json")
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("fetch the %s service reference: %s", prefix, resp.Status)
		}
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, err
		}
		if err := os.WriteFile(path, body, 0o644); err != nil {
			return nil, err
		}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var ref reference
	if err := json.Unmarshal(raw, &ref); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &ref, nil
}

func emitIAM(w io.Writer, m *model, cache string) error {
	t, err := buildIAM(m, cache)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(t)
}

// emitIAMGo writes the tables as Go: lazy, because a table built when the
// package loads is idle heap for every stack that never calls the service.
func emitIAMGo(w io.Writer, m *model, cache, pkg string) error {
	t, err := buildIAM(m, cache)
	if err != nil {
		return err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "// Code generated by dzaudit iam; DO NOT EDIT.\n\npackage %s\n\nimport \"sync\"\n\n", pkg)
	table := func(name, doc string, entries map[string]string) {
		keys := make([]string, 0, len(entries))
		for k := range entries {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		fmt.Fprintf(&b, "// %s\nvar %s = sync.OnceValue(func() map[string]string {\n\treturn map[string]string{\n", doc, name)
		for _, k := range keys {
			fmt.Fprintf(&b, "\t\t%q: %q,\n", k, entries[k])
		}
		b.WriteString("\t}\n})\n\n")
	}
	table("iamActions", "iamActions is the IAM action each operation is authorized as, from "+t.source+".\n// Regenerate: dzaudit iam -go "+pkg+" "+pkg, t.Actions)
	if len(t.Versioned) > 0 {
		table("iamVersioned", "iamVersioned is the action a request that names an object version is authorized\n// as instead, found in the reference: GetObject becomes GetObjectVersion.", t.Versioned)
	}
	src, err := format.Source([]byte(strings.TrimRight(b.String(), "\n") + "\n"))
	if err != nil {
		return err
	}
	_, err = w.Write(src)
	return err
}
