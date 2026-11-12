package agent_test

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/nonex-code/ai-ssh/internal/agent"
	"github.com/nonex-code/ai-ssh/internal/client"
	"github.com/nonex-code/ai-ssh/internal/config"
	"github.com/nonex-code/ai-ssh/internal/ipc"
	"github.com/nonex-code/ai-ssh/internal/proto"
	"github.com/nonex-code/ai-ssh/internal/vault"
)

func testEndpoint(t *testing.T) ipc.Endpoint {
	t.Helper()
	if runtime.GOOS == "windows" {
		return ipc.FromAddr(fmt.Sprintf(`\\.\pipe\ai-ssh-agenttest-%d`, time.Now().UnixNano()))
	}
	return ipc.FromAddr(filepath.Join(t.TempDir(), "agent.sock"))
}

const validConfig = `{
  "defaults": {"idle_lock": "5m"},
  "hosts": {
    "prod": {
      "addr": "192.0.2.10:22",
      "user": "deploy",
      "auth": {"type": "key", "key": "/home/u/.ssh/id_ed25519"}
    }
  }
}`

// staticPrompter answers every prompt with the same value, including the
// "repeat" confirmation on first start.
func staticPrompter(value string) agent.Prompter {
	return func(string) (string, error) { return value, nil }
}

// newVault seals cfgJSON into a vault the way a previous run would have left it.
func newVault(t *testing.T, path, cfgJSON string) {
	t.Helper()
	if err := vault.Save(path, []byte(cfgJSON), "correct-horse"); err != nil {
		t.Fatal(err)
	}
}

func quietLogger() *log.Logger { return log.New(io.Discard, "", 0) }

func TestOpenCreatesVaultOnFirstRun(t *testing.T) {
	dir := t.TempDir()
	vaultPath := filepath.Join(dir, "vault")

	a, err := agent.Open(agent.Options{
		VaultPath: vaultPath, AuditPath: filepath.Join(dir, "audit.log"),
		Endpoint: testEndpoint(t),
		Prompt:   staticPrompter("correct-horse"),
		Logger:   quietLogger(),
	})
	if err != nil {
		t.Fatalf("first start: %v", err)
	}
	defer a.Lock()

	if _, err := os.Stat(vaultPath); err != nil {
		t.Fatalf("vault was not created: %v", err)
	}
	if len(a.Hosts) != 0 {
		t.Fatalf("a fresh vault must have no hosts, got %v", a.Hosts)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "vault" {
		t.Fatalf("first start must write only the vault, got %v", entries)
	}
	if got := a.Config().Defaults.Approval; got != config.ApprovalAuto {
		t.Fatalf("defaults were not applied: approval = %q", got)
	}

	// The next start opens the same vault instead of creating a new one.
	a.Lock()
	a2, err := agent.Open(agent.Options{
		VaultPath: vaultPath, AuditPath: filepath.Join(dir, "audit.log"),
		Endpoint: testEndpoint(t),
		Prompt:   staticPrompter("correct-horse"),
		Logger:   quietLogger(),
	})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer a2.Lock()
}

func TestCreateVaultGuards(t *testing.T) {
	dir := t.TempDir()
	vaultPath := filepath.Join(dir, "vault")

	if err := agent.CreateVault(vaultPath, staticPrompter("short")); err == nil ||
		!strings.Contains(err.Error(), "at least 8") {
		t.Fatalf("expected a short-password error, got %v", err)
	}
	if _, err := os.Stat(vaultPath); !os.IsNotExist(err) {
		t.Fatal("a rejected password must not create the vault")
	}

	step := 0
	mismatch := func(string) (string, error) {
		step++
		if step == 1 {
			return "correct-horse", nil
		}
		return "battery-staple", nil
	}
	if err := agent.CreateVault(vaultPath, mismatch); err == nil ||
		!strings.Contains(err.Error(), "do not match") {
		t.Fatalf("expected a mismatch error, got %v", err)
	}
	if _, err := os.Stat(vaultPath); !os.IsNotExist(err) {
		t.Fatal("a mismatched password must not create the vault")
	}

	if err := agent.CreateVault(vaultPath, staticPrompter("correct-horse")); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := agent.CreateVault(vaultPath, staticPrompter("correct-horse")); err == nil ||
		!strings.Contains(err.Error(), "already exists") {
		t.Fatalf("an existing vault must not be overwritten, got %v", err)
	}
}

func TestOpenWrongPasswordFails(t *testing.T) {
	dir := t.TempDir()
	vaultPath := filepath.Join(dir, "vault")
	newVault(t, vaultPath, validConfig)

	a, err := agent.Open(agent.Options{
		VaultPath: vaultPath, AuditPath: filepath.Join(dir, "audit.log"), Endpoint: testEndpoint(t),
		Prompt: staticPrompter("correct-horse"), Logger: quietLogger(),
	})
	if err != nil {
		t.Fatal(err)
	}
	a.Lock()

	calls := 0
	_, err = agent.Open(agent.Options{
		VaultPath: vaultPath, AuditPath: filepath.Join(dir, "audit.log"), Endpoint: testEndpoint(t),
		Prompt: func(string) (string, error) {
			calls++
			return "wrong-password", nil
		},
		Logger: quietLogger(),
	})
	if err == nil {
		t.Fatal("expected failure with a wrong password")
	}
	if calls != 3 {
		t.Fatalf("expected 3 attempts, got %d", calls)
	}
}

func TestOpenRequiresMatchingPasswords(t *testing.T) {
	dir := t.TempDir()
	vaultPath := filepath.Join(dir, "vault")
	step := 0
	_, err := agent.Open(agent.Options{
		VaultPath: vaultPath, AuditPath: filepath.Join(dir, "audit.log"), Endpoint: testEndpoint(t),
		Prompt: func(msg string) (string, error) {
			step++
			if step == 1 {
				return "password-one", nil
			}
			return "password-two", nil
		},
		Logger: quietLogger(),
	})
	if err == nil || !strings.Contains(err.Error(), "do not match") {
		t.Fatalf("expected mismatch error, got %v", err)
	}
	if _, err := os.Stat(vaultPath); !os.IsNotExist(err) {
		t.Fatal("no vault should exist after a failed bootstrap")
	}
}

func TestDefaultVaultPath(t *testing.T) {
	if got, err := agent.DefaultVaultPath("/tmp/explicit"); err != nil || !strings.HasSuffix(filepath.ToSlash(got), "/tmp/explicit") {
		t.Fatalf("explicit path = %q, %v", got, err)
	}
	t.Setenv("AISSH_VAULT", "/tmp/from-env")
	if got, err := agent.DefaultVaultPath(""); err != nil || !strings.HasSuffix(filepath.ToSlash(got), "/tmp/from-env") {
		t.Fatalf("env path = %q, %v", got, err)
	}
}

func TestAgentServesAndReportsHosts(t *testing.T) {
	dir := t.TempDir()
	vaultPath := filepath.Join(dir, "vault")
	newVault(t, vaultPath, validConfig)
	ep := testEndpoint(t)

	a, err := agent.Open(agent.Options{
		VaultPath: vaultPath, AuditPath: filepath.Join(dir, "audit.log"), Endpoint: ep,
		Prompt: staticPrompter("correct-horse"), Logger: quietLogger(),
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = a.Serve(ctx) }()

	select {
	case <-a.Ready():
	case <-time.After(5 * time.Second):
		t.Fatal("agent never started listening")
	}

	resp := client.Call(ctx, ep, proto.Request{Op: proto.OpHosts})
	if !resp.OK || len(resp.Hosts) != 1 || resp.Hosts[0].Alias != "prod" {
		t.Fatalf("hosts via IPC = %+v", resp)
	}

	a.Lock()
	time.Sleep(100 * time.Millisecond)
	after := client.Call(context.Background(), ep, proto.Request{Op: proto.OpHosts})
	if after.OK || after.Error == nil || after.Error.Code != proto.ErrDaemonDown {
		t.Fatalf("the endpoint must be gone after Lock: %+v", after)
	}
}

func TestSaveConfigReloadsAndPersists(t *testing.T) {
	t.Setenv("PP", "the-real-passphrase")
	dir := t.TempDir()
	vaultPath := filepath.Join(dir, "vault")
	initial := `{
  "defaults": {"idle_lock": "5m"},
  "hosts": {
    "old": {
      "addr": "192.0.2.10:22",
      "user": "deploy",
      "auth": {"type": "key", "key": "/home/u/.ssh/id_ed25519", "passphrase": "env:PP"}
    }
  }
}`
	newVault(t, vaultPath, initial)

	ep := testEndpoint(t)
	a, err := agent.Open(agent.Options{
		VaultPath: vaultPath, AuditPath: filepath.Join(dir, "audit.log"), Endpoint: ep,
		Prompt: staticPrompter("correct-horse"), Logger: quietLogger(),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = a.Serve(ctx) }()
	select {
	case <-a.Ready():
	case <-time.After(5 * time.Second):
		t.Fatal("agent never started")
	}

	if err := a.UpdateConfig(func(cfg *config.Config) error {
		cfg.Hosts["new"] = &config.Host{
			Addr: "10.0.0.9:22",
			User: "ci",
			Auth: config.Auth{Type: "key", Key: "/home/u/.ssh/id_ed25519"},
		}
		return nil
	}); err != nil {
		t.Fatalf("save: %v", err)
	}

	// The running daemon serves the new host without a restart.
	resp := client.Call(ctx, ep, proto.Request{Op: proto.OpHosts})
	aliases := map[string]bool{}
	for _, h := range resp.Hosts {
		aliases[h.Alias] = true
	}
	if !aliases["new"] || !aliases["old"] {
		t.Fatalf("daemon hosts = %v", aliases)
	}

	// The vault kept the indirection instead of baking the secret in.
	raw, err := vault.Load(vaultPath, "correct-horse")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "env:PP") {
		t.Fatalf("the reference was lost:\n%s", raw)
	}
	if strings.Contains(string(raw), "the-real-passphrase") {
		t.Fatal("the resolved secret was written into the vault")
	}

	// A restart loads the same catalogue.
	a.Lock()
	time.Sleep(100 * time.Millisecond)
	a2, err := agent.Open(agent.Options{
		VaultPath: vaultPath, AuditPath: filepath.Join(dir, "audit.log"), Endpoint: testEndpoint(t),
		Prompt: staticPrompter("correct-horse"), Logger: quietLogger(),
	})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer a2.Lock()
	if _, ok := a2.Config().Hosts["new"]; !ok {
		t.Fatal("the new host did not survive a restart")
	}
}

func TestSaveConfigRejectsBrokenCatalogue(t *testing.T) {
	dir := t.TempDir()
	vaultPath := filepath.Join(dir, "vault")
	newVault(t, vaultPath, validConfig)
	a, err := agent.Open(agent.Options{
		VaultPath: vaultPath, AuditPath: filepath.Join(dir, "audit.log"), Endpoint: testEndpoint(t),
		Prompt: staticPrompter("correct-horse"), Logger: quietLogger(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Lock()

	if err := a.UpdateConfig(func(cfg *config.Config) error {
		cfg.Hosts["bad"] = &config.Host{User: "x", Auth: config.Auth{Type: "key", Key: "/k"}}
		return nil
	}); err == nil {
		t.Fatal("a host without an address must be rejected")
	}
	if _, err := a.Config().Hosts["bad"]; err {
		t.Fatal("the broken host must not be installed")
	}
}

func TestChangePasswordReencryptsLaterSaves(t *testing.T) {
	dir := t.TempDir()
	vaultPath := filepath.Join(dir, "vault")
	newVault(t, vaultPath, validConfig)

	a, err := agent.Open(agent.Options{
		VaultPath: vaultPath, AuditPath: filepath.Join(dir, "audit.log"), Endpoint: testEndpoint(t),
		Prompt: staticPrompter("correct-horse"), Logger: quietLogger(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Lock()

	if err := a.ChangePassword("wrong-password", "new-password-1"); err == nil {
		t.Fatal("a wrong current password must be refused")
	}
	if err := a.ChangePassword("correct-horse", "short"); err == nil {
		t.Fatal("a short new password must be refused")
	}
	if err := a.ChangePassword("correct-horse", "correct-horse"); err == nil {
		t.Fatal("an unchanged password must be refused")
	}
	if err := a.ChangePassword("correct-horse", "new-password-1"); err != nil {
		t.Fatalf("change: %v", err)
	}
	if _, err := vault.Load(vaultPath, "correct-horse"); err == nil {
		t.Fatal("the old password still opens the vault")
	}
	if _, err := vault.Load(vaultPath, "new-password-1"); err != nil {
		t.Fatalf("the new password does not open the vault: %v", err)
	}

	// The agent must keep using the new password, or its next save would seal
	// the catalogue under the old key again.
	if err := a.UpdateConfig(func(cfg *config.Config) error {
		cfg.Hosts["prod"].Workdir = "/srv/after-passwd-change"
		return nil
	}); err != nil {
		t.Fatalf("save after change: %v", err)
	}
	plain, err := vault.Load(vaultPath, "new-password-1")
	if err != nil {
		t.Fatalf("reload after save: %v", err)
	}
	if !strings.Contains(string(plain), "/srv/after-passwd-change") {
		t.Fatalf("the save did not land in the vault: %s", plain)
	}
}
