package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/nonex-code/ai-ssh/internal/onagent"
	"github.com/nonex-code/ai-ssh/internal/vault"
)

// cmdPasswd changes the master password of a vault that no agent has open.
//
// An agent keeps the password in memory and re-seals the catalogue with it on
// every edit, so changing the file behind its back would make the next save
// undo the change. Rather than send the master password over IPC, this asks the
// human to lock the agent first: the window has its own in-process change.
func cmdPasswd(args []string) int {
	f, err := parseFlags(args, map[string]bool{"vault": true, "endpoint": true})
	if err != nil {
		fatalf("%v", err)
	}
	if len(f.pos) > 0 {
		fatalf("unexpected argument %q", f.pos[0])
	}
	vp := vaultPath(f)
	if _, err := os.Stat(vp); os.IsNotExist(err) {
		fatalf("no vault at %s; run \"ai-ssh init\" to create one", vp)
	} else if err != nil {
		fatalf("%v", err)
	}

	ep, err := endpointFor(f)
	if err != nil {
		fatalf("%v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Millisecond)
	running := onagent.Listening(ctx, ep)
	cancel()
	if running {
		fatalf("an agent is running on %s and holds the vault open: lock it first "+
			"(the window's lock button, or Ctrl-C in \"ai-ssh daemon\") and run passwd again", ep)
	}

	old := os.Getenv("AISSH_MASTER_PASSWORD")
	if old == "" {
		if old, err = readPassword("current master password"); err != nil {
			fatalf("%v", err)
		}
	}
	next, err := readPassword("new master password")
	if err != nil {
		fatalf("%v", err)
	}
	if err := vault.CheckPassword(old, next); err != nil {
		fatalf("%v", err)
	}
	again, err := readPassword("repeat new master password")
	if err != nil {
		fatalf("%v", err)
	}
	if next != again {
		fatalf("the new passwords do not match")
	}

	if err := vault.ChangePassword(vp, old, next); err != nil {
		fatalf("change master password: %v", err)
	}
	fmt.Fprintf(os.Stderr, "ai-ssh: master password changed; %s now opens with the new password\n", vp)
	return 0
}

// readPassword always reads the controlling terminal: the new password must be
// typed, never taken from AISSH_MASTER_PASSWORD (which would make old == new).
func readPassword(what string) (string, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return "", fmt.Errorf("stdin is not a terminal: run \"ai-ssh passwd\" in one")
	}
	fmt.Fprintf(os.Stderr, "ai-ssh %s: ", what)
	b, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", fmt.Errorf("read password: %w", err)
	}
	if strings.TrimSpace(string(b)) == "" {
		return "", fmt.Errorf("empty password")
	}
	return string(b), nil
}
