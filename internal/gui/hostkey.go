package gui

import (
	"fmt"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/nonex-code/ai-ssh/internal/config"
	"github.com/nonex-code/ai-ssh/internal/daemon"
)

// hostKeyRequest is what the host-key window shows: a first meeting (unk) to
// trust, or a known host whose key changed (mismatch) to re-pin after the human
// checks it out of band.
type hostKeyRequest struct {
	host     string
	unk      *daemon.UnknownHostKeyError
	mismatch *daemon.HostKeyMismatchError
	// confirmed gates the re-pin of a changed key: checking it is the human
	// saying they verified the new fingerprint somewhere else.
	confirmed bool
	trust     func(fingerprint string)
}

// promptHostKey asks a human to trust a host seen for the first time, in the
// app's own window rather than a system message box, and pins its fingerprint
// in the connection when they do.
func (a *App) promptHostKey(tb *terminalTab, unk *daemon.UnknownHostKeyError) {
	a.promptHostKeyFor(tb.host, unk, func(fp string) { a.trustHostKey(tb, fp) })
}

// promptHostKeyFor is the same window for any caller that needs to pin a
// fingerprint, such as the connection form's test button.
func (a *App) promptHostKeyFor(host string, unk *daemon.UnknownHostKeyError, trust func(string)) {
	a.showHostKeyWindow(&hostKeyRequest{host: host, unk: unk, trust: trust}, 520, 320)
}

// promptHostKeyChange tells the human that a known host now presents a
// different key. Re-pinning is deliberately not one click: the box must be
// checked after the fingerprint was verified out of band, because accepting it
// blindly is what a machine-in-the-middle needs.
func (a *App) promptHostKeyChange(tb *terminalTab, mm *daemon.HostKeyMismatchError) {
	a.showHostKeyWindow(&hostKeyRequest{
		host:     tb.host,
		mismatch: mm,
		trust:    func(fp string) { a.trustHostKey(tb, fp) },
	}, 600, 520)
}

func (a *App) showHostKeyWindow(req *hostKeyRequest, width, height int) {
	if a.window() == nil {
		return
	}
	if a.hostKeyWin != nil {
		a.hostKeyWin.Close()
	}
	a.hostKeyReq = req
	win := mygo.NewWindow(mygo.WindowOptions{
		Title:         tr("ai-ssh 主机密钥"),
		Width:         width,
		Height:        height,
		MinWidth:      width - 60,
		MinHeight:     height - 40,
		DisableResize: true,
		AlwaysOnTop:   true,
		Content:       ui.View(a.hostKeyView),
	})
	setWindowIcon(win)
	a.hostKeyWin = win
	win.OnClosed(func() {
		if a.hostKeyWin == win {
			a.hostKeyWin = nil
		}
	})
	win.Center()
	win.Focus()
}

func (a *App) closeHostKey() {
	if a.hostKeyWin != nil {
		a.hostKeyWin.Close()
	}
}

func (a *App) hostKeyView(c *ui.Context) {
	t := a.applyTheme(c)
	req := a.hostKeyReq
	if req == nil {
		return
	}
	c.OnWindowShortcut(0, ui.KeyEscape, func() { a.closeHostKey() })
	if req.mismatch != nil {
		a.hostKeyChangedView(c, t, req)
		return
	}

	ui.Column(c).Fill().Background(t.Background).Padding(24).Gap(gapMid).Children(func() {
		ui.Text(c, tr("首次连接 ")+req.host).FontSize(fontPage).Bold()
		ui.Text(c, tr("服务器出示的主机密钥不在 known_hosts 里。先核对 SHA256 指纹：")).
			FontSize(fontBody).TextColor(t.TextMuted)

		a.hostKeyFingerprint(c, t, req.unk.Fingerprint)

		ui.Text(c, tr("信任会把指纹钉进这条连接（host_key），不再依赖 known_hosts。")).
			FontSize(fontSmall).TextColor(t.TextMuted)
		ui.Text(c, tr("如果和你预期的不一样，点取消，不要连接。")).
			FontSize(fontSmall).TextColor(t.Warning)
		ui.Spacer(c)

		ui.Row(c).Gap(gapMid).Children(func() {
			if ui.PrimaryButton(c.Key("hk:trust"), tr("信任")).Clicked() {
				a.closeHostKey()
				if req.trust != nil {
					req.trust(req.unk.Fingerprint)
				}
			}
			if ui.Button(c.Key("hk:cancel"), tr("取消")).Clicked() {
				a.closeHostKey()
			}
		})
	})
}

// hostKeyChangedView shows both fingerprints, so the human can compare the new
// one with the server's console or the provider's panel before accepting it.
func (a *App) hostKeyChangedView(c *ui.Context, t *ui.Theme, req *hostKeyRequest) {
	m := req.mismatch
	ui.Column(c).Fill().Background(t.Background).Children(func() {
		// The fingerprints and the verification hint scroll; the buttons keep
		// their strip at the bottom, so they can never be pushed out of the
		// window by a long fingerprint.
		ui.Scroll(c).Grow(1).Padding(24).Gap(gapMid).Children(func() {
			ui.Text(c, tr("主机密钥变了：")+req.host).FontSize(fontPage).Bold().TextColor(t.Danger)
			ui.Text(c, tr("服务器出示的密钥和这条连接固定的不一样。可能是系统重装或换了密钥，")+
				tr("也可能是有人在中间拦截——核对之前不要继续。")).
				FontSize(fontBody).TextColor(t.Warning)

			ui.Text(c, tr("服务器现在出示")).FontSize(fontMicro).TextColor(t.TextMuted)
			a.hostKeyFingerprint(c, t, m.Got)
			ui.Text(c, tr("连接里固定的是")).FontSize(fontMicro).TextColor(t.TextMuted)
			a.hostKeyFingerprint(c, t, m.Want)

			ui.Text(c, tr("核对方法：在服务器控制台运行 ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub ，")+
				tr("或向管理员/云厂商确认新指纹。")).
				FontSize(fontSmall).TextColor(t.TextMuted)
			ui.Checkbox(c.Key("hk:verified"), &req.confirmed, tr("我已通过可信渠道核对过上面的新指纹"))
		})

		ui.Divider(c)
		ui.Row(c).FillWidth().Padding(24, 14).Gap(gapMid).Children(func() {
			if ui.PrimaryButton(c.Key("hk:update"), tr("更新指纹并重连")).
				Disabled(!req.confirmed).Clicked() {
				a.closeHostKey()
				if req.trust != nil {
					req.trust(m.Got)
				}
			}
			if ui.Button(c.Key("hk:cancel"), tr("取消")).Clicked() {
				a.closeHostKey()
			}
		})
	})
}

// hostKeyFingerprint is the selectable-looking box with a fingerprint and a
// copy button.
func (a *App) hostKeyFingerprint(c *ui.Context, t *ui.Theme, fingerprint string) {
	ui.Row(c).FillWidth().Gap(gapSmall).Padding(14).Radius(radiusCard).
		Background(t.Surface).Border(1, t.Border).AlignItems(ui.Center).Children(func() {
		ui.Text(c, fingerprint).Font(mono).FontSize(fontBody).Grow(1)
		if ui.Button(c.Key("hk:copy:"+fingerprint), tr("复制")).Height(controlH).Clicked() {
			a.copyText(fingerprint)
		}
	})
}

// trustHostKey pins the fingerprint and opens a new session.
func (a *App) trustHostKey(tb *terminalTab, fingerprint string) {
	ag := a.agentRef()
	if ag == nil {
		return
	}
	go func() {
		err := ag.UpdateConfig(func(cfg *config.Config) error {
			h := cfg.Hosts[tb.host]
			if h == nil {
				return fmt.Errorf(tr("连接 %s 已经不存在"), tb.host)
			}
			h.HostKey = fingerprint
			return nil
		})
		if err != nil {
			a.onMain(func() {
				tb.err = err.Error()
				a.invalidate()
			})
			return
		}
		a.onMain(func() {
			a.closeTabByID(tb.id)
			a.openTerminal(tb.host)
		})
	}()
}
