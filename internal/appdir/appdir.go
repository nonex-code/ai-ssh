// Package appdir keeps ai-ssh's per-user files together in ~/.ai-ssh, the way
// OpenSSH keeps its own in ~/.ssh: the vault, the window state and the audit
// log. On Windows that is C:\Users\<name>\.ai-ssh.
package appdir

import (
	"os"
	"path/filepath"
)

// Name is the directory under the user's home.
const Name = ".ai-ssh"

// Dir returns ~/.ai-ssh without creating it.
func Dir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, Name), nil
}

// VaultPath is the default vault: ~/.ai-ssh/vault.
func VaultPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "vault"), nil
}

// AuditLogPath is the default audit log: ~/.ai-ssh/audit.log.
func AuditLogPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "audit.log"), nil
}
