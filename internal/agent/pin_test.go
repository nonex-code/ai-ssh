package agent

import (
	"path/filepath"
	"testing"

	"github.com/nonex-code/ai-ssh/internal/config"
	"github.com/nonex-code/ai-ssh/internal/vault"
)

const existingPin = "SHA256:Ux8n0n3Vq2kS9m1xYj5oZpQ0l5c4s1Ff2g7h8j9k0lm"

// A key trusted on first use without a window is kept in the vault, so the next
// connection is verified against it instead of trusted again. A host the user
// pinned (or one that disappeared while the handshake was in flight) is left
// alone.
func TestPinHostKeyPersistsOnlyForTOFU(t *testing.T) {
	dir := t.TempDir()
	vaultPath := filepath.Join(dir, "vault")
	initial := `{
  "hosts": {
    "tofu": {"addr": "192.0.2.10:22", "user": "deploy", "auth": {"type": "key", "key": "/k"}},
    "pinned": {"addr": "192.0.2.11:22", "user": "deploy", "auth": {"type": "key", "key": "/k"},
               "host_key": "` + existingPin + `"}
  }
}`
	if err := vault.Save(vaultPath, []byte(initial), "correct-horse"); err != nil {
		t.Fatal(err)
	}

	a, err := Open(Options{
		VaultPath: vaultPath,
		AuditPath: filepath.Join(dir, "audit.log"),
		Prompt:    func(string) (string, error) { return "correct-horse", nil },
		Logger:    nil,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Lock()

	const pin = "SHA256:Ux8n0n3Vq2kS9m1xYj5oZpQ0l5c4s1Ff2g7h8j9k0lA"
	if err := a.pinHostKey("tofu", pin); err != nil {
		t.Fatal(err)
	}
	if err := a.pinHostKey("gone", pin); err != nil {
		t.Fatalf("a host that vanished must not fail the connection: %v", err)
	}
	if err := a.pinHostKey("pinned", pin); err != nil {
		t.Fatalf("an explicit pin must not fail the connection: %v", err)
	}

	// The vault on disk, not just the in-memory copy, carries the decision.
	plain, err := vault.Load(vaultPath, "correct-horse")
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := config.Parse(plain, dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.Hosts["tofu"].HostKey; got != pin {
		t.Fatalf("tofu host_key = %q, want %q", got, pin)
	}
	if got := reloaded.Hosts["pinned"].HostKey; got != existingPin {
		t.Fatalf("an explicit pin was overwritten: %q", got)
	}
}
