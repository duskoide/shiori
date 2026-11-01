# Remote machines — plan

Status: plan, not built. Branch `remote-machines` (from `main` at `0a8c145`).

Goal: Shiori shows and works with the spaces, tabs and terminals of other
machines that herdr-core is connected to, as herdr-app does, beside this
machine's. herdr-core does the networking; Shiori has to stop refusing
remote machines and learn to read, write and show them.

## Working rules (for whoever picks this up)

- Commit as **Rafi Putra Nugraha <rafipeen@gmail.com>**, author and
  committer (`git -c user.name=… -c user.email=… commit`). No
  `Co-Authored-By`, no session or tool trailers, nothing naming an AI.
- Meaningful branch names. Do not open pull requests; push the branch.
  The owner merges, or says so when he wants it merged into `main`.
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

## What the core offers (docs/PROTOCOL.md)

- `machine.list {}` ⇒ `{machines:[{name,target,id,host,version,state,why,since_ms,session,temporary,via}]}`;
  `state` is `connected`, `needs_update`, `needs_login` or other.
  herdr-app polls it (every 2.5 s and when terminals change), ignores
  `temporary` machines, and re-reads a machine's document when its
  `since_ms` changes (a reconnect). Shiori has `core.Client.Machines`.
- `machine.add`, `machine.connect`, `machine.rename`, `machine.rm`,
  `machine.exec`: **names only; parameters unknown** (see Open questions).
  The core's test switches include `HC_SSH`: it likely reaches machines
  over SSH.
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
  - `AddSpace` takes the machine. `NewID` ids get the machine's prefix.
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
- `launchArgv` for a remote machine does not use the local `$SHELL`:
  `["sh", "-lc", "\"$@\"; exec \"${SHELL:-sh}\" -il", "sh", cmd…]`. On
  `spawn_failed` with "not found", it tries PowerShell (`-NoExit -Command`)
  as herdr-app does.
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
- **Settings → Machines** (new page, between Mods and Files). For each
  machine: name, target, host, version, state and why, with Connect,
  Rename and Remove. Add takes a target (`user@host`, as the core expects)
  and a name. `needs_login` and `needs_update` show the core's hint and
  what to do. Machines live in the core, not in Shiori's files, so this
  page is their only control.
- **Commands** for scripts and mods:
  - `machine.list` answers the list.
  - `machine.add {target, name}`, `machine.connect {name}`,
    `machine.rename {name, to}` and `machine.remove {name}` mirror the
    core.
  - `space.new {machine}` already has its argument.
- **`ls` and `status`** add `machine` to spaces and panes, and a
  `machines` list.

### Features that touch the local disk

- **Changes panel** on a remote space: run `git status --porcelain=v1 -z
  --untracked-files=all` there with `machine.exec`, if its parameters
  allow (Open questions), and feed the same parser (`internal/changes`
  takes a runner). The walk outside git is not offered remotely: "Changes
  outside git are not shown for other machines". The diff tab opens a
  terminal on that machine; "Open in editor" is local only and is hidden
  for remote files.
- **Mods**: `SHIORI_MACHINE` joins `SHIORI_CWD` (whose path is then on
  that machine). Events add `machine` to space, tab, pane and focus data.
  A new event, `machine.changed {name, state, why}`, is added to
  `eventNames` and docs/MODS.md.
- `tildePath` and `folderName` shorten only this machine's paths.

## Phases (one commit each)

0. **Learn the machine methods.** On the owner's machine, with a real
   core, get the methods' schemas:

   ```bash
   printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"hello","params":{}}' '{"jsonrpc":"2.0","id":2,"method":"rpc.discover","params":{}}' | hc connect
   ```

   Or read herdr-app's `machines` code. Record `machine.add`, `connect`,
   `rename`, `rm` and `exec` parameters and errors in docs/PROTOCOL.md §4.
   Until this is known, phases 1–3 can go ahead; phase 4's Add, and phase
   5's remote changes, wait.
1. **Core client.** `Change.DocMachines`. Typed helpers `MachineAdd`,
   `MachineConnect`, `MachineRename`, `MachineRemove`, `MachineExec`.
   Tests in `internal/core` with the fake core (`fake_test.go`).
2. **Layout per machine, read and shown.**
   - `layout.Qualify`, `Split` and `MachineOf`, with tests, unknown fields
     round-tripping.
   - Per-machine sync state; read every connected machine's document; the
     machines poll; remote spaces in the sidebar; remote panes attach.
   - Writing is still local only, so remote spaces are read only in this
     phase.
   - `fakeBackend` gains machines and a document per machine.
3. **Writing and terminals.**
   - Per-machine `flush` and conflicts.
   - Spawn, split, close, rename, move, adopt, keep and park on other
     machines; remote `launchArgv`; the PowerShell fallback.
   - Disconnected machines read only.
   - `space.new --machine`.
4. **Machines in the window.** Sidebar groups, status bar, Settings →
   Machines, `machine.*` commands, `ls`/`status` fields, the
   `machine.changed` event.
5. **Remote-aware features.** Changes panel through `machine.exec`;
   `SHIORI_MACHINE`; machine fields in events; paths shortened only
   locally; "Open in editor" local only.
6. **Docs.** README (features; Machines page), docs/PROTOCOL.md (what
   phase 0 found), docs/CONTRACTS.md (layout and core additions),
   docs/MODS.md (events). Screenshots checked by eye (`ui.Tester` image to
   PNG, then read it).

## Open questions (for the owner)

- `machine.add`: what is a target (`user@host`, an SSH config host, a
  URL)? Does the core install itself on the other machine, or must `hc` be
  there already?
- What does `needs_login` ask the person to do, and can Shiori do it, or
  only show the core's hint?
- Should `temporary` machines (herdr-app ignores them) show?
- Remote Windows machines: keep herdr-app's PowerShell fallback, or leave
  Windows remotes out?
- Testing: is there a second machine (or VM, or container with sshd) to
  try phases 2–5 against a real core? The tests here can only use a fake
  core.
