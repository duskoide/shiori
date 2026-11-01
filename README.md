<p align="center"><img src="assets/logo.svg" width="128" alt="栞"></p>

# Shiori 栞

*Shiori* (栞) is Japanese for a bookmark. It is a desktop app for terminals and coding agents, written in Go with the
[MyGo](https://github.com/egoist/mygo) native UI. It is a re-implementation
of [Herdr](https://github.com/herdrdev/herdr-app) and uses the same backend,
**herdr-core**: terminals run in the core and survive the window, and the
layout lives in the core's `herdr-app` document, so Shiori and
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
  (`shiori do tab.new --argv '["claude"]' --task "..."`)

## Requirements

- Go 1.27 or later (`GOTOOLCHAIN=auto` fetches it)
- herdr-core (`hc`) on `PATH`; the app starts the core if it is not running
- Linux: GTK 3 and OpenGL. macOS 13 or later.

## Build and run

```sh
make build      # ./shiori
make run
make check      # vet, tests, gofmt (what CI runs)
make package    # platform package via mygo build
```

## Control CLI

With the window open:

```sh
shiori ls                       # spaces, tabs and panes
shiori commands                 # every command and its arguments
shiori do pane.split --direction right
shiori do space.new --name api --folder ~/src/api
```

Commands herdr-app has but this app does not implement answer
"<name> is not supported by this app".

## Files

`$SHIORI_HOME` (default `~/.config/shiori`) holds
`settings.json`, `keys.json` and the control socket. Files of the
app's earlier name, `~/.config/herdr-mygo`, move there on first start. The window reads
`settings.json` and `keys.json` again when they change (within about
half a second), or on `shiori do app.reload`; a file that does not parse
keeps what was in use. `HC_SESSION`
selects the herdr-core session.

## Layout of the code

| Path | What |
| --- | --- |
| `cmd/shiori` | the command: opens the window, or talks to the open one (`ls`, `status`, `do`) |
| `internal/app` | the window: state, sync with the core, views, commands, control socket |
| `internal/core` | herdr-core client: framing, RPC, events, terminal streams |
| `internal/layout` | the shared layout document: model, edits, geometry |
| `internal/agent` | agent status words and attention |
| `internal/theme`, `internal/programs`, `internal/keys` | themes, program table and icons, key bindings |
| `docs/PROTOCOL.md` | notes on the core's wire protocol |
| `docs/CONTRACTS.md` | the packages' public APIs |
| `assets`, `tools/logo` | the logo and icon, and the script drawing them (`make logo`) |
| `bench` | the performance comparison with herdr-app |

## Tests

`go test ./...` runs the unit tests. The integration test drives the
real window headlessly against an isolated core session
(`HC_SESSION=shiori-it`):

```sh
CGO_ENABLED=0 go test -c -o /tmp/shiori-app.test ./internal/app
SHIORI_IT=1 SHIORI_SHOTS=/tmp/shots /tmp/shiori-app.test -test.run TestIntegration -test.v
```

## License

Apache License 2.0. See [LICENSE](LICENSE) and [NOTICE](NOTICE).
