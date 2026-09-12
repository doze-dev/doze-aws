package main

import (
	"os"
	"testing"

	names "github.com/doze-dev/doze-names"
)

// TestMain points the whole package at a throwaway .doze home.
//
// This is not tidiness. Several tests here go through gatewayFor, which asks
// the names registry where a running doze-aws is — and the registry is a file
// in the DEVELOPER'S home directory, shared with every doze binary on the
// machine. Without this, `go test ./cmd/doze-aws` on a machine where doze-aws
// happens to be running finds that real instance, applies the test's template
// to it, and then fails looking for the resources in the temp dir. That is
// exactly what happened the first time instance naming was wired up: three
// tests failed against 127.0.0.2:4566, the developer's own server.
//
// It cuts the other way too. These tests claim and release names; a shared
// home means a test run can disturb a server the developer is using. The
// binaries the main_test.go tests spawn inherit the variable, so they are
// isolated as well.
//
// DOZE_HOME is doze-names' own override and doze core reads the same variable,
// so this is the supported way to isolate rather than a test-only back door.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "doze-home")
	if err != nil {
		panic(err)
	}
	os.Setenv(names.EnvHome, home)
	code := m.Run()
	os.RemoveAll(home)
	os.Exit(code)
}
