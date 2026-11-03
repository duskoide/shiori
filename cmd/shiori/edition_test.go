package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/duskoide/shiori/internal/app"
)

// A dev build keeps apart from the installed Shiori: its own files, its
// own core session, no updates.
func TestDevEdition(t *testing.T) {
	old := edition
	t.Cleanup(func() { edition, app.Edition = old, "" })
	t.Setenv("SHIORI_HOME", "")
	t.Setenv("HC_SESSION", "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	edition = "dev"
	var out bytes.Buffer
	if err := run([]string{"--version"}, &out); err != nil || !strings.HasSuffix(strings.TrimSpace(out.String()), "(dev)") {
		t.Fatalf("version %q, %v", out.String(), err)
	}
	if filepath.Base(app.DefaultHome()) != "shiori-dev" || app.DefaultSession() != "shiori-dev" {
		t.Fatalf("home %s, session %q", app.DefaultHome(), app.DefaultSession())
	}
	t.Setenv("HC_SESSION", "default")
	if app.DefaultSession() != "default" {
		t.Fatal("HC_SESSION does not win")
	}
	if err := run([]string{"update", "--check"}, &out); err == nil || !strings.Contains(err.Error(), "dev build") {
		t.Fatalf("update in a dev build: %v", err)
	}

	edition = ""
	t.Setenv("HC_SESSION", "")
	if err := run([]string{"--version"}, &out); err != nil {
		t.Fatal(err)
	}
	if filepath.Base(app.DefaultHome()) != "shiori" || app.DefaultSession() != "" {
		t.Fatalf("release: home %s, session %q", app.DefaultHome(), app.DefaultSession())
	}
}
