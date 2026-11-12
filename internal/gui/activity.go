package gui

import (
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// openActivity shows the AI activity log in its own window, so the start page
// can stay empty. Main thread only.
func (a *App) openActivity() {
	if a.activityWin != nil {
		a.activityWin.Focus()
		return
	}
	if a.window() == nil {
		return // headless: tests render activityView directly
	}
	win := mygo.NewWindow(mygo.WindowOptions{
		Title:         tr("ai-ssh AI 活动"),
		Width:         680,
		Height:        480,
		MinWidth:      480,
		MinHeight:     320,
		DisableResize: false,
		Content:       ui.View(a.activityView),
	})
	setWindowIcon(win)
	a.activityWin = win
	win.OnClosed(func() {
		if a.activityWin == win {
			a.activityWin = nil
		}
	})
	win.Center()
	win.Focus()
}

func (a *App) closeActivity() {
	if a.activityWin != nil {
		a.activityWin.Close()
	}
}

func (a *App) activityView(c *ui.Context) {
	t := a.applyTheme(c)
	c.OnWindowShortcut(0, ui.KeyEscape, func() { a.closeActivity() })
	s := a.snapshot()

	ui.Column(c).Fill().Background(t.Background).Children(func() {
		ui.Row(c).Padding(20, 18).Gap(gapMid).Children(func() {
			ui.Textf(c, tr("AI 活动 · %d"), len(s.activity)).FontSize(fontPage).Bold()
			ui.Spacer(c)
			ui.Text(c, s.auditPath).Font(mono).FontSize(fontMicro).TextColor(t.TextMuted)
		})
		ui.Divider(c)

		ui.Scroll(c).Grow(1).Padding(20).Gap(gapTight).Children(func() {
			if len(s.activity) == 0 {
				ui.Text(c, tr("还没有记录。AI 通过 MCP 或 CLI 操作时会出现在这里。")).
					FontSize(fontBody).TextColor(t.TextMuted)
				return
			}
			// Newest first: the thing you open this window for is the last call.
			for i := len(s.activity) - 1; i >= 0; i-- {
				e := s.activity[i]
				color := t.Text
				if !e.OK {
					color = t.Danger
				}
				ui.Text(c.Key(i), e.summary()).Font(mono).FontSize(fontSmall).TextColor(color)
			}
		})
	})
}

// invalidateActivity repaints the activity window if it is open.
func (a *App) invalidateActivity() {
	if a.activityWin != nil {
		a.activityWin.Invalidate()
	}
}
