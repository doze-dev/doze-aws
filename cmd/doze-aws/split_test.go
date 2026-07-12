package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestSplitApplyArgs(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		file    string
		vars    map[string]string
		flags   []string
		wantErr string
	}{
		{name: "empty", args: nil, vars: map[string]string{}},
		{name: "file only", args: []string{"infra.yaml"}, file: "infra.yaml", vars: map[string]string{}},
		{
			// The regression this exists for: a space-separated flag value must
			// stay with its flag, not become the stack file.
			name:  "flag with space value",
			args:  []string{"--data-dir", "./data", "infra.yaml"},
			file:  "infra.yaml",
			vars:  map[string]string{},
			flags: []string{"--data-dir", "./data"},
		},
		{
			name:  "flag with = value",
			args:  []string{"--data-dir=./data", "infra.yaml"},
			file:  "infra.yaml",
			vars:  map[string]string{},
			flags: []string{"--data-dir=./data"},
		},
		{
			name:  "bool flag does not eat the file",
			args:  []string{"--console", "infra.yaml"},
			file:  "infra.yaml",
			vars:  map[string]string{},
			flags: []string{"--console"},
		},
		{
			name: "vars in all three spellings",
			args: []string{"--var", "a=1", "--var=b=2", "-var=c=3", "infra.yaml"},
			file: "infra.yaml",
			vars: map[string]string{"a": "1", "b": "2", "c": "3"},
		},
		{
			name:  "file before flags",
			args:  []string{"infra.yaml", "--listen", "127.0.0.1:9999"},
			file:  "infra.yaml",
			vars:  map[string]string{},
			flags: []string{"--listen", "127.0.0.1:9999"},
		},
		{name: "var missing value", args: []string{"--var"}, wantErr: "name=value"},
		{name: "var missing =", args: []string{"--var", "a"}, wantErr: "name=value"},
		{name: "two files", args: []string{"a.yaml", "b.yaml"}, wantErr: "unexpected argument"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			file, vars, flags, err := splitApplyArgs(c.args)
			if c.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("want error containing %q, got %v", c.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if file != c.file {
				t.Errorf("file = %q, want %q", file, c.file)
			}
			if !reflect.DeepEqual(vars, c.vars) {
				t.Errorf("vars = %v, want %v", vars, c.vars)
			}
			if !reflect.DeepEqual(flags, c.flags) {
				t.Errorf("flags = %v, want %v", flags, c.flags)
			}
		})
	}
}

func TestFlagTakesValue(t *testing.T) {
	for name, want := range map[string]bool{
		"data-dir": true, "listen": true, "stack": true, "config": true,
		"console": false, // boolean
		"nope":    false, // unregistered
	} {
		if got := flagTakesValue(name); got != want {
			t.Errorf("flagTakesValue(%q) = %v, want %v", name, got, want)
		}
	}
}
