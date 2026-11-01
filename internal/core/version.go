package core

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Protocol constants this client was written against.
const (
	// Generation is the protocol generation spoken by this client.
	Generation = 1
	// MinVersion is the oldest core version that works with this client.
	MinVersion = "0.1.11"
)

// requiredMethods are the RPC methods the app relies on.
var requiredMethods = []string{
	"terminal.run", "terminal.list", "terminal.kill", "terminal.label",
	"terminal.attach", "terminal.keep", "doc.get", "doc.set", "events.subscribe",
}

// Errors reported by Check (test with errors.Is).
var (
	// ErrCoreTooOld means the core is older than MinVersion or generation 1.
	ErrCoreTooOld = errors.New("herdr-core is too old (run `hc update`)")
	// ErrCoreNewer means the core speaks a newer protocol generation.
	ErrCoreNewer = errors.New("herdr-core is newer than this app supports (update the app)")
	// ErrCoreMissing means the core lacks a method the app needs.
	ErrCoreMissing = errors.New("herdr-core lacks a required method")
)

// Check verifies that a core's hello is compatible with this client:
// generation 1, version >= MinVersion and every required method present.
func Check(h Hello) error {
	switch {
	case h.Generation > Generation:
		return fmt.Errorf("%w (generation %d)", ErrCoreNewer, h.Generation)
	case h.Generation < Generation:
		return fmt.Errorf("%w (generation %d)", ErrCoreTooOld, h.Generation)
	}
	v, ok := parseVersion(h.Version)
	if !ok {
		return fmt.Errorf("%w: unreadable version %q", ErrCoreTooOld, h.Version)
	}
	min, _ := parseVersion(MinVersion)
	if compareVersions(v, min) < 0 {
		return fmt.Errorf("%w (version %s, need >= %s)", ErrCoreTooOld, h.Version, MinVersion)
	}
	have := make(map[string]bool, len(h.Methods))
	for _, m := range h.Methods {
		have[m] = true
	}
	var missing []string
	for _, m := range requiredMethods {
		if !have[m] {
			missing = append(missing, m)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("%w: %s", ErrCoreMissing, strings.Join(missing, ", "))
	}
	return nil
}

// parseVersion reads up to three numeric components from a string such as
// "0.1.16", "hc 0.1.16" or "0.2.0-rc1".
func parseVersion(s string) ([3]int, bool) {
	var v [3]int
	s = strings.TrimPrefix(strings.TrimSpace(s), "hc ")
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	parts := strings.FieldsFunc(s, func(r rune) bool { return r == '.' || r == '-' || r == '+' })
	if len(parts) < 3 {
		return v, false
	}
	for i := 0; i < 3; i++ {
		n, err := strconv.Atoi(parts[i])
		if err != nil || n < 0 {
			return v, false
		}
		v[i] = n
	}
	return v, true
}

func compareVersions(a, b [3]int) int {
	for i := range a {
		switch {
		case a[i] < b[i]:
			return -1
		case a[i] > b[i]:
			return 1
		}
	}
	return 0
}
