//go:build linux

package gui

import "os"

// guiAvailable checks the display first. mygo's GTK backend can still fail to
// initialise (missing libraries); runAutoGUI reports that and falls back.
func guiAvailable() bool {
	return os.Getenv("DISPLAY") != "" || os.Getenv("WAYLAND_DISPLAY") != ""
}
