package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/duskoide/shiori/internal/changes"
	"github.com/duskoide/shiori/internal/layout"
)

// The changes panel, on the right of the panes, lists the files being
// changed in the active space's folder: in a git work tree what differs
// from the last commit, elsewhere what was modified in the last day, most
// recent first. It follows the space's folder, not the focused pane, and
// watches only while it shows.

const (
	changesWidth    = 300
	changesMin      = 220
	changesMax      = 640
	changesFreshFor = 10 * time.Second // how long a change counts as new
)

// changesState is the panel's watcher and what it last found.
type changesState struct {
	w       *changes.Watcher
	machine string // whose folder w watches: "" this machine's
	snap    changes.Snapshot
	have    bool    // snap is the watched folder's
	width   float32 // the panel's width, 0 for the first
	list    ui.ListState
	files   bool // the panel shows the files tab, not the changes
}

func (s Settings) changesShown() bool { return s.Changes != nil && *s.Changes }

// changesFolder is the folder the panel watches: the active space's, on
// its machine.
func (a *App) changesFolder() (machine, folder string) {
	s := a.space()
	if s == nil {
		return "", ""
	}
	return s.Machine, a.spaceFolder(s)
}

// spaceFolder is a space's folder: the one it names, else where its first
// terminal is.
func (a *App) spaceFolder(s *layout.Space) string {
	if s.Folder != "" {
		return s.Folder
	}
	for _, t := range s.Tabs {
		for _, p := range t.Panes() {
			if cwd := a.cwdOf(p); cwd != "" {
				return cwd
			}
		}
	}
	return ""
}

// syncChanges watches the folder the panel shows, and nothing when it is
// hidden (main thread, each frame).
func (a *App) syncChanges() {
	machine, want := "", ""
	if a.cfg.changesShown() {
		machine, want = a.changesFolder()
	}
	if a.chg.w != nil && a.chg.w.Folder() == want && a.chg.machine == machine {
		// Nobody looks at it while the window is in the background.
		a.chg.w.SetIdle(!a.focused)
		return
	}
	a.stopChanges()
	if want == "" {
		return
	}
	opt := changes.Options{}
	if machine != "" {
		// Another machine's folder: git there, through hc exec.
		hc := a.hcRunner()
		opt.Run = func(ctx context.Context, dir string, argv ...string) ([]byte, int, error) {
			r, err := hc.Exec(ctx, machine, dir, 20*time.Second, argv...)
			return r.Stdout, r.Code, err
		}
	}
	var w *changes.Watcher
	w = changes.Watch(want, opt, func(s changes.Snapshot) {
		a.post(func() {
			if a.chg.w == w {
				a.chg.snap, a.chg.have = s, true
			}
		})
	})
	a.chg.w, a.chg.machine = w, machine
}

func (a *App) stopChanges() {
	if a.chg.w != nil {
		a.chg.w.Stop()
	}
	a.chg.w, a.chg.have, a.chg.snap, a.chg.machine = nil, false, changes.Snapshot{}, ""
}

func (a *App) toggleChanges() {
	shown := !a.cfg.changesShown()
	a.cfg.Changes = &shown
	a.saveSettings()
}

// changesDivider is the panel's edge, which resizes it.
func (a *App) changesDivider(c *ui.Context, k *colors) {
	// One pixel of the layout; the grip reaches over the panes, which are
	// drawn before it.
	div := ui.Box(c).Width(sidebarGrip).Margin(0, 0, 0, 1-sidebarGrip).Shrink(0).
		Cursor(ui.CursorResizeColumn).Role(ui.RoleSplitter).Label("Changes edge").Tooltip("Drag to resize, double-click to reset")
	if dx, _, ok := div.Dragged(); ok {
		a.chg.width = min(max(a.changesWidth()-dx, changesMin), changesMax)
	}
	if div.DoubleClicked() {
		a.chg.width = changesWidth
	}
	active := div.Hovered() || div.Dragging()
	div.Draw(func(p *ui.Painter, r ui.Rect) {
		if active {
			p.Fill(ui.Rect{X: r.X + r.W - 2, Y: r.Y, W: 2, H: r.H}, k.accent.Alpha(0.6), 0)
			return
		}
		p.Fill(ui.Rect{X: r.X + r.W - 1, Y: r.Y, W: 1, H: r.H}, k.border, 0)
	})
}

func (a *App) changesWidth() float32 {
	if a.chg.width == 0 {
		return changesWidth
	}
	return a.chg.width
}

// kindColor is the color of a change's mark.
func kindColor(k *colors, kind changes.Kind) ui.Color {
	switch kind {
	case changes.Added, changes.Untracked:
		return k.ansi[2]
	case changes.Deleted:
		return k.ansi[1]
	case changes.Renamed:
		return k.ansi[4]
	}
	return k.ansi[3]
}

func (a *App) changesPanel(c *ui.Context, k *colors) {
	snap := a.chg.snap
	machine, folder := a.changesFolder()
	ui.Column(c).Width(a.changesWidth()).Shrink(0).MinHeight(0).Background(k.chrome).Label("Changes").Children(func() {
		ui.Row(c).Height(36).Padding(0, 6, 0, 14).Gap(6).AlignItems(ui.Center).Shrink(0).Children(func() {
			a.panelTab(c, k, "Changes", !a.chg.files)
			a.panelTab(c, k, "Files", a.chg.files)
			if !a.chg.files && a.chg.have && len(snap.Files) > 0 {
				ui.Box(c).Padding(0, 6).Radius(8).Background(k.hover).Children(func() {
					ui.Textf(c, "%d", len(snap.Files)).FontSize(11).TextColor(k.muted)
				})
			}
			ui.Spacer(c)
			if iconButton(c, k, "reset", "Look again", 24, 13).Tooltip("Look again").Clicked() {
				if a.chg.w != nil {
					a.chg.w.Refresh()
				}
				a.files.cache = nil
				a.files.root = ""
			}
			if iconButton(c, k, "x", "Hide changes", 24, 13).Tooltip(withKeys("Hide changes", a.keysFor("layout.toggle:changes"))).Clicked() {
				a.post(a.toggleChanges)
			}
		})
		if a.chg.files {
			if folder != "" {
				ui.Text(c, pathOn(machine, folder)).FontSize(11).TextColor(k.dim).SingleLine().Ellipsis("…").Padding(0, 14, 6, 14).Shrink(0)
			}
			ui.Box(c).Height(1).Background(k.border).Shrink(0)
			a.filesTab(c, k, machine, folder)
			return
		}
		if folder != "" {
			what := "files modified in the last day"
			if a.chg.have && snap.Git {
				what = "what differs from the last commit"
			}
			ui.Text(c, pathOn(machine, folder)+" · "+what).FontSize(11).TextColor(k.dim).SingleLine().Ellipsis("…").Padding(0, 14, 6, 14).Shrink(0)
		}
		ui.Box(c).Height(1).Background(k.border).Shrink(0)
		switch {
		case folder == "":
			changesNote(c, k, "No space", "The panel shows the changes in the active space's folder.")
		case !a.chg.have:
			changesNote(c, k, "Looking…", "")
		case errors.Is(snap.Err, changes.ErrNotGit):
			changesNote(c, k, "Not in git", "Changes outside git are not shown for other machines.")
		case snap.Err != nil:
			changesNote(c, k, "Cannot look at this folder", snap.Err.Error())
		case len(snap.Files) == 0 && snap.Git:
			changesNote(c, k, "No changes", "Nothing differs from the last commit.")
		case len(snap.Files) == 0:
			changesNote(c, k, "No changes", "No file here was modified in the last day.")
		default:
			a.changesList(c, k, snap)
		}
		if a.chg.have && snap.Truncated {
			ui.Text(c, "This folder has more files than are looked at; changes past them do not show.").
				FontSize(11).TextColor(k.dim).Padding(8, 14).LineHeight(1.35).Shrink(0)
		}
	})
	// Times ago move on.
	c.After(5 * time.Second)
}

func changesNote(c *ui.Context, k *colors, title, detail string) {
	ui.Column(c).Padding(20, 14).Gap(4).Children(func() {
		ui.Text(c, title).FontSize(12.5).TextColor(k.muted)
		if detail != "" {
			ui.Text(c, detail).FontSize(11.5).TextColor(k.dim).LineHeight(1.35)
		}
	})
}

// changesList builds the rows in view alone: a long list costs a frame no
// more than a short one.
func (a *App) changesList(c *ui.Context, k *colors, snap changes.Snapshot) {
	now := time.Now()
	files := snap.Files
	a.chg.list.Key = func(i int) any {
		if i < len(files) {
			return files[i].Path
		}
		return i
	}
	ui.List(c.Key("changes:"+snap.Folder), &a.chg.list, len(files), func(i int) {
		a.changeRow(c, k, snap, files[i], now)
	}).Grow(1).MinHeight(0).Padding(4, 6)
}

func (a *App) changeRow(c *ui.Context, k *colors, snap changes.Snapshot, f changes.File, now time.Time) {
	dir, name := path.Split(f.Path)
	fresh := now.Sub(f.Time) < changesFreshFor
	row := ui.Row(c.Key(f.Path)).Height(28).Padding(0, 8).Gap(8).Radius(7).AlignItems(ui.Center).
		Cursor(ui.CursorPointer).Role(ui.RoleButton).Label("Change " + f.Path)
	if row.Hovered() {
		row.Background(k.hover)
	}
	tip := f.Kind.String() + " · " + f.Path
	if f.OldPath != "" {
		tip += " (was " + f.OldPath + ")"
	}
	if !f.Time.IsZero() {
		tip += " · " + f.Time.Format("Jan 2 15:04:05")
	}
	row.Tooltip(tip)
	remote := a.chg.machine != ""
	abs := filepath.Join(snap.Folder, filepath.FromSlash(f.Path))
	row.ContextMenu(func(m *ui.Menu) {
		if m.Item("Show changes").Disabled(!snap.Git && f.Kind == changes.Deleted).Chosen() {
			a.post(func() { a.showFileChanges(snap, f) })
		}
		if !remote && m.Item("Open in editor").Disabled(f.Kind == changes.Deleted).Chosen() {
			a.post(func() { a.openInEditor(snap.Folder, f.Path) })
		}
		m.Separator()
		if m.Item("Copy path").Chosen() {
			c.WriteClipboard(abs)
		}
	})
	row.Children(func() {
		col := kindColor(k, f.Kind)
		ui.Box(c).Size(16, 16).Radius(4).Background(col.Alpha(0.16)).Center().Shrink(0).Children(func() {
			ui.Text(c, f.Kind.Letter()).FontSize(10).FontWeight(700).TextColor(col)
		})
		nameCol := k.foreground
		if f.Kind == changes.Deleted {
			nameCol = k.muted
		}
		ui.Text(c, name).FontSize(12.5).TextColor(nameCol).SingleLine().Shrink(0).MaxWidthPercent(70)
		ui.Text(c, strings.TrimSuffix(dir, "/")).FontSize(11).TextColor(k.dim).SingleLine().Ellipsis("…").MinWidth(0).Shrink(1)
		ui.Spacer(c)
		when := ui.Text(c, changes.Since(now, f.Time)).FontSize(11).SingleLine().Shrink(0)
		if fresh {
			when.TextColor(k.accent).FontWeight(600)
		} else {
			when.TextColor(k.dim)
		}
	})
	if row.Clicked() {
		a.post(func() { a.showFileChanges(snap, f) })
	}
}

// showFileChanges opens a tab showing how a file changed: git's diff of it
// against the last commit, or, outside git, the file.
func (a *App) showFileChanges(snap changes.Snapshot, f changes.File) {
	var argv []string
	switch {
	case snap.Git && f.Kind == changes.Untracked:
		argv = []string{"git", "diff", "--no-index", "--", os.DevNull, f.Path}
	case snap.Git && f.Kind == changes.Renamed && f.OldPath != "":
		argv = []string{"git", "diff", "-M", "HEAD", "--", f.OldPath, f.Path}
	case snap.Git:
		argv = []string{"git", "diff", "HEAD", "--", f.Path}
	case f.Kind == changes.Deleted:
		a.notify(f.Path + " was deleted")
		return
	default:
		argv = []string{"less", "--", f.Path}
	}
	// It runs where the folder is: in the shown space, on its machine.
	if _, err := a.do("tab.new", args{"space": a.activeSpace, "argv": toAny(argv), "folder": snap.Folder, "name": "Δ " + path.Base(f.Path)}); err != nil {
		a.notifyLevel(err.Error(), levelError)
	}
}

// openInEditor opens a file in $VISUAL or $EDITOR, else vi, in a new tab.
func (a *App) openInEditor(folder, rel string) {
	editor := os.Getenv("VISUAL")
	if editor == "" {
		editor = os.Getenv("EDITOR")
	}
	if editor == "" {
		editor = "vi"
	}
	argv := append(strings.Fields(editor), rel)
	if _, err := a.do("tab.new", args{"argv": toAny(argv), "folder": folder, "name": path.Base(rel)}); err != nil {
		a.notifyLevel(fmt.Sprint(err), levelError)
	}
}

// panelTab is a tab of the panel's header.
func (a *App) panelTab(c *ui.Context, k *colors, title string, active bool) {
	t := ui.Box(c.Key("tab:"+title)).Padding(3, 8).Radius(7).Cursor(ui.CursorPointer).Role(ui.RoleTab).Selected(active).Label(title)
	col := k.muted
	if active {
		t.Background(k.selected)
		col = k.foreground
	} else if t.Hovered() {
		t.Background(k.hover)
	}
	t.Children(func() { ui.Text(c, title).FontSize(12.5).FontWeight(600).TextColor(col) })
	if t.Clicked() && !active {
		a.chg.files = title == "Files"
	}
}
