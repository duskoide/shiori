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
- A changes panel (`Ctrl+Shift+G`): the files being changed in the
  space's folder, most recent first (in git, what differs from the last
  commit); a click shows the diff
- Session recovery, as herdr's: when herdr-core stops without its
  terminals (a crash, a kill, a reboot), the panes stay where they were
  and get fresh shells in the folders they last worked in, instead of
  closing (see [Recovery](#recovery))
- Command palette, rename and confirm dialogs, theme presets
- A settings panel (`Ctrl+,`) for everything in `settings.json` and
  `keys.json`: theme, terminal font and size, sidebar, every key
  binding, recorded by pressing the keys, mods and machines
- Other machines: the spaces and terminals of machines herdr-core reaches
  over SSH, beside this one's, each under its name in the sidebar; added
  and managed in Settings → Machines (see "Work on another machine")
- Mods: commands, keys and event handlers added by programs in any
  language, over the control socket (see [docs/MODS.md](docs/MODS.md) and
  `examples/mods`)
- herdr-app's default key bindings (`keys.json` overrides them)
- A control CLI compatible with herdr-app's
  (`shiori do tab.new --argv '["claude"]' --task "..."`)

## Requirements

- Go 1.27 or later (`GOTOOLCHAIN=auto` fetches it)
- herdr-core (`hc`): bundled with the releases; for a source build, on `PATH`.
  The app starts the core if it is not running
- Linux: GTK 3 and OpenGL. macOS 13 or later.

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/duskoide/shiori/main/install.sh | sh
```

The script downloads the release for your OS and CPU (Linux and macOS, amd64
and arm64), checks its SHA-256, and installs `shiori` to `~/.local/bin`. Each
release bundles herdr-core, which goes to `~/.local/libexec/shiori` and is
the core Shiori starts (before any `hc` on `PATH`); `--no-core` skips it. On
Linux it also adds a desktop entry and icon. If there is no release for your
platform, it builds from source with Go and fetches herdr-core with its own
installer. Options go after `sh -s --`:

```sh
curl -fsSL https://raw.githubusercontent.com/duskoide/shiori/main/install.sh | sh -s -- --version v0.1.0 --dir /usr/local/bin
```

`--from-source`, `--no-core`, `--no-desktop`, `--uninstall` and `--help` are also accepted.
While the repository is private, pass a token that can read it:

```sh
curl -fsSL -H "Authorization: Bearer $GITHUB_TOKEN" https://raw.githubusercontent.com/duskoide/shiori/main/install.sh | GITHUB_TOKEN=$GITHUB_TOKEN sh
```

Releases are built by `.github/workflows/release.yml` when a `v*` tag is
pushed (`git tag v0.1.0 && git push origin v0.1.0`): one job per platform
builds `shiori` and bundles the latest herdr-core from
`https://releases.herdr.dev/core/install.sh`.

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

## Work on another machine

Shiori shows another machine's spaces and runs its terminals there, as
herdr-app does: herdr-core on this machine reaches the other over SSH,
and the other needs herdr-core (`hc`) too; Shiori there is optional. The
window still runs here.

1. Open Settings → Machines.
2. Type how SSH reaches it (`user@host`), or pick a host of your
   `~/.ssh/config`, and Add. Your SSH keys are used. When SSH needs an
   answer (a password, a host key) or herdr-core is missing there, the
   adding goes on in a tab where you answer; the machine shows once it
   is added.
3. Its spaces show under its name in the sidebar; its `+` makes a space
   there, starting in its home. Tabs and splits of its spaces run there,
   agents in that machine's login shell.

Each machine keeps its own layout, so herdr-app and Shiori on any machine
show the same spaces. A machine that disconnects keeps its spaces shown,
greyed; edits of them are saved once it is back. Removing a machine
leaves its terminals running there. Machines added with `hc machine add
--temp` (agents' sandboxes) stay out of the window. The changes panel of
another machine's space asks git there.

From the CLI: `shiori do machine.list`, `machine.add --target me@box`,
`machine.rename`, `machine.remove`, `machine.update`, `machine.login`,
and `space.new --machine box`.

## Updating

```sh
shiori update --check           # is a newer release out?
shiori update                   # install it in place of this one
shiori update --version v0.2.0  # or a release of your choice
```

`shiori update` downloads the release for this OS and CPU, checks it
against the release's `checksums.txt`, and replaces the binary and the
herdr-core bundled beside it (where `install.sh` put them). Close Shiori
and open it again afterwards. While the repository is private, set
`GITHUB_TOKEN` to a token that can read it.

The window also looks for a new release once a day and, when there is
one, says so once and shows "Update to …" in the status bar. That look
is one request to GitHub's API for the latest release; it sends nothing
about you or the machine. Turn it off in Settings → About, with
`"update_check": false` in `settings.json`, or with
`SHIORI_NO_UPDATE_CHECK=1`. Development builds (`0.1.0-dev`) do not look.

## Recovery

Terminals run in herdr-core and outlive the window, and survive a core
update or restart in its holder processes. When the core stops without
them (it crashes or is killed with its holders, `hc server stop`, the
computer restarts), their programs end. The layout survives in the core's
document, but its panes would point at terminals the new core never had,
and close.

So the window keeps `session.json` in its home (`session-<name>.json`
for another `HC_SESSION`): the layout, and for each pane its terminal,
the folder that worked in and the core process that ran it. It is
written a second after things change and when the window closes, to a
file beside it that then replaces it, so a crash cannot leave half of
it.

When the window connects to a core process other than the one the
snapshot saw, each pane whose terminal that core does not have (or has
given to another terminal) gets a fresh shell in the folder it last
worked in, in the same place in the layout, after the core has run for
three seconds (terminals that outlived the old core come back to the
new one first). A folder that is gone falls back to the space's, then
home. Programs and agents do not come back; start them again. If the new
core has no layout at all (its documents were lost too), the window puts
back the one it kept. Panes whose programs ended close as before. Only
this machine's panes are brought back: other machines' terminals run in
their own cores.

Turn it off in Settings → Layout, or with `"restore": false` in
`settings.json`: lost panes then close.

## Files

`$SHIORI_HOME` (default `~/.config/shiori`) holds
`settings.json`, `keys.json`, the `mods` folder, the control socket,
`update.json` (when the window last looked for a new version) and
`session.json` (what [recovery](#recovery) brings back). Files of the
app's earlier name, `~/.config/herdr-mygo`, move there on first start. The window reads
`settings.json` and `keys.json` again when they change (within about
half a second), or on `shiori do app.reload`; a file that does not parse
keeps what was in use; it reads `mods` again as it changes too. `HC_SESSION`
selects the herdr-core session.

| File | Key | What | Settings panel |
| --- | --- | --- | --- |
| `settings.json` | `theme` | `graphite`, `ink`, `moss`, `paper` or `atom-one-dark` | Appearance |
| | `font` | the terminals' font family, or a comma-separated list | Terminal |
| | `font_size` | the terminals' text size, 6 to 40 (default 13) | Terminal |
| | `sidebar` | `false` hides the sidebar | Layout |
| | `changes` | `true` shows the changes panel | Layout |
| | `update_check` | `false` stops the daily look for a new version | About |
| | `restore` | `false` closes the panes whose terminals herdr-core lost when it stopped, instead of starting fresh shells in them | Layout |
| | `mods` | the mods turned on, each with the manifest it was turned on for | Mods |
| `keys.json` | `{"section": {"chord": "action"}}` | changes to the default key bindings (and the mods'): a chord bound anew, or `"none"` to remove one; `global` and `terminal` here, other sections kept for herdr-app | Keyboard |
| `mods/<id>/mod.json` | | a mod: see [docs/MODS.md](docs/MODS.md) | Mods |

Machines are herdr-core's (`hc machine ls`), not Shiori's: no file here
holds them, and Settings → Machines is where they are managed.

The panel writes the files as you change things, and
`shiori do app.settings --section keyboard` opens it at a section
(`appearance`, `terminal`, `layout`, `keyboard`, `mods`, `files`).

## Layout of the code

| Path | What |
| --- | --- |
| `cmd/shiori` | the command: opens the window, or talks to the open one (`ls`, `status`, `do`) |
| `internal/app` | the window: state, sync with the core, views, commands, control socket |
| `internal/core` | herdr-core client: framing, RPC, events, terminal streams |
| `internal/layout` | the shared layout document: model, edits, geometry |
| `internal/agent` | agent status words and attention |
| `internal/recovery` | the session snapshot: what brings panes back after herdr-core stopped |
| `internal/theme`, `internal/programs`, `internal/keys` | themes, program table and icons, key bindings |
| `docs/PROTOCOL.md` | notes on the core's wire protocol |
| `docs/CONTRACTS.md` | the packages' public APIs |
| `internal/mods` | mods' manifests |
| `internal/changes` | the files being changed in a folder, for the changes panel |
| `internal/update` | new versions: the latest release, and installing one (`shiori update`) |
| `docs/MODS.md` | mods: the manifest, the protocol, events |
| `docs/REMOTE.md` | other machines' spaces and terminals: how they work, and how they were built |
| `examples/mods` | two mods: a one-shot command, and a service in Python |
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
