package gui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/nonex-code/ai-ssh/internal/config"
	"github.com/nonex-code/ai-ssh/internal/proto"
)

// hostGroup is one section of the server list: a named group, or the ungrouped
// remainder (name "").
type hostGroup struct {
	name  string
	hosts []proto.HostInfo
}

// groupNames is the order groups are offered in: the saved order first, then
// groups that only exist on a host, alphabetically. An empty name is never in
// the list; ungrouped hosts are handled separately.
func groupNames(order []string, hosts []proto.HostInfo) []string {
	out := make([]string, 0, len(order)+4)
	seen := map[string]bool{}
	for _, g := range order {
		g = strings.TrimSpace(g)
		if g == "" || seen[g] {
			continue
		}
		seen[g] = true
		out = append(out, g)
	}
	var extra []string
	for _, h := range hosts {
		if h.Group == "" || seen[h.Group] {
			continue
		}
		seen[h.Group] = true
		extra = append(extra, h.Group)
	}
	sort.Strings(extra)
	return append(out, extra...)
}

// groupSections partitions hosts into sections. Empty groups are kept when
// showEmpty (no search is active), so a group just created can be filled.
// Ungrouped hosts always come last, and only when there are any.
func groupSections(order []string, hosts []proto.HostInfo, showEmpty bool) []hostGroup {
	byGroup := map[string][]proto.HostInfo{}
	for _, h := range hosts {
		byGroup[h.Group] = append(byGroup[h.Group], h)
	}

	var out []hostGroup
	seen := map[string]bool{}
	add := func(name string, keepEmpty bool) {
		if seen[name] {
			return
		}
		seen[name] = true
		hs := byGroup[name]
		if len(hs) == 0 && (!keepEmpty || name == "") {
			return
		}
		out = append(out, hostGroup{name: name, hosts: hs})
	}
	for _, g := range groupNames(order, hosts) {
		add(g, showEmpty)
	}
	if len(byGroup[""]) > 0 {
		add("", true)
	}
	return out
}

func groupLabel(name string) string {
	if name == "" {
		return tr("未分组")
	}
	return name
}

// groupChoices is the list for the form's suggestions and the move menu.
func (a *App) groupChoices() []string {
	s := a.snapshot()
	return groupNames(s.groups, s.hosts)
}

// collapsedGroup / toggleGroup remember which manager sections are folded.
func (a *App) collapsedGroup(name string) bool { return a.collapsedGroups[name] }

func (a *App) toggleGroup(name string) {
	if a.collapsedGroups == nil {
		a.collapsedGroups = map[string]bool{}
	}
	a.collapsedGroups[name] = !a.collapsedGroups[name]
}

// --- group operations --------------------------------------------------------

// moveHostToGroup puts one server in a group ("" takes it out). The group is
// recorded in the saved order, so it stays in the manager even if it later
// becomes empty.
func (a *App) moveHostToGroup(alias, group string) {
	ag := a.agentRef()
	if ag == nil {
		a.setServersErr(tr("agent 未就绪"))
		return
	}
	a.setServersErr("")
	go func() {
		err := ag.UpdateConfig(func(cfg *config.Config) error {
			h := cfg.Hosts[alias]
			if h == nil {
				return fmt.Errorf(tr("连接 %s 已经不存在"), alias)
			}
			h.Group = group
			if group != "" {
				cfg.AddGroup(group)
			}
			return nil
		})
		a.onMain(func() {
			if err != nil {
				a.setServersErr(tr("移动分组失败：") + err.Error())
				return
			}
			a.refreshHosts()
			a.invalidateServers()
		})
	}()
}

// deleteGroup forgets a group but keeps every server in it (they become
// ungrouped).
func (a *App) deleteGroup(name string) {
	ag := a.agentRef()
	if ag == nil {
		a.setServersErr(tr("agent 未就绪"))
		return
	}
	a.setServersErr("")
	go func() {
		err := ag.UpdateConfig(func(cfg *config.Config) error {
			cfg.DeleteGroup(name)
			return nil
		})
		a.onMain(func() {
			delete(a.collapsedGroups, name)
			if err != nil {
				a.setServersErr(tr("解散分组失败：") + err.Error())
				return
			}
			a.refreshHosts()
			a.invalidateServers()
		})
	}()
}

// confirmDeleteGroup asks before dissolving a group: the servers stay, which is
// easy to misunderstand if a dialog only said "delete".
func (a *App) confirmDeleteGroup(name string, count int) {
	message := tr("这个分组还没有服务器，解散后分组名会消失。")
	if count > 0 {
		message = fmt.Sprintf(tr("分组里的 %d 台服务器会移到「未分组」，连接本身不会被删除。"), count)
	}
	a.askConfirm(confirmRequest{
		title:        tr("解散分组 ") + name,
		message:      message,
		detail:       tr("分组只是一个显示标签；认证、提权和隧道配置都不会变。"),
		confirmLabel: tr("解散"),
		onConfirm:    func() { a.deleteGroup(name) },
	})
}

// openEditorInGroup starts the new-connection form with the group already
// filled in.
func (a *App) openEditorInGroup(group string) {
	a.openEditor("")
	a.draft.group = group
	if a.editor != nil {
		a.editor.Invalidate()
	}
}

// --- create / rename dialog --------------------------------------------------

// groupDialog is the one dialog for creating a group and renaming one. When
// alias is set, the server is moved into the new group as it is created.
type groupDialog struct {
	// seq identifies one opening of the dialog, so a save that finishes after
	// the window was closed cannot act on a newer one.
	seq    int
	mode   string // create | rename
	alias  string
	from   string
	value  string
	err    string
	saving bool
}

func (a *App) openGroupCreate(alias string) {
	if a.window() == nil {
		return
	}
	a.groupDlg = groupDialog{seq: a.groupDlg.seq + 1, mode: "create", alias: alias}
	a.showGroupWindow(tr("新建分组"))
}

func (a *App) openGroupRename(name string) {
	if a.window() == nil {
		return
	}
	a.groupDlg = groupDialog{seq: a.groupDlg.seq + 1, mode: "rename", from: name, value: name}
	a.showGroupWindow(tr("重命名分组"))
}

func (a *App) showGroupWindow(title string) {
	if a.groupWin != nil {
		a.groupWin.Close()
	}
	win := mygo.NewWindow(mygo.WindowOptions{
		Title:         "ai-ssh " + title,
		Width:         460,
		Height:        250,
		MinWidth:      400,
		MinHeight:     220,
		DisableResize: true,
		AlwaysOnTop:   true,
		Content:       ui.View(a.groupDialogView),
	})
	setWindowIcon(win)
	a.groupWin = win
	win.OnClosed(func() {
		if a.groupWin == win {
			a.groupWin = nil
		}
	})
	win.Center()
	win.Focus()
}

func (a *App) closeGroupDialog() {
	a.groupDlg.seq = 0
	if a.groupWin != nil {
		a.groupWin.Close()
	}
}

func (a *App) groupDialogView(c *ui.Context) {
	t := a.applyTheme(c)
	d := &a.groupDlg
	c.OnWindowShortcut(0, ui.KeyEscape, func() {
		if !d.saving {
			a.closeGroupDialog()
		}
	})

	title, hint := tr("新建分组"), tr("输入一个分组名，会把服务器按组分开展示。")
	switch d.mode {
	case "rename":
		title = tr("重命名分组 ") + d.from
		hint = tr("分组里的服务器会一起改名；连接配置不变。")
	}
	if d.alias != "" && d.mode == "create" {
		hint = tr("创建后会把 ") + d.alias + tr(" 移进这个分组。")
	}

	ui.Column(c).Fill().Background(t.Background).Padding(24).Gap(gapMid).Children(func() {
		ui.Text(c, title).FontSize(fontPage).Bold()
		ui.Text(c, hint).FontSize(fontSmall).TextColor(t.TextMuted)

		name := ui.TextInput(c.Key("grp:name"), &d.value).
			Placeholder(tr("例如：生产 / 测试 / 客户")).AutoFocus()
		if d.err != "" {
			ui.Text(c, d.err).FontSize(fontBody).TextColor(t.Danger)
		}
		if d.saving {
			ui.Text(c, tr("保存中…")).FontSize(fontBody).TextColor(t.TextMuted)
		}

		ui.Spacer(c)
		ui.Row(c).Gap(gapMid).Children(func() {
			if ui.PrimaryButton(c.Key("grp:ok"), tr("保存")).Disabled(d.saving).Clicked() {
				a.saveGroupDialog()
			}
			if ui.Button(c.Key("grp:cancel"), tr("取消")).Disabled(d.saving).Clicked() {
				a.closeGroupDialog()
			}
		})
		if name.Submitted() {
			a.saveGroupDialog()
		}
	})
}

// saveGroupDialog applies the dialog through UpdateConfig, so it is serialised
// with every other catalogue edit.
func (a *App) saveGroupDialog() {
	d := &a.groupDlg
	if d.saving {
		return
	}
	name := strings.TrimSpace(d.value)
	ag := a.agentRef()
	d.err = ""
	switch {
	case name == "":
		d.err = tr("分组名不能为空")
	case ag == nil:
		d.err = tr("agent 未就绪")
	}
	if d.err != "" {
		if a.groupWin != nil {
			a.groupWin.Invalidate()
		}
		return
	}

	d.saving = true
	if a.groupWin != nil {
		a.groupWin.Invalidate()
	}
	mode, from, alias, seq := d.mode, d.from, d.alias, d.seq
	go func() {
		err := ag.UpdateConfig(func(cfg *config.Config) error {
			switch mode {
			case "rename":
				if err := cfg.RenameGroup(from, name); err != nil {
					return err
				}
			default:
				if cfg.GroupExists(name) {
					return fmt.Errorf(tr("分组 %q 已经存在"), name)
				}
				cfg.AddGroup(name)
			}
			if alias != "" {
				h := cfg.Hosts[alias]
				if h == nil {
					return fmt.Errorf(tr("连接 %s 已经不存在"), alias)
				}
				h.Group = name
			}
			return nil
		})
		a.onMain(func() {
			if a.groupDlg.seq != seq {
				// The window was closed or reopened while saving: keep the
				// change, but leave the newer dialog alone.
				a.refreshHosts()
				a.invalidateServers()
				return
			}
			d.saving = false
			if err != nil {
				d.err = err.Error()
				if a.groupWin != nil {
					a.groupWin.Invalidate()
				}
				return
			}
			if mode == "rename" {
				if a.collapsedGroups != nil {
					if folded, ok := a.collapsedGroups[from]; ok {
						delete(a.collapsedGroups, from)
						a.collapsedGroups[name] = folded
					}
				}
			}
			a.closeGroupDialog()
			a.refreshHosts()
			a.invalidateServers()
		})
	}()
}
