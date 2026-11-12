// Package localshell finds the shells a window can open on this machine: the
// platform's default shell, the ones installed beside it (PowerShell, cmd,
// WSL distributions, Git Bash on Windows; $SHELL, zsh, bash, fish and friends
// elsewhere), and any custom path in AISSH_LOCAL_SHELLS.
//
// A local session never touches the vault or the agent: it runs a program on
// this machine through the terminal widget's own PTY, exactly as the desktop
// terminal apps do.
package localshell

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Shell is one local shell the window offers.
//
// Command is what to run; an empty Command means the terminal picks the
// platform's login shell itself ($SHELL, or PowerShell on Windows).
type Shell struct {
	Key     string
	Label   string
	Command []string
}

// extra returns shells the user named in AISSH_LOCAL_SHELLS, separated by the
// OS path list separator (":" on Unix, ";" on Windows). Each entry is a label
// and a command, as "My shell=/usr/local/bin/mysh -l".
func extra() []Shell {
	raw := strings.TrimSpace(os.Getenv("AISSH_LOCAL_SHELLS"))
	if raw == "" {
		return nil
	}
	var out []Shell
	for i, entry := range strings.Split(raw, string(os.PathListSeparator)) {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		label, command := "", entry
		if before, after, ok := strings.Cut(entry, "="); ok {
			label, command = strings.TrimSpace(before), strings.TrimSpace(after)
		}
		fields := strings.Fields(command)
		if len(fields) == 0 {
			continue
		}
		if label == "" {
			label = filepath.Base(fields[0])
		}
		out = append(out, Shell{
			Key:     "env:" + label + ":" + strconv.Itoa(i),
			Label:   label,
			Command: fields,
		})
	}
	return out
}

// labelFor prettifies a shell's file name.
func labelFor(base string) string {
	switch strings.ToLower(strings.TrimSuffix(base, ".exe")) {
	case "zsh":
		return "Zsh"
	case "bash":
		return "Bash"
	case "fish":
		return "Fish"
	case "nu":
		return "Nushell"
	case "elvish":
		return "Elvish"
	case "xonsh":
		return "Xonsh"
	case "dash":
		return "dash"
	case "ksh":
		return "ksh"
	case "tcsh":
		return "tcsh"
	case "sh":
		return "sh"
	}
	return base
}
