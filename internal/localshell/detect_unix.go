//go:build !windows

package localshell

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Default is the login shell the terminal starts when no command is given:
// $SHELL, or /bin/zsh on macOS and /bin/sh elsewhere.
func Default() Shell {
	return Shell{Key: "default", Label: "登录 shell"}
}

// known is the order shells are offered in after $SHELL.
var known = []struct {
	key   string
	label string
	names []string
}{
	{"zsh", "Zsh", []string{"zsh"}},
	{"bash", "Bash", []string{"bash"}},
	{"fish", "Fish", []string{"fish"}},
	{"nu", "Nushell", []string{"nu"}},
	{"elvish", "Elvish", []string{"elvish"}},
	{"xonsh", "Xonsh", []string{"xonsh"}},
	{"dash", "dash", []string{"dash"}},
	{"ksh", "ksh", []string{"ksh"}},
	{"tcsh", "tcsh", []string{"tcsh"}},
	{"sh", "sh", []string{"sh"}},
}

// Detect lists the shells installed on this machine, $SHELL first. It never
// fails: with no shell at all the default entry still starts /bin/sh.
func Detect() []Shell {
	out := []Shell{Default()}
	seen := map[string]bool{}
	add := func(key, label, path string) {
		if path == "" {
			return
		}
		if abs, err := filepath.Abs(path); err == nil {
			path = abs
		}
		if seen[path] {
			return
		}
		seen[path] = true
		out = append(out, Shell{Key: key, Label: label, Command: []string{path}})
	}

	if shell := strings.TrimSpace(os.Getenv("SHELL")); shell != "" {
		if path, err := exec.LookPath(shell); err == nil {
			add("shell", labelFor(filepath.Base(path)), path)
		}
	}
	for _, k := range known {
		for _, name := range k.names {
			if path, err := exec.LookPath(name); err == nil {
				add(k.key, k.label, path)
				break
			}
		}
	}

	out = append(out, extra()...)
	return out
}
