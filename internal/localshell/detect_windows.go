//go:build windows

package localshell

import (
	"context"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode/utf16"
)

// Default is the system's login shell: PowerShell when installed, else cmd.
// An empty Command lets the terminal make that choice itself.
func Default() Shell {
	return Shell{Key: "default", Label: "系统默认 shell"}
}

// Detect lists the shells installed on this machine, most useful first. It
// never fails: a machine with only cmd still gets the default entry.
func Detect() []Shell {
	out := []Shell{Default()}
	seen := map[string]bool{}

	add := func(key, label string, command ...string) {
		if len(command) == 0 || seen[key] {
			return
		}
		path, err := exec.LookPath(command[0])
		if err != nil {
			return
		}
		seen[key] = true
		command[0] = path
		out = append(out, Shell{Key: key, Label: label, Command: command})
	}

	add("pwsh", "PowerShell 7", "pwsh.exe")
	add("powershell", "Windows PowerShell", "powershell.exe")
	comspec := strings.TrimSpace(os.Getenv("COMSPEC"))
	if comspec == "" {
		comspec = "cmd.exe"
	}
	add("cmd", "命令提示符 (cmd)", comspec)
	if !seen["cmd"] {
		add("cmd", "命令提示符 (cmd)", "cmd.exe")
	}

	// Git Bash and other MSYS builds. LookPath("bash.exe") may find WSL's
	// System32 launcher, which is offered as WSL instead.
	for _, p := range []string{
		`C:\Program Files\Git\bin\bash.exe`,
		`C:\Program Files (x86)\Git\bin\bash.exe`,
		`C:\msys64\usr\bin\bash.exe`,
	} {
		if fileExists(p) {
			seen["gitbash"] = true
			out = append(out, Shell{Key: "gitbash", Label: "Git Bash", Command: []string{p}})
			break
		}
	}
	if !seen["gitbash"] {
		if p, err := exec.LookPath("bash.exe"); err == nil &&
			!strings.Contains(strings.ToLower(filepath.ToSlash(p)), "/windows/system32/") {
			seen["bash"] = true
			out = append(out, Shell{Key: "bash", Label: "Bash", Command: []string{p}})
		}
	}

	if wsl, err := exec.LookPath("wsl.exe"); err == nil {
		distros := wslDistros(wsl)
		for _, d := range distros {
			out = append(out, Shell{
				Key:     "wsl:" + d,
				Label:   "WSL · " + d,
				Command: []string{wsl, "-d", d},
			})
		}
		label := "WSL（默认发行版）"
		if len(distros) == 0 {
			label = "WSL"
		}
		seen["wsl"] = true
		out = append(out, Shell{Key: "wsl", Label: label, Command: []string{wsl}})
	}

	out = append(out, extra()...)
	return dedupe(out)
}

// wslDistros asks WSL for its installed distributions. Older builds answer in
// UTF-16LE; WSL_UTF8 asks newer ones for UTF-8. A slow or missing WSL is not an
// error: the default WSL entry still works.
func wslDistros(wsl string) []string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, wsl, "-l", "-q")
	cmd.Env = append(os.Environ(), "WSL_UTF8=1")
	// A console would flash from this windowsgui process.
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
	raw, err := cmd.Output()
	if err != nil {
		return nil
	}
	return parseWSLList(raw)
}

// parseWSLList parses `wsl -l -q` output, whichever encoding it used.
func parseWSLList(raw []byte) []string {
	// Some builds end the answer with NUL padding.
	text := strings.ReplaceAll(decodeWSL(raw), "\x00", "")
	var out []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(line, "\ufeff"))
		if line == "" || strings.HasPrefix(line, "Windows Subsystem") {
			continue
		}
		out = append(out, line)
	}
	return out
}

// decodeWSL decodes UTF-16LE output, with or without a BOM, and leaves UTF-8
// alone. WSL.exe answers in the console's output encoding, usually UTF-16LE.
func decodeWSL(raw []byte) string {
	if len(raw) >= 2 && raw[0] == 0xff && raw[1] == 0xfe {
		return decodeUTF16LE(raw[2:])
	}
	if looksUTF16LE(raw) {
		return decodeUTF16LE(raw)
	}
	return string(raw)
}

// looksUTF16LE reports whether the high byte of most units is zero, which is
// what UTF-16LE text full of ASCII looks like.
func looksUTF16LE(raw []byte) bool {
	if len(raw) < 4 || len(raw)%2 != 0 {
		return false
	}
	units, highZero := len(raw)/2, 0
	for i := 1; i < len(raw); i += 2 {
		if raw[i] == 0 {
			highZero++
		}
	}
	return highZero*2 > units
}

func decodeUTF16LE(raw []byte) string {
	units := make([]uint16, 0, len(raw)/2)
	for i := 0; i+1 < len(raw); i += 2 {
		units = append(units, binary.LittleEndian.Uint16(raw[i:]))
	}
	return string(utf16.Decode(units))
}

// dedupe drops repeats that differ only in case (cmd.exe vs CMD.exe) and keeps
// the order.
func dedupe(in []Shell) []Shell {
	out := in[:0]
	seen := map[string]bool{}
	for _, sh := range in {
		k := strings.ToLower(sh.Key)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, sh)
	}
	return out
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}
