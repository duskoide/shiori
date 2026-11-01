package layout

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestGoldenRoundTrip(t *testing.T) {
	raw, err := os.ReadFile("testdata/herdr-app.json")
	if err != nil {
		t.Fatal(err)
	}
	d, err := Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	enc, err := d.Encode()
	if err != nil {
		t.Fatal(err)
	}
	// Semantic JSON equality with the original (nothing lost, nothing added).
	var want, got any
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(enc, &got); err != nil {
		t.Fatal(err)
	}
	if !jsonEq(want, got) {
		t.Fatalf("round trip differs:\n%s", enc)
	}
	d2, err := Decode(enc)
	if err != nil {
		t.Fatal(err)
	}
	if !Equal(d, d2) || !Equal(d, d.Clone()) {
		t.Fatal("Equal/Clone mismatch")
	}
	enc2, _ := d2.Encode()
	if !bytes.Equal(enc, enc2) {
		t.Fatal("encoding not deterministic")
	}
	// Spot-check known and unknown fields at each level.
	sp := d.Spaces[0]
	if string(sp.Extra["color"]) != `"teal"` || string(sp.Tabs[0].Extra["icon"]) != `"star"` {
		t.Fatal("space/tab extra lost")
	}
	root := sp.Tabs[0].Root
	if string(root.Extra["gap"]) != "2" || root.Children[1].Size.Px == nil || *root.Children[1].Size.Px != 268 {
		t.Fatal("node extra / px lost")
	}
	if string(root.Children[0].Content.Extra["cwd_hint"]) != `"/tmp"` {
		t.Fatal("content extra lost")
	}
	if m := root.Children[1].Children[0].Content; m.Mod != "clock" || !strings.Contains(string(m.Props), "fmt") {
		t.Fatal("mod props lost")
	}
	if len(sp.Tabs[0].Panes()) != 6 {
		t.Fatalf("panes = %d", len(sp.Tabs[0].Panes()))
	}
	if string(root.Children[2].Extra["active"]) != `"s2"` {
		t.Fatal("stack active lost")
	}
}

func jsonEq(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Equal(x, y)
}

func TestDecodeEmpty(t *testing.T) {
	for _, in := range []string{"", "  ", "null"} {
		d, err := Decode([]byte(in))
		if err != nil || len(d.Spaces) != 0 {
			t.Fatalf("%q: %v %v", in, d, err)
		}
	}
	if _, err := Decode([]byte("[1]")); err == nil {
		t.Fatal("want error")
	}
	if _, err := Decode([]byte(`{"spaces":[{"tabs":5}]}`)); err == nil {
		t.Fatal("want error")
	}
}

func TestEncodeSortedExtra(t *testing.T) {
	d, _ := Decode([]byte(`{"z":1,"a":2,"spaces":[]}`))
	b, _ := d.Encode()
	if string(b) != `{"a":2,"spaces":[],"z":1}` {
		t.Fatal(string(b))
	}
}

func TestEqualSemantic(t *testing.T) {
	a, _ := Decode([]byte(`{"spaces":[{"id":"s","tabs":[{"id":"t","root":{"id":"t","type":"row","size":{"px":268.0},"x":1.0}}]}]}`))
	b, _ := Decode([]byte(`{"spaces":[{"tabs":[{"root":{"x":1,"size":{"px":268},"type":"row","id":"t"},"id":"t"}],"id":"s"}]}`))
	if !Equal(a, b) {
		t.Fatal("want equal")
	}
	b.Spaces[0].Name = "n"
	if Equal(a, b) {
		t.Fatal("want different")
	}
	if !Equal(nil, &Document{}) {
		t.Fatal("nil == empty")
	}
}

func TestCloneIndependent(t *testing.T) {
	raw, _ := os.ReadFile("testdata/herdr-app.json")
	d, _ := Decode(raw)
	c := d.Clone()
	c.Spaces[0].Tabs[0].Root.Children[0].Content.Terminal = "x"
	c.Spaces[0].Extra["color"] = json.RawMessage(`"red"`)
	*c.Spaces[0].Tabs[0].Root.Children[1].Size.Px = 1
	if Equal(d, c) || d.Spaces[0].Tabs[0].Root.Children[0].Content.Terminal != "t27" ||
		*d.Spaces[0].Tabs[0].Root.Children[1].Size.Px != 268 || string(d.Spaces[0].Extra["color"]) != `"teal"` {
		t.Fatal("clone shares state")
	}
}

func TestNewID(t *testing.T) {
	a, b := NewID("sp"), NewID("sp")
	if len(a) != 34 || !strings.HasPrefix(a, "sp") || a == b || a != strings.ToLower(a) {
		t.Fatal(a, b)
	}
}
