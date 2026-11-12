// Package vault stores the ai-ssh host configuration encrypted at rest with a
// master password. The plaintext only ever exists in the daemon's (or the
// window's) memory, plus the short-lived 0600 scratch file that `ai-ssh config
// edit` hands to $EDITOR.
package vault

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/crypto/argon2"
)

const (
	version   = 1
	kdfName   = "argon2id"
	saltLen   = 16
	keyLen    = 32
	argonTime = 3
	argonMem  = 64 * 1024 // KiB
	argonProc = 4
)

// ErrWrongPassword is returned when decryption fails authentication.
var ErrWrongPassword = errors.New("wrong master password (or vault is corrupted)")

type envelope struct {
	V     int    `json:"v"`
	KDF   string `json:"kdf"`
	Salt  []byte `json:"salt"`
	Nonce []byte `json:"nonce"`
	CT    []byte `json:"ct"`
}

func derive(password string, salt []byte) []byte {
	return argon2.IDKey([]byte(password), salt, argonTime, argonMem, argonProc, keyLen)
}

// Seal encrypts plaintext with password and returns a JSON envelope.
func Seal(plain []byte, password string) ([]byte, error) {
	if password == "" {
		return nil, errors.New("empty master password")
	}
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(derive(password, salt))
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	ct := aead.Seal(nil, nonce, plain, []byte(kdfName))
	env := envelope{V: version, KDF: kdfName, Salt: salt, Nonce: nonce, CT: ct}
	return json.MarshalIndent(env, "", "  ")
}

// Open decrypts an envelope produced by Seal.
func Open(data []byte, password string) ([]byte, error) {
	var env envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, fmt.Errorf("parse vault: %w", err)
	}
	if env.V != version || env.KDF != kdfName {
		return nil, fmt.Errorf("unsupported vault format (v=%d kdf=%q)", env.V, env.KDF)
	}
	if len(env.Salt) != saltLen {
		return nil, errors.New("vault salt is malformed")
	}
	block, err := aes.NewCipher(derive(password, env.Salt))
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	plain, err := aead.Open(nil, env.Nonce, env.CT, []byte(kdfName))
	if err != nil {
		return nil, ErrWrongPassword
	}
	return plain, nil
}

// Save writes the envelope with 0600 permissions, replacing atomically.
func Save(path string, plain []byte, password string) error {
	sealed, err := Seal(plain, password)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, sealed, 0o600); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

// Load reads and decrypts a vault file.
func Load(path, password string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Open(data, password)
}

// MinPasswordLen is the shortest master password the vault accepts.
const MinPasswordLen = 8

// ErrSamePassword is returned when a change asks for the password it already
// has: re-sealing would only re-derive the same key.
var ErrSamePassword = errors.New("the new master password is the same as the old one")

// CheckPassword rejects a short or unchanged password.
func CheckPassword(old, next string) error {
	if next == old {
		return ErrSamePassword
	}
	if len(next) < MinPasswordLen {
		return fmt.Errorf("master password must be at least %d characters", MinPasswordLen)
	}
	return nil
}

// ChangePassword re-seals the vault at path under next, after checking that old
// opens it. The vault must exist and nothing else may hold it: an agent keeps
// the password in memory, so callers stop the agent first (or go through
// agent.ChangePassword, which updates its copy).
func ChangePassword(path, old, next string) error {
	if err := CheckPassword(old, next); err != nil {
		return err
	}
	plain, err := Load(path, old)
	if err != nil {
		return err
	}
	return Save(path, plain, next)
}
