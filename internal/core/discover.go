package core

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// sessionName resolves the session: option, then HC_SESSION, then "default".
func sessionName(o Options) string {
	if o.Session != "" {
		return o.Session
	}
	if s := os.Getenv("HC_SESSION"); s != "" {
		return s
	}
	return "default"
}

// SocketCandidates lists the socket paths a core may listen on, best first.
// An explicit Options.Socket is the only candidate.
func SocketCandidates(o Options) []string {
	if o.Socket != "" {
		return []string{o.Socket}
	}
	if s := os.Getenv("HC_SOCKET"); s != "" {
		return []string{s}
	}
	session := sessionName(o)
	var out []string
	add := func(p string) {
		for _, q := range out {
			if q == p {
				return
			}
		}
		out = append(out, p)
	}
	if run := os.Getenv("XDG_RUNTIME_DIR"); run != "" {
		add(filepath.Join(run, "herdr-core", session+".sock"))
	}
	runDir := filepath.Join("/run/user", strconv.Itoa(os.Getuid()), "herdr-core")
	if st, err := os.Stat(runDir); err == nil && st.IsDir() {
		add(filepath.Join(runDir, session+".sock"))
	}
	if state := stateDir(session); state != "" {
		// The lock file records the socket path of the running core.
		if b, err := os.ReadFile(filepath.Join(state, "core.lock")); err == nil {
			if p := strings.TrimSpace(string(b)); filepath.IsAbs(p) {
				add(p)
			}
		}
		add(filepath.Join(state, session+".sock"))
	}
	return out
}

func stateDir(session string) string {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, "herdr-core", session)
}

// dialCore dials the first reachable candidate socket.
func dialCore(ctx context.Context, o Options) (net.Conn, error) {
	var d net.Dialer
	var firstErr error
	for _, p := range SocketCandidates(o) {
		c, err := d.DialContext(ctx, "unix", p)
		if err == nil {
			return c, nil
		}
		if firstErr == nil {
			firstErr = err
		}
		if ctx.Err() != nil {
			break
		}
	}
	if firstErr == nil {
		firstErr = fmt.Errorf("no socket path candidates")
	}
	return nil, firstErr
}

var binaryNames = []string{"herdr-core", "hc"}

// extraBinaryDirs are searched after PATH.
func extraBinaryDirs() []string {
	home, _ := os.UserHomeDir()
	dirs := []string{}
	if home != "" {
		dirs = append(dirs, filepath.Join(home, ".local", "bin"), filepath.Join(home, ".nix-profile", "bin"))
	}
	dirs = append(dirs, "/usr/local/bin")
	if home != "" {
		dirs = append(dirs, filepath.Join(home, ".cargo", "bin"))
	}
	return dirs
}

// BundledDir is where a release keeps its own herdr-core and hc, relative
// to the directory of the shiori executable: <prefix>/bin/shiori next to
// <prefix>/libexec/shiori/{herdr-core,hc}.
const BundledDir = "../libexec/shiori"

// bundledBinaryDirs is the release's own core directory, if the running
// executable can be located.
func bundledBinaryDirs() []string {
	exe, err := os.Executable()
	if err != nil {
		return nil
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	return []string{filepath.Join(filepath.Dir(exe), filepath.FromSlash(BundledDir))}
}

// FindBinary locates a herdr-core/hc binary: explicit, then the core bundled
// with the release, then PATH, then the usual install directories. A candidate is accepted only if "--version"
// prints something starting with "hc ".
func FindBinary(explicit string) (string, error) {
	if explicit != "" {
		if strings.ContainsRune(explicit, os.PathSeparator) {
			return explicit, nil
		}
		return exec.LookPath(explicit)
	}
	dirs := bundledBinaryDirs()
	dirs = append(dirs, filepath.SplitList(os.Getenv("PATH"))...)
	dirs = append(dirs, extraBinaryDirs()...)
	if p := searchBinary(dirs, binaryNames, isHC); p != "" {
		return p, nil
	}
	return "", fmt.Errorf("herdr-core binary not found (looked for %s next to shiori, in PATH and common install dirs)", strings.Join(binaryNames, ", "))
}

func searchBinary(dirs, names []string, ok func(string) bool) string {
	seen := map[string]bool{}
	for _, d := range dirs {
		if d == "" || seen[d] {
			continue
		}
		seen[d] = true
		for _, n := range names {
			p := filepath.Join(d, n)
			if st, err := os.Stat(p); err != nil || st.IsDir() || st.Mode()&0o111 == 0 {
				continue
			}
			if ok(p) {
				return p
			}
		}
	}
	return ""
}

// isHC runs "<path> --version" and checks for the "hc " prefix.
func isHC(path string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "--version").Output()
	return err == nil && strings.HasPrefix(string(out), "hc ")
}
