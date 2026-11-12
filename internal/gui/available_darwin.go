//go:build darwin

package gui

// guiAvailable is optimistic on macOS: a window from a launchd or ssh session
// can still fail to activate, and RunAuto falls back when it does.
func guiAvailable() bool { return true }
