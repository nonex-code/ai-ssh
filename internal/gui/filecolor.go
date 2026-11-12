package gui

import (
	"path/filepath"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/nonex-code/ai-ssh/internal/agent"
)

// File kinds, as a file manager colors them: what it is comes before what it
// is called. The colors are tuned for the black and white themes, so they stay
// legible on both pages.
type fileKind int

const (
	fileNormal fileKind = iota
	fileDir
	fileLink
	fileExec
	fileArchive
	fileMedia
	fileSpecial
	fileHidden
)

var archiveExts = map[string]bool{
	".zip": true, ".tar": true, ".gz": true, ".tgz": true, ".bz2": true,
	".xz": true, ".zst": true, ".7z": true, ".rar": true, ".jar": true,
	".deb": true, ".rpm": true, ".apk": true, ".lz4": true,
}

var mediaExts = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true,
	".svg": true, ".ico": true, ".bmp": true, ".tiff": true,
	".mp4": true, ".mkv": true, ".webm": true, ".avi": true, ".mov": true,
	".mp3": true, ".flac": true, ".wav": true, ".ogg": true, ".m4a": true,
}

// kindOf classifies an entry from its name, mode and link flag.
func kindOf(e agent.FileEntry) fileKind {
	mode := e.Mode
	if mode == "" {
		switch {
		case e.Link:
			return fileLink
		case e.Dir:
			return fileDir
		}
		return fileNormal
	}
	switch mode[0] {
	case 'L', 'l':
		return fileLink
	case 'p', 'S', 'c', 'D', '?':
		return fileSpecial
	case 'd':
		return fileDir
	}
	if strings.Contains(mode[1:], "x") {
		return fileExec
	}
	name := strings.ToLower(e.Name)
	ext := filepath.Ext(name)
	if archiveExts[ext] {
		return fileArchive
	}
	if mediaExts[ext] {
		return fileMedia
	}
	if strings.HasPrefix(name, ".") {
		return fileHidden
	}
	return fileNormal
}

// fileColor is the name's color in the file pane.
func fileColor(e agent.FileEntry, t *ui.Theme) ui.Color {
	if t.Dark {
		switch kindOf(e) {
		case fileDir:
			return ui.Hex("#58a6ff")
		case fileLink:
			return ui.Hex("#39c5cf")
		case fileExec:
			return ui.Hex("#3fb950")
		case fileArchive:
			return ui.Hex("#d29922")
		case fileMedia:
			return ui.Hex("#bc8cff")
		case fileSpecial:
			return ui.Hex("#ff7b72")
		case fileHidden:
			return t.TextMuted
		}
		return t.Text
	}
	switch kindOf(e) {
	case fileDir:
		return ui.Hex("#0969da")
	case fileLink:
		return ui.Hex("#1b7c83")
	case fileExec:
		return ui.Hex("#1a7f37")
	case fileArchive:
		return ui.Hex("#9a6700")
	case fileMedia:
		return ui.Hex("#8250df")
	case fileSpecial:
		return ui.Hex("#cf222e")
	case fileHidden:
		return t.TextMuted
	}
	return t.Text
}

// modeColor colors the symbolic permissions: executable bits in green,
// anything that lets others write or setuid/setgid in the warning color.
func modeColor(e agent.FileEntry, t *ui.Theme) ui.Color {
	mode := e.Mode
	if len(mode) < 10 {
		return t.TextMuted
	}
	perm := mode[1:]
	if strings.ContainsAny(perm, "sS") || perm[7] == 'w' || mode[0] == 'd' && perm[7] == 'w' {
		return t.Warning
	}
	if mode[0] != 'd' && strings.Contains(perm, "x") {
		return fileColor(e, t)
	}
	return t.TextMuted
}

// modeText is the permissions without the type character, which the mark and
// the kind already show.
func modeText(e agent.FileEntry) string {
	if len(e.Mode) >= 10 {
		return e.Mode[1:]
	}
	return e.Mode
}
