package gui

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/nonex-code/ai-ssh/internal/client"
	"github.com/nonex-code/ai-ssh/internal/config"
	"github.com/nonex-code/ai-ssh/internal/daemon"
	"github.com/nonex-code/ai-ssh/internal/proto"
)

// editorDraft is the form's state. It holds what the user typed, including the
// "env:NAME" / "file:PATH" references, never a resolved secret.
type editorDraft struct {
	alias   string
	addr    string
	user    string
	group   string
	auth    string // key | password
	key     string
	pass    string // key passphrase reference
	secret  string // password reference
	hostKey string
	workdir string
	elev    string // none | sudo | su
	elevUsr string
	elevPwd string

	fav bool
	// forwards is edited as rows, not as text, so a typo cannot silently
	// become a tunnel; kind is local, remote or dynamic.
	forwards []config.Forward

	// The optional sections start closed, and open themselves when the host
	// already has something in them.
	showAdvanced bool
	showElevate  bool
	showTunnels  bool
}

// openEditor shows the connection form: a new host when alias is empty,
// otherwise that host for editing.
func (a *App) openEditor(alias string) {
	draft := editorDraft{auth: "key", hostKey: "known_hosts", elev: "none"}
	if alias != "" {
		if ag := a.agentRef(); ag != nil {
			if h, ok := ag.Config().Hosts[alias]; ok && h != nil {
				draft = draftOf(alias, h)
			}
		}
	}
	a.openEditorDraft(draft, alias, false)
}

// openEditorDraft shows the form with a prepared draft. connectOnSave opens a
// terminal as soon as the connection is stored.
func (a *App) openEditorDraft(draft editorDraft, alias string, connectOnSave bool) {
	a.editAlias = alias
	a.editErr = ""
	a.saving = false
	a.testing = false
	a.testMsg = ""
	a.testOK = false
	a.draft = draft
	a.connectOnSave = connectOnSave

	if a.editor != nil {
		a.editor.Invalidate()
		a.editor.Focus()
		return
	}
	if a.window() == nil {
		return // headless: tests render editorView directly
	}

	win := mygo.NewWindow(mygo.WindowOptions{
		Title:         tr("ai-ssh 连接"),
		Width:         540,
		Height:        660,
		MinWidth:      440,
		MinHeight:     520,
		DisableResize: false,
		Content:       ui.View(a.editorView),
	})
	setWindowIcon(win)
	a.editor = win
	win.OnClosed(func() {
		if a.editor == win {
			a.editor = nil
		}
	})
	win.Center()
	win.Focus()
}

func draftOf(alias string, h *config.Host) editorDraft {
	d := editorDraft{
		alias:   alias,
		addr:    h.Addr,
		user:    h.User,
		group:   h.Group,
		auth:    h.Auth.Type,
		key:     h.Auth.Key,
		pass:    h.Auth.PassphraseRef(),
		secret:  h.Auth.PasswordRef(),
		hostKey: h.HostKey,
		workdir: h.Workdir,
		elev:    "none",
		fav:     h.Favorite,
	}
	d.forwards = append([]config.Forward(nil), h.Forwards...)
	d.showTunnels = len(d.forwards) > 0
	if h.Elevate != nil && h.Elevate.Mode != "" && h.Elevate.Mode != "none" {
		d.elev = h.Elevate.Mode
		d.elevUsr = h.Elevate.User
		d.elevPwd = h.Elevate.PasswordRef()
		d.showElevate = true
	}
	if (h.HostKey != "" && h.HostKey != "known_hosts") || h.Workdir != "" {
		d.showAdvanced = true
	}
	return d
}

// tunnelFieldError marks a bad tunnel row, so the form can open the tunnel
// section without matching on a translated message.
type tunnelFieldError struct{ err error }

func (e *tunnelFieldError) Error() string { return e.err.Error() }
func (e *tunnelFieldError) Unwrap() error { return e.err }

// validateForwards checks each tunnel row before anything is written.
func validateForwards(list []config.Forward) error {
	for i, f := range list {
		where := fmt.Sprintf(tr("隧道 %d"), i+1)
		switch f.Kind {
		case "local", "remote", "dynamic":
		default:
			return fmt.Errorf(tr("%s：类型只能是 local / remote / dynamic"), where)
		}
		if _, _, err := net.SplitHostPort(f.Listen); err != nil {
			return fmt.Errorf(tr("%s：监听要写 host:port"), where)
		}
		if f.Kind == "dynamic" {
			continue
		}
		if _, _, err := net.SplitHostPort(f.Target); err != nil {
			return fmt.Errorf(tr("%s：目标要写 host:port"), where)
		}
	}
	return nil
}

// pickKeyFile asks the system for a private key and puts its path in the form.
func (a *App) pickKeyFile() {
	win := a.editor
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent: win,
			Title:  tr("选择私钥"),
			Filters: []mygo.FileFilter{
				{Name: tr("私钥"), Extensions: []string{"pem", "key", "ppk", "*"}},
			},
		})
		if err != nil || len(paths) == 0 {
			return
		}
		a.onMain(func() {
			a.draft.key = shortenHome(paths[0])
			if a.editor != nil {
				a.editor.Invalidate()
			}
		})
	}()
}

// shortenHome writes a path under the home directory as ~/..., as the rest of
// the config does.
func shortenHome(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if p == home {
		return "~"
	}
	if strings.HasPrefix(p, home+string(os.PathSeparator)) {
		return "~" + p[len(home):]
	}
	return p
}

func (a *App) closeEditor() {
	if a.editor == nil {
		return
	}
	win := a.editor
	a.editor = nil
	win.Close()
}

func (a *App) closeEditorSoon() {
	win := a.editor
	a.editor = nil
	if win != nil {
		// Closing from inside the window's own frame is unsafe.
		go win.Close()
	}
}

// hostFromDraft builds the host the form describes. It starts from the host
// as it is, so fields the form does not show — the approval mode, timeouts,
// deny/allow rules, the elevate policy's lists, the auth certificate — survive
// an edit.
func hostFromDraft(existing *config.Host, d editorDraft) (*config.Host, error) {
	forwards := append([]config.Forward(nil), d.forwards...)
	for i := range forwards {
		forwards[i].Listen = strings.TrimSpace(forwards[i].Listen)
		forwards[i].Target = strings.TrimSpace(forwards[i].Target)
	}
	if err := validateForwards(forwards); err != nil {
		return nil, err
	}

	h := &config.Host{}
	if existing != nil {
		*h = *existing
	}
	h.Addr = strings.TrimSpace(d.addr)
	h.User = strings.TrimSpace(d.user)
	h.Group = strings.TrimSpace(d.group)
	h.Favorite = d.fav
	h.Forwards = forwards
	h.HostKey = strings.TrimSpace(d.hostKey)
	h.Workdir = strings.TrimSpace(d.workdir)

	cert, userName := "", ""
	if existing != nil {
		cert, userName = existing.Auth.Cert, existing.Auth.Username
	}
	switch d.auth {
	case "password":
		h.Auth = config.Auth{Type: "password", Password: strings.TrimSpace(d.secret)}
	default:
		h.Auth = config.Auth{
			Type:       "key",
			Key:        strings.TrimSpace(d.key),
			Passphrase: strings.TrimSpace(d.pass),
		}
	}
	h.Auth.Cert, h.Auth.Username = cert, userName

	if d.elev == "" || d.elev == "none" {
		h.Elevate = nil
	} else {
		var e config.Elevate
		if existing != nil && existing.Elevate != nil {
			e = *existing.Elevate
		}
		e.Mode = d.elev
		e.User = strings.TrimSpace(d.elevUsr)
		e.Password = strings.TrimSpace(d.elevPwd)
		h.Elevate = &e
	}
	return h, nil
}

// applyDraft validates the form and writes it into cfg, returning the final
// alias. It is pure, so the rules can be tested without a running agent.
func applyDraft(cfg *config.Config, editAlias string, d editorDraft) (string, error) {
	alias := strings.TrimSpace(d.alias)
	switch {
	case alias == "":
		return "", errors.New(tr("名称不能为空"))
	case editAlias == "" && cfg.Hosts[alias] != nil:
		return "", errors.New(tr("名称已被占用"))
	case editAlias != "" && alias != editAlias && cfg.Hosts[alias] != nil:
		return "", errors.New(tr("名称已被占用"))
	}
	h, err := hostFromDraft(cfg.Hosts[editAlias], d)
	if err != nil {
		return "", err
	}
	if editAlias != "" && alias != editAlias {
		delete(cfg.Hosts, editAlias)
	}
	cfg.Hosts[alias] = h
	if h.Group != "" {
		// Keep the group in the saved order, and remember a group typed for
		// the first time even before another server joins it.
		cfg.AddGroup(h.Group)
	}
	return alias, nil
}

// draftHost resolves the form the way a saved host is resolved: paths are
// expanded and env:/file: references are read, so a test dials exactly what
// saving would store.
func (a *App) draftHost() (*config.Host, error) {
	ag := a.agentRef()
	if ag == nil {
		return nil, errors.New(tr("agent 未就绪"))
	}
	var existing *config.Host
	if a.editAlias != "" {
		existing = ag.Config().Hosts[a.editAlias]
	}
	h, err := hostFromDraft(existing, a.draft)
	if err != nil {
		return nil, err
	}
	tmp := &config.Config{Hosts: map[string]*config.Host{"t": h}}
	data, err := tmp.Marshal()
	if err != nil {
		return nil, err
	}
	parsed, err := config.Parse(data, filepath.Dir(a.opts.VaultPath))
	if err != nil {
		return nil, err
	}
	return parsed.Hosts["t"], nil
}

// testDraft dials the host the form describes, without saving it.
func (a *App) testDraft() {
	if a.testing || a.saving {
		return
	}
	ag := a.agentRef()
	if ag == nil {
		a.editErr = tr("agent 未就绪")
		a.invalidate()
		return
	}
	if strings.TrimSpace(a.draft.alias) == "" {
		a.editErr = tr("名称不能为空")
		a.invalidate()
		return
	}
	h, err := a.draftHost()
	if err != nil {
		a.editErr = err.Error()
		a.invalidate()
		return
	}
	a.editErr = ""
	a.testing, a.testMsg, a.testOK = true, tr("测试中…"), false
	a.invalidate()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		err := ag.TestHost(ctx, h)
		a.onMain(func() {
			a.testing = false
			var unk *daemon.UnknownHostKeyError
			if errors.As(err, &unk) {
				a.testMsg = tr("主机密钥不在 known_hosts，先固定指纹再测")
				a.testOK = false
				a.invalidate()
				a.promptHostKeyFor(a.draft.addr, unk, func(fp string) {
					a.draft.hostKey = fp
					a.testMsg = tr("已固定指纹，再点一次「测试连接」")
					a.testOK = false
					a.invalidate()
				})
				return
			}
			var mm *daemon.HostKeyMismatchError
			if errors.As(err, &mm) {
				a.testMsg = tr("主机密钥变了：服务器出示 ") + mm.Got + tr("，连接里固定的是 ") + mm.Want +
					tr("。核对后把 host key 改成新指纹再测")
				a.testOK = false
				a.invalidate()
				return
			}
			if err != nil {
				a.testMsg, a.testOK = err.Error(), false
			} else {
				a.testMsg, a.testOK = tr("连通正常：TCP、host key 和认证都通过"), true
			}
			a.invalidate()
		})
	}()
}

// saveEditor validates through the same parser the daemon uses, writes the
// vault, and reloads the running agent. The read-modify-write runs through
// UpdateConfig, so it cannot race another save (a delete in the manager, say).
func (a *App) saveEditor() {
	if a.saving {
		return
	}
	ag := a.agentRef()
	if ag == nil {
		a.editErr = tr("agent 未就绪")
		a.invalidate()
		return
	}
	d := a.draft
	editAlias := a.editAlias
	a.saving = true
	a.editErr = ""
	a.invalidate()

	alias := ""
	go func() {
		err := ag.UpdateConfig(func(cfg *config.Config) error {
			var derr error
			alias, derr = applyDraft(cfg, editAlias, d)
			return derr
		})
		a.onMain(func() {
			a.saving = false
			if err != nil {
				a.editErr = err.Error()
				var tunnelErr *tunnelFieldError
				if errors.As(err, &tunnelErr) {
					a.draft.showTunnels = true
				}
				a.invalidate()
				return
			}
			a.closeEditorSoon()
			a.refreshHosts()
			if a.connectOnSave {
				a.connectOnSave = false
				a.openTerminal(alias)
			}
		})
	}()
}

// confirmDeleteFromEditor asks in the app's window before deleting the
// connection the form is editing.
func (a *App) confirmDeleteFromEditor() {
	alias := a.editAlias
	if alias == "" {
		return
	}
	a.askConfirm(confirmRequest{
		title:        tr("删除连接 ") + alias,
		message:      tr("删除后它的认证、提权口令和隧道配置会一起消失，不能撤销。"),
		detail:       tr("正在运行的会话不会被断开。"),
		confirmLabel: tr("删除"),
		onConfirm:    func() { a.deleteHost() },
	})
}

func (a *App) deleteHost() {
	alias := a.editAlias
	if alias == "" || a.saving {
		return
	}
	ag := a.agentRef()
	if ag == nil {
		return
	}
	a.saving = true
	a.editErr = ""
	a.invalidate()
	go func() {
		err := ag.UpdateConfig(func(cfg *config.Config) error {
			if _, ok := cfg.Hosts[alias]; !ok {
				return fmt.Errorf(tr("连接 %s 已经不存在"), alias)
			}
			delete(cfg.Hosts, alias)
			return nil
		})
		a.onMain(func() {
			a.saving = false
			if err != nil {
				a.editErr = err.Error()
				a.invalidate()
				return
			}
			a.closeEditorSoon()
			a.refreshHosts()
			a.invalidateServers()
		})
	}()
}

// refreshHosts re-reads the catalogue through the daemon so the window shows
// exactly what an AI call would see.
func (a *App) refreshHosts() {
	ep := a.opts.Endpoint
	go func() {
		resp := client.Call(context.Background(), ep, proto.Request{Op: proto.OpHosts})
		if resp.Error != nil {
			return
		}
		a.onMain(func() {
			a.mu.Lock()
			a.hosts = resp.Hosts
			a.mu.Unlock()
			a.invalidate()
		})
	}()
}

// --- the form ----------------------------------------------------------------

func (a *App) editorView(c *ui.Context) {
	t := a.applyTheme(c)
	d := &a.draft

	// A form closes on Escape, as every dialog does.
	c.OnWindowShortcut(0, ui.KeyEscape, func() { a.closeEditor() })

	title := tr("新增连接")
	if a.editAlias != "" {
		title = tr("编辑 ") + a.editAlias
	}

	// Widgets are built inside the container they belong to; this one is
	// checked for Enter after the form is built.
	var name ui.Element

	ui.Column(c).Fill().Background(t.Background).Children(func() {
		ui.Row(c).Padding(20, 16).Gap(gapMid).Children(func() {
			ui.Text(c, title).FontSize(fontPage).Bold()
			ui.Spacer(c)
			if a.saving {
				ui.Text(c, tr("保存中…")).FontSize(fontSmall).TextColor(t.TextMuted)
			}
		})
		ui.Divider(c)

		ui.Scroll(c).Grow(1).Padding(gapWide, gapWide, gapMid, gapWide).Gap(gapSmall).Children(func() {
			section(c, t, tr("连接"))

			ui.Row(c).FillWidth().Gap(gapMid).Children(func() {
				a.fieldGrow(c, t, tr("名称"), func() ui.Element {
					name = ui.TextInput(c.Key("f:alias"), &d.alias).Placeholder("prod")
					return name
				})
				a.fieldGrow(c, t, tr("用户"), func() ui.Element {
					return ui.TextInput(c.Key("f:user"), &d.user).Placeholder("deploy")
				})
			})

			a.field(c, t, tr("地址"), func() ui.Element {
				return ui.TextInput(c.Key("f:addr"), &d.addr).Placeholder("10.0.0.5:22")
			})

			a.field(c, t, tr("分组"), func() ui.Element {
				return ui.Autocomplete(c.Key("f:group"), &d.group, a.groupChoices()).
					Placeholder(tr("可空；输入新分组名或选择已有分组"))
			})

			a.field(c, t, tr("认证"), func() ui.Element {
				return ui.Select(c.Key("f:auth"), &d.auth, []string{"key", "password"})
			})

			if d.auth == "password" {
				a.field(c, t, tr("密码"), func() ui.Element {
					return ui.TextInput(c.Key("f:secret"), &d.secret).
						Placeholder(tr("env:PROD_PASSWORD（也可直接填写）")).Password().
						Tooltip(tr("建议写 env:NAME 或 file:PATH，明文只存在加密 vault 里"))
				})
			} else {
				a.field(c, t, tr("私钥"), func() ui.Element {
					return ui.Row(c).FillWidth().Gap(gapSmall).Children(func() {
						ui.TextInput(c.Key("f:key"), &d.key).
							Placeholder("~/.ssh/id_ed25519").Grow(1)
						if ui.Button(c.Key("f:pick"), tr("选择…")).Clicked() {
							a.pickKeyFile()
						}
					})
				})
				a.field(c, t, tr("口令"), func() ui.Element {
					return ui.TextInput(c.Key("f:pass"), &d.pass).
						Placeholder(tr("密钥口令（可空）")).Password().
						Tooltip(tr("建议写 env:NAME 或 file:PATH"))
				})
			}

			ui.Collapsible(c, tr("高级"), &d.showAdvanced, func() {
				ui.Row(c).FillWidth().Gap(gapMid).Children(func() {
					a.fieldGrow(c, t, "host key", func() ui.Element {
						return ui.Select(c.Key("f:hostkey"), &d.hostKey,
							[]string{"known_hosts", "insecure"})
					})
					a.fieldGrow(c, t, "workdir", func() ui.Element {
						return ui.TextInput(c.Key("f:workdir"), &d.workdir).
							Placeholder(tr("/srv/app（远端，可空）"))
					})
				})
				if strings.HasPrefix(d.hostKey, "SHA256:") {
					ui.Text(c, tr("固定指纹：")+d.hostKey).FontSize(fontSmall).TextColor(t.TextMuted)
				}
				ui.Text(c, tr("host key 默认对照 ~/.ssh/known_hosts；insecure 关闭校验，只用于测试机。")).
					FontSize(fontSmall).TextColor(t.TextMuted)
			})

			ui.Collapsible(c, tr("提权（可选）"), &d.showElevate, func() {
				ui.Row(c).FillWidth().Gap(gapMid).Children(func() {
					a.fieldGrow(c, t, tr("方式"), func() ui.Element {
						return ui.Select(c.Key("f:elev"), &d.elev, []string{"none", "sudo", "su"})
					})
					if d.elev == "sudo" || d.elev == "su" {
						a.fieldGrow(c, t, tr("目标用户"), func() ui.Element {
							return ui.TextInput(c.Key("f:elevuser"), &d.elevUsr).
								Placeholder(tr("root（可空）"))
						})
						a.fieldGrow(c, t, tr("提权口令"), func() ui.Element {
							return ui.TextInput(c.Key("f:elevpwd"), &d.elevPwd).
								Placeholder("env:PROD_SUDO").Password().
								Tooltip(tr("建议写 env:NAME 或 file:PATH"))
						})
					}
				})
				ui.Text(c, tr("只有这里开了 sudo/su，AI 才能用 --sudo；allow/deny 规则在 config edit 里配。")).
					FontSize(fontSmall).TextColor(t.TextMuted)
			})

			ui.Collapsible(c, tr("隧道（可选）"), &d.showTunnels, func() {
				remove := -1
				for i := range d.forwards {
					fw := &d.forwards[i]
					idx := i
					ui.Row(c).FillWidth().Gap(gapSmall).AlignItems(ui.Center).Children(func() {
						ui.Select(c.Key(fmt.Sprintf("f:fwkind:%d", idx)), &fw.Kind,
							[]string{"local", "remote", "dynamic"}).Width(112)
						ui.TextInput(c.Key(fmt.Sprintf("f:fwlisten:%d", idx)), &fw.Listen).
							Placeholder("127.0.0.1:5432").Grow(1)
						ui.TextInput(c.Key(fmt.Sprintf("f:fwtarget:%d", idx)), &fw.Target).
							Placeholder("db.internal:5432").Grow(1).
							Disabled(fw.Kind == "dynamic")
						del := ui.ButtonBase(c.Key(fmt.Sprintf("f:fwdel:%d", idx))).
							Label(tr("删除这条隧道")).Tooltip(tr("删除这条隧道")).
							Width(26).Height(26).Radius(radiusTab)
						del.Children(func() {
							ui.Icon(c, icon("x")).Size(12, 12).TextColor(t.TextMuted)
						})
						if del.Clicked() {
							remove = idx
						}
					})
				}
				if remove >= 0 {
					d.forwards = append(d.forwards[:remove], d.forwards[remove+1:]...)
				}
				if ui.Button(c.Key("f:fwadd"), tr("添加隧道")).Clicked() {
					d.forwards = append(d.forwards, config.Forward{Kind: "local"})
				}
			})

		})

		if a.testMsg != "" {
			ui.Row(c).Padding(20, 6).Children(func() {
				color := t.Danger
				if a.testOK {
					color = t.Success
				}
				ui.Text(c, a.testMsg).FontSize(fontSmall).TextColor(color)
			})
		}
		if a.editErr != "" {
			ui.Row(c).Padding(20, 6).Children(func() {
				ui.Text(c, a.editErr).FontSize(fontSmall).TextColor(t.Danger)
			})
		}
		ui.Divider(c)
		ui.Row(c).Padding(20, 12).Gap(gapMid).Children(func() {
			if ui.PrimaryButton(c.Key("save"), tr("保存")).Disabled(a.saving || a.testing).Clicked() {
				a.saveEditor()
			}
			if ui.Button(c.Key("cancel"), tr("取消")).Clicked() {
				a.closeEditor()
			}
			if ui.Button(c.Key("test"), tr("测试连接")).Disabled(a.saving || a.testing).Clicked() {
				a.testDraft()
			}
			ui.Spacer(c)
			if a.editAlias != "" {
				if ui.Button(c.Key("delete"), tr("删除连接")).Disabled(a.saving || a.testing).Clicked() {
					a.confirmDeleteFromEditor()
				}
			}
		})
	})

	if name.Submitted() {
		a.saveEditor()
	}
}

// field is one labelled input, the label above it: the form reads down a
// single column, so the window stays narrow. build makes the widget in the
// column it belongs to.
func (a *App) field(c *ui.Context, t *ui.Theme, label string, build func() ui.Element) {
	ui.Column(c).FillWidth().Gap(4).Children(func() {
		ui.Text(c, label).FontSize(fontSmall).TextColor(t.TextMuted)
		build().FillWidth()
	})
}

// fieldGrow is field inside a row, sharing the width with its neighbour.
func (a *App) fieldGrow(c *ui.Context, t *ui.Theme, label string, build func() ui.Element) {
	ui.Column(c).Grow(1).Gap(4).Children(func() {
		ui.Text(c, label).FontSize(fontSmall).TextColor(t.TextMuted)
		build().FillWidth()
	})
}
