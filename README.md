# herdr-mygo

A desktop app for terminals and coding agents, written in Go with the
[MyGo](https://github.com/egoist/mygo) native UI. It is a re-implementation
of [Herdr](https://github.com/herdrdev/herdr-app) and uses the same backend,
**herdr-core**: terminals run in the core and survive the window, and the
layout lives in the core's `herdr-app` document, so herdr-mygo and
herdr-app can show the same spaces side by side.

## Features

- Spaces, tabs and split panes, with drag-to-resize dividers, zoom and
  directional focus
- Terminals rendered with libghostty-vt through MyGo's terminal plugin
- Agent status (OSC 7501: working, blocked, done) in the sidebar, tabs
  and status bar, with herdr.seen read tracking
- Command palette, rename and confirm dialogs, settings, theme presets
- herdr-app's default key bindings (`keys.json` overrides them)
- A control CLI compatible with herdr-app's
  (`herdr-mygo do tab.new --argv '["claude"]' --task "..."`)

## Requirements

- Go 1.27 or later (`GOTOOLCHAIN=auto` fetches it)
- herdr-core (`hc`) on `PATH`; the app starts the core if it is not running
- Linux: GTK 3 and OpenGL. macOS 13 or later.

## Build and run

```sh
make build      # ./herdr-mygo
make run
make check      # vet, tests, gofmt (what CI runs)
make package    # platform package via mygo build
```

## Control CLI

With the window open:

```sh
herdr-mygo ls                       # spaces, tabs and panes
herdr-mygo commands                 # every command and its arguments
herdr-mygo do pane.split --direction right
herdr-mygo do space.new --name api --folder ~/src/api
```

Commands herdr-app has but this app does not implement answer
"<name> is not supported by this app".

## Files

`$HERDR_MYGO_HOME` (default `~/.config/herdr-mygo`) holds
`settings.json`, `keys.json` and the control socket. `HC_SESSION`
selects the herdr-core session.

## Layout of the code

| Path | What |
| --- | --- |
| `*.go` (package main) | the window: state, sync with the core, views, commands, control socket |
| `internal/core` | herdr-core client: framing, RPC, events, terminal streams |
| `internal/layout` | the shared layout document: model, edits, geometry |
| `internal/agent` | agent status words and attention |
| `internal/theme`, `internal/programs`, `internal/keys` | themes, program table and icons, key bindings |
| `docs/PROTOCOL.md` | notes on the core's wire protocol |
| `docs/CONTRACTS.md` | the packages' public APIs |

## Tests

`go test ./...` runs the unit tests. The integration test drives the
real window headlessly against an isolated core session
(`HC_SESSION=herdr-mygo-it`):

```sh
CGO_ENABLED=0 go test -c -o /tmp/hm.test .
HERDR_MYGO_IT=1 HERDR_MYGO_SHOTS=/tmp/shots /tmp/hm.test -test.run TestIntegration -test.v
```

## License

Apache License 2.0. See [LICENSE](LICENSE) and [NOTICE](NOTICE).
