package app

import (
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/duskoide/shiori/internal/update"
)

// aboutPage is the settings' About page: the versions, and the look for
// new ones.
func (a *App) aboutPage(c *ui.Context, k *colors) {
	settingsGroup(c, k, "Shiori", "", func() {
		detail := "A desktop app for terminals and coding agents."
		if update.Development(Version) {
			detail = "A development build: it has no release to update to."
		}
		settingRow(c, k, "Version "+Version, detail, func() {})
		core := "Not connected"
		if a.client != nil {
			core = a.client.Hello().Version
		}
		settingRow(c, k, "herdr-core", core, func() {})
	})

	on, why := a.updateChecksOn()
	settingsGroup(c, k, "Updates", "Once a day Shiori asks GitHub for its latest release. The request carries nothing about you or this computer, and nothing is installed without you.", func() {
		envOff := why == "SHIORI_NO_UPDATE_CHECK is set"
		overSwitch := false
		row := settingRow(c, k, "Look for new versions", map[bool]string{true: "Off: SHIORI_NO_UPDATE_CHECK is set in the environment.", false: "When one is out, it shows in the status bar."}[envOff], func() {
			v := on
			sw := ui.Switch(c, &v).Label("Look for updates").Disabled(envOff)
			overSwitch = sw.Hovered()
			if v != on {
				a.post(func() { a.setUpdateChecks(v) })
			}
		})
		if !envOff {
			row.Cursor(ui.CursorPointer).Label("Look for new versions")
			if row.Clicked() && !overSwitch {
				a.post(func() { a.setUpdateChecks(!on) })
			}
		}
		fileNote(c, k, `settings.json  "update_check"`)

		ui.Column(c).Padding(12, 14).Gap(10).Radius(10).Background(k.raised).Border(1, k.border).Children(func() {
			title, detail, col := a.updateStatus()
			ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
				if a.updChecking {
					ui.Spinner(c).Size(14, 14).TextColor(k.muted)
				}
				ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
					ui.Text(c, title).FontSize(ts(13)).FontWeight(600).TextColor(col)
					if detail != "" {
						ui.Text(c, detail).FontSize(ts(12)).TextColor(k.muted).LineHeight(1.35)
					}
				})
			})
			ui.Row(c).Gap(8).Wrap().Children(func() {
				newer, isNewer := a.newerRelease()
				if isNewer {
					if ui.PrimaryButton(c, "Update to "+newer).Tooltip("Runs shiori update in a new tab").Clicked() {
						a.post(func() {
							a.settings = false
							if _, err := a.do("app.update", nil); err != nil {
								a.notifyLevel(err.Error(), levelError)
							}
						})
					}
				}
				if a.upd.URL != "" {
					if ui.Button(c, map[bool]string{true: "What's new", false: "Release notes"}[isNewer]).Clicked() {
						c.OpenURL(a.upd.URL)
					}
				}
				if ui.Button(c, "Check now").Disabled(a.updChecking).Clicked() {
					a.post(func() { a.checkUpdates(true) })
				}
			})
		})
		ui.Text(c, "Installed with a package manager? Update with it instead. After an update, close Shiori and open it again.").
			FontSize(ts(11.5)).TextColor(k.dim).LineHeight(1.35)
	})
}

// updateStatus is what the last look found, for the About page.
func (a *App) updateStatus() (title, detail string, col ui.Color) {
	k := a.k
	checked := ""
	if !a.upd.CheckedAt.IsZero() {
		checked = "Looked " + since(a.upd.CheckedAt) + " ago"
		if time.Since(a.upd.CheckedAt) < time.Minute {
			checked = "Looked just now"
		}
	}
	switch {
	case a.updChecking:
		return "Looking for a new version…", "", k.muted
	case a.upd.Err != "":
		return "Could not look for a new version", a.upd.Err + ". " + checked + ".", k.blocked
	case a.upd.CheckedAt.IsZero():
		return "Not looked yet", "", k.muted
	}
	if v, ok := a.newerRelease(); ok {
		return "Shiori " + v + " is out", "This is " + Version + ". " + checked + ".", k.accent
	}
	return "Shiori " + Version + " is up to date", "The latest release is " + a.upd.Latest + ". " + checked + ".", k.foreground
}
