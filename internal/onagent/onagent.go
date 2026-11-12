// Package onagent starts the credential-holding agent when an AI-facing
// process needs one, so nobody has to launch the desktop app or `daemon` by
// hand.
//
// The client stays credential free: it spawns the same binary in `agent
// --auto` mode, the agent asks the human to unlock, and the client only talks
// to the resulting endpoint. A lock file makes sure two simultaneous calls
// open one password window, not two.
package onagent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/nonex-code/ai-ssh/internal/appdir"
	"github.com/nonex-code/ai-ssh/internal/ipc"
)

// Errors callers turn into an unlock_required reply.
var (
	ErrNoAutostart = errors.New("automatic agent start is disabled")
	ErrTimeout     = errors.New("timed out waiting for the agent to unlock")
)

// Options configures Ensure.
type Options struct {
	VaultPath string
	Endpoint  ipc.Endpoint
	Logger    *log.Logger
	// Timeout bounds the wait for a human to enter the password. Five minutes
	// is long enough for a slow typist and short enough to fail an AI call.
	Timeout time.Duration
	// NoAutostart only probes; it never spawns. AISSH_NO_AUTOSTART does the
	// same for scripts and tests.
	NoAutostart bool
}

// Ensure returns when an agent is listening on o.Endpoint, starting one when
// necessary. The returned error explains why no agent could be reached.
func Ensure(ctx context.Context, o Options) error {
	if o.Endpoint.Addr == "" {
		return errors.New("onagent: empty endpoint")
	}
	if o.Logger == nil {
		o.Logger = log.New(os.Stderr, "ai-ssh: ", log.LstdFlags)
	}
	if o.Timeout <= 0 {
		o.Timeout = 5 * time.Minute
	}
	if running(ctx, o.Endpoint) {
		return nil
	}
	if o.NoAutostart || os.Getenv("AISSH_NO_AUTOSTART") != "" {
		return ErrNoAutostart
	}
	if os.Getenv("AISSH_AGENT_CHILD") != "" {
		// Only the client spawns; an agent that cannot unlock must fail
		// visibly instead of forking another agent.
		return errors.New("onagent: refusing to start an agent from an agent")
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	deadline := time.Now().Add(o.Timeout)
	lockPath, err := spawnLockPath(o.VaultPath)
	if err != nil {
		return err
	}

	for {
		if running(ctx, o.Endpoint) {
			return nil
		}
		if time.Now().After(deadline) {
			return ErrTimeout
		}
		lk, ok := tryLock(lockPath)
		if !ok {
			// Somebody else is already showing the password window; wait for
			// its agent instead of opening a second one.
			if !sleep(ctx, 100*time.Millisecond) {
				return ctx.Err()
			}
			continue
		}

		if running(ctx, o.Endpoint) {
			lk.Close()
			return nil
		}
		o.Logger.Printf("starting the agent (vault %s)", o.VaultPath)
		done, err := spawn(o)
		if err != nil {
			lk.Close()
			return fmt.Errorf("start %s: %w", "ai-ssh agent", err)
		}
		err = waitReady(ctx, o, deadline, done)
		lk.Close()
		return err
	}
}

// waitReady polls the endpoint until the agent listens, the child exits or the
// deadline passes.
func waitReady(ctx context.Context, o Options, deadline time.Time, done <-chan error) error {
	t := time.NewTicker(100 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-done:
			// The child may have become ready and exited in one step (a
			// request served immediately); trust the endpoint first.
			if running(ctx, o.Endpoint) {
				return nil
			}
			return fmt.Errorf("the agent exited before it was ready: %w (see %s)", err, AgentLogPath())
		case <-t.C:
			if running(ctx, o.Endpoint) {
				return nil
			}
			if time.Now().After(deadline) {
				return ErrTimeout
			}
		}
	}
}

// running reports whether the endpoint accepts a connection right now.
func running(ctx context.Context, ep ipc.Endpoint) bool {
	dctx, cancel := context.WithTimeout(ctx, 400*time.Millisecond)
	defer cancel()
	conn, err := ep.Dial(dctx)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// Listening reports whether something accepts connections on ep right now. It
// is for operator commands that must refuse to act while an agent holds the
// vault open, such as changing the master password.
func Listening(ctx context.Context, ep ipc.Endpoint) bool { return running(ctx, ep) }

// spawnLockPath is per vault, so two vaults do not serialise behind one
// password window.
func spawnLockPath(vaultPath string) (string, error) {
	dir, err := appdir.Dir()
	if err != nil {
		dir = os.TempDir()
	}
	dir = filepath.Join(dir, "locks")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create lock dir: %w", err)
	}
	sum := sha256.Sum256([]byte(vaultPath))
	return filepath.Join(dir, hex.EncodeToString(sum[:6])+".lock"), nil
}

func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// AgentLogPath is where the on-demand agent's diagnostics go. The agent must
// not inherit the client's stdout/stderr: an AI host or a shell pipeline would
// wait for EOF and hang until the agent exits.
func AgentLogPath() string {
	dir, err := appdir.Dir()
	if err != nil {
		return "the agent log"
	}
	return filepath.Join(dir, "agent.log")
}

// openAgentLog opens the shared diagnostic log, appending. A failure is not
// fatal: a silent agent is better than one that refuses to start.
func openAgentLog() *os.File {
	dir, err := appdir.Dir()
	if err != nil {
		return nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil
	}
	f, err := os.OpenFile(AgentLogPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil
	}
	return f
}

// agentArgs is how the client starts the very same binary in agent mode.
func agentArgs(o Options) []string {
	args := []string{"agent", "--auto"}
	if o.VaultPath != "" {
		args = append(args, "--vault", o.VaultPath)
	}
	if o.Endpoint.Addr != "" {
		args = append(args, "--endpoint", o.Endpoint.Addr)
	}
	return args
}
