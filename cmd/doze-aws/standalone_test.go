package main

import (
	"os"
	"strings"
	"testing"

	"github.com/doze-dev/doze-aws/internal/config"
)

// The standalone binary is an address: 127.0.0.1:4566 unless told otherwise,
// with no machine setup, no names and no sudo.
func TestTheDefaultIsAnAddress(t *testing.T) {
	if got := config.Default().ListenAddr; got != "127.0.0.1:4566" {
		t.Errorf("default listen = %q, want 127.0.0.1:4566", got)
	}
	if _, ok := dispatch([]string{"dns-setup"}); !ok {
		t.Fatal("dns-setup should be refused as an unknown command, not started as a server")
	}
	for _, c := range commands() {
		if c.name == "dns-setup" {
			t.Error("dns-setup is still a command")
		}
	}
	mod, err := os.ReadFile("../../go.mod")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(mod), "doze-names") {
		t.Error("go.mod still requires doze-names")
	}
}
