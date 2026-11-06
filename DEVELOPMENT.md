# Developing Shiori

How to build, test and change Shiori, and how work gets into the
repository. The README says what Shiori is and how to use it; this is for
whoever changes it.

## Branches

- **All work happens on `develop`.** Commit to it and push it:
  `git switch develop && git pull --ff-only`, work, commit,
  `git push origin develop`.
- No branch per feature, and no pull requests. The owner says when
  `develop` goes into `main`; nobody else merges into or pushes to `main`.
- `main` is what releases are cut from (a `v*` tag on it runs
  `.github/workflows/release.yml`), and what `install.sh` and
  `shiori update` point users at. Do not commit to it directly.
- Keep `develop` linear: `git pull --rebase` rather than merge commits.
  Never force-push it.

## Commits

- Author and committer: **Rafi Putra Nugraha <rafipeen@gmail.com>**. If
  your git is set up otherwise, pass it per commit:
  `git -c user.name='Rafi Putra Nugraha' -c user.email=rafipeen@gmail.com commit`.
- No `Co-Authored-By`, no session or tool trailers, nothing naming an AI.
- One commit per coherent change, each passing `make check` on its own.
  Unrelated changes go in separate commits, even when made together.

### Commit messages

A subject line, a blank line, then a body when one is needed:

```
Resume agents on their conversation after a crash

herdr-core loses its terminals when it stops, and a pane that ran an
agent came back as a bare shell, the conversation left to find by hand.
The window now keeps each agent's session in the snapshot and starts
the agent again on it.

Agents do not say which conversation they are in, so it is worked out
from their session files every 15 seconds.
```

The subject:

- **Imperative mood**, as if giving an order: `Add`, `Fix`, `Drop`,
  `Keep`, not `Added` or `Adds`. It should complete the sentence "If
  applied, this commit will …".
- **50 characters or fewer** where possible; 72 at most.
- **Capitalized**, with **no trailing period**.
- Says what changes for the user or the code, not how
  (`Fix paste into a terminal in command mode`, not `Change prefix.go`).
- May start with the area it touches, then a colon, when that helps:
  `Command mode: read a key that comes as its text alone`,
  `Settings: add app text size and interface scale`. Use the area's
  plain name, not a type prefix such as `feat:` or `fix:`.

The body:

- Wrapped at **72 columns**.
- Says **why** the change was made and what it does differently; the
  diff already shows how. Name the problem first, then the change.
- Mentions anything a reader of the log would not guess: a behaviour
  that changed, a setting added, a follow-up left to do.
- Can be left out when the subject says it all (`Fix a typo in the
  README`).

And:

- No issue numbers or links in the subject; put them in the body.
- No merge commits on `develop` (see Branches), so no `Merge branch …`
  subjects.
- Commits already pushed are not reworded: `develop` is never
  force-pushed. Older commits in other styles stay as they are.

## Setup

- Go 1.27 or later. `GOTOOLCHAIN=auto` (the Makefile sets it) fetches the
  version `go.mod` asks for.
- herdr-core (`hc`) on `PATH`. Releases bundle it; for a source build
  install it with `curl -fsSL https://releases.herdr.dev/core/install.sh | sh`.
  The app starts the core if it is not running.
- Linux: GTK 3 and OpenGL. macOS 13 or later. No Windows work.
- Builds are pure Go (`CGO_ENABLED=0`): MyGo loads the system GUI
  libraries at run time. Only the race detector needs cgo.

## Build and run

```sh
make build        # ./shiori, the release edition
make run          # build and open the dev edition (./shiori-dev)
make dev-install  # ~/.local/bin/shiori-dev and a "Shiori Dev" launcher; again for newer changes
make dev-uninstall
make package      # platform package via mygo build
make clean
```

Use the **dev edition** for day-to-day work. It runs beside an installed
Shiori without touching it: its files are in `~/.config/shiori-dev`, its
terminals in the herdr-core session `shiori-dev`, and it never updates
itself. `HC_SESSION=default ./shiori-dev` shows your usual terminals;
`SHIORI_HOME=/some/dir` keeps its files elsewhere.
`SHIORI_DEVTOOLS=1` turns on MyGo's element inspector (F12, or
Ctrl+Shift+C to pick an element).

## Checks

Before every commit:

```sh
make check                                   # go vet, go test, gofmt (what CI runs)
CGO_ENABLED=1 go test -race ./internal/app   # -race on the packages you touched
```

`make fmt` fixes formatting. CI (`.github/workflows/ci.yml`) runs gofmt,
vet and `go test -race ./...` on pushes to `develop` and `main`.

The integration test drives the real window headlessly against an
isolated core session (`HC_SESSION=shiori-it`):

```sh
CGO_ENABLED=0 go test -c -o /tmp/shiori-app.test ./internal/app
SHIORI_IT=1 SHIORI_SHOTS=/tmp/shots /tmp/shiori-app.test -test.run TestIntegration -test.v
```

## Writing tests

- UI tests drive the window headlessly with `ui.NewTester` and the
  helpers in `internal/app/*_test.go`: `uxApp` (an app on a fake core),
  `settle`, `mustClick`, `drain`. The fake core is `fakeBackend` in
  `commands_test.go`; use it rather than a real herdr-core.
- Tests that start processes must leave none behind. After a run, check
  with `pgrep -f '^/tmp/go-build[0-9]+/b001/app\.test'`.
- Do not strip `SHIORI_*` variables from a test's own environment
  (`modEnv` in `mods.go` drops them from mods' children, not from tests).
- Leave no debugging tests behind in commits.

## Code conventions

- Doc comments in the code's plain style: short sentences, what and why.
- Follow herdr-app's way (https://github.com/herdrdev/herdr-app) in
  behaviour and the shared formats: Shiori and herdr-app read and write
  the same herdr-core layout document, so changes there must stay
  compatible with it.
- Every setting a file holds is shown and changeable in the settings
  panel, and listed in the README's Files table.
- New user-facing behaviour goes in the README in the same commit.
- Linux and macOS only.

## Where things are

| Path | What |
| --- | --- |
| `cmd/shiori` | the command: opens the window, or talks to the open one |
| `internal/app` | the window: state, sync with the core, views, commands, control socket |
| `internal/core` | herdr-core client: framing, RPC, events, terminal streams |
| `internal/layout` | the shared layout document: model, edits, geometry |
| `internal/keys` | key bindings; `default_keys.json` holds herdr-app's defaults |
| `internal/agent`, `recovery`, `theme`, `programs`, `mods`, `changes`, `update` | see the README's "Layout of the code" |
| `examples/mods` | how to write a mod, with two examples |
| `tools/dev-install.sh` | installs and removes the dev edition |
| `tools/logo` | draws the logo and icons (`make logo`, needs `uv`) |
| `bench` | the performance comparison with herdr-app |

## Releases

Only the owner releases, from `main` once `develop` has gone into it:
`git tag vX.Y.Z && git push origin vX.Y.Z`. The release workflow tests,
builds Linux and macOS (amd64, arm64) with herdr-core bundled, and
publishes the archives with `checksums.txt`.
