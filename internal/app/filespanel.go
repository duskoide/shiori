package app

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

// The files tab of the changes panel is a directory viewer: the active
// space's folder as a tree, whose folders open on a click and whose files
// open in the editor. It reads this machine's folders only, and a folder
// when it opens.

// filesState is the tab's tree.
type filesState struct {
	root   string
	open   map[string]bool      // folders shown open, by absolute path
	cache  map[string]cachedDir // what a folder held when it was read
	list   ui.ListState
	hidden bool // dot files are shown
}

// cachedDir is a folder's listing and when it was read; it is read again
// after filesFresh, so the tree follows the disk.
type cachedDir struct {
	es  []fileEntry
	at  time.Time
	err error
}

const filesFresh = 3 * time.Second

type fileEntry struct {
	name string
	dir  bool
}

// fileRow is a row of the tree in view.
type fileRow struct {
	path  string
	entry fileEntry
	depth int
}

// reset forgets the tree when the folder shown changes.
func (s *filesState) reset(root string) {
	if s.root == root && s.open != nil {
		return
	}
	s.root, s.open, s.cache = root, map[string]bool{}, map[string]cachedDir{}
}

func (s *filesState) read(dir string) ([]fileEntry, error) {
	if s.cache == nil {
		s.cache = map[string]cachedDir{}
	}
	if cd, ok := s.cache[dir]; ok && time.Since(cd.at) < filesFresh {
		return cd.es, cd.err
	}
	des, err := os.ReadDir(dir)
	if err != nil {
		s.cache[dir] = cachedDir{at: time.Now(), err: err}
		return nil, err
	}
	var es []fileEntry
	for _, d := range des {
		name := d.Name()
		if name == ".git" || (!s.hidden && strings.HasPrefix(name, ".")) {
			continue
		}
		isDir := d.IsDir()
		if !isDir && d.Type()&os.ModeSymlink != 0 {
			if st, err := os.Stat(filepath.Join(dir, name)); err == nil && st.IsDir() {
				isDir = true
			}
		}
		es = append(es, fileEntry{name: name, dir: isDir})
	}
	sort.SliceStable(es, func(i, j int) bool {
		if es[i].dir != es[j].dir {
			return es[i].dir
		}
		return strings.ToLower(es[i].name) < strings.ToLower(es[j].name)
	})
	s.cache[dir] = cachedDir{es: es, at: time.Now()}
	return es, nil
}

// rows flattens the open folders into the rows in order.
func (s *filesState) rows() []fileRow {
	var out []fileRow
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		es, _ := s.read(dir)
		for _, e := range es {
			p := filepath.Join(dir, e.name)
			out = append(out, fileRow{path: p, entry: e, depth: depth})
			if e.dir && s.open[p] {
				walk(p, depth+1)
			}
		}
	}
	walk(s.root, 0)
	return out
}

func (a *App) filesTab(c *ui.Context, k *colors, machine, folder string) {
	switch {
	case folder == "":
		changesNote(c, k, "No space", "The viewer shows the active space's folder.")
		return
	case machine != "":
		changesNote(c, k, "Another machine", "Folders of other machines are not listed here.")
		return
	}
	f := &a.files
	f.reset(folder)
	if _, err := f.read(folder); err != nil {
		changesNote(c, k, "Cannot read this folder", err.Error())
		return
	}
	c.After(filesFresh) // the disk moves on
	rows := f.rows()
	if len(rows) == 0 {
		changesNote(c, k, "Empty", "Nothing in this folder.")
		return
	}
	f.list.Key = func(i int) any {
		if i < len(rows) {
			return rows[i].path
		}
		return i
	}
	ui.List(c.Key("files:"+folder), &f.list, len(rows), func(i int) {
		a.fileRowView(c, k, rows[i])
	}).Grow(1).MinHeight(0).Padding(4, 6)
}

func (a *App) fileRowView(c *ui.Context, k *colors, r fileRow) {
	f := &a.files
	row := ui.Row(c.Key(r.path)).Height(26).Padding(0, 6, 0, 6+float32(r.depth)*14).Gap(6).Radius(7).AlignItems(ui.Center).
		Cursor(ui.CursorPointer).Role(ui.RoleButton).Label("File " + r.entry.name)
	if row.Hovered() {
		row.Background(k.hover)
	}
	row.Tooltip(r.path)
	row.ContextMenu(func(m *ui.Menu) {
		if m.Item("Open in editor").Disabled(r.entry.dir).Chosen() {
			a.post(func() { a.openInEditor(filepath.Dir(r.path), filepath.Base(r.path)) })
		}
		if m.Item("New terminal here").Chosen() {
			dir := r.path
			if !r.entry.dir {
				dir = filepath.Dir(dir)
			}
			a.post(func() {
				if _, err := a.do("tab.new", args{"folder": dir}); err != nil {
					a.notifyLevel(err.Error(), levelError)
				}
			})
		}
		m.Separator()
		if m.Item("Copy path").Chosen() {
			c.WriteClipboard(r.path)
		}
	})
	row.Children(func() {
		switch {
		case r.entry.dir:
			ch := "chevron-right"
			if f.open[r.path] {
				ch = "chevron-down"
			}
			ui.Icon(c, glyph(ch)).Size(12, 12).TextColor(k.muted)
			ui.Icon(c, glyph("folder")).Size(14, 14).TextColor(k.accent)
		default:
			ui.Box(c).Size(12, 12).Shrink(0)
			ui.Icon(c, glyph("file")).Size(14, 14).TextColor(k.muted)
		}
		ui.Text(c, r.entry.name).FontSize(12.5).TextColor(k.foreground).SingleLine().Ellipsis("…").MinWidth(0).Shrink(1)
	})
	if row.Clicked() {
		p, dir := r.path, r.entry.dir
		a.post(func() {
			if dir {
				f.open[p] = !f.open[p]
				return
			}
			a.openInEditor(filepath.Dir(p), filepath.Base(p))
		})
	}
}
