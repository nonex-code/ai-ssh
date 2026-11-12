package gui

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/nonex-code/ai-ssh/internal/localshell"
	"github.com/nonex-code/ai-ssh/internal/proto"
)

func TestLanguageSettingTranslatesTheWindow(t *testing.T) {
	a := readyApp()
	a.settingsTab = "language"
	tt := ui.NewTester(a.settingsView, 840, 600)
	if !tt.HasText("界面语言") || !tt.HasText("中文") {
		t.Fatalf("language page is missing: %v", tt.Texts())
	}

	setLanguage("en")
	t.Cleanup(func() { setLanguage("zh") })
	tt.Frame()
	for _, want := range []string{"Interface language", "Language", "Appearance", "About"} {
		if !tt.HasText(want) {
			t.Fatalf("English settings is missing %q: %v", want, tt.Texts())
		}
	}
	if tt.HasText("界面语言") {
		t.Fatalf("Chinese text leaked into English settings: %v", tt.Texts())
	}
}

func TestLanguagePrefsRoundTrip(t *testing.T) {
	path := t.TempDir() + "/gui.json"
	st := windowState{Language: "en"}
	if err := saveWindowState(path, st); err != nil {
		t.Fatal(err)
	}
	if got := loadWindowState(path).themePrefs().Language; got != "en" {
		t.Fatalf("language = %q", got)
	}
	if got := (themePrefs{Language: "bogus"}).normalized().Language; got != "system" {
		t.Fatalf("bogus language = %q", got)
	}
}

func containsHan(s string) bool {
	for _, r := range s {
		if r >= 0x4e00 && r <= 0x9fff {
			return true
		}
	}
	return false
}

func TestEnglishInterfaceHasNoChinese(t *testing.T) {
	a := readyApp(demoHosts...)
	setLanguage("en")
	t.Cleanup(func() { setLanguage("zh") })
	a.localShells = []localshell.Shell{{Key: "cmd", Label: "命令提示符 (cmd)"}}
	tt := ui.NewTester(a.view, 1100, 700)
	for _, text := range tt.Texts() {
		if containsHan(text) {
			t.Fatalf("Chinese leaked into the English start page: %q", text)
		}
	}

	for _, page := range []string{"appearance", "terminal", "security", "about"} {
		a.settingsTab = page
		tt.Frame()
		for _, text := range tt.Texts() {
			if containsHan(text) {
				t.Fatalf("page %s leaked Chinese: %q", page, text)
			}
		}
	}
}

func TestEnglishViewsHaveNoChinese(t *testing.T) {
	setLanguage("en")
	t.Cleanup(func() { setLanguage("zh") })
	servers := readyApp(
		proto.HostInfo{Alias: "prod", User: "deploy", Addr: "10.0.0.5:22", Auth: "key", Group: "Production"},
	)
	cases := []struct {
		name string
		app  *App
		view func(*App, *ui.Context)
		w, h int
	}{
		{"servers", servers, (*App).serversView, 760, 620},
		{"editor", editorApp(), (*App).editorView, 540, 620},
		{"files", filesShotApp(), (*App).view, 1200, 700},
		{"monitor", monitorShotApp(), (*App).view, 1100, 700},
		{"activity", activityApp(), (*App).activityView, 680, 480},
		{"first-key", trustShotApp(), (*App).hostKeyView, 520, 320},
		{"changed-key", changedKeyShotApp(), (*App).hostKeyView, 600, 520},
	}
	setLanguage("en")
	t.Cleanup(func() { setLanguage("zh") })
	for _, c := range cases {
		tt := ui.NewTester(func(ctx *ui.Context) { c.view(c.app, ctx) }, c.w, c.h)
		for _, text := range tt.Texts() {
			if containsHan(text) {
				t.Fatalf("%s leaked Chinese: %q", c.name, text)
			}
		}
	}
}

func TestLanguageSwitchAppliesWithoutRestart(t *testing.T) {
	a := readyApp()
	a.prefs.Language = "en"
	a.savePrefs()
	t.Cleanup(func() { setLanguage("zh") })
	if currentLanguage() != "en" {
		t.Fatalf("language = %q", currentLanguage())
	}
	tt := ui.NewTester(a.view, 1000, 700)
	if !tt.HasText("Home") || tt.HasText("主页") {
		t.Fatalf("main window did not switch immediately: %v", tt.Texts())
	}

	a.prefs.Language = "zh"
	a.savePrefs()
	tt.Frame()
	if !tt.HasText("主页") {
		t.Fatalf("main window did not switch back: %v", tt.Texts())
	}
}

func TestLanguageControlSwitchesImmediately(t *testing.T) {
	a := readyApp()
	a.settingsTab = "language"
	a.prefs.Language = "zh"
	tt := ui.NewTester(a.settingsView, 840, 600)
	if err := tt.Click("English"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	t.Cleanup(func() { setLanguage("zh") })
	if currentLanguage() != "en" {
		t.Fatalf("language = %q, prefs = %q", currentLanguage(), a.prefs.Language)
	}
	if !tt.HasText("Interface language") {
		t.Fatalf("settings did not switch: %v", tt.Texts())
	}
}
