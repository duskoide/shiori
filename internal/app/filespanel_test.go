package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFilesTree(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "b", "inner"), 0o755)
	os.MkdirAll(filepath.Join(root, ".git"), 0o755)
	os.WriteFile(filepath.Join(root, "a.txt"), nil, 0o644)
	os.WriteFile(filepath.Join(root, ".hidden"), nil, 0o644)
	os.WriteFile(filepath.Join(root, "b", "x.go"), nil, 0o644)
	var s filesState
	s.reset(root)
	rows := s.rows()
	if len(rows) != 2 || rows[0].entry.name != "b" || rows[1].entry.name != "a.txt" {
		t.Fatalf("rows %+v", rows)
	}
	s.open[filepath.Join(root, "b")] = true
	rows = s.rows()
	if len(rows) != 4 || rows[1].entry.name != "inner" || rows[1].depth != 1 || rows[3].entry.name != "a.txt" {
		t.Fatalf("open rows %+v", rows)
	}
}
