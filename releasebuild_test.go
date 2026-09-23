package dozeaws_test

// Every target .goreleaser.yaml claims must actually compile.
//
// # The hole this closes
//
// The matrix said linux, darwin and windows. The Windows build had not
// compiled since doze-names joined as a dependency a month after v0.3.0: the
// .doze registry locks with syscall.Flock and manages the hosts file through
// Unix-only paths. Nothing noticed, because nothing had been released since,
// and GoReleaser builds every matrix entry — so the next tag would have failed
// the whole release rather than just one asset.
//
// Reading the matrix out of the config rather than listing targets here is the
// point: a target added to .goreleaser.yaml is a target this test starts
// building, and one that cannot compile fails before it is tagged.

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestEveryReleaseTargetCompiles(t *testing.T) {
	if testing.Short() {
		t.Skip("cross-compiles the whole binary once per target")
	}
	raw, err := os.ReadFile(".goreleaser.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Builds []struct {
			ID     string   `yaml:"id"`
			GOOS   []string `yaml:"goos"`
			GOARCH []string `yaml:"goarch"`
			Ignore []struct {
				GOOS   string `yaml:"goos"`
				GOARCH string `yaml:"goarch"`
			} `yaml:"ignore"`
		} `yaml:"builds"`
	}
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Builds) == 0 {
		t.Fatal("no builds in .goreleaser.yaml: this test would pass vacuously")
	}

	dir := t.TempDir()
	for _, b := range cfg.Builds {
		if len(b.GOOS) == 0 || len(b.GOARCH) == 0 {
			t.Errorf("build %q declares no goos/goarch", b.ID)
			continue
		}
		for _, goos := range b.GOOS {
			for _, goarch := range b.GOARCH {
				if ignored(b.Ignore, goos, goarch) {
					continue
				}
				t.Run(goos+"/"+goarch, func(t *testing.T) {
					// Same flags as the release, minus the version string: this
					// asks whether it compiles, not what it weighs.
					// binarysize_test.go owns the size.
					cmd := exec.Command("go", "build", "-trimpath",
						"-o", filepath.Join(dir, "doze-aws-"+goos+"-"+goarch), "./cmd/doze-aws")
					cmd.Env = append(os.Environ(),
						"GOWORK=off", "CGO_ENABLED=0", "GOOS="+goos, "GOARCH="+goarch)
					if out, err := cmd.CombinedOutput(); err != nil {
						t.Errorf("the release matrix claims %s/%s and it does not build:\n%s\n"+
							"  GoReleaser builds every entry, so a tag would fail the whole\n"+
							"  release. Fix the build or drop the target from .goreleaser.yaml.",
							goos, goarch, out)
					}
				})
			}
		}
	}
}

func ignored(rules []struct {
	GOOS   string `yaml:"goos"`
	GOARCH string `yaml:"goarch"`
}, goos, goarch string) bool {
	for _, r := range rules {
		if (r.GOOS == "" || r.GOOS == goos) && (r.GOARCH == "" || r.GOARCH == goarch) {
			return true
		}
	}
	return false
}
