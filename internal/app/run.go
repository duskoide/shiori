package app

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"

	"github.com/duskoide/shiori/assets"
)

// Run opens the window, with the app's files in home, on the herdr-core
// session ("" is the default one), and returns when it closes. When a
// window already runs from home, it raises that one instead.
func Run(home, session string) error {
	// A second launch raises the open window.
	if _, err := ask(home, request{Raise: true}); err == nil {
		return nil
	}
	if err := terminal.Load(); err != nil {
		return fmt.Errorf("cannot load the terminal emulator (libghostty-vt): %w", err)
	}
	a := newApp(home, session)
	l, err := a.listen()
	if err != nil {
		return err
	}
	defer l.Close()
	mygo.App.WhenReady(func() {
		a.win = mygo.NewWindow(mygo.WindowOptions{
			Title:           "Shiori",
			Width:           1280,
			Height:          820,
			MinWidth:        640,
			MinHeight:       400,
			StateKey:        "main",
			TitleBarStyle:   mygo.TitleBarHidden,
			BackgroundColor: hexOf(a.k.canvas),
			Content:         ui.View(a.view),
			Page:            mygo.PageOptions{DevTools: devTools()},
		})
		_ = a.win.SetIcon(assets.Icon) // packages carry it too; this covers go run and plain builds
		a.win.OnClosed(func() {
			a.shutdown()
			a.win = nil
		})
		a.start()
	})
	mygo.App.OnWindowAllClosed(func() {
		if runtime.GOOS != "darwin" {
			mygo.App.Quit()
		}
	})
	return mygo.App.Run()
}

// devTools is whether MyGo's element inspector is on: only with
// SHIORI_DEVTOOLS=1. MyGo turns it on in any build not made with mygo
// build, and its keys (Ctrl+Shift+C, Ctrl+Shift+I, F12) are a terminal's.
func devTools() mygo.DevTools {
	if os.Getenv("SHIORI_DEVTOOLS") == "1" {
		return mygo.DevToolsEnabled
	}
	return mygo.DevToolsDisabled
}

// Commands returns every command with its arguments, for JSON: the open
// window's, with its mods' commands, else the app's own.
func Commands(home string) any {
	if b, err := ask(home, request{Commands: true}); err == nil {
		return b
	}
	return commands
}

// Ls asks the window running from home for its spaces, tabs and panes.
func Ls(home string) (json.RawMessage, error) { return ask(home, request{Ls: true}) }

// Status asks the window running from home for its connection, focus and
// overlays.
func Status(home string) (json.RawMessage, error) { return ask(home, request{Status: true}) }

// Do runs a command in the window running from home.
func Do(home, name string, args map[string]any) (json.RawMessage, error) {
	return ask(home, request{Do: name, Args: args})
}
