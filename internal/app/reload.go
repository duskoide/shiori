package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"time"
)

// configPoll is how often the watcher looks at settings.json and keys.json.
const configPoll = 500 * time.Millisecond

// watchConfig reloads settings.json and keys.json when either changes on
// disk, until ctx ends. It polls the files' times and sizes, which needs
// nothing from the system and costs two stat calls a poll.
func (a *App) watchConfig(ctx context.Context) {
	type stamp struct {
		mod  time.Time
		size int64
		ok   bool
	}
	look := func() (out [2]stamp) {
		for i, name := range [...]string{"settings.json", "keys.json"} {
			if fi, err := os.Stat(filepath.Join(a.home, name)); err == nil {
				out[i] = stamp{fi.ModTime(), fi.Size(), true}
			}
		}
		return out
	}
	last := look()
	t := time.NewTicker(configPoll)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if now := look(); now != last {
			last = now
			a.post(func() { a.reloadConfig(false) })
		}
	}
}

// reloadConfig reads settings.json and keys.json again and applies what
// changed: the theme, the font and size, the sidebar and the key bindings.
// The window's own writes of settings.json come back as no change, so they
// say nothing. manual says the person asked, so it always answers.
func (a *App) reloadConfig(manual bool) {
	if a.saveTimer != nil {
		// A write of ours is due; what is in memory is newer than the file.
		time.AfterFunc(2*settingsDelay, func() { a.post(func() { a.reloadConfig(manual) }) })
		return
	}
	oldCfg, oldKeys := a.cfg, a.keysRaw
	a.loadSettings()
	a.loadKeys()
	settings := !reflect.DeepEqual(oldCfg, a.cfg)
	if settings {
		a.applyTheme()
		for _, pt := range a.terms {
			pt.term.SetFont(a.termFont())
		}
	}
	if settings || !bytes.Equal(oldKeys, a.keysRaw) || manual {
		a.notify("Settings reloaded")
	}
}
