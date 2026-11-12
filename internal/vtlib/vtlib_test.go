package vtlib

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExtractWritesOnceAndReusesTheHashPath(t *testing.T) {
	root := t.TempDir()
	data := []byte("not really a library")

	path, err := extractTo(data, root, "ghostty-vt.dll")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(filepath.Dir(path)) == "" {
		t.Fatalf("path = %q", path)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != string(data) {
		t.Fatalf("read back: %q, %v", got, err)
	}

	// A second start must reuse the file, not rewrite it.
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	again, err := extractTo(data, root, "ghostty-vt.dll")
	if err != nil {
		t.Fatal(err)
	}
	fi2, err := os.Stat(again)
	if err != nil {
		t.Fatal(err)
	}
	if !fi.ModTime().Equal(fi2.ModTime()) {
		t.Fatal("the existing library was rewritten")
	}
}

func TestExtractRepairsACorruptFile(t *testing.T) {
	root := t.TempDir()
	good := []byte("good library")
	path, err := extractTo(good, root, "libghostty-vt.so")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("truncated"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := extractTo(good, root, "libghostty-vt.so"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != string(good) {
		t.Fatalf("not repaired: %q, %v", got, err)
	}
}

func TestExtractRejectsEmptyInput(t *testing.T) {
	if _, err := extractTo(nil, t.TempDir(), "x"); err == nil {
		t.Fatal("expected an error")
	}
}

func TestEnsureIsANoOpWithoutAnEmbeddedLibrary(t *testing.T) {
	if embeddedName != "" {
		t.Skip("this build embeds a library")
	}
	if err := Ensure(); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
}
