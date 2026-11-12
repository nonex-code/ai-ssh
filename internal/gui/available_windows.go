//go:build windows

package gui

// guiAvailable is optimistic on Windows: the window may still fail in a
// service session, in which case RunAuto falls back to a terminal prompt.
func guiAvailable() bool { return true }
