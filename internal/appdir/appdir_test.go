package appdir

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPathsLiveInHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	vault, err := VaultPath()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, Name, "vault"); vault != want {
		t.Fatalf("vault = %q, want %q", vault, want)
	}

	audit, err := AuditLogPath()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, Name, "audit.log"); audit != want {
		t.Fatalf("audit = %q, want %q", audit, want)
	}

	// Dir does not create anything: the vault writer owns that.
	if _, err := os.Stat(filepath.Join(home, Name)); !os.IsNotExist(err) {
		t.Fatal("Dir must not create the directory")
	}
}
