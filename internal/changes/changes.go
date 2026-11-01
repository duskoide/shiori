// Package changes tells which files are being changed in a folder. In a
// git work tree it asks git (git status: the files that differ from the
// last commit, and the new ones, minus what .gitignore leaves out);
// elsewhere it walks the folder for files modified lately. Either way the
// files come most recently changed first. It needs no cgo and no file
// system notifications: a Watcher looks again every few seconds.
package changes

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Kind is how a file changed.
type Kind int

const (
	Modified Kind = iota
	Added         // new to git, staged
	Deleted
	Renamed
	Untracked // new, not in git
	Changed   // modified lately, outside git
)

// Letter is the kind's mark, as git status shows it.
func (k Kind) Letter() string {
	return [...]string{"M", "A", "D", "R", "U", "M"}[k]
}

func (k Kind) String() string {
	return [...]string{"modified", "added", "deleted", "renamed", "untracked", "changed"}[k]
}

// File is a changed file.
type File struct {
	Path    string    // relative to the folder, with /
	OldPath string    // a renamed file's path before, relative to the folder
	Kind    Kind      //
	Time    time.Time // its last change: its modification time, or when it was seen deleted
}

// Snapshot is what a scan found.
type Snapshot struct {
	Folder    string
	Git       bool   // the folder is in a git work tree
	Root      string // the work tree's root, for git
	Files     []File // most recent first
	Truncated bool   // the folder had more files than a walk looks at
	Err       error
	// Took is what the scan cost: its time, or the CPU time git took
	// when that was more (git works on several cores at once).
	Took time.Duration
}

// Since is how long ago a file last changed, as the list shows it.
func Since(now, t time.Time) string {
	d := now.Sub(t)
	switch {
	case d < 10*time.Second:
		return "now"
	case d < time.Minute:
		return "<1m"
	case d < time.Hour:
		return itoa(int(d.Minutes())) + "m"
	case d < 24*time.Hour:
		return itoa(int(d.Hours())) + "h"
	}
	return itoa(int(d.Hours()/24)) + "d"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}

// Options tune a scan.
type Options struct {
	// Recent is how far back a walk outside git looks; 0 is a day.
	Recent time.Duration
	// MaxFiles is how many files a walk looks at; 0 is 20000.
	MaxFiles int
	// NoGit walks even in a git work tree.
	NoGit bool
}

// skipDirs are folders a walk does not go into: tools' and builds'.
var skipDirs = []string{"node_modules", "vendor", "target", "build", "dist", "out", "__pycache__", "venv", ".venv"}

// Scan looks at a folder once. prev, from the scan before (or nil), keeps
// when deleted files were first missed.
func Scan(ctx context.Context, folder string, prev *Snapshot, opt Options) Snapshot {
	start := time.Now()
	s := Snapshot{Folder: folder}
	if fi, err := os.Stat(folder); err != nil || !fi.IsDir() {
		if err == nil {
			err = errors.New("not a folder")
		}
		s.Err = err
		return s
	}
	if !opt.NoGit {
		if root, ok := gitRoot(ctx, folder); ok {
			s.Git, s.Root = true, root
			var cpu time.Duration
			s.Files, cpu, s.Err = gitScan(ctx, folder, root, prev)
			defer func() { s.Took = max(s.Took, cpu) }()
		}
	}
	if !s.Git {
		s.Files, s.Truncated, s.Err = walkScan(ctx, folder, prev, opt)
	}
	sort.SliceStable(s.Files, func(i, j int) bool {
		if !s.Files[i].Time.Equal(s.Files[j].Time) {
			return s.Files[i].Time.After(s.Files[j].Time)
		}
		return s.Files[i].Path < s.Files[j].Path
	})
	s.Took = time.Since(start)
	return s
}

func gitRoot(ctx context.Context, folder string) (string, bool) {
	if _, err := exec.LookPath("git"); err != nil {
		return "", false
	}
	out, err := gitCmd(ctx, folder, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", false
	}
	root := strings.TrimSpace(string(out))
	return root, root != ""
}

func gitCmd(ctx context.Context, dir string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir, "--no-optional-locks"}, args...)...)
	// git's own pager, prompts and locale stay out of it.
	cmd.Env = append(os.Environ(), "GIT_PAGER=cat", "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
	return cmd
}

// gitScan lists what git status says changed under folder.
func gitScan(ctx context.Context, folder, root string, prev *Snapshot) ([]File, time.Duration, error) {
	cmd := gitCmd(ctx, folder, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	out, err := cmd.Output()
	var cpu time.Duration
	if ps := cmd.ProcessState; ps != nil {
		cpu = ps.UserTime() + ps.SystemTime()
	}
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return nil, cpu, errors.New("git status: " + strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, cpu, err
	}
	// Paths come relative to the root; the list is of the folder's.
	realFolder, err := filepath.EvalSymlinks(folder)
	if err != nil {
		realFolder = folder
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		realRoot = root
	}
	prefix, err := filepath.Rel(realRoot, realFolder)
	if err != nil || prefix == "." {
		prefix = ""
	} else {
		prefix = filepath.ToSlash(prefix) + "/"
	}
	under := func(p string) (string, bool) {
		if prefix == "" {
			return p, true
		}
		rel, ok := strings.CutPrefix(p, prefix)
		return rel, ok
	}
	var files []File
	entries := bytes.Split(out, []byte{0})
	for i := 0; i < len(entries); i++ {
		e := entries[i]
		if len(e) < 4 {
			continue
		}
		x, y, path := e[0], e[1], string(e[3:])
		f := File{}
		switch {
		case x == '?' && y == '?':
			f.Kind = Untracked
		case x == 'R' || x == 'C':
			f.Kind = Renamed
			if i+1 < len(entries) {
				i++
				if old, ok := under(string(entries[i])); ok {
					f.OldPath = old
				}
			}
		case x == 'D' || y == 'D':
			f.Kind = Deleted
		case x == 'A':
			f.Kind = Added
		case x == '!':
			continue
		default:
			f.Kind = Modified
		}
		rel, ok := under(path)
		if !ok {
			continue
		}
		f.Path = rel
		if fi, err := os.Lstat(filepath.Join(folder, filepath.FromSlash(rel))); err == nil && f.Kind != Deleted {
			f.Time = fi.ModTime()
		} else {
			f.Time = missedSince(prev, rel)
		}
		files = append(files, f)
	}
	return files, cpu, nil
}

// missedSince is when a file was first seen gone: in the scan before, or
// now.
func missedSince(prev *Snapshot, path string) time.Time {
	if prev != nil {
		for _, f := range prev.Files {
			if f.Path == path && f.Kind == Deleted {
				return f.Time
			}
		}
	}
	return time.Now()
}

// walkScan lists the files under folder modified within opt.Recent, and
// those gone since prev. It looks at opt.MaxFiles files at most.
func walkScan(ctx context.Context, folder string, prev *Snapshot, opt Options) ([]File, bool, error) {
	recent, maxFiles := opt.Recent, opt.MaxFiles
	if recent <= 0 {
		recent = 24 * time.Hour
	}
	if maxFiles <= 0 {
		maxFiles = 20000
	}
	since := time.Now().Add(-recent)
	var files []File
	seen := 0
	present := map[string]bool{}
	truncated := false
	err := filepath.WalkDir(folder, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == folder {
				return err
			}
			return nil // a folder that cannot be read is passed by
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		name := d.Name()
		if d.IsDir() {
			if p != folder && (strings.HasPrefix(name, ".") || slices.Contains(skipDirs, name)) {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		if seen++; seen > maxFiles {
			truncated = true
			return filepath.SkipAll
		}
		rel, _ := filepath.Rel(folder, p)
		rel = filepath.ToSlash(rel)
		present[rel] = true
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if info.ModTime().After(since) {
			files = append(files, File{Path: rel, Kind: Changed, Time: info.ModTime()})
		}
		return nil
	})
	if err != nil && !errors.Is(err, filepath.SkipAll) {
		return nil, false, err
	}
	// What was listed and is gone was deleted; a walk cut short cannot tell.
	if prev != nil && !prev.Git && !truncated {
		for _, f := range prev.Files {
			if !present[f.Path] && time.Since(f.Time) < recent {
				t := f.Time
				if f.Kind != Deleted {
					t = time.Now()
				}
				files = append(files, File{Path: f.Path, Kind: Deleted, Time: t})
			}
		}
	}
	return files, truncated, nil
}

// Same reports whether two scans found the same files.
func Same(a, b Snapshot) bool {
	return a.Folder == b.Folder && a.Git == b.Git && a.Truncated == b.Truncated &&
		errText(a.Err) == errText(b.Err) && slices.EqualFunc(a.Files, b.Files, func(x, y File) bool {
		return x.Path == y.Path && x.OldPath == y.OldPath && x.Kind == y.Kind && x.Time.Equal(y.Time)
	})
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// Watcher scans a folder again and again, and calls back with each scan
// that differs from the one before.
type Watcher struct {
	folder string
	stop   context.CancelFunc
	once   sync.Once
	kick   chan struct{}
	idle   atomic.Bool
}

// Interval is how often a Watcher scans, at least.
var Interval = 2 * time.Second

// costFactor is how many times as long as a scan cost a Watcher waits
// before the next: a slow folder takes some 5% of one core at most.
const costFactor = 30

// idleFactor slows a Watcher down while nobody looks (SetIdle), to
// idleInterval at least.
const (
	idleFactor   = 5
	idleInterval = 10 * time.Second
)

// nextWait is how long a Watcher waits after a scan that took took.
func nextWait(interval, took time.Duration, idle bool) time.Duration {
	wait := max(interval, costFactor*took)
	if idle {
		wait = max(idleFactor*wait, idleInterval)
	}
	return wait
}

// Watch starts watching folder. changed runs on the Watcher's goroutine
// with every scan that differs from the last, the first included.
func Watch(folder string, opt Options, changed func(Snapshot)) *Watcher {
	ctx, cancel := context.WithCancel(context.Background())
	w := &Watcher{folder: folder, stop: cancel, kick: make(chan struct{}, 1)}
	interval := Interval
	go func() {
		var last *Snapshot
		for {
			sctx, scancel := context.WithTimeout(ctx, 30*time.Second)
			s := Scan(sctx, folder, last, opt)
			scancel()
			if ctx.Err() != nil {
				return
			}
			if last == nil || !Same(*last, s) {
				changed(s)
			}
			last = &s
			wait := nextWait(interval, s.Took, w.idle.Load())
			select {
			case <-ctx.Done():
				return
			case <-w.kick:
			case <-time.After(wait):
			}
		}
	}()
	return w
}

// Folder is the folder the Watcher watches.
func (w *Watcher) Folder() string { return w.folder }

// Refresh scans again now.
func (w *Watcher) Refresh() {
	select {
	case w.kick <- struct{}{}:
	default:
	}
}

// SetIdle slows the Watcher down while nobody looks at what it finds, as
// when the window is in the background. Looked at again, it scans at once.
func (w *Watcher) SetIdle(idle bool) {
	if w.idle.Swap(idle) && !idle {
		w.Refresh()
	}
}

// Stop stops watching.
func (w *Watcher) Stop() { w.once.Do(w.stop) }
