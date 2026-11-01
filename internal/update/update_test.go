package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompare(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
	}{
		{"0.1.0", "0.2.0", -1},
		{"v0.2.0", "0.2.0", 0},
		{"0.10.0", "0.9.9", 1},
		{"1.0.0", "1.0.0-rc.1", 1},
		{"1.0.0-rc.2", "1.0.0-rc.10", -1},
		{"1.0.0-alpha", "1.0.0-beta", -1},
		{"0.1.0+3e6086e-dirty", "0.1.0", 0},
		{"0.1", "0.1.0", 0},
		{"junk", "0.0.1", -1},
	} {
		if got := Compare(tc.a, tc.b); got != tc.want {
			t.Errorf("Compare(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
	for v, dev := range map[string]bool{"0.1.0-dev": true, "0.0.0+abc": true, "junk": true, "0.1.0": false, "0.1.0+abc": false, "1.0.0-rc.1": false} {
		if Development(v) != dev {
			t.Errorf("Development(%q) = %v", v, !dev)
		}
	}
}

// fakeGitHub serves a release whose archive holds files.
type fakeGitHub struct {
	*httptest.Server
	tag      string
	archive  []byte
	sums     string
	sawToken string
	sawUA    string
}

func newFakeGitHub(t *testing.T, tag string, files map[string]string) *fakeGitHub {
	t.Helper()
	f := &fakeGitHub{tag: tag, archive: tarGz(t, files)}
	sum := sha256.Sum256(f.archive)
	f.sums = hex.EncodeToString(sum[:]) + "  " + ArchiveName() + "\n"
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.sawToken, f.sawUA = r.Header.Get("Authorization"), r.Header.Get("User-Agent")
		switch r.URL.Path {
		case "/repos/o/r/releases/latest", "/repos/o/r/releases/tags/" + f.tag:
			json.NewEncoder(w).Encode(map[string]any{
				"tag_name": f.tag, "html_url": "https://example.com/" + f.tag, "body": "notes",
				"assets": []map[string]any{
					{"name": ArchiveName(), "size": len(f.archive), "browser_download_url": f.URL + "/dl/archive", "url": f.URL + "/api/archive"},
					{"name": "checksums.txt", "browser_download_url": f.URL + "/dl/sums", "url": f.URL + "/api/sums"},
				}})
		case "/dl/archive", "/api/archive":
			w.Write(f.archive)
		case "/dl/sums", "/api/sums":
			w.Write([]byte(f.sums))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeGitHub) opts() Options { return Options{API: f.URL, Repo: "o/r"} }

func tarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg})
		tw.Write([]byte(body))
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func TestLatest(t *testing.T) {
	gh := newFakeGitHub(t, "v0.3.0", map[string]string{"bin/shiori": "x"})
	o := gh.opts()
	o.Token = "secret"
	r, err := Latest(context.Background(), o, "")
	if err != nil || r.Version() != "0.3.0" || r.URL != "https://example.com/v0.3.0" {
		t.Fatalf("%+v %v", r, err)
	}
	if gh.sawToken != "Bearer secret" || gh.sawUA != "shiori-update" {
		t.Fatalf("headers %q %q", gh.sawToken, gh.sawUA)
	}
	if r, err := Latest(context.Background(), gh.opts(), "0.3.0"); err != nil || r.Tag != "v0.3.0" {
		t.Fatalf("by tag: %+v %v", r, err)
	}
	if _, err := Latest(context.Background(), gh.opts(), "v9.9.9"); !errors.Is(err, ErrNoRelease) {
		t.Fatalf("missing tag: %v", err)
	}
	if _, err := Latest(context.Background(), Options{API: "http://127.0.0.1:1", Repo: "o/r"}, ""); err == nil {
		t.Fatal("offline gave no error")
	}
}

// installed is a prefix as install.sh leaves it: bin/shiori and the core.
func installed(t *testing.T, core bool) Layout {
	t.Helper()
	prefix := t.TempDir()
	os.MkdirAll(filepath.Join(prefix, "bin"), 0o755)
	exe := filepath.Join(prefix, "bin", "shiori")
	os.WriteFile(exe, []byte("#!/bin/sh\necho shiori 0.1.0\n"), 0o755)
	l := Layout{Exe: exe, CoreDir: filepath.Join(prefix, "libexec", "shiori")}
	if core {
		os.MkdirAll(l.CoreDir, 0o755)
		os.WriteFile(filepath.Join(l.CoreDir, "hc"), []byte("old core"), 0o755)
	}
	return l
}

var release = map[string]string{
	"./bin/shiori":           "#!/bin/sh\necho shiori 0.3.0\n",
	"./libexec/shiori/hc":    "new core",
	"./README.md":            "readme",
	"./libexec/shiori/extra": "x",
}

func TestInstall(t *testing.T) {
	gh := newFakeGitHub(t, "v0.3.0", release)
	r, _ := Latest(context.Background(), gh.opts(), "")
	l := installed(t, true)
	var steps []string
	if err := Install(context.Background(), gh.opts(), r, l, func(s string) { steps = append(steps, s) }); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(l.Exe); !strings.Contains(string(b), "0.3.0") {
		t.Fatalf("binary %s", b)
	}
	if b, _ := os.ReadFile(filepath.Join(l.CoreDir, "hc")); string(b) != "new core" {
		t.Fatalf("core %s", b)
	}
	joined := strings.Join(steps, "\n")
	if !strings.Contains(joined, "checksum ok") || !strings.Contains(joined, "unpacked shiori 0.3.0") {
		t.Fatalf("steps %s", joined)
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(l.Exe), ".shiori-*")); len(left) != 0 {
		t.Fatalf("left over %v", left)
	}
	// Without a bundled core (install.sh --no-core), none is added.
	l2 := installed(t, false)
	if err := Install(context.Background(), gh.opts(), r, l2, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(l2.CoreDir); !os.IsNotExist(err) {
		t.Fatal("a core was added")
	}
}

func TestInstallRefuses(t *testing.T) {
	gh := newFakeGitHub(t, "v0.3.0", release)
	r, _ := Latest(context.Background(), gh.opts(), "")
	l := installed(t, true)
	old, _ := os.ReadFile(l.Exe)
	unchanged := func(what string) {
		t.Helper()
		if b, _ := os.ReadFile(l.Exe); !bytes.Equal(b, old) {
			t.Fatalf("%s: the binary changed", what)
		}
	}

	gh.sums = strings.Repeat("0", 64) + "  " + ArchiveName() + "\n"
	if err := Install(context.Background(), gh.opts(), r, l, nil); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("bad sum: %v", err)
	}
	unchanged("bad sum")

	for name, files := range map[string]map[string]string{
		"no binary": {"./README.md": "x"},
		"escapes":   {"../evil": "x", "./bin/shiori": "#!/bin/sh\n"},
		"no run":    {"./bin/shiori": "#!/bin/sh\nexit 1\n"},
	} {
		gh2 := newFakeGitHub(t, "v0.3.0", files)
		r2, _ := Latest(context.Background(), gh2.opts(), "")
		if err := Install(context.Background(), gh2.opts(), r2, l, nil); err == nil {
			t.Errorf("%s: installed", name)
		}
		unchanged(name)
	}

	r.Assets = r.Assets[1:]
	if err := Install(context.Background(), gh.opts(), r, l, nil); err == nil || !strings.Contains(err.Error(), "has no") {
		t.Fatalf("no archive: %v", err)
	}
}

func TestChecksumOf(t *testing.T) {
	list := "abc  shiori_linux_amd64.tar.gz\nDEF *install.sh\n"
	if s, ok := checksumOf(list, "install.sh"); !ok || s != "def" {
		t.Fatal(s)
	}
	if _, ok := checksumOf(list, "x"); ok {
		t.Fatal("found")
	}
}
