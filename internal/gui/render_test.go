package gui

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/nonex-code/ai-ssh/internal/agent"
	"github.com/nonex-code/ai-ssh/internal/config"
	"github.com/nonex-code/ai-ssh/internal/daemon"
	"github.com/nonex-code/ai-ssh/internal/proto"
)

// TestViewRenders proves the views actually rasterise, not just build a tree.
// Set AISSH_GUI_SHOTS to a directory to keep the images for inspection.
func TestViewRenders(t *testing.T) {
	type shot struct {
		app  *App
		view func(*App, *ui.Context)
		w, h int
		dark bool
	}
	cases := map[string]shot{
		"icons":             {readyApp(demoHosts...), (*App).iconSheet, 720, 420, false},
		"locked":            {newTestApp(), (*App).view, 1000, 700, false},
		"session":           {sessionApp(), (*App).view, 1000, 700, false},
		"many-tabs":         {manyTabsApp(), (*App).view, 1000, 700, false},
		"ready":             {readyApp(demoHosts...), (*App).view, 1000, 700, false},
		"home":              {historyApp(), (*App).view, 1000, 700, false},
		"ready-dark":        {readyApp(demoHosts...), (*App).view, 1000, 700, true},
		"approval":          {pendingApp(nil), (*App).approvalView, 560, 360, false},
		"activity":          {activityApp(), (*App).activityView, 680, 480, false},
		"editor":            {editorApp(), (*App).editorView, 540, 620, false},
		"editor-password":   {passwordEditorApp(), (*App).editorView, 540, 620, false},
		"editor-open":       {openEditorApp(), (*App).editorView, 540, 760, false},
		"servers":           {readyApp(demoHosts...), (*App).serversView, 760, 620, false},
		"servers-groups":    {groupedServersApp(), (*App).serversView, 760, 620, false},
		"group-dialog":      {groupDialogShotApp(), (*App).groupDialogView, 460, 250, false},
		"files":             {filesShotApp(), (*App).view, 1200, 700, false},
		"monitor":           {monitorShotApp(), (*App).view, 1100, 700, false},
		"trust":             {trustShotApp(), (*App).hostKeyView, 520, 300, false},
		"trust-changed":     {changedKeyShotApp(), (*App).hostKeyView, 600, 520, false},
		"confirm":           {confirmShotApp(), (*App).confirmView, 480, 260, false},
		"settings-security": {securityShotApp(), (*App).settingsView, 840, 600, false},
		"settings":          {readyApp(demoHosts...), (*App).settingsView, 840, 600, false},
		"settings-dark":     {readyApp(demoHosts...), (*App).settingsView, 840, 600, true},
	}
	outDir := os.Getenv("AISSH_GUI_SHOTS")

	for name, c := range cases {
		view := func(ctx *ui.Context) { c.view(c.app, ctx) }
		tt := ui.NewTesterWith(view, c.w, c.h, ui.TesterOptions{Dark: c.dark})
		img := tt.Image()
		if img == nil {
			t.Fatalf("%s: no image", name)
		}
		if got := distinctColors(img.Pix, 4096); got < 3 {
			t.Fatalf("%s: the frame looks blank (%d colours)", name, got)
		}
		if outDir == "" {
			continue
		}
		if err := os.MkdirAll(outDir, 0o755); err != nil {
			t.Fatal(err)
		}
		f, err := os.Create(filepath.Join(outDir, name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(f, img); err != nil {
			f.Close()
			t.Fatal(err)
		}
		f.Close()
	}
	if outDir != "" {
		t.Logf("wrote shots to %s", outDir)
	}
}

func editorApp() *App {
	a := readyApp(demoHosts...)
	a.draft = editorDraft{
		alias: "prod", addr: "10.0.0.5:22", user: "deploy",
		auth: "key", key: "~/.ssh/id_ed25519", pass: "env:PROD_KEY",
		hostKey: "known_hosts", workdir: "/srv/app",
		elev: "sudo", elevUsr: "root", elevPwd: "env:PROD_SUDO",
		forwards: []config.Forward{{Kind: "local", Listen: "127.0.0.1:5432", Target: "db.internal:5432"}},
	}
	return a
}

func passwordEditorApp() *App {
	a := editorApp()
	a.draft.auth = "password"
	a.draft.secret = "env:PROD_PASSWORD"
	a.draft.key = ""
	return a
}

func openEditorApp() *App {
	a := editorApp()
	a.draft.showAdvanced = true
	a.draft.showElevate = true
	a.draft.showTunnels = true
	return a
}

func historyApp() *App {
	a := readyApp(demoHosts...)
	a.recent = []recentConn{
		{Alias: "prod", At: time.Now().Add(-2 * time.Minute)},
		{Alias: "staging", At: time.Now().Add(-3 * time.Hour)},
	}
	return a
}

// iconSheet draws every icon with its name, for review.
func (a *App) iconSheet(c *ui.Context) {
	t := a.applyTheme(c)
	names := []string{
		"home", "server", "newtab", "duplicate", "log", "mcp",
		"check", "appearance", "gear", "sun", "moon", "lock", "terminal", "folder", "forward",
		"gauge", "pencil", "arrow-right", "plus", "x",
		"chevron-left", "chevron-right", "chevron-down", "arrow-up", "refresh",
	}
	ui.Column(c).Fill().Background(t.Background).Padding(24).Gap(20).Children(func() {
		for i := 0; i < len(names); i += 6 {
			ui.Row(c).Gap(20).Children(func() {
				for _, name := range names[i:min(i+6, len(names))] {
					ui.Column(c).Width(90).Gap(8).AlignItems(ui.Center).Children(func() {
						ui.Icon(c, icon(name)).Size(28, 28).TextColor(t.Text)
						ui.Text(c, name).FontSize(fontSmall).TextColor(t.TextMuted)
					})
				}
			})
		}
	})
}

func filesShotApp() *App {
	a := filesApp(
		agent.FileEntry{Name: "bin", Path: "/srv/app/bin", Dir: true, Mode: "drwxr-xr-x"},
		agent.FileEntry{Name: "run.sh", Path: "/srv/app/run.sh", Size: 512, Mode: "-rwxr-xr-x"},
		agent.FileEntry{Name: "README.md", Path: "/srv/app/README.md", Size: 1024, Mode: "-rw-r--r--"},
		agent.FileEntry{Name: "release.tar.gz", Path: "/srv/app/release.tar.gz", Size: 10485760, Mode: "-rw-r--r--"},
		agent.FileEntry{Name: "logo.png", Path: "/srv/app/logo.png", Size: 8192, Mode: "-rw-r--r--"},
		agent.FileEntry{Name: "current", Path: "/srv/app/current", Link: true, Mode: "Lrwxrwxrwx"},
		agent.FileEntry{Name: ".env", Path: "/srv/app/.env", Size: 200, Mode: "-rw-rw-rw-"},
	)
	fp := &a.tabs[0].filesPane
	fp.remote.children["/srv/app/bin"] = []agent.FileEntry{
		{Name: "app", Path: "/srv/app/bin/app", Size: 4096, Mode: "-rwxr-xr-x"},
		{Name: "deploy.sh", Path: "/srv/app/bin/deploy.sh", Size: 512, Mode: "-rw-r--r--"},
	}
	fp.remote.expanded["/srv/app/bin"] = true
	return a
}

func confirmShotApp() *App {
	a := readyApp(demoHosts...)
	a.confirmReq = &confirmRequest{
		title:        "删除连接 prod",
		message:      "删除后它的认证、提权口令和隧道配置会一起消失，不能撤销。",
		detail:       "正在运行的会话不会被断开，只是配置里不再有这台服务器。",
		confirmLabel: "删除",
	}
	return a
}

func trustShotApp() *App {
	a := readyApp(demoHosts...)
	a.hostKeyReq = &hostKeyRequest{
		host: "prod",
		unk:  &daemon.UnknownHostKeyError{Host: "10.0.0.5:22", Fingerprint: "SHA256:Ux8n0n3Vq2kS9m1xYj5oZpQ0l5c4s1Ff2g7h8j9k0lm"},
	}
	return a
}

func changedKeyShotApp() *App {
	a := readyApp(demoHosts...)
	a.hostKeyReq = &hostKeyRequest{
		host: "prod",
		mismatch: &daemon.HostKeyMismatchError{
			Host: "10.0.0.5:22",
			Got:  "SHA256:Ux8n0n3Vq2kS9m1xYj5oZpQ0l5c4s1Ff2g7h8j9k0lm",
			Want: "SHA256:Ab3dEf6gHi9jKl2mNo5pQr8sTu1vWx4yZa7bCd0eFg3h",
		},
		confirmed: false,
	}
	return a
}

func groupedServersApp() *App {
	return readyApp(
		proto.HostInfo{Alias: "web1", User: "u", Addr: "10.0.0.1:22", Auth: "key", Group: "生产"},
		proto.HostInfo{Alias: "web2", User: "u", Addr: "10.0.0.2:22", Auth: "key", Group: "生产"},
		proto.HostInfo{Alias: "db1", User: "u", Addr: "10.0.0.3:22", Auth: "key", Group: "测试"},
		proto.HostInfo{Alias: "loose", User: "u", Addr: "10.0.0.4:22", Auth: "key"},
	)
}

func securityShotApp() *App {
	a := readyApp(demoHosts...)
	a.settingsTab = "security"
	return a
}

func groupDialogShotApp() *App {
	a := readyApp(demoHosts...)
	a.groupDlg = groupDialog{mode: "create", alias: "prod", value: "生产"}
	return a
}

func monitorShotApp() *App {
	a := readyApp(demoHosts...)
	tb := &terminalTab{id: 1, host: "prod", forwardPane: newForwardPane()}
	tb.monitorPane.open = true
	tb.monitorPane.text = monitorSample
	tb.monitorPane.at = time.Now()
	a.tabs = []*terminalTab{tb}
	a.activeTab = 0
	return a
}

func manyTabsApp() *App {
	a := readyApp(demoHosts...)
	for i := 1; i <= 12; i++ {
		a.tabs = append(a.tabs, &terminalTab{id: i, host: "prod", label: "prod", forwardPane: newForwardPane()})
	}
	a.activeTab = 3
	return a
}

func sessionApp() *App {
	a := readyApp(demoHosts...)
	a.tabs = []*terminalTab{{id: 1, host: "prod", opening: true, forwardPane: newForwardPane()}}
	a.activeTab = 0
	return a
}

func activityApp() *App {
	a := readyApp(demoHosts...)
	a.auditPath = "~/.ai-ssh/audit.log"
	a.activity = []auditEntry{
		{Time: "2026-01-02T03:04:05.000Z", Op: "exec", Host: "prod", Command: "systemctl status nginx", OK: true, DurationMs: 42},
		{Time: "2026-01-02T03:04:06.000Z", Op: "upload", Host: "prod", Local: "build/app.tar.gz", Remote: "/srv/app/app.tar.gz", OK: true, Bytes: 10485760, DurationMs: 812},
		{Time: "2026-01-02T03:04:07.000Z", Op: "exec", Host: "staging", Command: "cat /etc/shadow", Elevated: true, ErrorCode: "blocked"},
	}
	return a
}

// distinctColors counts how many unique pixels a frame has, capped at max.
func distinctColors(pix []byte, max int) int {
	seen := make(map[uint32]bool, max)
	for i := 0; i+3 < len(pix); i += 4 {
		c := uint32(pix[i])<<24 | uint32(pix[i+1])<<16 | uint32(pix[i+2])<<8 | uint32(pix[i+3])
		if !seen[c] {
			seen[c] = true
			if len(seen) >= max {
				return len(seen)
			}
		}
	}
	return len(seen)
}
