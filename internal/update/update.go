// Package update finds out whether a newer Shiori is out, and installs it.
//
// It asks GitHub for the repository's latest release: one HTTPS request,
// which carries nothing about the person or the machine beyond what any
// request does. Installing downloads the release's archive for this OS and
// CPU, checks it against the release's checksums.txt, and puts its files
// where install.sh put them: bin/shiori, and the bundled herdr-core in
// libexec/shiori beside it.
package update

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// DefaultRepo is where releases come from.
const DefaultRepo = "duskoide/shiori"

// Options say where to look and how.
type Options struct {
	API    string // GitHub's API, "" for https://api.github.com ($SHIORI_UPDATE_API)
	Repo   string // owner/name, "" for DefaultRepo ($SHIORI_REPO)
	Token  string // for a private repository ($GITHUB_TOKEN, $GH_TOKEN)
	Client *http.Client
}

// FromEnv are the options the environment sets.
func FromEnv() Options {
	tok := os.Getenv("GITHUB_TOKEN")
	if tok == "" {
		tok = os.Getenv("GH_TOKEN")
	}
	return Options{API: os.Getenv("SHIORI_UPDATE_API"), Repo: os.Getenv("SHIORI_REPO"), Token: tok}
}

func (o Options) api() string {
	if o.API != "" {
		return strings.TrimRight(o.API, "/")
	}
	return "https://api.github.com"
}

func (o Options) repo() string { return o.RepoName() }

// RepoName is the repository releases come from.
func (o Options) RepoName() string {
	if o.Repo != "" {
		return o.Repo
	}
	return DefaultRepo
}

func (o Options) client() *http.Client {
	if o.Client != nil {
		return o.Client
	}
	return &http.Client{Timeout: 5 * time.Minute}
}

// Release is a published release.
type Release struct {
	Tag       string    `json:"tag_name"`
	Name      string    `json:"name"`
	URL       string    `json:"html_url"`
	Notes     string    `json:"body"`
	Published time.Time `json:"published_at"`
	Draft     bool      `json:"draft"`
	Assets    []Asset   `json:"assets"`
}

// Asset is a file of a release.
type Asset struct {
	Name        string `json:"name"`
	Size        int64  `json:"size"`
	DownloadURL string `json:"browser_download_url"`
	APIURL      string `json:"url"`
}

// Version is the release's version without its v.
func (r Release) Version() string { return strings.TrimPrefix(r.Tag, "v") }

// ArchiveName is the archive of a release for this OS and CPU.
func ArchiveName() string { return fmt.Sprintf("shiori_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH) }

// Asset is the release's file of a name.
func (r Release) Asset(name string) (Asset, bool) {
	for _, a := range r.Assets {
		if a.Name == name {
			return a, true
		}
	}
	return Asset{}, false
}

// ErrNoRelease is the error when the repository has no release (or, for a
// private one without a token, none that can be seen).
var ErrNoRelease = errors.New("no release found")

// Latest is the latest release; tag, when not "", is the release of that
// tag instead.
func Latest(ctx context.Context, o Options, tag string) (Release, error) {
	path := "/repos/" + o.repo() + "/releases/latest"
	if tag != "" {
		if !strings.HasPrefix(tag, "v") {
			tag = "v" + tag
		}
		path = "/repos/" + o.repo() + "/releases/tags/" + tag
	}
	req, err := o.request(ctx, o.api()+path, "application/vnd.github+json")
	if err != nil {
		return Release{}, err
	}
	resp, err := o.client().Do(req)
	if err != nil {
		return Release{}, err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return Release{}, ErrNoRelease
	case resp.StatusCode != http.StatusOK:
		return Release{}, fmt.Errorf("GitHub answered %s", resp.Status)
	}
	var r Release
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&r); err != nil {
		return Release{}, fmt.Errorf("GitHub's answer: %w", err)
	}
	if r.Tag == "" {
		return Release{}, ErrNoRelease
	}
	return r, nil
}

func (o Options) request(ctx context.Context, url, accept string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	// GitHub wants a User-Agent; this one names the app alone.
	req.Header.Set("User-Agent", "shiori-update")
	req.Header.Set("Accept", accept)
	if o.Token != "" {
		req.Header.Set("Authorization", "Bearer "+o.Token)
	}
	return req, nil
}

// fetch downloads an asset: by its API URL with a token (a private
// repository's assets are not public), else by its download URL.
func (o Options) fetch(ctx context.Context, a Asset, w io.Writer, limit int64) error {
	url, accept := a.DownloadURL, "*/*"
	if o.Token != "" && a.APIURL != "" {
		url, accept = a.APIURL, "application/octet-stream"
	}
	req, err := o.request(ctx, url, accept)
	if err != nil {
		return err
	}
	resp, err := o.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("downloading %s: %s", a.Name, resp.Status)
	}
	n, err := io.Copy(w, io.LimitReader(resp.Body, limit+1))
	if err == nil && n > limit {
		err = fmt.Errorf("%s is larger than %d bytes", a.Name, limit)
	}
	return err
}

// --- Versions -----------------------------------------------------------

// semver is a version's numbers and pre-release; build metadata (after +)
// does not count.
type semver struct {
	nums [3]int
	pre  string
	ok   bool
}

func parse(v string) semver {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	v, _, _ = strings.Cut(v, "+")
	core, pre, _ := strings.Cut(v, "-")
	parts := strings.Split(core, ".")
	if len(parts) == 0 || len(parts) > 3 {
		return semver{}
	}
	var s semver
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return semver{}
		}
		s.nums[i] = n
	}
	s.pre, s.ok = pre, true
	return s
}

// Compare orders two versions as semantic versions: -1, 0 or 1. A version
// that does not read sorts before every one that does.
func Compare(a, b string) int {
	x, y := parse(a), parse(b)
	switch {
	case !x.ok && !y.ok:
		return 0
	case !x.ok:
		return -1
	case !y.ok:
		return 1
	}
	for i := range x.nums {
		if x.nums[i] != y.nums[i] {
			if x.nums[i] < y.nums[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case x.pre == y.pre:
		return 0
	case x.pre == "":
		return 1 // a release is after its pre-releases
	case y.pre == "":
		return -1
	}
	return comparePre(x.pre, y.pre)
}

func comparePre(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		an, aerr := strconv.Atoi(as[i])
		bn, berr := strconv.Atoi(bs[i])
		switch {
		case aerr == nil && berr == nil && an != bn:
			if an < bn {
				return -1
			}
			return 1
		case aerr == nil && berr != nil:
			return -1
		case aerr != nil && berr == nil:
			return 1
		case as[i] != bs[i]:
			if as[i] < bs[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(as) < len(bs):
		return -1
	case len(as) > len(bs):
		return 1
	}
	return 0
}

// Development reports whether a version is a build of no release: go run,
// or a source build of main, which have nothing to update to.
func Development(v string) bool {
	s := parse(v)
	return !s.ok || strings.HasSuffix(s.pre, "dev") || s.nums == [3]int{}
}

// --- Installing ---------------------------------------------------------

// Layout is where an installed Shiori's files are: the prefix of bin/shiori
// and libexec/shiori.
type Layout struct {
	Exe     string // the shiori binary
	CoreDir string // libexec/shiori, the bundled herdr-core
}

// Here is the layout of the running binary.
func Here() (Layout, error) {
	exe, err := os.Executable()
	if err != nil {
		return Layout{}, err
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	if strings.Contains(exe, string(filepath.Separator)+"go-build") {
		return Layout{}, errors.New("this shiori was built by go run: build it, or install it with install.sh")
	}
	prefix := filepath.Dir(filepath.Dir(exe))
	return Layout{Exe: exe, CoreDir: filepath.Join(prefix, "libexec", "shiori")}, nil
}

// Install downloads r's archive for this OS and CPU, checks it, and puts
// its files in place of l's: the binary, and herdr-core when l has a
// bundled one. progress, if set, hears of each step.
func Install(ctx context.Context, o Options, r Release, l Layout, progress func(string)) error {
	say := func(f string, a ...any) {
		if progress != nil {
			progress(fmt.Sprintf(f, a...))
		}
	}
	name := ArchiveName()
	archive, ok := r.Asset(name)
	if !ok {
		return fmt.Errorf("release %s has no %s: install it with install.sh, which can build from source", r.Tag, name)
	}
	dir := filepath.Dir(l.Exe)
	if err := writable(dir); err != nil {
		return fmt.Errorf("cannot write to %s: %w", dir, err)
	}
	// Unpacked next to the binary, so that moving into place is a rename.
	tmp, err := os.MkdirTemp(dir, ".shiori-update-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	say("downloading %s (%s)", name, size(archive.Size))
	f, err := os.Create(filepath.Join(tmp, name))
	if err != nil {
		return err
	}
	h := sha256.New()
	err = o.fetch(ctx, archive, io.MultiWriter(f, h), 1<<30)
	f.Close()
	if err != nil {
		return err
	}
	got := hex.EncodeToString(h.Sum(nil))
	if sums, ok := r.Asset("checksums.txt"); ok {
		var b strings.Builder
		if err := o.fetch(ctx, sums, &b, 1<<20); err != nil {
			return err
		}
		want, found := checksumOf(b.String(), name)
		if !found {
			return fmt.Errorf("checksums.txt has no line for %s", name)
		}
		if want != got {
			return fmt.Errorf("%s does not match its checksum: the download is damaged or not the release's", name)
		}
		say("checksum ok")
	} else {
		say("the release has no checksums.txt: the archive is not checked")
	}

	x := filepath.Join(tmp, "x")
	if err := unpack(filepath.Join(tmp, name), x); err != nil {
		return fmt.Errorf("unpacking %s: %w", name, err)
	}
	bin := filepath.Join(x, "bin", "shiori")
	if fi, err := os.Stat(bin); err != nil || !fi.Mode().IsRegular() {
		return fmt.Errorf("%s has no bin/shiori", name)
	}
	os.Chmod(bin, 0o755)
	// The new binary must run here before it takes the old one's place.
	out, err := exec.CommandContext(ctx, bin, "--version").Output()
	if err != nil {
		return fmt.Errorf("the new shiori does not run here: %w", err)
	}
	say("unpacked %s", strings.TrimSpace(string(out)))

	if err := put(bin, l.Exe); err != nil {
		return err
	}
	say("installed %s", l.Exe)
	core := filepath.Join(x, "libexec", "shiori")
	if fi, err := os.Stat(l.CoreDir); err == nil && fi.IsDir() {
		entries, _ := os.ReadDir(core)
		for _, e := range entries {
			if err := put(filepath.Join(core, e.Name()), filepath.Join(l.CoreDir, e.Name())); err != nil {
				return err
			}
		}
		if len(entries) > 0 {
			say("installed herdr-core in %s", l.CoreDir)
		}
	}
	return nil
}

func size(n int64) string {
	if n <= 0 {
		return "size unknown"
	}
	return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
}

func writable(dir string) error {
	f, err := os.CreateTemp(dir, ".shiori-write-")
	if err != nil {
		return err
	}
	f.Close()
	return os.Remove(f.Name())
}

// checksumOf finds a file's sum in a sha256sum listing.
func checksumOf(listing, name string) (string, bool) {
	sc := bufio.NewScanner(strings.NewReader(listing))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == name {
			return strings.ToLower(fields[0]), true
		}
	}
	return "", false
}

// unpack extracts a .tar.gz into dir, refusing paths that leave it.
func unpack(archive, dir string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		clean := filepath.Clean(filepath.FromSlash(h.Name))
		if clean == "." {
			continue
		}
		if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return fmt.Errorf("%q is outside the archive", h.Name)
		}
		target := filepath.Join(dir, clean)
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(h.Mode)&0o755|0o600)
			if err != nil {
				return err
			}
			_, err = io.Copy(out, io.LimitReader(tr, 1<<30))
			out.Close()
			if err != nil {
				return err
			}
		}
		// Links and the rest are no part of a release.
	}
}

// put writes src next to dst and renames it over dst, so that a running
// copy of dst is not disturbed.
func put(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".new"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	os.Chmod(tmp, 0o755)
	if runtime.GOOS == "darwin" {
		exec.Command("xattr", "-d", "com.apple.quarantine", tmp).Run()
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
