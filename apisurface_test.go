package dozeaws_test

// The exported surface of this module, written down.
//
// # Why this exists
//
// At 1.0.0 the exported API is a promise under semver: anything named here can
// only change in a v2. That makes an accidental export permanent, and an
// accidental export is easy — a helper made public to share it between two
// files inside the repo is indistinguishable, from the outside, from a
// deliberate contract.
//
// So the surface is a committed fixture, the same technique
// testdata/lightness.json uses for size: the ceiling gates, and the recorded
// value makes a change VISIBLE. Adding an export is fine; adding one without
// noticing is not. `task api:update` records it and puts it in the diff.
//
// # What it records, and what it therefore catches
//
// For each exported declaration: its kind, its name, and — for functions and
// methods — the full signature. Struct types additionally record their
// exported field names.
//
// That covers the breaking changes that actually happen: removing a symbol,
// renaming one, changing a parameter or result type, and removing a struct
// field. It does NOT cover changing an interface's method set, changing a
// constant's value, or a type's underlying representation. Those are real and
// rarer; this is not a substitute for reading the diff.
//
// # Why go/ast rather than `go doc`
//
// `go doc`'s output format is a presentation, not an interface — it wraps,
// elides and reflows. Parsing the source is exact and moves only when the
// source does.

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

var updateAPI = flag.Bool("api.update", false, "rewrite testdata/api.txt")

const apiPath = "testdata/api.txt"

// notPublic are the directories whose packages are not part of the promise:
// internal by the compiler's own rule, the command, and the two subprojects
// that are tooling rather than library code.
var notPublic = map[string]bool{
	"internal": true, "cmd": true, "e2e": true, "demo": true,
	"testdata": true, "bin": true, ".git": true, ".github": true,
	".claude": true, "node_modules": true, ".audit-models": true,
}

func TestThePublicAPIIsWhatWeSaidItWas(t *testing.T) {
	got := publicSurface(t)
	if len(got) == 0 {
		t.Fatal("no exported symbols found — this check would pass vacuously")
	}

	if *updateAPI {
		body := strings.Join(got, "\n") + "\n"
		if err := os.WriteFile(apiPath, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("recorded %d exported symbols in %s", len(got), apiPath)
		return
	}

	raw, err := os.ReadFile(apiPath)
	if err != nil {
		t.Fatalf("no recorded API surface: %v\n"+
			"  Run `go tool task api:update` and commit %s.", err, apiPath)
	}
	want := strings.Split(strings.TrimSpace(string(raw)), "\n")

	added, removed := diffLines(want, got)
	for _, s := range added {
		t.Errorf("NEW export: %s\n"+
			"  After 1.0.0 this is a promise that can only be withdrawn in a v2.\n"+
			"  If it is meant to be public, run `task api:update` and say why in the "+
			"commit.\n  If it is not, un-export it now — this is the last cheap "+
			"moment to do that.", s)
	}
	for _, s := range removed {
		t.Errorf("REMOVED export: %s\n"+
			"  Removing or renaming an exported symbol is a breaking change under "+
			"semver.\n  Before 1.0.0 that is free; after it, it needs a v2.", s)
	}
}

// publicSurface walks every package outside notPublic and renders its exported
// declarations, sorted, one per line.
func publicSurface(t *testing.T) []string {
	t.Helper()
	var out []string
	fset := token.NewFileSet()

	err := filepath.WalkDir(".", func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != "." && (notPublic[d.Name()] || strings.HasPrefix(d.Name(), ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, perr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if perr != nil {
			return fmt.Errorf("%s: %w", path, perr)
		}
		pkg := filepath.ToSlash(filepath.Dir(path))
		if pkg == "." {
			pkg = "dozeaws"
		}
		out = append(out, declsIn(fset, f, pkg)...)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(out)
	return out
}

func declsIn(fset *token.FileSet, f *ast.File, pkg string) []string {
	var out []string
	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if !d.Name.IsExported() {
				continue
			}
			recv := ""
			if d.Recv != nil && len(d.Recv.List) > 0 {
				name := render(fset, d.Recv.List[0].Type)
				// A method on an unexported type is not reachable from outside,
				// so it is not part of the promise.
				if !ast.IsExported(strings.TrimPrefix(name, "*")) {
					continue
				}
				recv = "(" + name + ") "
			}
			out = append(out, fmt.Sprintf("%s: func %s%s%s",
				pkg, recv, d.Name.Name, render(fset, d.Type)[len("func"):]))
		case *ast.GenDecl:
			out = append(out, genDecls(fset, d, pkg)...)
		}
	}
	return out
}

func genDecls(fset *token.FileSet, d *ast.GenDecl, pkg string) []string {
	var out []string
	for _, spec := range d.Specs {
		switch s := spec.(type) {
		case *ast.TypeSpec:
			if !s.Name.IsExported() {
				continue
			}
			out = append(out, fmt.Sprintf("%s: type %s %s", pkg, s.Name.Name, kindOf(s)))
			// Fields, because removing one is a breaking change and adding one
			// is not — so the two must be told apart in a diff.
			if st, ok := s.Type.(*ast.StructType); ok {
				for _, fld := range st.Fields.List {
					for _, n := range fld.Names {
						if n.IsExported() {
							out = append(out, fmt.Sprintf("%s: field %s.%s %s",
								pkg, s.Name.Name, n.Name, render(fset, fld.Type)))
						}
					}
				}
			}
		case *ast.ValueSpec:
			what := "var"
			if d.Tok == token.CONST {
				what = "const"
			}
			for _, n := range s.Names {
				if n.IsExported() {
					out = append(out, fmt.Sprintf("%s: %s %s", pkg, what, n.Name))
				}
			}
		}
	}
	return out
}

// kindOf names the shape of a type without printing the whole body, which
// would make every internal edit a diff in this file.
func kindOf(s *ast.TypeSpec) string {
	if s.Assign.IsValid() {
		return "= alias"
	}
	switch s.Type.(type) {
	case *ast.StructType:
		return "struct"
	case *ast.InterfaceType:
		return "interface"
	case *ast.FuncType:
		return "func"
	case *ast.MapType:
		return "map"
	case *ast.ArrayType:
		return "slice"
	}
	return "defined"
}

func render(fset *token.FileSet, n ast.Node) string {
	var b strings.Builder
	if err := printer.Fprint(&b, fset, n); err != nil {
		return "?"
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

func diffLines(want, got []string) (added, removed []string) {
	in := func(list []string, s string) bool {
		i := sort.SearchStrings(list, s)
		return i < len(list) && list[i] == s
	}
	w := append([]string(nil), want...)
	sort.Strings(w)
	for _, s := range got {
		if !in(w, s) {
			added = append(added, s)
		}
	}
	for _, s := range w {
		if !in(got, s) {
			removed = append(removed, s)
		}
	}
	return added, removed
}
