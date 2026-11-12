package gui

import (
	"context"
	"fmt"

	"github.com/egoist/mygo/ui"

	"github.com/nonex-code/ai-ssh/internal/agent"
)

// forwardPane is the tunnel panel of one session tab.
type forwardPane struct {
	open   bool
	kind   string
	listen string
	target string
	err    string
	list   []*agent.Forward
}

func newForwardPane() forwardPane {
	return forwardPane{kind: agent.ForwardLocal, listen: "127.0.0.1:8080"}
}

func (a *App) showForward(tb *terminalTab) {
	tb.filesPane.open = false
	tb.monitorPane.open = false
	a.stopMonitor(tb)
	tb.forwardPane.open = true
	a.invalidate()
}

func (a *App) showFiles(tb *terminalTab) {
	tb.forwardPane.open = false
	a.stopMonitor(tb)
	a.toggleFiles(tb)
}

func (a *App) showTerminal(tb *terminalTab) {
	tb.filesPane.open = false
	tb.forwardPane.open = false
	// The monitor owns the pane while it samples: leave it, or the view
	// keeps showing it (it is checked before the terminal).
	a.stopMonitor(tb)
	a.invalidate()
}

// startForward opens the tunnel described by the form.
func (a *App) startForward(tb *terminalTab) {
	ag := a.agentRef()
	fp := &tb.forwardPane
	if ag == nil {
		fp.err = tr("agent 未就绪")
		a.invalidate()
		return
	}
	kind := fp.kind
	listen := fp.listen
	target := fp.target
	fp.err = ""
	a.invalidate()

	go func() {
		fwd, err := ag.StartForward(context.Background(), tb.host, kind, listen, target)
		a.onMain(func() {
			if err != nil {
				fp.err = err.Error()
				a.invalidate()
				return
			}
			fp.list = append(fp.list, fwd)
			a.invalidate()
		})
	}()
}

func (a *App) stopForward(tb *terminalTab, i int) {
	fp := &tb.forwardPane
	if i < 0 || i >= len(fp.list) {
		return
	}
	fwd := fp.list[i]
	fp.list = append(fp.list[:i], fp.list[i+1:]...)
	a.invalidate()
	go func() { _ = fwd.Close() }()
}

// startConfiguredForwards opens the tunnels the host carries in the vault, so a
// saved rule is live with the session.
func (a *App) startConfiguredForwards(tb *terminalTab) {
	ag := a.agentRef()
	if ag == nil {
		return
	}
	h, ok := ag.Config().Hosts[tb.host]
	if !ok || h == nil || len(h.Forwards) == 0 {
		return
	}
	for _, fw := range h.Forwards {
		fw := fw
		go func() {
			f, err := ag.StartForward(context.Background(), tb.host, fw.Kind, fw.Listen, fw.Target)
			a.onMain(func() {
				if err != nil {
					tb.forwardPane.err = fmt.Sprintf(tr("%s %s：%v"), fw.Kind, fw.Listen, err)
					a.invalidate()
					return
				}
				tb.forwardPane.list = append(tb.forwardPane.list, f)
				a.invalidate()
			})
		}()
	}
}

func (a *App) closeForwards(tb *terminalTab) {
	for _, fwd := range tb.forwardPane.list {
		_ = fwd.Close()
	}
	tb.forwardPane.list = nil
}

// --- view --------------------------------------------------------------------

func (a *App) forwardView(c *ui.Context, t *ui.Theme, tb *terminalTab) {
	fp := &tb.forwardPane

	ui.Scroll(c).Grow(1).FillWidth().Padding(gapWide).Gap(gapMid).Children(func() {
		ui.Text(c, tr("端口转发")).FontSize(fontCard).Bold()
		ui.Text(c, tr("本地 (-L) 在这里监听、从服务器出站；动态 (-D) 是 SOCKS5 代理；远程 (-R) 在服务器上监听、回到这里。")).
			FontSize(fontSmall).TextColor(t.TextMuted)

		ui.Row(c).Gap(gapSmall).Children(func() {
			ui.Text(c, tr("类型")).Width(48).FontSize(fontBody).TextColor(t.TextMuted)
			ui.Select(c.Key("fw:kind"), &fp.kind, []string{
				agent.ForwardLocal, agent.ForwardDynamic, agent.ForwardRemote,
			}).Width(120)
			ui.Text(c, tr("监听")).Width(40).FontSize(fontBody).TextColor(t.TextMuted)
			ui.TextInput(c.Key("fw:listen"), &fp.listen).Placeholder("127.0.0.1:8080").Grow(1)
		})
		if fp.kind != agent.ForwardDynamic {
			ui.Row(c).Gap(gapSmall).Children(func() {
				ui.Text(c, tr("目标")).Width(48).FontSize(fontBody).TextColor(t.TextMuted)
				ui.TextInput(c.Key("fw:target"), &fp.target).Placeholder("10.0.0.9:5432").Grow(1)
			})
		}
		ui.Row(c).Gap(gapSmall).Children(func() {
			if ui.PrimaryButton(c.Key("fw:start"), tr("开始转发")).Height(controlH).Clicked() {
				a.startForward(tb)
			}
			ui.Spacer(c)
		})
		if fp.err != "" {
			ui.Text(c, fp.err).FontSize(fontSmall).TextColor(t.Danger)
		}

		ui.Divider(c)
		ui.Textf(c, tr("已建立 · %d"), len(fp.list)).FontSize(fontSmall).TextColor(t.TextMuted)
		if len(fp.list) == 0 {
			ui.Text(c, tr("还没有转发规则。")).FontSize(fontSmall).TextColor(t.TextMuted)
		}
		for i, fwd := range fp.list {
			i := i
			ui.Row(c).Gap(gapMid).Children(func() {
				ui.Text(c, fmt.Sprintf("%s  %s", fwd.Kind(), fwd.Listen())).Font(mono).FontSize(fontSmall)
				if target := fwd.Target(); target != "" {
					ui.Text(c, "→ "+target).Font(mono).FontSize(fontSmall).TextColor(t.TextMuted)
				}
				ui.Spacer(c)
				if ui.Button(c.Key("fw:stop"+fwd.Listen()), tr("停止")).Height(controlH).Clicked() {
					a.stopForward(tb, i)
				}
			})
		}
	})
}
