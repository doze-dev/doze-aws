package main

import (
	"os"
	"testing"
)

// TestMain points the package at a throwaway DOZE_HOME, and then holds it to
// what the standalone binary promises: it writes nothing there. doze-aws no
// longer takes part in doze's .doze names (they are doze's), so nothing it or
// its tests run may touch the home doze shares with the machine.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "doze-home")
	if err != nil {
		panic(err)
	}
	os.Setenv("DOZE_HOME", home)
	code := m.Run()
	if entries, _ := os.ReadDir(home); len(entries) > 0 && code == 0 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		println("doze-aws wrote under DOZE_HOME, which it must not:", names[0])
		code = 1
	}
	os.RemoveAll(home)
	os.Exit(code)
}
