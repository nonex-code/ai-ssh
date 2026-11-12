package gui

import (
	"context"
	"fmt"
	"image"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/nonex-code/ai-ssh/internal/agent"
	"github.com/nonex-code/ai-ssh/internal/config"
	"github.com/nonex-code/ai-ssh/internal/daemon"
	"github.com/nonex-code/ai-ssh/internal/proto"
)

func newTestApp() *App {
	setLanguage("zh")
	return &App{phase: phaseLocked, copyText: func(string) {}, prefs: themePrefs{Language: "zh"}}
}

func readyApp(hosts ...proto.HostInfo) *App {
	setLanguage("zh")
	return &App{
		phase:     phaseReady,
		hosts:     hosts,
		copyText:  func(string) {},
		activeTab: -1,
		q:         newTransferQueue(),
		prefs:     themePrefs{Language: "zh"},
	}
}

var demoHosts = []proto.HostInfo{
	{Alias: "prod", User: "deploy", Addr: "10.0.0.5:22", Auth: "key", HostKey: "known_hosts", Elevate: "sudo", Workdir: "/srv/app"},
	{Alias: "staging", User: "ci", Addr: "10.0.0.9:22", Auth: "key", HostKey: "SHA256:abc"},
}

func TestLockedView(t *testing.T) {
	tt := ui.NewTester(newTestApp().view, 1000, 700)
	for _, want := range []string{"解锁 vault", "主密码只用于在本机", "解锁"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %v", want, tt.Texts())
		}
	}
	if tt.HasText("AI 活动") {
		t.Fatal("the locked view must not show the unlocked panes")
	}
}

func TestLockedViewNeedsVault(t *testing.T) {
	a := newTestApp()
	a.needsVault = true
	tt := ui.NewTester(a.view, 1000, 700)
	if !tt.HasText("创建 vault") {
		t.Fatalf("missing create title: %v", tt.Texts())
	}
	if !tt.HasText("创建并解锁") {
		t.Fatalf("missing create button: %v", tt.Texts())
	}
}

func TestUnlockWithoutPasswordShowsHint(t *testing.T) {
	a := newTestApp()
	tt := ui.NewTester(a.view, 1000, 700)
	if err := tt.Click("解锁"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("请输入主密码") {
		t.Fatalf("expected a hint, got %v", tt.Texts())
	}
	if a.curPhase() != phaseLocked {
		t.Fatal("an empty password must not start unlocking")
	}
}

func TestReadyViewShowsTabs(t *testing.T) {
	tt := ui.NewTester(readyApp(demoHosts...).view, 1000, 700)
	// The strip holds the home tab only; saved connections live on the page.
	if !tt.HasText("主页") || !tt.HasText("AI 活动") {
		t.Fatalf("strip incomplete: %v", tt.Texts())
	}
	if tt.HasText("断开 prod") {
		t.Fatalf("a saved connection must not be open before it is clicked: %v", tt.Texts())
	}
	for _, want := range []string{"连接", "prod", "staging", "deploy@10.0.0.5:22", "Enter 连接并保存为配置"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %v", want, tt.Texts())
		}
	}
}

func TestTabBarShowsSessions(t *testing.T) {
	a := readyApp(demoHosts...)
	a.tabs = []*terminalTab{
		{id: 1, host: "prod"},
		{id: 2, host: "staging", opening: true},
	}
	tt := ui.NewTester(a.view, 1000, 700)

	for _, want := range []string{"主页", "prod", "staging"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %v", want, tt.Texts())
		}
	}
	// The start page is shown while a tab is not active.
	if !tt.HasText("Enter 连接并保存为配置") {
		t.Fatalf("start page missing: %v", tt.Texts())
	}
}

func TestTabSwitchesToTerminal(t *testing.T) {
	a := readyApp(demoHosts...)
	a.tabs = []*terminalTab{{id: 1, host: "prod", opening: true}}
	tt := ui.NewTester(a.view, 1000, 700)

	if err := tt.Click("prod"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()

	if a.activeTab != 0 {
		t.Fatalf("activeTab = %d", a.activeTab)
	}
	if !tt.HasText("正在连接 prod") {
		t.Fatalf("terminal placeholder missing: %v", tt.Texts())
	}
}

func TestTabCloseButton(t *testing.T) {
	a := readyApp(demoHosts...)
	a.tabs = []*terminalTab{{id: 1, host: "prod"}, {id: 2, host: "staging"}}
	a.activeTab = 1
	tt := ui.NewTester(a.view, 1000, 700)

	// Only the active tab has a close button.
	if _, ok := tt.Find("断开 prod"); ok {
		t.Fatalf("an inactive tab must not carry a close button: %v", tt.Texts())
	}
	// Closing a session falls back to the previous one.
	if err := tt.Click("断开 staging"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(a.tabs) != 1 || a.tabs[0].host != "prod" {
		t.Fatalf("tabs = %+v", a.tabs)
	}
	if a.activeTab != 0 {
		t.Fatalf("activeTab = %d", a.activeTab)
	}
}

func TestServerTabReturnsToStartPage(t *testing.T) {
	a := readyApp(demoHosts...)
	a.tabs = []*terminalTab{{id: 1, host: "prod"}}
	a.activeTab = 0
	tt := ui.NewTester(a.view, 1000, 700)

	if err := tt.Click("主页"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.activeTab != -1 {
		t.Fatalf("activeTab = %d", a.activeTab)
	}
	if !tt.HasText("Enter 连接并保存为配置") {
		t.Fatalf("start page missing: %v", tt.Texts())
	}
}

func TestLockReturnsToUnlockScreen(t *testing.T) {
	a := readyApp(demoHosts...)
	tt := ui.NewTester(a.view, 1000, 700)

	if err := tt.Click("锁定"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()

	if a.curPhase() != phaseLocked {
		t.Fatalf("phase = %v", a.curPhase())
	}
	if !tt.HasText("解锁 vault") {
		t.Fatalf("expected the unlock screen, got %v", tt.Texts())
	}
	if a.password != "" || a.confirm != "" {
		t.Fatal("the password buffer must be cleared on lock")
	}
}

func TestCopyMCPConfig(t *testing.T) {
	var copied string
	a := readyApp(demoHosts...)
	a.copyText = func(s string) { copied = s }

	tt := ui.NewTester(a.view, 1000, 700)
	if err := tt.Click("复制 MCP 配置"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(copied, `"mcpServers"`) || !strings.Contains(copied, `"mcp"`) {
		t.Fatalf("clipboard snippet = %q", copied)
	}

	tt.Frame()
	if !a.copied {
		t.Fatalf("no feedback after copying: %v", tt.Texts())
	}
}

func TestActivityRowsAreRendered(t *testing.T) {
	a := readyApp(demoHosts...)
	a.activity = []auditEntry{
		{Time: "2026-01-02T03:04:05.000Z", Op: "exec", Host: "prod", Command: "systemctl status nginx", OK: true, DurationMs: 42},
		{Time: "2026-01-02T03:04:06.000Z", Op: "exec", Host: "prod", Command: "cat /etc/shadow", Elevated: true, ErrorCode: "blocked"},
	}
	tt := ui.NewTester(a.activityView, 680, 480)
	if !tt.HasText("systemctl status nginx") {
		t.Fatalf("activity row missing: %v", tt.Texts())
	}
	if !tt.HasText("blocked") {
		t.Fatalf("failed row missing: %v", tt.Texts())
	}
	if !tt.HasText("sudo") {
		t.Fatalf("elevated marker missing: %v", tt.Texts())
	}
}

func TestAuditSummary(t *testing.T) {
	e := auditEntry{Time: "2026-01-02T03:04:05.123Z", Op: "exec", Host: "prod", Command: "uptime", OK: true, DurationMs: 12}
	s := e.summary()
	for _, want := range []string{"prod", "exec", "ok", "12ms", "uptime"} {
		if !strings.Contains(s, want) {
			t.Fatalf("%q missing from %q", want, s)
		}
	}

	fail := auditEntry{Op: "exec", Host: "prod", ExitCode: 2}
	if !strings.Contains(fail.summary(), "exit=2") {
		t.Fatalf("exit code missing: %q", fail.summary())
	}

	up := auditEntry{Op: "upload", Host: "prod", Local: "/tmp/a", Remote: "/srv/a", OK: true}
	if !strings.Contains(up.summary(), "/tmp/a → /srv/a") {
		t.Fatalf("transfer summary = %q", up.summary())
	}
}

func TestReadAudit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")
	body := strings.Join([]string{
		`{"ts":"2026-01-02T03:04:05Z","op":"exec","host":"a","command":"one","ok":true}`,
		`not json`,
		`{"ts":"2026-01-02T03:04:06Z","op":"upload","host":"b","local":"/x","remote":"/y","ok":true,"bytes":10}`,
		"",
	}, "\n")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	entries := readAudit(path, 10)
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2 (%+v)", len(entries), entries)
	}
	if entries[1].Host != "b" || entries[1].Bytes != 10 {
		t.Fatalf("last entry = %+v", entries[1])
	}
	if got := readAudit(path, 1); len(got) != 1 || got[0].Host != "b" {
		t.Fatalf("limit not applied: %+v", got)
	}
	if got := readAudit(filepath.Join(dir, "missing"), 5); got != nil {
		t.Fatalf("missing file should return nil, got %+v", got)
	}
}

// --- approval banner ---------------------------------------------------------

func pendingApp(decisions *[]proto.Decision) *App {
	a := readyApp(demoHosts...)
	a.pending = []proto.Approval{{
		ID: "ap-1", Op: proto.OpExec, Host: "prod",
		Command: "systemctl restart nginx", Elevated: true,
		Reason: "approval policy: privileged commands", TimeoutS: 120,
	}}
	a.decider = func(d proto.Decision) {
		if decisions != nil {
			*decisions = append(*decisions, d)
		}
	}
	return a
}

func TestApprovalPopupContent(t *testing.T) {
	tt := ui.NewTester(pendingApp(nil).approvalView, 560, 360)
	for _, want := range []string{"AI 请求授权", "prod · 执行命令（提权）", "systemctl restart nginx", "120 秒内无响应"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %v", want, tt.Texts())
		}
	}
}

func TestApprovalAllowButton(t *testing.T) {
	var got []proto.Decision
	a := pendingApp(&got)
	tt := ui.NewTester(a.approvalView, 560, 360)

	if err := tt.Click("允许一次"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()

	if len(got) != 1 || !got[0].Allow || got[0].ID != "ap-1" || got[0].Op != proto.OpDecide {
		t.Fatalf("decision = %+v", got)
	}
	if len(a.pending) != 0 {
		t.Fatalf("pending not cleared: %+v", a.pending)
	}
	if tt.HasText("AI 请求授权") {
		t.Fatalf("banner should be gone: %v", tt.Texts())
	}
}

func TestApprovalDenyButton(t *testing.T) {
	var got []proto.Decision
	a := pendingApp(&got)
	tt := ui.NewTester(a.approvalView, 560, 360)

	if err := tt.Click("拒绝"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()

	if len(got) != 1 || got[0].Allow {
		t.Fatalf("decision = %+v", got)
	}
	if len(a.pending) != 0 {
		t.Fatalf("pending not cleared")
	}
}

func TestApprovalQueueShowsCount(t *testing.T) {
	a := pendingApp(nil)
	a.pending = append(a.pending, proto.Approval{
		ID: "ap-2", Op: proto.OpUpload, Host: "staging", Local: "/tmp/a", Remote: "/srv/a", TimeoutS: 60,
	})
	tt := ui.NewTester(a.approvalView, 560, 360)
	if !tt.HasText("还有 1 个") {
		t.Fatalf("queue size not shown: %v", tt.Texts())
	}

	// Answering the head moves the next one up.
	if err := tt.Click("拒绝"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("staging · 上传文件") {
		t.Fatalf("next approval not shown: %v", tt.Texts())
	}
}

// The main window must not carry an authorization control: approvals live in
// their own always-on-top window.
func TestMainWindowHasNoApprovalControls(t *testing.T) {
	tt := ui.NewTester(pendingApp(nil).view, 1000, 700)
	for _, banned := range []string{"AI 请求授权", "允许一次", "拒绝"} {
		if tt.HasText(banned) {
			t.Fatalf("the main window must not show %q: %v", banned, tt.Texts())
		}
	}
}

func TestDenyAllPending(t *testing.T) {
	var got []proto.Decision
	a := pendingApp(&got)
	a.pending = append(a.pending, proto.Approval{ID: "ap-2", Op: proto.OpExec, Host: "staging"})
	a.denyAllPending()
	if len(got) != 2 || got[0].Allow || got[1].Allow {
		t.Fatalf("decisions = %+v", got)
	}
	if len(a.pending) != 0 {
		t.Fatalf("pending = %+v", a.pending)
	}
}

func TestDropPendingIgnoresUnknownIDs(t *testing.T) {
	a := pendingApp(nil)
	a.dropPending("nope")
	if len(a.pending) != 1 {
		t.Fatalf("pending = %+v", a.pending)
	}
}

func TestNeedsBootstrap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vault")
	if !needsBootstrap(path) {
		t.Fatal("a missing vault must offer to create one")
	}
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if needsBootstrap(path) {
		t.Fatal("an existing vault must not offer to create one")
	}
}

// --- bookmarks bar -----------------------------------------------------------

func TestEditorViewRendersKeyAuth(t *testing.T) {
	a := readyApp(demoHosts...)
	a.openEditor("") // headless: fills the draft, no window
	tt := ui.NewTester(a.editorView, 560, 660)

	for _, want := range []string{"新增连接", "名称", "地址", "用户", "私钥", "高级", "提权（可选）", "隧道（可选）"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %v", want, tt.Texts())
		}
	}
	// host key and workdir live in the advanced section.
	if err := tt.Click("高级"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("host key") || !tt.HasText("workdir") {
		t.Fatalf("advanced fields missing: %v", tt.Texts())
	}
	if tt.HasText("密码") {
		t.Fatalf("key auth must not ask for a password: %v", tt.Texts())
	}
}

func TestEditorSwitchesToPasswordAuth(t *testing.T) {
	a := readyApp(demoHosts...)
	a.openEditor("")
	a.draft.auth = "password"
	tt := ui.NewTester(a.editorView, 560, 660)
	if !tt.HasText("密码") {
		t.Fatalf("password field missing: %v", tt.Texts())
	}
	if tt.HasText("私钥") {
		t.Fatalf("key field still shown: %v", tt.Texts())
	}
}

func TestEditorLoadsExistingHost(t *testing.T) {
	t.Setenv("PP", "passphrase-value")
	t.Setenv("SUDO", "sudo-value")
	dir := t.TempDir()
	cfg, err := config.Parse([]byte(`{
  "hosts": {
    "prod": {
      "addr": "10.0.0.5:22",
      "user": "deploy",
      "auth": {"type": "key", "key": "~/.ssh/id_ed25519", "passphrase": "env:PP"},
      "host_key": "SHA256:abc",
      "workdir": "/srv/app",
      "elevate": {"mode": "sudo", "password": "env:SUDO"}
    }
  }
}`), dir)
	if err != nil {
		t.Fatal(err)
	}
	a := readyApp(demoHosts...)
	a.loadDraftForTest(cfg, "prod")

	if a.draft.addr != "10.0.0.5:22" || a.draft.user != "deploy" {
		t.Fatalf("draft = %+v", a.draft)
	}
	if a.draft.pass != "env:PP" || a.draft.elevPwd != "env:SUDO" {
		t.Fatalf("references must survive the form, got %+v", a.draft)
	}
	if a.draft.elev != "sudo" || a.draft.workdir != "/srv/app" {
		t.Fatalf("elevation/workdir lost: %+v", a.draft)
	}
}

func TestEditorValidation(t *testing.T) {
	a := readyApp(demoHosts...)
	a.openEditor("")
	a.draft.alias = ""
	a.saveEditor()
	if a.editErr == "" {
		t.Fatal("an empty alias must be rejected")
	}

	a.draft.alias = "broken"
	a.draft.addr = "not-a-host-port"
	a.saveEditor()
	if a.editErr == "" {
		t.Fatal("a malformed address must be rejected")
	}
	if len(a.tabs) != 0 {
		t.Fatal("nothing should have been saved")
	}
}

// --- SFTP pane ---------------------------------------------------------------

func filesApp(entries ...agent.FileEntry) *App {
	a := readyApp(demoHosts...)
	a.tabs = []*terminalTab{{
		id: 1, host: "demo",
		filesPane: filePane{
			open:  true,
			ready: true,
			local: dirPane{
				root: "/tmp/work",
				addr: "/tmp/work",
				children: map[string][]agent.FileEntry{
					"/tmp/work": {{Name: "upload.txt", Path: "/tmp/work/upload.txt", Size: 512}},
				},
				expanded: map[string]bool{"/tmp/work": true},
			},
			remote: dirPane{
				root: "/srv/app",
				addr: "/srv/app",
				children: map[string][]agent.FileEntry{
					"/srv/app": entries,
				},
				expanded: map[string]bool{"/srv/app": true},
			},
		},
	}}
	a.activeTab = 0
	return a
}

func TestFilesPaneRenders(t *testing.T) {
	a := filesApp(
		agent.FileEntry{Name: "bin", Path: "/srv/app/bin", Dir: true, Mode: "drwxr-xr-x"},
		agent.FileEntry{Name: "run.sh", Path: "/srv/app/run.sh", Size: 2048, Mode: "-rwxr-xr-x"},
	)
	tt := ui.NewTester(a.view, 1200, 700)

	for _, want := range []string{"本地", "远端 · demo", "bin", "run.sh", "2.0 KB", "后退", "前进", "刷新", "展开或折叠 bin"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %v", want, tt.Texts())
		}
	}
}

func TestFilesPaneSelectsRow(t *testing.T) {
	a := filesApp(
		agent.FileEntry{Name: "run.sh", Path: "/srv/app/run.sh", Size: 10},
	)
	tt := ui.NewTester(a.view, 1200, 700)

	if err := tt.Click("run.sh"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()

	fp := &a.tabs[0].filesPane
	if !fp.remote.sel["/srv/app/run.sh"] {
		t.Fatalf("selection = %v", fp.remote.sel)
	}
}

func TestFilesPaneEmptyDirectory(t *testing.T) {
	tt := ui.NewTester(filesApp().view, 1200, 700)
	if !tt.HasText("空目录") {
		t.Fatalf("empty state missing: %v", tt.Texts())
	}
}

func TestFilesToggleWithoutAgent(t *testing.T) {
	a := readyApp(demoHosts...)
	a.tabs = []*terminalTab{{id: 1, host: "demo"}}
	a.activeTab = 0
	tt := ui.NewTester(a.view, 1200, 700)

	if err := tt.Click("文件"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()

	if !a.tabs[0].filesPane.open {
		t.Fatal("the pane did not open")
	}
	if !tt.HasText("agent 未就绪") {
		t.Fatalf("expected an explanatory error, got %v", tt.Texts())
	}
}

func TestHumanSize(t *testing.T) {
	cases := map[int64]string{
		512:                    "512 B",
		2048:                   "2.0 KB",
		5 * 1024 * 1024:        "5.0 MB",
		3 * 1024 * 1024 * 1024: "3.0 GB",
	}
	for n, want := range cases {
		if got := humanSize(n); got != want {
			t.Fatalf("humanSize(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestLauncherTabOpensTerminal(t *testing.T) {
	a := readyApp(demoHosts...)
	tt := ui.NewTester(a.view, 1000, 700)

	if err := tt.Click("prod"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(a.tabs) != 1 || a.tabs[0].host != "prod" {
		t.Fatalf("tabs = %+v", a.tabs)
	}
	if a.activeTab != 0 {
		t.Fatalf("activeTab = %d", a.activeTab)
	}

	// Clicking the tab switches to it; it does not open another session.
	if err := tt.Click("prod"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(a.tabs) != 1 {
		t.Fatalf("clicking a tab must not open another session: %+v", a.tabs)
	}
}

func TestSameServerCanHaveManyTabs(t *testing.T) {
	a := readyApp(demoHosts...)
	tt := ui.NewTester(a.view, 1000, 700)

	// Open prod from the home page, go back home, open it again.
	if err := tt.Click("连接 prod"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("主页"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("连接 prod"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()

	if len(a.tabs) != 2 || a.tabs[0].host != "prod" || a.tabs[1].host != "prod" {
		t.Fatalf("tabs = %+v", a.tabs)
	}
	if a.tabs[0].id == a.tabs[1].id {
		t.Fatal("the two sessions must be distinct")
	}
	if a.activeTab != 1 {
		t.Fatalf("the newest session should be active, got %d", a.activeTab)
	}
}

func TestDuplicateSessionFromToolbar(t *testing.T) {
	a := readyApp(demoHosts...)
	a.tabs = []*terminalTab{{id: 1, host: "prod", forwardPane: newForwardPane()}}
	a.activeTab = 0
	tt := ui.NewTester(a.view, 1000, 700)

	if err := tt.Click("新会话（复制标签）"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(a.tabs) != 2 || a.tabs[1].host != "prod" {
		t.Fatalf("tabs = %+v", a.tabs)
	}
	if a.activeTab != 1 {
		t.Fatalf("activeTab = %d", a.activeTab)
	}
}

func TestStartPageIsCenteredAndStripIsOneRow(t *testing.T) {
	a := readyApp(demoHosts...)
	tt := ui.NewTester(a.view, 1000, 700)

	if r, ok := tt.Find("主页"); !ok || r.Y > 80 {
		t.Fatalf("the tab strip must sit at the top, got %+v", r)
	}
	hint, ok := tt.Find("Enter 连接并保存为配置")
	if !ok {
		t.Fatalf("quick connect hint missing: %v", tt.Texts())
	}
	if hint.Y < 250 || hint.Y > 450 {
		t.Fatalf("the quick connect field should be in the middle, y = %v", hint.Y)
	}
}

func TestHomeTabIsPinned(t *testing.T) {
	a := readyApp(demoHosts...)
	a.tabs = []*terminalTab{{id: 1, host: "prod"}}
	a.activeTab = 0
	tt := ui.NewTester(a.view, 1000, 700)

	// The home tab has no close button: sessions do.
	if _, ok := tt.Find("断开 prod"); !ok {
		t.Fatalf("session close button missing: %v", tt.Texts())
	}
	if err := tt.Click("主页"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.activeTab != -1 {
		t.Fatalf("home tab did not switch: activeTab = %d", a.activeTab)
	}
	if !tt.HasText("Enter 连接并保存为配置") {
		t.Fatalf("home page missing: %v", tt.Texts())
	}
}

func TestHomeAddServerButton(t *testing.T) {
	a := readyApp(demoHosts...)
	tt := ui.NewTester(a.view, 1000, 700)

	if err := tt.Click("添加服务器"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.editAlias != "" || a.draft.addr != "" || a.draft.auth != "key" {
		t.Fatalf("new server draft = %+v alias %q", a.draft, a.editAlias)
	}
}

func TestRecentConnections(t *testing.T) {
	a := readyApp(demoHosts...)
	a.recent = []recentConn{
		{Alias: "prod", At: time.Now().Add(-2 * time.Minute)},
		{Alias: "gone", At: time.Now()}, // no such host: not shown
	}
	tt := ui.NewTester(a.view, 1000, 700)

	if !tt.HasText("连接") {
		t.Fatalf("connection list header missing: %v", tt.Texts())
	}
	if !tt.HasText("staging") {
		t.Fatalf("a saved connection never used must still be listed: %v", tt.Texts())
	}
	if !tt.HasText("deploy@10.0.0.5:22") || !tt.HasText("2 分钟前") {
		t.Fatalf("history row missing: %v", tt.Texts())
	}
	if tt.HasText("gone") {
		t.Fatalf("a removed host must not appear: %v", tt.Texts())
	}

	if err := tt.Click("连接 prod"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(a.tabs) != 1 || a.tabs[0].host != "prod" {
		t.Fatalf("clicking history did not connect: %+v", a.tabs)
	}
	if a.recent[0].Alias != "prod" {
		t.Fatalf("recent = %+v", a.recent)
	}
}

func TestHomeContentIsCentered(t *testing.T) {
	a := readyApp(demoHosts...)
	a.recent = []recentConn{{Alias: "prod", At: time.Now()}}
	tt := ui.NewTester(a.view, 1000, 700)

	hint, ok := tt.Find("Enter 连接并保存为配置")
	if !ok {
		t.Fatalf("quick connect hint missing: %v", tt.Texts())
	}
	// The 560-wide block starts at (1000-560)/2 = 220; the hint sits at its
	// left edge, under the field.
	if hint.X < 200 || hint.X > 260 {
		t.Fatalf("the quick connect block should be centered, x = %v", hint.X)
	}
	// The add-server button sits after the field, at the block's right edge.
	add, ok := tt.Find("添加服务器")
	if !ok {
		t.Fatalf("add server button missing: %v", tt.Texts())
	}
	if add.X < 700 {
		t.Fatalf("add server should follow the input, x = %v", add.X)
	}
	head, ok := tt.Find("连接 prod")
	if !ok {
		t.Fatalf("connection list missing: %v", tt.Texts())
	}
	// The 560-wide block starts at (1000-560)/2 = 220.
	if head.X < 200 || head.X > 260 {
		t.Fatalf("the history block should be centered, x = %v", head.X)
	}
}

func TestNewTabMenuListsServers(t *testing.T) {
	a := readyApp(demoHosts...)
	tt := ui.NewTester(a.view, 1000, 700)

	if err := tt.Click("新建标签"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("添加服务器…") || !tt.HasText("导入 ~/.ssh/config") || !tt.HasText("设置…") {
		t.Fatalf("the server list menu did not open: %v", tt.Texts())
	}

	// Choosing an action from the menu works.
	if err := tt.Click("添加服务器…"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.editAlias != "" || a.draft.addr != "" {
		t.Fatalf("new server draft = %+v", a.draft)
	}
}

func TestEditorFitsItsWindow(t *testing.T) {
	a := editorApp()
	tt := ui.NewTester(a.editorView, 540, 620)

	for _, label := range []string{"名称", "地址", "分组", "认证", "私钥", "提权", "隧道"} {
		if !tt.HasText(label) {
			t.Fatalf("missing %q in %v", label, tt.Texts())
		}
	}
	// The last section and the footer must be on screen without scrolling.
	tunnel, ok := tt.Find("隧道（可选）")
	if !ok || tunnel.H <= 0 || tunnel.Y+tunnel.H > 560 {
		t.Fatalf("the form does not fit its window: 隧道 at %+v", tunnel)
	}
	if save, ok := tt.Find("保存"); !ok || save.H <= 0 {
		t.Fatalf("the footer is not visible: %+v", save)
	}
}

// TestEditorInputsAreAligned guards the layout bug where a widget built
// outside its container rendered full width above the row that expected it:
// every input must sit on the form's left edge, and paired ones side by side.
func TestEditorInputsAreAligned(t *testing.T) {
	a := openEditorApp()
	tt := ui.NewTester(a.editorView, 540, 660)
	img := tt.Image()
	if img == nil {
		t.Fatal("no image")
	}
	type box struct{ x0, y0, x1, y1 int }
	seen := make([][]bool, img.Bounds().Dy())
	for i := range seen {
		seen[i] = make([]bool, img.Bounds().Dx())
	}
	var boxes []box
	for y := 0; y < img.Bounds().Dy(); y++ {
		for x := 0; x < img.Bounds().Dx(); x++ {
			if seen[y][x] || !isWhite(img, x, y) {
				continue
			}
			stack := [][2]int{{x, y}}
			seen[y][x] = true
			b := box{x0: x, y0: y, x1: x, y1: y}
			n := 0
			for len(stack) > 0 {
				p := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				n++
				if p[0] < b.x0 {
					b.x0 = p[0]
				}
				if p[0] > b.x1 {
					b.x1 = p[0]
				}
				if p[1] < b.y0 {
					b.y0 = p[1]
				}
				if p[1] > b.y1 {
					b.y1 = p[1]
				}
				for _, d := range [][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
					nx, ny := p[0]+d[0], p[1]+d[1]
					if nx >= 0 && ny >= 0 && nx < img.Bounds().Dx() && ny < img.Bounds().Dy() &&
						!seen[ny][nx] && isWhite(img, nx, ny) {
						seen[ny][nx] = true
						stack = append(stack, [2]int{nx, ny})
					}
				}
			}
			if n > 400 {
				boxes = append(boxes, b)
			}
		}
	}
	if len(boxes) < 9 {
		t.Fatalf("expected the form's inputs, found %d white regions", len(boxes))
	}
	// A full-width field must sit on the form's left edge; a paired field on
	// one of the pair's two edges. The bug this guards was a full-width input
	// laid out inset above its row.
	for _, b := range boxes {
		if b.y1 > 600 || b.x1-b.x0 < 80 {
			continue // the footer and buttons beside fields
		}
		w := b.x1 - b.x0
		// Fields sit at x=21, or x=43 inside a collapsible; the second of a
		// pair at 277, or 288 inside one.
		near := func(v int, allowed ...int) bool {
			for _, a := range allowed {
				if v >= a-3 && v <= a+3 {
					return true
				}
			}
			return false
		}
		switch {
		case w > 300:
			if !near(b.x0, 21, 43) {
				t.Fatalf("a full-width field at x%d is not on the left edge (y%d)", b.x0, b.y0)
			}
		case w >= 200:
			if !near(b.x0, 21, 43, 277, 288) {
				t.Fatalf("a paired field at x%d is outside its column (y%d)", b.x0, b.y0)
			}
		}
	}
}

func isWhite(img *image.RGBA, x, y int) bool {
	i := img.PixOffset(x, y)
	return img.Pix[i] > 250 && img.Pix[i+1] > 250 && img.Pix[i+2] > 250
}

func TestHostFromDraftKeepsHiddenFields(t *testing.T) {
	existing := &config.Host{
		Addr: "10.0.0.5:22", User: "deploy",
		Approval: "always",
		Timeout:  config.Duration(30 * time.Second),
		Deny:     []string{"^reboot"},
		Allow:    []string{"^ls"},
		HostKey:  "SHA256:abc",
		Workdir:  "/srv",
		Auth:     config.Auth{Type: "key", Key: "/k", Cert: "/cert", Username: "u2"},
		Elevate: &config.Elevate{
			Mode: "sudo", User: "root", Password: "pw",
			Nopasswd: true, Always: true, WrapShell: true, PTY: "always",
			Deny: []string{"^visudo"}, Allow: []string{"^systemctl"},
		},
	}
	d := draftOf("prod", existing)
	d.addr = "10.0.0.6:22"
	d.elevPwd = "new-pw"

	h, err := hostFromDraft(existing, d)
	if err != nil {
		t.Fatal(err)
	}
	if h.Addr != "10.0.0.6:22" {
		t.Fatalf("addr = %q", h.Addr)
	}
	if h.Approval != "always" || h.Timeout != config.Duration(30*time.Second) ||
		len(h.Deny) != 1 || len(h.Allow) != 1 {
		t.Fatalf("host policy lost: %+v", h)
	}
	if h.Auth.Cert != "/cert" || h.Auth.Username != "u2" {
		t.Fatalf("auth extras lost: %+v", h.Auth)
	}
	e := h.Elevate
	if e == nil || e.Mode != "sudo" || e.Password != "new-pw" ||
		!e.Nopasswd || !e.Always || !e.WrapShell || e.PTY != "always" ||
		len(e.Deny) != 1 || len(e.Allow) != 1 {
		t.Fatalf("elevate policy lost: %+v", e)
	}
	if existing.Addr != "10.0.0.5:22" || existing.Elevate.Password != "pw" {
		t.Fatal("the existing host must not be mutated")
	}

	// Turning elevation off clears it, as the form says.
	d.elev = "none"
	if h, _ := hostFromDraft(existing, d); h.Elevate != nil {
		t.Fatalf("elevation should be cleared: %+v", h.Elevate)
	}
}

func TestEditorTunnelRows(t *testing.T) {
	a := readyApp(demoHosts...)
	a.openEditor("")
	a.draft.showTunnels = true
	tt := ui.NewTester(a.editorView, 540, 660)

	if err := tt.Click("添加隧道"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(a.draft.forwards) != 1 || a.draft.forwards[0].Kind != "local" {
		t.Fatalf("forwards = %+v", a.draft.forwards)
	}

	// A row that is still empty is refused, not silently dropped.
	a.draft.alias = "x"
	a.draft.addr = "1.2.3.4:22"
	a.draft.user = "u"
	if _, err := hostFromDraft(nil, a.draft); err == nil {
		t.Fatal("an empty tunnel row must be refused")
	}

	a.draft.forwards[0] = config.Forward{Kind: "local", Listen: "127.0.0.1:5432", Target: "db:5432"}
	if _, err := hostFromDraft(nil, a.draft); err != nil {
		t.Fatalf("a filled tunnel row must be accepted: %v", err)
	}

	// × removes the row.
	if err := tt.Click("删除这条隧道"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(a.draft.forwards) != 0 {
		t.Fatalf("forwards = %+v", a.draft.forwards)
	}
}

func TestLockKeepsSessions(t *testing.T) {
	a := readyApp(demoHosts...)
	a.tabs = []*terminalTab{
		{id: 1, host: "prod", forwardPane: newForwardPane()},
		{id: 2, host: "staging", forwardPane: newForwardPane()},
	}
	a.activeTab = 1
	a.lock()
	if len(a.tabs) != 2 || a.activeTab != 1 {
		t.Fatalf("a lock must not close sessions: %+v active=%d", a.tabs, a.activeTab)
	}
	if a.tabs[1].closed {
		t.Fatal("the session was closed by the lock")
	}
}

func TestCloseTabKeepsSelection(t *testing.T) {
	a := readyApp(demoHosts...)
	a.tabs = []*terminalTab{
		{id: 1, host: "a", forwardPane: newForwardPane()},
		{id: 2, host: "b", forwardPane: newForwardPane()},
		{id: 3, host: "c", forwardPane: newForwardPane()},
	}
	a.activeTab = 1 // b
	a.closeTab(0)   // a, before the active one
	if a.activeTab != 0 || a.tabs[0].host != "b" {
		t.Fatalf("active = %d, tabs = %+v", a.activeTab, a.tabs)
	}

	a.activeTab = 1 // c (b, c)
	a.closeTab(1)   // the active one
	if a.activeTab != 0 || a.tabs[0].host != "b" {
		t.Fatalf("active = %d, tabs = %+v", a.activeTab, a.tabs)
	}
}

func TestPasswordAuthSavesWithoutExtraPolicy(t *testing.T) {
	dir := t.TempDir()
	cfg, err := config.Parse([]byte(`{"hosts":{}}`), dir)
	if err != nil {
		t.Fatal(err)
	}
	d := editorDraft{
		alias: "p", addr: "1.2.3.4:22", user: "u",
		auth: "password", secret: "hunter2", hostKey: "known_hosts", elev: "none",
	}
	alias, err := applyDraft(cfg, "", d)
	if err != nil {
		t.Fatalf("password auth must save: %v", err)
	}
	if cfg.Hosts[alias].Auth.Password != "hunter2" {
		t.Fatalf("password = %q", cfg.Hosts[alias].Auth.Password)
	}

	// What the vault would store parses back, so the save cannot fail later.
	data, err := cfg.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	back, err := config.Parse(data, dir)
	if err != nil {
		t.Fatalf("saved catalogue does not parse: %v", err)
	}
	if back.Hosts["p"].Auth.Password != "hunter2" {
		t.Fatalf("password after reload = %q", back.Hosts["p"].Auth.Password)
	}
}

func TestServersWindow(t *testing.T) {
	a := readyApp(demoHosts...)
	tt := ui.NewTester(a.serversView, 760, 560)

	for _, want := range []string{
		"服务器 · 2", "prod", "staging", "deploy@10.0.0.5:22", "key",
		"连接 prod", "编辑 prod", "删除 prod",
		"连接 staging", "编辑 staging", "添加服务器",
	} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %v", want, tt.Texts())
		}
	}

	// The search filters by alias, address or group.
	a.serverQuery = "staging"
	tt.Frame()
	if !tt.HasText("staging") {
		t.Fatalf("filtered host missing: %v", tt.Texts())
	}
	if tt.HasText("编辑 prod") {
		t.Fatalf("prod should be filtered out: %v", tt.Texts())
	}
}

func TestTitleBarHasServerManager(t *testing.T) {
	a := readyApp(demoHosts...)
	a.tabs = []*terminalTab{{id: 1, host: "prod", forwardPane: newForwardPane()}}
	a.activeTab = 0
	tt := ui.NewTester(a.view, 1000, 700)

	if !tt.HasText("服务器管理") {
		t.Fatalf("manager button missing from the strip: %v", tt.Texts())
	}
	if err := tt.Click("服务器管理"); err != nil {
		t.Fatal(err) // headless openServers is a no-op, but the hit must hit
	}
}

func TestNewTabMenuHasManager(t *testing.T) {
	a := readyApp(demoHosts...)
	tt := ui.NewTester(a.view, 1000, 700)
	if err := tt.Click("新建标签"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("管理服务器…") {
		t.Fatalf("manager entry missing from the + menu: %v", tt.Texts())
	}
}

func TestTabContextMenuDuplicates(t *testing.T) {
	a := readyApp(demoHosts...)
	a.tabs = []*terminalTab{{id: 1, host: "prod", forwardPane: newForwardPane()}}
	a.activeTab = 0
	tt := ui.NewTester(a.view, 1000, 700)

	if err := tt.RightClick("prod"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("新会话（复制标签）") || !tt.HasText("断开") {
		t.Fatalf("tab menu missing: %v", tt.Texts())
	}
	if err := tt.Click("新会话（复制标签）"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(a.tabs) != 2 || a.tabs[1].host != "prod" {
		t.Fatalf("tabs = %+v", a.tabs)
	}
}

func TestTabsFitMany(t *testing.T) {
	a := readyApp()
	for i := 1; i <= 20; i++ {
		label := fmt.Sprintf("srv%02d", i)
		a.tabs = append(a.tabs, &terminalTab{
			id: i, host: label, label: label, forwardPane: newForwardPane(),
		})
	}
	a.activeTab = 19
	tt := ui.NewTester(a.view, 1189, 760)

	// Compact tabs fit more than a dozen at this width; the rest scroll.
	visible := 0
	for i := 1; i <= 20; i++ {
		if r, ok := tt.Find(fmt.Sprintf("srv%02d", i)); ok && r.W > 0 {
			visible++
		}
	}
	if visible < 16 {
		t.Fatalf("only %d of 20 tabs fit at 1189 px", visible)
	}
}

func TestDirPaneHistory(t *testing.T) {
	p := &dirPane{}
	p.record("/a")
	p.record("/b")
	p.record("/c")
	if !p.canBack() || p.canForward() {
		t.Fatalf("history = %v at %d", p.hist, p.histIdx)
	}
	p.histIdx--
	p.histIdx--
	if p.hist[p.histIdx] != "/a" {
		t.Fatalf("back reached %q", p.hist[p.histIdx])
	}
	p.record("/x") // a new visit drops the forward entries
	if len(p.hist) != 2 || p.hist[1] != "/x" || p.canForward() {
		t.Fatalf("history = %v at %d", p.hist, p.histIdx)
	}
}

func TestLeavingMonitorShowsTerminal(t *testing.T) {
	a := readyApp(demoHosts...)
	tb := &terminalTab{id: 1, host: "prod", forwardPane: newForwardPane()}
	tb.monitorPane.open = true
	tb.monitorPane.text = monitorSample
	a.tabs = []*terminalTab{tb}
	a.activeTab = 0

	tt := ui.NewTester(a.view, 1000, 700)
	if !tt.HasText("主机概览") {
		t.Fatalf("the monitor should be shown: %v", tt.Texts())
	}
	if err := tt.Click("终端"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if tb.mode() != modeTerminal {
		t.Fatalf("mode = %v after clicking 终端", tb.mode())
	}
	if tt.HasText("主机概览") {
		t.Fatalf("the monitor still owns the pane: %v", tt.Texts())
	}
	if !tt.HasText("正在连接 prod") {
		t.Fatalf("the terminal is not shown: %v", tt.Texts())
	}
}

func TestHostKeyPromptView(t *testing.T) {
	a := readyApp(demoHosts...)
	a.hostKeyReq = &hostKeyRequest{
		host: "prod",
		unk: &daemon.UnknownHostKeyError{
			Host:        "10.0.0.5:22",
			Fingerprint: "SHA256:abcdef",
		},
	}
	tt := ui.NewTester(a.hostKeyView, 520, 300)
	for _, want := range []string{"首次连接 prod", "SHA256:abcdef", "信任", "取消"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %v", want, tt.Texts())
		}
	}
}

func TestFilesTreeExpandsAndCollapses(t *testing.T) {
	a := filesApp(
		agent.FileEntry{Name: "bin", Path: "/srv/app/bin", Dir: true, Mode: "drwxr-xr-x"},
	)
	fp := &a.tabs[0].filesPane
	fp.remote.children["/srv/app/bin"] = []agent.FileEntry{
		{Name: "app", Path: "/srv/app/bin/app", Size: 10, Mode: "-rwxr-xr-x"},
	}

	tt := ui.NewTester(a.view, 1200, 700)
	if tt.HasText("app") {
		t.Fatalf("a collapsed folder must not show its children: %v", tt.Texts())
	}
	if err := tt.Click("展开或折叠 bin"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !fp.remote.expanded["/srv/app/bin"] {
		t.Fatal("the folder did not expand")
	}
	if !tt.HasText("app") {
		t.Fatalf("the child is not shown: %v", tt.Texts())
	}
	if err := tt.Click("展开或折叠 bin"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if tt.HasText("app") {
		t.Fatalf("the folder did not collapse: %v", tt.Texts())
	}
}

func TestEditorHasTestConnection(t *testing.T) {
	a := readyApp(demoHosts...)
	a.openEditor("")
	tt := ui.NewTester(a.editorView, 540, 660)

	for _, want := range []string{"保存", "取消", "测试连接"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %v", want, tt.Texts())
		}
	}
	// Without a running agent the test must say so rather than fail silently.
	if err := tt.Click("测试连接"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !strings.Contains(a.editErr, "agent") {
		t.Fatalf("editErr = %q", a.editErr)
	}
}

func TestUnlockButtonFollowsTheField(t *testing.T) {
	for _, needs := range []bool{false, true} {
		a := newTestApp()
		a.needsVault = needs
		tt := ui.NewTester(a.view, 1000, 700)
		label := "解锁"
		if needs {
			label = "创建并解锁"
		}
		r, ok := tt.Find(label)
		if !ok {
			t.Fatalf("missing %q in %v", label, tt.Texts())
		}
		// The sheet is 470 wide in a 1000 window: a button after the field
		// sits in its right half, not under it.
		if r.X < 500 {
			t.Fatalf("%q is under the field, x = %v", label, r.X)
		}
	}
}

func TestConfirmWindowView(t *testing.T) {
	a := readyApp(demoHosts...)
	confirmed := false
	a.confirmReq = &confirmRequest{
		title:        "删除连接 prod",
		message:      "删除后它的认证、提权口令和隧道配置会一起消失，不能撤销。",
		detail:       "正在运行的会话不会被断开。",
		confirmLabel: "删除",
		onConfirm:    func() { confirmed = true },
	}
	tt := ui.NewTester(a.confirmView, 480, 260)
	for _, want := range []string{"删除连接 prod", "不能撤销", "删除", "取消"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %v", want, tt.Texts())
		}
	}
	if err := tt.Click("删除"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !confirmed {
		t.Fatal("the confirm button did not run the action")
	}
}

func TestManagerDeleteRemovesHost(t *testing.T) {
	dir := t.TempDir()
	ep := testEndpoint(t)
	ag, err := agent.Open(agent.Options{
		VaultPath: filepath.Join(dir, "vault"),
		Endpoint:  ep,
		Prompt:    func(string) (string, error) { return "correct-horse", nil },
		Logger:    log.New(io.Discard, "", 0),
		AuditPath: filepath.Join(dir, "audit.log"),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer ag.Lock()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = ag.Serve(ctx) }()
	select {
	case <-ag.Ready():
	case <-time.After(5 * time.Second):
		t.Fatal("agent never started")
	}
	if err := ag.UpdateConfig(func(cfg *config.Config) error {
		cfg.Hosts["prod"] = &config.Host{
			Addr: "192.0.2.10:22", User: "deploy",
			Auth: config.Auth{Type: "key", Key: "/home/u/.ssh/id_ed25519"},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	a := readyApp()
	a.opts.Endpoint = ep
	a.mu.Lock()
	a.ag = ag
	a.mu.Unlock()
	a.hosts = []proto.HostInfo{{Alias: "prod", Addr: "192.0.2.10:22", User: "deploy"}}

	a.deleteHostByAlias("prod")

	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		_, stillThere := ag.Config().Hosts["prod"]
		hosts := a.snapshot().hosts
		if !stillThere && len(hosts) == 0 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("host not deleted: config=%v hosts=%v err=%q",
		ag.Config().Hosts, a.snapshot().hosts, a.snapshot().serversErr)
}

func TestHostKeyChangedView(t *testing.T) {
	a := readyApp(demoHosts...)
	a.hostKeyReq = &hostKeyRequest{
		host: "prod",
		mismatch: &daemon.HostKeyMismatchError{
			Host: "10.0.0.5:22",
			Got:  "SHA256:newfingerprint",
			Want: "SHA256:oldfingerprint",
		},
	}
	tt := ui.NewTester(a.hostKeyView, 560, 430)
	for _, want := range []string{
		"主机密钥变了：prod",
		"SHA256:newfingerprint",
		"SHA256:oldfingerprint",
		"更新指纹并重连",
		"我已通过可信渠道核对过上面的新指纹",
	} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %v", want, tt.Texts())
		}
	}
	if a.hostKeyReq.confirmed {
		t.Fatal("re-pinning must start unconfirmed")
	}
	if err := tt.Click("我已通过可信渠道核对过上面的新指纹"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.hostKeyReq.confirmed {
		t.Fatal("checking the box did not enable the re-pin")
	}
}

func TestGroupSectionsOrderAndEmpty(t *testing.T) {
	hosts := []proto.HostInfo{
		{Alias: "a", Group: "生产"},
		{Alias: "b", Group: "测试"},
		{Alias: "c"},
	}
	sections := groupSections([]string{"空分组", "生产"}, hosts, true)
	var names []string
	for _, g := range sections {
		names = append(names, g.name)
	}
	if strings.Join(names, ",") != "空分组,生产,测试," {
		t.Fatalf("sections = %v", names)
	}
	if len(sections[0].hosts) != 0 {
		t.Fatal("an empty group must stay visible when not searching")
	}
	if len(sections[1].hosts) != 1 || sections[3].hosts[0].Alias != "c" {
		t.Fatalf("sections = %+v", sections)
	}

	// A search hides empty groups; ungrouped hosts stay for themselves.
	sections = groupSections([]string{"空分组", "生产"}, hosts[1:], false)
	names = names[:0]
	for _, g := range sections {
		names = append(names, g.name)
	}
	if strings.Join(names, ",") != "测试," {
		t.Fatalf("searched sections = %v", names)
	}
}

func TestServersViewShowsGroups(t *testing.T) {
	a := readyApp(
		proto.HostInfo{Alias: "web1", User: "u", Addr: "10.0.0.1:22", Auth: "key", Group: "生产"},
		proto.HostInfo{Alias: "web2", User: "u", Addr: "10.0.0.2:22", Auth: "key", Group: "生产"},
		proto.HostInfo{Alias: "db1", User: "u", Addr: "10.0.0.3:22", Auth: "key", Group: "测试"},
		proto.HostInfo{Alias: "loose", User: "u", Addr: "10.0.0.4:22", Auth: "key"},
	)
	tt := ui.NewTester(a.serversView, 760, 620)
	for _, want := range []string{
		"生产", "测试", "未分组", "web1", "web2", "db1", "loose",
		"在 生产 中添加服务器", "重命名分组 生产", "解散分组 生产",
		"把 loose 移到分组",
	} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %v", want, tt.Texts())
		}
	}
}

func TestNewTabMenuGroupsServers(t *testing.T) {
	a := readyApp(
		proto.HostInfo{Alias: "web1", User: "u", Addr: "10.0.0.1:22", Auth: "key", Group: "生产"},
		proto.HostInfo{Alias: "web2", User: "u", Addr: "10.0.0.2:22", Auth: "key", Group: "生产"},
		proto.HostInfo{Alias: "box", User: "u", Addr: "10.0.0.3:22", Auth: "key"},
	)
	tt := ui.NewTester(a.view, 1000, 700)
	if err := tt.Click("新建标签"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	menu := strings.Join(tt.Menu(), "|")
	if !strings.Contains(menu, "生产 (2)") || !strings.Contains(menu, "未分组 (1)") {
		t.Fatalf("grouped menu = %v", tt.Menu())
	}
	if err := tt.ChooseMenuItem("生产 (2)", "web2"); err != nil {
		t.Fatal(err)
	}
	if len(a.tabs) != 1 || a.tabs[0].host != "web2" {
		t.Fatalf("chosen session = %+v", a.tabs)
	}
}

func TestGroupDialogView(t *testing.T) {
	a := readyApp()
	a.groupDlg = groupDialog{mode: "create", alias: "prod", value: "生产"}
	tt := ui.NewTester(a.groupDialogView, 460, 250)
	for _, want := range []string{"新建分组", "创建后会把 prod 移进这个分组。", "保存", "取消"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %v", want, tt.Texts())
		}
	}

	a.groupDlg = groupDialog{mode: "rename", from: "生产", value: "生产", err: "分组 \"生产\" 已经存在"}
	tt = ui.NewTester(a.groupDialogView, 460, 250)
	for _, want := range []string{"重命名分组 生产", "已经存在"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %v", want, tt.Texts())
		}
	}
}
