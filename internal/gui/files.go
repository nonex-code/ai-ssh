package gui

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/nonex-code/ai-ssh/internal/agent"
)

// dragSelection is what a file row drags: the selection it belongs to.
type dragSelection struct {
	from  side
	paths []string
}

// side selects one of the two browsers in the file panel.
type side int

const (
	sideLocal side = iota
	sideRemote
)

// dirPane is the state of one browser: where it is, what it shows, what is
// selected.
type dirPane struct {
	// root is the top of the tree; children are the directories loaded so
	// far, expanded the ones the human opened. Directories load lazily, so a
	// deep tree never costs a full walk up front.
	root     string
	addr     string // what the address field shows (the root; editable)
	children map[string][]agent.FileEntry
	loading  map[string]bool
	expanded map[string]bool

	sel   map[string]bool // the selection; Ctrl-click adds to it
	focus string          // the last clicked path

	err string

	// History, as a file manager keeps it: back and forward walk it.
	hist    []string
	histIdx int

	// Inline editing, as Explorer does it: the row itself becomes an input.
	renaming   string // path being renamed
	renameText string
	creating   bool
	newName    string
}

func (p *dirPane) ensure() {
	if p.children == nil {
		p.children = map[string][]agent.FileEntry{}
	}
	if p.loading == nil {
		p.loading = map[string]bool{}
	}
	if p.expanded == nil {
		p.expanded = map[string]bool{}
	}
}

// record appends a directory to the history, dropping the forward entries.
func (p *dirPane) record(dir string) {
	if dir == "" {
		return
	}
	if p.histIdx >= 0 && p.histIdx < len(p.hist) && p.hist[p.histIdx] == dir {
		return
	}
	if p.histIdx+1 < len(p.hist) {
		p.hist = p.hist[:p.histIdx+1]
	}
	p.hist = append(p.hist, dir)
	p.histIdx = len(p.hist) - 1
}

func (p *dirPane) canBack() bool    { return p.histIdx > 0 }
func (p *dirPane) canForward() bool { return p.histIdx+1 < len(p.hist) }

func (p *dirPane) single(path string) {
	p.sel = map[string]bool{path: true}
	p.focus = path
}

func (p *dirPane) toggle(path string) {
	if p.sel == nil {
		p.sel = map[string]bool{}
	}
	if p.sel[path] {
		delete(p.sel, path)
	} else {
		p.sel[path] = true
	}
	p.focus = path
}

func (p *dirPane) clear() {
	p.sel = nil
	p.focus = ""
}

func (p *dirPane) count() int { return len(p.sel) }

func (p *dirPane) selected() []string {
	out := make([]string, 0, len(p.sel))
	for _, entries := range p.children {
		for _, e := range entries {
			if p.sel[e.Path] {
				out = append(out, e.Path)
			}
		}
	}
	return out
}

// entry finds a loaded entry by its path.
func (p *dirPane) entry(path string, remote bool) (agent.FileEntry, bool) {
	for _, e := range p.children[parentOf(path, remote)] {
		if e.Path == path {
			return e, true
		}
	}
	return agent.FileEntry{}, false
}

// targetDir is where transfers and new folders go: the selected folder, the
// folder of the selected file, or the tree's root.
func (p *dirPane) targetDir(remote bool) string {
	if p.focus == "" {
		return p.root
	}
	if e, ok := p.entry(p.focus, remote); ok && e.Dir {
		return e.Path
	}
	if !strings.Contains(p.focus, "/") {
		return p.root
	}
	return parentOf(p.focus, remote)
}

// filePane is the tab's file panel: local on the left, remote on the right.
type filePane struct {
	files  *agent.Files
	ready  bool
	open   bool
	busy   string
	local  dirPane
	remote dirPane
}

func (fp *filePane) pane(s side) *dirPane {
	if s == sideLocal {
		return &fp.local
	}
	return &fp.remote
}

func (fp *filePane) prefix(s side) string {
	if s == sideLocal {
		return "l:"
	}
	return "r:"
}

// toggleFiles switches the tab between the terminal and the file panel.
func (a *App) toggleFiles(tb *terminalTab) {
	fp := &tb.filesPane
	if fp.open {
		fp.open = false
		a.invalidate()
		return
	}
	fp.open = true

	if fp.local.root == "" {
		fp.local.root = localHome()
		fp.local.addr = fp.local.root
		// Headless tests have no window to post a frame to: they seed the
		// panes themselves, so only a real window lists the disk.
		if a.window() != nil {
			a.loadTree(tb, sideLocal, fp.local.root, true)
		}
	}

	if fp.files != nil {
		a.invalidate()
		return
	}
	ag := a.agentRef()
	if ag == nil {
		fp.remote.err = tr("agent 未就绪")
		a.invalidate()
		return
	}

	fp.busy = tr("连接中…")
	a.invalidate()

	go func() {
		f, err := ag.OpenFiles(context.Background(), tb.host)
		a.onMain(func() {
			fp.busy = ""
			if err != nil {
				fp.remote.err = err.Error()
				a.invalidate()
				return
			}
			fp.files = f
			fp.ready = true
			a.loadRemote(tb, f.Home())
		})
	}()
}

// --- loading -----------------------------------------------------------------

func (a *App) loadDir(tb *terminalTab, s side, dir string) {
	a.navigate(tb, s, dir, true)
}

// navigate makes dir the tree's root, records it in the history when record
// is set, and lists it. The tree is rebuilt from there.
func (a *App) navigate(tb *terminalTab, s side, dir string, record bool) {
	fp := &tb.filesPane
	p := fp.pane(s)
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return
	}
	p.ensure()
	p.root = dir
	p.addr = dir
	p.err = ""
	p.children = map[string][]agent.FileEntry{}
	p.loading = map[string]bool{}
	p.expanded = map[string]bool{}
	p.clear()
	if record {
		p.record(dir)
	}
	a.loadTree(tb, s, dir, true)
}

// loadTree lists one directory of the tree and shows it: expand opens it.
func (a *App) loadTree(tb *terminalTab, s side, dir string, expand bool) {
	fp := &tb.filesPane
	p := fp.pane(s)
	p.ensure()
	p.loading[dir] = true
	if expand {
		p.expanded[dir] = true
	}
	a.invalidate()

	apply := func(entries []agent.FileEntry, err error) {
		a.onMain(func() {
			delete(p.loading, dir)
			if err != nil {
				p.err = err.Error()
			} else {
				p.children[dir] = entries
				p.err = ""
			}
			a.invalidate()
		})
	}
	if s == sideRemote {
		f := fp.files
		if f == nil {
			delete(p.loading, dir)
			return
		}
		go func() {
			entries, err := f.List(dir)
			apply(entries, err)
		}()
		return
	}
	go func() {
		entries, err := localList(dir)
		apply(entries, err)
	}()
}

// toggleDir opens or closes one folder of the tree.
func (a *App) toggleDir(tb *terminalTab, s side, dir string, open bool) {
	p := tb.filesPane.pane(s)
	p.ensure()
	if open == p.expanded[dir] {
		return
	}
	if !open {
		delete(p.expanded, dir)
		a.invalidate()
		return
	}
	if _, loaded := p.children[dir]; loaded {
		p.expanded[dir] = true
		a.invalidate()
		return
	}
	a.loadTree(tb, s, dir, true)
}

// refreshTree reloads the root and every folder currently open.
func (a *App) refreshTree(tb *terminalTab, s side) {
	p := tb.filesPane.pane(s)
	p.ensure()
	dirs := []string{p.root}
	for d := range p.expanded {
		if d != p.root {
			dirs = append(dirs, d)
		}
	}
	for _, d := range dirs {
		a.loadTree(tb, s, d, false)
	}
}

// paneBack and paneForward walk the history.
func (a *App) paneBack(tb *terminalTab, s side) {
	p := tb.filesPane.pane(s)
	if !p.canBack() {
		return
	}
	p.histIdx--
	a.navigate(tb, s, p.hist[p.histIdx], false)
}

func (a *App) paneForward(tb *terminalTab, s side) {
	p := tb.filesPane.pane(s)
	if !p.canForward() {
		return
	}
	p.histIdx++
	a.navigate(tb, s, p.hist[p.histIdx], false)
}

func (a *App) loadRemote(tb *terminalTab, dir string) { a.loadDir(tb, sideRemote, dir) }

// --- actions -----------------------------------------------------------------

// paneCreate makes a directory in the pane; name comes from the inline row.
func (a *App) paneCreate(tb *terminalTab, s side, name string) {
	fp := &tb.filesPane
	p := fp.pane(s)
	name = strings.TrimSpace(name)
	if name == "" {
		p.creating = false
		a.invalidate()
		return
	}
	target := joinPath(p.targetDir(s == sideRemote), name, s == sideRemote)
	fp.busy = tr("创建中…")
	a.invalidate()

	done := func(err error) {
		a.onMain(func() {
			fp.busy = ""
			p.creating = false
			p.newName = ""
			if err != nil {
				p.err = err.Error()
			}
			a.refreshTree(tb, s)
		})
	}
	if s == sideRemote && fp.files != nil {
		f := fp.files
		go func() { done(f.Mkdir(target)) }()
		return
	}
	go func() { done(localMkdir(target)) }()
}

// paneRenameTo renames one entry; name comes from the inline input.
func (a *App) paneRenameTo(tb *terminalTab, s side, from, name string) {
	fp := &tb.filesPane
	p := fp.pane(s)
	name = strings.TrimSpace(name)
	if name == "" || name == baseOf(from, s == sideRemote) {
		p.renaming, p.renameText = "", ""
		a.invalidate()
		return
	}
	to := joinPath(parentOf(from, s == sideRemote), name, s == sideRemote)
	fp.busy = tr("重命名中…")
	a.invalidate()

	done := func(err error) {
		a.onMain(func() {
			fp.busy = ""
			p.renaming, p.renameText = "", ""
			if err != nil {
				p.err = err.Error()
			}
			p.clear()
			a.refreshTree(tb, s)
		})
	}
	if s == sideRemote && fp.files != nil {
		f := fp.files
		go func() { done(f.Rename(from, to)) }()
		return
	}
	go func() { done(localRename(from, to)) }()
}

func (a *App) paneDelete(tb *terminalTab, s side) {
	fp := &tb.filesPane
	p := fp.pane(s)
	targets := p.selected()
	if len(targets) == 0 {
		return
	}

	detail := targets[0]
	if len(targets) > 1 {
		detail = fmt.Sprintf(tr("%d 项（%s …）"), len(targets), targets[0])
	}
	res, err := mygo.Dialog.Message(mygo.MessageOptions{
		Parent:             a.window(),
		Type:               mygo.MessageWarning,
		Title:              tr("删除"),
		Message:            tr("确定删除？目录会连同内容一起删除。"),
		Detail:             detail,
		Buttons:            []string{tr("删除"), tr("取消")},
		CancelButton:       1,
		DestructiveButtons: []int{0},
	})
	if err != nil || res.Button != 0 {
		return
	}

	fp.busy = tr("删除中…")
	a.invalidate()

	if s == sideRemote && fp.files != nil {
		f := fp.files
		go func() {
			for _, target := range targets {
				if err := f.Remove(target, true); err != nil {
					a.onMain(func() { p.err = err.Error() })
					break
				}
			}
			a.onMain(func() {
				fp.busy = ""
				p.clear()
				a.refreshTree(tb, sideRemote)
			})
		}()
		return
	}
	go func() {
		for _, target := range targets {
			if err := localRemove(target, true); err != nil {
				a.onMain(func() { p.err = err.Error() })
				break
			}
		}
		a.onMain(func() {
			fp.busy = ""
			p.clear()
			a.refreshTree(tb, sideLocal)
		})
	}()
}

// upload queues the local selection into the remote directory.
func (a *App) upload(tb *terminalTab) {
	fp := &tb.filesPane
	f := fp.files
	if f == nil {
		return
	}
	local := fp.local.selected()
	if len(local) == 0 {
		return
	}
	for _, p := range local {
		a.q.add(&transferJob{
			up:     true,
			dir:    isLocalDir(p),
			local:  p,
			remote: joinPath(fp.remote.targetDir(true), baseOf(p, false), true),
			files:  f,
			resume: true,
		})
	}
	fp.busy = tr("已加入队列")
	a.invalidate()
	a.refreshTree(tb, sideRemote)
}

// download queues the remote selection into the local directory.
func (a *App) download(tb *terminalTab) {
	fp := &tb.filesPane
	f := fp.files
	if f == nil {
		return
	}
	remote := fp.remote.selected()
	if len(remote) == 0 {
		return
	}
	for _, p := range remote {
		a.q.add(&transferJob{
			up:     false,
			dir:    isDirEntry(&fp.remote, p, true),
			remote: p,
			local:  joinPath(fp.local.targetDir(false), baseOf(p, true), false),
			files:  f,
			resume: true,
		})
	}
	fp.busy = tr("已加入队列")
	a.invalidate()
	a.refreshTree(tb, sideLocal)
}

// dropSelection queues transfers for a drag dropped on the other pane.
func (a *App) dropSelection(tb *terminalTab, to side, sel dragSelection) {
	fp := &tb.filesPane
	switch {
	case to == sideRemote && sel.from == sideLocal:
		a.dropSelectionInto(tb, sideRemote, fp.remote.targetDir(true), sel)
	case to == sideLocal && sel.from == sideRemote:
		a.dropSelectionInto(tb, sideLocal, fp.local.targetDir(false), sel)
	}
}

// dropSelectionInto queues transfers into dir, the folder under the drop.
func (a *App) dropSelectionInto(tb *terminalTab, to side, dir string, sel dragSelection) {
	a.lastDrop = &sel
	fp := &tb.filesPane
	f := fp.files
	if f == nil || len(sel.paths) == 0 {
		return
	}
	switch {
	case to == sideRemote && sel.from == sideLocal:
		for _, p := range sel.paths {
			a.q.add(&transferJob{
				up:     true,
				dir:    isLocalDir(p),
				local:  p,
				remote: joinPath(dir, baseOf(p, false), true),
				files:  f,
				resume: true,
			})
		}
		a.refreshTree(tb, sideRemote)
	case to == sideLocal && sel.from == sideRemote:
		for _, p := range sel.paths {
			a.q.add(&transferJob{
				up:     false,
				dir:    isDirEntry(&fp.remote, p, true),
				remote: p,
				local:  joinPath(dir, baseOf(p, true), false),
				files:  f,
				resume: true,
			})
		}
		a.refreshTree(tb, sideLocal)
	default:
		return
	}
	fp.busy = tr("已加入队列")
	a.invalidate()
}

// dropLocalFiles queues uploads for files dropped from another application.
func (a *App) dropLocalFiles(tb *terminalTab, paths []string) {
	a.lastDrop = &dragSelection{from: sideLocal, paths: paths}
	fp := &tb.filesPane
	f := fp.files
	if f == nil || len(paths) == 0 {
		return
	}
	queued := 0
	for _, p := range paths {
		a.q.add(&transferJob{
			up:     true,
			dir:    isLocalDir(p),
			local:  p,
			remote: joinPath(fp.remote.targetDir(true), baseOf(p, false), true),
			files:  f,
			resume: true,
		})
		queued++
	}
	if queued > 0 {
		fp.busy = tr("已加入队列")
		a.refreshTree(tb, sideRemote)
		a.invalidate()
	}
}

// onlyParent reports a selection that is just "..", which cannot download.
func onlyParent(p *dirPane) bool {
	for path := range p.sel {
		if strings.HasSuffix(path, "..") {
			return true
		}
	}
	return false
}

func isLocalDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// --- view --------------------------------------------------------------------

func (a *App) filesView(c *ui.Context, t *ui.Theme, tb *terminalTab) {
	fp := &tb.filesPane

	ui.Column(c).Grow(1).FillWidth().Padding(8).Gap(gapSmall).Children(func() {
		ui.Row(c).Gap(gapMid).Children(func() {
			ui.Text(c, tr("文件传输")).FontSize(fontCard).Bold()
			if fp.busy != "" {
				ui.Text(c, fp.busy).FontSize(fontMicro).TextColor(t.TextMuted)
			}
			ui.Spacer(c)
			if ui.Button(c.Key("f:up"), tr("上传 →")).Height(controlH).Disabled(fp.local.count() == 0).Clicked() {
				a.upload(tb)
			}
			if ui.Button(c.Key("f:down"), tr("← 下载")).Height(controlH).Disabled(fp.remote.count() == 0 || onlyParent(&fp.remote)).Clicked() {
				a.download(tb)
			}
		})

		ui.Divider(c)

		a.queueView(c, t)

		ui.Row(c).Grow(1).FillWidth().Gap(gapMid).Children(func() {
			a.dirPaneView(c, t, tb, sideLocal)
			ui.Divider(c)
			a.dirPaneView(c, t, tb, sideRemote)
		})
	})
}

func (a *App) dirPaneView(c *ui.Context, t *ui.Theme, tb *terminalTab, s side) {
	fp := &tb.filesPane
	p := fp.pane(s)
	p.ensure()
	prefix := fp.prefix(s)
	title := tr("本地")
	if s == sideRemote {
		title = tr("远端 · ") + tb.host
	}
	remote := s == sideRemote

	col := ui.Column(c).Grow(1).FillHeight().Gap(gapTight)

	// Drop from the other pane moves files here.
	if over, ok := ui.DragOver[dragSelection](col); ok && over.from != s {
		col.Background(t.SurfaceHover)
	}
	if sel, ok := ui.Drop[dragSelection](col); ok && sel.from != s {
		a.dropSelection(tb, s, sel)
	}
	// Files dragged in from the desktop upload to the remote directory.
	if remote {
		if dropped := col.DroppedFiles(); len(dropped) > 0 {
			a.dropLocalFiles(tb, dropped)
		}
	}

	col.Children(func() {
		// The address bar, with the buttons a file manager puts beside it.
		ui.Row(c).Gap(gapTight).AlignItems(ui.Center).Children(func() {
			ui.Text(c, title).FontSize(fontSmall).Bold()
			a.navButton(c, t, prefix+"back", "chevron-left", tr("后退"), p.canBack(), func() { a.paneBack(tb, s) })
			a.navButton(c, t, prefix+"fwd", "chevron-right", tr("前进"), p.canForward(), func() { a.paneForward(tb, s) })
			a.navButton(c, t, prefix+"up", "arrow-up", tr("上一级"), p.root != "", func() {
				a.navigate(tb, s, parentOf(p.root, remote), true)
			})
			addr := ui.TextInput(c.Key(prefix+"addr"), &p.addr).
				Font(mono).FontSize(fontMicro).Height(22).Grow(1)
			if addr.Submitted() {
				a.loadDir(tb, s, strings.TrimSpace(p.addr))
			}
			a.navButton(c, t, prefix+"refresh", "refresh", tr("刷新"), true, func() {
				a.refreshTree(tb, s)
			})
		})

		scroll := ui.Scroll(c).Grow(1).Gap(1)
		scroll.ContextMenu(func(m *ui.Menu) {
			if m.Item(tr("新建文件夹…")).Chosen() {
				p.creating, p.newName = true, ""
				a.invalidate()
			}
			if m.Item(tr("刷新")).Chosen() {
				a.refreshTree(tb, s)
			}
		})
		scroll.Children(func() {
			if p.creating {
				ui.Row(c).Gap(gapSmall).Padding(5, 2).Children(func() {
					ui.Box(c).Width(14).Shrink(0)
					in := ui.TextInput(c.Key(prefix+"newname"), &p.newName).
						Placeholder(tr("新文件夹名")).Grow(1).AutoFocus()
					if in.Submitted() {
						a.paneCreate(tb, s, p.newName)
					}
				})
			}
			if p.root == "" {
				ui.Text(c, tr("还没有目录")).FontSize(fontSmall).TextColor(t.TextMuted)
				return
			}
			if p.loading[p.root] && len(p.children[p.root]) == 0 {
				ui.Text(c, tr("加载中…")).FontSize(fontSmall).TextColor(t.TextMuted)
				return
			}
			var walk func(dir string, depth int)
			walk = func(dir string, depth int) {
				if p.loading[dir] && len(p.children[dir]) > 0 {
					ui.Text(c, tr("加载中…")).FontSize(fontMicro).TextColor(t.TextMuted).
						Margin(float32(depth)*14+18, 0)
				}
				for _, e := range p.children[dir] {
					a.dirRow(c, t, tb, s, e, depth)
					if e.Dir && p.expanded[e.Path] {
						walk(e.Path, depth+1)
					}
				}
			}
			walk(p.root, 0)
			if len(p.children[p.root]) == 0 && !p.loading[p.root] {
				ui.Text(c, tr("空目录")).FontSize(fontSmall).TextColor(t.TextMuted)
			}
		})
		if p.err != "" {
			ui.Text(c, p.err).FontSize(fontMicro).TextColor(t.Danger)
		}
	})
}

// navButton is one of the small flat controls of a pane's address bar.
func (a *App) navButton(c *ui.Context, t *ui.Theme, key, name, label string, enabled bool, on func()) {
	el := ui.ButtonBase(c.Key(key)).Label(label).Tooltip(label).
		Width(22).Height(22).Radius(4)
	el.Children(func() {
		color := t.Text
		if !enabled {
			color = t.SurfacePressed
		}
		ui.Icon(c, icon(name)).Size(13, 13).TextColor(color)
	})
	if !enabled {
		return
	}
	if el.Hovered() {
		el.Background(t.SurfaceHover)
	}
	if el.Clicked() {
		on()
	}
}

func (a *App) dirRow(c *ui.Context, t *ui.Theme, tb *terminalTab, s side, e agent.FileEntry, depth int) {
	fp := &tb.filesPane
	p := fp.pane(s)
	prefix := fp.prefix(s)
	remote := s == sideRemote

	var name ui.Element
	row := ui.Row(c.Key(prefix+e.Path)).FillWidth().Gap(gapSmall).Padding(5, 2).Radius(4)

	if sel, ok := ui.Drop[dragSelection](row); ok && sel.from != s && e.Dir {
		a.dropSelectionInto(tb, s, e.Path, sel)
	}
	row.Children(func() {
		if depth > 0 {
			ui.Box(c).Width(float32(depth) * 14).Shrink(0)
		}
		if e.Dir {
			ch := "chevron-right"
			if p.expanded[e.Path] {
				ch = "chevron-down"
			}
			btn := ui.ButtonBase(c.Key(prefix + "expand:" + e.Path)).
				Label(tr("展开或折叠 ") + e.Name).Tooltip(tr("展开或折叠")).
				Width(14).Height(14).Radius(3)
			btn.Children(func() {
				ui.Icon(c, icon(ch)).Size(11, 11).TextColor(t.TextMuted)
			})
			if btn.Hovered() {
				btn.Background(t.SurfacePressed)
			}
			if btn.Clicked() {
				a.toggleDir(tb, s, e.Path, !p.expanded[e.Path])
			}
		} else {
			ui.Box(c).Width(14).Shrink(0)
		}

		if p.renaming == e.Path {
			in := ui.TextInput(c.Key(prefix+"rename"), &p.renameText).
				Height(22).Grow(1).AutoFocus()
			name = in
			if in.Submitted() {
				a.paneRenameTo(tb, s, e.Path, p.renameText)
			}
		} else {
			name = ui.Text(c, e.Name).FontSize(fontBody).TextColor(fileColor(e, t))
			if e.Dir {
				name.Bold()
			}
			name.Grow(1)
		}

		if mode := modeText(e); mode != "" {
			ui.Text(c, mode).Font(mono).FontSize(fontMicro).TextColor(modeColor(e, t))
		}
		if !e.Dir {
			ui.Text(c, humanSize(e.Size)).Font(mono).FontSize(fontMicro).TextColor(t.TextMuted)
		}
	})

	switch {
	case p.sel[e.Path]:
		row.Background(t.SurfacePressed)
	case row.Hovered():
		row.Background(t.SurfaceHover)
	}

	// Text is selectable, so it takes the pointer itself: the row's own
	// Clicked never fires for a press on the name. Ask both.
	clicked, mods := false, ui.Modifiers(0)
	if row.Clicked() {
		clicked, mods = true, row.ClickModifiers()
	}
	if name.Clicked() {
		clicked, mods = true, name.ClickModifiers()
	}
	if clicked {
		if mods&(ui.Ctrl|ui.Super) != 0 {
			p.toggle(e.Path)
		} else {
			p.single(e.Path)
		}
	}
	// Double-clicking a folder opens or closes it, as a file tree does.
	if (row.DoubleClicked() || name.DoubleClicked()) && e.Dir {
		a.toggleDir(tb, s, e.Path, !p.expanded[e.Path])
	}

	row.ContextMenu(func(m *ui.Menu) {
		if e.Dir {
			open, close := tr("展开"), tr("折叠")
			if p.expanded[e.Path] {
				open, close = close, open
			}
			if m.Item(open).Chosen() {
				a.toggleDir(tb, s, e.Path, !p.expanded[e.Path])
			}
			if m.Item(tr("设为上传/下载目标")).Chosen() {
				p.single(e.Path)
				a.invalidate()
			}
			_ = close
		} else if remote {
			if m.Item(tr("下载")).Chosen() {
				p.single(e.Path)
				a.download(tb)
			}
		} else if m.Item(tr("上传到远端")).Chosen() {
			p.single(e.Path)
			a.upload(tb)
		}
		if m.Item(tr("重命名")).Chosen() {
			p.single(e.Path)
			p.renaming, p.renameText = e.Path, e.Name
			a.invalidate()
		}
		if m.Item(tr("删除")).Chosen() {
			p.single(e.Path)
			a.paneDelete(tb, s)
		}
		m.Separator()
		if m.Item(tr("新建文件夹…")).Chosen() {
			p.creating, p.newName = true, ""
			a.invalidate()
		}
		if m.Item(tr("刷新")).Chosen() {
			a.refreshTree(tb, s)
		}
		if m.Item(tr("复制名称")).Chosen() {
			a.copyText(e.Name)
		}
	})

	// A row drags itself, or the whole selection when it is part of one.
	paths := []string{e.Path}
	if p.sel[e.Path] {
		paths = p.selected()
	}
	row.Drag(dragSelection{from: s, paths: paths})
}

func isDirEntry(p *dirPane, target string, remote bool) bool {
	for _, e := range p.children[parentOf(target, remote)] {
		if e.Path == target {
			return e.Dir
		}
	}
	return false
}

func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}
