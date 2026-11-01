package app

import (
	"reflect"
	"testing"

	"github.com/egoist/mygo/ui"
)

// replyNow is a deferred's reply if it came, else ok is false.
func replyNow(d *deferred) (v any, err error, ok bool) {
	select {
	case r := <-d.ch:
		d.ch <- r // keep it for a later look
		return r.v, r.err, true
	default:
		return nil, nil, false
	}
}

func dialogCmd(t *testing.T, a *App, name string, v args) *deferred {
	t.Helper()
	r, err := a.do(name, v)
	if err != nil {
		t.Fatal(err)
	}
	d, ok := r.(*deferred)
	if !ok {
		t.Fatalf("%s answered at once: %v", name, r)
	}
	return d
}

func wantReply(t *testing.T, d *deferred, want any) {
	t.Helper()
	v, err, ok := replyNow(d)
	if !ok {
		t.Fatal("no reply yet")
	}
	if err != nil || !reflect.DeepEqual(v, want) {
		t.Fatalf("reply %#v, %v; want %#v", v, err, want)
	}
}

func TestAppAsk(t *testing.T) {
	a, _ := uxApp(t)
	tt := uxTester(a)
	d := dialogCmd(t, a, "app.ask", args{"title": "Branch name", "placeholder": "feature/…"})
	settle(tt)
	if !tt.HasText("Branch name") {
		t.Fatalf("texts %q", tt.Texts())
	}
	tt.Type("fix-42")
	tt.Key(0, ui.KeyEnter)
	settle(tt)
	wantReply(t, d, map[string]string{"value": "fix-42"})
	if a.prompt != nil || len(a.waiting) != 0 {
		t.Fatal("left over")
	}

	d = dialogCmd(t, a, "app.ask", args{"title": "Again"})
	settle(tt)
	tt.Key(0, ui.KeyEscape)
	settle(tt)
	wantReply(t, d, cancelled)
}

func TestAppConfirm(t *testing.T) {
	a, _ := uxApp(t)
	tt := uxTester(a)
	d := dialogCmd(t, a, "app.confirm", args{"title": "Push the branch?", "yes": "Push"})
	settle(tt)
	if !tt.HasText("Push") {
		t.Fatalf("texts %q", tt.Texts())
	}
	tt.Key(0, ui.KeyEnter)
	settle(tt)
	wantReply(t, d, map[string]bool{"ok": true})

	d = dialogCmd(t, a, "app.confirm", args{"title": "Push the branch?"})
	settle(tt)
	mustClick(t, tt, "Cancel")
	wantReply(t, d, map[string]bool{"ok": false})
}

func TestAppPick(t *testing.T) {
	a, _ := uxApp(t)
	tt := uxTester(a)
	items := []any{
		map[string]any{"id": "main", "title": "main", "detail": "default branch"},
		map[string]any{"id": "fix", "title": "fix-42"},
		"release",
	}
	d := dialogCmd(t, a, "app.pick", args{"title": "Switch branch", "items": items})
	settle(tt)
	if !tt.HasText("Switch branch") || !tt.HasText("default branch") || tt.HasText("Close space") {
		t.Fatalf("texts %q", tt.Texts())
	}
	tt.Type("fix")
	tt.Key(0, ui.KeyEnter)
	settle(tt)
	wantReply(t, d, map[string]string{"id": "fix"})
	if a.palette.open || a.palette.pick != nil {
		t.Fatal("the picker stays")
	}

	// From the command line, items come as a JSON string.
	d = dialogCmd(t, a, "app.pick", args{"items": `["a","b"]`})
	settle(tt)
	tt.Key(0, ui.KeyEscape)
	settle(tt)
	wantReply(t, d, cancelled)

	for _, bad := range []any{"[]", `[{"title":"no id"}]`, `["a","a"]`, `{"a":1}`, []any{3.0}} {
		if _, err := a.do("app.pick", args{"items": bad}); err == nil {
			t.Errorf("items %v taken", bad)
		}
	}
}

func TestDialogsWaitTheirTurn(t *testing.T) {
	a, _ := uxApp(t)
	tt := uxTester(a)
	a.openSettings("")
	settle(tt)
	first := dialogCmd(t, a, "app.ask", args{"title": "First"})
	second := dialogCmd(t, a, "app.confirm", args{"title": "Second"})
	settle(tt)
	if a.prompt != nil || a.confirm != nil {
		t.Fatal("a dialog opened over the settings")
	}
	a.settings = false
	settle(tt)
	if a.prompt == nil || a.prompt.title != "First" || a.confirm != nil {
		t.Fatalf("prompt %+v confirm %+v", a.prompt, a.confirm)
	}
	tt.Key(0, ui.KeyEscape)
	settle(tt)
	wantReply(t, first, cancelled)
	if a.confirm == nil || a.confirm.title != "Second" {
		t.Fatal("the second did not follow")
	}
	tt.Key(0, ui.KeyEnter)
	settle(tt)
	wantReply(t, second, map[string]bool{"ok": true})
}

func TestADialogWhoseCallerWentCloses(t *testing.T) {
	a, _ := uxApp(t)
	tt := uxTester(a)
	shown := dialogCmd(t, a, "app.pick", args{"items": []any{"x"}})
	queued := dialogCmd(t, a, "app.ask", args{"title": "Later"})
	settle(tt)
	a.cancelDeferred(queued, errGone)
	a.cancelDeferred(shown, errGone)
	settle(tt)
	if a.palette.open || a.prompt != nil || len(a.dialogs) != 0 || len(a.waiting) != 0 {
		t.Fatalf("palette %v prompt %v dialogs %d waiting %d", a.palette.open, a.prompt, len(a.dialogs), len(a.waiting))
	}
	if _, err, _ := replyNow(shown); err != errGone {
		t.Fatalf("err %v", err)
	}
}

func TestAppNotify(t *testing.T) {
	a, _ := testApp(t)
	if _, err := a.do("app.notify", args{"text": "Pushed", "level": "error"}); err != nil {
		t.Fatal(err)
	}
	if a.toasts[0].level != levelError || a.toasts[0].text != "Pushed" {
		t.Fatalf("%+v", a.toasts)
	}
	if _, err := a.do("app.notify", args{"text": "x", "level": "loud"}); err == nil {
		t.Fatal("a bad level was taken")
	}
	if _, err := a.do("app.notify", nil); err == nil {
		t.Fatal("no text was taken")
	}
}
