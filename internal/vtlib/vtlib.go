// Package vtlib makes the terminal's native library (libghostty-vt) part of
// the ai-ssh binary.
//
// Self-contained builds (`make build`, `make package`) embed the target
// platform's library; the first time a terminal is opened it is extracted to
// ~/.ai-ssh/lib/vt/<sha256>/, and mygo is pointed at it with MYGO_GHOSTTY_VT.
// A build without the embed tag (`make build-dev`, `go test`) still works:
// mygo looks next to the executable, in its cache, or downloads it.
package vtlib

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/nonex-code/ai-ssh/internal/appdir"
)

// Ensure makes the embedded terminal library usable by mygo. It is a no-op in
// builds that do not embed one, and never fails a launch: callers log the
// error and the terminal pane reports it.
func Ensure() error {
	if p := os.Getenv("MYGO_GHOSTTY_VT"); p != "" {
		return nil
	}
	if len(embedded) == 0 || embeddedName == "" {
		return nil
	}
	// A library next to the executable wins, so portable copies and `go run`
	// keep working exactly as before.
	if exe, err := os.Executable(); err == nil {
		if _, err := os.Stat(filepath.Join(filepath.Dir(exe), embeddedName)); err == nil {
			return nil
		}
	}
	dir, err := appdir.Dir()
	if err != nil {
		return err
	}
	path, err := extractTo(embedded, filepath.Join(dir, "lib", "vt"), embeddedName)
	if err != nil {
		return err
	}
	return os.Setenv("MYGO_GHOSTTY_VT", path)
}

// extractTo writes data under root/<sha256>/<name> and returns the path. The
// hash in the directory name makes upgrades and concurrent starts safe: a
// different library is a different file, and the same one is shared.
func extractTo(data []byte, root, name string) (string, error) {
	if len(data) == 0 || name == "" {
		return "", errors.New("vtlib: nothing to extract")
	}
	sum := sha256.Sum256(data)
	dir := filepath.Join(root, hex.EncodeToString(sum[:]))
	path := filepath.Join(dir, name)

	if matches(path, sum) {
		return path, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, name+".tmp-*")
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return "", fmt.Errorf("write %s: %w", tmpName, err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return "", fmt.Errorf("sync %s: %w", tmpName, err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return "", fmt.Errorf("chmod %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("close %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		// Windows refuses to replace a library another process has loaded;
		// if that file is already the one we want, sharing is correct.
		if matches(path, sum) {
			return path, nil
		}
		return "", fmt.Errorf("install %s: %w", path, err)
	}
	if !matches(path, sum) {
		return "", fmt.Errorf("%s does not match its embedded checksum", path)
	}
	return path, nil
}

func matches(path string, sum [sha256.Size]byte) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return false
	}
	return hex.EncodeToString(h.Sum(nil)) == hex.EncodeToString(sum[:])
}
