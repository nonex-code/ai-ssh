package daemon

import (
	"context"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/nonex-code/ai-ssh/internal/proto"
)

// Shell is an interactive remote session.
//
// It is deliberately NOT reachable through the request protocol. The AI gets
// exec/upload/download over the socket; the human's window calls this
// in-process, so a local process speaking the wire protocol cannot open a raw
// shell and bypass the command policy.
type Shell struct {
	srv     *Server
	host    string
	client  *ssh.Client
	session *ssh.Session
	stdin   io.WriteCloser
	stdout  io.Reader

	mu     sync.Mutex
	closed bool
	opened time.Time
}

// OpenShell starts a login shell in a PTY on the remote host.
func (s *Server) OpenShell(ctx context.Context, alias string, cols, rows int) (*Shell, error) {
	h, err := s.config().Host(alias)
	if err != nil {
		return nil, errf(proto.ErrUnknownHost, "%v", err)
	}
	if cols <= 0 {
		cols = 80
	}
	if rows <= 0 {
		rows = 24
	}

	client, err := s.dial(ctx, h)
	if err != nil {
		return nil, err
	}

	sess, err := client.NewSession()
	if err != nil {
		_ = client.Close()
		return nil, errf(proto.ErrRemote, "open session: %v", err)
	}

	// ECHO on: the remote shell owns echo, as in any terminal.
	modes := ssh.TerminalModes{
		ssh.ECHO:          1,
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
	}
	if err := sess.RequestPty("xterm-256color", rows, cols, modes); err != nil {
		_ = sess.Close()
		_ = client.Close()
		return nil, errf(proto.ErrRemote, "request pty: %v", err)
	}

	stdin, err := sess.StdinPipe()
	if err != nil {
		_ = sess.Close()
		_ = client.Close()
		return nil, errf(proto.ErrRemote, "stdin: %v", err)
	}
	stdout, err := sess.StdoutPipe()
	if err != nil {
		_ = sess.Close()
		_ = client.Close()
		return nil, errf(proto.ErrRemote, "stdout: %v", err)
	}
	if err := sess.Shell(); err != nil {
		_ = sess.Close()
		_ = client.Close()
		return nil, errf(proto.ErrRemote, "start shell: %v", err)
	}

	// Give ls its colours. The remote shell owns this, so the client can
	// only add the alias the user's rc did not; it goes in before the shell
	// reads, and clears the line it was echoed on.
	if s.sessionInit {
		if init := s.shellInit(h.Alias, client); init != "" {
			_, _ = io.WriteString(stdin, init)
		}
	}

	sh := &Shell{
		srv:     s,
		host:    h.Alias,
		client:  client,
		session: sess,
		stdin:   stdin,
		stdout:  stdout,
		opened:  time.Now(),
	}

	// A session is the human's, so it is not gated by the approval policy, but
	// it is recorded: opening a raw shell is a privileged act.
	s.audit.write(auditEntry{
		Time: time.Now().UTC().Format(time.RFC3339Nano),
		Op:   "session",
		Host: h.Alias,
		OK:   true,
	})
	s.log.Printf("op=session host=%q opened", h.Alias)
	return sh, nil
}

// shellProbe asks the login shell and the remote OS in one round trip: both
// decide how ls is aliased.
const shellProbe = `printf '%s|%s' "$SHELL" "$(uname -s 2>/dev/null)"`

// shellInit probes the remote shell and returns the line to run at login, or
// "" when it cannot help. The answer is kept per host: the shell and OS do not
// change between tabs.
func (s *Server) shellInit(host string, client *ssh.Client) string {
	s.initMu.Lock()
	if v, ok := s.initCache[host]; ok {
		s.initMu.Unlock()
		return v
	}
	s.initMu.Unlock()

	init := s.probeShellInit(client)

	s.initMu.Lock()
	if len(s.initCache) >= 64 {
		s.initCache = map[string]string{}
	}
	s.initCache[host] = init
	s.initMu.Unlock()
	return init
}

func (s *Server) probeShellInit(client *ssh.Client) string {
	sess, err := client.NewSession()
	if err != nil {
		return ""
	}
	defer sess.Close()
	type probe struct {
		out []byte
		err error
	}
	ch := make(chan probe, 1)
	go func() {
		out, err := sess.Output(shellProbe)
		ch <- probe{out, err}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			return ""
		}
		shell, osName := parseShellProbe(string(r.out))
		return sessionInitFor(shell, osName)
	case <-time.After(3 * time.Second):
		// A server that does not answer the probe simply gets no init line.
		return ""
	}
}

func parseShellProbe(out string) (shell, osName string) {
	parts := strings.SplitN(strings.TrimSpace(out), "|", 2)
	if len(parts) != 2 {
		return "", ""
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
}

// sessionInitFor is the setup line: only shells whose alias syntax this
// understands, only when ls has no alias yet (so eza/lsd setups are left
// alone), and with the right colour flag for GNU and BSD ls. It clears its
// own echoed line, so the scrollback stays clean.
func sessionInitFor(shell, osName string) string {
	switch strings.ToLower(filepath.Base(strings.TrimSpace(shell))) {
	case "bash", "zsh", "sh", "dash", "ksh", "ash", "mksh", "pdksh":
	default:
		return ""
	}
	flag := "--color=auto"
	if osName == "Darwin" || strings.Contains(osName, "BSD") {
		flag = "-G"
	}
	return " alias ls >/dev/null 2>&1 || alias ls='ls " + flag +
		"'; printf '\\r\\033[1A\\033[2K'\n"
}

func (sh *Shell) Read(p []byte) (int, error)  { return sh.stdout.Read(p) }
func (sh *Shell) Write(p []byte) (int, error) { return sh.stdin.Write(p) }

// Resize tells the remote PTY the new size, as a terminal emulator does.
func (sh *Shell) Resize(cols, rows int) error {
	sh.mu.Lock()
	defer sh.mu.Unlock()
	if sh.closed {
		return io.ErrClosedPipe
	}
	return sh.session.WindowChange(rows, cols)
}

// Wait blocks until the remote shell exits.
func (sh *Shell) Wait() error { return sh.session.Wait() }

// Close ends the session and its connection.
func (sh *Shell) Close() error {
	sh.mu.Lock()
	if sh.closed {
		sh.mu.Unlock()
		return nil
	}
	sh.closed = true
	sh.mu.Unlock()

	_ = sh.stdin.Close()
	_ = sh.session.Close()
	err := sh.client.Close()

	if sh.srv != nil {
		sh.srv.audit.write(auditEntry{
			Time:       time.Now().UTC().Format(time.RFC3339Nano),
			Op:         "session",
			Host:       sh.host,
			OK:         true,
			DurationMs: time.Since(sh.opened).Milliseconds(),
		})
		sh.srv.log.Printf("op=session host=%q closed after %s", sh.host, time.Since(sh.opened).Round(time.Second))
	}
	return err
}
