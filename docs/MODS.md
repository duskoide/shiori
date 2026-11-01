# Mods — design

Status: built (the first phase). The examples below are in
`examples/mods/`, and the tests run them.

A mod adds commands to Shiori and reacts to what happens in the window. It
is a folder with a manifest and a program; the program runs outside the
window and talks to it over the control socket. This first phase gives
mods commands, events and the dialogs Shiori already has. Drawing panes
and status-bar items is a later phase (see the end).

## Why out of process

- Go cannot load code at run time portably, and the app is built with
  `CGO_ENABLED=0`, which rules out the `plugin` package.
- A mod in its own process can be written in any language, and when it
  crashes or hangs, the window carries on.
- The control socket already runs every command for `shiori do`. Mods
  extend that one protocol instead of adding a second API.

The cost is a process per running mod and a JSON round trip per call. For
commands and events that is cheap enough; it would not be for drawing,
which is one reason drawing waits for a later phase.

## A mod on disk

```
$SHIORI_HOME/mods/
  git-tools/
    mod.json      the manifest
    git-tools     the program (any executable: binary, script)
    log           what the program wrote to stderr (the window writes it)
```

The folder's name is the mod's id: lower case letters, digits and `-`,
at most 32 characters. Shiori reads `mods/` at start and watches it as it
watches `settings.json` (a poll every half second), so adding, changing
or removing a mod applies without a restart.

## The manifest

```json
{
  "api": 1,
  "name": "Git tools",
  "version": "0.3.0",
  "description": "Switch branches and open pull requests from the palette.",
  "run": ["./git-tools", "serve"],
  "start": "demand",
  "commands": [
    {
      "name": "branch",
      "title": "Git: switch branch",
      "about": "Pick a branch of the focused folder and check it out",
      "args": [{"name": "branch"}]
    },
    {
      "name": "open-pr",
      "title": "Git: open pull request",
      "run": ["./open-pr.sh"],
      "timeout": 10
    }
  ],
  "keys": {"global": {"ctrl-alt-b": "git-tools.branch"}},
  "events": ["focus.changed", "agent.status"]
}
```

| Field | Meaning |
| --- | --- |
| `api` | The protocol version the mod speaks. Required; this design is `1`. |
| `name`, `version`, `description` | For people, in Settings → Mods. `name` is required. |
| `run` | The mod's **service**: a program that stays running and handles the commands that have no `run` of their own, and the events. Paths starting with `./` are in the mod's folder. Optional. |
| `start` | `"demand"` (default): the service starts the first time one of its commands runs or the window has an event it subscribed to. `"startup"`: with the window. |
| `commands[].name` | The command is `<id>.<name>`: `git-tools.branch`. Letters, digits, `-`, `_`. |
| `commands[].title`, `about`, `args` | As the built-in commands have them (`shiori commands` prints the same shape). `args[].required` works as for built-ins. |
| `commands[].run` | A **one-shot** program for this command alone; without it the service handles the command. |
| `commands[].timeout` | Seconds a one-shot program may run, default 30, at most 600. |
| `commands[].hidden` | Not in the palette; still runs from keys and `shiori do`, and shows on the Keyboard page. |
| `keys` | Default key bindings, in `keys.json`'s format. Only the mod's own commands may be bound. |
| `events` | Events the service always gets (it can also subscribe when it connects). |

Unknown fields are an error, so a typo does not go unnoticed; a manifest
with an error does not load, and Settings → Mods shows why.

A mod needs at least one command or one event. A mod with only one-shot
commands needs no service at all: a shell script is a complete mod.

## How a command runs

The palette, key bindings and `shiori do` all go through the same command
registry. Today that is the fixed `commands` slice; it becomes a registry
the built-ins and the loaded mods add to. Every caller gets mod commands
for free: they show in the palette (the mod's name as detail), on the
Keyboard page (grouped under the mod) and in `shiori commands` (with a
`"mod"` field).

### One-shot

The window starts `run` with the folder of the mod as its working
directory and this environment:

| Variable | Value |
| --- | --- |
| `SHIORI_API` | `1` |
| `SHIORI_SOCKET` | the control socket's path |
| `SHIORI_HOME` | the app's folder |
| `SHIORI_MOD` | the mod's id |
| `SHIORI_COMMAND` | `git-tools.open-pr` |
| `SHIORI_ARGS` | the arguments, a JSON object |
| `SHIORI_SPACE`, `SHIORI_TAB`, `SHIORI_PANE` | the focused space, tab and pane ids |
| `SHIORI_TERMINAL`, `SHIORI_CWD` | the focused pane's terminal and folder, when it has one |
| `SHIORI_BIN` | the `shiori` executable, to run `"$SHIORI_BIN" do …` without it on `PATH` |
| `HC_SESSION` | the herdr-core session, so `hc` reaches the same core |

What the program prints on stdout is the command's result: JSON when it
parses, else `{"output": "<text>"}`. A non-zero exit is an error, its
message the last line of stderr. The program may call back into the
window (`shiori do …`, or the socket) while it runs, for instance to ask
the person something with `app.pick`.

### Through the service

The window sends the service a `command.run` request (below) and passes
its reply on as the command's result. A service that is not running is
started first (`start: demand`), and the call waits up to 10 seconds for
it to connect.

## The protocol

Today a client writes one JSON line and reads one reply line:
`{"do":"tab.new","args":{}}`. That stays as it is.

A client that starts with `hello` instead keeps the connection, and both
sides then speak **JSON-RPC 2.0**, one message per line. JSON-RPC is what
herdr-core speaks as well, so client libraries exist in every language,
and it lets the window call the mod as well as the other way round.

```
→ {"jsonrpc":"2.0","id":1,"method":"hello","params":{"api":1,"mod":"git-tools","token":"…"}}
← {"jsonrpc":"2.0","id":1,"result":{"api":1,"app":"shiori","version":"0.4.0","session":"default"}}
```

`mod` and `token` come from the environment the window started the
service with (`SHIORI_MOD`, `SHIORI_MOD_TOKEN`); they tell the window
which mod is on the line. A client without them (a script, an editor
integration) is anonymous: it can call and subscribe, but has no commands
to be called for. The socket is already readable by its owner alone; the
token identifies a mod, it does not add security.

### Mod → window

| Method | Params | Result |
| --- | --- | --- |
| `do` | `{"command": "tab.new", "args": {…}}` | the command's result, as `shiori do` prints it |
| `ls` | — | as `shiori ls` |
| `status` | — | as `shiori status` |
| `subscribe` | `{"events": ["tab.created", …]}` | `{"events": [what is now subscribed]}` |
| `unsubscribe` | `{"events": […]}` | the same |

Errors use JSON-RPC's error object: `-32601` for an unknown method,
`-32602` for bad params, `1` for a command that failed (its message as
`message`, as `shiori do` prints it).

### Window → mod

| Message | Kind | Params |
| --- | --- | --- |
| `command.run` | request | `{"command": "git-tools.branch", "args": {…}, "context": {space, tab, pane, terminal, cwd}}`; the reply's `result` or `error` is the command's |
| `event` | notification | `{"name": "focus.changed", "seq": 41, "data": {…}}` |
| `shutdown` | notification | the window closes or the mod is turned off; the service has 2 seconds to exit before SIGTERM, then SIGKILL 2 seconds later |

## Events

The window works out its events once per frame, by comparing a small
summary of its state (ids of spaces, tabs and panes; names; focus; agent
statuses) with the last frame's. That catches every change, including
those that arrive from the core because another window (or herdr-app)
edited the layout, without touching every place that edits it. Events are
only worked out while someone is subscribed.

| Event | Data |
| --- | --- |
| `space.created`, `space.closed` | `{space, name, label, folder}`: `name` as given (maybe empty), `label` as shown |
| `space.renamed` | `{space, name, label}`, when `name` changes |
| `tab.created`, `tab.closed` | `{space, tab, name, label}` |
| `tab.renamed` | `{space, tab, name, label}`, when `name` changes |
| `pane.created`, `pane.closed` | `{space, tab, pane, terminal}` |
| `focus.changed` | `{space, tab, pane, terminal, cwd}` |
| `window.focus` | `{focused}`: the window got or lost the keyboard focus |
| `agent.status` | `{pane, terminal, app, status, previous}`; status is `working`, `blocked`, `done` or `idle`, as `shiori ls` says it |
| `settings.changed` | `{theme, font, font_size, sidebar}` |
| `keys.changed` | `{}` |
| `connection` | `{connected, error}`: herdr-core came or went |

Events are numbered (`seq`) per window. Each connection has a buffer of
256 events; a client that falls further behind loses the oldest and gets
`{"name": "events.lost", "data": {"count": n}}`, then the rest. A slow mod
cannot slow the window down.

Events are additive across versions: new events and new fields can come
without a new `api`; removing or changing one cannot.

## New built-in commands

Mods need to talk to the person. These commands are for everyone, mods
and `shiori do` alike, and reuse the window's existing dialogs:

| Command | Args | Result |
| --- | --- | --- |
| `app.notify` | `text`, `level` (`info`, `error`) | `{}`: a toast |
| `app.ask` | `title`, `value`, `placeholder`, `hint` | `{"value": "…"}`, or `{"cancelled": true}` |
| `app.confirm` | `title`, `detail`, `yes` (the button's label) | `{"ok": true\|false}` |
| `app.pick` | `title`, `items: [{id, title, detail}]`, `placeholder` | `{"id": "…"}`, or `{"cancelled": true}`: the palette as a picker, filtered as it is |

`app.ask`, `app.confirm` and `app.pick` answer when the person does.
Today the socket gives the window 5 seconds to answer a request
(`answerTimeout`). Commands that wait for the person get up to 10 minutes,
and are cancelled (`{"cancelled": true}`) when the window closes or the
connection drops. Only one such dialog shows at a time; a second waits
for the first.

## Key bindings

Built-in defaults (`default_keys.json`) and the mods' `keys` make the
default keymap; `keys.json` overrides it. Today a `keys.json` replaces
the defaults whole, which would hide every mod's keys as soon as the file
exists. So the rule becomes: the effective keymap is the defaults, then
the mods' keys where the chord is still free, then `keys.json` on top;
`"none"` as an action in `keys.json` removes a default binding. The
Keyboard page writes `none` when you remove a default key, and shows a
mod's keys as that mod's defaults (with its reset button).

A mod's key that clashes with a built-in default or another mod's loses,
and Settings → Mods says so.

## Turning mods on

A mod runs code as you, with everything you can reach. Shiori downloads
nothing; a mod is something you put in `mods/`. Even so, a folder that
appears there (dropped by an installer, a synced dotfiles repo, another
program) should not start running unnoticed. So:

- A new mod is **off** until you turn it on in Settings → Mods, which shows
  its name, folder, what it runs and the commands and keys it adds.
- On turning it on, Shiori records it in `settings.json` with a hash of its
  manifest: `"mods": {"git-tools": {"enabled": true, "manifest": "sha256:…"}}`.
- When the manifest changes (a new `run`, new commands), the mod is off
  again until you look at it. A change to the program alone does not turn
  it off: updating a mod should not need a click each time.

The new `settings.json` key gets its row in the table in the README and
its controls on the Mods page, as every setting has.

## Settings → Mods

A page beside Keyboard and Files:

- Each mod: its name, version and description, its state (off, running,
  idle, stopped, crashed, manifest error with the error), a switch to turn
  it on or off, Restart, Open folder and Show log (the last 200 lines).
- Its commands, with their keys, linking to the Keyboard page filtered to
  the mod.
- A note on where mods live, and that Shiori reads the folder as it
  changes.

## The service's life

- Started on demand or at startup, as the manifest says; stopped when the
  window closes, when the mod is turned off, or when its manifest changes
  (then started again if it is still on).
- When it exits on its own, it is started again after 1, 2, 5, 10 and 30
  seconds; five exits within a minute mark it crashed, with a toast, until
  you press Restart.
- stderr goes to `mods/<id>/log`, kept to its last megabyte. stdout is not
  read: the protocol is on the socket.
- A service that does not answer `command.run` in 30 seconds fails that
  command; the service keeps running.

## Examples

A complete one-shot mod, `mods/open-in-editor/mod.json`:

```json
{
  "api": 1,
  "name": "Open in editor",
  "commands": [{"name": "open", "title": "Open folder in editor", "run": ["sh", "-c", "exec ${EDITOR_GUI:-code} \"$SHIORI_CWD\""]}],
  "keys": {"global": {"ctrl-alt-e": "open-in-editor.open"}}
}
```

A service that sends a desktop notification when an agent needs you, in
Python, using only the standard library:

```python
import json, os, socket, subprocess

s = socket.socket(socket.AF_UNIX)
s.connect(os.environ["SHIORI_SOCKET"])
f = s.makefile("rw")

def send(msg):
    f.write(json.dumps({"jsonrpc": "2.0", **msg}) + "\n"); f.flush()

send({"id": 1, "method": "hello", "params": {"api": 1,
      "mod": os.environ["SHIORI_MOD"], "token": os.environ["SHIORI_MOD_TOKEN"]}})
send({"id": 2, "method": "subscribe", "params": {"events": ["agent.status"]}})
for line in f:
    msg = json.loads(line)
    if msg.get("method") == "event" and msg["params"]["data"]["status"] == "blocked":
        subprocess.run(["notify-send", "An agent needs you", msg["params"]["data"]["app"]])
    elif msg.get("method") == "shutdown":
        break
```

## How it is built

| Part | Where |
| --- | --- |
| Manifests: reading and checking | `internal/mods` |
| Layered keymaps (`Overlay`, `AddFree`, `Diff`, `none`) | `internal/keys` |
| The command registry, replies that come later | `internal/app/commands.go`, `deferred.go` |
| `app.notify`, `app.ask`, `app.confirm`, `app.pick` | `internal/app/dialogs.go` |
| Sessions, events | `internal/app/session.go`, `events.go` |
| Loading mods, one-shot commands, logs | `internal/app/mods.go` |
| Services | `internal/app/services.go` |
| Settings → Mods | `internal/app/modspage.go` |

The tests run a fake service built from the test binary itself (it runs
`os.Args[0]` again as the mod's program), and both examples.

## Later

- **Drawing.** Mod panes (the layout's `mod` blocks, which show a
  placeholder today) and status-bar items, from a description the mod
  sends (text, lists, buttons, progress) that Shiori draws with its own
  widgets, in the theme. A web view is the other way, at the cost of
  WebKitGTK on Linux and a look apart from the app's.
- **Programs.** A `programs` field adding program names and icons, merged
  with `programs.Load`; a user's own `programs.json`, which the app does
  not read yet, at the same time.
- **Installing.** `shiori mod install <git url>`, updates, and a list of
  known mods.
- **Permissions**, if mods from people you do not know become common.

## Decisions

- **The keymap rule** is as in [Key bindings](#key-bindings): defaults,
  then mods' keys on free chords, then `keys.json`, with `none` removing a
  default.
- **`settings.json` gets `mods`**, as in [Turning mods on](#turning-mods-on),
  with its controls on the Mods page.
- **herdr-app is not a constraint.** Shiori does not need to stay
  compatible with herdr-app's handling of `keys.json` or `settings.json`,
  nor run herdr-app's own mods (the layout's `mod` blocks, `mod:<name>`
  key sections). Those stay as they are today: kept, not used.

## Not planned

- **Windows.** Shiori runs on Linux and macOS; the control socket is a Unix
  socket and services are stopped by process group.
