package gui

import (
	"context"
	"errors"
	"log"
	"slices"
	"time"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"

	"github.com/nonex-code/ai-ssh/internal/agent"
	"github.com/nonex-code/ai-ssh/internal/daemon"
	"github.com/nonex-code/ai-ssh/internal/localshell"
)

// terminalTab is one interactive session. The human's shells live in this
// process, never on the AI-facing socket.
// tabStatus drives the dot on a tab.
type tabStatus int

const (
	tabConnecting tabStatus = iota
	tabLive
	tabFailed
)

// tabMode is which pane a session tab shows.
type tabMode int

const (
	modeTerminal tabMode = iota
	modeFiles
	modeForward
	modeMonitor
)

type terminalTab struct {
	id      int
	host    string
	label   string // the tab's title: the host, or what the remote shell sets
	status  tabStatus
	shell   *agent.Session
	term    *terminal.Terminal
	err     string
	opening bool
	closed  bool

	// localShell is set for a shell on this machine: no agent, no vault, no
	// remote panes. host then carries the shell's label.
	localShell *localshell.Shell

	filesPane   filePane
	forwardPane forwardPane
	monitorPane monitorPane
	monitorAuto bool
}

// openTerminal dials in the background and builds the terminal on the main
// thread once the session is up.
func (a *App) openTerminal(alias string) {
	a.noteRecent(alias)
	// Every call opens a new session: the same server can have as many tabs
	// as you want, as a browser opens a page again.
	tb := &terminalTab{id: a.nextTab, host: alias, label: alias, opening: true, forwardPane: newForwardPane(), status: tabConnecting}
	a.nextTab++
	a.tabs = append(a.tabs, tb)
	a.activeTab = len(a.tabs) - 1
	a.invalidate()

	ag := a.agentRef()
	if ag == nil {
		tb.opening = false
		tb.err = tr("agent 未就绪")
		a.invalidate()
		return
	}

	go func() {

		shell, err := ag.OpenSession(context.Background(), alias, 80, 24)
		if err != nil {
			a.onMain(func() {
				tb.opening = false
				tb.status = tabFailed
				tb.err = err.Error()
				a.invalidate()
				var unk *daemon.UnknownHostKeyError
				if errors.As(err, &unk) {
					a.promptHostKey(tb, unk)
				}
				var mm *daemon.HostKeyMismatchError
				if errors.As(err, &mm) {
					a.promptHostKeyChange(tb, mm)
				}
			})
			return
		}

		a.onMain(func() {
			if tb.closed {
				_ = shell.Close()
				return
			}
			if !a.attachTerminal(tb, terminal.Options{Conn: shell}) {
				_ = shell.Close()
				return
			}
			tb.shell = shell
			a.startConfiguredForwards(tb)
			a.invalidate()
		})
	}()
}

// attachTerminal builds the terminal widget with the app's theme, font and the
// tab's title/exit callbacks, and records the outcome on the tab. It reports
// whether the terminal was created.
func (a *App) attachTerminal(tb *terminalTab, opts terminal.Options) bool {
	light, dark := terminalThemes(a.prefs.Terminal)
	opts.Theme, opts.DarkTheme = light, dark
	opts.Font = terminal.Font{Family: monoFont, Size: 13, Weight: 400}
	opts.Padding = terminal.Padding{X: 10, Y: 8}
	opts.OnTitle = func(title string) {
		a.onMain(func() {
			if title == "" {
				return
			}
			if len([]rune(title)) > 24 {
				title = string([]rune(title)[:24]) + "…"
			}
			tb.label = title
			a.invalidate()
		})
	}
	opts.OnExit = func(int) {
		a.onMain(func() { a.closeTabByID(tb.id) })
	}

	term, err := terminal.New(opts)
	if err != nil {
		tb.opening = false
		tb.status = tabFailed
		tb.err = err.Error()
		a.invalidate()
		return false
	}
	tb.term = term
	tb.opening = false
	tb.status = tabLive
	a.invalidate()
	return true
}

// agentRef is the running agent, read under the lock.
func (a *App) agentRef() *agent.Agent {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.ag
}

// maxRecent is how many connections the home page remembers.
const maxRecent = 8

// noteRecent records that the human opened a connection.
func (a *App) noteRecent(alias string) {
	if alias == "" {
		return
	}
	out := make([]recentConn, 0, maxRecent)
	out = append(out, recentConn{Alias: alias, At: time.Now()})
	for _, r := range a.recent {
		if r.Alias == alias {
			continue
		}
		if len(out) >= maxRecent {
			break
		}
		out = append(out, r)
	}
	a.recent = out
	a.saveGeometry()
}

func (a *App) closeTabByID(id int) {
	for i, tb := range a.tabs {
		if tb.id == id {
			a.closeTab(i)
			return
		}
	}
}

// closeTab closes one session. Safe to call twice.
func (a *App) closeTab(i int) {
	if i < 0 || i >= len(a.tabs) {
		return
	}
	tb := a.tabs[i]
	if !tb.closed {
		tb.closed = true
		if tb.term != nil {
			_ = tb.term.Close()
		}
		if tb.shell != nil {
			_ = tb.shell.Close()
		}
		if tb.filesPane.files != nil {
			_ = tb.filesPane.files.Close()
			tb.filesPane.files = nil
		}
		a.closeForwards(tb)
		a.stopMonitor(tb)
	}
	a.tabs = append(a.tabs[:i], a.tabs[i+1:]...)
	switch {
	case a.activeTab == i:
		a.activeTab = i - 1 // the tab before it, or the home page
	case a.activeTab > i:
		a.activeTab--
	}
	if a.activeTab >= len(a.tabs) {
		a.activeTab = len(a.tabs) - 1
	}
	a.invalidate()
}

// tabDropTarget is where a dragged tab would land: before the tab id, or
// after it when the pointer was on its right half.
type tabDropTarget struct {
	id    int
	after bool
}

// tabIndex finds an open tab by its stable id.
func (a *App) tabIndex(id int) int {
	return slices.IndexFunc(a.tabs, func(tb *terminalTab) bool { return tb.id == id })
}

// moveTab moves the dragged tab next to targetID: before it, or after it when
// after is set. The active tab follows its id, so reordering never switches
// which session is on screen.
func (a *App) moveTab(id, targetID int, after bool) {
	from, to := a.tabIndex(id), a.tabIndex(targetID)
	if from < 0 || to < 0 || from == to {
		return
	}
	activeID := -1
	if a.activeTab >= 0 && a.activeTab < len(a.tabs) {
		activeID = a.tabs[a.activeTab].id
	}
	tab := a.tabs[from]
	rest := slices.Concat(a.tabs[:from], a.tabs[from+1:])
	if to > from {
		to-- // the target shifted left when the dragged tab was taken out
	}
	if after {
		to++
	}
	a.tabs = slices.Insert(rest, to, tab)
	a.activeTab = a.tabIndex(activeID)
	if activeID < 0 {
		a.activeTab = -1
	}
	a.invalidate()
}

// moveTabToEnd is what dropping a tab on the "+" button does.
func (a *App) moveTabToEnd(id int) {
	from := a.tabIndex(id)
	if from < 0 {
		return
	}
	activeID := -1
	if a.activeTab >= 0 && a.activeTab < len(a.tabs) {
		activeID = a.tabs[a.activeTab].id
	}
	tab := a.tabs[from]
	a.tabs = append(slices.Concat(a.tabs[:from], a.tabs[from+1:]), tab)
	a.activeTab = a.tabIndex(activeID)
	if activeID < 0 {
		a.activeTab = -1
	}
	a.invalidate()
}

// mode reports which pane the tab shows.
func (tb *terminalTab) mode() tabMode {
	switch {
	case tb.monitorPane.open:
		return modeMonitor
	case tb.forwardPane.open:
		return modeForward
	case tb.filesPane.open:
		return modeFiles
	}
	return modeTerminal
}

// isLocal reports whether the tab runs a shell on this machine instead of an
// SSH session: remote files, tunnels and monitoring do not apply to it.
func (tb *terminalTab) isLocal() bool { return tb.localShell != nil }

func (a *App) activeTerminal() *terminalTab {
	if a.activeTab < 0 || a.activeTab >= len(a.tabs) {
		return nil
	}
	return a.tabs[a.activeTab]
}

// --- view --------------------------------------------------------------------

func (a *App) terminalPane(c *ui.Context, t *ui.Theme, tb *terminalTab) {
	ui.Column(c).Grow(1).FillHeight().Children(func() {
		a.terminalToolbar(c, t, tb)
		switch {
		case tb.monitorPane.open:
			a.monitorView(c, t, tb)
		case tb.forwardPane.open:
			a.forwardView(c, t, tb)
		case tb.filesPane.open:
			a.filesView(c, t, tb)
		default:
			a.terminalBody(c, t, tb)
		}
	})
}

// terminalToolbar is the one strip above the terminal, where the file pane is
// toggled. It stays out of the terminal's way once the pane is open.
func (a *App) terminalToolbar(c *ui.Context, t *ui.Theme, tb *terminalTab) {
	ui.Row(c).FillWidth().Height(controlH+4).Padding(10, 0).Gap(gapSmall).Children(func() {
		ui.Text(c, tb.host).FontSize(fontMicro).TextColor(t.TextMuted)
		ui.Spacer(c)
		if !tb.isLocal() {
			a.modeButton(c, t, "terminal", tr("终端"), "mode:term", tb.mode() == modeTerminal, func() { a.showTerminal(tb) })
			a.modeButton(c, t, "folder", tr("文件"), "mode:files", tb.mode() == modeFiles, func() { a.showFiles(tb) })
			a.modeButton(c, t, "forward", tr("转发"), "mode:fw", tb.mode() == modeForward, func() { a.showForward(tb) })
			a.modeButton(c, t, "gauge", tr("详情"), "mode:mon", tb.mode() == modeMonitor, func() { a.showMonitor(tb) })
		}
		a.stripButton(c, t, "duplicate", "duplicate", tr("新会话（复制标签）"), t.TextMuted, func() {
			a.duplicateTab(tb)
		})
		if !tb.isLocal() {
			a.stripButton(c, t, "edit", "pencil", tr("编辑连接 ")+tb.host, t.TextMuted, func() { a.openEditor(tb.host) })
		}
	})
}

// modeButton keeps all four modes in place, with the active one highlighted, so
// the toolbar never jumps as panes open and close.
func (a *App) modeButton(c *ui.Context, t *ui.Theme, name, label, key string, active bool, on func()) {
	color := t.TextMuted
	if active {
		color = t.Accent
	}
	el := ui.ButtonBase(c.Key(key)).Label(label).Tooltip(label).
		Width(controlH).Height(controlH).Radius(radiusTab)
	el.Children(func() {
		ui.Icon(c, icon(name)).Size(iconSize, iconSize).TextColor(color)
	})
	if el.Hovered() && !active {
		el.Background(t.SurfaceHover)
	}
	if el.Clicked() && !active {
		on()
	}
}

func (a *App) terminalBody(c *ui.Context, t *ui.Theme, tb *terminalTab) {
	switch {
	case tb.term != nil:
		terminal.View(c, tb.term).Grow(1).FillWidth().AutoFocus()

	case tb.err != "":
		ui.Column(c).Grow(1).FillWidth().Center().Gap(gapMid).Children(func() {
			ui.Text(c, tr("无法打开终端")).FontSize(fontCard).TextColor(t.Danger)
			ui.Text(c, tb.err).Font(mono).FontSize(fontSmall).TextColor(t.TextMuted)
			ui.Row(c).Gap(gapSmall).Children(func() {
				if ui.Button(c.Key("retry"), tr("重试")).Clicked() {
					a.closeTab(a.activeTab)
					a.duplicateTab(tb) // the same host, or the same local shell
				}
				if ui.Button(c.Key("dismiss"), tr("断开")).Clicked() {
					a.closeTab(a.activeTab)
				}
			})
		})

	default:
		ui.Column(c).Grow(1).FillWidth().Center().Gap(gapSmall).Children(func() {
			ui.Text(c, tr("正在连接 ")+tb.host+" …").FontSize(fontBody).TextColor(t.TextMuted)
			if !a.libReady {
				ui.Text(c, tr("首次打开终端会先下载终端组件（约 2 MB），之后走缓存。")).
					FontSize(fontSmall).TextColor(t.TextMuted)
			}
		})
	}
}

// warmTerminalLibrary downloads libghostty-vt (once, sha256 checked) before the
// first session, so opening a tab is instant instead of freezing the window.
func (a *App) warmTerminalLibrary() {
	if _, err := terminal.LibraryPath(); err != nil {
		log.Printf("terminal: %v", err)
		return
	}
	a.onMain(func() {
		a.libReady = true
		a.invalidate()
	})
}
