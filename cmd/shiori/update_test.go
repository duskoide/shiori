package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/duskoide/shiori/internal/update"
)

// fakeReleases serves release v0.2.0, whose archive holds a shiori that
// says it is 0.2.0.
func fakeReleases(t *testing.T) {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	body := "#!/bin/sh\necho shiori 0.2.0\n"
	tw.WriteHeader(&tar.Header{Name: "./bin/shiori", Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg})
	tw.Write([]byte(body))
	tw.Close()
	gz.Close()
	archive := buf.Bytes()
	sum := sha256.Sum256(archive)
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/o/r/releases/latest", "/repos/o/r/releases/tags/v0.2.0":
			json.NewEncoder(w).Encode(map[string]any{"tag_name": "v0.2.0", "html_url": "https://example.com/v0.2.0",
				"assets": []map[string]any{
					{"name": update.ArchiveName(), "browser_download_url": srv.URL + "/a"},
					{"name": "checksums.txt", "browser_download_url": srv.URL + "/s"},
				}})
		case "/a":
			w.Write(archive)
		case "/s":
			w.Write([]byte(hex.EncodeToString(sum[:]) + "  " + update.ArchiveName() + "\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("SHIORI_UPDATE_API", srv.URL)
	t.Setenv("SHIORI_REPO", "o/r")
	t.Setenv("SHIORI_HOME", t.TempDir())
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
}

func setVersion(t *testing.T, v string) {
	old := version
	version = v
	t.Cleanup(func() { version = old })
}

func TestUpdateCheck(t *testing.T) {
	fakeReleases(t)
	setVersion(t, "0.1.0")
	var out bytes.Buffer
	if err := run([]string{"update", "--check"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Shiori 0.2.0 is out; this is 0.1.0") {
		t.Fatalf("out %q", out.String())
	}
	setVersion(t, "0.2.0")
	out.Reset()
	run([]string{"update", "--check"}, &out)
	if !strings.Contains(out.String(), "is up to date") {
		t.Fatalf("out %q", out.String())
	}
	if err := run([]string{"update", "--soon"}, &out); err == nil {
		t.Fatal("a bad flag was taken")
	}
	if err := run([]string{"update", "--version", "v9.9.9"}, &out); err == nil || !strings.Contains(err.Error(), "no release v9.9.9") {
		t.Fatalf("err %v", err)
	}
}

func TestUpdateInstalls(t *testing.T) {
	fakeReleases(t)
	setVersion(t, "0.1.0")
	prefix := t.TempDir()
	exe := filepath.Join(prefix, "bin", "shiori")
	os.MkdirAll(filepath.Dir(exe), 0o755)
	os.WriteFile(exe, []byte("#!/bin/sh\necho shiori 0.1.0\n"), 0o755)
	old := hereLayout
	hereLayout = func() (update.Layout, error) {
		return update.Layout{Exe: exe, CoreDir: filepath.Join(prefix, "libexec", "shiori")}, nil
	}
	t.Cleanup(func() { hereLayout = old })
	var out bytes.Buffer
	if err := run([]string{"update"}, &out); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(exe); !strings.Contains(string(b), "0.2.0") {
		t.Fatalf("binary %s", b)
	}
	for _, want := range []string{"Updating Shiori 0.1.0 to 0.2.0", "checksum ok", "Shiori 0.2.0 is installed"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("no %q in %q", want, out.String())
		}
	}
	// The window knows of it, and does not look again at once.
	b, _ := os.ReadFile(filepath.Join(os.Getenv("SHIORI_HOME"), "update.json"))
	if !strings.Contains(string(b), `"latest": "0.2.0"`) {
		t.Fatalf("update.json %s", b)
	}
	setVersion(t, "0.2.0")
	out.Reset()
	run([]string{"update"}, &out)
	if !strings.Contains(out.String(), "is up to date") {
		t.Fatalf("out %q", out.String())
	}
}
