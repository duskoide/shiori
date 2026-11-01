package main

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestMigrateHome(t *testing.T) {
	dir := t.TempDir()
	old, home := filepath.Join(dir, "herdr-mygo"), filepath.Join(dir, "shiori")
	os.MkdirAll(old, 0o700)
	os.WriteFile(filepath.Join(old, "settings.json"), []byte("{}"), 0o600)

	// A window of the old name still running keeps its files.
	l, err := net.Listen("unix", filepath.Join(old, "herdr-mygo.sock"))
	if err != nil {
		t.Skip(err)
	}
	migrateHome(old, home)
	if _, err := os.Stat(home); err == nil {
		t.Fatal("moved the files of a running window")
	}
	l.Close()

	migrateHome(old, home)
	if _, err := os.Stat(filepath.Join(home, "settings.json")); err != nil {
		t.Fatal("not moved:", err)
	}
	if _, err := os.Stat(old); err == nil {
		t.Fatal("old home left behind")
	}
	// Once there is a home, an old one is left alone.
	os.MkdirAll(old, 0o700)
	migrateHome(old, home)
	if _, err := os.Stat(old); err != nil {
		t.Fatal("touched an old home although the new one exists")
	}
}
