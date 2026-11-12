package gui

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/nonex-code/ai-ssh/internal/config"
	"github.com/nonex-code/ai-ssh/internal/proto"
)

// openServers shows the server manager: every saved connection, grouped, with
// actions to connect, test, edit, delete, and move between groups. Main thread
// only.
func (a *App) openServers() {
	if a.servers != nil {
		a.servers.Focus()
		return
	}
	if a.window() == nil {
		return // headless: tests render serversView directly
	}
	win := mygo.NewWindow(mygo.WindowOptions{
		Title:         tr("ai-ssh 服务器"),
		Width:         760,
		Height:        620,
		MinWidth:      560,
		MinHeight:     420,
		DisableResize: false,
		Content:       ui.View(a.serversView),
	})
	setWindowIcon(win)
	a.servers = win
	win.OnClosed(func() {
		if a.servers == win {
			a.servers = nil
		}
	})
	win.Center()
	win.Focus()
}

func (a *App) closeServers() {
	if a.servers != nil {
		a.servers.Close()
	}
}

func (a *App) serversView(c *ui.Context) {
	t := a.applyTheme(c)
	c.OnWindowShortcut(0, ui.KeyEscape, func() { a.closeServers() })
	s := a.snapshot()
	query := strings.TrimSpace(a.serverQuery)
	hosts := a.filterHosts(s.hosts, query)
	sections := groupSections(s.groups, hosts, query == "")

	ui.Column(c).Fill().Background(t.Background).Children(func() {
		ui.Row(c).Padding(20, 16).Gap(gapSmall).AlignItems(ui.Center).Children(func() {
			ui.Textf(c, tr("服务器 · %d"), len(hosts)).FontSize(fontPage).Bold()
			named := 0
			for _, g := range sections {
				if g.name != "" {
					named++
				}
			}
			if named > 0 {
				ui.Textf(c, tr("%d 个分组"), named).
					FontSize(fontSmall).TextColor(t.TextMuted)
			}
			ui.Spacer(c)
			ui.TextInput(c.Key("srv:search"), &a.serverQuery).
				Placeholder(tr("搜索别名 / 地址 / 分组")).Width(220)
			if ui.Button(c.Key("srv:newgroup"), tr("新建分组")).Clicked() {
				a.openGroupCreate("")
			}
			a.stripButton(c, t, "srv:add", "plus", tr("添加服务器"), t.TextMuted, func() { a.openEditor("") })
		})
		if s.serversErr != "" {
			ui.Row(c).Padding(20, 0).Children(func() {
				ui.Text(c, s.serversErr).FontSize(fontSmall).TextColor(t.Danger)
			})
		}
		ui.Divider(c)

		ui.Scroll(c).Grow(1).Padding(gapSmall, gapWide, gapSmall, gapWide).Gap(2).Children(func() {
			if len(sections) == 0 {
				hint := tr("没有匹配的服务器。")
				if query == "" {
					hint = tr("还没有服务器。点右上角的 + 添加一台，或先「新建分组」再往里填。")
				}
				ui.Text(c, hint).FontSize(fontBody).TextColor(t.TextMuted).Padding(12, 16)
				return
			}
			for _, g := range sections {
				a.serverGroupSection(c, t, g)
			}
		})
	})
}

// serverGroupSection is one group header and its rows, foldable. An empty group
// stays visible: it is how a newly created group is filled.
func (a *App) serverGroupSection(c *ui.Context, t *ui.Theme, g hostGroup) {
	folded := a.collapsedGroup(g.name)

	header := ui.Row(c).FillWidth().Height(34).Padding(8, 0).Radius(radiusTab).
		Gap(gapSmall).AlignItems(ui.Center)
	header.Children(func() {
		chev := ui.ButtonBase(c.Key("srv:fold:" + g.name)).
			Label(tr("折叠或展开分组 ") + groupLabel(g.name)).
			Tooltip(tr("折叠或展开分组 ") + groupLabel(g.name)).Width(22).Height(22).Radius(3)
		chev.Children(func() {
			iconName := "chevron-down"
			if folded {
				iconName = "chevron-right"
			}
			ui.Icon(c, icon(iconName)).Size(12, 12).TextColor(t.TextMuted)
		})
		if chev.Hovered() {
			chev.Background(t.SurfaceHover)
		}
		if chev.Clicked() {
			a.toggleGroup(g.name)
		}

		color := t.Text
		if g.name == "" {
			color = t.TextMuted
		}
		ui.Text(c, groupLabel(g.name)).FontSize(fontBody).Bold().TextColor(color)
		ui.Row(c).Padding(7, 1).Radius(5).Background(t.Background).Children(func() {
			ui.Textf(c, "%d", len(g.hosts)).FontSize(fontMicro).TextColor(t.TextMuted)
		})

		ui.Spacer(c)
		if g.name != "" {
			a.stripButton(c, t, "srv:grpadd:"+g.name, "plus",
				tr("在 ")+g.name+tr(" 中添加服务器"), t.TextMuted, func() { a.openEditorInGroup(g.name) })
			a.stripButton(c, t, "srv:grprename:"+g.name, "pencil",
				tr("重命名分组 ")+g.name, t.TextMuted, func() { a.openGroupRename(g.name) })
			a.stripButton(c, t, "srv:grpdel:"+g.name, "x",
				tr("解散分组 ")+g.name, t.Danger,
				func() { a.confirmDeleteGroup(g.name, len(g.hosts)) })
		}
	})
	if folded {
		return
	}
	if len(g.hosts) == 0 {
		ui.Row(c).FillWidth().Height(38).Padding(36, 0).AlignItems(ui.Center).Gap(gapSmall).Children(func() {
			ui.Text(c, tr("这个分组还没有服务器。")).
				FontSize(fontSmall).TextColor(t.TextMuted)
			ui.Spacer(c)
			if ui.Button(c.Key("srv:emptyadd:"+g.name), tr("添加服务器")).Clicked() {
				a.openEditorInGroup(g.name)
			}
		})
		return
	}
	for _, h := range g.hosts {
		a.serverRow(c, t, h)
	}
}

// filterHosts is the manager's search: alias, user@addr or group.
func (a *App) filterHosts(hosts []proto.HostInfo, query string) []proto.HostInfo {
	hosts = favoriteOrder(hosts, a.bookmarkOrder())
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return hosts
	}
	out := make([]proto.HostInfo, 0, len(hosts))
	for _, h := range hosts {
		if strings.Contains(strings.ToLower(h.Alias), q) ||
			strings.Contains(strings.ToLower(h.User+"@"+h.Addr), q) ||
			strings.Contains(strings.ToLower(h.Group), q) {
			out = append(out, h)
		}
	}
	return out
}

// serverRow is one connection: what it is on the left, what can be done with
// it on the right, and the last test result beside the actions.
func (a *App) serverRow(c *ui.Context, t *ui.Theme, h proto.HostInfo) {
	row := ui.Row(c).FillWidth().Height(44).Padding(24, 0).Radius(radiusTab).
		Gap(gapSmall).AlignItems(ui.Center)
	row.Children(func() {
		ui.Text(c, h.Alias).FontSize(fontBody).Bold()
		if h.Favorite {
			ui.Text(c, "★").FontSize(fontSmall).TextColor(t.Warning)
		}
		ui.Row(c).Padding(7, 2).Radius(5).Background(t.Background).Children(func() {
			ui.Text(c, h.Auth).FontSize(fontMicro).TextColor(t.TextMuted)
		})
		ui.Text(c, h.User+"@"+h.Addr).Font(mono).FontSize(fontSmall).TextColor(t.TextMuted)
		ui.Spacer(c)
		a.groupButton(c, t, h)
		a.stripButton(c, t, "srv:open:"+h.Alias, "terminal", tr("连接 ")+h.Alias, t.TextMuted, func() {
			a.openTerminal(h.Alias)
		})
		a.stripButton(c, t, "srv:edit:"+h.Alias, "pencil", tr("编辑 ")+h.Alias, t.TextMuted, func() {
			a.openEditor(h.Alias)
		})
		a.stripButton(c, t, "srv:del:"+h.Alias, "x", tr("删除 ")+h.Alias, t.Danger, func() {
			a.confirmDeleteHost(h.Alias)
		})
	})
	if row.Hovered() {
		row.Background(t.SurfaceHover)
	}
}

// groupButton moves one server between groups without opening its form.
func (a *App) groupButton(c *ui.Context, t *ui.Theme, h proto.HostInfo) {
	label := tr("把 ") + h.Alias + tr(" 移到分组")
	btn := ui.ButtonBase(c.Key("srv:group:" + h.Alias)).Label(label).Tooltip(label).
		Width(controlH).Height(controlH).Radius(radiusTab)
	btn.Children(func() {
		ui.Icon(c, icon("folder")).Size(iconSize, iconSize).TextColor(t.TextMuted)
	})
	if btn.Hovered() {
		btn.Background(t.SurfaceHover)
	}
	groups := a.groupChoices()
	btn.PopoverMenuButton(func(m *ui.Menu) {
		if h.Group != "" {
			if m.Item(tr("移出分组")).Chosen() {
				a.moveHostToGroup(h.Alias, "")
			}
			m.Separator()
		}
		for _, g := range groups {
			it := m.Item(g).Radio(g == h.Group)
			if it.Chosen() && g != h.Group {
				a.moveHostToGroup(h.Alias, g)
			}
		}
		if len(groups) > 0 {
			m.Separator()
		}
		if m.Item(tr("新建分组…")).Chosen() {
			a.openGroupCreate(h.Alias)
		}
	})
}

// confirmDeleteHost asks before removing a connection, in the app's own window.
func (a *App) confirmDeleteHost(alias string) {
	a.askConfirm(confirmRequest{
		title:        tr("删除连接 ") + alias,
		message:      tr("删除后它的认证、提权口令和隧道配置会一起消失，不能撤销。"),
		detail:       tr("正在运行的会话不会被断开，只是配置里不再有这台服务器。"),
		confirmLabel: tr("删除"),
		onConfirm:    func() { a.deleteHostByAlias(alias) },
	})
}

// deleteHostByAlias removes one connection. The read-modify-write is
// serialised in the agent (UpdateConfig), so a delete cannot race a save from
// the connection form and be silently lost; failures show in the manager.
func (a *App) deleteHostByAlias(alias string) {
	ag := a.agentRef()
	if ag == nil {
		a.setServersErr(tr("agent 未就绪"))
		return
	}
	a.setServersErr("")
	go func() {
		err := ag.UpdateConfig(func(cfg *config.Config) error {
			if _, ok := cfg.Hosts[alias]; !ok {
				return fmt.Errorf(tr("连接 %s 已经不存在"), alias)
			}
			delete(cfg.Hosts, alias)
			return nil
		})
		a.onMain(func() {
			if err != nil {
				a.setServersErr(tr("删除失败：") + err.Error())
				return
			}
			// Drop it from the list at once, then resync with the daemon.
			a.mu.Lock()
			kept := make([]proto.HostInfo, 0, len(a.hosts))
			for _, h := range a.hosts {
				if h.Alias != alias {
					kept = append(kept, h)
				}
			}
			a.hosts = kept
			a.mu.Unlock()
			a.invalidate()
			a.refreshHosts()
			a.invalidateServers()
		})
	}()
}

func (a *App) setServersErr(msg string) {
	a.mu.Lock()
	a.serversErr = msg
	a.mu.Unlock()
	a.invalidateServers()
}

func (a *App) invalidateServers() {
	if a.servers != nil {
		a.servers.Invalidate()
	}
}
