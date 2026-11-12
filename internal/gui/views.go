package gui

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/nonex-code/ai-ssh/internal/proto"
)

// view is the whole window. The layout is the one a browser uses: a tab strip
// across the top, and below it whatever the active tab shows — a terminal, or
// the "new tab" page with the servers.
func (a *App) view(c *ui.Context) {
	t := a.applyTheme(c)
	a.shortcuts(c)
	s := a.snapshot()

	ui.Column(c).Fill().Background(t.Background).Children(func() {
		if s.phase != phaseReady {
			a.unlockPane(c, t, s.phase, s.needsVault, s.errMsg)
			return
		}
		a.syncTitle()
		a.tabStrip(c, t, s)
		if tb := a.activeTerminal(); tb != nil {
			a.terminalPane(c, t, tb)
			return
		}
		a.startPage(c, t, s)
	})
}

// syncTitle puts the active session in the window title, as a terminal app does.
func (a *App) syncTitle() {
	w := a.window()
	if w == nil {
		return
	}
	want := "ai-ssh"
	if tb := a.activeTerminal(); tb != nil {
		want = "ai-ssh — " + tb.host
	}
	if want != a.lastTitle {
		a.lastTitle = want
		w.SetTitle(want)
	}
}

// shortcuts are registered every frame, at window level, so they work whatever
// has the focus. They avoid keys a shell needs: no Ctrl+W, and no plain Esc or
// F5 while the terminal is showing.
//
// A window shortcut claims its key before the focused element sees it (mygo
// checks claimed before delivering to the focus handler). Binding Esc while a
// live terminal is on screen therefore stole Esc from vi, vim, less and every
// other full-screen program; the same went for F5 in htop and mc. Only bind
// them while another pane, whose own controls use them, is showing.
func (a *App) shortcuts(c *ui.Context) {
	if a.curPhase() == phaseReady {
		c.OnWindowShortcut(ui.Ctrl, ui.KeyTab, func() { a.cycleTab(1) })
		c.OnWindowShortcut(ui.Ctrl|ui.Shift, ui.KeyTab, func() { a.cycleTab(-1) })
	}

	// The terminal gets these keys itself; a shell or a TUI needs them.
	if tb := a.activeTerminal(); tb != nil && tb.mode() == modeTerminal {
		return
	}

	c.OnWindowShortcut(0, ui.KeyEscape, func() {
		if tb := a.activeTerminal(); tb != nil && tb.mode() != modeTerminal {
			a.showTerminal(tb)
		}
	})
	c.OnWindowShortcut(0, ui.KeyF5, func() {
		tb := a.activeTerminal()
		if tb == nil {
			return
		}
		switch tb.mode() {
		case modeFiles:
			a.refreshTree(tb, sideLocal)
			a.refreshTree(tb, sideRemote)
		case modeMonitor:
			a.refreshMonitor(context.Background(), tb)
		case modeForward:
			a.invalidate()
		}
	})
}

// cycleTab moves through the start page and the sessions, wrapping around.
func (a *App) cycleTab(step int) {
	n := len(a.tabs) + 1 // the start page sits at -1
	if n <= 1 {
		return
	}
	cur := (a.activeTab + 1 + step) % n
	if cur < 0 {
		cur += n
	}
	a.activeTab = cur - 1
	a.invalidate()
}

// --- tab strip ---------------------------------------------------------------
//
// The strip is the whole navigation: the home tab and the open sessions, the
// way a browser keeps only open pages as tabs. The utilities at the far right
// are icon-only buttons, left of the native window buttons.

func (a *App) tabStrip(c *ui.Context, t *ui.Theme, s uiState) {
	// On a window with a hidden title bar the strip *is* the title bar: it
	// keeps clear of the native window buttons and drags the window.
	bar := c.TitleBar()
	height := bar.Height
	if height < titleBarH {
		height = titleBarH
	}
	strip := ui.Row(c).FillWidth().Height(height).
		Padding(0, bar.Right+10, 0, bar.Left+10).
		AlignItems(ui.Center).Gap(gapSmall)
	strip.DragWindow()
	strip.Children(func() {
		// Tabs scroll on their own, so a crowd of them can never squeeze the
		// actions: the strip keeps its groups whatever is open.
		tabs := ui.ScrollHorizontal(c).Grow(1).Height(controlH).Scrollbars(false).Gap(4)
		tabs.Children(func() {
			a.homeTab(c, t)
			for i, tb := range a.tabs {
				a.sessionTab(c, t, i, tb)
			}
			a.newTabButton(c, t, s)
		})

		a.stripGroup(c, t, func() {
			a.stripButton(c, t, "servers", "server", tr("服务器管理"), t.TextMuted, func() { a.openServers() })
			a.stripButton(c, t, "ai", "log", tr("AI 活动"), t.TextMuted, func() { a.openActivity() })
		})
		a.stripGroup(c, t, func() {
			copyIcon := "mcp"
			if s.copied {
				copyIcon = "check"
			}
			a.stripButton(c, t, "mcp", copyIcon, tr("复制 MCP 配置"), t.TextMuted, func() { a.copyMCPConfig() })
			a.themeButton(c, t)
			a.stripButton(c, t, "settings", "gear", tr("设置"), t.TextMuted, func() { a.openSettings() })
			if a.updateAvailable() {
				a.stripButton(c, t, "update", "arrow-up", tr("有新版本 ")+a.upd.Version, t.Accent, func() {
					a.settingsTab = "about"
					a.openSettings()
				})
			} else if a.updDone {
				a.stripButton(c, t, "update", "refresh", tr("更新已就绪，重启生效"), t.Success, func() {
					a.settingsTab = "about"
					a.openSettings()
				})
			}
		})
		a.stripGroup(c, t, func() {
			a.stripButton(c, t, "lock", "lock", tr("锁定"), t.TextMuted, func() { a.lock() })
		})
	})
	ui.Divider(c)
}

// addServerButton is the + right after the quick connect field: it opens the
// empty connection form.
func (a *App) addServerButton(c *ui.Context, t *ui.Theme) {
	el := ui.ButtonBase(c.Key("strip:add")).Label(tr("添加服务器")).Tooltip(tr("添加服务器")).
		Width(inputH).Height(inputH).Radius(radiusCard)
	el.Children(func() {
		ui.Icon(c, icon("plus")).Size(20, 20).TextColor(t.TextMuted)
	})
	if el.Hovered() {
		el.Background(t.SurfaceHover)
	}
	if el.Clicked() {
		a.openEditor("")
	}
}

// newTabButton is the + after the last tab: it pops up the saved servers, and
// choosing one opens its session in a new tab.
func (a *App) newTabButton(c *ui.Context, t *ui.Theme, s uiState) {
	btn := ui.ButtonBase(c.Key("strip:newtab")).Label(tr("新建标签")).
		Tooltip(tr("新建标签：选择已保存的服务器")).Width(controlH).Height(controlH).Radius(radiusTab)
	btn.Children(func() {
		ui.Icon(c, icon("newtab")).Size(iconSize, iconSize).TextColor(t.TextMuted)
	})
	if btn.Hovered() {
		btn.Background(t.SurfaceHover)
	}
	// Dropping an open tab here puts it after the last one.
	if _, over := ui.DragOver[int](btn); over {
		btn.Background(t.SurfaceHover)
	}
	if id, ok := ui.Drop[int](btn); ok {
		a.moveTabToEnd(id)
	}
	btn.PopoverMenuButton(func(m *ui.Menu) {
		hosts := favoriteOrder(s.hosts, a.bookmarkOrder())
		if len(hosts) == 0 {
			m.Item(tr("还没有保存的连接")).Disabled(true)
		}
		// Group the servers, so a long catalogue stays findable. With no named
		// groups at all, keep the old flat list: nesting would only add a
		// pointless submenu.
		sections := groupSections(s.groups, hosts, false)
		named := false
		for _, g := range sections {
			if g.name != "" {
				named = true
				break
			}
		}
		for _, g := range sections {
			if len(g.hosts) == 0 {
				continue
			}
			if !named {
				for _, h := range g.hosts {
					if m.Item(h.Alias).Trailing(h.User + "@" + h.Addr).Chosen() {
						a.openTerminal(h.Alias)
					}
				}
				continue
			}
			m.Submenu(fmt.Sprintf("%s (%d)", groupLabel(g.name), len(g.hosts)), func(sub *ui.Menu) {
				for _, h := range g.hosts {
					if sub.Item(h.Alias).Trailing(h.User + "@" + h.Addr).Chosen() {
						a.openTerminal(h.Alias)
					}
				}
			})
		}
		if len(hosts) > 0 {
			m.Separator()
		}
		for _, sh := range a.localShellChoices() {
			if m.Item(tr("本地 · ") + tr(sh.Label)).Chosen() {
				a.openLocalTerminal(sh)
			}
		}
		m.Separator()
		if m.Item(tr("添加服务器…")).Chosen() {
			a.openEditor("")
		}
		if m.Item(tr("管理服务器…")).Chosen() {
			a.openServers()
		}
		if m.Item(tr("导入 ~/.ssh/config")).Chosen() {
			a.importSSHConfig()
		}
		m.Separator()
		if m.Item(tr("设置…")).Chosen() {
			a.openSettings()
		}
	})
}

// stripGroup wraps a few actions with a hairline on the left, so the strip
// reads as groups instead of one row of icons.
func (a *App) stripGroup(c *ui.Context, t *ui.Theme, build func()) {
	ui.Row(c).Gap(gapSmall).AlignItems(ui.Center).Children(func() {
		ui.Box(c).Width(1).Height(16).Background(t.Border).Margin(0, gapTight)
		ui.Row(c).Gap(gapTight).AlignItems(ui.Center).Children(build)
	})
}

// stripButton is a flat icon control: no plate, no border, just the glyph.
// The label carries the meaning for assistive technology and tests.
func (a *App) stripButton(c *ui.Context, t *ui.Theme, id, name, label string, color ui.Color, on func()) {
	el := ui.ButtonBase(c.Key("strip:" + id)).Label(label).Tooltip(label).
		Width(controlH).Height(controlH).Radius(radiusTab)
	el.Children(func() {
		ui.Icon(c, icon(name)).Size(iconSize, iconSize).TextColor(color)
	})
	if el.Hovered() {
		el.Background(t.SurfaceHover)
	}
	if el.Clicked() {
		on()
	}
}

// homeTab is the pinned first tab: it always exists, cannot be closed, and
// clicks back to the home page from any session.
func (a *App) homeTab(c *ui.Context, t *ui.Theme) {
	color := t.TextMuted
	if a.activeTab < 0 {
		color = t.Text
	}
	row := ui.Row(c.Key("tab:home")).Label(tr("主页")).Tooltip(tr("主页")).Gap(4).
		Height(controlH).Padding(9, 0).Radius(3).
		BorderWidth(0, 0, 2, 0).BorderColor(ui.Transparent)
	row.Children(func() {
		ui.Icon(c, icon("home")).Size(iconSize, iconSize).TextColor(color)
	})
	// Same emphasis as the sessions: a 2px ink bar under the active tab.
	switch {
	case a.activeTab < 0:
		row.BorderColor(t.Text)
	case row.Hovered():
		row.Background(t.SurfaceHover)
	}
	if row.Clicked() {
		a.activeTab = -1
		a.invalidate()
	}
}

// sessionTab is one open or opening session: click to switch, × to disconnect.
func (a *App) sessionTab(c *ui.Context, t *ui.Theme, idx int, tb *terminalTab) {
	label := tb.label
	if label == "" {
		label = tb.host
	}
	var labelEl ui.Element
	row := ui.Row(c.Key(tabKey(tb))).Label(tr("切换到 ")+label).Tooltip(tr("拖动可排序；右键可断开")).
		Gap(3).Height(controlH).Padding(8, 0).Radius(3).
		BorderWidth(0, 0, 2, 0).BorderColor(ui.Transparent)
	// The tab is a drag source carrying its stable id, and every other tab is
	// a drop target: the half the pointer is on chooses before or after it.
	row.Drag(tb.id)
	overID, over := ui.DragOver[int](row)
	dropID, dropped := ui.Drop[int](row)
	dropAfter := false
	if over && overID != tb.id {
		if x, _, ok := row.PointerPosition(); ok {
			dropAfter = x > row.Bounds().W/2
		}
		a.tabDrop = tabDropTarget{id: tb.id, after: dropAfter}
	}
	row.ContextMenu(func(m *ui.Menu) {
		if m.Item(tr("新会话（复制标签）")).Chosen() {
			a.duplicateTab(tb)
		}
		if m.Item(tr("断开")).Chosen() {
			a.closeTabByID(tb.id)
		}
	})
	row.Children(func() {
		dot := t.TextMuted
		switch tb.status {
		case tabLive:
			dot = t.Success
		case tabFailed:
			dot = t.Danger
		}
		ui.Text(c, "●").FontSize(7).TextColor(dot)
		labelEl = ui.Text(c, shortLabel(label, 14)).FontSize(fontBody).SingleLine()
		if a.activeTab == idx {
			labelEl = labelEl.Bold().TextColor(t.Text)
		} else {
			labelEl = labelEl.TextColor(t.TextMuted)
		}
		// Only the active tab carries a close button: it keeps every tab
		// narrow, and an inactive one closes from its right-click menu.
		if a.activeTab == idx {
			closeBtn := ui.ButtonBase(c.Key("close")).Label(tr("断开 ") + tb.host).
				Tooltip(tr("断开 ") + tb.host).Width(14).Height(14).Radius(3)
			closeBtn.Children(func() {
				ui.Icon(c, icon("x")).Size(9, 9).TextColor(t.TextMuted)
			})
			if closeBtn.Hovered() {
				closeBtn.Background(t.SurfacePressed)
			}
			if closeBtn.Clicked() {
				a.closeTab(idx)
				return
			}
		}
		if over && overID != tb.id {
			// The accent bar shows where the tab would land.
			bar := ui.Box(c).Absolute().Top(4).Bottom(4).Width(2).Background(t.Accent)
			if dropAfter {
				bar.Right(0)
			} else {
				bar.Left(0)
			}
		}
	})
	if dropped {
		after := a.tabDrop.id == tb.id && a.tabDrop.after
		a.tabDrop = tabDropTarget{}
		a.moveTab(dropID, tb.id, after)
	}
	// Flat, like the home tab: ink color and an underline mark the active one.
	switch {
	case a.activeTab == idx:
		row.BorderColor(t.Text)
	case row.Hovered():
		row.Background(t.SurfaceHover)
	}
	if row.Clicked() || labelEl.Clicked() {
		a.activeTab = idx
		a.invalidate()
	}
}

// shortLabel keeps a tab narrow: labels longer than maxCols columns (CJK counts
// two) end in an ellipsis, so a long remote title cannot eat the strip.
func shortLabel(s string, maxCols int) string {
	var out []rune
	width := 0
	for _, r := range s {
		w := 1
		if r >= 0x1100 {
			w = 2
		}
		if width+w > maxCols {
			return string(out) + "…"
		}
		out = append(out, r)
		width += w
	}
	return s
}

func tabKey(tb *terminalTab) string {
	return "tab:" + tb.host + ":" + strconv.Itoa(tb.id)
}

// --- start page --------------------------------------------------------------
//
// The start page is deliberately almost empty: connections live in the tab
// strip, so all it holds is the one field that creates a new one.

func (a *App) startPage(c *ui.Context, t *ui.Theme, s uiState) {
	// The field sits a little above the middle; the history grows under it.
	ui.Column(c).Fill().AlignItems(ui.Center).Children(func() {
		ui.Spacer(c).Grow(2)
		ui.Column(c).Width(560).Gap(gapMid).Children(func() {
			// The brand, then the one field that makes a connection.
			ui.Row(c).Gap(10).AlignItems(ui.Center).Margin(0, 0, 0, gapSmall).Children(func() {
				logoMark(c, 34)
				ui.Column(c).Gap(1).Children(func() {
					ui.Text(c, "ai-ssh").FontSize(fontPage).Bold()
					ui.Text(c, tr("给 AI 用的 SSH，凭证不出本机")).
						FontSize(fontMicro).TextColor(t.TextMuted)
				})
			})
			ui.Row(c).Gap(gapSmall).AlignItems(ui.Center).Children(func() {
				field := ui.TextInput(c.Key("quick"), &a.quick).
					Placeholder("user@host:port").
					Height(inputH).
					FontSize(fontBody).Grow(1)
				if field.Submitted() {
					a.quickConnect()
				}
				a.addServerButton(c, t)
			})
			ui.Text(c, tr("Enter 连接并保存为配置")).FontSize(fontSmall).TextColor(t.TextMuted)
			a.flashLine(c, t)
			a.localShellRow(c, t)
			a.hostList(c, t, s)
		})
		ui.Spacer(c).Grow(3)
	})
}

// hostList is the home page's list of saved connections: the ones used
// recently first, with when, then the rest in catalogue order. Tabs are
// transient, so this list is how a closed connection is opened again.
func (a *App) hostList(c *ui.Context, t *ui.Theme, s uiState) {
	if len(s.hosts) == 0 {
		return
	}
	last := make(map[string]recentConn, len(s.recent))
	for _, r := range s.recent {
		last[r.Alias] = r
	}

	var used, rest []proto.HostInfo
	for _, h := range favoriteOrder(s.hosts, a.bookmarkOrder()) {
		if _, ok := last[h.Alias]; ok {
			used = append(used, h)
		} else {
			rest = append(rest, h)
		}
	}
	sort.SliceStable(used, func(i, j int) bool {
		return last[used[i].Alias].At.After(last[used[j].Alias].At)
	})

	ui.Column(c).Gap(2).Children(func() {
		ui.Text(c, tr("连接")).FontSize(fontSmall).TextColor(t.TextMuted).Margin(0, gapSmall, 0, 0)
		for _, h := range append(used, rest...) {
			r := last[h.Alias]
			row := ui.ButtonBase(c.Key("host:"+h.Alias)).Label(tr("连接 ")+h.Alias).
				FillWidth().Gap(gapSmall).Height(controlH).Padding(10, 0).Radius(radiusTab)
			row.Children(func() {
				ui.Text(c, h.Alias).FontSize(fontBody)
				ui.Text(c, h.User+"@"+h.Addr).Font(mono).FontSize(fontMicro).TextColor(t.TextMuted)
				ui.Spacer(c)
				if !r.At.IsZero() {
					ui.Text(c, shortAgo(r.At)).FontSize(fontMicro).TextColor(t.TextMuted)
				}
			})
			if row.Hovered() {
				row.Background(t.SurfaceHover)
			}
			if row.Clicked() {
				a.openTerminal(h.Alias)
			}
		}
	})
}

// shortAgo renders a connection time the way a history list does.
func shortAgo(tm time.Time) string {
	d := time.Since(tm)
	switch {
	case d < time.Minute:
		return tr("刚刚")
	case d < time.Hour:
		return fmt.Sprintf(tr("%d 分钟前"), int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf(tr("%d 小时前"), int(d.Hours()))
	default:
		return tm.Local().Format("01-02 15:04")
	}
}

// --- locked ------------------------------------------------------------------

func (a *App) unlockPane(c *ui.Context, t *ui.Theme, ph phase, needsVault bool, errMsg string) {
	ui.Column(c).Fill().Center().Children(func() {
		ui.Column(c).Width(470).Padding(paddingSheet).Radius(radiusSheet).Gap(gapMid).
			Background(t.Surface).Border(1, t.Border).Shadow(0, 10, 32, 0, shadow(t)).Children(func() {
			title := tr("解锁 vault")
			if needsVault {
				title = tr("创建 vault")
			}
			ui.Row(c).Gap(gapSmall).AlignItems(ui.Center).Children(func() {
				logoMark(c, 30)
				ui.Text(c, title).FontSize(fontPage).Bold()
			})
			ui.Text(c, tr("主密码只用于在本机解开加密的服务器配置，不会发送给任何模型。")).
				FontSize(fontBody).TextColor(t.TextMuted)

			label := tr("解锁")
			if needsVault {
				label = tr("创建并解锁")
			}
			var password, confirm ui.Element
			if needsVault {
				password = ui.TextInput(c.Key("password"), &a.password).
					Placeholder(tr("主密码")).Password().AutoFocus()
				ui.Row(c).FillWidth().Gap(gapSmall).AlignItems(ui.Center).Children(func() {
					confirm = ui.TextInput(c.Key("confirm"), &a.confirm).
						Placeholder(tr("再次输入主密码")).Password().Grow(1)
					a.unlockButton(c, label, ph)
				})
			} else {
				ui.Row(c).FillWidth().Gap(gapSmall).AlignItems(ui.Center).Children(func() {
					password = ui.TextInput(c.Key("password"), &a.password).
						Placeholder(tr("主密码")).Password().AutoFocus().Grow(1)
					a.unlockButton(c, label, ph)
				})
			}

			if ph == phaseUnlocking {
				ui.Text(c, tr("正在派生密钥…")).FontSize(fontBody).TextColor(t.TextMuted)
			}
			if password.Submitted() || confirm.Submitted() {
				a.unlock()
			}
			if errMsg != "" {
				ui.Text(c, errMsg).FontSize(fontBody).TextColor(t.Danger)
			}
			if needsVault {
				ui.Text(c, tr("首次启动会直接创建加密的 vault，不会写出任何明文配置。")).
					FontSize(fontSmall).TextColor(t.TextMuted)
			}
		})
	})
}

// unlockButton is the button that follows the password field, as a search
// field's button does.
func (a *App) unlockButton(c *ui.Context, label string, ph phase) {
	if ui.PrimaryButton(c.Key("unlock"), label).
		Disabled(ph == phaseUnlocking).Clicked() {
		a.unlock()
	}
}

// approvalTitle names a pending action for the popup.
func approvalTitle(ap proto.Approval) string {
	verb := map[string]string{
		proto.OpExec:     tr("执行命令"),
		proto.OpUpload:   tr("上传文件"),
		proto.OpDownload: tr("下载文件"),
	}[ap.Op]
	if verb == "" {
		verb = ap.Op
	}
	if ap.Elevated {
		verb += tr("（提权）")
	}
	return ap.Host + " · " + verb
}
