// Package agent derives a display status word from a terminal's reported
// agent status and the shared "seen" label (see herdr-app docs/labels.md).
package agent

import "strconv"

// Word is a status word, ordered by urgency.
type Word int

// Status words, least to most urgent.
const (
	None Word = iota
	Idle
	Working
	Done
	Blocked
)

// Label keys shared between clients.
const (
	seenKey   = "herdr.seen"
	pinnedKey = "herdr.pinned"
)

func (w Word) String() string {
	switch w {
	case None:
		return "none"
	case Idle:
		return "idle"
	case Working:
		return "working"
	case Done:
		return "done"
	case Blocked:
		return "blocked"
	}
	return "Word(" + strconv.Itoa(int(w)) + ")"
}

// Input is what Derive needs about one terminal.
type Input struct {
	State     string // status.state; empty when the terminal is not an agent
	Turn      uint64
	Labels    map[string]string
	LocalSeen bool // this client's own seen flag, used when no herdr.seen label
}

// Derive computes the status word. Working and blocked/error map directly;
// any other state is Done when unseen, using the shared herdr.seen label when
// it parses (Done iff Turn > seen) and LocalSeen otherwise.
func Derive(in Input) Word {
	switch in.State {
	case "":
		return None
	case "working":
		return Working
	case "blocked", "error":
		return Blocked
	}
	if s, ok := in.Labels[seenKey]; ok {
		if seen, err := strconv.ParseUint(s, 10, 64); err == nil {
			if in.Turn > seen {
				return Done
			}
			return Idle
		}
	}
	if !in.LocalSeen {
		return Done
	}
	return Idle
}

// Rollup returns the most urgent of words (None when empty).
func Rollup(words ...Word) Word {
	out := None
	for _, w := range words {
		out = max(out, w)
	}
	return out
}

// SeenLabel returns the label map recording that turn has been seen.
func SeenLabel(turn uint64) map[string]string {
	return map[string]string{seenKey: strconv.FormatUint(turn, 10)}
}

// Pinned reports whether the terminal is pinned (herdr.pinned non-empty).
func Pinned(labels map[string]string) bool { return labels[pinnedKey] != "" }
