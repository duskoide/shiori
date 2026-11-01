// Command herdr-mygo is a desktop app for terminals and coding agents: a
// client of herdr-core, built with MyGo's native UI.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"runtime"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"
)

// version is set at build time with -ldflags "-X main.version=…".
var version = "0.1.0-dev"

const usage = `herdr-mygo - a desktop app for terminals and coding agents (herdr-core client)

Usage:
  herdr-mygo                     open the window
  herdr-mygo ls                  spaces, tabs and panes of the open window
  herdr-mygo status              connection, focus and overlays of the open window
  herdr-mygo commands            every command and its arguments, as JSON
  herdr-mygo do <cmd> [--k v ...|'{json}']
                                 run a command in the open window
  herdr-mygo --version | --help

Environment:
  HERDR_MYGO_HOME   the app's files (default ~/.config/herdr-mygo)
  HC_SESSION        the herdr-core session to use (default "default")
`

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "herdr-mygo:", err)
		os.Exit(1)
	}
}

func run(argv []string, out io.Writer) error {
	home := defaultHome()
	if len(argv) == 0 {
		return openWindow(home)
	}
	switch argv[0] {
	case "-h", "--help", "help":
		fmt.Fprint(out, usage)
		return nil
	case "-V", "--version", "version":
		fmt.Fprintln(out, "herdr-mygo", version)
		return nil
	case "commands":
		return printJSON(out, commands)
	case "ls":
		return printReply(out, home, request{Ls: true})
	case "status":
		return printReply(out, home, request{Status: true})
	case "do":
		if len(argv) < 2 {
			return fmt.Errorf("do needs a command; see herdr-mygo commands")
		}
		v, err := parseArgs(argv[2:])
		if err != nil {
			return err
		}
		return printReply(out, home, request{Do: argv[1], Args: v})
	}
	return fmt.Errorf("unknown command %q\n\n%s", argv[0], usage)
}

// parseArgs reads a command's arguments: one JSON object, or --name value
// pairs.
func parseArgs(list []string) (map[string]any, error) {
	if len(list) == 1 && strings.HasPrefix(strings.TrimSpace(list[0]), "{") {
		var v map[string]any
		if err := json.Unmarshal([]byte(list[0]), &v); err != nil {
			return nil, fmt.Errorf("arguments: %w", err)
		}
		return v, nil
	}
	v := map[string]any{}
	for i := 0; i < len(list); i++ {
		name, ok := strings.CutPrefix(list[i], "--")
		if !ok || name == "" {
			return nil, fmt.Errorf("expected --name value, got %q", list[i])
		}
		if k, val, ok := strings.Cut(name, "="); ok {
			v[k] = val
			continue
		}
		if i+1 >= len(list) {
			return nil, fmt.Errorf("--%s needs a value", name)
		}
		v[name] = list[i+1]
		i++
	}
	return v, nil
}

func printReply(out io.Writer, home string, req request) error {
	b, err := ask(home, req)
	if err != nil {
		return err
	}
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	return printJSON(out, v)
}

func printJSON(out io.Writer, v any) error {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func openWindow(home string) error {
	// A second launch raises the open window.
	if _, err := ask(home, request{Raise: true}); err == nil {
		return nil
	}
	if err := terminal.Load(); err != nil {
		return fmt.Errorf("cannot load the terminal emulator (libghostty-vt): %w", err)
	}
	a := newApp(home, os.Getenv("HC_SESSION"))
	l, err := a.listen()
	if err != nil {
		return err
	}
	defer l.Close()
	mygo.App.WhenReady(func() {
		a.win = mygo.NewWindow(mygo.WindowOptions{
			Title:           "Herdr",
			Width:           1280,
			Height:          820,
			MinWidth:        640,
			MinHeight:       400,
			StateKey:        "main",
			TitleBarStyle:   mygo.TitleBarHidden,
			BackgroundColor: hexOf(a.k.canvas),
			Content:         ui.View(a.view),
		})
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
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
	return nil
}
