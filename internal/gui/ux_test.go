package gui

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/nonex-code/ai-ssh/internal/localshell"
)

func TestWindowStateRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gui.json")

	st := loadWindowState(path)
	if st.Width != defaultWidth || st.Height != defaultHeight {
		t.Fatalf("defaults = %+v", st)
	}
	if !st.autoCheckUpdates() {
		t.Fatal("a fresh state must check for updates by default")
	}

	no := false
	want := windowState{
		Width: 1400, Height: 900, X: 40, Y: 60,
		AutoCheckUpdate:   &no,
		AutoInstallUpdate: true,
		LastUpdateCheck:   time.Unix(1700000000, 0).UTC(),
	}
	if err := saveWindowState(path, want); err != nil {
		t.Fatal(err)
	}
	got := loadWindowState(path)
	if got.Width != want.Width || got.Height != want.Height ||
		got.X != want.X || got.Y != want.Y || got.Maximized != want.Maximized {
		t.Fatalf("round trip = %+v, want %+v", got, want)
	}
	if got.autoCheckUpdates() || !got.AutoInstallUpdate || !got.LastUpdateCheck.Equal(want.LastUpdateCheck) {
		t.Fatalf("update prefs = %+v", got)
	}

	// Nonsense sizes fall back to the defaults instead of a sliver of a window.
	bad := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(bad, []byte(`{"width":10,"height":10}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if st := loadWindowState(bad); st.Width != defaultWidth || st.Height != defaultHeight {
		t.Fatalf("clamp = %+v", st)
	}

	// A corrupt file is not fatal.
	corrupt := filepath.Join(t.TempDir(), "c.json")
	if err := os.WriteFile(corrupt, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if st := loadWindowState(corrupt); st.Width != defaultWidth {
		t.Fatalf("corrupt file = %+v", st)
	}
}

func TestTabStatusDot(t *testing.T) {
	a := readyApp(demoHosts...)
	a.tabs = []*terminalTab{
		{id: 1, host: "live", status: tabLive, forwardPane: newForwardPane()},
		{id: 2, host: "broken", status: tabFailed, forwardPane: newForwardPane()},
	}
	tt := ui.NewTester(a.view, 1000, 700)
	if !tt.HasText("live") || !tt.HasText("broken") {
		t.Fatalf("tabs missing: %v", tt.Texts())
	}
	// One dot per session tab.
	dots := 0
	for _, s := range tt.Texts() {
		if s == "●" {
			dots++
		}
	}
	if dots != 2 {
		t.Fatalf("dots = %d in %v", dots, tt.Texts())
	}
}

func TestToolbarShowsEveryMode(t *testing.T) {
	modes := []tabMode{modeTerminal, modeFiles, modeForward, modeMonitor}
	for _, mode := range modes {
		a := readyApp(demoHosts...)
		tb := &terminalTab{id: 1, host: "demo", forwardPane: newForwardPane()}
		switch mode {
		case modeFiles:
			tb.filesPane.open = true
		case modeForward:
			tb.forwardPane.open = true
		case modeMonitor:
			tb.monitorPane.open = true
		}
		a.tabs = []*terminalTab{tb}
		a.activeTab = 0

		tt := ui.NewTester(a.view, 1100, 700)
		for _, label := range []string{"终端", "文件", "转发", "详情"} {
			if !tt.HasText(label) {
				t.Fatalf("mode %d: missing %q in %v", mode, label, tt.Texts())
			}
		}
	}
}

func TestShortcutsCycleTabs(t *testing.T) {
	a := readyApp(demoHosts...)
	a.tabs = []*terminalTab{
		{id: 1, host: "one", forwardPane: newForwardPane()},
		{id: 2, host: "two", forwardPane: newForwardPane()},
	}
	a.activeTab = -1
	tt := ui.NewTester(a.view, 1000, 700)

	tt.Key(ui.Ctrl, ui.KeyTab)
	tt.Frame()
	if a.activeTab != 0 {
		t.Fatalf("after Ctrl+Tab: %d", a.activeTab)
	}
	tt.Key(ui.Ctrl, ui.KeyTab)
	tt.Frame()
	if a.activeTab != 1 {
		t.Fatalf("after another Ctrl+Tab: %d", a.activeTab)
	}
	tt.Key(ui.Ctrl|ui.Shift, ui.KeyTab)
	tt.Frame()
	if a.activeTab != 0 {
		t.Fatalf("after Ctrl+Shift+Tab: %d", a.activeTab)
	}
}

func TestEscapeReturnsToTerminal(t *testing.T) {
	a := readyApp(demoHosts...)
	tb := &terminalTab{id: 1, host: "demo", forwardPane: newForwardPane()}
	tb.forwardPane.open = true
	a.tabs = []*terminalTab{tb}
	a.activeTab = 0

	tt := ui.NewTester(a.view, 1100, 700)
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if tb.mode() != modeTerminal {
		t.Fatalf("mode = %v", tb.mode())
	}
}

func TestPercentParsing(t *testing.T) {
	cases := map[string]int{
		"/dev/sda1        40G   31G  7.4G  83% /":    83,
		"tmpfs           1.6G  1.5G  100M  94% /run": 94,
		"/dev/sdb1 100%":  100,
		"no percent here": -1,
		"":                -1,
	}
	for line, want := range cases {
		if got := lastPercent(line); got != want {
			t.Fatalf("lastPercent(%q) = %d, want %d", line, got, want)
		}
	}

	if got := memPercent("Mem:          15884        4210        9876"); got != 26 {
		t.Fatalf("memPercent = %d, want 26", got)
	}
	if got := memPercent("Mem:"); got != -1 {
		t.Fatalf("memPercent short line = %d", got)
	}
	if got := memPercent("Mem: 0 0 0"); got != -1 {
		t.Fatalf("memPercent zero total = %d", got)
	}
}

func TestMonitorColor(t *testing.T) {
	theme := ui.LightTheme()
	warn := monitorColor("/dev/sda1 40G 33G 7G 85% /", theme)
	if warn != theme.Warning {
		t.Fatalf("85%% should be amber")
	}
	bad := monitorColor("/dev/sda1 40G 39G 1G 95% /", theme)
	if bad != theme.Danger {
		t.Fatalf("95%% should be red")
	}
	if got := monitorColor("Mem: 1000 950 50", theme); got != theme.Danger {
		t.Fatalf("95%% memory should be red")
	}
	if got := monitorColor("## disk", theme); got != theme.TextMuted {
		t.Fatalf("headers should be muted")
	}
	if got := monitorColor("  PID COMMAND %CPU", theme); got != theme.Text {
		t.Fatalf("plain lines keep the text color")
	}
}

func TestDenseControlsAreCompact(t *testing.T) {
	a := readyApp(demoHosts...)
	a.tabs = []*terminalTab{{id: 1, host: "demo", forwardPane: newForwardPane()}}
	a.activeTab = 0
	tt := ui.NewTester(a.view, 1200, 800)

	// The tab strip and the terminal toolbar.
	for _, want := range []string{"锁定", "AI 活动", "终端", "文件", "转发", "详情"} {
		r, ok := tt.Find(want)
		if !ok {
			t.Fatalf("missing %q in %v", want, tt.Texts())
		}
		if r.H > controlH+2 {
			t.Fatalf("%q is %.0f tall, want <= %.0f", want, r.H, controlH+2)
		}
	}
}

// TestEscapeReachesTheTerminal locks in the fix for vi/vim: a window shortcut
// for Esc claims the key before the focus handler sees it, so Esc must not be
// bound while the terminal itself is on screen.
func TestEscapeReachesTheTerminal(t *testing.T) {
	hits := 0
	a := readyApp()
	a.tabs = []*terminalTab{{id: 1, host: "demo", forwardPane: newForwardPane()}}
	a.activeTab = 0

	tt := ui.NewTester(func(c *ui.Context) {
		a.shortcuts(c)
		ui.Box(c).Focusable().AutoFocus().HandleInput(func(ev ui.InputEvent) bool {
			if ev.Kind == ui.InputKeyDown && ev.Key == ui.KeyEscape {
				hits++
				return true
			}
			return false
		})
	}, 600, 400)

	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if hits != 1 {
		t.Fatalf("the focused terminal got Esc %d times, want 1", hits)
	}

	// A pane on screen still uses Esc to go back, and keeps it from the
	// terminal behind it.
	a.tabs[0].forwardPane.open = true
	tt.Frame()
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if hits != 1 {
		t.Fatalf("Esc reached the hidden terminal while a pane was open (%d)", hits)
	}
	if a.tabs[0].mode() != modeTerminal {
		t.Fatalf("Esc did not close the pane: mode = %v", a.tabs[0].mode())
	}
}

func TestLocalShellsOnStartPage(t *testing.T) {
	a := readyApp()
	a.localShells = []localshell.Shell{
		{Key: "cmd", Label: "命令提示符 (cmd)"},
		{Key: "wsl:Ubuntu", Label: "WSL · Ubuntu"},
	}
	tt := ui.NewTester(a.view, 1000, 700)
	for _, want := range []string{"本地终端", "命令提示符 (cmd)", "WSL · Ubuntu"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %v", want, tt.Texts())
		}
	}
}

func TestLocalTabHidesRemotePanes(t *testing.T) {
	a := readyApp()
	sh := localshell.Shell{Key: "cmd", Label: "命令提示符 (cmd)"}
	tb := &terminalTab{
		id: 1, host: sh.Label, label: sh.Label, status: tabLive,
		forwardPane: newForwardPane(), localShell: &sh,
	}
	a.tabs = []*terminalTab{tb}
	a.activeTab = 0

	tt := ui.NewTester(a.view, 1100, 700)
	for _, gone := range []string{"文件", "转发", "详情"} {
		if tt.HasText(gone) {
			t.Fatalf("local tab shows the remote pane %q: %v", gone, tt.Texts())
		}
	}
	if !tt.HasText("终端") {
		t.Fatalf("local tab lost its terminal mode: %v", tt.Texts())
	}
}

func TestMoveTabReordersAndKeepsActive(t *testing.T) {
	a := readyApp()
	a.tabs = []*terminalTab{{id: 1}, {id: 2}, {id: 3}, {id: 4}}
	a.activeTab = 2 // id 3

	a.moveTab(1, 3, false) // one before three
	if got := tabIDs(a.tabs); strings.Join(got, ",") != "2,1,3,4" {
		t.Fatalf("after move: %v", got)
	}
	if a.activeTab != 2 || a.tabs[a.activeTab].id != 3 {
		t.Fatalf("active tab did not follow id 3: %d", a.activeTab)
	}

	a.moveTab(1, 4, true) // one after four
	if got := tabIDs(a.tabs); strings.Join(got, ",") != "2,3,4,1" {
		t.Fatalf("after move to end of target: %v", got)
	}
	if a.tabs[a.activeTab].id != 3 {
		t.Fatalf("active id = %d", a.tabs[a.activeTab].id)
	}

	a.moveTabToEnd(2)
	if got := tabIDs(a.tabs); strings.Join(got, ",") != "3,4,1,2" {
		t.Fatalf("moveTabToEnd: %v", got)
	}

	// Self-drops and unknown ids are no-ops.
	before := strings.Join(tabIDs(a.tabs), ",")
	a.moveTab(3, 3, false)
	a.moveTab(99, 4, false)
	if got := strings.Join(tabIDs(a.tabs), ","); got != before {
		t.Fatalf("no-op moves changed the order: %s -> %s", before, got)
	}
}

func tabIDs(tabs []*terminalTab) []string {
	out := make([]string, len(tabs))
	for i, tb := range tabs {
		out[i] = strconv.Itoa(tb.id)
	}
	return out
}

// dragPointer presses at from, moves in a few steps and releases at to.
func dragPointer(tt *ui.Tester, fromX, fromY, toX, toY float32) {
	tt.Press(fromX, fromY)
	for k := 1; k <= 4; k++ {
		f := float32(k) / 4
		tt.Move(fromX+(toX-fromX)*f, fromY+(toY-fromY)*f)
	}
	tt.Release(toX, toY)
	tt.Frame()
}

func TestTabsDragToReorder(t *testing.T) {
	a := readyApp()
	a.tabs = []*terminalTab{
		{id: 1, host: "one", label: "one", forwardPane: newForwardPane()},
		{id: 2, host: "two", label: "two", forwardPane: newForwardPane()},
		{id: 3, host: "three", label: "three", forwardPane: newForwardPane()},
	}
	a.activeTab = 1 // "two" stays the active tab across the reorder
	tt := ui.NewTester(a.view, 1000, 700)

	one, ok := tt.Find("one")
	if !ok {
		t.Fatalf("missing tab one: %v", tt.Texts())
	}
	three, ok := tt.Find("three")
	if !ok {
		t.Fatalf("missing tab three: %v", tt.Texts())
	}
	// Drop one on the right half of three: it lands after it.
	dragPointer(tt, one.X+one.W/2, one.Y+one.H/2, three.X+three.W*0.8, three.Y+three.H/2)
	if got := strings.Join(tabIDs(a.tabs), ","); got != "2,3,1" {
		t.Fatalf("drag order = %s", got)
	}
	if a.tabs[a.activeTab].id != 2 {
		t.Fatalf("active tab changed to %d", a.tabs[a.activeTab].id)
	}

	// Dropping on the new-tab button moves a tab to the end.
	two, ok := tt.Find("two")
	if !ok {
		t.Fatalf("missing tab two: %v", tt.Texts())
	}
	newTab, ok := tt.Find("新建标签")
	if !ok {
		t.Fatal("missing the new-tab button")
	}
	dragPointer(tt, two.X+two.W/2, two.Y+two.H/2, newTab.X+newTab.W/2, newTab.Y+newTab.H/2)
	if got := strings.Join(tabIDs(a.tabs), ","); got != "3,1,2" {
		t.Fatalf("drop on + order = %s", got)
	}
}

func TestStripHasSettingsAndThemeButtons(t *testing.T) {
	a := readyApp(demoHosts...)
	tt := ui.NewTester(a.view, 1000, 700)
	if !tt.HasText("设置") {
		t.Fatalf("the strip has no settings button: %v", tt.Texts())
	}
	if !tt.HasText("切换到深色主题") && !tt.HasText("切换到浅色主题") {
		t.Fatalf("the strip has no quick theme button: %v", tt.Texts())
	}
	// Headless openSettings is a no-op, but the hit must land on the button.
	if err := tt.Click("设置"); err != nil {
		t.Fatal(err)
	}
}
