# herdr-core wire protocol, as used by herdr-app

Sources: /tmp/herdr-core/schema/{README.md,hc.openrpc.json}, docs/security.md, crates/herdr-core/src/{client.rs,cli.rs,platform/unix.rs};
/tmp/herdr-app/src/{hc.rs,core_bin.rs,setup.rs,mirror.rs,mirror_sync.rs,terminal.rs,window.rs,model.rs,layout.rs}, docs/labels.md.
Verified live (read-only, plain mode) against core 0.1.16 on this machine: `hello`, `doc.get`, `machine.list`, `terminal.list`.

## 0. TL;DR for a Go client

1. Find the core Unix socket (`1) and dial it directly. (herdr-app instead spawns `hc connect`, a dumb byte pump to the same socket that also auto-starts the core. Both work; bytes are identical.)
2. Send one line: `{"jsonrpc":"2.0","id":0,"method":"hello","params":{"framed":true}}\n`. Read exactly one reply line. After it the connection is **binary framed**.
3. Check `result.generation == 1`, `result.version >= 0.1.11`, and that `result.methods` has the methods you use.
4. Channel 0 carries JSON-RPC (one whole JSON message per DATA frame). `events.subscribe` and `terminal.attach` return a `channel` number; their bytes arrive as DATA frames on that channel.
5. Terminal output = raw VT bytes (you need your own terminal emulator; core draws nothing and does NOT encode keys). Input = DATA frames on the attach channel containing the raw bytes for the pty. Resize/focus/display-facts are dedicated frame kinds.
6. Layout (spaces/tabs/panes) lives in a core-stored JSON **document** named `herdr-app` (`doc.get`/`doc.set`, version CAS), one per machine; each terminal carries `herdr.space`/`herdr.tab` labels linking it to the layout.

## 1. Socket discovery, env vars, launching the core

### 1.1 Core side (docs/security.md "State and endpoints"; platform/unix.rs `endpoints()`)
Unix socket, in priority order:
1. `HC_SOCKET` env var (full path; then config.toml `socket` setting).
2. `$XDG_RUNTIME_DIR/herdr-core/<session>.sock` (if set and non-empty).
3. `/run/user/<uid>/herdr-core/<session>.sock` if that dir exists.
4. else inside the state dir: `$XDG_STATE_HOME|~/.local/state/herdr-core/<session>/<session>.sock`.

`<session>` = `default` unless `HC_SESSION` (hc has no `--session` flag: use the env var, e.g. `HC_SESSION=x hc server start`). Holder sockets `default.holder.<id>.sock` sit beside it — ignore. Windows: named pipe (account+session). Config: `~/.config/herdr-core/config.toml`. Here: `/run/user/1000/herdr-core/default.sock`. State dir also has `core.lock` (contains the actual socket path of the running core; verified: it is the plain path, written without newline), `core.log`, `holder.log`, `docs.json` (persisted documents).

### 1.2 Env vars
- `HC_TERMINAL_SOCKET` = core socket path, **set by core only inside terminals it spawns** (api/run.rs). It is just an "I am inside this core's terminal" marker for the hc CLI (client.rs `terminal_id()` honours `HC_TERMINAL_ID` only if HC_TERMINAL_SOCKET is the own core). herdr-app does NOT read it. A Go client may use it as a first-guess candidate but must not require it.
- `HC_TERMINAL_ID` (e.g. `t2n`): address of the terminal you run in (passed as `caller`). `HC_SCOPE`: internal cleanup scope.
- `HC_SESSION`, `HC_SOCKET`, `HC_INSTALL_DIR` (installer target). Test-only: HC_HEARTBEAT_MS, HC_RETAIN_MS, HC_HOLDER_GRACE_MS, HC_SETTLE_MS, HC_QUIET_MS, HC_SSH, HC_OLD_BIN, HC_STARTED_MS, HC_KEEP_TEST.
- herdr-app does no socket discovery; it runs `<core-bin> connect` (src/hc.rs `Core::connect`) inheriting env, so HC_SOCKET / HC_SESSION / XDG_RUNTIME_DIR apply through the CLI. App-only env: `HERDR_CORE_DIRS` (tests: replaces binary search dirs), `HERDR_RELEASES_URL` (default https://releases.herdr.dev). herdr-app's own control socket `$HERDR_APP_HOME/herdr-app.sock` (control.rs) is unrelated to the core.

### 1.3 Launching the core if absent
- `hc connect` (cli.rs `connect()` → client.rs `connect_or_start`): try socket; on failure run `<exe> serve` detached (Linux: systemd user service if possible, else detached, log appended to `<state>/core.log`, `HC_SCOPE` removed from env); poll the socket every 10 ms up to **5 s** ("core did not start within 5 s"). Every hc command auto-starts the core the same way. If a core runs on a different socket than this env expects: kind `unavailable`, "the core listens at X, but this shell looks at Y … set HC_SOCKET".
- Go options: (a) dial, on ENOENT/ECONNREFUSED exec `hc connect`-less `herdr-core serve` detached and retry ≤5 s; or (b) always spawn `hc connect` and use its stdin/stdout as the connection (what the app does; it kills the child on drop; stderr inherited).

### 1.4 Finding the binary (src/core_bin.rs)
Order: `core` in settings.json → PATH → login shell PATH (`$SHELL -lc 'printf %s "$PATH"'`, 3 s timeout) → `~/.local/bin, /opt/homebrew/bin, /usr/local/bin, ~/.local/share/mise/shims, ~/.nix-profile/bin, /run/current-system/sw/bin, ~/.cargo/bin`. Names per dir: `herdr-core`, `hc`. Accept only if `<bin> --version` stdout starts with `hc ` (e.g. `hc 0.1.16`). If none: setup screen (setup.rs) offers install `curl -fsSL https://releases.herdr.dev/core/install.sh | sh` (→ ~/.local/bin or HC_INSTALL_DIR; installs herdr-core + hc), or choose a binary. Update: `hc update --json [--version X]` (stdout `{now:..}`; failure JSON on stderr `{error:{message,hint}}`; terminals keep running).

## 2. Transport framing

### 2.1 Plain mode (start of every connection)
JSON-RPC 2.0, one message per line (UTF-8 JSON + `\n`). Params are by-name objects. Reply `{"jsonrpc":"2.0","id":N,"result":...}` or `"error":{"code":-32000,"message":"...","data":{"kind":"not_found","hint":"..."}}`. Unknown method: code -32601, kind `unknown_method`; bad params -32602 `invalid_params`.

### 2.2 Framed mode (required for attach; the app always uses it)
Enabled by `hello {framed:true}`. The *reply to that hello* is still one plain line; all later traffic, both directions, is binary frames (src/hc.rs `encode`/`read_frame`, HEADER = 9):
```
[0..4)  channel  u32 little-endian
[4]     kind     u8
[5..9)  len      u32 little-endian   (payload ≤ 16 MiB)
[9..)   payload
```
| kind | name | dir | payload |
|---|---|---|---|
| 0 | DATA | both | ch 0: one complete JSON-RPC message (no newline needed). other ch: stream bytes |
| 1 | CREDIT | viewer→core | u32 LE bytes consumed (flow control) |
| 2 | CLOSE | both | viewer→core: detach, empty payload. core→viewer: stream over, JSON reason e.g. `{"reason":"exited","exit_code":0,"signal":null}`; core answers your CLOSE with an empty CLOSE |
| 3 | RESIZE | viewer→core | u16 LE cols, u16 LE rows |
| 4 | FOCUS | viewer→core | 1 byte: 1 gained, 0 lost |
| 5 | FACTS | viewer→core | JSON Display: `{"fg":"#rrggbb","bg":"#rrggbb","cursor":"#rrggbb","cell":[w_px,h_px]}` (all optional) |

- Channel 0 calls run concurrently; match replies by `id` (app: u64 counter from 1; hello id 0). Replies may be out of order.
- **Race:** first frames of a new channel can arrive before you've processed the RPC reply announcing the channel → buffer frames for unregistered channels (`early` map in hc.rs), flush on registration.
- Flow control: each stream starts with 256 KiB credit. Send CREDIT with the byte count after consuming each DATA frame (app: `credit(ch, bytes.len())`). >4 MiB behind ⇒ core drops backlog and repaints. Channel 0 never blocked. For the events channel the app sends an initial 1 MiB credit right after subscribe, then credits per frame.
- App: call timeout 10 s; a dedicated writer thread/queue so UI never blocks. On connection drop: pending calls fail with "lost the core", all stream channels get `Lost`, then reconnect every 1000 ms; on success re-subscribe, re-read docs, re-attach all panes (terminals survive core restarts).
- Writes must be serialized (single writer) so frames don't interleave.

## 3. Handshake / version check
Request: `{"jsonrpc":"2.0","id":0,"method":"hello","params":{"framed":true}}`. Params: `framed` bool; `generation` u32 (optional; the app omits it; unsupported ⇒ kind `unsupported_generation`); `core`/`calls_you` (core-to-core, ignore).

Live reply (plain mode; `framed` would be true if requested):
```json
{"id":1,"jsonrpc":"2.0","result":{"core":{"id":"02f2c6f4…","name":"aerynos"},
 "features":["framed","attach","events","status","shell"],"framed":false,"generation":1,
 "methods":["hello","server.status","server.stop","server.restart","terminal.run","templates.list","terminal.list","terminal.get","terminal.kill","terminal.rm","terminal.keep","terminal.label","terminal.read","terminal.attach","terminal.send","terminal.wait","terminal.keys","terminal.interrupt","terminal.put","machine.exec","file.read","file.write","file.edit","doc.get","doc.set","events.subscribe","machine.add","machine.list","machine.connect","machine.rename","machine.rm","rpc.discover"],
 "version":"0.1.16"}}
```
App check (`hc::check`):
- `GENERATION = 1`. generation > 1 ⇒ Newer (update the app); < 1 ⇒ TooOld.
- `version` (strip "hc ", split on `.`/`-`, 3 numeric parts) < **0.1.11** (`distribution/core-version`) ⇒ TooOld (run `hc update`). Numeric compare (0.10.0 > 0.9.3).
- `methods` must include USES = `terminal.run, terminal.list, terminal.kill, terminal.label, terminal.attach, terminal.keep, doc.get, doc.set, events.subscribe` else Missing. (App also calls terminal.get, terminal.read, machine.list, server.status.)
- `rpc.discover` / `hc schema` returns the whole OpenRPC doc (info.version = core version).

Error `data.kind` values: not_found, machine_unknown, finished, unavailable, needs_framed, invalid_params, name_taken, spawn_failed, invalid_template, config, conflict, internal, needs_update, needs_login, unknown_method, unsupported_generation. App handling: attach `unavailable` ⇒ pane "lost", retry later (kept terminal awaiting new shell / spawn_pending); attach `finished` ⇒ drop pane; doc.set `conflict` ⇒ re-read; `needs_update`/`needs_login` on doc ops ⇒ blocked, no timer retry. Hint in `error.data.hint`.

## 4. RPC methods the app uses
Addresses: local `t7`, remote `machine/t7`. All params by-name.

### 4.1 terminal.run (hc.rs `Core::run`)
```json
{"method":"terminal.run","params":{
  "argv":["sh","-c","…"],      // [] ⇒ that machine's default shell
  "cwd":"/home/me/proj",       // null ⇒ core cwd / remote home
  "env":{"TERM":"xterm-256color","COLORTERM":"truecolor","TERM_PROGRAM":"herdr"},
  "labels":{"herdr.space":"<spaceId>","herdr.tab":"<tabId>","herdr.seen":"0"},
  "on":null,                   // machine name or null = local
  "keep":true}}
```
Result = Terminal record + extras; the app reads only `result.address` (e.g. `"t2p"`, remote `"mac/t7"`). Returns once running. Other params: `line` (shell line), `name` (1-32 of a-z0-9-_.), `parent`/`no_parent`/`outlive_parent`, `wait`/`timeout`, `rm`, `no_notify`, `caller`. Errors: spawn_failed (app retries remote `sh` start as powershell argv if message contains "not found"), name_taken, machine_unknown, config. App always sets keep:true.

### 4.2 terminal.list
`{all?:bool, labels?:{k:v}}` ⇒ `{terminals:[Terminal], hidden_finished, machines:[MachineView]}`. (App derives its list from the events snapshot instead.)
### 4.3 terminal.get
`{terminal}` ⇒ Terminal (live or finished). Used to adopt orphans and to check a missing pane's terminal is truly gone.
### 4.4 terminal.read
`{terminal, lines:5}` ⇒ `{text, complete, how, why}` (screen + N history lines as plain text).
### 4.5 terminal.label
`{terminal, labels:{k:v}}` ⇒ Terminal. Value `""` removes the label. Used for `herdr.space`/`herdr.tab` (park = both ""), `herdr.seen`, `herdr.right-click` ("program"/"menu"), `herdr.pinned`.
### 4.6 terminal.keep
`{terminal, keep:bool}` ⇒ Terminal (set true when adopting/moving).
### 4.7 terminal.kill
`{terminal}` ⇒ Terminal. HUP→TERM→KILL; waits, so call off the UI thread. Core then emits `finished` (reason killed). `terminal.rm` (unused by app) removes finished records.
### 4.8 terminal.attach — `5. 4.9 events.subscribe — `6. 4.10 doc.get/doc.set — `7.
### 4.11 machine.list
`{}` ⇒ `{machines:[{name,target,id,host,version,state,why?,since_ms?,session?,temporary,via}]}`. `state`: "connected" | "needs_update" | "needs_login" | other. App polls on every 5th 500 ms tick and when terminals change; ignores `temporary` machines; a changed `since_ms` = reconnect ⇒ re-read that machine's doc. Live: `{"machines":[]}`.
### 4.12 server.status
`{}` ⇒ `{core,version,pid,uptime_ms,holders,holder,live,finished,installed_by}` (app uses version/installed_by for updater).
### 4.13 Unused by app, available
terminal.send {terminal,text}, terminal.keys {terminal,keys}, terminal.interrupt, terminal.wait, terminal.put, machine.add/rm/rename/connect, machine.exec, file.read/write/edit, server.stop/restart, templates.list. (Mods can forward arbitrary core calls, mods.rs.)
### 4.14 Machines, through the `hc` command
herdr-app (and Shiori, `core.HC`) manage machines with the command line, not the RPC methods above:

| What | Command |
| --- | --- |
| List | `hc machine ls --json` ⇒ `{machines:[…]}`, as `machine.list` |
| Add | `hc machine add <target> --json`; target is how SSH reaches it (`user@host`, a `~/.ssh/config` host) |
| Add, needing a person | on error kind `hc_missing`, `incompatible`, `login_needed`, `auth_failed`, `host_key`, or a message about a password, passphrase, host key, permission or login: `hc machine add <target>` (no `--json`) in a terminal; the list shows the machine once added |
| Rename | `hc machine rename <name> <new>` |
| Remove | `hc machine rm <name>` (its terminals keep running there) |
| Update its core | `hc machine update <name>` ⇒ `{version}` |
| Log in | `hc machine login <name>`, in a terminal, for `needs_login` |
| Run a program there | `hc exec --json --on <name> --timeout <s> [--cwd <dir>] -- <cmd> <args…>` ⇒ `{code, stdout, stderr (base64), timed_out}` |

Errors from `hc … --json` come as `{"error":{"kind","message"}}` on stdout or stderr, with a non-zero exit code.

An agent started on another machine runs in that machine's login and interactive shell (its core may have only SSH's minimal PATH): `sh -c '<script>' sh <cmd…>`, the script picking `$SHELL` there (herdr-app `remote_shell.rs`; Shiori `remoteLaunch`).

## 5. Terminal streaming

### 5.1 Attach (hc.rs `Core::attach`)
```json
{"method":"terminal.attach","params":{"terminal":"t27","cols":120,"rows":40,"history":2000,
  "display":{"fg":"#d4d4d4","bg":"#1e1e1e","cursor":"#ffffff","cell":[9,18]}}}
→ {"channel":7,"terminal":{…Terminal…}}
```
Extra params: `observe` (never types/resizes), `resumed` (after a dropped line; size set only when active again), `caller`. Needs framed mode (`needs_framed` otherwise). App takes `terminal.title` as initial title. `display.cell` is device pixels; core answers OSC 10/11/12 and CSI 14t/16t queries from it. Register the channel before draining early frames.

### 5.2 Output
Core → you: DATA frames on the channel = one byte stream:
1. First paint: current screen (+ up to `history` scrollback lines, main screen only), latest status report and title, then `CSI ?9001h`/`l` (win32-input-mode wanted by program).
2. Then raw program output, minus queries the core already answered.
=> Create a **fresh** emulator (cols×rows) per (re)attach, feed each payload verbatim (`vt.write(bytes)`), CREDIT(len). Title arrives as OSC 0/2 in the stream (app: `vt.take_title()`) and also as `title` events. Any reply bytes your emulator generates (e.g. kitty graphics replies, `take_replies()`) go back as input DATA.
Stream end = CLOSE frame with JSON `{reason,exit_code,signal}` (reason: exited, killed, server_stop, host_shutdown, holder_lost, stopped_by_restart, signaled…). Kept terminal lost ⇒ CLOSE, later `restarted` event ⇒ attach again (new channel). Connection loss ⇒ treat as Lost (program likely alive), reconnect, attach again.

### 5.3 Input
DATA frame on the attach channel; payload = **raw bytes for the pty**. Core never encodes keys: client encodes keys (legacy xterm / kitty keyboard protocol per modes the program set; the app uses libghostty encoders, vt.rs `encode_key`/`encode_mouse`), bracketed paste, mouse reports (SGR etc. per program's mode), focus reports. If the stream announced `CSI ?9001h` the client sends key events in win32-input-mode format (src/win32_keys.rs), only when kitty keyboard flags are 0. Empty payloads are not sent. Typing makes this viewer "active" (last active viewer sets pty size). Mouse wheel: with mouse tracking ⇒ mouse report; on alt screen w/o tracking ⇒ arrow-key bytes; else scroll local viewport (scrollback is held client-side from the 2000-line first paint).
Alternatives without attach: `terminal.send` (paste + enter), `terminal.keys` (named keys).

### 5.4 Resize / focus / facts / detach
- RESIZE (3): u16LE cols + u16LE rows. Re-send if the pane changed size while attach was in flight.
- FOCUS (4): 1 byte 1/0; gaining focus makes this viewer active.
- FACTS (5): JSON Display, only when fg/bg/cursor/cell changed.
- Detach: CLOSE (2) empty on the channel; core replies empty CLOSE. Terminal keeps running.

Go sketch:
```go
func frame(ch uint32, kind byte, p []byte) []byte {
    b := make([]byte, 9+len(p))
    binary.LittleEndian.PutUint32(b[0:], ch); b[4] = kind
    binary.LittleEndian.PutUint32(b[5:], uint32(len(p))); copy(b[9:], p); return b
}
// read: io.ReadFull(r, hdr[:9]); n := LE32(hdr[5:9]); io.ReadFull(r, payload[:n])
// call:   frame(0, 0, json{"jsonrpc":"2.0","id":id,"method":m,"params":p})
// credit: frame(ch, 1, LE32(n));  resize: frame(ch, 3, LE16(cols)||LE16(rows));  focus: frame(ch, 4, {1})
```

## 6. Events (hc.rs `subscribe`, `EventLines`; schema/README.md "Events")
### 6.1 Subscribe
`events.subscribe {}` (optional filters `terminal`, `children_of`, `labels`) ⇒ `{seq, channel, terminals:[Terminal]}` snapshot (local and remote terminals; remote as `machine/tN`; **no documents**). In framed mode `channel` is non-null and notifications are **newline-delimited JSON-RPC notifications inside DATA frames on that channel**; frame boundaries can fall mid-line, so buffer and split on `\n`. Credit as you consume. Channel CLOSE / connection loss ⇒ resubscribe (fresh snapshot) and re-read docs.
### 6.2 Notifications
```
{"jsonrpc":"2.0","method":"event","params":{"seq":42,"type":"status","terminal":"t7","data":{…}}}
{"jsonrpc":"2.0","method":"heartbeat","params":{"seq":42}}                  // every 15 s if idle
{"jsonrpc":"2.0","method":"missed","params":{"seq":57,"terminals":[…]}}     // fell behind: replace snapshot
```
Ignore events with `seq ≤ last applied` (seq of snapshot / missed). `seq` restarts per core process. Remote events arrive in the same stream (`terminal` = `mac/t7`; doc events carry `machine:"mac"`).

| type | data | apply |
|---|---|---|
| created / restarted | whole Terminal | insert/replace (restarted ⇒ reattach) |
| lost | {reason,exit_code,signal,finished_ms:null} | set fields; state stays live; pid=null; drop foreground |
| finished | {reason,exit_code,signal,finished_ms} | state="finished"; set fields; drop foreground |
| removed | {} | delete |
| keep | {keep} | set |
| status | {status,records,reported} | replace all three (status null = cleared) |
| turn | {turn,state} | set turn |
| title | {title} | set |
| labels | {labels[,record]} | replace labels; if `record` present insert/replace whole record |
| size | {cols,rows} | set |
| foreground | {pid,name,argv,cwd} or null | set (core polls ~2×/s) |
| doc | {name,version} (no terminal, no value) | re-read via doc.get |

App keeps `state=="live"` terminals as its list (`EventLines::live()`, same shape as terminal.list) and processes the stream in a ~500 ms loop (window.rs).
### 6.3 Rules the app follows
- Subscribe **before** reading documents; re-read docs after subscribe, reconnect, `missed`, or remote reconnect (changes while disconnected aren't replayed).
- `labels` events carry the whole map; app tracks max `herdr.seen` per terminal across a drained batch so a later lower "mark unread" can't hide its own newer write.

## 7. Layout: machine layout documents (src/mirror.rs, mirror_sync.rs)
### 7.1 API
- `doc.get {name:"herdr-app", on?:machine}` ⇒ `{value, version}` (never written ⇒ `{value:null,version:0}`).
- `doc.set {name, on?, version, value}` ⇒ `{version:new}`. CAS: version must equal current else kind `conflict` (message has the current version; app just re-reads). `value:null` deletes but keeps version. ≤1 MiB. Name rules = terminal name rules. Persisted in `docs.json`, survives restart. Core never interprets content. Each write emits a `doc` event.
- **One document per machine** named `herdr-app`: local = no `on`; remote machine "mac" = `on:"mac"`.
- Sync (mirror_sync.rs `round`): latest-state CAS, one request in flight per machine, no op queue: if local projection ≠ last known doc ⇒ doc.set with last version; on conflict re-read and adopt theirs (toast "Layout changed elsewhere"); on doc event/reconnect ⇒ doc.get. Skipped while dragging a divider. Undecodable doc ⇒ "Layout unavailable" and no writes.
### 7.2 Shape (live sample from this machine)
```json
{"spaces":[
 {"id":"sp0757831f…","name":"","folder":"/home/pn",
  "tabs":[{"id":"tb2f9e920b…","name":"",
           "root":{"id":"tb2f9e920b…","type":"row","children":[
              {"id":"pe844ff7e…","type":"block","content":{"terminal":"t27"},"size":{"weight":1.0}}]}}]},
 {"id":"spa21589b…","name":"","folder":"/home/pn/pi-config",
  "tabs":[{"id":"tb977044…","name":"","root":{"id":"tb977044…","type":"row","children":[
     {"id":"p513fdc…","type":"block","content":{"terminal":"t2c"},"size":{"weight":0.5}},
     {"id":"pb8c66d…","type":"block","content":{"terminal":"t2f"},"size":{"weight":0.5}}]}}]}]}
```
`Document{spaces}` → `Space{id,name,folder,tabs}` → `Tab{id,name,root:Node}`; all fields default (missing root ⇒ empty row). **Space = project (folder), tab = tiled arrangement, pane = block node holding a terminal.** Ids are opaque client-generated strings (app: prefix sp/tb/p + 32 hex; globally unique, not counters). Tab id doubles as its root node id.
Node (layout.rs): `{id, type:"block"|"row"|"column"|"stack", size?, min?, max?, hidden?, cloned_from?, look fields…}`
- block `content`: `{"terminal":"t27"}` | `{"mod":name,"props":{…}}` | `{"tab":"active"|id,"pane":PaneFrame}` | `{"agent":id|"focused"}` (last two mainly in layout *files*; stored space docs are mostly terminal and mod blocks).
- row/column: `children:[Node]` (row = side by side, column = stacked). stack: `children`, `active:<childId>`, `header?`.
- `size`: `{"px":240}` or `{"weight":0.5}`. Also optional look fields (background, border, focus, radius, …) and `floating` blocks (`Float{id,content,edge,align,peek,hidden}`).
- **Round-trip unknown fields** (decode into generic maps) — both frontends overwrite the same document, so dropping fields written by herdr-app would destroy data. Whole floats like `268.0` vs `268` are treated equal by the app.
- Not in the doc (private "View", per screen): active space/tab, focused pane, zoom, focus history, recent closed spaces, attention/seen state.
- Remote docs: ids/addresses stored in machine "mac"'s doc are local to it (`t7`, `sp…`); app qualifies in memory as `mac/<id>` on load (`Document::replace`) and strips on save (`Document::capture`). Spaces ordered local first then machines alphabetically.
### 7.3 Terminal ↔ layout link (labels)
App-started terminals get `herdr.space=<localSpaceId>`, `herdr.tab=<localTabId>`, `herdr.seen=0`. `reconcile()`: a live terminal with non-empty `herdr.space` that is in no pane (and whose machine doc is loaded) is an orphan; if `terminal.get` confirms (live, labels match) it is adopted (`terminal.keep true` if needed) into that space/tab as a new pane. Park = both labels "". A pane whose terminal isn't in the live list ⇒ `terminal.get` (`check_closed`), pane closed if not_found/finished. Newly spawned terminals get a "just born" grace period before pruning.
Create pane: terminal.run (labels, keep, cwd = space folder) → insert block {terminal:address} → doc.set. Close pane: remove node → terminal.kill (async) → doc.set. Quitting the app doesn't kill terminals.

## 8. Agent status (working / blocked / done / idle)
Core: programs report via **OSC 7501** (Program Status Protocol); core stores what was reported, never infers. Terminal fields: `status: {state, app?, kind?, msg?, progress?, title?}|null`, `records` (per-source map), `reported`, `status_ms`, `turn` (u64, +1 each time it stops working; a `status` event precedes the `turn` event), `foreground`, `title`, `parent` (sub-agent ⇒ app rings no alerts), `labels`. Live: `"status":{"app":"pi","state":"done"},"turn":7,"reported":true`.
App derivation (model.rs `status()`, docs/labels.md):
```
word = status.state   (null ⇒ not an agent)
"working"            -> working
"blocked" | "error"  -> blocked
else (idle/done/…):  if label herdr.seen parses: done if turn > seen else idle
                     else local rule: done if !seen else idle
rollup: blocked > done > working > idle > none
```
Local `seen`: set true on working/blocked; on busy→non-busy set to "was looking" (window active & pane visible). Alerts: newly blocked/error ⇒ Blocked; busy→non-busy while not looking ⇒ Done; none for sub-agents, terminals outside app spaces, or the first listing.
Shared seen: when truly looking, write once per turn (raise only, also if missing) `terminal.label {terminal, labels:{"herdr.seen":"<turn>"}}`; mark-unread = turn-1; new terminals "0". `herdr.pinned=1` pins (empty removes). Last writer wins.

## 9. Terminal lifecycle
- Create: terminal.run → address (all clients see `created`); then terminal.attach to view.
- Close window/pane view: CLOSE on its channel; terminal keeps running.
- Kill: terminal.kill ⇒ `finished` event, stream CLOSE reason killed.
- Program exits (code): stream CLOSE reason exited + `finished` ⇒ app closes the pane.
- Kept terminal's program lost (e.g. signal): `lost` (reason signaled), core spawns a replacement shell after backoff 1.5, 3, 6, 12, 30, 60 s (reset after ≥10 s lifetime), emits `restarted` ⇒ reattach; meanwhile attach/send ⇒ `unavailable`.
- Core restart/update: connection drops, terminals survive in holder processes; reconnect → resubscribe → reattach.

## 10. Terminal record (live sample)
```json
{"address":"t27","argv":["/home/pn/.nix-profile/bin/zsh"],"cols":92,"rows":38,"created_ms":1793508923634,
 "cwd":"/home/pn","die_with_parent":false,"exit_code":null,"finished_ms":null,
 "foreground":{"argv":["pi"],"cwd":"/home/pn/pi-config","name":"pi","pid":1980},
 "identity":"ghostty","keep":true,
 "labels":{"herdr.space":"sp0757…","herdr.tab":"tb2f9e…","where":"pi-config / 1"},
 "name":null,"notify":true,"parent":null,"pid":1586,"reason":null,"reported":true,"rm":false,
 "screen":"exact","signal":null,"state":"live","status":{"app":"pi","state":"done"},
 "status_ms":1793511722400,"template":null,"title":"π - pi-config","turn":7}
```
Required: address, labels, argv, cwd, state ("live"|"finished"), created_ms, screen, cols, rows. Others optional: paste_gap_ms, submit, stop, records, template.

## 11. Go checklist
1. Resolve socket (`1.1); if dial fails, start core (`1.3), retry ≤5 s.
2. Plain hello{framed:true}; read exactly one line (keep the bufio remainder for the frame reader).
3. check(): generation 1, version ≥ 0.1.11, required methods.
4. Reader goroutine (frame → ch0 reply by id / per-channel queue + early buffer) and single writer goroutine.
5. events.subscribe → table from snapshot; line-split channel data; credit.
6. doc.get herdr-app (+ each connected machine from machine.list); write with doc.set CAS; re-read on doc events / missed / reconnect.
7. Per visible pane: terminal.attach → VT emulator, credit, encode keys/mouse yourself, RESIZE on layout change, FOCUS, FACTS on theme change.
8. Status from status/turn/labels events per `8.
9. Reconnect loop every 1 s: resubscribe, re-read docs, reattach all panes.

## 12. Corrections found while implementing internal/core
- There is no `hc --session` flag; the session is selected with `HC_SESSION` only. `HC_TERMINAL_SOCKET`/`HC_TERMINAL_ID` are inherited inside core terminals and point at the *default* core: unset them (and `HC_SOCKET`) when working with an isolated session.
- `hc server status` (and every other hc command) auto-starts the core of the selected session; use `hc server stop` to stop only that session.
- Fallback socket (no runtime dir): `<state>/<session>.sock` with state = `$XDG_STATE_HOME/herdr-core/<session>`.
- The client skips frames of kinds other than DATA on channel 0 and ignores channel-0 messages without an `id` (notifications).
- A stream-closing CLOSE from the core is also the acknowledgement of our own CLOSE; after sending CLOSE the client must drop late DATA on that channel until it arrives.
