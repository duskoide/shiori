package app

import (
	"fmt"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/duskoide/shiori/internal/core"
)

// The Machines page of the settings: the other machines herdr-core reaches,
// what state each is in, and what can be done with them; and adding one.
// Machines live in herdr-core, not in Shiori's files, so this page is
// their only control.

// machineTrouble is what is wrong with the machines, for the page's mark
// in the settings' list, or "".
func (a *App) machineTrouble() string {
	for _, m := range a.machines {
		if _, bad := machineWord(m); bad {
			return m.Name + " needs you"
		}
	}
	return ""
}

func (a *App) machinesPage(c *ui.Context, k *colors) {
	settingsGroup(c, k, "Machines", "Other machines herdr-core reaches over SSH. Their spaces show in the sidebar under their names, and their terminals run there. Each needs herdr-core (hc); Shiori there is optional.", func() {
		switch {
		case !a.connected:
			machinesNote(c, k, "Not connected to herdr-core", "Machines show once the window reaches the core.")
		case len(a.machines) == 0:
			machinesNote(c, k, "No other machines yet", "Add one below. It needs herdr-core (hc) there.")
		}
		for _, m := range a.machines {
			a.machineCard(c, k, m)
		}
	})
	a.addMachineForm(c, k)
	fileNote(c, k, "hc machine ls  (machines are herdr-core's; added with --temp, they stay out of Shiori)")
}

func machinesNote(c *ui.Context, k *colors, title, detail string) {
	ui.Column(c).Padding(22, 16).Gap(6).AlignItems(ui.Center).Radius(10).Border(1, k.border).Shrink(0).Children(func() {
		ui.Icon(c, glyph("server")).Size(24, 24).TextColor(k.dim)
		ui.Text(c, title).FontSize(13).FontWeight(600).TextColor(k.foreground)
		ui.Text(c, detail).FontSize(12).TextColor(k.muted)
	})
}

func (a *App) machineCard(c *ui.Context, k *colors, m core.Machine) {
	name := m.Name
	op := a.mach.ops[name]
	busy := op != nil && op.busy != ""
	word, bad := machineWord(m)
	ui.Column(c.Key("machine:"+name)).Padding(14).Gap(10).Radius(10).Background(k.raised).Border(1, k.border).Shrink(0).Label("Machine " + name).Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, glyph("server")).Size(16, 16).TextColor(k.muted).Shrink(0)
			ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Text(c, name).FontSize(13.5).FontWeight(600).TextColor(k.foreground).SingleLine().Ellipsis("…").MinWidth(0).Shrink(1)
					if m.Version != "" {
						ui.Text(c, "hc "+m.Version).FontSize(11.5).TextColor(k.dim).SingleLine()
					}
					col := machineDot(k, m)
					ui.Box(c).Padding(1, 7).Radius(9).Background(col.Alpha(0.14)).Shrink(0).Children(func() {
						ui.Text(c, word).FontSize(11).FontWeight(600).TextColor(col).SingleLine()
					})
				})
				var where []string
				for _, w := range []string{m.Target, m.Host} {
					if w != "" && !slices.Contains(where, w) {
						where = append(where, w)
					}
				}
				if len(where) > 0 {
					ui.Text(c, strings.Join(where, " · ")).FontSize(11.5).TextColor(k.dim).Font("monospace").SingleLine().Ellipsis("…")
				}
			})
		})
		if m.Why != "" && !m.Usable() {
			ui.Text(c, m.Why).FontSize(12).TextColor(map[bool]ui.Color{true: k.blocked, false: k.muted}[bad]).LineHeight(1.35)
		}
		switch {
		case a.mach.renaming == name:
			a.renameMachineRow(c, k, name)
		case a.mach.removing == name:
			ui.Row(c).Padding(10, 12).Gap(10).Radius(8).Background(k.background).Border(1, k.blocked.Alpha(0.5)).AlignItems(ui.Center).Children(func() {
				ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
					ui.Text(c, "Remove "+name+"?").FontSize(13).FontWeight(600).TextColor(k.foreground)
					ui.Text(c, "Its terminals keep running there; its spaces leave the sidebar.").FontSize(12).TextColor(k.muted)
				})
				if ui.Button(c, "Cancel").Clicked() {
					a.mach.removing = ""
				}
				if ui.PrimaryButton(c, "Remove machine").Label("Remove " + name).Clicked() {
					a.mach.removing = ""
					a.post(func() { a.do("machine.remove", args{"name": name}) })
				}
			})
		default:
			ui.Row(c).Gap(8).Wrap().AlignItems(ui.Center).Children(func() {
				if m.State == core.MachineNeedsLogin {
					if ui.PrimaryButton(c, "Log in").Label("Log in to " + name).Tooltip("hc machine login " + name + ", in a tab").Clicked() {
						a.post(func() { a.do("machine.login", args{"name": name}) })
					}
				}
				if m.State == core.MachineNeedsUpdate || a.olderCore(m) {
					if ui.Button(c, "Update").Label("Update " + name).Tooltip("Put this machine's herdr-core version on it").Disabled(busy).Clicked() {
						a.post(func() { a.do("machine.update", args{"name": name}) })
					}
				}
				if ui.Button(c, "Rename").Label("Rename " + name).Disabled(busy).Clicked() {
					a.mach.renaming, a.mach.newName, a.mach.removing = name, name, ""
				}
				if ui.Button(c, "Remove").Label("Remove machine " + name).Disabled(busy).Clicked() {
					a.mach.removing, a.mach.renaming = name, ""
				}
				if busy {
					ui.Text(c, map[string]string{"update": "Updating…", "rename": "Renaming…", "rm": "Removing…"}[op.busy]).FontSize(12).TextColor(k.muted)
				}
			})
		}
		if op != nil && !busy && op.msg != "" {
			ui.Text(c, op.msg).FontSize(12).TextColor(map[bool]ui.Color{true: k.done, false: k.blocked}[op.ok]).LineHeight(1.35)
		}
	})
}

func (a *App) renameMachineRow(c *ui.Context, k *colors, name string) {
	ui.Column(c).Gap(6).Children(func() {
		ui.Text(c, "New name (addresses with the old name stop working)").FontSize(12).TextColor(k.dim)
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			in := ui.TextInput(c, &a.mach.newName).Grow(1).Label("New name of " + name).AutoFocus()
			to := strings.TrimSpace(a.mach.newName)
			save := ui.PrimaryButton(c, "Save").Label("Save the name of " + name).Disabled(to == "" || to == name)
			if ui.Button(c, "Cancel").Clicked() {
				a.mach.renaming = ""
			}
			if (save.Clicked() || in.Submitted()) && to != "" && to != name {
				a.mach.renaming = ""
				a.post(func() {
					if _, err := a.do("machine.rename", args{"name": name, "to": to}); err != nil {
						op := a.machineOp(name)
						op.msg, op.ok = err.Error(), false
					}
				})
			}
		})
	})
}

func (a *App) addMachineForm(c *ui.Context, k *colors) {
	settingsGroup(c, k, "Add a machine", "How SSH reaches it: user@host, or a host of your ~/.ssh/config. Your SSH keys are used; when it needs a password, a host key confirmed or herdr-core installed there, it goes on in a tab where you answer.", func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			in := ui.TextInput(c, &a.mach.target).Grow(1).Placeholder("user@host").Label("Machine to add").Font("monospace")
			target := strings.TrimSpace(a.mach.target)
			add := ui.PrimaryButton(c, "Add").Label("Add machine").Disabled(target == "" || !a.connected)
			if (add.Clicked() || in.Submitted()) && target != "" {
				a.post(func() { a.addMachine(target) })
			}
		})
		var hosts []string
		for _, h := range sshHosts() {
			if !a.added(h) {
				hosts = append(hosts, h)
			}
		}
		if len(hosts) > 0 {
			ui.Text(c, "From ~/.ssh/config").FontSize(11.5).FontWeight(600).TextColor(k.dim)
			ui.Row(c).Gap(6).Wrap().Children(func() {
				for _, h := range hosts {
					adding := a.mach.ops[h] != nil && a.mach.ops[h].busy != ""
					if ui.Button(c.Key("host:"+h), h).Label("Add " + h).Disabled(adding || !a.connected).Clicked() {
						a.post(func() { a.addMachine(h) })
					}
				}
			})
		}
		for target, op := range a.mach.ops {
			if _, known := a.machine(target); known || op.what != "add" || (op.busy == "" && op.msg == "") {
				continue
			}
			msg := op.msg
			col := map[bool]ui.Color{true: k.muted, false: k.blocked}[op.ok]
			if op.busy != "" {
				msg, col = fmt.Sprintf("Adding %s…", target), k.muted
			}
			ui.Text(c.Key("addop:"+target), msg).FontSize(12).TextColor(col).LineHeight(1.35)
		}
	})
}

// addMachine runs machine.add, keeping its failure for the page.
func (a *App) addMachine(target string) {
	if _, err := a.do("machine.add", args{"target": target}); err != nil {
		op := a.machineOp(target)
		op.what, op.msg, op.ok = "add", err.Error(), false
	}
}
