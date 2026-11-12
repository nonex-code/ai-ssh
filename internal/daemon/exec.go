package daemon

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/nonex-code/ai-ssh/internal/config"
	"github.com/nonex-code/ai-ssh/internal/proto"
)

// elevation describes one prepared privileged invocation.
type elevation struct {
	mode    string // "" when not elevated, else "sudo" / "su"
	command string // full command string handed to the remote shell
	stdin   string // data written to the session's stdin (the sudo/su password)
	pty     bool   // a terminal is required (su, sudo with requiretty)
}

func (s *Server) handleExec(ctx context.Context, req proto.Request) proto.Response {
	h, err := s.config().Host(req.Host)
	if err != nil {
		return fail(proto.ErrUnknownHost, err.Error(), req.Host)
	}
	raw := strings.TrimSpace(strings.Join(req.Command, " "))
	if raw == "" {
		return fail(proto.ErrUsage, "empty command", req.Host)
	}

	// The host policy is always a floor, elevated or not.
	if ok, why := h.Allowed(raw); !ok {
		return fail(proto.ErrBlocked, why+": "+raw, req.Host)
	}

	workdir := req.Workdir
	if workdir == "" {
		workdir = h.Workdir
	}

	wantElevation := req.Elevate || (h.Elevate != nil && h.Elevate.Always)
	var elev *elevation
	if wantElevation {
		if ok, why := h.AllowedElevated(raw); !ok {
			return fail(proto.ErrElevDenied, why+": "+raw, req.Host)
		}
		elev, err = s.elevation(h, raw, workdir, req.PTY)
		if err != nil {
			code, msg := codeOf(err)
			return fail(code, msg, req.Host)
		}
	} else {
		elev = &elevation{command: applyWorkdir(raw, workdir)}
	}

	approval, denied := s.gate(ctx, h, proto.Approval{
		Op:       proto.OpExec,
		Command:  raw,
		Elevated: wantElevation,
	})
	if denied != nil {
		return *denied
	}

	timeout := s.config().Timeout(h)
	if req.Timeout > 0 {
		timeout = time.Duration(req.Timeout) * time.Second
	}
	maxBytes := req.MaxBytes
	if maxBytes <= 0 {
		maxBytes = s.config().Defaults.MaxOutput
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	client, err := s.dial(ctx, h)
	if err != nil {
		code, msg := codeOf(err)
		return fail(code, msg, req.Host)
	}
	defer client.Close()

	res := s.runCommand(ctx, client, elev.command, elev.stdin, elev.pty, maxBytes, req.Tail)

	if res.timedOut {
		resp := fail(proto.ErrTimeout, fmt.Sprintf("command timed out after %s", timeout), req.Host)
		resp.ExitCode = &res.exitCode
		resp.Stdout = res.stdout
		resp.Stderr = res.stderr
		resp.Truncated = res.truncated
		resp.Elevated = elev.mode != ""
		resp.ElevateMode = elev.mode
		resp.Merged = elev.pty
		resp.Approval = approval
		return resp
	}

	if res.runErr != nil {
		return fail(proto.ErrRemote, res.runErr.Error(), req.Host)
	}

	// Some distributions ship `Defaults requiretty`. sudo refuses to run
	// without a terminal, and the command itself never executed, so a single
	// retry with a PTY is safe.
	if elev.mode != "" && !elev.pty && needsTTY(res.stderr) {
		elev.pty = true
		res = s.runCommand(ctx, client, elev.command, elev.stdin, true, maxBytes, req.Tail)
		if res.runErr != nil {
			return fail(proto.ErrRemote, res.runErr.Error(), req.Host)
		}
	}

	resp := proto.Response{
		OK:          res.exitCode == 0,
		Host:        req.Host,
		ExitCode:    &res.exitCode,
		Stdout:      res.stdout,
		Stderr:      res.stderr,
		StdoutBytes: res.stdoutBytes,
		StderrBytes: res.stderrBytes,
		Truncated:   res.truncated,
		Elevated:    elev.mode != "",
		ElevateMode: elev.mode,
		Merged:      elev.pty,
		Approval:    approval,
	}
	if elev.mode != "" {
		if msg := elevationFailure(res); msg != "" {
			return withOutput(fail(proto.ErrElevation, msg, req.Host), res, resp)
		}
	}
	return resp
}

// gate asks a human when the host policy requires it. It returns the approval
// label for the response ("" when the policy did not ask) and, when the action
// must be refused, the prepared failure.
func (s *Server) gate(ctx context.Context, h *config.Host, ap proto.Approval) (string, *proto.Response) {
	need, why := s.config().NeedsApproval(h, ap.Elevated)
	if !need {
		return "", nil
	}

	wait := s.config().ApprovalWait()
	ap.ID = newID()
	ap.Host = h.Alias
	ap.Reason = why
	ap.TimeoutS = int(wait.Seconds())

	actx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()

	allow, code := s.hub.ask(actx, ap)
	if allow {
		return "approved", nil
	}

	label := "denied"
	msg := "the user denied this action"
	switch code {
	case proto.ErrApprovalUnavailable:
		label = "unavailable"
		msg = "this host requires human approval, but no front end is attached: open ai-ssh and unlock it"
	case proto.ErrApprovalTimeout:
		label = "timeout"
		msg = fmt.Sprintf("nobody approved the action within %s", wait)
	}
	resp := fail(code, msg, h.Alias)
	resp.Approval = label
	return label, &resp
}

// elevation builds the remote command line for a privileged invocation.
//
// Nothing secret ever reaches the command line: the password travels on stdin
// (`sudo -S`), and for `su` it is written to the pty.
func (s *Server) elevation(h *config.Host, raw, workdir string, forcePTY bool) (*elevation, error) {
	e := h.Elevate
	if e == nil || e.Mode == "" || e.Mode == "none" {
		return nil, errf(proto.ErrElevDenied,
			"host %q has no elevate policy; add elevate.mode: sudo (or su) to the vault", h.Alias)
	}

	out := &elevation{mode: e.Mode}

	switch e.Mode {
	case "sudo":
		var prefix string
		if e.Nopasswd || e.Password == "" {
			prefix = "sudo -n "
		} else {
			prefix = "sudo -S -p '' "
			out.stdin = e.Password
		}
		if e.User != "" {
			prefix += "-u " + shellQuote(e.User) + " "
		}
		inner := raw
		if e.WrapShell {
			// Only needed when the command must run redirections or pipes as
			// root. It also widens the sudoers rule to `sh`, so it is opt-in.
			inner = "sh -c " + shellQuote(raw)
		}
		out.command = applyWorkdir(prefix+"-- "+inner, workdir)

	case "su":
		target := e.User
		if target == "" {
			target = "root"
		}
		if e.Password == "" {
			return nil, errf(proto.ErrElevation,
				"su requires elevate.password on host %q (env: or file: indirection recommended)", h.Alias)
		}
		// su runs its -c argument through the target's login shell, which
		// resets the cwd, so the workdir has to go inside.
		out.command = "su - " + shellQuote(target) + " -c " + shellQuote(applyWorkdir(raw, workdir))
		out.stdin = e.Password
		out.pty = true

	default:
		return nil, errf(proto.ErrConfig, "host %q: unsupported elevate.mode %q", h.Alias, e.Mode)
	}

	switch e.PTY {
	case "always":
		out.pty = true
	case "never":
		out.pty = out.pty && true // su still needs one; never overrides sudo only
	default:
		if e.Mode == "su" {
			out.pty = true
		}
	}
	if forcePTY {
		out.pty = true
	}
	return out, nil
}

func applyWorkdir(cmd, dir string) string {
	if dir == "" {
		return cmd
	}
	return "cd -- " + shellQuote(dir) + " && " + cmd
}

// --- running -----------------------------------------------------------------

type runResult struct {
	stdout, stderr           string
	stdoutBytes, stderrBytes int64
	truncated                bool
	exitCode                 int
	timedOut                 bool
	runErr                   error
}

// runCommand executes one command on an existing client. Every call uses a
// fresh session, which is what makes the PTY retry possible.
func (s *Server) runCommand(ctx context.Context, client *ssh.Client, command, stdinText string, pty bool, maxBytes int64, tail bool) runResult {
	session, err := client.NewSession()
	if err != nil {
		return runResult{runErr: err}
	}
	defer session.Close()

	var out, errOut *capWriter
	out = newCapWriter(maxBytes, tail)
	session.Stdout = out

	if pty {
		if err := session.RequestPty("xterm-256color", 80, 24, ssh.TerminalModes{
			ssh.ECHO:  0, // never echo the password we are about to write
			ssh.ONLCR: 0, // keep LF, drop the CRLF translation
			ssh.OCRNL: 0,
		}); err != nil {
			return runResult{runErr: fmt.Errorf("request pty: %w", err)}
		}
	} else {
		errOut = newCapWriter(maxBytes, tail)
		session.Stderr = errOut
	}

	// Always give the remote side an stdin so commands that read it get EOF
	// instead of hanging until the timeout. With sudo -S this carries the
	// password: sudo consumes the first line, the rest is EOF.
	session.Stdin = strings.NewReader(stdinText)

	if err := session.Start(command); err != nil {
		return runResult{runErr: err}
	}

	done := make(chan error, 1)
	go func() { done <- session.Wait() }()

	var waitErr error
	select {
	case waitErr = <-done:
	case <-ctx.Done():
		_ = client.Close()
		<-done
		r := runResult{timedOut: true, exitCode: 124}
		var kept int64
		r.stdout, kept, r.stdoutBytes = out.snapshot()
		r.truncated = kept < r.stdoutBytes
		if errOut != nil {
			r.stderr, r.stderrBytes, _ = errOut.snapshot()
		}
		r.stdout = normalizePTY(r.stdout)
		r.stderr = normalizePTY(r.stderr)
		return r
	}

	res := runResult{}
	var exitErr *ssh.ExitError
	var missing *ssh.ExitMissingError
	switch {
	case waitErr == nil:
		res.exitCode = 0
	case errors.As(waitErr, &exitErr):
		res.exitCode = exitErr.ExitStatus()
	case errors.As(waitErr, &missing):
		res.exitCode = 255
	default:
		res.runErr = waitErr
		return res
	}

	var kept int64
	res.stdout, kept, res.stdoutBytes = out.snapshot()
	res.truncated = kept < res.stdoutBytes
	if errOut != nil {
		var errKept int64
		res.stderr, errKept, res.stderrBytes = errOut.snapshot()
		res.truncated = res.truncated || errKept < res.stderrBytes
	}

	if pty {
		// A pty merges stderr into stdout; report it as merged rather than
		// pretending stderr is empty for no reason.
		res.stdout = normalizePTY(res.stdout)
		res.stderr = ""
		res.stderrBytes = 0
	}
	return res
}

// normalizePTY undoes terminal line discipline artefacts and the su prompt.
func normalizePTY(s string) string {
	if s == "" {
		return s
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.TrimPrefix(s, "Password: ")
	return s
}

func needsTTY(stderr string) bool {
	l := strings.ToLower(stderr)
	return strings.Contains(l, "must have a tty") ||
		strings.Contains(l, "no tty present") ||
		strings.Contains(l, "a terminal is required")
}

func elevationFailure(res runResult) string {
	l := strings.ToLower(res.stderr)
	switch {
	case strings.Contains(l, "a password is required"), strings.Contains(l, "no askpass program specified"):
		return "sudo needs a password: set elevate.password in the vault (env: or file: indirection)"
	case strings.Contains(l, "sorry, try again"), strings.Contains(l, "incorrect password"),
		strings.Contains(l, "authentication failure"):
		return "sudo rejected the configured password"
	case strings.Contains(l, "not in the sudoers file"), strings.Contains(l, "is not allowed to execute"):
		return "the remote user is not permitted to run this command via sudo"
	case strings.Contains(l, "must have a tty"):
		return "sudo refused to run without a terminal (set elevate.pty: always and try again)"
	}
	return ""
}

func withOutput(resp proto.Response, res runResult, base proto.Response) proto.Response {
	resp.Stdout = res.stdout
	resp.Stderr = res.stderr
	resp.ExitCode = &res.exitCode
	resp.Truncated = base.Truncated
	resp.Elevated = base.Elevated
	resp.ElevateMode = base.ElevateMode
	resp.Merged = base.Merged
	return resp
}

// shellQuote produces a POSIX-safe single-quoted word.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// capWriter keeps at most max bytes of a stream. In head mode it keeps the
// beginning, in tail mode the end (usually what you want for logs).
type capWriter struct {
	max  int64
	tail bool

	total int64
	buf   []byte // head mode
	ring  []byte // tail mode
	pos   int
	full  bool
}

func newCapWriter(max int64, tail bool) *capWriter {
	return &capWriter{max: max, tail: tail}
}

func (w *capWriter) Write(p []byte) (int, error) {
	n := len(p)
	w.total += int64(n)
	if w.max <= 0 {
		return n, nil
	}

	if !w.tail {
		room := w.max - int64(len(w.buf))
		if room > 0 {
			if int64(len(p)) > room {
				p = p[:room]
			}
			w.buf = append(w.buf, p...)
		}
		return n, nil
	}

	if int64(len(p)) >= w.max {
		if w.ring == nil {
			w.ring = make([]byte, w.max)
		}
		copy(w.ring, p[int64(len(p))-w.max:])
		w.pos = 0
		w.full = true
		return n, nil
	}
	if w.ring == nil {
		w.ring = make([]byte, w.max)
	}
	copied := copy(w.ring[w.pos:], p)
	if copied < len(p) {
		copy(w.ring, p[copied:])
	}
	w.pos = (w.pos + len(p)) % len(w.ring)
	if w.total >= w.max {
		w.full = true
	}
	return n, nil
}

// snapshot renders the captured bytes plus a truncation marker.
func (w *capWriter) snapshot() (text string, kept, total int64) {
	total = w.total
	var body string
	if !w.tail {
		body = string(w.buf)
		kept = int64(len(w.buf))
	} else {
		if w.ring == nil {
			return "", 0, total
		}
		var raw []byte
		if !w.full {
			raw = w.ring[:w.pos]
		} else {
			raw = append(append([]byte{}, w.ring[w.pos:]...), w.ring[:w.pos]...)
		}
		body = string(raw)
		kept = int64(len(raw))
	}
	if total > kept {
		marker := fmt.Sprintf("...[truncated %d bytes]...\n", total-kept)
		if w.tail {
			return marker + body, kept, total
		}
		return body + "\n" + marker, kept, total
	}
	return body, kept, total
}
