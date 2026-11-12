// Package agent is the embeddable core: it owns the vault, the decrypted host
// catalogue and the daemon that serves requests.
//
// Both front ends use it:
//
//   - the CLI (`ai-ssh daemon`) drives it with a terminal prompter
//   - a GUI drives it with a dialog prompter in a goroutine
//
// Nothing in this package talks to the network directly.
package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/nonex-code/ai-ssh/internal/appdir"
	"github.com/nonex-code/ai-ssh/internal/config"
	"github.com/nonex-code/ai-ssh/internal/daemon"
	"github.com/nonex-code/ai-ssh/internal/ipc"
	"github.com/nonex-code/ai-ssh/internal/vault"
)

// Prompter asks the human for a secret. A GUI can block this on a modal; the
// CLI reads from the terminal without echo.
type Prompter func(message string) (string, error)

// Options configures Open.
type Options struct {
	VaultPath string
	Endpoint  ipc.Endpoint
	Prompt    Prompter
	Logger    *log.Logger
	AuditPath string
	// MaxConcurrent caps simultaneous remote operations.
	MaxConcurrent int
	// IdleLock overrides the configured idle timeout. DisableIdleLock wins.
	IdleLock        time.Duration
	DisableIdleLock bool
}

// Agent is a running (or ready to run) credential holder.
type Agent struct {
	Endpoint ipc.Endpoint
	Hosts    []string

	mu        sync.Mutex
	saveMu    sync.Mutex // one read-modify-write of the catalogue at a time
	cfg       *config.Config
	vaultPath string
	srv       *daemon.Server
	// password is kept so the host catalogue can be re-sealed when a GUI edits
	// it. It never leaves this process.
	password string

	ready     chan struct{}
	readyOnce sync.Once
}

// Open unlocks the vault, creating it on first run, and prepares the daemon. It
// does not start listening; call Serve.
func Open(opts Options) (*Agent, error) {
	if opts.Prompt == nil {
		return nil, errors.New("agent: a Prompter is required")
	}
	if opts.Logger == nil {
		opts.Logger = log.New(os.Stderr, "ai-ssh: ", log.LstdFlags)
	}

	plain, password, err := unlock(opts.VaultPath, opts.Prompt, opts.Logger)
	if err != nil {
		return nil, err
	}

	cfg, err := config.Parse(plain, filepath.Dir(opts.VaultPath))
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	switch {
	case opts.DisableIdleLock:
		cfg.Defaults.IdleLock = -1
	case opts.IdleLock != 0:
		cfg.Defaults.IdleLock = config.Duration(opts.IdleLock)
	}

	a := &Agent{
		Endpoint:  opts.Endpoint,
		Hosts:     cfg.Aliases(),
		cfg:       cfg,
		vaultPath: opts.VaultPath,
		password:  password,
		ready:     make(chan struct{}),
	}

	srv, err := daemon.New(daemon.Options{
		Config:        cfg,
		Endpoint:      opts.Endpoint,
		VaultPath:     opts.VaultPath,
		Logger:        opts.Logger,
		AuditPath:     opts.AuditPath,
		MaxConcurrent: opts.MaxConcurrent,
		// Interactive sessions get the ls alias; the daemon probes the
		// remote shell and leaves its own configuration alone.
		SessionInit: true,
		OnListen:    func() { a.readyOnce.Do(func() { close(a.ready) }) },
	})
	if err != nil {
		return nil, err
	}
	a.srv = srv
	return a, nil
}

// Ready is closed once the endpoint accepts connections: a GUI can then talk to
// its own agent over IPC without racing the listener.
func (a *Agent) Ready() <-chan struct{} { return a.ready }

// Serve blocks until the context is cancelled, the idle timer fires or a lock
// request arrives. It returns after the endpoint is closed and keys are gone.
func (a *Agent) Serve(ctx context.Context) error { return a.srv.Serve(ctx) }

// Lock stops the agent immediately: the socket disappears, in-flight work is
// abandoned, and the process should drop its reference to the Agent.
func (a *Agent) Lock() { a.srv.Shutdown() }

// Config exposes the decrypted catalogue for a GUI to render. Callers must not
// serialise it anywhere the agent can read. The pointer stays valid; a save
// swaps in a new one.
func (a *Agent) Config() *config.Config {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cfg
}

// ConfigCopy returns a detached catalogue a front end can edit.
func (a *Agent) ConfigCopy() (*config.Config, error) {
	a.mu.Lock()
	cur, path := a.cfg, a.vaultPath
	a.mu.Unlock()

	data, err := cur.Marshal()
	if err != nil {
		return nil, err
	}
	return config.Parse(data, filepath.Dir(path))
}

// UpdateConfig applies fn to a fresh copy of the catalogue and re-seals it,
// with the read-modify-write serialised: two edits at once cannot lose one of
// them (a delete racing a save, say). fn runs off the UI thread.
func (a *Agent) UpdateConfig(fn func(*config.Config) error) error {
	a.saveMu.Lock()
	defer a.saveMu.Unlock()
	cfg, err := a.ConfigCopy()
	if err != nil {
		return err
	}
	if err := fn(cfg); err != nil {
		return err
	}
	return a.saveLocked(cfg)
}

// saveLocked is SaveConfig with the write lock already held.
func (a *Agent) saveLocked(cfg *config.Config) error {
	a.mu.Lock()
	password, path := a.password, a.vaultPath
	a.mu.Unlock()

	data, err := cfg.Marshal()
	if err != nil {
		return err
	}
	// Round trip through the parser: what we store must be exactly what a
	// restart would load.
	clean, err := config.Parse(data, filepath.Dir(path))
	if err != nil {
		return err
	}
	data, err = clean.Marshal()
	if err != nil {
		return err
	}
	if err := vault.Save(path, data, password); err != nil {
		return err
	}

	a.mu.Lock()
	a.cfg = clean
	a.Hosts = clean.Aliases()
	a.mu.Unlock()
	a.srv.SetConfig(clean)
	return nil
}

// ChangePassword re-seals the vault under a new master password and swaps the
// copy the agent keeps for later catalogue saves, so an edit after the change
// stays encrypted with the new key. The old password is checked both against
// the agent's copy and against the vault on disk.
func (a *Agent) ChangePassword(old, next string) error {
	a.saveMu.Lock()
	defer a.saveMu.Unlock()

	a.mu.Lock()
	path, current := a.vaultPath, a.password
	a.mu.Unlock()
	if current != "" && old != current {
		return vault.ErrWrongPassword
	}
	if err := vault.CheckPassword(old, next); err != nil {
		return err
	}
	if err := vault.ChangePassword(path, old, next); err != nil {
		return err
	}

	a.mu.Lock()
	a.password = next
	a.mu.Unlock()
	return nil
}

// DefaultVaultPath resolves the vault location.
func DefaultVaultPath(flagValue string) (string, error) {
	if flagValue != "" {
		return filepath.Abs(flagValue)
	}
	if v := os.Getenv("AISSH_VAULT"); v != "" {
		return filepath.Abs(v)
	}
	return appdir.VaultPath()
}

// Unlock opens (or creates) the vault and hands back the decrypted payload plus
// the master password. Operator commands use it to re-seal an edited catalogue;
// long-lived callers should use Open instead.
func Unlock(path string, prompt Prompter, logger *log.Logger) ([]byte, string, error) {
	if prompt == nil {
		return nil, "", errors.New("agent: a Prompter is required")
	}
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}
	return unlock(path, prompt, logger)
}

// unlock opens the vault, or creates it when this is the first start.
func unlock(path string, prompt Prompter, logger *log.Logger) (plain []byte, password string, err error) {
	data, statErr := os.ReadFile(path)
	if statErr == nil {
		const attempts = 3
		var lastErr error
		for i := 0; i < attempts; i++ {
			pw, perr := prompt("master password")
			if perr != nil {
				return nil, "", perr
			}
			plain, oerr := vault.Open(data, pw)
			if oerr == nil {
				return plain, pw, nil
			}
			if !errors.Is(oerr, vault.ErrWrongPassword) {
				return nil, "", oerr
			}
			lastErr = oerr
			logger.Printf("%v (%d/%d)", oerr, i+1, attempts)
		}
		return nil, "", lastErr
	} else if !os.IsNotExist(statErr) {
		return nil, "", statErr
	}

	return create(path, prompt, logger)
}

// CreateVault writes a fresh, empty vault at path. First start does this
// automatically; `ai-ssh init` exposes it so the vault can be prepared before
// the daemon or the window runs. It is a no-op error if the vault exists.
func CreateVault(path string, prompt Prompter) error {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("vault %s already exists", path)
	} else if !os.IsNotExist(err) {
		return err
	}
	if prompt == nil {
		return errors.New("agent: a Prompter is required")
	}
	_, _, err := create(path, prompt, log.New(io.Discard, "", 0))
	return err
}

// create asks for a new master password and seals the empty catalogue. Nothing
// is ever written to disk unencrypted.
func create(path string, prompt Prompter, logger *log.Logger) ([]byte, string, error) {
	pw, err := prompt("choose a master password")
	if err != nil {
		return nil, "", err
	}
	if len(pw) < vault.MinPasswordLen {
		return nil, "", fmt.Errorf("master password must be at least %d characters", vault.MinPasswordLen)
	}
	again, err := prompt("repeat master password")
	if err != nil {
		return nil, "", err
	}
	if pw != again {
		return nil, "", errors.New("passwords do not match")
	}
	plain, err := config.EmptyPayload()
	if err != nil {
		return nil, "", err
	}
	// Round trip through the parser so a bug in the defaults can never produce
	// a vault that does not open.
	if _, err := config.Parse(plain, filepath.Dir(path)); err != nil {
		return nil, "", fmt.Errorf("refusing to write an invalid config: %w", err)
	}
	if err := vault.Save(path, plain, pw); err != nil {
		return nil, "", fmt.Errorf("write vault: %w", err)
	}
	logger.Printf("created %s", path)
	return plain, pw, nil
}
