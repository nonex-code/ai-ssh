package gui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestThemePrefsNormalize(t *testing.T) {
	p := themePrefs{}.normalized()
	if p.Appearance != "system" || p.Terminal != "window" {
		t.Fatalf("defaults = %+v", p)
	}

	p = themePrefs{Appearance: "dark", Terminal: "tokyonight"}.normalized()
	if p.Appearance != "dark" || p.Terminal != "tokyonight" {
		t.Fatalf("kept = %+v", p)
	}

	p = themePrefs{Appearance: "neon", Terminal: "comic"}.normalized()
	if p.Appearance != "system" || p.Terminal != "window" {
		t.Fatalf("unknown values must fall back, got %+v", p)
	}
}

func TestAppTheme(t *testing.T) {
	light := appTheme(ui.LightTheme())
	if light.Dark {
		t.Fatal("a light base must stay light")
	}
	if light.Radius != 10 || light.Font != uiFont || light.FontSize != 13 {
		t.Fatalf("metrics = radius %v font %q size %v", light.Radius, light.Font, light.FontSize)
	}
	if light.Background == ui.LightTheme().Background {
		t.Fatal("the app should not use mygo's default page color")
	}
	// The accent is ink: black in a light window, white in a dark one.
	if light.Accent != light.Text {
		t.Fatalf("light accent = %v, want text %v", light.Accent, light.Text)
	}

	dark := appTheme(ui.DarkTheme())
	if !dark.Dark {
		t.Fatal("a dark base must stay dark")
	}
	if dark.Accent != dark.Text {
		t.Fatalf("dark accent = %v, want text %v", dark.Accent, dark.Text)
	}
	if dark.Background != ui.Hex("#0d0d0d") {
		t.Fatalf("dark page = %v", dark.Background)
	}
	// Hover states must move away from the page, not into it.
	if dark.Surface == dark.Background || light.Surface == light.Background {
		t.Fatal("surface and page must differ")
	}
}

func TestTerminalThemesResolve(t *testing.T) {
	light, dark := terminalThemes("window")
	if light == nil || dark == nil {
		t.Fatal("the window preset must bring its own pair")
	}
	// The page and the terminal meet without a seam, and neither black nor
	// white is invisible on its background.
	if light.Background != ui.Hex("#f4f4f4") || dark.Background != ui.Hex("#0d0d0d") {
		t.Fatalf("backgrounds = %v / %v", light.Background, dark.Background)
	}
	for _, p := range light.Palette {
		if p == light.Background {
			t.Fatal("a light palette color equals the background")
		}
	}
	for _, p := range dark.Palette {
		if p == dark.Background {
			t.Fatal("a dark palette color equals the background")
		}
	}
	for _, key := range []string{"catppuccin", "tokyonight", "onedark", "solarized"} {
		light, dark := terminalThemes(key)
		if light == nil || dark == nil {
			t.Fatalf("%s did not resolve a light/dark pair", key)
		}
		if light.Background == dark.Background {
			t.Fatalf("%s: light and dark palettes are identical", key)
		}
	}
}

func TestWindowStateKeepsThemePrefs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gui.json")
	st := windowState{Width: 1400, Height: 900, X: 10, Y: 20}
	st.setThemePrefs(themePrefs{Appearance: "dark", Terminal: "solarized"})
	if err := saveWindowState(path, st); err != nil {
		t.Fatal(err)
	}

	got := loadWindowState(path)
	if p := got.themePrefs(); p.Appearance != "dark" || p.Terminal != "solarized" {
		t.Fatalf("theme prefs = %+v", p)
	}
	if got.Width != 1400 || got.Height != 900 {
		t.Fatalf("geometry lost: %+v", got)
	}

	// An old gui.json without the new fields must keep working.
	old := filepath.Join(t.TempDir(), "old.json")
	if err := os.WriteFile(old, []byte(`{"width":1000,"height":700}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if p := loadWindowState(old).themePrefs(); p.Appearance != "system" || p.Terminal != "window" {
		t.Fatalf("old state prefs = %+v", p)
	}
}

func TestBundledFontsRegister(t *testing.T) {
	ensureFonts()
	var gotInter, gotMono bool
	for _, f := range ui.FontFamilies() {
		switch {
		case strings.EqualFold(f.Name, "Inter") && f.Registered:
			gotInter = true
		case strings.EqualFold(f.Name, "JetBrains Mono") && f.Registered:
			gotMono = true
		}
	}
	if !gotInter || !gotMono {
		t.Fatalf("bundled fonts missing (Inter=%v, JetBrains Mono=%v)", gotInter, gotMono)
	}
}

func TestSettingsViewAndPrefs(t *testing.T) {
	a := readyApp(demoHosts...)
	tt := ui.NewTester(a.settingsView, 840, 600)
	// The page list and the default appearance page.
	for _, want := range []string{"外观", "终端", "安全", "关于", "跟随系统", "白", "黑"} {
		if !tt.HasText(want) {
			t.Fatalf("settings sheet is missing %q: %v", want, tt.Texts())
		}
	}

	a.settingsTab = "terminal"
	tt.Frame()
	for _, want := range []string{"终端配色", "跟窗口（黑白）", "字体"} {
		if !tt.HasText(want) {
			t.Fatalf("terminal settings page is missing %q: %v", want, tt.Texts())
		}
	}

	a.settingsTab = "security"
	tt.Frame()
	for _, want := range []string{"主密码", "验证当前密码后", "修改主密码"} {
		if !tt.HasText(want) {
			t.Fatalf("security page is missing %q: %v", want, tt.Texts())
		}
	}

	a.settingsTab = "about"
	tt.Frame()
	for _, want := range []string{"给 AI 用的 SSH，凭证不出本机", "开源项目", "Ghostty", "mygo", "检查更新"} {
		if !tt.HasText(want) {
			t.Fatalf("about page is missing %q: %v", want, tt.Texts())
		}
	}

	// Changing a preference headless must not panic and must normalise.
	a.setAppearance("dark")
	a.setTerminalTheme("tokyonight")
	if p := a.prefs.normalized(); p.Appearance != "dark" || p.Terminal != "tokyonight" {
		t.Fatalf("prefs = %+v", p)
	}
}

func TestThemeButtonToggles(t *testing.T) {
	a := readyApp(demoHosts...)
	a.prefs.Appearance = "light"
	tt := ui.NewTester(a.view, 1000, 700)
	if err := tt.Click("切换到深色主题"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.prefs.Appearance != "dark" {
		t.Fatalf("appearance = %q", a.prefs.Appearance)
	}
	// The window follows the preference; the headless tester needs the dark
	// theme set explicitly to show the moon turning into a sun.
	tt.SetDark(true)
	tt.Frame()
	if err := tt.Click("切换到浅色主题"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.prefs.Appearance != "light" {
		t.Fatalf("appearance = %q", a.prefs.Appearance)
	}
}
