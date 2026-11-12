package gui

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/nonex-code/ai-ssh/internal/agent"
)

func TestFileKinds(t *testing.T) {
	cases := []struct {
		e    agent.FileEntry
		want fileKind
	}{
		{agent.FileEntry{Dir: true, Mode: "drwxr-xr-x"}, fileDir},
		{agent.FileEntry{Link: true, Mode: "Lrwxrwxrwx"}, fileLink},
		{agent.FileEntry{Mode: "-rwxr-xr-x"}, fileExec},
		{agent.FileEntry{Mode: "-rw-r--r--"}, fileNormal},
		{agent.FileEntry{Name: "app.tar.gz", Mode: "-rw-r--r--"}, fileArchive},
		{agent.FileEntry{Name: "logo.png", Mode: "-rw-r--r--"}, fileMedia},
		{agent.FileEntry{Name: ".env", Mode: "-rw-------"}, fileHidden},
		{agent.FileEntry{Mode: "prw-r--r--"}, fileSpecial},
		{agent.FileEntry{Mode: "Srwxrwxrwx"}, fileSpecial},
	}
	for _, c := range cases {
		if got := kindOf(c.e); got != c.want {
			t.Errorf("%q %q: kind = %v, want %v", c.e.Name, c.e.Mode, got, c.want)
		}
	}
}

func TestFileColors(t *testing.T) {
	light := ui.LightTheme()
	dark := ui.DarkTheme()

	plain := agent.FileEntry{Name: "notes.txt", Mode: "-rw-r--r--"}
	if got := fileColor(plain, light); got != light.Text {
		t.Fatalf("a plain file takes the text color, got %v", got)
	}

	entries := map[string]agent.FileEntry{
		"dir":     {Name: "src", Dir: true, Mode: "drwxr-xr-x"},
		"link":    {Name: "run", Link: true, Mode: "Lrwxrwxrwx"},
		"exec":    {Name: "run.sh", Mode: "-rwxr-xr-x"},
		"archive": {Name: "app.zip", Mode: "-rw-r--r--"},
		"media":   {Name: "shot.png", Mode: "-rw-r--r--"},
		"special": {Name: "sock", Mode: "Srwxrwxrwx"},
		"hidden":  {Name: ".env", Mode: "-rw-------"},
	}
	seen := map[ui.Color]string{}
	for name, e := range entries {
		c := fileColor(e, light)
		if other, ok := seen[c]; ok {
			t.Fatalf("%s and %s share a color %v", name, other, c)
		}
		seen[c] = name
		if fileColor(e, dark) == c {
			t.Fatalf("%s: the color does not follow the theme", name)
		}
	}
}

func TestModeColorAndText(t *testing.T) {
	light := ui.LightTheme()

	exec := agent.FileEntry{Name: "run.sh", Mode: "-rwxr-xr-x"}
	if got := modeColor(exec, light); got != fileColor(exec, light) {
		t.Fatalf("exec mode should be green, got %v", got)
	}
	if modeText(exec) != "rwxr-xr-x" {
		t.Fatalf("mode text = %q", modeText(exec))
	}

	worldWritable := agent.FileEntry{Name: "open", Mode: "-rw-rw-rw-"}
	if got := modeColor(worldWritable, light); got != light.Warning {
		t.Fatalf("world-writable mode should be a warning, got %v", got)
	}
	setuid := agent.FileEntry{Name: "suid", Mode: "-rwsr-xr-x"}
	if got := modeColor(setuid, light); got != light.Warning {
		t.Fatalf("setuid mode should be a warning, got %v", got)
	}

	dir := agent.FileEntry{Name: "src", Dir: true, Mode: "drwxr-xr-x"}
	if got := modeColor(dir, light); got != light.TextMuted {
		t.Fatalf("a directory's x bit is traversal, not executability, got %v", got)
	}
}

// TestFilePaneIsColored renders the files pane and checks that the kinds
// really reach the pixels, not just the classifier.
func TestFilePaneIsColored(t *testing.T) {
	a := filesShotApp()
	tt := ui.NewTester(a.view, 1200, 700)
	img := tt.Image()
	if img == nil {
		t.Fatal("no image")
	}
	near := func(c ui.Color, tol int) int {
		n := 0
		b := img.Bounds()
		for y := 0; y < b.Dy(); y++ {
			for x := 0; x < b.Dx(); x++ {
				r, g, bl, _ := img.At(x, y).RGBA()
				d := func(v uint32, want uint8) int {
					got := int(v >> 8)
					if got > int(want) {
						return got - int(want)
					}
					return int(want) - got
				}
				if d(r, c.R) < tol && d(g, c.G) < tol && d(bl, c.B) < tol {
					n++
				}
			}
		}
		return n
	}
	for name, c := range map[string]ui.Color{
		"dir":     ui.Hex("#0969da"),
		"exec":    ui.Hex("#1a7f37"),
		"archive": ui.Hex("#9a6700"),
		"media":   ui.Hex("#8250df"),
		"link":    ui.Hex("#1b7c83"),
	} {
		if got := near(c, 24); got < 10 {
			t.Fatalf("%s: only %d pixels of its color in the pane", name, got)
		}
	}
}
