# Package contracts

Internal packages are UI-agnostic (no `github.com/egoist/mygo` imports) and
fully unit-tested. Only package `main` (the app) and `internal/ui*` touch MyGo.

## internal/core — herdr-core client

Wire protocol: see docs/PROTOCOL.md. Public API:

```go
type Options struct {
    Socket    string // explicit socket path; else discovery (HC_SOCKET, XDG_RUNTIME_DIR/herdr-core/<session>.sock, /run/user/<uid>/..., ~/.local/state/herdr-core/<session>/)
    Session   string // HC_SESSION; "" = env or "default"
    Binary    string // herdr-core/hc binary used to auto-start the core ("" = search ../libexec/shiori beside the executable, PATH, ~/.local/bin, ...)
    AutoStart bool   // when dialing fails, run "<binary> connect" as a byte pump (like herdr-app) instead
}
func Connect(ctx context.Context, o Options) (*Client, error) // hello{framed:true} + Check()
type Hello struct { Version string; Generation int; Methods []string; Features []string; Core struct{ID, Name string} }
func Check(h Hello) error            // generation==1, version>=MinVersion ("0.1.11"), required methods present
func (c *Client) Hello() Hello
func (c *Client) Call(ctx context.Context, method string, params, result any) error // *RPCError on error replies
type RPCError struct { Code int; Message, Kind, Hint string }
func (c *Client) Done() <-chan struct{} // closed when the connection is lost
func (c *Client) Close() error

// typed helpers
type RunParams struct { Argv []string; Cwd string; Env map[string]string; Labels map[string]string; On string; Keep bool; Name string }
func (c *Client) Run(ctx, RunParams) (address string, err error)  // Env defaults TERM/COLORTERM/TERM_PROGRAM=herdr
func (c *Client) Kill(ctx, address string) error
func (c *Client) Label(ctx, address string, labels map[string]string) error
func (c *Client) Keep(ctx, address string, keep bool) error
func (c *Client) Get(ctx, address string) (Terminal, error)
func (c *Client) DocGet(ctx, name, on string) (value json.RawMessage, version uint64, err error)
func (c *Client) DocSet(ctx, name, on string, version uint64, value json.RawMessage) (newVersion uint64, err error) // Kind "conflict" on CAS failure
func (c *Client) Machines(ctx) ([]Machine, error)
type ServerStatus struct { Core, Version string; Pid int; UptimeMs int64; Live, Finished int }
func (c *Client) Status(ctx) (ServerStatus, error) // server.status: which core process serves

type Terminal struct {
    Address, State, Cwd, Title string; Name, Parent *string; Argv []string
    Labels map[string]string; Status *Status; Turn uint64; Keep bool
    Cols, Rows int; Foreground *Foreground; CreatedMs, StatusMs int64; ExitCode *int; Reason *string
}
type Status struct { State, App, Kind, Msg string }
type Foreground struct { Pid int; Name string; Argv []string; Cwd string }

// events
func (c *Client) Subscribe(ctx) (*Events, error)
func (e *Events) Terminals() []Terminal   // snapshot, live+finished, core order, copy
func (e *Events) Live() []Terminal
func (e *Events) Changes() <-chan Change  // coalesced notifications; closed when the stream ends
type Change struct { Docs bool; DocMachines map[string]bool; Terminals bool; Seen map[string]uint64 } // DocMachines: whose document changed ("" this one's); nil with Docs: every machine's (events were missed)
func (c Change) DocChanged(machine string) bool

// machines, through the hc command (as herdr-app manages them)
type Machine struct { Name, Target, ID, Host, Version, State, Why string; SinceMs int64; Session string; Temporary, Via bool }
func (m Machine) Usable() bool // State == MachineConnected
const MachineConnected, MachineNeedsUpdate, MachineNeedsLogin = "connected", "needs_update", "needs_login"
type HC struct { Bin string; Env []string; Timeout time.Duration } // Bin "" = FindBinary; Env e.g. HC_SESSION=…
type HCError struct { Kind, Message string }                      // {"error":{"kind","message"}} from hc
func (h *HC) Path() (string, error)
func (h *HC) Argv(args ...string) ([]string, error)                // for a terminal to run
func (h *HC) Run(ctx, timeout time.Duration, out any, args ...string) error
func (h *HC) MachineList(ctx) ([]Machine, error)                   // hc machine ls --json
func (h *HC) MachineAdd(ctx, target string) error                  // hc machine add <target> --json (3 min)
func AddNeedsPerson(err error) bool                                // herdr-app's rule: run "hc machine add <target>" in a terminal instead
func (h *HC) MachineRename(ctx, name, to string) error
func (h *HC) MachineRemove(ctx, name string) error                 // its terminals keep running there
func (h *HC) MachineUpdate(ctx, name string) (version string, err error)
func (h *HC) Version(ctx) (string, error)                          // "0.1.16"
type ExecResult struct { Code int; Stdout, Stderr []byte }
func (h *HC) Exec(ctx, machine, cwd string, timeout time.Duration, argv ...string) (ExecResult, error) // hc exec --json; ErrExecTimedOut

// terminal streams: a net.Conn-like value for the MyGo terminal plugin (Options.Conn)
type Display struct { FG, BG, Cursor string; Cell [2]int } // JSON {"fg","bg","cursor","cell"}
func (c *Client) Stream(address string, d *Display) *Stream // lazy: attaches on first Resize/ResizePixels, or after 300ms at 80x24
func (s *Stream) Read(p []byte) (int, error)  // terminal output bytes; sends CREDIT; io.EOF after core CLOSE (see CloseReason), ErrLost when the connection dropped
func (s *Stream) Write(p []byte) (int, error) // input bytes -> DATA
func (s *Stream) Resize(cols, rows int) error
func (s *Stream) ResizePixels(cols, rows, cellW, cellH int) error // also sends FACTS with cell size when changed
func (s *Stream) Focus(focused bool)
func (s *Stream) CloseReason() *CloseReason   // {Reason string; ExitCode *int; Signal *string} after EOF
func (s *Stream) Close() error                // detach (CLOSE frame); the terminal keeps running
var ErrLost error
```

## internal/layout — the machine layout document ("herdr-app")

Round-trips unknown JSON fields at every level (both herdr-app and this app
write the same document).

```go
type Document struct { Spaces []*Space; Extra map[string]json.RawMessage }
type Space struct { ID, Name, Folder string; Tabs []*Tab; Extra map[string]json.RawMessage; Machine string } // Machine: the window's, never saved
type Tab struct { ID, Name string; Root *Node; Extra map[string]json.RawMessage }
type Node struct { ID, Type string; Children []*Node; Content *Content; Size *Size; Extra map[string]json.RawMessage }
type Content struct { Terminal, Mod string; Props json.RawMessage; Extra map[string]json.RawMessage }
type Size struct { Px, Weight *float64 }
func Decode(b []byte) (*Document, error)   // null/empty -> empty document
func (d *Document) Encode() ([]byte, error)
func Equal(a, b *Document) bool            // semantic, 268.0 == 268
func (d *Document) Clone() *Document
func NewID(prefix string) string           // prefix + 32 lowercase hex
func NewIDOn(machine, prefix string) string // NewID qualified for a machine
// machines: another machine's ids and addresses are qualified "mac/<id>" in the window
func MachineOf(id string) string            // "mac" for "mac/t7", "" for "t7"
func Qualify(id, machine string) string
func Local(id, machine string) string
func (d *Document) Machines() []string
func (d *Document) Part(machine string) *Document          // that machine's document, in its own ids (Extra only for this machine's)
func (d *Document) Replace(part *Document, machine string) // put it in place of that machine's spaces, qualified
func (d *Document) Drop(machine string)
func (d *Document) OrderSpaces()                           // this machine's first, then each machine's by name
func (d *Document) AddSpaceOn(machine, name, folder string) *Space
// queries
func (d *Document) Space(id string) *Space
func (d *Document) Tab(id string) (*Space, *Tab)
func (d *Document) Pane(id string) (*Space, *Tab, *Node)
func (d *Document) PaneOfTerminal(addr string) (*Space, *Tab, *Node)
func (t *Tab) Panes() []*Node               // block leaves, document order
// edits
func (d *Document) AddSpace(name, folder string) *Space
func (d *Document) RemoveSpace(id string) bool
func (d *Document) MoveSpace(id string, index int) bool  // clamped to its machine's spaces
func (s *Space) AddTab(name, terminal string) (*Tab, *Node)   // root {id: tab.ID, type: row, children: [block]}
func (s *Space) RemoveTab(id string) bool
func (s *Space) MoveTab(id string, index int) bool
type Direction int // Right, Down, Left, Up
func (t *Tab) Split(paneID string, dir Direction, terminal string) (*Node, error) // new pane after (Right/Down); halves the weight
func (t *Tab) RemovePane(paneID string) bool  // collapses single-child containers; root keeps id == tab id
func (t *Tab) Swap(paneID string, dir Direction) bool
func (t *Tab) Neighbor(paneID string, dir Direction) string   // spatial, from Rects
type Rect struct { X, Y, W, H float64 }     // 0..1
func (t *Tab) Rects() map[string]Rect
// naming
func (s *Space) Label(home string) string   // Name, else basename(Folder), "~" for home
```

## internal/agent — status derivation (docs/labels.md of herdr-app)

```go
type Word int // None, Idle, Working, Done, Blocked (ordered by urgency)
func (w Word) String() string
type Input struct { State string /* status.state */; Turn uint64; Labels map[string]string; LocalSeen bool }
func Derive(in Input) Word
func Rollup(words ...Word) Word
func SeenLabel(turn uint64) map[string]string // {"herdr.seen": "<turn>"}
func Pinned(labels map[string]string) bool
```

## internal/theme

```go
type Color struct{ R, G, B, A uint8 }
func ParseHex(s string) (Color, error)
func (c Color) Hex() string
func Mix(a, b Color, t float64) Color
type Palette struct { Background, Foreground, Selection, Cursor Color; ANSI [16]Color; Status *[3]Color }
func Preset(name string) (Palette, bool)  // graphite, ink, moss, paper, atom-one-dark
func LoadPalette(b []byte) (Palette, error)
type Tokens struct { Background, TerminalForeground, Foreground, Surface, Canvas, Chrome, Popover, Raised, Hover, Selected, Border, Input, Muted, Dim, Accent, AccentForeground, Solid, SolidForeground, Ring, Selection, Cursor, Working, Blocked, Done, Idle Color; ANSI [16]Color; Dark bool }
func Resolve(p Palette) Tokens
```

## internal/programs

```go
type Program struct { Key, Name, Icon string; Tint bool; Tile, Color string; Shell, Agent bool; Run []string }
func Resolve(app string, argv []string) Program // OSC-7501 app first, then argv rules
func IconSVG(name string) []byte               // embedded icons/<name>.svg, nil if none
func Startable() []Program                     // agents with run argv, menu order
```

## internal/keys

```go
type Chord struct { Ctrl, Alt, Shift, Super bool; Key string } // Key normalized: "a".."z", "0".."9", "enter", "tab", "pageup", "left", "f2", "/", "=", "-", ",", "[", "]" ...
func ParseChord(s string) (Chord, error)        // "ctrl-shift-t", "alt-[", "ctrl-+"
type Action struct { Command, Arg string; Send string; Terminal bool }
type Keymap struct { Sections map[string]map[Chord]Action }
func Parse(b []byte) (*Keymap, error)
func Default() *Keymap                          // embedded default_keys.json (Linux)
func (k *Keymap) Lookup(section string, c Chord) (Action, bool)
func (k *Keymap) KeysFor(command string) []string // display strings, e.g. "Ctrl+Shift+T"
func (c Chord) Spec() string                    // keys.json syntax, "ctrl-shift-t"; ParseChord reads it back
func (a Action) Spec() string                   // "tab.new", "pane.go:left", "send:…", "terminal"
func (k *Keymap) Clone() *Keymap
func (k *Keymap) Encode() ([]byte, error)       // a keys.json document, sorted; Parse reads it back
func (k *Keymap) Bind(section string, c Chord, a Action) (old Action, had bool)
func (k *Keymap) Unbind(section string, c Chord) bool
func (k *Keymap) ChordsFor(section, spec string) []Chord
// Action.None is keys.json's "none": Overlay removes the binding.
func (k *Keymap) Overlay(over *Keymap)          // over's bindings replace k's; its "none" remove them
func (k *Keymap) AddFree(other *Keymap) map[string][]Chord // adds other's bindings on free chords; returns those taken
func Diff(base, target *Keymap) *Keymap         // base.Overlay(Diff(base, target)) == target
func (k *Keymap) Empty() bool
```

## internal/mods

Mods' manifests (docs/MODS.md). UI-agnostic; the app runs the programs.

```go
const API = 1
type Manifest struct { API int; Name, Version, Description string; Run []string; Start string; Commands []Command; Keys map[string]map[string]string; Events []string }
type Command struct { Name, Title, About string; Args []Arg; Run []string; Timeout float64; Hidden bool }
type Mod struct { ID, Dir string; Manifest *Manifest; Hash string; Err error } // Hash: "sha256:<hex>" of mod.json
func Load(dir string, events []string) []Mod     // every folder of dir, by id; a missing dir is none
func Parse(id string, b []byte, events []string) (*Manifest, error) // strict: unknown fields are errors
func (m *Manifest) Keymap(id string) (*keys.Keymap, error) // its default keys; its own commands only
func Resolve(dir string, argv []string) []string // "./x" in the mod's folder
func ValidID(id string) bool
```

## internal/changes

The files being changed in a folder: in a git work tree, `git status`
(what differs from the last commit, and new files); elsewhere, a walk for
files modified in the last day. No cgo, no file notifications: it polls.

```go
type Kind int // Modified, Added, Deleted, Renamed, Untracked, Changed (outside git)
func (k Kind) Letter() string // "M", "A", "D", "R", "U"
type File struct { Path, OldPath string; Kind Kind; Time time.Time } // paths relative to the folder
type Snapshot struct { Folder string; Git bool; Root string; Files []File; Truncated bool; Err error; Took time.Duration } // Took: wall time, or git's CPU time if more
type Options struct { Recent time.Duration; MaxFiles int; NoGit bool; Run Runner } // a day; 20000; walk even in git; Run: git on another machine
type Runner func(ctx context.Context, dir string, argv ...string) (stdout []byte, code int, err error)
var ErrNotGit error // another machine's folder outside git: not looked at
func Scan(ctx context.Context, folder string, prev *Snapshot, opt Options) Snapshot // most recent first
func Same(a, b Snapshot) bool
var Interval = 2 * time.Second // a Watcher's least wait; it waits 30× what a scan cost (git's CPU time included)
func Watch(folder string, opt Options, changed func(Snapshot)) *Watcher // changed: each scan that differs
func (w *Watcher) Folder() string
func (w *Watcher) Refresh()
func (w *Watcher) SetIdle(idle bool) // 5× slower (10s at least) while nobody looks; scans at once when looked at again
func (w *Watcher) Stop()
func Since(now, t time.Time) string // "now", "<1m", "5m", "3h", "2d"; "" for a zero time (another machine's change seen before the first scan)
```

## internal/recovery

The session snapshot (`session.json`) that brings panes back after
herdr-core stopped without their terminals. Panes are keyed by id: a new
core gives terminal addresses out again.

```go
const Version = 1
type Core struct { Pid int; StartMs int64 }       // a core process
func At(pid int, uptimeMs int64, now time.Time) Core
func (c Core) Known() bool
func (c Core) Same(o Core) bool                    // same pid, starts within 5 s
type Pane struct { Terminal string; CreatedMs int64; Cwd string; Core Core }
type Snapshot struct { Version int; SavedMs int64; Core Core; Layout json.RawMessage; Panes map[string]Pane }
func FileName(session string) string              // session.json, session-<name>.json
func Load(path string) (*Snapshot, error)         // missing = empty; newer version is an error
func (s *Snapshot) Encode() ([]byte, error)
func WriteFile(path string, b []byte) error       // temp file, fsync, rename; 0600
// Lost: the pane showed addr under a core other than cur, and cur lists no
// terminal there (or one created at another time).
func (s *Snapshot) Lost(pane, addr string, cur Core, listed bool, createdMs int64) (Pane, bool)
```

## internal/update

New versions. One HTTPS request to GitHub's API for the latest release
(User-Agent `shiori-update`, a token only when one is set); installing
checks the archive against the release's checksums.txt.

```go
type Options struct { API, Repo, Token string; Client *http.Client }
func FromEnv() Options // SHIORI_UPDATE_API, SHIORI_REPO, GITHUB_TOKEN / GH_TOKEN
func (o Options) RepoName() string
type Release struct { Tag, Name, URL, Notes string; Published time.Time; Assets []Asset }
func (r Release) Version() string // the tag without its v
func Latest(ctx context.Context, o Options, tag string) (Release, error) // tag "" for the latest; ErrNoRelease
func Compare(a, b string) int     // semantic versions; build metadata (+…) ignored
func Development(v string) bool   // "-dev", 0.0.0 or unreadable: no release to update to
func ArchiveName() string         // shiori_<goos>_<goarch>.tar.gz
type Layout struct { Exe, CoreDir string }
func Here() (Layout, error)       // the running binary's; refuses go run builds
func Install(ctx context.Context, o Options, r Release, l Layout, progress func(string)) error
```

