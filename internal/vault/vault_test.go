package vault

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

const pw = "correct horse battery staple"

func TestSealOpenRoundTrip(t *testing.T) {
	plain := []byte("hosts:\n  prod:\n    addr: 10.0.0.5:22\n")
	sealed, err := Seal(plain, pw)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte("10.0.0.5")) {
		t.Fatal("ciphertext leaks plaintext")
	}
	got, err := Open(sealed, pw)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("round trip mismatch: %q", got)
	}
}

func TestSealUsesFreshNonceAndSalt(t *testing.T) {
	a, err := Seal([]byte("same"), pw)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Seal([]byte("same"), pw)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(a, b) {
		t.Fatal("two seals of the same plaintext are identical")
	}
}

func TestOpenWrongPassword(t *testing.T) {
	sealed, err := Seal([]byte("secret"), pw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Open(sealed, "nope"); err == nil {
		t.Fatal("expected failure with wrong password")
	}
}

func TestOpenDetectsTampering(t *testing.T) {
	sealed, err := Seal([]byte("secret"), pw)
	if err != nil {
		t.Fatal(err)
	}
	// Flip a byte inside the base64 ciphertext field.
	idx := bytes.Index(sealed, []byte(`"ct": "`))
	if idx < 0 {
		t.Fatal("envelope shape changed")
	}
	pos := idx + len(`"ct": "`) + 4
	if sealed[pos] == 'A' {
		sealed[pos] = 'B'
	} else {
		sealed[pos] = 'A'
	}
	if _, err := Open(sealed, pw); err == nil {
		t.Fatal("expected tampering to be detected")
	}
}

func TestSaveLoadPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "vault")
	if err := Save(path, []byte("data"), pw); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm()&0o077 != 0 {
		t.Fatalf("vault is too permissive: %04o", fi.Mode().Perm())
	}
	got, err := Load(path, pw)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "data" {
		t.Fatalf("got %q", got)
	}
}

func TestChangePassword(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vault")
	if err := Save(path, []byte("data"), pw); err != nil {
		t.Fatal(err)
	}
	if err := ChangePassword(path, pw, "short"); err == nil {
		t.Fatal("a short password must be refused")
	}
	if err := ChangePassword(path, pw, pw); err != ErrSamePassword {
		t.Fatalf("same password: %v, want ErrSamePassword", err)
	}
	if err := ChangePassword(path, "wrong", "a new long password"); err == nil {
		t.Fatal("the old password must be checked")
	}
	if err := ChangePassword(path, pw, "a new long password"); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path, pw); err == nil {
		t.Fatal("the old password still opens the vault")
	}
	got, err := Load(path, "a new long password")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "data" {
		t.Fatalf("payload changed: %q", got)
	}
}

func TestCheckPassword(t *testing.T) {
	if err := CheckPassword("old", "1234567"); err == nil {
		t.Fatal("7 characters must be refused")
	}
	if err := CheckPassword("old", "old"); err != ErrSamePassword {
		t.Fatalf("same: %v", err)
	}
	if err := CheckPassword("old", "12345678"); err != nil {
		t.Fatal(err)
	}
}
