package gui

import (
	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"

	"github.com/nonex-code/ai-ssh/internal/localshell"
)

// detectLocalShells probes the machine off the main thread: on Windows it asks
// WSL for its distributions, which can take a moment. The window repaints when
// the list is ready.
func (a *App) detectLocalShells() {
	shells := localshell.Detect()
	a.onMain(func() {
		a.localShells = shells
		a.invalidate()
	})
}

// localShellChoices is always usable: before detection finishes it offers the
// platform's default shell, so the button never opens an empty menu.
func (a *App) localShellChoices() []localshell.Shell {
	if len(a.localShells) > 0 {
		return a.localShells
	}
	return []localshell.Shell{localshell.Default()}
}

// openLocalTerminal starts a shell on this machine in a new tab. It is the one
// session kind that needs neither the agent nor the vault: the terminal widget
// runs the program itself, in a PTY, as a desktop terminal would.
func (a *App) openLocalTerminal(sh localshell.Shell) {
	if sh.Label == "" {
		sh.Label = tr("本地终端")
	}
	tb := &terminalTab{
		id:          a.nextTab,
		host:        tr(sh.Label),
		label:       tr(sh.Label),
		status:      tabConnecting,
		opening:     true,
		forwardPane: newForwardPane(),
		localShell:  &sh,
	}
	a.nextTab++
	a.tabs = append(a.tabs, tb)
	a.activeTab = len(a.tabs) - 1
	a.invalidate()

	a.onMain(func() {
		if tb.closed {
			return
		}
		a.attachTerminal(tb, terminal.Options{Command: sh.Command})
	})
}

// duplicateTab opens another session of the same kind: the same remote host, or
// the same local shell.
func (a *App) duplicateTab(tb *terminalTab) {
	if tb.isLocal() {
		a.openLocalTerminal(*tb.localShell)
		return
	}
	a.openTerminal(tb.host)
}

// localShellRow is the home page's list of shells on this machine: one click
// starts one in a new tab, without touching the vault or the network.
func (a *App) localShellRow(c *ui.Context, t *ui.Theme) {
	shells := a.localShellChoices()
	if len(shells) == 0 {
		return
	}
	ui.Column(c).Gap(2).Margin(0, gapSmall, 0, 0).Children(func() {
		ui.Text(c, tr("本地终端")).FontSize(fontSmall).TextColor(t.TextMuted)
		ui.ScrollHorizontal(c).FillWidth().Height(controlH).Gap(gapSmall).Scrollbars(false).Children(func() {
			for _, sh := range shells {
				el := ui.ButtonBase(c.Key("local:"+sh.Key)).Label(tr("本地 ")+tr(sh.Label)).
					Tooltip(tr("在本机打开 ")+tr(sh.Label)).
					Height(controlH).Padding(10, 0).Radius(radiusTab)
				el.Children(func() {
					ui.Icon(c, icon("terminal")).Size(iconSize, iconSize).TextColor(t.TextMuted)
					ui.Text(c, tr(sh.Label)).FontSize(fontBody)
				})
				if el.Hovered() {
					el.Background(t.SurfaceHover)
				}
				if el.Clicked() {
					a.openLocalTerminal(sh)
				}
			}
		})
	})
}
