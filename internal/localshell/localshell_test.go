package localshell

import (
	"os"
	"strings"
	"testing"
)

func TestLabelFor(t *testing.T) {
	cases := map[string]string{
		"bash":        "Bash",
		"BASH":        "Bash",
		"bash.exe":    "Bash",
		"zsh":         "Zsh",
		"fish":        "Fish",
		"mysh":        "mysh",
		"/usr/bin/nu": "/usr/bin/nu",
	}
	for in, want := range cases {
		if got := labelFor(in); got != want {
			t.Fatalf("labelFor(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestExtraShells(t *testing.T) {
	t.Setenv("AISSH_LOCAL_SHELLS", strings.Join([]string{
		"My shell=/opt/mysh -l",
		"/bin/other",
		"=",
	}, string(os.PathListSeparator)))
	got := extra()
	if len(got) != 2 {
		t.Fatalf("extra = %+v", got)
	}
	if got[0].Label != "My shell" || strings.Join(got[0].Command, " ") != "/opt/mysh -l" {
		t.Fatalf("labelled entry = %+v", got[0])
	}
	if got[1].Label != "other" || got[1].Command[0] != "/bin/other" {
		t.Fatalf("bare entry = %+v", got[1])
	}
	if got[0].Key == got[1].Key {
		t.Fatal("keys must be unique")
	}
}
