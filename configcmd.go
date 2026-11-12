package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"golang.org/x/term"

	"github.com/nonex-code/ai-ssh/internal/agent"
	"github.com/nonex-code/ai-ssh/internal/config"
	"github.com/nonex-code/ai-ssh/internal/vault"
)

// cmdConfig is the operator-facing side of the vault: it is the only place
// (besides the window) where the catalogue is decrypted for editing. None of
// these commands are meant for the AI; they all need the master password.
func cmdConfig(args []string) int {
	if len(args) == 0 {
		fatalf("usage: ai-ssh config show|edit|import [--vault PATH]")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "show":
		return cmdConfigShow(rest)
	case "edit":
		return cmdConfigEdit(rest)
	case "import":
		return cmdConfigImport(rest)
	default:
		fatalf("unknown config subcommand %q (want show, edit or import)", sub)
		return 1
	}
}

func vaultPath(f *flags) string {
	vp, err := agent.DefaultVaultPath(f.str("vault", ""))
	if err != nil {
		fatalf("%v", err)
	}
	return vp
}

// cmdConfigShow prints the catalogue with literal secrets masked. References
// such as env:NAME are kept: they are not secrets themselves.
func cmdConfigShow(args []string) int {
	f, err := parseFlags(args, map[string]bool{"vault": true})
	if err != nil {
		fatalf("%v", err)
	}
	vp := vaultPath(f)
	if _, err := os.Stat(vp); os.IsNotExist(err) {
		fatalf("no vault at %s; run \"ai-ssh init\" to create one", vp)
	}
	pw, err := terminalPrompter("master password")
	if err != nil {
		fatalf("%v", err)
	}
	plain, err := vault.Load(vp, pw)
	if err != nil {
		fatalf("open %s: %v", vp, err)
	}
	cfg, err := config.Parse(plain, filepath.Dir(vp))
	if err != nil {
		fatalf("%v", err)
	}
	out, err := cfg.Redacted().Marshal()
	if err != nil {
		fatalf("%v", err)
	}
	if _, err := os.Stdout.Write(out); err != nil {
		fatalf("%v", err)
	}
	return 0
}

// cmdConfigImport replaces the whole catalogue with a JSON file (or stdin). It
// is the scriptable alternative to the window's connection form; the input is
// validated before anything is written.
func cmdConfigImport(args []string) int {
	f, err := parseFlags(args, map[string]bool{"vault": true, "force": false})
	if err != nil {
		fatalf("%v", err)
	}
	if len(f.pos) > 1 {
		fatalf("usage: ai-ssh config import <file|-> [--force]")
	}
	src := "-"
	if len(f.pos) == 1 {
		src = f.pos[0]
	}
	var data []byte
	if src == "-" {
		data, err = io.ReadAll(os.Stdin)
	} else {
		data, err = os.ReadFile(src)
	}
	if err != nil {
		fatalf("read %s: %v", src, err)
	}

	vp := vaultPath(f)
	cfg, err := config.Parse(data, filepath.Dir(vp))
	if err != nil {
		fatalf("%v", err)
	}
	// Unlock (creating an empty vault on first use) and re-seal the validated
	// catalogue with the same password.
	plain, pw, err := openVault(vp)
	if err != nil {
		fatalf("%v", err)
	}
	if !f.bool("force") {
		if n := config.HostCount(plain); n > 0 {
			fatalf("vault already holds %d host(s); pass --force to replace the whole catalogue", n)
		}
	}
	if err := saveConfig(vp, pw, cfg); err != nil {
		fatalf("%v", err)
	}
	fmt.Fprintf(os.Stderr, "ai-ssh: wrote %s (%d host(s))\n", vp, len(cfg.Hosts))
	return 0
}

// cmdConfigEdit decrypts the catalogue into a 0600 file next to the vault, runs
// $EDITOR on it and seals the result again. The plaintext file only exists while
// the editor is open.
func cmdConfigEdit(args []string) int {
	f, err := parseFlags(args, map[string]bool{"vault": true, "editor": true})
	if err != nil {
		fatalf("%v", err)
	}
	if len(f.pos) > 0 {
		fatalf("unexpected argument %q", f.pos[0])
	}
	vp := vaultPath(f)
	if err := os.MkdirAll(filepath.Dir(vp), 0o700); err != nil {
		fatalf("create %s: %v", filepath.Dir(vp), err)
	}

	plain, pw, err := openVault(vp)
	if err != nil {
		fatalf("%v", err)
	}
	cfg, err := config.Parse(plain, filepath.Dir(vp))
	if err != nil {
		fatalf("%v", err)
	}
	data, err := cfg.Marshal()
	if err != nil {
		fatalf("%v", err)
	}
	original := data

	tmp := vp + ".edit"
	// os.Exit skips defers, so every failure below has to clean up explicitly.
	fail := func(format string, args ...any) int {
		_ = os.Remove(tmp)
		fatalf(format, args...)
		return 1
	}
	defer os.Remove(tmp)
	// Ctrl-C while the editor is open must not leave the plaintext behind.
	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigc
		_ = os.Remove(tmp)
		os.Exit(1)
	}()
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fail("write %s: %v", tmp, err)
	}
	_ = os.Chmod(tmp, 0o600)

	editor := f.str("editor", "")
	if editor == "" {
		editor = defaultEditor()
	}
	attempts := 0
	var edited []byte
	for {
		attempts++
		if err := runEditor(editor, tmp); err != nil {
			return fail("%v", err)
		}
		edited, err = os.ReadFile(tmp)
		if err != nil {
			return fail("read %s: %v", tmp, err)
		}
		cfg, err = config.Parse(edited, filepath.Dir(vp))
		if err == nil {
			break
		}
		fmt.Fprintf(os.Stderr, "ai-ssh: %v\n", err)
		if attempts >= 3 || !term.IsTerminal(int(os.Stdin.Fd())) {
			return fail("fix the config and run again (nothing was written)")
		}
		fmt.Fprintf(os.Stderr, "ai-ssh: opening the editor again; Ctrl-C to abort\n")
	}
	if bytes.Equal(edited, original) {
		fmt.Fprintf(os.Stderr, "ai-ssh: no changes\n")
		return 0
	}
	if err := saveConfig(vp, pw, cfg); err != nil {
		return fail("%v", err)
	}
	fmt.Fprintf(os.Stderr, "ai-ssh: wrote %s (%d host(s))\n", vp, len(cfg.Hosts))
	return 0
}

// openVault unlocks the vault, creating an empty one on first use. The password
// is needed to re-seal the catalogue after an edit.
func openVault(vp string) ([]byte, string, error) {
	lg := log.New(os.Stderr, "ai-ssh: ", log.LstdFlags)
	return agent.Unlock(vp, terminalPrompter, lg)
}

// saveConfig validates a catalogue exactly the way a restart would load it, then
// seals it into the vault.
func saveConfig(vp, pw string, cfg *config.Config) error {
	data, err := cfg.Marshal()
	if err != nil {
		return err
	}
	clean, err := config.Parse(data, filepath.Dir(vp))
	if err != nil {
		return err
	}
	data, err = clean.Marshal()
	if err != nil {
		return err
	}
	return vault.Save(vp, data, pw)
}

func defaultEditor() string {
	for _, env := range []string{"VISUAL", "EDITOR"} {
		if v := strings.TrimSpace(os.Getenv(env)); v != "" {
			return v
		}
	}
	if runtime.GOOS == "windows" {
		return "notepad"
	}
	return "vi"
}

// runEditor runs the editor with stdout/stderr/stdin attached. The editor
// string may carry arguments ("code --wait"), split on spaces.
func runEditor(editor, path string) error {
	fields := strings.Fields(editor)
	if len(fields) == 0 {
		return errors.New("empty $EDITOR")
	}
	cmd := exec.Command(fields[0], append(fields[1:], path)...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("run %s: %w", editor, err)
	}
	return nil
}
