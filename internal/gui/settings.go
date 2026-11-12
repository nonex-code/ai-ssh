package gui

import (
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"
)

// settingsItem is one page in the settings window's left column.
type settingsItem struct {
	key, label, hint string
}

var settingsItems = []settingsItem{
	{"language", "语言", "界面语言"},
	{"appearance", "外观", "窗口的浅色 / 深色"},
	{"terminal", "终端", "配色与字体"},
	{"security", "安全", "主密码"},
	{"about", "关于", "版本、更新与开源项目"},
}

func settingsItemFor(key string) settingsItem {
	for _, it := range settingsItems {
		if it.key == key {
			return it
		}
	}
	return settingsItems[1]
}

// openSettings shows the settings window: a list of pages on the left and the
// chosen page on the right. Changes apply at once and go into gui.json; the
// master password is changed by the in-process agent.
func (a *App) openSettings() {
	if a.settings != nil {
		a.settings.Focus()
		return
	}
	if a.window() == nil {
		return // headless: tests render settingsView directly
	}
	if a.settingsTab == "" {
		a.settingsTab = "appearance"
	}
	a.resetPasswd()
	win := mygo.NewWindow(mygo.WindowOptions{
		Title:         tr("ai-ssh 设置"),
		Width:         840,
		Height:        600,
		MinWidth:      680,
		MinHeight:     460,
		DisableResize: false,
		Content:       ui.View(a.settingsView),
	})
	setWindowIcon(win)
	a.settings = win
	win.OnClosed(func() {
		if a.settings == win {
			a.settings = nil
		}
		a.resetPasswd()
	})
	win.Center()
	win.Focus()
}

func (a *App) closeSettings() {
	if a.settings != nil {
		a.settings.Close()
	}
	a.resetPasswd()
}

func (a *App) settingsView(c *ui.Context) {
	t := a.applyTheme(c)
	c.OnWindowShortcut(0, ui.KeyEscape, func() { a.closeSettings() })
	if a.settingsTab == "" {
		a.settingsTab = "appearance"
	}

	ui.Row(c).Fill().Background(t.Background).Children(func() {
		a.settingsSidebar(c, t)
		ui.Box(c).Width(1).FillHeight().Background(t.Border)
		ui.Column(c).Grow(1).FillHeight().Children(func() {
			it := settingsItemFor(a.settingsTab)
			ui.Row(c).Padding(24, 18).Gap(gapMid).AlignItems(ui.Center).Children(func() {
				ui.Text(c, tr(it.label)).FontSize(fontPage).Bold()
				ui.Text(c, tr(it.hint)).FontSize(fontSmall).TextColor(t.TextMuted)
			})
			ui.Divider(c)
			ui.Scroll(c).Grow(1).Padding(24).Gap(gapWide).Children(func() {
				switch a.settingsTab {
				case "language":
					a.languageSettings(c, t)
				case "terminal":
					a.terminalSettings(c, t)
				case "security":
					a.passwordPane(c, t)
				case "about":
					a.aboutSettings(c, t)
				default:
					a.appearanceSettings(c, t)
				}
			})
		})
	})
}

// settingsSidebar is the page list: one flat row per page, the chosen one on a
// card so it stands out without drawing a second navigation bar.
func (a *App) settingsSidebar(c *ui.Context, t *ui.Theme) {
	ui.Column(c).Width(196).FillHeight().Padding(12, 18).Gap(2).Children(func() {
		ui.Row(c).Gap(gapSmall).AlignItems(ui.Center).Padding(8, 6).Children(func() {
			logoMark(c, 20)
			ui.Text(c, tr("设置")).FontSize(fontBody).Bold()
		})
		ui.Spacer(c).Height(10)

		for _, it := range settingsItems {
			active := a.settingsTab == it.key
			row := ui.ButtonBase(c.Key("settings:page:"+it.key)).Label(tr(it.label)).
				FillWidth().Height(38).Padding(12, 0).Radius(radiusTab)
			row.Children(func() {
				color := t.TextMuted
				el := ui.Text(c, tr(it.label)).FontSize(fontBody)
				if active {
					color = t.Text
					el = el.Bold()
				}
				el.TextColor(color)
			})
			if active {
				row.Background(t.Surface).Border(1, t.Border)
			} else if row.Hovered() {
				row.Background(t.SurfaceHover)
			}
			if row.Clicked() && !active {
				a.settingsTab = it.key
				a.invalidateSettings()
			}
		}

		ui.Spacer(c)
		ui.Text(c, "ai-ssh "+strings.TrimSpace(a.opts.Version)).
			FontSize(fontMicro).TextColor(t.TextMuted).Padding(8, 0)
	})
}

func (a *App) languageSettings(c *ui.Context, t *ui.Theme) {
	p := a.prefs.normalized()
	section(c, t, tr("界面语言"))
	keys := []string{"system", "zh", "en"}
	labels := []string{tr("跟随系统"), tr("中文"), "English"}
	sel := 0
	for i, k := range keys {
		if p.Language == k {
			sel = i
		}
	}
	if ui.Segmented(c.Key("settings:language"), &sel, labels...).Changed() {
		a.prefs.Language = keys[sel]
		a.savePrefs()
	}
	ui.Text(c, tr("语言会立即应用到窗口、菜单和对话框。")).
		FontSize(fontSmall).TextColor(t.TextMuted)
}

func (a *App) appearanceSettings(c *ui.Context, t *ui.Theme) {
	p := a.prefs.normalized()
	section(c, t, tr("窗口外观"))
	sel := indexOf(appearanceKeys, p.Appearance)
	if ui.Segmented(c.Key("settings:appearance"), &sel, trAll(appearanceLabels)...).Changed() {
		a.setAppearance(appearanceKeys[sel])
	}
	ui.Text(c, tr("跟随系统会随系统的浅色 / 深色切换；白和黑固定不变。原生标题栏按钮也跟着变。")).
		FontSize(fontSmall).TextColor(t.TextMuted)

	section(c, t, tr("快速切换"))
	ui.Text(c, tr("标签条右侧的太阳 / 月亮按钮点一下就切换浅色与深色；右键可选「跟随系统」或打开设置。")).
		FontSize(fontSmall).TextColor(t.TextMuted)
}

func (a *App) terminalSettings(c *ui.Context, t *ui.Theme) {
	p := a.prefs.normalized()
	section(c, t, tr("终端配色"))
	labels := make([]string, len(terminalPresets))
	chosen := tr(terminalPresets[0].Label)
	for i, tp := range terminalPresets {
		labels[i] = tr(tp.Label)
		if tp.Key == p.Terminal {
			chosen = labels[i]
		}
	}
	if ui.Select(c.Key("settings:terminal"), &chosen, labels).Changed() {
		for _, tp := range terminalPresets {
			if tr(tp.Label) == chosen {
				a.setTerminalTheme(tp.Key)
				break
			}
		}
	}
	light, dark := terminalThemes(p.Terminal)
	palette := light
	if t.Dark {
		palette = dark
	}
	if palette != nil {
		palettePreview(c, palette)
	} else {
		ui.Text(c, tr("跟随窗口的浅色 / 深色外观。")).
			FontSize(fontSmall).TextColor(t.TextMuted)
	}

	section(c, t, tr("字体"))
	ui.Text(c, tr("界面使用内置 Inter（中文回落到系统 UI 字体），终端使用内置 JetBrains Mono；")+
		tr("两者均以 SIL Open Font License 随程序分发。")).
		FontSize(fontSmall).TextColor(t.TextMuted)
}

func (a *App) aboutSettings(c *ui.Context, t *ui.Theme) {
	ui.Row(c).Gap(gapMid).AlignItems(ui.Center).Children(func() {
		logoMark(c, 40)
		ui.Column(c).Gap(2).Children(func() {
			ui.Text(c, "ai-ssh").FontSize(fontCard).Bold()
			ui.Text(c, tr("给 AI 用的 SSH，凭证不出本机")).FontSize(fontSmall).TextColor(t.TextMuted)
		})
	})

	section(c, t, tr("版本"))
	ui.Row(c).FillWidth().Gap(gapMid).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "ai-ssh "+strings.TrimSpace(a.opts.Version)).Font(mono).FontSize(fontSmall)
		ui.Spacer(c)
		if ui.Button(c.Key("upd:check"), tr("检查更新")).
			Disabled(a.updChecking || a.updInstalling).Clicked() {
			a.checkUpdates(false)
		}
	})
	a.updateStatus(c, t)

	section(c, t, tr("自动更新"))
	if ui.Checkbox(c.Key("upd:auto"), &a.autoCheckUpdate, tr("启动时自动检查更新")).Changed() {
		a.updateToggle()
	}
	if ui.Checkbox(c.Key("upd:autoinstall"), &a.autoInstallUpdate,
		tr("发现新版本时自动下载并安装（重启后生效）")).Disabled(!a.autoCheckUpdate).Changed() {
		a.updateToggle()
	}
	ui.Text(c, tr("更新从 GitHub Releases 获取：先校验压缩包的 SHA256SUMS.txt，再替换当前可执行文件；")+
		tr("运行中的进程会在下次启动时使用新版本。")).
		FontSize(fontMicro).TextColor(t.TextMuted)

	section(c, t, tr("设计"))
	ui.Text(c, tr("窗口、CLI、MCP 和 agent 是同一个二进制；主密码只进入本机 agent，")+
		tr("服务器口令、私钥和提权口令都只存在 Argon2id + AES-256-GCM 加密的 vault 里。")).
		FontSize(fontSmall).TextColor(t.TextMuted)

	section(c, t, tr("开源项目"))
	ui.Text(c, tr("这个程序使用了下面这些开源项目；完整的版本与校验和见 go.mod / go.sum。")).
		FontSize(fontMicro).TextColor(t.TextMuted)
	for _, cr := range credits {
		ui.Row(c).FillWidth().Gap(gapSmall).AlignItems(ui.Center).Children(func() {
			ui.Link(c, cr.name, cr.url).FontSize(fontBody)
			ui.Text(c, cr.license).FontSize(fontMicro).TextColor(t.TextMuted)
			ui.Spacer(c)
			ui.Text(c, cr.use).FontSize(fontMicro).TextColor(t.TextMuted)
		})
	}
	ui.Text(c, tr("字体许可证全文随发布包分发：OFL-Inter.txt、OFL-JetBrainsMono.txt。")).
		FontSize(fontMicro).TextColor(t.TextMuted)
}

// updateStatus paints the current state of the update check in the About page.
func (a *App) updateStatus(c *ui.Context, t *ui.Theme) {
	switch {
	case a.updInstalling:
		ui.Text(c, tr("正在下载并安装更新…")).FontSize(fontSmall).TextColor(t.TextMuted)
		return
	case a.updChecking:
		ui.Text(c, tr("正在检查更新…")).FontSize(fontSmall).TextColor(t.TextMuted)
		return
	}
	if a.updErr != "" {
		ui.Text(c, a.updErr).FontSize(fontSmall).TextColor(t.Danger)
	}
	if a.updDone {
		ui.Text(c, tr("更新已安装，重启 ai-ssh 后生效。")).FontSize(fontSmall).TextColor(t.Success)
		if ui.PrimaryButton(c.Key("upd:restart"), tr("重启并应用更新")).Clicked() {
			mygo.App.Relaunch()
		}
		return
	}
	if a.upd == nil {
		if !a.updLastCheck.IsZero() {
			ui.Text(c, tr("上次检查：")+a.updLastCheck.Local().Format("2006-01-02 15:04")).
				FontSize(fontMicro).TextColor(t.TextMuted)
		}
		return
	}
	if !a.upd.Available {
		ui.Text(c, tr("已是最新版本（")+a.upd.Version+"）。").FontSize(fontSmall).TextColor(t.Success)
		return
	}
	ui.Text(c, tr("发现新版本 ")+a.upd.Version+tr("（当前 ")+strings.TrimSpace(a.opts.Version)+tr("）。")).
		FontSize(fontBody).TextColor(t.Accent)
	ui.Row(c).Gap(gapMid).Children(func() {
		if ui.PrimaryButton(c.Key("upd:install"), tr("下载并安装")).
			Disabled(a.updInstalling).Clicked() {
			a.installUpdate()
		}
		if ui.Button(c.Key("upd:page"), tr("打开 Release 页面")).Clicked() && a.upd.PageURL != "" {
			c.OpenURL(a.upd.PageURL)
		}
	})
}

func (a *App) invalidateSettings() {
	if a.settings != nil {
		a.settings.Invalidate()
	}
}

func section(c *ui.Context, t *ui.Theme, label string) {
	ui.Text(c, label).FontSize(fontSmall).Bold().TextColor(t.TextMuted)
}

// palettePreview shows the 16 program colors of a terminal theme.
func palettePreview(c *ui.Context, th *terminal.Theme) {
	ui.Row(c).Gap(3).Children(func() {
		for _, col := range th.Palette {
			ui.Box(c).Size(12, 12).Radius(3).Background(col)
		}
	})
}

// themeButton is the strip's quick theme switch. A click flips light and dark
// at once; the context menu offers "follow the system" and the settings window.
func (a *App) themeButton(c *ui.Context, t *ui.Theme) {
	name, label := "moon", tr("切换到深色主题")
	if t.Dark {
		name, label = "sun", tr("切换到浅色主题")
	}
	el := ui.ButtonBase(c.Key("strip:theme")).Label(label).
		Tooltip(label + tr("（右键：主题选项与设置）")).
		Width(controlH).Height(controlH).Radius(radiusTab)
	el.Children(func() {
		ui.Icon(c, icon(name)).Size(iconSize, iconSize).TextColor(t.TextMuted)
	})
	if el.Hovered() {
		el.Background(t.SurfaceHover)
	}
	el.ContextMenu(func(m *ui.Menu) {
		for i, key := range appearanceKeys {
			it := m.Item(appearanceLabels[i]).Radio(a.prefs.normalized().Appearance == key)
			if it.Chosen() {
				a.setAppearance(key)
			}
		}
		m.Separator()
		if m.Item(tr("打开设置…")).Chosen() {
			a.openSettings()
		}
	})
	if el.Clicked() {
		if t.Dark {
			a.setAppearance("light")
		} else {
			a.setAppearance("dark")
		}
	}
}

// --- changing preferences ----------------------------------------------------

func (a *App) setAppearance(key string) {
	a.prefs.Appearance = key
	a.savePrefs()
}

func (a *App) setTerminalTheme(key string) {
	a.prefs.Terminal = key
	a.savePrefs()
	light, dark := terminalThemes(a.prefs.Terminal)
	for _, tb := range a.tabs {
		if tb != nil && tb.term != nil {
			tb.term.SetTheme(light, dark)
		}
	}
}

// savePrefs normalises, applies and persists the choice, then repaints every
// window. Main thread only.
func (a *App) savePrefs() {
	a.prefs = a.prefs.normalized()
	setLanguage(a.prefs.Language)
	if a.window() != nil {
		mygo.Theme.SetSource(mygo.ThemeSource(a.prefs.source()))
	}
	a.saveGeometry()
	a.invalidate()
	a.invalidateSettings()
	if a.popup != nil {
		a.popup.Invalidate()
	}
	if a.editor != nil {
		a.editor.Invalidate()
	}
	if a.servers != nil {
		a.servers.Invalidate()
	}
	if a.activityWin != nil {
		a.activityWin.Invalidate()
	}
	if a.hostKeyWin != nil {
		a.hostKeyWin.Invalidate()
	}
	if a.confirmWin != nil {
		a.confirmWin.Invalidate()
	}
	if a.groupWin != nil {
		a.groupWin.Invalidate()
	}
}
