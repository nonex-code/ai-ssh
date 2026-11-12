package gui

import (
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/nonex-code/ai-ssh/internal/proto"
)

// syncApprovalWindow is the only thing that manages the authorization popup.
// Main thread only. A privilege prompt belongs in its own window: it must stay
// visible above everything else, and it must not be possible to dismiss it
// without answering.
func (a *App) syncApprovalWindow() {
	// Headless tests have no windows; they render approvalView directly.
	if a.window() == nil {
		return
	}

	a.mu.Lock()
	n := len(a.pending)
	a.mu.Unlock()

	if n == 0 {
		if a.popup != nil {
			win := a.popup
			a.popup = nil
			// Close outside the frame that may have triggered this, so the
			// window is not destroyed while its own view is running.
			time.AfterFunc(10*time.Millisecond, func() {
				mygo.RunOnMain(func() { win.Close() })
			})
		}
		return
	}

	if a.popup != nil {
		a.popup.Invalidate()
		time.AfterFunc(60*time.Millisecond, func() {
			mygo.RunOnMain(func() { a.popup.Show(); a.popup.Focus() })
		})
		return
	}

	win := mygo.NewWindow(mygo.WindowOptions{
		Title:         tr("ai-ssh 授权"),
		Width:         560,
		Height:        360,
		MinWidth:      480,
		MinHeight:     300,
		DisableResize: true,
		DisableClose:  true, // answer, or wait for the timeout
		AlwaysOnTop:   true,
		Content:       ui.View(a.approvalView),
	})
	setWindowIcon(win)
	a.popup = win
	win.OnClosed(func() {
		if a.popup == win {
			a.popup = nil
		}
		// Closed by the platform (app quitting, screen locked) while requests
		// are still waiting: answer them instead of leaving the agent stuck.
		a.denyAllPending()
	})
	win.Center()
	// A window created from the main loop may not exist in the platform yet;
	// focus it once it does, so the prompt is never buried behind the editor.
	time.AfterFunc(150*time.Millisecond, func() {
		mygo.RunOnMain(func() {
			win.Show()
			win.Focus()
		})
	})
}

// denyAllPending answers every queued request with "no".
func (a *App) denyAllPending() {
	a.mu.Lock()
	pending := append([]proto.Approval(nil), a.pending...)
	a.mu.Unlock()
	for _, ap := range pending {
		a.decide(ap.ID, false)
	}
}

// approvalView is the popup's content: one request at a time, with the queue
// depth in the corner.
func (a *App) approvalView(c *ui.Context) {
	t := a.applyTheme(c)
	s := a.snapshot()

	ui.Column(c).Fill().Padding(22).Gap(14).Background(t.Background).Children(func() {
		if len(s.pending) == 0 {
			// The window is on its way out.
			ui.Text(c, tr("已处理")).FontSize(fontBody).TextColor(t.TextMuted)
			return
		}
		ap := s.pending[0]

		// Keyboard: Enter allows, Escape denies. A security prompt should be
		// answerable without aiming at a button.
		if c.Shortcut(0, ui.KeyEnter) {
			a.decide(ap.ID, true)
		}
		if c.Shortcut(0, ui.KeyEscape) {
			a.decide(ap.ID, false)
		}

		ui.Row(c).Gap(gapMid).Children(func() {
			ui.Text(c, tr("AI 请求授权")).FontSize(fontPage).Bold().TextColor(t.Warning)
			ui.Spacer(c)
			if len(s.pending) > 1 {
				ui.Textf(c, tr("还有 %d 个"), len(s.pending)-1).FontSize(fontBody).TextColor(t.TextMuted)
			}
		})

		ui.Text(c, approvalTitle(ap)).FontSize(fontCard)

		ui.Column(c).FillWidth().Padding(12).Radius(8).Gap(gapSmall).Background(t.Surface).Children(func() {
			if ap.Command != "" {
				ui.Text(c, ap.Command).Font(mono).FontSize(fontBody)
			}
			if ap.Local != "" || ap.Remote != "" {
				ui.Textf(c, "%s → %s", ap.Local, ap.Remote).Font(mono).FontSize(fontBody)
			}
			if ap.Reason != "" {
				ui.Text(c, ap.Reason).FontSize(fontSmall).TextColor(t.TextMuted)
			}
		})

		ui.Spacer(c)

		ui.Row(c).Gap(gapMid).Children(func() {
			if ui.PrimaryButton(c.Key("approve"), tr("允许一次")).Clicked() {
				a.decide(ap.ID, true)
			}
			if ui.Button(c.Key("reject"), tr("拒绝")).Clicked() {
				a.decide(ap.ID, false)
			}
			ui.Spacer(c)
			if ap.TimeoutS > 0 {
				ui.Textf(c, tr("%d 秒内无响应将自动拒绝"), ap.TimeoutS).FontSize(fontSmall).TextColor(t.TextMuted)
			}
		})
	})
}
