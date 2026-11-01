package main

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

func TestParseArgs(t *testing.T) {
	for _, tc := range []struct {
		in   []string
		want map[string]any
		err  bool
	}{
		{nil, map[string]any{}, false},
		{[]string{"--direction", "right"}, map[string]any{"direction": "right"}, false},
		{[]string{"--name=api", "--argv", "[\"pi\"]"}, map[string]any{"name": "api", "argv": "[\"pi\"]"}, false},
		{[]string{`{"direction":"down","n":2}`}, map[string]any{"direction": "down", "n": float64(2)}, false},
		{[]string{"--direction"}, nil, true},
		{[]string{"direction", "right"}, nil, true},
		{[]string{"{bad"}, nil, true},
	} {
		got, err := parseArgs(tc.in)
		if (err != nil) != tc.err || (!tc.err && !reflect.DeepEqual(got, tc.want)) {
			t.Errorf("parseArgs(%q) = %v, %v", tc.in, got, err)
		}
	}
}

func TestRunWithoutWindow(t *testing.T) {
	t.Setenv("SHIORI_HOME", t.TempDir())
	var out bytes.Buffer
	if err := run([]string{"--version"}, &out); err != nil || !strings.HasPrefix(out.String(), "shiori ") {
		t.Fatalf("--version: %q %v", out.String(), err)
	}
	out.Reset()
	if err := run([]string{"commands"}, &out); err != nil || !strings.Contains(out.String(), "\"pane.split\"") {
		t.Fatalf("commands: %v", err)
	}
	if err := run([]string{"ls"}, &out); err == nil || !strings.Contains(err.Error(), "no shiori window") {
		t.Fatalf("ls without a window: %v", err)
	}
	if err := run([]string{"do"}, &out); err == nil {
		t.Fatal("do without a command")
	}
	if err := run([]string{"nope"}, &out); err == nil {
		t.Fatal("unknown command")
	}
}
