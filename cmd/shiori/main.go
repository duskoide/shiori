// Command shiori is a desktop app for terminals and coding agents: a
// client of herdr-core, built with MyGo's native UI.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/duskoide/shiori/internal/app"
)

// version is set at build time with -ldflags "-X main.version=…".
var version = "0.1.0-dev"

const usage = `shiori - a desktop app for terminals and coding agents (herdr-core client)

Usage:
  shiori                     open the window
  shiori ls                  spaces, tabs and panes of the open window
  shiori status              connection, focus and overlays of the open window
  shiori commands            every command and its arguments, as JSON
                             (the open window's, with its mods' commands)
  shiori do <cmd> [--k v ...|'{json}']
                             run a command in the open window
  shiori update [--check] [--version <v>]
                             install the latest release (or v) in place of
                             this one; --check only says whether one is out
  shiori --version | --help

Environment:
  SHIORI_HOME   the app's files (default ~/.config/shiori)
  HC_SESSION    the herdr-core session to use (default "default")
  GITHUB_TOKEN  for shiori update while the repository is private
`

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "shiori:", err)
		os.Exit(1)
	}
}

func run(argv []string, out io.Writer) error {
	home := app.DefaultHome()
	app.Version = version
	if len(argv) == 0 {
		return app.Run(home, os.Getenv("HC_SESSION"))
	}
	switch argv[0] {
	case "-h", "--help", "help":
		fmt.Fprint(out, usage)
		return nil
	case "-V", "--version", "version":
		fmt.Fprintln(out, "shiori", version)
		return nil
	case "commands":
		return printJSON(out, app.Commands(home))
	case "ls":
		b, err := app.Ls(home)
		return printReply(out, b, err)
	case "status":
		b, err := app.Status(home)
		return printReply(out, b, err)
	case "do":
		if len(argv) < 2 {
			return fmt.Errorf("do needs a command; see shiori commands")
		}
		v, err := parseArgs(argv[2:])
		if err != nil {
			return err
		}
		b, err := app.Do(home, argv[1], v)
		return printReply(out, b, err)
	case "update":
		return runUpdate(argv[1:], out, home)
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

// printReply prints the open window's answer, indented.
func printReply(out io.Writer, b json.RawMessage, err error) error {
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
