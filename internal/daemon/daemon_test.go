package daemon

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/nonex-code/ai-ssh/internal/config"
	"github.com/nonex-code/ai-ssh/internal/proto"
)

func TestCapWriterHead(t *testing.T) {
	w := newCapWriter(8, false)
	w.Write([]byte("0123456789abcdef"))
	text, kept, total := w.snapshot()
	if total != 16 || kept != 8 {
		t.Fatalf("total=%d kept=%d", total, kept)
	}
	if !strings.HasPrefix(text, "01234567") {
		t.Fatalf("head not preserved: %q", text)
	}
	if !strings.Contains(text, "truncated 8 bytes") {
		t.Fatalf("missing marker: %q", text)
	}
}

func TestCapWriterTail(t *testing.T) {
	w := newCapWriter(8, true)
	w.Write([]byte("0123456789abcdef"))
	text, kept, total := w.snapshot()
	if total != 16 || kept != 8 {
		t.Fatalf("total=%d kept=%d", total, kept)
	}
	if !strings.HasSuffix(text, "89abcdef") {
		t.Fatalf("tail not preserved: %q", text)
	}
	if !strings.HasPrefix(text, "...[truncated 8 bytes]") {
		t.Fatalf("missing marker: %q", text)
	}
}

func TestCapWriterTailMultipleWrites(t *testing.T) {
	w := newCapWriter(4, true)
	for _, s := range []string{"ab", "cd", "ef", "gh"} {
		w.Write([]byte(s))
	}
	text, _, _ := w.snapshot()
	if !strings.HasSuffix(text, "efgh") {
		t.Fatalf("tail not preserved across writes: %q", text)
	}
}

func TestCapWriterNoTruncation(t *testing.T) {
	w := newCapWriter(64, false)
	w.Write([]byte("short"))
	text, kept, total := w.snapshot()
	if text != "short" || kept != 5 || total != 5 {
		t.Fatalf("text=%q kept=%d total=%d", text, kept, total)
	}
}

func TestWithinPath(t *testing.T) {
	base := "/srv/app"
	cases := []struct {
		target string
		want   bool
	}{
		{"/srv/app", true},
		{"/srv/app/file.txt", true},
		{"/srv/app/sub/file.txt", true},
		{"/srv/application/file.txt", false},
		{"/srv", false},
		{"/etc/passwd", false},
	}
	for _, c := range cases {
		if got := withinPath(base, c.target); got != c.want {
			t.Errorf("withinPath(%q, %q) = %v, want %v", base, c.target, got, c.want)
		}
	}
}

func TestCheckLocalPath(t *testing.T) {
	dir := t.TempDir()
	cfg, err := config.Parse([]byte(fmt.Sprintf(`{
  "defaults": {"upload_roots": [%q]},
  "hosts": {}
}`, toSlash(dir))), dir)
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(Options{Config: cfg, VaultPath: dir + "/vault", AuditPath: dir + "/audit.log"})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := s.checkLocalPath(dir+"/ok.txt", s.cfg.Defaults.UploadRoots, false); err != nil {
		t.Fatalf("expected allowed path: %v", err)
	}
	_, err = s.checkLocalPath(dir+"/../escape.txt", s.cfg.Defaults.UploadRoots, false)
	if err == nil {
		t.Fatal("expected path traversal to be rejected")
	}
	if code, _ := codeOf(err); code != proto.ErrPathDenied {
		t.Fatalf("code = %q", code)
	}
}

func TestDenyAndAllowRules(t *testing.T) {
	dir := t.TempDir()
	key := toSlash(dir) + "/k"
	cfg, err := config.Parse([]byte(fmt.Sprintf(`{
  "hosts": {
    "a": {
      "addr": "127.0.0.1:22",
      "user": "u",
      "auth": {"type": "key", "key": %q},
      "deny": ["^reboot", "rm[[:space:]]+-rf[[:space:]]+/"]
    },
    "b": {
      "addr": "127.0.0.1:22",
      "user": "u",
      "auth": {"type": "key", "key": %q},
      "allow": ["^ls\\b"]
    }
  }
}`, key, key)), dir)
	if err != nil {
		t.Fatal(err)
	}

	flagTest := func(alias, cmd string, want bool) {
		t.Helper()
		h, err := cfg.Host(alias)
		if err != nil {
			t.Fatal(err)
		}
		if got, why := h.Allowed(cmd); got != want {
			t.Fatalf("%s / %q allowed=%v want %v (%s)", alias, cmd, got, want, why)
		}
	}
	flagTest("a", "ls -la", true)
	flagTest("a", "reboot now", false)
	flagTest("a", "rm -rf /", false)
	flagTest("b", "ls -la", true)
	flagTest("b", "cat /etc/shadow", false)
}

func TestPasswordAuthIsAllowed(t *testing.T) {
	cfg, err := config.Parse([]byte(`{
  "hosts": {
    "a": {
      "addr": "127.0.0.1:22",
      "user": "u",
      "auth": {"type": "password", "password": "hunter2"}
    }
  }
}`), t.TempDir())
	if err != nil {
		t.Fatalf("password auth must be allowed: %v", err)
	}
	if cfg.Hosts["a"].Auth.Password != "hunter2" {
		t.Fatalf("password = %q", cfg.Hosts["a"].Auth.Password)
	}
}

func TestSecretResolutionFromEnv(t *testing.T) {
	t.Setenv("AISSH_TEST_PASS", "s3cret-value")
	cfg, err := config.Parse([]byte(`{
  "hosts": {
    "a": {
      "addr": "127.0.0.1:22",
      "user": "u",
      "auth": {"type": "password", "password": "env:AISSH_TEST_PASS"}
    }
  }
}`), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Hosts["a"].Auth.Password; got != "s3cret-value" {
		t.Fatalf("password = %q", got)
	}
	found := false
	for _, s := range cfg.Secrets() {
		if s == "s3cret-value" {
			found = true
		}
	}
	if !found {
		t.Fatal("resolved secret was not registered for redaction")
	}
}

// --- helpers ---------------------------------------------------------------

func toSlash(p string) string {
	return strings.ReplaceAll(p, `\`, `/`)
}

func TestHostKeyCallbackKnownHosts(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	kh := filepath.Join(dir, "known_hosts")
	line := "[127.0.0.1]:2222 " + strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key))) + "\n"
	if err := os.WriteFile(kh, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}

	s := &Server{
		cfg: &config.Config{Defaults: config.Defaults{KnownHosts: kh}},
		log: log.New(io.Discard, "", 0),
	}
	cb, err := s.hostKeyCallback(&config.Host{Alias: "x", HostKey: "known_hosts"})
	if err != nil {
		t.Fatal(err)
	}
	remote := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 2222}
	if err := cb("127.0.0.1:2222", remote, key); err != nil {
		t.Fatalf("known key rejected: %v", err)
	}

	otherPub, _, _ := ed25519.GenerateKey(rand.Reader)
	otherKey, _ := ssh.NewPublicKey(otherPub)
	err = cb("127.0.0.1:2222", remote, otherKey)
	if err == nil {
		t.Fatal("mismatched key accepted")
	}
	var mm *HostKeyMismatchError
	if !errors.As(err, &mm) {
		t.Fatalf("want a HostKeyMismatchError, got %T: %v", err, err)
	}

	// A host with no entry at all must also be rejected, not silently trusted.
	if err := cb("127.0.0.1:2299", &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 2299}, key); err == nil {
		t.Fatal("unknown host accepted")
	}
}

func TestHostKeyCallbackMissingKnownHosts(t *testing.T) {
	s := &Server{
		cfg: &config.Config{Defaults: config.Defaults{KnownHosts: filepath.Join(t.TempDir(), "nope")}},
		log: log.New(io.Discard, "", 0),
	}
	if _, err := s.hostKeyCallback(&config.Host{Alias: "x", HostKey: "known_hosts"}); err == nil {
		t.Fatal("expected an error for a missing known_hosts file")
	}
}

func TestHostKeyCallbackInsecure(t *testing.T) {
	s := &Server{log: log.New(io.Discard, "", 0)}
	cb, err := s.hostKeyCallback(&config.Host{Alias: "x", HostKey: "insecure"})
	if err != nil {
		t.Fatal(err)
	}
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	key, _ := ssh.NewPublicKey(pub)
	if err := cb("anything:22", nil, key); err != nil {
		t.Fatalf("insecure callback should accept: %v", err)
	}
}

// --- elevation command construction ----------------------------------------

func TestElevationSudoShape(t *testing.T) {
	s := &Server{}
	h := &config.Host{
		Alias:   "prod",
		Elevate: &config.Elevate{Mode: "sudo", Password: "pw"},
	}

	el, err := s.elevation(h, "systemctl restart nginx", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if want := "sudo -S -p '' -- systemctl restart nginx"; el.command != want {
		t.Fatalf("command = %q, want %q", el.command, want)
	}
	if el.stdin != "pw" {
		t.Fatalf("stdin = %q", el.stdin)
	}
	if el.pty {
		t.Fatal("plain sudo must not request a pty")
	}

	// The workdir has to stay outside sudo, otherwise it would need root to cd.
	el, _ = s.elevation(h, "id", "/srv/app", false)
	if want := "cd -- '/srv/app' && sudo -S -p '' -- id"; el.command != want {
		t.Fatalf("command = %q, want %q", el.command, want)
	}
}

func TestElevationSudoOptions(t *testing.T) {
	s := &Server{}

	nopass := &config.Host{Elevate: &config.Elevate{Mode: "sudo", Nopasswd: true}}
	el, err := s.elevation(nopass, "id", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if el.command != "sudo -n -- id" || el.stdin != "" {
		t.Fatalf("nopasswd: %q stdin=%q", el.command, el.stdin)
	}

	withUser := &config.Host{Elevate: &config.Elevate{Mode: "sudo", Password: "x", User: "postgres"}}
	el, _ = s.elevation(withUser, "id", "", false)
	if want := "sudo -S -p '' -u 'postgres' -- id"; el.command != want {
		t.Fatalf("user: %q", el.command)
	}

	wrapped := &config.Host{Elevate: &config.Elevate{Mode: "sudo", Password: "x", WrapShell: true}}
	el, _ = s.elevation(wrapped, "id > /root/x", "", false)
	if want := "sudo -S -p '' -- sh -c 'id > /root/x'"; el.command != want {
		t.Fatalf("wrap_shell: %q", el.command)
	}

	noPolicy := &config.Host{}
	_, perr := s.elevation(noPolicy, "id", "", false)
	if perr == nil {
		t.Fatal("elevation without a policy must be refused")
	}
	if code, _ := codeOf(perr); code != proto.ErrElevDenied {
		t.Fatalf("code = %q", code)
	}
}

func TestElevationSuShape(t *testing.T) {
	s := &Server{}
	h := &config.Host{
		Elevate: &config.Elevate{Mode: "su", Password: "pw", User: "root", PTY: "always"},
	}
	el, err := s.elevation(h, "id", "/srv/app", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(el.command, "su - 'root' -c '") {
		t.Fatalf("command = %q", el.command)
	}
	if !strings.Contains(el.command, "/srv/app") {
		t.Fatalf("workdir must live inside su -c: %q", el.command)
	}
	if el.stdin != "pw" || !el.pty {
		t.Fatalf("stdin=%q pty=%v", el.stdin, el.pty)
	}
}

func TestElevationForcePTY(t *testing.T) {
	s := &Server{}
	h := &config.Host{Elevate: &config.Elevate{Mode: "sudo", Password: "pw", PTY: "auto"}}
	el, err := s.elevation(h, "id", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if !el.pty {
		t.Fatal("--pty must force a pty")
	}
}

func TestElevateConfigValidation(t *testing.T) {
	base := `{
  "hosts": {
    "a": {
      "addr": "127.0.0.1:22",
      "user": "u",
      "auth": {"type": "key", "key": "/tmp/k"},
      "elevate": %s
    }
  }
}`
	cases := []struct {
		name string
		body string
		ok   bool
	}{
		{"sudo without password", `{"mode": "sudo"}`, true},
		{"sudo with password", `{"mode": "sudo", "password": "hunter2"}`, true},
		{"su without password", `{"mode": "su"}`, false},
		{"bad mode", `{"mode": "doas"}`, false},
		{"bad pty", `{"mode": "sudo", "pty": "sometimes"}`, false},
		{"bad user", `{"mode": "sudo", "user": "root; id"}`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := config.Parse([]byte(fmt.Sprintf(base, c.body)), t.TempDir())
			if c.ok && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !c.ok && err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestElevateSecretIsRedacted(t *testing.T) {
	dir := t.TempDir()
	cfg, err := config.Parse([]byte(`{
  "hosts": {
    "a": {
      "addr": "127.0.0.1:22",
      "user": "u",
      "auth": {"type": "key", "key": "/tmp/k"},
      "elevate": {"mode": "sudo", "password": "super-secret-sudo"}
    }
  }
}`), dir)
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(Options{Config: cfg, VaultPath: dir + "/vault", AuditPath: dir + "/audit.log"})
	if err != nil {
		t.Fatal(err)
	}
	if got := s.redact("the password super-secret-sudo leaked"); strings.Contains(got, "super-secret-sudo") {
		t.Fatalf("not redacted: %q", got)
	}
}

func TestAllowedElevatedRules(t *testing.T) {
	dir := t.TempDir()
	cfg, err := config.Parse([]byte(`{
  "hosts": {
    "a": {
      "addr": "127.0.0.1:22",
      "user": "u",
      "auth": {"type": "key", "key": "/tmp/k"},
      "elevate": {
        "mode": "sudo",
        "password": "x",
        "deny": ["^visudo$"],
        "allow": ["^systemctl "]
      }
    }
  }
}`), dir)
	if err != nil {
		t.Fatal(err)
	}
	h := cfg.Hosts["a"]
	cases := []struct {
		cmd  string
		want bool
	}{
		{"systemctl restart nginx", true},
		{"cat /etc/shadow", false},
		{"visudo", false},
	}
	for _, c := range cases {
		if got, why := h.AllowedElevated(c.cmd); got != c.want {
			t.Fatalf("%q allowed=%v want %v (%s)", c.cmd, got, c.want, why)
		}
	}
}

func TestUnknownHostKeyCarriesTheFingerprint(t *testing.T) {
	dir := t.TempDir()
	known := filepath.Join(dir, "known_hosts")
	if err := os.WriteFile(known, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Parse([]byte(fmt.Sprintf(`{
  "hosts": {
    "h": {
      "addr": "192.0.2.1:22",
      "user": "u",
      "auth": {"type": "key", "key": "/k"},
      "host_key": %q
    }
  }
}`, toSlash(known))), dir)
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(Options{Config: cfg, VaultPath: filepath.Join(dir, "vault"), AuditPath: filepath.Join(dir, "audit.log")})
	if err != nil {
		t.Fatal(err)
	}
	cb, err := s.hostKeyCallback(cfg.Hosts["h"])
	if err != nil {
		t.Fatal(err)
	}

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pub, err := ssh.NewPublicKey(priv.Public())
	if err != nil {
		t.Fatal(err)
	}
	err = cb("192.0.2.1:22", &net.TCPAddr{IP: net.IPv4(192, 0, 2, 1), Port: 22}, pub)
	var unk *UnknownHostKeyError
	if !errors.As(err, &unk) {
		t.Fatalf("a first meeting must be structured, got %T %v", err, err)
	}
	if unk.Fingerprint != ssh.FingerprintSHA256(pub) {
		t.Fatalf("fingerprint = %q", unk.Fingerprint)
	}
	if unk.Host != "192.0.2.1:22" {
		t.Fatalf("host = %q", unk.Host)
	}
}

func TestHostKeyMismatchIsStructured(t *testing.T) {
	dir := t.TempDir()
	known := filepath.Join(dir, "known_hosts")

	_, wantPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	wantPub, err := ssh.NewPublicKey(wantPriv.Public())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(known, []byte(knownhosts.Line([]string{"192.0.2.1"}, wantPub)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, gotPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	gotPub, err := ssh.NewPublicKey(gotPriv.Public())
	if err != nil {
		t.Fatal(err)
	}

	newServer := func(hostKey string) *Server {
		cfg, err := config.Parse([]byte(fmt.Sprintf(`{
  "hosts": {
    "h": {
      "addr": "192.0.2.1:22",
      "user": "u",
      "auth": {"type": "key", "key": "/k"},
      "host_key": %q
    }
  }
}`, hostKey)), dir)
		if err != nil {
			t.Fatal(err)
		}
		s, err := New(Options{Config: cfg, VaultPath: filepath.Join(dir, "vault"), AuditPath: filepath.Join(dir, "audit.log")})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	remote := &net.TCPAddr{IP: net.IPv4(192, 0, 2, 1), Port: 22}
	check := func(t *testing.T, err error) {
		t.Helper()
		var mm *HostKeyMismatchError
		if !errors.As(err, &mm) {
			t.Fatalf("a changed key must be structured, got %T %v", err, err)
		}
		if mm.Host != "192.0.2.1:22" || mm.Got != ssh.FingerprintSHA256(gotPub) || mm.Want != ssh.FingerprintSHA256(wantPub) {
			t.Fatalf("mismatch = %+v", mm)
		}
		code, msg := codeOf(err)
		if code != proto.ErrHostKey || !strings.Contains(msg, "host key mismatch") {
			t.Fatalf("codeOf = %q %q", code, msg)
		}
	}

	t.Run("known_hosts", func(t *testing.T) {
		s := newServer(known)
		cb, err := s.hostKeyCallback(s.config().Hosts["h"])
		if err != nil {
			t.Fatal(err)
		}
		check(t, cb("192.0.2.1:22", remote, gotPub))
	})

	t.Run("pin", func(t *testing.T) {
		s := newServer(ssh.FingerprintSHA256(wantPub))
		cb, err := s.hostKeyCallback(s.config().Hosts["h"])
		if err != nil {
			t.Fatal(err)
		}
		check(t, cb("192.0.2.1:22", remote, gotPub))
	})
}
