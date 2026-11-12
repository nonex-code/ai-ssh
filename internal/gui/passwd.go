package gui

import (
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/nonex-code/ai-ssh/internal/vault"
)

// resetPasswd clears the inline change-password form. The fields live in the
// App because the settings window is rebuilt every frame, but they are cleared
// when the window closes so a password left half-typed never lingers.
func (a *App) resetPasswd() {
	a.passwdOld, a.passwdNew, a.passwdAgain = "", "", ""
	a.passwdErr, a.passwdDone, a.passwdSaving = "", false, false
}

// passwordPane is the "安全" page of the settings window: the form changes the
// vault's master password in place, through the agent that already holds it.
func (a *App) passwordPane(c *ui.Context, t *ui.Theme) {
	section(c, t, tr("主密码"))

	if a.passwdDone {
		ui.Text(c, tr("主密码已修改。下次解锁请使用新密码。")).
			FontSize(fontBody).TextColor(t.Success)
		ui.Text(c, tr("vault 已用新密码重新加密；连接配置、正在运行的会话和 AI 授权记录都不受影响。")).
			FontSize(fontSmall).TextColor(t.TextMuted)
		if ui.Button(c.Key("pw:again"), tr("再改一次")).Clicked() {
			a.resetPasswd()
			a.invalidateSettings()
		}
		return
	}

	ui.Text(c, tr("验证当前密码后，用新密码重新加密 vault；连接配置和正在运行的会话保持不变。")).
		FontSize(fontSmall).TextColor(t.TextMuted)

	old := ui.TextInput(c.Key("pw:old"), &a.passwdOld).
		Placeholder(tr("当前主密码")).Password()
	next := ui.TextInput(c.Key("pw:new"), &a.passwdNew).
		Placeholder(tr("新主密码（至少 8 位）")).Password()
	again := ui.TextInput(c.Key("pw:again"), &a.passwdAgain).
		Placeholder(tr("再次输入新主密码")).Password()

	if a.passwdErr != "" {
		ui.Text(c, a.passwdErr).FontSize(fontBody).TextColor(t.Danger)
	}
	if a.passwdSaving {
		ui.Text(c, tr("正在重新加密…")).FontSize(fontBody).TextColor(t.TextMuted)
	}
	ui.Row(c).Gap(gapMid).Children(func() {
		if ui.PrimaryButton(c.Key("pw:ok"), tr("修改主密码")).
			Disabled(a.passwdSaving).Clicked() {
			a.changePassword()
		}
	})
	if old.Submitted() || next.Submitted() || again.Submitted() {
		a.changePassword()
	}
}

// changePassword validates the form, then runs the (slow) Argon2id derivation
// off the UI thread.
func (a *App) changePassword() {
	if a.passwdSaving || a.passwdDone {
		return
	}
	old, next, again := a.passwdOld, a.passwdNew, a.passwdAgain
	var msg string
	switch {
	case strings.TrimSpace(old) == "":
		msg = tr("请输入当前主密码")
	case len(next) < vault.MinPasswordLen:
		msg = tr("新主密码至少 8 位")
	case next != again:
		msg = tr("两次输入的新主密码不一致")
	case next == old:
		msg = tr("新主密码不能与当前密码相同")
	}
	ag := a.agentRef()
	if msg == "" && ag == nil {
		msg = tr("agent 未就绪")
	}
	if msg != "" {
		a.passwdErr = msg
		a.invalidateSettings()
		return
	}

	a.passwdErr = ""
	a.passwdSaving = true
	a.invalidateSettings()

	go func() {
		err := ag.ChangePassword(old, next)
		a.onMain(func() {
			a.passwdSaving = false
			if err != nil {
				a.passwdErr = humanError(err)
				a.invalidateSettings()
				return
			}
			a.passwdOld, a.passwdNew, a.passwdAgain = "", "", ""
			a.passwdDone = true
			a.flash = tr("主密码已修改，下次解锁请使用新密码")
			a.flashStyle = flashSuccess
			a.invalidateSettings()
			a.invalidate()
		})
	}()
}
