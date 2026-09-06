package lambdaruntime

import (
	"archive/zip"
	"crypto/sha256"
	"embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Runtime clients for the interpreted runtimes, embedded and written to the
// data dir at start, so a python3.x, nodejs*.x or ruby3.x function runs
// with nothing installed but the interpreter. Each is the Lambda Runtime
// API loop in its own language — the same four routes the Go bootstrap and
// AWS's own clients speak — which is why an AWS client still works when a
// function ships one.

//go:embed shims/bootstrap.py shims/bootstrap.mjs shims/bootstrap.rb
var shimFS embed.FS

// shimFiles maps a runtime family to its embedded client.
var shimFiles = map[string]string{
	"python": "bootstrap.py",
	"nodejs": "bootstrap.mjs",
	"ruby":   "bootstrap.rb",
}

// Materialize writes the shims under dir, rewriting a file only when its
// content changed, and returns the directory.
func Materialize(dir string) (string, error) {
	// Absolute, because the path is handed to an interpreter whose working
	// directory is the function's code dir, not the server's.
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	for _, name := range shimFiles {
		want, err := shimFS.ReadFile("shims/" + name)
		if err != nil {
			return "", err
		}
		path := filepath.Join(dir, name)
		if have, err := os.ReadFile(path); err == nil && sha256.Sum256(have) == sha256.Sum256(want) {
			continue
		}
		tmp := path + ".tmp"
		if err := os.WriteFile(tmp, want, 0o644); err != nil {
			return "", err
		}
		if err := os.Rename(tmp, path); err != nil {
			return "", err
		}
	}
	return dir, nil
}

// family reduces a runtime identifier to what launches it.
func family(runtime string) string {
	switch {
	case runtime == "" || strings.HasPrefix(runtime, "go") || strings.HasPrefix(runtime, "provided"):
		return "provided"
	case strings.HasPrefix(runtime, "python"):
		return "python"
	case strings.HasPrefix(runtime, "nodejs"):
		return "nodejs"
	case strings.HasPrefix(runtime, "ruby"):
		return "ruby"
	case strings.HasPrefix(runtime, "java"):
		return "java"
	case strings.HasPrefix(runtime, "dotnet"):
		return "dotnet"
	}
	return ""
}

// interpreters names the executables each family is launched with, in the
// order they are looked for. A configured override wins over the lookup.
var interpreters = map[string][]string{
	"python": {"python3", "python"},
	"nodejs": {"node", "nodejs"},
	"ruby":   {"ruby"},
	"java":   {"java"},
	"dotnet": {"dotnet"},
}

// Interpreters is the configured overrides: family → executable path.
type Interpreters map[string]string

// Resolve finds the executable for a family, or explains what to install.
func (in Interpreters) Resolve(fam string) (string, error) {
	if p := in[fam]; p != "" {
		if _, err := os.Stat(p); err != nil {
			return "", fmt.Errorf("[lambda.runtimes] %s = %q: %v", fam, p, err)
		}
		return p, nil
	}
	for _, name := range interpreters[fam] {
		if p, err := exec.LookPath(name); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("runtime family %s needs %s on PATH, or [lambda.runtimes] %s = \"/path/to/%s\" in the config",
		fam, strings.Join(interpreters[fam], " or "), fam, interpreters[fam][0])
}

// CheckRuntime says whether a function of this runtime can be launched here,
// with the reason when it cannot — for CreateFunction to warn on, and the
// first invoke to fail with, rather than a timeout.
func CheckRuntime(runtime, dir string, in Interpreters) error {
	fam := family(runtime)
	switch fam {
	case "":
		return fmt.Errorf("unsupported runtime %q (use provided.*, go, python3.x, nodejs*, java*, ruby*, dotnet*, or set an explicit Command)", runtime)
	case "provided":
		return nil
	case "java":
		if _, err := in.Resolve("java"); err != nil {
			return err
		}
		return checkJavaRIC(dir)
	case "dotnet":
		if _, err := in.Resolve("dotnet"); err != nil {
			return err
		}
		if dotnetEntry(dir) == "" {
			return fmt.Errorf("a .NET function needs a self-hosting assembly: reference the NuGet package Amazon.Lambda.RuntimeSupport, call LambdaBootstrap from Main, and publish it as an executable in the code directory")
		}
		return nil
	}
	_, err := in.Resolve(fam)
	return err
}

// command resolves the runtime into argv. Spec.Command wins.
func (r *Runner) command() ([]string, error) {
	if len(r.spec.Command) > 0 {
		return append([]string(nil), r.spec.Command...), nil
	}
	fam := family(r.spec.Runtime)
	switch fam {
	case "provided":
		bin := r.spec.Handler
		if bin == "" {
			bin = "bootstrap"
		}
		return []string{"./" + strings.TrimPrefix(bin, "./")}, nil
	case "python", "nodejs", "ruby":
		interp, err := r.spec.Interpreters.Resolve(fam)
		if err != nil {
			return nil, err
		}
		if r.spec.ShimDir == "" {
			return nil, fmt.Errorf("runtime %s needs the embedded runtime client, which was not materialised (Spec.ShimDir)", r.spec.Runtime)
		}
		return []string{interp, filepath.Join(r.spec.ShimDir, shimFiles[fam]), r.spec.Handler}, nil
	case "java":
		interp, err := r.spec.Interpreters.Resolve("java")
		if err != nil {
			return nil, err
		}
		if err := checkJavaRIC(r.spec.Dir); err != nil {
			return nil, err
		}
		// The AWS Java runtime interface client, on the classpath the
		// package brought: its entry class reads the handler from argv.
		cp := strings.Join([]string{filepath.Join(r.spec.Dir, "*"), filepath.Join(r.spec.Dir, "lib", "*"), r.spec.Dir}, string(os.PathListSeparator))
		return []string{interp, "-cp", cp, "com.amazonaws.services.lambda.runtime.api.client.AWSLambda", r.spec.Handler}, nil
	case "dotnet":
		interp, err := r.spec.Interpreters.Resolve("dotnet")
		if err != nil {
			return nil, err
		}
		entry := dotnetEntry(r.spec.Dir)
		if entry == "" {
			return nil, fmt.Errorf("a .NET function needs a self-hosting assembly (Amazon.Lambda.RuntimeSupport, LambdaBootstrap in Main) published as an executable in %s", r.spec.Dir)
		}
		return []string{interp, entry}, nil
	}
	return nil, fmt.Errorf("unsupported runtime %q (use provided.*, go, python3.x, nodejs*, java*, ruby*, dotnet*, or set an explicit Command)", r.spec.Runtime)
}

// javaRICClass is the entry class of aws-lambda-java-runtime-interface-client.
const javaRICClass = "com/amazonaws/services/lambda/runtime/api/client/AWSLambda.class"

// checkJavaRIC looks for the AWS Java runtime interface client in the
// package's jars, and says what to add when it is absent.
func checkJavaRIC(dir string) error {
	if dir == "" {
		return nil
	}
	if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(javaRICClass))); err == nil {
		return nil
	}
	for _, pattern := range []string{"*.jar", "lib/*.jar"} {
		jars, _ := filepath.Glob(filepath.Join(dir, pattern))
		for _, jar := range jars {
			if jarHas(jar, javaRICClass) {
				return nil
			}
		}
	}
	return fmt.Errorf("a Java function needs the AWS runtime interface client in its package: add com.amazonaws:aws-lambda-java-runtime-interface-client (Maven) or implementation(\"com.amazonaws:aws-lambda-java-runtime-interface-client:2.+\") (Gradle) and ship it shaded or under lib/. It binds a Linux-only native client, so on macOS use an explicit Command")
}

func jarHas(jar, entry string) bool {
	z, err := zip.OpenReader(jar)
	if err != nil {
		return false
	}
	defer z.Close()
	for _, f := range z.File {
		if f.Name == entry {
			return true
		}
	}
	return false
}

// dotnetEntry finds the self-hosting assembly a published .NET Lambda
// executable leaves: the *.runtimeconfig.json names it.
func dotnetEntry(dir string) string {
	if dir == "" {
		return ""
	}
	configs, _ := filepath.Glob(filepath.Join(dir, "*.runtimeconfig.json"))
	for _, cfg := range configs {
		dll := strings.TrimSuffix(cfg, ".runtimeconfig.json") + ".dll"
		if _, err := os.Stat(dll); err == nil {
			// Only an assembly that carries the runtime support can serve; a
			// class library's runtimeconfig would launch and exit.
			if _, err := os.Stat(filepath.Join(dir, "Amazon.Lambda.RuntimeSupport.dll")); err == nil {
				return dll
			}
		}
	}
	return ""
}

