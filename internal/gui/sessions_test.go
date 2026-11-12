package gui

import (
	"net"
	"testing"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"

	"github.com/nonex-code/ai-ssh/internal/agent"
	"github.com/nonex-code/ai-ssh/internal/config"
	"github.com/nonex-code/ai-ssh/internal/proto"
	"github.com/nonex-code/ai-ssh/internal/sshconfig"
)

func TestParseQuick(t *testing.T) {
	cases := []struct {
		in               string
		user, host, port string
		ok               bool
	}{
		{"deploy@10.0.0.5:2222", "deploy", "10.0.0.5", "2222", true},
		{"deploy@prod", "deploy", "prod", "22", true},
		{"prod:2200", osUser(), "prod", "2200", true},
		{"prod", osUser(), "prod", "22", true},
		{"", "", "", "", false},
		{"@", "", "", "", false},
	}
	for _, c := range cases {
		user, host, port, ok := parseQuick(c.in)
		if user != c.user || host != c.host || port != c.port || ok != c.ok {
			t.Fatalf("parseQuick(%q) = %q %q %q %v, want %q %q %q %v",
				c.in, user, host, port, ok, c.user, c.host, c.port, c.ok)
		}
	}
}

func TestMergeSSHConfig(t *testing.T) {
	cfg, err := config.Parse([]byte(`{
  "hosts": {
    "prod": {
      "addr": "10.0.0.5:22",
      "user": "deploy",
      "auth": {"type": "key", "key": "/k"}
    }
  }
}`), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	added, skipped := mergeSSHConfig(cfg, []sshconfig.Entry{
		{Alias: "prod", HostName: "10.0.0.5", User: "deploy"},
		{Alias: "stg", HostName: "10.0.0.9", User: "ci", Port: "2222", IdentityFile: "~/.ssh/stg"},
	})
	if added != 1 || skipped != 1 {
		t.Fatalf("added=%d skipped=%d", added, skipped)
	}
	h := cfg.Hosts["stg"]
	if h == nil {
		t.Fatal("imported host missing")
	}
	if h.Addr != "10.0.0.9:2222" || h.User != "ci" || h.Auth.Key != "~/.ssh/stg" || h.Group != "ssh-config" {
		t.Fatalf("imported host = %+v", h)
	}
	// The existing one is untouched.
	if cfg.Hosts["prod"].Addr != "10.0.0.5:22" {
		t.Fatalf("existing host changed: %+v", cfg.Hosts["prod"])
	}
}

func TestStartPageQuickConnect(t *testing.T) {
	a := readyApp(demoHosts...)
	tt := ui.NewTester(a.view, 1000, 700)

	if !tt.HasText("Enter 连接并保存为配置") {
		t.Fatalf("quick connect field missing: %v", tt.Texts())
	}
	// The home page has the field and the add-server button; imports moved
	// to the + menu of the tab strip.
	if tt.HasText("导入 ~/.ssh/config") {
		t.Fatalf("the import arrow should be gone: %v", tt.Texts())
	}
	if err := tt.Click("添加服务器"); err != nil {
		t.Fatal(err)
	}
	if a.editAlias != "" || a.draft.addr != "" {
		t.Fatalf("add server draft = %+v", a.draft)
	}
}

func TestQuickConnectPrefillsEditor(t *testing.T) {
	a := readyApp(demoHosts...)
	a.quick = "ops@bastion:2222"
	a.quickConnect()

	if a.draft.addr != "bastion:2222" || a.draft.user != "ops" {
		t.Fatalf("draft = %+v", a.draft)
	}
	if !a.connectOnSave {
		t.Fatal("quick connect should open a terminal after saving")
	}
	if a.editAlias != "" {
		t.Fatalf("quick connect creates a new host, editAlias = %q", a.editAlias)
	}
}

// --- transfer queue and multi-select -----------------------------------------

func TestTransferQueue(t *testing.T) {
	q := newTransferQueue()
	q.add(&transferJob{local: "a"})
	q.add(&transferJob{local: "b"})

	if got := q.active(); got != 2 {
		t.Fatalf("active = %d", got)
	}
	jobs := q.snapshot(5)
	if len(jobs) != 2 || jobs[0].local != "a" || jobs[0].state != "queued" {
		t.Fatalf("jobs = %+v", jobs)
	}
	if last := q.snapshot(1); len(last) != 1 || last[0].local != "b" {
		t.Fatalf("snapshot(1) = %+v", last)
	}
}

func TestFilesMultiSelect(t *testing.T) {
	a := filesApp(
		agent.FileEntry{Name: "run.sh", Path: "/srv/app/run.sh", Size: 10},
		agent.FileEntry{Name: "app.log", Path: "/srv/app/app.log", Size: 20},
	)
	tt := ui.NewTester(a.view, 1200, 700)

	if err := tt.Click("run.sh"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ClickWith(ui.Ctrl, "app.log"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()

	fp := &a.tabs[0].filesPane
	if fp.remote.count() != 2 {
		t.Fatalf("selection = %v", fp.remote.sel)
	}
	if !fp.remote.sel["/srv/app/run.sh"] || !fp.remote.sel["/srv/app/app.log"] {
		t.Fatalf("selection = %v", fp.remote.sel)
	}

	// A plain click narrows it back to one.
	if err := tt.Click("run.sh"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if fp.remote.count() != 1 {
		t.Fatalf("plain click should replace the selection: %v", fp.remote.sel)
	}
}

// --- port forwarding pane ----------------------------------------------------

func forwardApp() *App {
	a := readyApp(demoHosts...)
	a.tabs = []*terminalTab{{id: 1, host: "demo", forwardPane: newForwardPane()}}
	a.tabs[0].forwardPane.open = true
	a.activeTab = 0
	return a
}

func TestForwardPaneRenders(t *testing.T) {
	tt := ui.NewTester(forwardApp().view, 1200, 700)
	for _, want := range []string{"端口转发", "开始转发", "还没有转发规则"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %v", want, tt.Texts())
		}
	}
}

func TestForwardStartWithoutAgent(t *testing.T) {
	a := forwardApp()
	tt := ui.NewTester(a.view, 1200, 700)
	if err := tt.Click("开始转发"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("agent 未就绪") {
		t.Fatalf("expected an explanation, got %v", tt.Texts())
	}
	if len(a.tabs[0].forwardPane.list) != 0 {
		t.Fatal("nothing should have started")
	}
}

func TestForwardStopRemovesRule(t *testing.T) {
	a := forwardApp()
	// A forward without a live tunnel: stop only has to drop the row, and a
	// nil Close is not reached because we pass a real value below.
	a.tabs[0].forwardPane.list = []*agent.Forward{}
	tt := ui.NewTester(a.view, 1200, 700)
	if !tt.HasText("还没有转发规则") {
		t.Fatalf("unexpected rows: %v", tt.Texts())
	}
}

func TestToolbarSwitchesModes(t *testing.T) {
	a := readyApp(demoHosts...)
	a.tabs = []*terminalTab{{id: 1, host: "demo", forwardPane: newForwardPane()}}
	a.activeTab = 0
	tt := ui.NewTester(a.view, 1200, 700)

	if err := tt.Click("转发"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.tabs[0].forwardPane.open || a.tabs[0].filesPane.open {
		t.Fatalf("mode = %+v", a.tabs[0])
	}
	if !tt.HasText("端口转发") {
		t.Fatalf("forward pane missing: %v", tt.Texts())
	}

	if err := tt.Click("终端"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.tabs[0].forwardPane.open || a.tabs[0].filesPane.open {
		t.Fatalf("terminal mode not restored: %+v", a.tabs[0])
	}
}

// --- layout: the terminal must not cover the toolbar -------------------------

func TestToolbarIsAboveTerminal(t *testing.T) {
	if _, err := terminal.LibraryPath(); err != nil {
		t.Skipf("terminal library unavailable: %v", err)
	}

	client, server := net.Pipe()
	defer server.Close()
	term, err := terminal.New(terminal.Options{Conn: client})
	if err != nil {
		t.Fatalf("terminal: %v", err)
	}
	defer term.Close()

	a := readyApp(demoHosts...)
	a.tabs = []*terminalTab{{
		id: 1, host: "demo", term: term, forwardPane: newForwardPane(),
	}}
	a.activeTab = 0
	tt := ui.NewTester(a.view, 1200, 700)

	toolbar, ok := tt.Find("转发")
	if !ok {
		t.Fatalf("toolbar missing: %v", tt.Texts())
	}
	grid, ok := tt.Find("Terminal")
	if !ok {
		t.Fatalf("terminal missing: %v", tt.Texts())
	}
	if grid.Y < toolbar.Y+toolbar.H-1 {
		t.Fatalf("the terminal (y=%.0f) overlaps the toolbar (y=%.0f h=%.0f)", grid.Y, toolbar.Y, toolbar.H)
	}

	// And the toolbar really is clickable where it is drawn.
	tt.ClickAt(toolbar.X+toolbar.W/2, toolbar.Y+toolbar.H/2)
	tt.Frame()
	if !a.tabs[0].forwardPane.open {
		t.Fatal("the toolbar click did not reach the button")
	}
}

// --- favorites, forwards text, drag and drop ---------------------------------

func TestFavoriteOrder(t *testing.T) {
	hosts := []proto.HostInfo{
		{Alias: "a"},
		{Alias: "b", Favorite: true},
		{Alias: "c"},
		{Alias: "d", Favorite: true},
	}
	got := favoriteOrder(hosts, nil)
	var order []string
	for _, h := range got {
		order = append(order, h.Alias)
	}
	if len(order) != 4 || order[0] != "b" || order[1] != "d" {
		t.Fatalf("favorites first: %v", order)
	}

	// The saved order applies within each half.
	got = favoriteOrder([]proto.HostInfo{{Alias: "x"}, {Alias: "y"}, {Alias: "z"}}, []string{"z", "y"})
	order = nil
	for _, h := range got {
		order = append(order, h.Alias)
	}
	if order[0] != "z" || order[1] != "y" || order[2] != "x" {
		t.Fatalf("saved order: %v", order)
	}
}

func TestValidateForwards(t *testing.T) {
	ok := []config.Forward{
		{Kind: "local", Listen: "127.0.0.1:5432", Target: "db:5432"},
		{Kind: "dynamic", Listen: "127.0.0.1:1080"},
	}
	if err := validateForwards(ok); err != nil {
		t.Fatalf("valid tunnels rejected: %v", err)
	}

	bad := []struct {
		name string
		fw   config.Forward
	}{
		{"unknown kind", config.Forward{Kind: "sideways", Listen: "1:2", Target: "3:4"}},
		{"listen without port", config.Forward{Kind: "local", Listen: "8080", Target: "db:5432"}},
		{"target without port", config.Forward{Kind: "local", Listen: "127.0.0.1:5432", Target: "db"}},
		{"local without target", config.Forward{Kind: "local", Listen: "127.0.0.1:5432"}},
	}
	for _, c := range bad {
		if err := validateForwards([]config.Forward{c.fw}); err == nil {
			t.Fatalf("%s must be refused", c.name)
		}
	}
}
