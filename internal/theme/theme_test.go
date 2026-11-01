package theme

import "testing"

func mustHex(t *testing.T, s string) Color {
	t.Helper()
	c, err := ParseHex(s)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestParseHex(t *testing.T) {
	tests := []struct {
		in      string
		want    Color
		wantErr bool
	}{
		{"#17191d", Color{0x17, 0x19, 0x1d, 255}, false},
		{"FFFFFF", Color{255, 255, 255, 255}, false},
		{"#ffffff0e", Color{255, 255, 255, 0x0e}, false},
		{"#fff", Color{}, true},
		{"#gggggg", Color{}, true},
		{"", Color{}, true},
		{"#+12345", Color{}, true},
	}
	for _, tc := range tests {
		got, err := ParseHex(tc.in)
		if (err != nil) != tc.wantErr || (!tc.wantErr && got != tc.want) {
			t.Errorf("ParseHex(%q) = %v, %v", tc.in, got, err)
		}
	}
}

func TestHexRoundTrip(t *testing.T) {
	for _, s := range []string{"#17191d", "#ffffff0e", "#00000012", "#abcdef"} {
		if got := mustHex(t, s).Hex(); got != s {
			t.Errorf("Hex round trip %q -> %q", s, got)
		}
	}
}

func TestMix(t *testing.T) {
	a, b := Color{0, 100, 200, 255}, Color{100, 200, 0, 255}
	if got := Mix(a, b, 0); got != a {
		t.Errorf("t=0: %v", got)
	}
	if got := Mix(a, b, 1); got != b {
		t.Errorf("t=1: %v", got)
	}
	if got, want := Mix(a, b, 0.5), (Color{50, 150, 100, 255}); got != want {
		t.Errorf("t=.5: %v want %v", got, want)
	}
}

func TestPresets(t *testing.T) {
	for _, n := range PresetNames {
		if _, ok := Preset(n); !ok {
			t.Errorf("missing preset %s", n)
		}
	}
	if _, ok := Preset("nope"); ok {
		t.Error("unexpected preset")
	}
	g, _ := Preset("graphite")
	if g.Background.Hex() != "#17191d" || g.ANSI[15].Hex() != "#f0f0f2" || g.Selection.Hex() != "#394454" {
		t.Errorf("graphite: %+v", g)
	}
	// Mutating a copy must not change the preset.
	g.Status[0] = Color{}
	g2, _ := Preset("graphite")
	if g2.Status[0].Hex() != "#e5c07b" {
		t.Error("preset status shared")
	}
	a, _ := Preset("atom-one-dark")
	if a.Background.Hex() != "#21252b" || a.ANSI[8].Hex() != "#767676" || a.Cursor.Hex() != "#abb2bf" {
		t.Errorf("atom: %+v", a)
	}
}

func TestResolveDark(t *testing.T) {
	p, _ := Preset("graphite")
	tk := Resolve(p)
	if !tk.Dark {
		t.Error("graphite should be dark")
	}
	want := map[string]Color{
		"background": tk.Background, "popover": tk.Popover, "raised": tk.Raised, "hover": tk.Hover,
		"selected": tk.Selected, "border": tk.Border, "input": tk.Input, "canvas": tk.Canvas,
		"chrome": tk.Chrome, "foreground": tk.Foreground, "muted": tk.Muted, "dim": tk.Dim,
		"solid": tk.Solid, "solid_foreground": tk.SolidForeground, "accent": tk.Accent,
		"accent_foreground": tk.AccentForeground, "working": tk.Working, "blocked": tk.Blocked,
		"done": tk.Done, "idle": tk.Idle, "unknown": tk.Unknown, "cursor": tk.Cursor,
		"terminal_foreground": tk.TerminalForeground, "selection": tk.Selection, "surface": tk.Surface,
	}
	expect := map[string]string{
		"background": "#17191d", "popover": "#25272b", "raised": "#2c2e31", "hover": "#ffffff0e",
		"selected": "#ffffff17", "border": "#ffffff18", "input": "#ffffff24", "canvas": "#131518",
		"chrome": "#101114", "foreground": "#eeeeef", "muted": "#98999b", "dim": "#696a6d",
		"solid": "#eeeeef", "solid_foreground": "#111215", "accent": "#96b2d7",
		"accent_foreground": "#17191d", "working": "#e5c07b", "blocked": "#f0705f",
		"done": "#76a8ff", "idle": "#696a6d", "unknown": "#696a6d", "cursor": "#d2d4d9",
		"terminal_foreground": "#d2d4d9", "selection": "#394454", "surface": "#17191d",
	}
	for k, v := range expect {
		if got := want[k].Hex(); got != v {
			t.Errorf("graphite %s = %s, want %s", k, got, v)
		}
	}
	if tk.ANSI[2].Hex() != "#a6bd91" {
		t.Errorf("ansi2 = %s", tk.ANSI[2].Hex())
	}
}

func TestResolveLight(t *testing.T) {
	p, _ := Preset("paper")
	tk := Resolve(p)
	if tk.Dark {
		t.Error("paper should be light")
	}
	expect := map[string]Color{}
	for k, v := range map[string]string{
		"foreground": "#1b1b1e", "popover": "#ffffff", "raised": "#f6f6f6", "hover": "#0000000c",
		"selected": "#00000012", "border": "#00000017", "input": "#00000026", "canvas": "#fafaf9",
		"chrome": "#f1f1ee", "solid_foreground": "#ffffff", "cursor": "#25272c",
		"working": "#9a6700", "blocked": "#c93c2c", "done": "#2f6fe4", "accent": "#1f62d0",
	} {
		expect[k] = mustHex(t, v)
	}
	got := map[string]Color{
		"foreground": tk.Foreground, "popover": tk.Popover, "raised": tk.Raised, "hover": tk.Hover,
		"selected": tk.Selected, "border": tk.Border, "input": tk.Input, "canvas": tk.Canvas,
		"chrome": tk.Chrome, "solid_foreground": tk.SolidForeground, "cursor": tk.Cursor,
		"working": tk.Working, "blocked": tk.Blocked, "done": tk.Done, "accent": tk.Accent,
	}
	for k, v := range expect {
		if got[k] != v {
			t.Errorf("paper %s = %s, want %s", k, got[k].Hex(), v.Hex())
		}
	}
	// muted/dim/ring: only the red channel is away from rounding ties.
	if tk.Muted.R != 0x69 || tk.Dim.R != 0x96 || tk.Ring.R != 0x5f {
		t.Errorf("muted/dim/ring R = %x %x %x", tk.Muted.R, tk.Dim.R, tk.Ring.R)
	}
}

func TestResolveAtomStatusFromANSI(t *testing.T) {
	p, _ := Preset("atom-one-dark")
	tk := Resolve(p)
	if tk.Working.Hex() != "#e5c07b" || tk.Blocked.Hex() != "#e06c75" || tk.Done.Hex() != "#61afef" {
		t.Errorf("status = %s %s %s", tk.Working.Hex(), tk.Blocked.Hex(), tk.Done.Hex())
	}
	if !tk.Dark {
		t.Error("atom should be dark")
	}
}

const sampleTheme = `{
  "background": "#21252b", "foreground": "#abb2bf", "selection": "#323844",
  "ansi": ["#454c59","#e06c75","#98c379","#e5c07b","#61afef","#c678dd","#56b6c2","#abb2bf",
           "#767676","#e06c75","#98c379","#e5c07b","#61afef","#c678dd","#56b6c2","#abb2bf"],
  "colors": {"border": "#ff000080", "ansi3": "#010203", "accent": "#112233", "bogus": "#000000"}
}`

func TestLoadPaletteAndOverrides(t *testing.T) {
	p, err := LoadPalette([]byte(sampleTheme))
	if err != nil {
		t.Fatal(err)
	}
	atom, _ := Preset("atom-one-dark")
	if p.Background != atom.Background || p.ANSI != atom.ANSI || p.Status != nil {
		t.Errorf("palette mismatch: %+v", p)
	}
	tk := Resolve(p)
	if tk.Cursor != p.Foreground {
		t.Errorf("cursor default = %s", tk.Cursor.Hex())
	}
	if tk.Border.Hex() != "#ff000080" || tk.ANSI[3].Hex() != "#010203" || tk.Accent.Hex() != "#112233" {
		t.Errorf("overrides not applied: %s %s %s", tk.Border.Hex(), tk.ANSI[3].Hex(), tk.Accent.Hex())
	}
	if tk.Popover.Hex() == "" || tk.Surface != p.Background {
		t.Error("non-overridden tokens changed")
	}
}

func TestLoadPaletteErrors(t *testing.T) {
	ok := `"background":"#000000","foreground":"#ffffff","selection":"#111111"`
	ansi := `"ansi":["#000000","#000000","#000000","#000000","#000000","#000000","#000000","#000000","#000000","#000000","#000000","#000000","#000000","#000000","#000000","#000000"]`
	tests := map[string]string{
		"not json":      `nope`,
		"unknown field": `{` + ok + `,` + ansi + `,"extra":1}`,
		"short ansi":    `{` + ok + `,"ansi":["#000000"]}`,
		"bad colour":    `{"background":"red","foreground":"#ffffff","selection":"#111111",` + ansi + `}`,
		"alpha base":    `{"background":"#00000000","foreground":"#ffffff","selection":"#111111",` + ansi + `}`,
		"bad override":  `{` + ok + `,` + ansi + `,"colors":{"border":"zzz"}}`,
		"bad cursor":    `{` + ok + `,` + ansi + `,"cursor":"1"}`,
	}
	for name, in := range tests {
		if _, err := LoadPalette([]byte(in)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
	good := `{` + ok + `,` + ansi + `,"cursor":"#123456"}`
	p, err := LoadPalette([]byte(good))
	if err != nil || p.Cursor.Hex() != "#123456" {
		t.Errorf("cursor: %v %v", p.Cursor, err)
	}
}
