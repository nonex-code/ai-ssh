package gui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nonex-code/ai-ssh/internal/appdir"
	"github.com/nonex-code/ai-ssh/internal/update"
)

// updateInterval keeps the automatic check from asking GitHub on every launch.
const updateInterval = 24 * time.Hour

// startUpdateCheck runs the automatic check when it is enabled, this is not a
// development build, and the last check is old enough. It never blocks.
func (a *App) startUpdateCheck() {
	if !a.autoCheckUpdate {
		return
	}
	if strings.Contains(a.opts.Version, "dev") {
		return // a dev build is not what releases update
	}
	if !a.updLastCheck.IsZero() && time.Since(a.updLastCheck) < updateInterval {
		return
	}
	a.checkUpdates(true)
}

// checkUpdates asks GitHub for the latest release. manual checks report errors
// in the settings page; an automatic one still records them, quietly.
func (a *App) checkUpdates(auto bool) {
	if a.updChecking || a.updInstalling {
		return
	}
	a.updChecking = true
	a.updErr = ""
	a.updDone = false
	a.invalidateSettings()
	a.invalidate()

	current := a.opts.Version
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		rel, err := update.Check(ctx, current)
		a.onMain(func() {
			a.updChecking = false
			a.updLastCheck = time.Now()
			a.saveGeometry()
			if err != nil {
				a.updErr = humanUpdateError(err)
				a.invalidateSettings()
				a.invalidate()
				return
			}
			a.upd = rel
			a.invalidateSettings()
			a.invalidate()
			if auto && rel.Available && a.autoInstallUpdate {
				a.installUpdate()
			}
		})
	}()
}

// installUpdate downloads the release archive, verifies its checksums and
// replaces the running executable. It needs a restart to take effect.
func (a *App) installUpdate() {
	if a.upd == nil || !a.upd.Available || a.updInstalling || a.updDone {
		return
	}
	a.updInstalling = true
	a.updErr = ""
	a.invalidateSettings()
	a.invalidate()

	rel := *a.upd
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		dir, err := appdir.Dir()
		if err != nil {
			a.finishInstall(err)
			return
		}
		archive, err := update.Download(ctx, &rel, filepath.Join(dir, "update"))
		if err == nil {
			err = update.Install(archive)
			_ = os.Remove(archive)
		}
		a.finishInstall(err)
	}()
}

func (a *App) finishInstall(err error) {
	a.onMain(func() {
		a.updInstalling = false
		if err != nil {
			a.updErr = tr("更新失败：") + err.Error()
		} else {
			a.updDone = true
			a.flash = tr("更新已安装，重启 ai-ssh 后生效")
			a.flashStyle = flashSuccess
		}
		a.invalidateSettings()
		a.invalidate()
	})
}

func (a *App) updateAvailable() bool {
	return a.upd != nil && a.upd.Available && !a.updDone
}

func humanUpdateError(err error) string {
	switch {
	case errors.Is(err, update.ErrNoReleases):
		return tr("GitHub 上还没有已发布的版本。")
	case errors.Is(err, context.DeadlineExceeded):
		return tr("检查更新超时。")
	default:
		return fmt.Sprintf(tr("检查更新失败：%v"), err)
	}
}

// updateToggle persists the two update preferences. Main thread only.
func (a *App) updateToggle() {
	a.saveGeometry()
	a.invalidateSettings()
}
