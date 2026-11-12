package gui

import (
	"sync"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"
)

// themePrefs is what the user chose in the appearance sheet. It is stored next
// to the window geometry in gui.json; the zero value means "system default".
type themePrefs struct {
	Appearance string `json:"appearance,omitempty"` // system | light | dark
	Terminal   string `json:"terminal,omitempty"`   // window | catppuccin | tokyonight | onedark | solarized
	Language   string `json:"language,omitempty"`   // system | zh | en
}

// Appearance modes, in the order the segmented control shows them.
var appearanceKeys = []string{"system", "light", "dark"}
var appearanceLabels = []string{"跟随系统", "白", "黑"}

// Terminal palette presets: a light and a dark Ghostty theme. "window" passes
// no theme at all, so the terminal follows the window's appearance.
type terminalPreset struct {
	Key, Label  string
	Light, Dark string
}

var terminalPresets = []terminalPreset{
	{"window", "跟窗口（黑白）", "", ""},
	{"catppuccin", "Catppuccin", "Catppuccin Latte", "Catppuccin Mocha"},
	{"tokyonight", "Tokyo Night", "TokyoNight Day", "TokyoNight"},
	{"onedark", "One Dark", "Atom One Light", "Atom One Dark"},
	{"solarized", "Solarized", "iTerm2 Solarized Light", "iTerm2 Solarized Dark"},
}

func (p themePrefs) normalized() themePrefs {
	switch p.Appearance {
	case "light", "dark", "system":
	default:
		p.Appearance = "system"
	}
	if _, ok := terminalPresetFor(p.Terminal); !ok {
		p.Terminal = "window"
	}
	switch p.Language {
	case "zh", "en", "system":
	default:
		p.Language = "system"
	}
	return p
}

// source maps the stored appearance to mygo's theme source.
func (p themePrefs) source() string {
	switch p.Appearance {
	case "light":
		return "light"
	case "dark":
		return "dark"
	default:
		return "system"
	}
}

func terminalPresetFor(key string) (terminalPreset, bool) {
	for _, t := range terminalPresets {
		if t.Key == key {
			return t, true
		}
	}
	return terminalPresets[0], false
}

func indexOf(list []string, want string) int {
	for i, v := range list {
		if v == want {
			return i
		}
	}
	return 0
}

// appTheme turns mygo's light/dark base into the app's own look: a tinted page,
// white cards, hairline borders, 10pt corners and Inter. Everything the
// widgets draw comes from here, so both the main window and the dialogs use it.
func appTheme(base *ui.Theme) *ui.Theme {

	// Plain black and white: the accent is the text color, so primary
	// buttons and focus rings are ink, not a brand color.
	t := *base
	white := ui.Color{R: 255, G: 255, B: 255, A: 255}
	black := ui.Color{A: 255}

	if base.Dark {
		t.Background = ui.Hex("#0d0d0d")
		t.Surface = ui.Hex("#171717")
		t.SurfaceHover = ui.Hex("#1f1f1f")
		t.SurfacePressed = ui.Hex("#282828")
		t.Border = ui.Hex("#2c2c2c")
		t.Text = ui.Hex("#f2f2f2")
		t.TextMuted = ui.Hex("#a0a0a0")
		t.Accent = t.Text
		t.AccentHover = t.Text.Mix(black, 0.15)
		t.AccentPressed = t.Text.Mix(black, 0.30)
		t.AccentText = ui.Hex("#101010")
		t.Selection = t.Text.Alpha(0.22)
		t.Focus = t.Text.Alpha(0.40)
		t.Scrollbar = ui.RGBA(255, 255, 255, 0.24)
		t.Inverse = t.Text
		t.InverseText = t.Background
	} else {
		t.Background = ui.Hex("#f4f4f4")
		t.Surface = ui.Hex("#ffffff")
		t.SurfaceHover = ui.Hex("#efefef")
		t.SurfacePressed = ui.Hex("#e4e4e4")
		t.Border = ui.Hex("#e0e0e0")
		t.Text = ui.Hex("#111111")
		t.TextMuted = ui.Hex("#6b6b6b")
		t.Accent = t.Text
		t.AccentHover = t.Text.Mix(white, 0.18)
		t.AccentPressed = t.Text.Mix(white, 0.32)
		t.AccentText = white
		t.Selection = t.Text.Alpha(0.16)
		t.Focus = t.Text.Alpha(0.38)
		t.Scrollbar = ui.RGBA(0, 0, 0, 0.22)
		t.Inverse = ui.Hex("#1b1b1b")
		t.InverseText = ui.Hex("#f7f7f7")
	}

	t.Radius = 10
	t.Spacing = 4
	t.FontSize = 13
	t.Font = uiFont
	t.ScrollbarWidth = 8
	t.FocusRingWidth = 2
	t.FocusRingOffset = 1
	return &t
}

// shadow is the soft lift under cards and sheets.
func shadow(t *ui.Theme) ui.Color {
	if t.Dark {
		return ui.RGBA(0, 0, 0, 0.45)
	}
	return ui.RGBA(16, 18, 27, 0.07)
}

// ensureFonts is called from every window's root view: the window may be
// created before the main view ran (a dialog, say).
func (a *App) applyTheme(c *ui.Context) *ui.Theme {
	ensureFonts()
	c.SetTheme(appTheme(c.Theme()))
	return c.Theme()
}

// terminalThemes resolves the chosen palette. "window" is our own black and
// white pair: the terminal background is the window's, with an ANSI set tuned
// to stay legible on it, instead of the stock VS Code colors (whose black
// vanishes on a dark page and whose white vanishes on a light one).
func terminalThemes(key string) (light, dark *terminal.Theme) {
	preset, _ := terminalPresetFor(key)
	if preset.Light == "" && preset.Dark == "" {
		return plainTerminalThemes()
	}
	return ghosttyTheme(preset.Light), ghosttyTheme(preset.Dark)
}

// plainTerminalThemes is the default: ink on the window's page, with the
// GitHub palettes' hues, which are readable on both.
func plainTerminalThemes() (*terminal.Theme, *terminal.Theme) {
	light := &terminal.Theme{
		Foreground:    ui.Hex("#1b1b1b"),
		Background:    ui.Hex("#f4f4f4"),
		Cursor:        ui.Hex("#111111"),
		CursorText:    ui.Hex("#f4f4f4"),
		Selection:     ui.RGBA(17, 17, 17, 0.16),
		SelectionText: ui.Hex("#111111"),
		Palette: [16]ui.Color{
			ui.Hex("#24292f"), ui.Hex("#cf222e"), ui.Hex("#116329"), ui.Hex("#7d4e00"),
			ui.Hex("#0969da"), ui.Hex("#8250df"), ui.Hex("#1b7c83"), ui.Hex("#6e7781"),
			ui.Hex("#57606a"), ui.Hex("#a40e26"), ui.Hex("#1a7f37"), ui.Hex("#8a5a00"),
			ui.Hex("#218bff"), ui.Hex("#a475f9"), ui.Hex("#3192aa"), ui.Hex("#8c959f"),
		},
	}
	dark := &terminal.Theme{
		Foreground:    ui.Hex("#e6e6e6"),
		Background:    ui.Hex("#0d0d0d"),
		Cursor:        ui.Hex("#f2f2f2"),
		CursorText:    ui.Hex("#0d0d0d"),
		Selection:     ui.RGBA(242, 242, 242, 0.20),
		SelectionText: ui.Hex("#f2f2f2"),
		Palette: [16]ui.Color{
			ui.Hex("#4b515a"), ui.Hex("#ff7b72"), ui.Hex("#3fb950"), ui.Hex("#d29922"),
			ui.Hex("#58a6ff"), ui.Hex("#bc8cff"), ui.Hex("#39c5cf"), ui.Hex("#b1bac4"),
			ui.Hex("#6e7681"), ui.Hex("#ffa198"), ui.Hex("#56d364"), ui.Hex("#e3b341"),
			ui.Hex("#79c0ff"), ui.Hex("#d2a8ff"), ui.Hex("#56d4dd"), ui.Hex("#f0f6fc"),
		},
	}
	return light, dark
}

var ghosttyThemes sync.Map // name -> *terminal.Theme

func ghosttyTheme(name string) *terminal.Theme {
	if name == "" {
		return nil
	}
	if v, ok := ghosttyThemes.Load(name); ok {
		return v.(*terminal.Theme)
	}
	th, err := terminal.GhosttyTheme(name)
	if err != nil || th == nil {
		return nil
	}
	ghosttyThemes.Store(name, th)
	return th
}
