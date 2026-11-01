package app

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// The dialog commands let mods and scripts talk to the person: a toast,
// a line of text, a yes or no, a choice from a list. Each answers when
// the person does. One dialog shows at a time; the others wait in line,
// and none opens over another overlay (the palette, the settings).

// queuedDialog is a dialog waiting for its turn, or showing.
type queuedDialog struct {
	open  func()
	close func() // takes it away, unanswered, when its caller goes
	shown bool
}

// overlayOpen reports whether a dialog, the palette or the settings show.
func (a *App) overlayOpen() bool {
	return a.palette.open || a.prompt != nil || a.confirm != nil || a.settings
}

// enqueueDialog adds a dialog to the line and returns the reply its
// answer resolves. open shows it with what resolves the reply; close takes
// it away when nobody waits for it any more.
func (a *App) enqueueDialog(open func(answer func(any)), close func()) *deferred {
	q := &queuedDialog{}
	var d *deferred
	d = a.newDeferred(maxWait, func() {
		if q.shown {
			close()
		} else {
			a.dialogs = slices.DeleteFunc(a.dialogs, func(x *queuedDialog) bool { return x == q })
		}
	})
	answer := func(v any) {
		a.settle(d)
		d.resolve(v, nil)
	}
	q.open = func() { open(answer) }
	q.close = close
	a.dialogs = append(a.dialogs, q)
	a.nextDialog()
	return d
}

// nextDialog shows the first dialog in line when nothing else shows.
func (a *App) nextDialog() {
	if len(a.dialogs) == 0 || a.overlayOpen() {
		return
	}
	q := a.dialogs[0]
	a.dialogs = a.dialogs[1:]
	q.shown = true
	q.open()
}

var cancelled = map[string]bool{"cancelled": true}

func (a *App) cmdAppNotify(v args) (any, error) {
	lv := levelInfo
	switch v.str("level") {
	case "", "info":
	case "error":
		lv = levelError
	default:
		return nil, fmt.Errorf("level is info or error")
	}
	a.notifyLevel(v.str("text"), lv)
	return nil, nil
}

func (a *App) cmdAppAsk(v args) (any, error) {
	title, value, placeholder, hint := v.str("title"), v.str("value"), v.str("placeholder"), v.str("hint")
	var p *promptState
	return a.enqueueDialog(func(answer func(any)) {
		a.askText(title, value, placeholder, hint, func(s string) { answer(map[string]string{"value": s}) })
		p = a.prompt
		p.cancel = func() { answer(cancelled) }
	}, func() {
		if a.prompt == p {
			a.prompt = nil
			a.focusReq = a.focus[a.activeTab[a.activeSpace]]
		}
	}), nil
}

func (a *App) cmdAppConfirm(v args) (any, error) {
	title, detail, yes := v.str("title"), v.str("detail"), v.str("yes")
	if yes == "" {
		yes = "OK"
	}
	var p *confirmState
	return a.enqueueDialog(func(answer func(any)) {
		p = &confirmState{title: title, detail: detail, yesLabel: yes, open: true,
			yes: func() { answer(map[string]bool{"ok": true}) },
			no:  func() { answer(map[string]bool{"ok": false}) }}
		a.confirm = p
	}, func() {
		if a.confirm == p {
			a.confirm = nil
			a.focusReq = a.focus[a.activeTab[a.activeSpace]]
		}
	}), nil
}

func (a *App) cmdAppPick(v args) (any, error) {
	items, err := pickItems(v["items"])
	if err != nil {
		return nil, err
	}
	title, placeholder := v.str("title"), v.str("placeholder")
	var pk *pickState
	return a.enqueueDialog(func(answer func(any)) {
		pk = &pickState{title: title, placeholder: placeholder, items: items, done: func(id string) {
			if id == "" {
				answer(cancelled)
				return
			}
			answer(map[string]string{"id": id})
		}}
		a.palette = paletteState{open: true, reveal: true, pick: pk}
	}, func() {
		if a.palette.pick == pk {
			a.palette = paletteState{}
			a.focusReq = a.focus[a.activeTab[a.activeSpace]]
		}
	}), nil
}

// pickItems reads app.pick's items: a list, or a JSON array in a string
// (from the command line), of objects {id, title, detail} or of strings,
// each its own id and title.
func pickItems(raw any) ([]pickItem, error) {
	if s, ok := raw.(string); ok {
		if err := json.Unmarshal([]byte(s), &raw); err != nil {
			return nil, fmt.Errorf("items is a JSON array: %w", err)
		}
	}
	list, ok := raw.([]any)
	if !ok || len(list) == 0 {
		return nil, fmt.Errorf("items is a list of {id, title, detail} or of strings")
	}
	out := make([]pickItem, 0, len(list))
	seen := map[string]bool{}
	for i, e := range list {
		var it pickItem
		switch x := e.(type) {
		case string:
			it = pickItem{id: x, title: x}
		case map[string]any:
			str := func(k string) string { s, _ := x[k].(string); return strings.TrimSpace(s) }
			it = pickItem{id: str("id"), title: str("title"), detail: str("detail")}
			if it.title == "" {
				it.title = it.id
			}
		default:
			return nil, fmt.Errorf("items[%d] is not an object or a string", i)
		}
		if it.id == "" {
			return nil, fmt.Errorf("items[%d] has no id", i)
		}
		if seen[it.id] {
			return nil, fmt.Errorf("items[%d]: id %q is there twice", i, it.id)
		}
		seen[it.id] = true
		out = append(out, it)
	}
	return out, nil
}
