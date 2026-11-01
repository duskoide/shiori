package agent

import "testing"

func TestDerive(t *testing.T) {
	seen := func(v string) map[string]string { return map[string]string{"herdr.seen": v} }
	tests := []struct {
		name string
		in   Input
		want Word
	}{
		{"not an agent", Input{}, None},
		{"not an agent ignores labels", Input{Turn: 9, Labels: seen("0")}, None},
		{"working", Input{State: "working", Turn: 5, Labels: seen("0")}, Working},
		{"blocked", Input{State: "blocked"}, Blocked},
		{"error", Input{State: "error", LocalSeen: true}, Blocked},
		{"unseen by label", Input{State: "done", Turn: 7, Labels: seen("6")}, Done},
		{"seen by label", Input{State: "done", Turn: 7, Labels: seen("7")}, Idle},
		{"label ahead of turn", Input{State: "idle", Turn: 3, Labels: seen("9")}, Idle},
		{"label beats local seen", Input{State: "idle", Turn: 2, Labels: seen("1"), LocalSeen: true}, Done},
		{"label zero turn zero", Input{State: "idle", Labels: seen("0")}, Idle},
		{"bad label falls back unseen", Input{State: "done", Turn: 1, Labels: seen("x")}, Done},
		{"bad label falls back seen", Input{State: "done", Turn: 1, Labels: seen("-1"), LocalSeen: true}, Idle},
		{"empty label falls back", Input{State: "idle", Labels: seen("")}, Done},
		{"no label unseen", Input{State: "idle"}, Done},
		{"no label seen", Input{State: "done", LocalSeen: true}, Idle},
		{"unknown state", Input{State: "weird", LocalSeen: true}, Idle},
		{"huge turn", Input{State: "done", Turn: 1 << 63, Labels: seen("18446744073709551615")}, Idle},
	}
	for _, tt := range tests {
		if got := Derive(tt.in); got != tt.want {
			t.Errorf("%s: got %v want %v", tt.name, got, tt.want)
		}
	}
}

func TestOrderAndRollup(t *testing.T) {
	if !(None < Idle && Idle < Working && Working < Done && Done < Blocked) {
		t.Fatal("order")
	}
	tests := []struct {
		in   []Word
		want Word
	}{
		{nil, None},
		{[]Word{Idle, None}, Idle},
		{[]Word{Idle, Working}, Working},
		{[]Word{Working, Done, Idle}, Done},
		{[]Word{Done, Blocked, Working}, Blocked},
	}
	for _, tt := range tests {
		if got := Rollup(tt.in...); got != tt.want {
			t.Errorf("%v: got %v want %v", tt.in, got, tt.want)
		}
	}
}

func TestString(t *testing.T) {
	for w, s := range map[Word]string{None: "none", Idle: "idle", Working: "working", Done: "done", Blocked: "blocked", Word(9): "Word(9)"} {
		if w.String() != s {
			t.Errorf("%d: %q", int(w), w.String())
		}
	}
}

func TestLabelsHelpers(t *testing.T) {
	if got := SeenLabel(42); len(got) != 1 || got["herdr.seen"] != "42" {
		t.Fatal(got)
	}
	if got := SeenLabel(0); got["herdr.seen"] != "0" {
		t.Fatal(got)
	}
	for labels, want := range map[string]bool{"1": true, "yes": true, "": false} {
		if Pinned(map[string]string{"herdr.pinned": labels}) != want {
			t.Errorf("pinned %q", labels)
		}
	}
	if Pinned(nil) || Pinned(map[string]string{"other": "1"}) {
		t.Fatal("unpinned")
	}
	// Round trip with Derive.
	if Derive(Input{State: "done", Turn: 3, Labels: SeenLabel(3)}) != Idle {
		t.Fatal("seen label round trip")
	}
}
