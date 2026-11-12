package update

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCompare(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"0.1.0", "0.1.0", 0},
		{"0.1.0", "0.2.0", -1},
		{"0.2.0", "0.10.0", -1},
		{"v1.2.3", "1.2.3", 0},
		{"1.2.3+dev", "1.2.3", 0},
		{"1.2.3-rc1", "1.2.3", -1},
		{"1.2", "1.2.1", -1},
	}
	for _, c := range cases {
		if got := compare(c.a, c.b); got != c.want {
			t.Fatalf("compare(%q,%q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestArchiveName(t *testing.T) {
	if got := ArchiveName("0.1.0", "windows", "amd64"); got != "ai-ssh-0.1.0-windows-amd64.zip" {
		t.Fatalf("windows = %q", got)
	}
	if got := ArchiveName("0.1.0", "linux", "arm64"); got != "ai-ssh-0.1.0-linux-arm64.tar.gz" {
		t.Fatalf("linux = %q", got)
	}
}

func TestFindSum(t *testing.T) {
	text := "aa  ai-ssh\nbb  ai-ssh-0.1.0.tar.gz\n"
	if got := findSum(strings.NewReader(text), "ai-ssh-0.1.0.tar.gz"); got != "bb" {
		t.Fatalf("got %q", got)
	}
	if got := findSum(strings.NewReader(text), "missing"); got != "" {
		t.Fatalf("missing got %q", got)
	}
}

func TestInstallToZip(t *testing.T) {
	dir := t.TempDir()
	entry := "ai-ssh"
	if runtime.GOOS == "windows" {
		entry = "ai-ssh.exe"
	}
	target := filepath.Join(dir, entry)
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(dir, "update.zip")
	newData := []byte("new binary")
	writeTestZip(t, archive, entry, newData, true)

	if err := installTo(archive, target); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(newData) {
		t.Fatalf("installed %q", got)
	}

	// A wrong checksum must leave the current binary alone.
	if err := os.WriteFile(target, []byte("keep"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestZip(t, archive, entry, []byte("tampered"), false)
	if err := installTo(archive, target); err == nil {
		t.Fatal("a bad checksum was accepted")
	}
	if got, _ := os.ReadFile(target); string(got) != "keep" {
		t.Fatalf("failed install changed the binary: %q", got)
	}
}

func TestInstallToRejectsUnsupportedArchive(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "ai-ssh")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "update.rar")
	if err := os.WriteFile(path, []byte("nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := installTo(path, target); err == nil {
		t.Fatal("an unsupported archive was accepted")
	}
}

// writeTestZip builds an archive with the executable and its inner checksum.
func writeTestZip(t *testing.T, path, entry string, data []byte, goodSum bool) {
	t.Helper()
	// Remove an old archive first: zip.OpenReader caches nothing, but the file
	// must be truncated.
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, err := zw.Create(entry)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(data); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	line := hex.EncodeToString(sum[:]) + "  " + entry + "\n"
	if !goodSum {
		line = strings.Repeat("0", 64) + "  " + entry + "\n"
	}
	w, err = zw.Create(sumsAsset)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bytes.NewBufferString(line).WriteTo(w); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}
