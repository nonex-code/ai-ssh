package gui

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/nonex-code/ai-ssh/internal/update"
)

func TestAboutShowsUpdateAndCredits(t *testing.T) {
	a := readyApp()
	a.opts.Version = "0.1.0"
	a.settingsTab = "about"
	a.upd = &update.Release{
		Version:   "0.2.0",
		PageURL:   "https://github.com/nonex-code/ai-ssh/releases",
		Available: true,
	}
	tt := ui.NewTester(a.settingsView, 840, 700)
	for _, want := range []string{
		"发现新版本 0.2.0", "下载并安装", "打开 Release 页面",
		"启动时自动检查更新", "开源项目", "Ghostty", "JetBrains Mono",
	} {
		if !tt.HasText(want) {
			t.Fatalf("about page is missing %q: %v", want, tt.Texts())
		}
	}
}

func TestAboutShowsRestartAfterInstall(t *testing.T) {
	a := readyApp()
	a.opts.Version = "0.1.0"
	a.settingsTab = "about"
	a.updDone = true
	tt := ui.NewTester(a.settingsView, 840, 600)
	if !tt.HasText("重启并应用更新") || !tt.HasText("更新已安装") {
		t.Fatalf("installed update is not announced: %v", tt.Texts())
	}
}

func TestAutoCheckSkipsDevBuilds(t *testing.T) {
	a := readyApp()
	a.opts.Version = "0.1.0+dev"
	a.autoCheckUpdate = true
	a.startUpdateCheck()
	if a.updChecking {
		t.Fatal("a dev build started an automatic update check")
	}

	a.opts.Version = "0.1.0"
	a.autoCheckUpdate = false
	a.startUpdateCheck()
	if a.updChecking {
		t.Fatal("a disabled preference started an automatic update check")
	}
}
