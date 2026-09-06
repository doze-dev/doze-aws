package lambdaruntime

import (
	"os"
	"path/filepath"
	"strings"
)

// Layers, without /opt.
//
// On AWS a layer is unpacked into /opt and the runtimes find it there: Python
// on /opt/python, Node on /opt/nodejs/node_modules, Ruby on /opt/ruby/lib,
// binaries on /opt/bin, shared libraries on /opt/lib. A local process cannot
// be given a /opt without root, so each layer's directories are put on the
// same search paths the runtimes read — PYTHONPATH, NODE_PATH, RUBYLIB,
// GEM_PATH, PATH, LD_LIBRARY_PATH — in layer order, later layers first, which
// is the precedence AWS gives them. Code that opens "/opt/..." by literal
// path still does not find it; the ledger says so, and LAMBDA_LAYERS_DIRS
// names the directories for code that wants to look.

// layerEnv is the environment a set of extracted layer directories adds for
// a runtime, merged onto env (which wins for keys it already has).
func layerEnv(dirs []string, runtime string, env map[string]string) {
	if len(dirs) == 0 {
		return
	}
	// Later layers take precedence on AWS, so they come first on every path.
	ordered := make([]string, 0, len(dirs))
	for i := len(dirs) - 1; i >= 0; i-- {
		ordered = append(ordered, dirs[i])
	}
	env["LAMBDA_LAYERS_DIRS"] = strings.Join(ordered, string(os.PathListSeparator))

	add := func(key string, parts []string) {
		if len(parts) == 0 {
			return
		}
		// What the function set itself comes first; the parent's PATH and
		// library path come after the layers, as on AWS.
		if prior := env[key]; prior != "" {
			parts = append([]string{prior}, parts...)
		} else if inherited := os.Getenv(key); inherited != "" {
			parts = append(parts, inherited)
		}
		env[key] = strings.Join(parts, string(os.PathListSeparator))
	}
	var pyPaths, nodePaths, rubyLib, gemPaths, binPaths, libPaths []string
	for _, d := range ordered {
		pyPaths = append(pyPaths, existing(d, "python")...)
		pyPaths = append(pyPaths, globDirs(filepath.Join(d, "python", "lib", "python3*", "site-packages"))...)
		nodePaths = append(nodePaths, existing(d, filepath.Join("nodejs", "node_modules"))...)
		nodePaths = append(nodePaths, globDirs(filepath.Join(d, "nodejs", "node*", "node_modules"))...)
		rubyLib = append(rubyLib, existing(d, filepath.Join("ruby", "lib"))...)
		gemPaths = append(gemPaths, globDirs(filepath.Join(d, "ruby", "gems", "*"))...)
		binPaths = append(binPaths, existing(d, "bin")...)
		libPaths = append(libPaths, existing(d, "lib")...)
	}
	switch family(runtime) {
	case "python":
		add("PYTHONPATH", pyPaths)
	case "nodejs":
		add("NODE_PATH", nodePaths)
	case "ruby":
		add("RUBYLIB", rubyLib)
		add("GEM_PATH", gemPaths)
	}
	add("PATH", binPaths)
	add("LD_LIBRARY_PATH", libPaths)
	add("DYLD_LIBRARY_PATH", libPaths)
}

func existing(base, rel string) []string {
	p := filepath.Join(base, rel)
	if info, err := os.Stat(p); err == nil && info.IsDir() {
		return []string{p}
	}
	return nil
}

func globDirs(pattern string) []string {
	matches, _ := filepath.Glob(pattern)
	var out []string
	for _, m := range matches {
		if info, err := os.Stat(m); err == nil && info.IsDir() {
			out = append(out, m)
		}
	}
	return out
}
