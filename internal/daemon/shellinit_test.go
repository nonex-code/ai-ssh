package daemon

import (
	"strings"
	"testing"
)

func TestParseShellProbe(t *testing.T) {
	shell, osName := parseShellProbe("/bin/bash|Linux\n")
	if shell != "/bin/bash" || osName != "Linux" {
		t.Fatalf("probe = %q / %q", shell, osName)
	}
	if shell, _ := parseShellProbe("hello"); shell != "" {
		t.Fatalf("a command that did not run it must not count: %q", shell)
	}
}

func TestSessionInitFor(t *testing.T) {
	linux := sessionInitFor("/bin/bash", "Linux")
	for _, want := range []string{"alias ls", "--color=auto", "\\r\\033[1A\\033[2K"} {
		if !strings.Contains(linux, want) {
			t.Fatalf("linux init missing %q: %q", want, linux)
		}
	}
	if !strings.HasSuffix(linux, "\n") {
		t.Fatalf("the init must be a whole line: %q", linux)
	}
	if strings.Contains(sessionInitFor("/bin/zsh", "Linux"), "-G") {
		t.Fatal("GNU ls takes --color, not -G")
	}
	if got := sessionInitFor("/bin/zsh", "Darwin"); !strings.Contains(got, "ls -G") {
		t.Fatalf("BSD ls takes -G: %q", got)
	}

	// Shells whose alias syntax this does not know are left alone, and so
	// are servers that report no login shell at all.
	for _, shell := range []string{"", "/usr/bin/fish", "/usr/bin/pwsh", "/usr/bin/nu", `C:\Windows\cmd.exe`} {
		if got := sessionInitFor(shell, "Linux"); got != "" {
			t.Fatalf("%q should not be initialised: %q", shell, got)
		}
	}
}
