package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/duskoide/shiori/internal/update"
)

// Version is the running Shiori's version; package main sets it.
var Version = "0.1.0-dev"

// The window looks for a newer Shiori once a day, unless the person turned
// that off (settings.json "update_check", or SHIORI_NO_UPDATE_CHECK=1): one
// request to GitHub for the latest release, which says nothing about the
// person or the machine. A newer release is told of once, and stays in the
// status bar and on the About page; installing it is "shiori update".

const (
	updateEvery = 24 * time.Hour
	updateFirst = 10 * time.Second // after the window opens
	updateWait  = 20 * time.Second // for GitHub to answer
)

// updateState is update.json: when the window last looked, and what it
// found. It is the app's own record, no setting.
type updateState struct {
	CheckedAt time.Time `json:"checked_at"`
	Latest    string    `json:"latest,omitempty"`
	URL       string    `json:"url,omitempty"`
	Notified  string    `json:"notified,omitempty"` // the version told of
	Err       string    `json:"error,omitempty"`
}

func updatePath(home string) string { return filepath.Join(home, "update.json") }

func loadUpdateState(home string) updateState {
	var s updateState
	if b, err := os.ReadFile(updatePath(home)); err == nil {
		json.Unmarshal(b, &s)
	}
	return s
}

func (s updateState) save(home string) {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return
	}
	os.MkdirAll(home, 0o755)
	tmp := updatePath(home) + ".new"
	if os.WriteFile(tmp, append(b, '\n'), 0o644) == nil {
		os.Rename(tmp, updatePath(home))
	}
}

// NoteUpdateCheck records a release found by "shiori update", so that the
// window does not look again at once, nor tell of one already installed.
func NoteUpdateCheck(home string, r update.Release) {
	s := loadUpdateState(home)
	s.CheckedAt, s.Latest, s.URL, s.Err = time.Now(), r.Version(), r.URL, ""
	s.save(home)
}

// updateChecksOn reports whether the window looks for new versions, and
// why not when it does not.
func (a *App) updateChecksOn() (bool, string) {
	if os.Getenv("SHIORI_NO_UPDATE_CHECK") != "" {
		return false, "SHIORI_NO_UPDATE_CHECK is set"
	}
	if a.cfg.UpdateCheck != nil && !*a.cfg.UpdateCheck {
		return false, "turned off"
	}
	return true, ""
}

// newerRelease is the newer release found, if there is one.
func (a *App) newerRelease() (string, bool) {
	v := a.upd.Latest
	if v == "" || update.Development(Version) || update.Compare(v, Version) <= 0 {
		return "", false
	}
	return v, true
}

// scheduleUpdateCheck arranges the next look: a day after the last one,
// and not before the window has been open a little (main thread).
func (a *App) scheduleUpdateCheck() {
	if a.updTimer != nil {
		a.updTimer.Stop()
		a.updTimer = nil
	}
	if on, _ := a.updateChecksOn(); !on || update.Development(Version) {
		return // a build of no release has nothing to update to
	}
	wait := max(time.Until(a.upd.CheckedAt.Add(updateEvery)), updateFirst)
	a.updTimer = time.AfterFunc(wait, func() {
		a.post(func() {
			if on, _ := a.updateChecksOn(); on {
				a.checkUpdates(false)
			}
		})
	})
}

// checkUpdates looks for a newer release now, and answers with what it
// found; manual says the person asked, so it always says what came of it.
func (a *App) checkUpdates(manual bool) *deferred {
	d := a.newDeferred(updateWait+5*time.Second, nil)
	a.updWaiters = append(a.updWaiters, d)
	if a.updChecking {
		return d
	}
	a.updChecking = true
	opts := a.updOpts
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), updateWait)
		defer cancel()
		r, err := update.Latest(ctx, opts, "")
		a.post(func() { a.updateFound(r, err, manual) })
	}()
	return d
}

// updateFound takes what a look found.
func (a *App) updateFound(r update.Release, err error, manual bool) {
	a.updChecking = false
	a.upd.CheckedAt = time.Now()
	waiters := a.updWaiters
	a.updWaiters = nil
	if err != nil {
		msg := err.Error()
		if errors.Is(err, update.ErrNoRelease) {
			msg = "no release found"
		}
		a.upd.Err = msg
		a.upd.save(a.home)
		if manual {
			a.notifyLevel("Cannot look for a new version: "+msg, levelError)
		}
		for _, d := range waiters {
			d.resolve(nil, errors.New(msg))
		}
		a.scheduleUpdateCheck()
		return
	}
	a.upd.Latest, a.upd.URL, a.upd.Err = r.Version(), r.URL, ""
	newer, isNewer := a.newerRelease()
	switch {
	case isNewer && (manual || a.upd.Notified != newer):
		a.notify("Shiori " + newer + " is out: update it from Settings → About, or run shiori update")
		a.upd.Notified = newer
	case manual && !isNewer:
		a.notify("Shiori " + Version + " is up to date")
	}
	a.upd.save(a.home)
	result := map[string]any{"current": Version, "latest": r.Version(), "newer": isNewer, "url": r.URL}
	for _, d := range waiters {
		d.resolve(result, nil)
	}
	a.scheduleUpdateCheck()
}

func (a *App) setUpdateChecks(on bool) {
	a.cfg.UpdateCheck = &on
	if on {
		a.cfg.UpdateCheck = nil // the default
	}
	a.saveSettings()
	a.scheduleUpdateCheck()
}

func (a *App) cmdCheckUpdates(args) (any, error) { return a.checkUpdates(true), nil }

// cmdUpdate runs "shiori update" in a new tab, where its progress shows.
func (a *App) cmdUpdate(args) (any, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	if _, err := update.Here(); err != nil {
		return nil, err
	}
	return a.do("tab.new", args{"argv": []any{exe, "update"}, "name": "Update Shiori", "folder": homeDir()})
}
