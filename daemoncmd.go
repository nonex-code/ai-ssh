package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"

	"github.com/nonex-code/ai-ssh/internal/agent"
)

func cmdInit(args []string) int {
	spec := map[string]bool{"vault": true, "force": false}
	f, err := parseFlags(args, spec)
	if err != nil {
		fatalf("%v", err)
	}
	if len(f.pos) > 0 {
		fatalf("unexpected argument %q", f.pos[0])
	}
	vp, err := agent.DefaultVaultPath(f.str("vault", ""))
	if err != nil {
		fatalf("%v", err)
	}
	if f.bool("force") {
		if err := os.Remove(vp); err != nil && !os.IsNotExist(err) {
			fatalf("remove %s: %v", vp, err)
		}
	}
	if err := agent.CreateVault(vp, terminalPrompter); err != nil {
		fatalf("%v", err)
	}

	fmt.Fprintf(os.Stderr, `
vault: %s

Next steps:
  1. Add a server: "ai-ssh config edit" (or use the desktop app's + button).
  2. Start the agent yourself with "ai-ssh daemon". You will be asked for the
     master password once; the catalogue is only ever decrypted in memory.
  3. The agent only ever runs: ai-ssh mcp  (or exec/upload/download)
`, vp)
	return 0
}

func cmdDaemon(args []string) int {
	spec := map[string]bool{
		"vault": true, "endpoint": true,
		"idle-lock": true, "no-idle-lock": false, "audit-log": true,
	}
	f, err := parseFlags(args, spec)
	if err != nil {
		fatalf("%v", err)
	}
	if len(f.pos) > 0 {
		fatalf("unexpected argument %q", f.pos[0])
	}

	ep, err := endpointFor(f)
	if err != nil {
		fatalf("%v", err)
	}
	vp, err := agent.DefaultVaultPath(f.str("vault", ""))
	if err != nil {
		fatalf("%v", err)
	}

	var override time.Duration
	if v := f.str("idle-lock", ""); v != "" {
		d, perr := time.ParseDuration(v)
		if perr != nil {
			fatalf("--idle-lock: %v", perr)
		}
		override = d
	}

	lg := log.New(os.Stderr, "ai-ssh: ", log.LstdFlags)
	a, err := agent.Open(agent.Options{
		VaultPath:       vp,
		Endpoint:        ep,
		Logger:          lg,
		Prompt:          terminalPrompter,
		AuditPath:       f.str("audit-log", ""),
		IdleLock:        override,
		DisableIdleLock: f.bool("no-idle-lock"),
	})
	if err != nil {
		fatalf("%v", err)
	}

	if runtime.GOOS != "windows" {
		if fi, err := os.Stat(vp); err == nil && fi.Mode().Perm()&0o077 != 0 {
			lg.Printf("warning: %s is readable by other users (mode %04o)", vp, fi.Mode().Perm())
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg := a.Config()
	lg.Printf("vault %s unlocked", vp)
	lg.Printf("endpoint %s", a.Endpoint)
	lg.Printf("%d host(s) available", len(a.Hosts))
	if len(a.Hosts) == 0 {
		lg.Printf("no hosts configured yet: add one with \"ai-ssh config edit\" (or the desktop app), then restart the daemon")
	}
	lg.Printf("upload roots: %s", strings.Join(cfg.Defaults.UploadRoots, ", "))
	lg.Printf("download roots: %s", strings.Join(cfg.Defaults.DownloadRoots, ", "))
	lg.Printf("idle lock: %s", time.Duration(cfg.Defaults.IdleLock))
	lg.Printf("ready; press Ctrl-C to lock")

	if err := a.Serve(ctx); err != nil {
		lg.Printf("stopped: %v", err)
		return 1
	}
	lg.Printf("stopped, keys dropped")
	return 0
}

// terminalPrompter prefers AISSH_MASTER_PASSWORD so the daemon can run
// unattended, otherwise it reads from the console without echo.
func terminalPrompter(what string) (string, error) {
	if v := os.Getenv("AISSH_MASTER_PASSWORD"); v != "" {
		return v, nil
	}
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return "", fmt.Errorf("stdin is not a terminal: set AISSH_MASTER_PASSWORD to run unattended")
	}
	fmt.Fprintf(os.Stderr, "ai-ssh %s: ", what)
	b, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", fmt.Errorf("read password: %w", err)
	}
	if len(b) == 0 {
		return "", fmt.Errorf("empty password")
	}
	return string(b), nil
}
