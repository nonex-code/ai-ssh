package gui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// recentConn is one entry of the home page's connection history.
type recentConn struct {
	Alias string    `json:"alias"`
	At    time.Time `json:"at"`
}

// windowState is what the app remembers about its window between runs. It holds
// no secrets, so it lives next to the vault in plain text.
type windowState struct {
	Width     int  `json:"width"`
	Height    int  `json:"height"`
	X         int  `json:"x"`
	Y         int  `json:"y"`
	Maximized bool `json:"maximized"`

	// Appearance choices. Empty means the default: follow the system, black
	// or white, terminal follows the window.
	Appearance    string `json:"appearance,omitempty"`
	TerminalTheme string `json:"terminal_theme,omitempty"`
	Language      string `json:"language,omitempty"`

	// Recent is the home page's connection history, newest first.
	Recent []recentConn `json:"recent,omitempty"`

	// Update preferences and the time of the last successful check. A nil
	// AutoCheckUpdate means the default: check on start.
	AutoCheckUpdate   *bool     `json:"auto_check_update,omitempty"`
	AutoInstallUpdate bool      `json:"auto_install_update,omitempty"`
	LastUpdateCheck   time.Time `json:"last_update_check,omitempty"`
}

func (st windowState) autoCheckUpdates() bool {
	return st.AutoCheckUpdate == nil || *st.AutoCheckUpdate
}

func (st windowState) themePrefs() themePrefs {
	return themePrefs{
		Appearance: st.Appearance,
		Terminal:   st.TerminalTheme,
		Language:   st.Language,
	}.normalized()
}

func (st *windowState) setThemePrefs(p themePrefs) {
	st.Appearance = p.Appearance
	st.TerminalTheme = p.Terminal
	st.Language = p.Language
}

const (
	defaultWidth  = 1200
	defaultHeight = 760
	minWidth      = 780
	minHeight     = 520
)

func statePath(vaultPath string) string {
	return filepath.Join(filepath.Dir(vaultPath), "gui.json")
}

func loadWindowState(path string) windowState {
	st := windowState{Width: defaultWidth, Height: defaultHeight}

	data, err := os.ReadFile(path)
	if err != nil {
		return st
	}
	var saved windowState
	if json.Unmarshal(data, &saved) != nil {
		return st
	}
	if saved.Width >= minWidth && saved.Width <= 8000 {
		st.Width = saved.Width
	}
	if saved.Height >= minHeight && saved.Height <= 8000 {
		st.Height = saved.Height
	}
	st.X, st.Y = saved.X, saved.Y
	st.Maximized = saved.Maximized
	st.Appearance = saved.Appearance
	st.TerminalTheme = saved.TerminalTheme
	st.Language = saved.Language
	st.Recent = saved.Recent
	st.AutoCheckUpdate = saved.AutoCheckUpdate
	st.AutoInstallUpdate = saved.AutoInstallUpdate
	st.LastUpdateCheck = saved.LastUpdateCheck
	return st
}

func saveWindowState(path string, st windowState) error {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
