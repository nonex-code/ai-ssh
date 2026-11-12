// Package prompt asks a human for the master password when no desktop window
// can be shown: a terminal, an askpass helper, or the environment.
//
// The AI-facing processes never unlock the vault themselves. An on-demand
// agent uses this chain only when it cannot draw the app's window (headless
// servers, services, ssh sessions without a display).
package prompt

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Func asks for a secret. It matches agent.Prompter.
type Func func(message string) (string, error)

// ErrNoChannel means the vault is locked and there is no way to ask anyone.
var ErrNoChannel = errors.New("vault is locked and there is no prompt window, terminal or askpass helper")

// Chain is the fallback order used when the agent cannot show a window:
// AISSH_MASTER_PASSWORD, then the controlling terminal, then AISSH_ASKPASS
// (or SSH_ASKPASS). It never returns an empty password.
func Chain() Func {
	return chain(Env(), TTYPrompt, Askpass())
}

func chain(steps ...Func) Func {
	return func(message string) (string, error) {
		var last error
		for _, step := range steps {
			if step == nil {
				continue
			}
			pw, err := step(message)
			if err == nil && strings.TrimSpace(pw) != "" {
				return pw, nil
			}
			if err != nil && !errors.Is(err, ErrUnavailable) {
				last = err
			}
		}
		if last != nil {
			return "", fmt.Errorf("%w: %v", ErrNoChannel, last)
		}
		return "", ErrNoChannel
	}
}

// ErrUnavailable means this channel does not exist here; the chain moves on.
var ErrUnavailable = errors.New("prompt channel unavailable")

// Env reads AISSH_MASTER_PASSWORD, which is how unattended setups run.
func Env() Func {
	return func(string) (string, error) {
		if pw := os.Getenv("AISSH_MASTER_PASSWORD"); pw != "" {
			return pw, nil
		}
		return "", ErrUnavailable
	}
}

// Askpass runs AISSH_ASKPASS (or SSH_ASKPASS) with the message as its only
// argument and reads the first line of its output. That is the convention
// ssh-askpass helpers and systemd-ask-password expect.
func Askpass() Func {
	return func(message string) (string, error) {
		prog := os.Getenv("AISSH_ASKPASS")
		if prog == "" {
			prog = os.Getenv("SSH_ASKPASS")
		}
		if prog == "" {
			return "", ErrUnavailable
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, prog, message)
		var out bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return "", fmt.Errorf("askpass %s: %w", prog, err)
		}
		line, _, _ := strings.Cut(out.String(), "\n")
		line = strings.TrimRight(line, "\r")
		if line == "" {
			return "", fmt.Errorf("askpass %s returned an empty password", prog)
		}
		return line, nil
	}
}
