# Remote machines

Status: built (phases 0 to 6 below); phase 7, trying it on real machines
with the owner, is left. Branch `remote-machines` (from `main` at
`0a8c145`). Reference: herdr-app, public at
https://github.com/herdrdev/herdr-app (clone it with `git clone --depth 1`).
How to use it: README, "Work on another machine". What follows is the
plan as it was written, with each phase's notes of what was done.

Goal: Shiori shows and works with the spaces, tabs and terminals of other
machines that herdr-core is connected to, as herdr-app does, beside this
machine's. herdr-core does the networking; Shiori has to stop refusing
remote machines and learn to read, write and show them.

## Working rules (for whoever picks this up)

- Commit as **Rafi Putra Nugraha <rafipeen@gmail.com>**, author and
  committer (`git -c user.name=… -c user.email=… commit`). No
  `Co-Authored-By`, no session or tool trailers, nothing naming an AI.
- Meaningful branch names. Do not open pull requests; push the branch.
  The owner merges, or says so when they want it merged into `main`.
- One commit per phase below, each passing `make check` (vet, tests,
  gofmt) and `CGO_ENABLED=1 go test -race` on the packages it touches.
- Every setting a file holds is shown and changeable in the settings
  panel, and listed in the README's Files table.
- Tests drive the window headlessly (`ui.NewTester`, `uxApp`, `settle`,
  `mustClick`, `drain` in `internal/app/*_test.go`) against `fakeBackend`
  (`commands_test.go`). Tests that start processes must leave none; check
  with `pgrep -f '^/tmp/go-build[0-9]+/b001/app\.test'`. Never strip
  `SHIORI_*` names from a test's own environment variables (mods.go's
  `modEnv` drops them from children).
- Doc comments in the code's plain style: short sentences, what and why.
- Linux and macOS only; no Windows work on this side.

## Decisions (from the owner)

- Follow herdr-app's way in everything. Another machine needs herdr-core
  (`hc`) at least; Shiori there is optional.
- Machines are reached over SSH (key exchange, as herdr-core does it).
- Temporary machines: leave them out, as herdr-app does (they are agents'
  short-lived sandboxes).
- No Windows machines: no PowerShell fallback.
- Testing: a headless Proxmox node (only needs `hc`; no GUI) and a second
  workstation. The window runs on the owner's machine; the tests here use
  a fake core.

## What herdr-app does (phase 0)

herdr-app manages machines with the **`hc` command**, not with RPC calls
(only `machine.list` is called as RPC, in `src/window.rs`):

| What | Command | Where in herdr-app |
| --- | --- | --- |
| List | `hc machine ls --json` ⇒ `{machines:[…]}` as `machine.list` | `defaults/frame/settings/main.js` `loadMachines` |
| Add | `hc machine add <target> --json`; target is how SSH reaches it (`user@host` or a `~/.ssh/config` host) | `defaults/mods/machine-add/main.js` `add` |
| Add, when it needs a person | on error kind `hc_missing`, `incompatible`, `login_needed`, `auth_failed`, `host_key`, or a message about a password, passphrase, host key, permission or login: run `hc machine add <target>` (no `--json`) in a terminal and show it, so the person answers; poll `hc machine ls` every 3 s until it is there | same, `inTerminal` |
| Rename | `hc machine rename <name> <new>` | settings `machineAction` |
| Remove | `hc machine rm <name>`: "its terminals keep running there" | same |
| Update its core | `hc machine update <name>` (offered when its version is older than this core's); answers `{version}` | same |
| Log in | `hc machine login <name>`, for `needs_login` | `src/window.rs` 1862 |
| Run a program there | `hc exec --json --on <name> --timeout <s> [--cwd <dir>] -- <cmd> <args…>` ⇒ `{code, stdout, stderr (base64), timed_out}` or `{error:{message}}` | `src/mods.rs` `run_remote` |

Errors from `hc … --json` come as `{"error":{"kind","message"}}` on stdout
or stderr; the exit code is not 0.

The Add dialog lists the `Host` entries of `~/.ssh/config` (patterns with
`*`, `?` or `!` left out) to pick from, and takes a typed target. On a Mac
it says once that macOS asks on that Mac's screen before programs read
Desktop, Documents or Downloads (optional for Shiori).

Launching on another machine (`src/remote_shell.rs` `launch`): the core
there may have only SSH's minimal PATH, so the command runs in that
machine's login and interactive shell, picked from `$SHELL` there:

```text
sh -c '<script>' sh <cmd…>     where <script> is
shell=${SHELL:-/bin/sh}
case "${shell##*/}" in
  fish) exec "$shell" -lic 'command $argv[2..]; exec $argv[1] -li' "$shell" "$@" ;;
  sh|bash|zsh|dash|ksh) exec "$shell" -lic '"$@"; exec "$0" -li' "$shell" "$@" ;;
  *) exec sh -c '"$@"; exec "${SHELL:-sh}" -i' sh "$@" ;;
esac
```

Layout (`src/mirror.rs`): `qualify(id, machine)` is `machine/id`;
`local(id, machine)` strips it. A space records its machine (it is not
read from the id). `capture` writes one machine's spaces with ids made
local; `replace` puts a machine's document back at the place its spaces
had, qualified. Moves keep spaces inside their machine's block. When a
machine leaves the list, its spaces go from the window.

## What the core offers (docs/PROTOCOL.md)

- `machine.list {}` ⇒ `{machines:[{name,target,id,host,version,state,why,since_ms,session,temporary,via}]}`;
  `state` is `connected`, `needs_update`, `needs_login` or other.
  herdr-app polls it (every 2.5 s and when terminals change), ignores
  `temporary` machines, and re-reads a machine's document when its
  `since_ms` changes (a reconnect). Shiori has `core.Client.Machines`.
- `machine.add`, `machine.connect`, `machine.rename`, `machine.rm`,
  `machine.exec` exist as RPC, but herdr-app uses the `hc` command for
  them (above), and so does Shiori.
- Remote terminals come in the same event stream as local ones, addressed
  `mac/t7`. `terminal.attach`, `kill`, `label`, `keep`, `get` take such
  addresses; the core routes them.
- `terminal.run {…, on: "mac"}` starts a terminal there (`core.RunParams.On`
  exists). `cwd` is a path on that machine, `null` its home; `argv: []`
  its default shell. herdr-app retries a remote start that fails with
  "not found" as PowerShell (a Windows machine).
- Layout: **one `herdr-app` document per machine**, `doc.get/doc.set
  {name, on: "mac", …}` (`core.Client.DocGet/DocSet` take `on`). Ids and
  terminal addresses inside a machine's document are that machine's own
  (`sp…`, `t7`); herdr-app qualifies them as `mac/<id>` in memory and strips
  the prefix when saving. Spaces are ordered local first, then machines
  alphabetically.
- Doc events carry `machine: "mac"`; `core.Events` turns every doc event
  into `Change{Docs: true}` today, without the machine.
- Labels: Shiori labels the terminals it starts `herdr.space`,
  `herdr.tab` (layout ids) and `herdr.seen`. For a remote terminal they
  are the ids **within that machine's document** (unqualified).

## What Shiori does today

- Refuses other machines: `localMachine` (`space.new --machine`) and
  `localTerminal` (`terminal.keep`, `terminal.park`) in
  `internal/app/commands.go`; `remote()` in `internal/app/sync.go` makes
  `reconcile`, `orphan` and adoption skip remote terminals.
- Reads and writes only the local document: `session1`, `remoteDoc`,
  `flush`, `refetchDoc`, `drain` in `sync.go`, with one set of sync state
  on `App` (`doc`, `base`, `version`, `loaded`, `saving`, `dirty`,
  `refetch`, `sy syncState`).
- Assumes this machine elsewhere:
  - `launchArgv` (commands.go) wraps agents in the local `$SHELL`.
  - `tildePath`, `homeDir`, `folderName` (model.go) use the local home.
  - The changes panel (`changespanel.go`, `internal/changes`) watches a
    local folder.
  - Mods' environment (`modEnv` in mods.go) gives `SHIORI_CWD` with no
    machine.
  - "Open in editor" and the diff tab (`changespanel.go`) run locally.
  - The status bar says "This computer" (`tabbar.go` `statusBar`).
- `ls` and `status` (control.go), events (events.go) and the agents
  list know nothing of machines.

## Design

### Model

- `machines []core.Machine` on `App`, from `machine.list`, polled every
  2.5 s while connected and when the terminal list changes; temporary
  machines left out. A machine is *usable* when `state == "connected"`.
- The window keeps **one merged `layout.Document`** (`a.doc`) as now, so
  views, commands and focus code stay as they are. Remote spaces carry
  qualified ids (`mac/sp…`, tabs `mac/tb…`, panes `mac/p…`, terminals
  `mac/t7`, which already is how the core names them).
- New in `internal/layout`:
  - `func Qualify(d *Document, machine string) *Document`: prefixes every
    id and terminal address.
  - `func (d *Document) Split() map[string]*Document`: by machine, with
    prefixes stripped (`""` is local). Unknown fields must round-trip
    (PROTOCOL.md §7.2).
  - `func MachineOf(id string) string`.
  - `Space` gets a `Machine string` field, not saved in the document
    (herdr-app's `Space.machine`); `AddSpace` takes it. New ids on a
    machine are qualified (herdr-app's `fresh_id_on`). Moving a space keeps
    it inside its machine's block.
- Sync state becomes **per machine**: `map[string]*docSync{base, version,
  loaded, saving, dirty, refetch, saveDone, saveFails, saveAfter}`.
  `flush` saves each machine whose part of `a.doc` differs from its
  `base`, one request in flight per machine. On conflict it re-reads that
  machine. A machine that disconnects keeps its spaces shown, read only,
  greyed, with "Reconnecting to mac…"; its saves wait.
- `remoteDoc(machine, value, version)` replaces that machine's spaces in
  `a.doc`, keeping the order local first, then machines alphabetically.
- `core.Change` gains `DocMachines map[string]bool` (`""` local), filled
  from doc events' `machine`. A missed-events reset sets all. A changed
  `since_ms` re-reads that machine's document.

### Terminals

- `reconcile`, `orphan`, `startAdoption`, `adopt`, `checkMissing` handle
  remote addresses once that machine's document is loaded and the machine
  is usable. Adoption puts a remote orphan in the space its (unqualified)
  labels name, qualified.
- `spawn` passes `On: machineOf(space)`, a cwd on that machine, and
  unqualified ids in `herdr.space` and `herdr.tab`. The returned address
  (`mac/t7`) is what the pane stores.
- `launchArgv` for a remote machine uses herdr-app's remote launch script
  (above), not the local `$SHELL`. No PowerShell fallback.
- `terminal.keep` and `terminal.park` accept remote addresses.
  `space.new --machine mac` and `tab.new` in a remote space start there.

### Window

- **Sidebar.** Spaces are grouped by machine: a small header per machine
  with its name and a state dot (connected, reconnecting, needs login,
  needs update), and a `+` for a new space there. This machine's spaces
  come first, under "This computer" (no header when there is no other
  machine). The agents list labels remote agents `mac · space / tab`.
- **Status bar.** "This computer" becomes the machines: "This computer ·
  2 machines" with a dot per state. A click opens Settings → Machines.
- **Settings → Machines** (new page, between Mods and Files), herdr-app's
  settings Machines section:
  - Each machine: name, target, host, version, state and why. Rename, and
    Remove (confirmed inline).
  - Update when its core is older than this one's.
  - Log in for `needs_login`: `hc machine login <name>` in a new tab.
  - Add: the `~/.ssh/config` hosts to pick from, or a typed target.
    `hc machine add <target> --json`; when it needs a person, the same
    command without `--json` in a new tab, and the list polled until the
    machine is there.
  - Machines live in the core, not in Shiori's files, so this page is
    their only control.
- **`hc`** is found as the core binary is (`internal/core/discover.go`:
  `../libexec/shiori` beside Shiori, then `PATH` and the usual folders).
  A small runner in `internal/core` (or a new `internal/hccli`) runs
  `hc … --json` with a timeout and decodes `{error:{kind,message}}`.
- **Commands** for scripts and mods:
  - `machine.list` answers the list.
  - `machine.add {target}`, `machine.rename {name, to}`,
    `machine.remove {name}` and `machine.update {name}` run the `hc`
    commands.
  - `space.new {machine}` already has its argument; with a machine and no
    folder, the space starts in that machine's home.
- **`ls` and `status`** add `machine` to spaces and panes, and a
  `machines` list.

### Features that touch the local disk

- **Changes panel** on a remote space: run `git status --porcelain=v1 -z
  --untracked-files=all` there with `hc exec --json --on <m> --cwd <dir>`
  (stdout base64), and feed the same parser (`internal/changes` takes a
  runner; `rev-parse --show-toplevel` the same way). Times come from
  `stat` there, or are the times the files were first seen changed. The walk outside git is not offered remotely: "Changes
  outside git are not shown for other machines". The diff tab opens a
  terminal on that machine; "Open in editor" is local only and is hidden
  for remote files.
- **Mods**: `SHIORI_MACHINE` joins `SHIORI_CWD` (whose path is then on
  that machine). Events add `machine` to space, tab, pane and focus data.
  A new event, `machine.changed {name, state, why}`, is added to
  `eventNames` and docs/MODS.md.
- `tildePath` and `folderName` shorten only this machine's paths.

## Phases (one commit each)

0. **Learn how machines are managed.** Done: herdr-app uses the `hc`
   command (above).
1. **Core side.** Done: `Change.DocMachines` and `DocChanged`;
   `core.HC` in `internal/core/hc.go`, with `AddNeedsPerson`.
   - `core.Change.DocMachines`, from doc events' `machine`.
   - An `hc` runner: find the binary, run `hc … --json` with a timeout,
     decode results and `{error:{kind,message}}`. Helpers `MachineList`,
     `MachineAdd`, `MachineRename`, `MachineRemove`, `MachineUpdate`, and
     `Exec(machine, cwd, argv)` ⇒ code, stdout, stderr.
   - Tests: the fake core (`fake_test.go`); a fake `hc` script on `PATH`.
2. **Layout per machine, read, shown and written.** Done:
   `layout/machines.go` (`Space.Machine`, `Qualify`, `Local`, `MachineOf`,
   `Part`, `Replace`, `Drop`, `OrderSpaces`, `AddSpaceOn`, `NewIDOn`;
   `MoveSpace` keeps a machine's spaces together). In the window,
   `docSync` per machine (`a.docs`, `a.local()`), `a.machines` polled
   every 2.5 s and when terminals change, temporary ones left out;
   `fetchDoc` reads a machine's document on connect, on reconnect
   (`since_ms`) and on its doc events; `flush1` saves each machine's part
   (writing came along, as it is the same code: phase 3 is left with the
   terminals). A machine not connected keeps its spaces and its saves
   wait; one that leaves takes its spaces away. Until phase 3, `spawn`
   refuses spaces of other machines. Tests: `internal/app/machines_test.go`
   (`fakeCore` has machines and a document per machine).
3. **Terminals on other machines.** Done: `spawn` starts a terminal on
   its space's machine (`On`, labels in that machine's ids, an empty cwd
   for its home) after `machineReady` (listed, connected, document read;
   says why not, with the `hc machine login/update` to run).
   `launchArgv(v, machine)` uses herdr-app's remote launch script
   (`remoteLaunch`). `space.new {machine}` starts in that machine's home
   and is named after it; `tab.new` and `pane.split` start in
   `folderFor(space)`. `terminal.keep` puts a remote terminal in a space of
   its machine; `terminal.park` takes any. `reconcile`, `orphan`,
   `startAdoption`, `adopt` judge a machine's terminals once
   `terminalsKnown` (connected, document read). Layout edits of a machine
   not connected stay in the window and are saved when it is back (as
   herdr-app keeps a machine's saves waiting); only terminals refuse.
4. **Machines in the window.** Done (`machines.go`, `machinespage.go`):
   the sidebar heads each machine's spaces (state dot, `+` for a space
   there, greyed spaces while it is not connected); the status bar counts
   the machines and opens Settings → Machines, which lists them (state,
   why, version) with Log in, Update (older core), Rename and Remove
   (confirmed inline), and adds one by target or from `~/.ssh/config`,
   going on in a tab when `core.AddNeedsPerson`. Commands
   `machine.list/add/rename/remove/update/login` and `app.machines`;
   `ls` gives spaces' `machine` and `machines`, `status` each machine's
   layout state; the `machine.changed` event (and `machine` in
   `space.created`).
5. **Remote-aware features.** Done: `changes.Options.Run` runs git on
   another machine (`hc exec`; `rev-parse --show-toplevel --show-prefix`,
   then `status`), times being when a change was first seen (none for the
   first scan), and `changes.ErrNotGit` outside git; the panel watches
   the active space's folder on its machine, shows `mac:/path`, opens
   diffs in that space, and has no "Open in editor" for another machine.
   `SHIORI_MACHINE` for mods; `machine` in `focus.changed` and in a
   service's `command.run` context. `pathOn` and `folderName` shorten
   only this machine's paths; agents of another machine read
   `mac · space`.
6. **Docs.** Done: README (features, "Work on another machine", the
   note under Files, the Machines page), docs/PROTOCOL.md §4.14 (the `hc`
   commands and the remote launch), docs/CONTRACTS.md (core, layout,
   changes), docs/MODS.md (`SHIORI_MACHINE`, `machine` in events,
   `machine.changed`). Screenshots checked by eye.
7. **On real machines** (with the owner): add the Proxmox node and the
   workstation; spaces, terminals, agents, reconnects, removal.

## Open questions

- None blocking. Whether temporary machines should ever show stays as
  herdr-app has it (they do not).
