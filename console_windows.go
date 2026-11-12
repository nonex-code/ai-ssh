//go:build windows

package main

import (
	"os"

	"golang.org/x/sys/windows"
)

var procAttachConsole = windows.NewLazySystemDLL("kernel32.dll").NewProc("AttachConsole")

// attachParentProcess is ATTACH_PARENT_PROCESS, (DWORD)-1.
const attachParentProcess = ^uint32(0)

// attachConsole gives a windowsgui binary a console when its parent has one.
// The binary is built without a console so double-clicking it never flashes a
// black window; CLI and MCP invocations still need stdout/stderr, and this is
// what connects them to the terminal (or keeps the parent's pipes and files,
// which is how an AI host captures JSON).
func attachConsole() {
	outType, _ := windows.GetFileType(windows.Stdout)
	inType, _ := windows.GetFileType(windows.Stdin)

	if r, _, _ := procAttachConsole.Call(uintptr(attachParentProcess)); r == 0 {
		// No parent console (Explorer, service, AI host with pipes): keep
		// whatever handles were inherited.
		return
	}
	// A redirected stdout or stderr is already what the caller wants; only a
	// real console handle is replaced by CONOUT$, which is the one that works
	// once the process has been attached.
	if outType == windows.FILE_TYPE_CHAR {
		if f, err := os.OpenFile("CONOUT$", os.O_WRONLY, 0); err == nil {
			os.Stdout = f
			os.Stderr = f
		}
	}
	if inType == windows.FILE_TYPE_CHAR {
		if f, err := os.OpenFile("CONIN$", os.O_RDONLY, 0); err == nil {
			os.Stdin = f
		}
	}
	// JSON with Chinese text and emoji must not arrive as mojibake.
	_ = windows.SetConsoleOutputCP(65001)
}
