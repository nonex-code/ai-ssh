package gui

import (
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// confirmRequest is one in-app confirmation, in the app's own window rather
// than a system message box (which looks foreign and blocks differently).
type confirmRequest struct {
	title        string
	message      string
	detail       string
	confirmLabel string
	onConfirm    func()
}

func (a *App) askConfirm(req confirmRequest) {
	if req.confirmLabel == "" {
		req.confirmLabel = tr("确定")
	}
	if a.window() == nil {
		return
	}
	if a.confirmWin != nil {
		a.confirmWin.Close()
	}
	a.confirmReq = &req
	win := mygo.NewWindow(mygo.WindowOptions{
		Title:         "ai-ssh",
		Width:         480,
		Height:        260,
		MinWidth:      420,
		MinHeight:     220,
		DisableResize: true,
		AlwaysOnTop:   true,
		Content:       ui.View(a.confirmView),
	})
	setWindowIcon(win)
	a.confirmWin = win
	win.OnClosed(func() {
		if a.confirmWin == win {
			a.confirmWin = nil
		}
	})
	win.Center()
	win.Focus()
}

func (a *App) closeConfirm() {
	if a.confirmWin != nil {
		a.confirmWin.Close()
	}
}

func (a *App) confirmView(c *ui.Context) {
	t := a.applyTheme(c)
	req := a.confirmReq
	if req == nil {
		return
	}
	c.OnWindowShortcut(0, ui.KeyEscape, func() { a.closeConfirm() })

	ui.Column(c).Fill().Background(t.Background).Padding(24).Gap(gapMid).Children(func() {
		ui.Text(c, req.title).FontSize(fontPage).Bold()
		ui.Text(c, req.message).FontSize(fontBody)
		if req.detail != "" {
			ui.Text(c, req.detail).FontSize(fontSmall).TextColor(t.TextMuted)
		}
		ui.Spacer(c)
		ui.Row(c).Gap(gapMid).Children(func() {
			if ui.PrimaryButton(c.Key("cf:ok"), req.confirmLabel).Clicked() {
				a.closeConfirm()
				if req.onConfirm != nil {
					req.onConfirm()
				}
			}
			if ui.Button(c.Key("cf:cancel"), tr("取消")).Clicked() {
				a.closeConfirm()
			}
		})
	})
}
