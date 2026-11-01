package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/duskoide/shiori/internal/update"
)

// releaseServer answers GitHub's latest-release request with tag, or 404
// when tag is "".
func releaseServer(t *testing.T, tag *string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if *tag == "" || r.URL.Path != "/repos/o/r/releases/latest" {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"tag_name": *tag, "html_url": "https://example.com/" + *tag})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func withVersion(t *testing.T, v string) {
	old := Version
	Version = v
	t.Cleanup(func() { Version = old })
}

func checkNow(t *testing.T, a *App, manual bool) (any, error) {
	t.Helper()
	d := a.checkUpdates(manual)
	var v any
	var err error
	pumpUntilTrue(t, a, "the check", func() bool {
		var ok bool
		v, err, ok = replyNow(d)
		return ok
	})
	return v, err
}

func TestUpdateCheckTellsOfANewVersionOnce(t *testing.T) {
	withVersion(t, "0.1.0")
	tag := "v0.2.0"
	srv := releaseServer(t, &tag)
	a, _ := uxApp(t)
	a.updOpts = update.Options{API: srv.URL, Repo: "o/r"}
	v, err := checkNow(t, a, false)
	if err != nil || v.(map[string]any)["newer"] != true {
		t.Fatalf("%v %v", v, err)
	}
	if len(a.toasts) != 1 || !strings.Contains(a.toasts[0].text, "Shiori 0.2.0 is out") {
		t.Fatalf("toasts %v", a.toasts)
	}
	checkNow(t, a, false)
	if len(a.toasts) != 1 {
		t.Fatal("told twice")
	}
	// Remembered across windows.
	if s := loadUpdateState(a.home); s.Latest != "0.2.0" || s.Notified != "0.2.0" || s.CheckedAt.IsZero() {
		t.Fatalf("state %+v", s)
	}
	tt := uxTester(a)
	if !tt.HasText("Update to 0.2.0") {
		t.Fatalf("texts %q", tt.Texts())
	}
	mustClick(t, tt, "Update to 0.2.0")
	if !a.settings || a.set.section != "about" || !tt.HasText("Shiori 0.2.0 is out") {
		t.Fatalf("section %q texts %q", a.set.section, tt.Texts())
	}
}

func TestUpdateCheckUpToDateAndOffline(t *testing.T) {
	withVersion(t, "0.2.0")
	tag := "v0.2.0"
	srv := releaseServer(t, &tag)
	a, _ := uxApp(t)
	a.updOpts = update.Options{API: srv.URL, Repo: "o/r"}
	if v, _ := checkNow(t, a, false); v.(map[string]any)["newer"] != false || len(a.toasts) != 0 {
		t.Fatalf("%v toasts %v", v, a.toasts)
	}
	checkNow(t, a, true)
	if len(a.toasts) != 1 || a.toasts[0].text != "Shiori 0.2.0 is up to date" {
		t.Fatalf("toasts %v", a.toasts)
	}
	tt := uxTester(a)
	if tt.HasText("Update to 0.2.0") {
		t.Fatal("an update shows for the version running")
	}
	a.openSettings("updates")
	settle(tt)
	if !tt.HasText("Shiori 0.2.0 is up to date") {
		t.Fatalf("texts %q", tt.Texts())
	}

	// Offline: a quiet look says nothing, the page says what happened.
	a.updOpts = update.Options{API: "http://127.0.0.1:1", Repo: "o/r"}
	a.toasts = nil
	if _, err := checkNow(t, a, false); err == nil || len(a.toasts) != 0 {
		t.Fatalf("err %v toasts %v", err, a.toasts)
	}
	settle(tt)
	if !tt.HasText("Could not look for a new version") {
		t.Fatalf("texts %q", tt.Texts())
	}
	tag = ""
	a.updOpts = update.Options{API: srv.URL, Repo: "o/r"}
	if _, err := checkNow(t, a, true); err == nil || err.Error() != "no release found" || len(a.toasts) != 1 {
		t.Fatalf("err %v toasts %v", err, a.toasts)
	}
}

func TestUpdateChecksTurnOff(t *testing.T) {
	withVersion(t, "0.1.0")
	a, _ := uxApp(t)
	a.scheduleUpdateCheck()
	if a.updTimer == nil {
		t.Fatal("no look is due")
	}
	tt := uxTester(a)
	a.openSettings("about")
	settle(tt)
	mustClick(t, tt, "Look for updates")
	if on, _ := a.updateChecksOn(); on || a.updTimer != nil {
		t.Fatal("still on")
	}
	b, _ := os.ReadFile(a.home + "/settings.json")
	if !strings.Contains(string(b), `"update_check": false`) {
		t.Fatalf("settings.json %s", b)
	}
	mustClick(t, tt, "Look for new versions")
	if on, _ := a.updateChecksOn(); !on || a.updTimer == nil || a.cfg.UpdateCheck != nil {
		t.Fatal("not on again")
	}
	t.Setenv("SHIORI_NO_UPDATE_CHECK", "1")
	a.scheduleUpdateCheck()
	settle(tt)
	if a.updTimer != nil || !tt.HasText("Off: SHIORI_NO_UPDATE_CHECK is set in the environment.") {
		t.Fatal("the environment did not turn it off")
	}
}

func TestDevelopmentBuildsDoNotLook(t *testing.T) {
	withVersion(t, "0.1.0-dev")
	a, _ := uxApp(t)
	a.scheduleUpdateCheck()
	if a.updTimer != nil {
		t.Fatal("a development build looks")
	}
	a.upd.Latest = "9.9.9"
	if _, ok := a.newerRelease(); ok {
		t.Fatal("a development build is told to update")
	}
}

func TestTheNextLookWaitsADay(t *testing.T) {
	withVersion(t, "0.1.0")
	a, _ := uxApp(t)
	NoteUpdateCheck(a.home, update.Release{Tag: "v0.1.0", URL: "u"})
	a.upd = loadUpdateState(a.home)
	if time.Since(a.upd.CheckedAt) > time.Minute || a.upd.Latest != "0.1.0" {
		t.Fatalf("state %+v", a.upd)
	}
	if _, err := a.do("app.update", nil); err == nil || !strings.Contains(err.Error(), "go run") {
		t.Fatalf("update from a test binary: %v", err)
	}
}
