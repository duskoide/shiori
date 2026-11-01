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
type Change struct { Docs bool; Terminals bool; Seen map[string]uint64 }

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
type Space struct { ID, Name, Folder string; Tabs []*Tab; Extra map[string]json.RawMessage }
type Tab struct { ID, Name string; Root *Node; Extra map[string]json.RawMessage }
type Node struct { ID, Type string; Children []*Node; Content *Content; Size *Size; Extra map[string]json.RawMessage }
type Content struct { Terminal, Mod string; Props json.RawMessage; Extra map[string]json.RawMessage }
type Size struct { Px, Weight *float64 }
func Decode(b []byte) (*Document, error)   // null/empty -> empty document
func (d *Document) Encode() ([]byte, error)
func Equal(a, b *Document) bool            // semantic, 268.0 == 268
func (d *Document) Clone() *Document
func NewID(prefix string) string           // prefix + 32 lowercase hex
// queries
func (d *Document) Space(id string) *Space
func (d *Document) Tab(id string) (*Space, *Tab)
func (d *Document) Pane(id string) (*Space, *Tab, *Node)
func (d *Document) PaneOfTerminal(addr string) (*Space, *Tab, *Node)
func (t *Tab) Panes() []*Node               // block leaves, document order
// edits
func (d *Document) AddSpace(name, folder string) *Space
func (d *Document) RemoveSpace(id string) bool
func (d *Document) MoveSpace(id string, index int) bool
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
type Options struct { Recent time.Duration; MaxFiles int; NoGit bool } // a day; 20000; walk even in git
func Scan(ctx context.Context, folder string, prev *Snapshot, opt Options) Snapshot // most recent first
func Same(a, b Snapshot) bool
var Interval = 2 * time.Second // a Watcher's least wait; it waits 30× what a scan cost (git's CPU time included)
func Watch(folder string, opt Options, changed func(Snapshot)) *Watcher // changed: each scan that differs
func (w *Watcher) Folder() string
func (w *Watcher) Refresh()
func (w *Watcher) SetIdle(idle bool) // 5× slower (10s at least) while nobody looks; scans at once when looked at again
func (w *Watcher) Stop()
func Since(now, t time.Time) string // "now", "<1m", "5m", "3h", "2d"
```
