package daemon_test

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"github.com/nonex-code/ai-ssh/internal/client"
	"github.com/nonex-code/ai-ssh/internal/config"
	"github.com/nonex-code/ai-ssh/internal/daemon"
	"github.com/nonex-code/ai-ssh/internal/ipc"
	"github.com/nonex-code/ai-ssh/internal/proto"
)

// --- a tiny in-process SSH server -------------------------------------------

type testSSH struct {
	addr string
	fp   string
	pub  ssh.PublicKey
}

// fakeOpts controls how the in-process sshd emulates sudo/su.
type fakeOpts struct {
	sudoPassword string // password sudo expects; empty = no password accepted
	requireTTY   bool   // emulate `Defaults requiretty`
}

func startSSHServer(t *testing.T, authorized ssh.PublicKey, opts *fakeOpts) *testSSH {
	t.Helper()

	_, hostPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	hostSigner, err := ssh.NewSignerFromKey(hostPriv)
	if err != nil {
		t.Fatal(err)
	}

	cfg := &ssh.ServerConfig{
		PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if ssh.FingerprintSHA256(key) != ssh.FingerprintSHA256(authorized) {
				return nil, fmt.Errorf("unauthorized key")
			}
			return nil, nil
		},
	}
	cfg.AddHostKey(hostSigner)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			nc, err := ln.Accept()
			if err != nil {
				return
			}
			go serveSSHConn(nc, cfg, opts)
		}
	}()

	return &testSSH{
		addr: ln.Addr().String(),
		fp:   ssh.FingerprintSHA256(hostSigner.PublicKey()),
		pub:  hostSigner.PublicKey(),
	}
}

func serveSSHConn(nc net.Conn, cfg *ssh.ServerConfig, opts *fakeOpts) {
	sc, chans, reqs, err := ssh.NewServerConn(nc, cfg)
	if err != nil {
		_ = nc.Close()
		return
	}
	defer sc.Close()
	go ssh.DiscardRequests(reqs)

	for newCh := range chans {
		if newCh.ChannelType() == "direct-tcpip" {
			// The daemon's forwards dial from the server side: give them a
			// real TCP connection so a tunnel can be tested.
			var p struct {
				Host  string
				Port  uint32
				OHost string
				OPort uint32
			}
			_ = ssh.Unmarshal(newCh.ExtraData(), &p)
			upstream, err := net.Dial("tcp", net.JoinHostPort(p.Host, strconv.Itoa(int(p.Port))))
			if err != nil {
				_ = newCh.Reject(ssh.ConnectionFailed, err.Error())
				continue
			}
			ch, reqs, err := newCh.Accept()
			if err != nil {
				_ = upstream.Close()
				continue
			}
			go ssh.DiscardRequests(reqs)
			go func() {
				defer ch.Close()
				defer upstream.Close()
				go io.Copy(upstream, ch)
				_, _ = io.Copy(ch, upstream)
			}()
			continue
		}
		if newCh.ChannelType() != "session" {
			_ = newCh.Reject(ssh.UnknownChannelType, "only sessions")
			continue
		}
		ch, chReqs, err := newCh.Accept()
		if err != nil {
			continue
		}
		go handleSession(ch, chReqs, opts)
	}
}

func handleSession(ch ssh.Channel, reqs <-chan *ssh.Request, opts *fakeOpts) {
	hasPTY := false
	for req := range reqs {
		switch req.Type {
		case "pty-req":
			hasPTY = true
			_ = req.Reply(true, nil)
		case "window-change":
			_ = req.Reply(true, nil)
		case "exec":
			var payload struct{ Command string }
			_ = ssh.Unmarshal(req.Payload, &payload)
			_ = req.Reply(true, nil)
			go runFakeCommand(ch, payload.Command, hasPTY, opts)
		case "shell":
			_ = req.Reply(true, nil)
			go func() {
				buf := make([]byte, 1024)
				for {
					n, err := ch.Read(buf)
					if n > 0 {
						_, _ = ch.Write(buf[:n])
					}
					if err != nil {
						return
					}
				}
			}()
		case "subsystem":
			var payload struct{ Name string }
			_ = ssh.Unmarshal(req.Payload, &payload)
			if payload.Name != "sftp" {
				_ = req.Reply(false, nil)
				continue
			}
			_ = req.Reply(true, nil)
			go func() {
				srv, err := sftp.NewServer(ch)
				if err != nil {
					_ = ch.Close()
					return
				}
				_ = srv.Serve()
				_ = srv.Close()
			}()
		default:
			_ = req.Reply(false, nil)
		}
	}
}

func finishChannel(ch ssh.Channel, code uint32) {
	_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{code}))
	_ = ch.Close()
}

// runFakeCommand emulates just enough sudo/su to exercise the daemon: it reads
// the password from stdin (never from argv), enforces `requiretty`, and then
// runs the remainder of the command line.
func runFakeCommand(ch ssh.Channel, cmd string, hasPTY bool, opts *fakeOpts) {
	if opts == nil {
		opts = &fakeOpts{}
	}
	nl := "\n"
	if hasPTY {
		nl = "\r\n"
	}

	if strings.Contains(cmd, "sudo ") {
		if opts.requireTTY && !hasPTY {
			_, _ = io.WriteString(ch.Stderr(), "sudo: sorry, you must have a tty to run sudo"+nl)
			finishChannel(ch, 1)
			return
		}
		if opts.sudoPassword != "" && strings.Contains(cmd, opts.sudoPassword) {
			_, _ = io.WriteString(ch, "PASSWORD_IN_ARGV"+nl)
			finishChannel(ch, 9)
			return
		}
		switch {
		case strings.Contains(cmd, " -S "):
			line, _ := bufio.NewReader(ch).ReadString('\n')
			if strings.TrimRight(line, "\r\n") != opts.sudoPassword {
				_, _ = io.WriteString(ch.Stderr(), "Sorry, try again."+nl)
				finishChannel(ch, 1)
				return
			}
		case strings.Contains(cmd, " -n "):
			_, _ = io.WriteString(ch.Stderr(), "sudo: a password is required"+nl)
			finishChannel(ch, 1)
			return
		}
		if i := strings.Index(cmd, " -- "); i >= 0 {
			cmd = cmd[i+4:]
		}
	}

	if strings.HasPrefix(cmd, "su ") {
		if opts.sudoPassword != "" {
			line, _ := bufio.NewReader(ch).ReadString('\n')
			if strings.TrimRight(line, "\r\n") != opts.sudoPassword {
				_, _ = io.WriteString(ch, "su: Authentication failure"+nl)
				finishChannel(ch, 1)
				return
			}
		}
		if i := strings.Index(cmd, " -c "); i >= 0 {
			cmd = strings.Trim(strings.TrimSpace(cmd[i+4:]), "'")
		}
	}

	code := uint32(0)
	switch {
	case strings.Contains(cmd, "fail"):
		code = 3
		_, _ = io.WriteString(ch.Stderr(), "boom"+nl)
	case strings.Contains(cmd, "sleep"):
		time.Sleep(10 * time.Second)
	case strings.Contains(cmd, "big"):
		_, _ = ch.Write([]byte(strings.Repeat("x", 1<<20)))
	default:
		_, _ = io.WriteString(ch, "hello"+nl)
		_, _ = io.WriteString(ch.Stderr(), "warn"+nl)
	}
	finishChannel(ch, code)
}

// --- harness ----------------------------------------------------------------

func testEndpoint(t *testing.T) ipc.Endpoint {
	t.Helper()
	unique := fmt.Sprintf("%d-%d", os.Getpid(), time.Now().UnixNano())
	if runtime.GOOS == "windows" {
		return ipc.FromAddr(`\\.\pipe\ai-ssh-test-` + unique)
	}
	return ipc.FromAddr(filepath.Join(t.TempDir(), "agent.sock"))
}

func writeKey(t *testing.T, path string, priv ed25519.PrivateKey) {
	t.Helper()
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	block := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	if err := os.WriteFile(path, block, 0o600); err != nil {
		t.Fatal(err)
	}
}

type harness struct {
	srv     *daemon.Server
	ep      ipc.Endpoint
	cfg     *config.Config
	workDir string
	remote  string
	keyPath string
	sshAddr string
	sshFP   string
	sshPub  ssh.PublicKey
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	return newHarnessCfg(t, harnessCfg{})
}

// newHarnessOpts builds a full stack. useKnownHosts exercises the production
// default (a known_hosts file) instead of a SHA256 pin.
func newHarnessOpts(t *testing.T, useKnownHosts bool) *harness {
	t.Helper()
	return newHarnessCfg(t, harnessCfg{useKnownHosts: useKnownHosts})
}

// harnessCfg tunes the stack under test.
type harnessCfg struct {
	useKnownHosts bool
	sudoPassword  string
	requireTTY    bool
	elevMode      string
	elevPassword  string // overrides sudoPassword for the *vault* side
	elevPTY       string
	elevDeny      []string
	elevAllow     []string
	nopasswd      bool
	always        bool
	approval      string
	approvalAfter string
	workdir       string
}

func quoteList(in []string) string {
	out := make([]string, len(in))
	for i, v := range in {
		out[i] = fmt.Sprintf("%q", v)
	}
	return strings.Join(out, ", ")
}

func newHarnessCfg(t *testing.T, hc harnessCfg) *harness {
	t.Helper()
	tmp := t.TempDir()

	_, clientPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	clientSigner, err := ssh.NewSignerFromKey(clientPriv)
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(tmp, "id_ed25519")
	writeKey(t, keyPath, clientPriv)

	sshSrv := startSSHServer(t, clientSigner.PublicKey(), &fakeOpts{
		sudoPassword: hc.sudoPassword,
		requireTTY:   hc.requireTTY,
	})

	work := filepath.Join(tmp, "work")
	remote := filepath.Join(tmp, "remote")
	for _, d := range []string{work, remote} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	hostKey := sshSrv.fp
	defaultsFields := []string{
		`"timeout": "10s"`,
		`"max_output": 64`,
		fmt.Sprintf(`"upload_roots": [%q]`, work),
		fmt.Sprintf(`"download_roots": [%q]`, work),
		fmt.Sprintf(`"audit_log": %q`, filepath.Join(tmp, "audit.log")),
	}
	if hc.useKnownHosts {
		khPath := filepath.Join(tmp, "known_hosts")
		host, port, err := net.SplitHostPort(sshSrv.addr)
		if err != nil {
			t.Fatal(err)
		}
		entry := fmt.Sprintf("[%s]:%s %s\n", host, port,
			strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshSrv.pub))))
		if err := os.WriteFile(khPath, []byte(entry), 0o600); err != nil {
			t.Fatal(err)
		}
		defaultsFields = append(defaultsFields, fmt.Sprintf(`"known_hosts": %q`, khPath))
		hostKey = "known_hosts"
	}
	if hc.approval != "" {
		defaultsFields = append(defaultsFields, fmt.Sprintf(`"approval": %q`, hc.approval))
	}
	if hc.approvalAfter != "" {
		defaultsFields = append(defaultsFields, fmt.Sprintf(`"approval_timeout": %q`, hc.approvalAfter))
	}

	hostFields := []string{
		fmt.Sprintf(`"addr": %q`, sshSrv.addr),
		`"user": "tester"`,
		fmt.Sprintf(`"auth": {"type": "key", "key": %q}`, keyPath),
		fmt.Sprintf(`"host_key": %q`, hostKey),
		fmt.Sprintf(`"workdir": %q`, hc.workdir),
		`"deny": ["^reboot"]`,
	}
	if hc.elevMode != "" {
		elevFields := []string{fmt.Sprintf(`"mode": %q`, hc.elevMode)}
		pw := hc.sudoPassword
		if hc.elevPassword != "" {
			pw = hc.elevPassword
		}
		// elevPassword == "-" means: leave the password out of the vault.
		if pw != "" && pw != "-" {
			elevFields = append(elevFields, fmt.Sprintf(`"password": %q`, pw))
		}
		if hc.nopasswd {
			elevFields = append(elevFields, `"nopasswd": true`)
		}
		if hc.elevPTY != "" {
			elevFields = append(elevFields, fmt.Sprintf(`"pty": %q`, hc.elevPTY))
		}
		if len(hc.elevDeny) > 0 {
			elevFields = append(elevFields, `"deny": [`+quoteList(hc.elevDeny)+`]`)
		}
		if len(hc.elevAllow) > 0 {
			elevFields = append(elevFields, `"allow": [`+quoteList(hc.elevAllow)+`]`)
		}
		if hc.always {
			elevFields = append(elevFields, `"always": true`)
		}
		hostFields = append(hostFields, `"elevate": {`+strings.Join(elevFields, ", ")+`}`)
	}

	cfgJSON := fmt.Sprintf("{\n  \"defaults\": {%s},\n  \"hosts\": {\n    \"local\": {%s}\n  }\n}",
		strings.Join(defaultsFields, ", "), strings.Join(hostFields, ", "))

	cfg, err := config.Parse([]byte(cfgJSON), tmp)
	if err != nil {
		t.Fatalf("config: %v", err)
	}

	ep := testEndpoint(t)
	srv, err := daemon.New(daemon.Options{
		Config:    cfg,
		Endpoint:  ep,
		VaultPath: filepath.Join(tmp, "vault"),
		AuditPath: filepath.Join(tmp, "audit.log"),
		Logger:    log.New(io.Discard, "", 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = srv.Serve(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		srv.Shutdown()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
		}
	})

	waitForDaemon(t, ep)

	return &harness{
		srv:     srv,
		ep:      ep,
		cfg:     cfg,
		sshPub:  sshSrv.pub,
		workDir: work,
		remote:  filepath.ToSlash(remote),
		keyPath: keyPath,
		sshAddr: sshSrv.addr,
		sshFP:   sshSrv.fp,
	}
}

func waitForDaemon(t *testing.T, ep ipc.Endpoint) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		conn, err := ep.Dial(ctx)
		cancel()
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("daemon never came up on %s", ep)
}

func (h *harness) call(t *testing.T, req proto.Request) proto.Response {
	t.Helper()
	return client.Call(context.Background(), h.ep, req)
}

// --- tests ------------------------------------------------------------------

func TestHostsHidesSecrets(t *testing.T) {
	h := newHarness(t)
	resp := h.call(t, proto.Request{Op: proto.OpHosts})
	if !resp.OK {
		t.Fatalf("hosts failed: %+v", resp.Error)
	}
	if len(resp.Hosts) != 1 || resp.Hosts[0].Alias != "local" {
		t.Fatalf("unexpected hosts: %+v", resp.Hosts)
	}
	if resp.Hosts[0].Addr != h.sshAddr {
		t.Fatalf("addr mismatch")
	}
}

func TestExecStreamsAndExitCode(t *testing.T) {
	h := newHarness(t)

	resp := h.call(t, proto.Request{Op: proto.OpExec, Host: "local", Command: []string{"echo", "hi"}})
	if !resp.OK || resp.Error != nil {
		t.Fatalf("exec failed: %+v", resp)
	}
	if resp.Stdout != "hello\n" {
		t.Fatalf("stdout = %q", resp.Stdout)
	}
	if resp.Stderr != "warn\n" {
		t.Fatalf("stderr = %q", resp.Stderr)
	}
	if resp.ExitCode == nil || *resp.ExitCode != 0 {
		t.Fatalf("exit code = %v", resp.ExitCode)
	}

	fail := h.call(t, proto.Request{Op: proto.OpExec, Host: "local", Command: []string{"please", "fail"}})
	if fail.Error != nil {
		t.Fatalf("unexpected error: %+v", fail.Error)
	}
	if fail.ExitCode == nil || *fail.ExitCode != 3 {
		t.Fatalf("exit code = %v, want 3", fail.ExitCode)
	}
	if fail.OK {
		t.Fatal("non-zero remote command must not be reported as ok")
	}
	if got := fail.ExitCodeFor(); got != proto.ExitRemote {
		t.Fatalf("process exit = %d, want %d", got, proto.ExitRemote)
	}
}

func TestExecTruncatesOutput(t *testing.T) {
	h := newHarness(t)
	resp := h.call(t, proto.Request{Op: proto.OpExec, Host: "local", Command: []string{"big"}})
	if resp.Error != nil {
		t.Fatalf("exec error: %+v", resp.Error)
	}
	if !resp.Truncated {
		t.Fatal("expected truncation")
	}
	if resp.StdoutBytes != 1<<20 {
		t.Fatalf("stdout_bytes = %d", resp.StdoutBytes)
	}
	if int64(len(resp.Stdout)) >= resp.StdoutBytes {
		t.Fatalf("stdout was not truncated (%d bytes)", len(resp.Stdout))
	}
	if !strings.Contains(resp.Stdout, "truncated") {
		t.Fatalf("missing truncation marker: %q", resp.Stdout)
	}
}

func TestExecDenyRule(t *testing.T) {
	h := newHarness(t)
	resp := h.call(t, proto.Request{Op: proto.OpExec, Host: "local", Command: []string{"reboot", "now"}})
	if resp.Error == nil || resp.Error.Code != proto.ErrBlocked {
		t.Fatalf("expected blocked, got %+v", resp)
	}
	if got := resp.ExitCodeFor(); got != proto.ExitBlocked {
		t.Fatalf("exit = %d", got)
	}
}

func TestExecUnknownHost(t *testing.T) {
	h := newHarness(t)
	resp := h.call(t, proto.Request{Op: proto.OpExec, Host: "nope", Command: []string{"true"}})
	if resp.Error == nil || resp.Error.Code != proto.ErrUnknownHost {
		t.Fatalf("expected unknown_host, got %+v", resp)
	}
}

func TestUploadDownloadRoundTrip(t *testing.T) {
	h := newHarness(t)

	local := filepath.Join(h.workDir, "payload.txt")
	content := []byte("the quick brown fox\n")
	if err := os.WriteFile(local, content, 0o644); err != nil {
		t.Fatal(err)
	}
	remotePath := h.remote + "/sub/payload.txt"

	up := h.call(t, proto.Request{
		Op: proto.OpUpload, Host: "local",
		Local: local, Remote: remotePath, Mkdir: true, Verify: true,
	})
	if !up.OK || up.Error != nil {
		t.Fatalf("upload failed: %+v", up.Error)
	}
	if up.Bytes != int64(len(content)) || up.Sha256 == "" {
		t.Fatalf("upload result: %+v", up)
	}

	onDisk := filepath.Join(filepath.FromSlash(h.remote), "sub", "payload.txt")
	got, err := os.ReadFile(onDisk)
	if err != nil {
		t.Fatalf("remote file missing: %v", err)
	}
	if string(got) != string(content) {
		t.Fatalf("remote content = %q", got)
	}

	back := filepath.Join(h.workDir, "back.txt")
	down := h.call(t, proto.Request{
		Op: proto.OpDownload, Host: "local",
		Remote: remotePath, Local: back, Verify: true,
	})
	if !down.OK || down.Error != nil {
		t.Fatalf("download failed: %+v", down.Error)
	}
	gotBack, err := os.ReadFile(back)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotBack) != string(content) {
		t.Fatalf("downloaded content = %q", gotBack)
	}
	if down.Sha256 != up.Sha256 {
		t.Fatalf("checksum mismatch %s != %s", down.Sha256, up.Sha256)
	}
}

func TestUploadRefusesFilesOutsideRoots(t *testing.T) {
	h := newHarness(t)

	// The private key lives outside upload_roots: an agent must not be able to
	// launder arbitrary local files through the remote host.
	resp := h.call(t, proto.Request{
		Op: proto.OpUpload, Host: "local",
		Local: h.keyPath, Remote: h.remote + "/leak.bin",
	})
	if resp.Error == nil || resp.Error.Code != proto.ErrPathDenied {
		t.Fatalf("expected path_denied, got %+v", resp)
	}
	if got := resp.ExitCodeFor(); got != proto.ExitBlocked {
		t.Fatalf("exit = %d", got)
	}
}

func TestDownloadRefusesProtectedPaths(t *testing.T) {
	h := newHarness(t)
	resp := h.call(t, proto.Request{
		Op: proto.OpDownload, Host: "local",
		Remote: h.remote + "/anything", Local: h.keyPath,
	})
	if resp.Error == nil || resp.Error.Code != proto.ErrPathDenied {
		t.Fatalf("expected path_denied, got %+v", resp)
	}
}

func TestPing(t *testing.T) {
	h := newHarness(t)
	resp := h.call(t, proto.Request{Op: proto.OpPing, Host: "local"})
	if !resp.OK || resp.Error != nil {
		t.Fatalf("ping failed: %+v", resp.Error)
	}
}

func TestEndToEndWithKnownHosts(t *testing.T) {
	h := newHarnessOpts(t, true)
	resp := h.call(t, proto.Request{Op: proto.OpPing, Host: "local"})
	if !resp.OK || resp.Error != nil {
		t.Fatalf("ping over known_hosts failed: %+v", resp.Error)
	}
	out := h.call(t, proto.Request{Op: proto.OpExec, Host: "local", Command: []string{"echo", "hi"}})
	if out.Stdout != "hello\n" {
		t.Fatalf("stdout = %q", out.Stdout)
	}
}

func TestExecTimeout(t *testing.T) {
	h := newHarness(t)
	start := time.Now()
	resp := h.call(t, proto.Request{
		Op: proto.OpExec, Host: "local", Command: []string{"sleep"}, Timeout: 1,
	})
	if resp.Error == nil || resp.Error.Code != proto.ErrTimeout {
		t.Fatalf("expected timeout, got %+v", resp)
	}
	if got := resp.ExitCodeFor(); got != proto.ExitTimeout {
		t.Fatalf("exit = %d, want %d", got, proto.ExitTimeout)
	}
	if elapsed := time.Since(start); elapsed > 6*time.Second {
		t.Fatalf("timeout took %s", elapsed)
	}
}

// --- elevation ---------------------------------------------------------------

func TestSudoWithPassword(t *testing.T) {
	const pw = "s3cret-sudo-pw"
	h := newHarnessCfg(t, harnessCfg{elevMode: "sudo", sudoPassword: pw})

	resp := h.call(t, proto.Request{
		Op: proto.OpExec, Host: "local", Command: []string{"echo", "hi"}, Elevate: true,
	})
	if resp.Error != nil {
		t.Fatalf("sudo exec failed: %+v", resp.Error)
	}
	if !resp.OK || !resp.Elevated || resp.ElevateMode != "sudo" {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if resp.Stdout != "hello\n" || resp.Stderr != "warn\n" {
		t.Fatalf("streams: %q / %q", resp.Stdout, resp.Stderr)
	}
	if resp.Merged {
		t.Fatal("plain sudo must not need a pty")
	}
	// The fake sudo fails with PASSWORD_IN_ARGV if the secret ever shows up in
	// the command line, and the daemon must never echo it back.
	for _, s := range []string{resp.Stdout, resp.Stderr} {
		if strings.Contains(s, pw) || strings.Contains(s, "PASSWORD_IN_ARGV") {
			t.Fatalf("password leaked: %q", s)
		}
	}
}

func TestSudoWrongPassword(t *testing.T) {
	h := newHarnessCfg(t, harnessCfg{
		elevMode:     "sudo",
		sudoPassword: "the-right-one",
		elevPassword: "the-wrong-one",
	})
	resp := h.call(t, proto.Request{
		Op: proto.OpExec, Host: "local", Command: []string{"echo", "hi"}, Elevate: true,
	})
	if resp.Error == nil || resp.Error.Code != proto.ErrElevation {
		t.Fatalf("expected elevation_failed, got %+v", resp)
	}
	if !strings.Contains(resp.Error.Message, "rejected") {
		t.Fatalf("unhelpful message: %q", resp.Error.Message)
	}
	if got := resp.ExitCodeFor(); got != proto.ExitConnect {
		t.Fatalf("exit = %d, want %d", got, proto.ExitConnect)
	}
}

func TestSudoNeedsPasswordButNoneConfigured(t *testing.T) {
	h := newHarnessCfg(t, harnessCfg{
		elevMode: "sudo", sudoPassword: "whatever", elevPassword: "-",
	})
	resp := h.call(t, proto.Request{
		Op: proto.OpExec, Host: "local", Command: []string{"echo", "hi"}, Elevate: true,
	})
	// No elevate.password => daemon uses `sudo -n`, which the fake rejects.
	if resp.Error == nil || resp.Error.Code != proto.ErrElevation {
		t.Fatalf("expected elevation_failed, got %+v", resp)
	}
	if !strings.Contains(resp.Error.Message, "elevate.password") {
		t.Fatalf("message should point at elevate.password: %q", resp.Error.Message)
	}
}

func TestSudoDeniedWithoutPolicy(t *testing.T) {
	h := newHarness(t)
	resp := h.call(t, proto.Request{
		Op: proto.OpExec, Host: "local", Command: []string{"echo", "hi"}, Elevate: true,
	})
	if resp.Error == nil || resp.Error.Code != proto.ErrElevDenied {
		t.Fatalf("expected elevation_denied, got %+v", resp)
	}
	if got := resp.ExitCodeFor(); got != proto.ExitBlocked {
		t.Fatalf("exit = %d, want %d", got, proto.ExitBlocked)
	}
}

func TestSudoRequireTTYFallsBackToPTY(t *testing.T) {
	const pw = "pty-pw"
	h := newHarnessCfg(t, harnessCfg{
		elevMode: "sudo", sudoPassword: pw, requireTTY: true, elevPTY: "auto",
	})
	resp := h.call(t, proto.Request{
		Op: proto.OpExec, Host: "local", Command: []string{"echo", "hi"}, Elevate: true,
	})
	if resp.Error != nil {
		t.Fatalf("retry with pty failed: %+v", resp.Error)
	}
	if !resp.OK || !resp.Merged {
		t.Fatalf("expected merged pty output, got %+v", resp)
	}
	if resp.Stderr != "" {
		t.Fatalf("stderr should be empty when merged, got %q", resp.Stderr)
	}
	if !strings.HasPrefix(resp.Stdout, "hello\n") {
		t.Fatalf("stdout = %q", resp.Stdout)
	}
	if strings.Contains(resp.Stdout, "\r") {
		t.Fatalf("CRLF not normalised: %q", resp.Stdout)
	}
	if strings.Contains(resp.Stdout, pw) {
		t.Fatal("password echoed by the terminal")
	}
}

func TestSudoAllowListBlocksOtherCommands(t *testing.T) {
	h := newHarnessCfg(t, harnessCfg{
		elevMode: "sudo", sudoPassword: "pw", elevAllow: []string{"^systemctl "},
	})
	blocked := h.call(t, proto.Request{
		Op: proto.OpExec, Host: "local", Command: []string{"cat", "/etc/shadow"}, Elevate: true,
	})
	if blocked.Error == nil || blocked.Error.Code != proto.ErrElevDenied {
		t.Fatalf("expected elevation_denied, got %+v", blocked)
	}
	allowed := h.call(t, proto.Request{
		Op: proto.OpExec, Host: "local", Command: []string{"systemctl", "status", "nginx"}, Elevate: true,
	})
	if allowed.Error != nil || !allowed.OK {
		t.Fatalf("allow-listed command failed: %+v", allowed)
	}
}

func TestSuUsesPTY(t *testing.T) {
	const pw = "su-pw"
	h := newHarnessCfg(t, harnessCfg{elevMode: "su", sudoPassword: pw})
	resp := h.call(t, proto.Request{
		Op: proto.OpExec, Host: "local", Command: []string{"echo", "hi"}, Elevate: true,
	})
	if resp.Error != nil {
		t.Fatalf("su failed: %+v", resp.Error)
	}
	if !resp.OK || !resp.Merged || resp.ElevateMode != "su" {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if !strings.HasPrefix(resp.Stdout, "hello\n") {
		t.Fatalf("stdout = %q", resp.Stdout)
	}
}

func TestElevateAlwaysSkipsFlag(t *testing.T) {
	h := newHarnessCfg(t, harnessCfg{elevMode: "sudo", sudoPassword: "pw", always: true})
	resp := h.call(t, proto.Request{
		Op: proto.OpExec, Host: "local", Command: []string{"echo", "hi"},
	})
	if !resp.OK || !resp.Elevated {
		t.Fatalf("expected automatic elevation: %+v", resp)
	}
}

// --- approval channel --------------------------------------------------------

// uiClient is a stand-in for the GUI: it attaches to the daemon and answers
// approval requests.
type uiClient struct {
	conn net.Conn
	dec  *json.Decoder
}

func attachUI(t *testing.T, ep ipc.Endpoint) *uiClient {
	return attachRole(t, ep, "")
}

func attachRole(t *testing.T, ep ipc.Endpoint, role string) *uiClient {
	t.Helper()
	conn, err := ep.Dial(context.Background())
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	if err := json.NewEncoder(conn).Encode(proto.Request{Op: proto.OpAttach, Role: role}); err != nil {
		t.Fatalf("attach encode: %v", err)
	}
	dec := json.NewDecoder(conn)
	var ack proto.Response
	if err := dec.Decode(&ack); err != nil || !ack.OK {
		t.Fatalf("attach ack: %v %+v", err, ack)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return &uiClient{conn: conn, dec: dec}
}

func (u *uiClient) nextEvent(t *testing.T) proto.Event {
	t.Helper()
	_ = u.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var ev proto.Event
	if err := u.dec.Decode(&ev); err != nil {
		t.Fatalf("read event: %v", err)
	}
	return ev
}

func (u *uiClient) decide(t *testing.T, id string, allow bool) {
	t.Helper()
	err := json.NewEncoder(u.conn).Encode(proto.Decision{Op: proto.OpDecide, ID: id, Allow: allow})
	if err != nil {
		t.Fatalf("decide: %v", err)
	}
}

func TestApprovalAllowsCommand(t *testing.T) {
	h := newHarnessCfg(t, harnessCfg{approval: "always"})
	ui := attachUI(t, h.ep)

	done := make(chan proto.Response, 1)
	go func() {
		done <- h.call(t, proto.Request{Op: proto.OpExec, Host: "local", Command: []string{"echo", "hi"}})
	}()

	ev := ui.nextEvent(t)
	if ev.Type != proto.EventApprovalRequest || ev.Approval == nil {
		t.Fatalf("expected an approval request, got %+v", ev)
	}
	if ev.Approval.Host != "local" || ev.Approval.Command != "echo hi" || ev.Approval.Op != proto.OpExec {
		t.Fatalf("approval payload = %+v", ev.Approval)
	}
	if ev.Approval.TimeoutS <= 0 {
		t.Fatalf("timeout not advertised: %+v", ev.Approval)
	}

	ui.decide(t, ev.Approval.ID, true)

	select {
	case resp := <-done:
		if resp.Error != nil {
			t.Fatalf("exec failed: %+v", resp.Error)
		}
		if resp.Approval != "approved" || !resp.OK || resp.Stdout != "hello\n" {
			t.Fatalf("response = %+v", resp)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the exec never returned after approval")
	}

	closed := ui.nextEvent(t)
	if closed.Type != proto.EventApprovalClosed || closed.ID != ev.Approval.ID {
		t.Fatalf("expected the approval to close, got %+v", closed)
	}
}

func TestApprovalDeniesCommand(t *testing.T) {
	h := newHarnessCfg(t, harnessCfg{approval: "always"})
	ui := attachUI(t, h.ep)

	done := make(chan proto.Response, 1)
	go func() {
		done <- h.call(t, proto.Request{Op: proto.OpExec, Host: "local", Command: []string{"echo", "hi"}})
	}()

	ev := ui.nextEvent(t)
	ui.decide(t, ev.Approval.ID, false)

	select {
	case resp := <-done:
		if resp.Error == nil || resp.Error.Code != proto.ErrApprovalDenied {
			t.Fatalf("expected approval_denied, got %+v", resp)
		}
		if resp.Approval != "denied" {
			t.Fatalf("approval label = %q", resp.Approval)
		}
		if got := resp.ExitCodeFor(); got != proto.ExitBlocked {
			t.Fatalf("exit = %d", got)
		}
		if resp.Stdout != "" {
			t.Fatalf("a denied command must not produce output: %q", resp.Stdout)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the exec never returned after denial")
	}
}

func TestApprovalWithoutFrontEndIsRefused(t *testing.T) {
	h := newHarnessCfg(t, harnessCfg{approval: "always"})

	start := time.Now()
	resp := h.call(t, proto.Request{Op: proto.OpExec, Host: "local", Command: []string{"echo", "hi"}})
	if resp.Error == nil || resp.Error.Code != proto.ErrApprovalUnavailable {
		t.Fatalf("expected approval_unavailable, got %+v", resp)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("refusing an unattended approval took %s", elapsed)
	}
}

func TestApprovalTimeout(t *testing.T) {
	h := newHarnessCfg(t, harnessCfg{approval: "always", approvalAfter: "1s"})
	ui := attachUI(t, h.ep)

	start := time.Now()
	resp := h.call(t, proto.Request{Op: proto.OpExec, Host: "local", Command: []string{"echo", "hi"}})
	if resp.Error == nil || resp.Error.Code != proto.ErrApprovalTimeout {
		t.Fatalf("expected approval_timeout, got %+v", resp)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("timeout took %s", elapsed)
	}
	// The request was broadcast even though nobody answered.
	ev := ui.nextEvent(t)
	if ev.Type != proto.EventApprovalRequest {
		t.Fatalf("event = %+v", ev)
	}
}

func TestApprovalElevateOnly(t *testing.T) {
	h := newHarnessCfg(t, harnessCfg{
		approval: "elevate", elevMode: "sudo", sudoPassword: "pw",
	})

	// A plain command is not gated.
	plain := h.call(t, proto.Request{Op: proto.OpExec, Host: "local", Command: []string{"echo", "hi"}})
	if plain.Error != nil {
		t.Fatalf("plain exec should not need approval: %+v", plain.Error)
	}

	// An elevated one is, and with no front end it is refused.
	elev := h.call(t, proto.Request{
		Op: proto.OpExec, Host: "local", Command: []string{"echo", "hi"}, Elevate: true,
	})
	if elev.Error == nil || elev.Error.Code != proto.ErrApprovalUnavailable {
		t.Fatalf("elevated exec should require approval, got %+v", elev)
	}
}

func TestApprovalForUpload(t *testing.T) {
	h := newHarnessCfg(t, harnessCfg{approval: "always"})
	ui := attachUI(t, h.ep)

	local := filepath.Join(h.workDir, "a.txt")
	if err := os.WriteFile(local, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	done := make(chan proto.Response, 1)
	go func() {
		done <- h.call(t, proto.Request{
			Op: proto.OpUpload, Host: "local", Local: local, Remote: h.remote + "/a.txt",
		})
	}()

	ev := ui.nextEvent(t)
	if ev.Approval == nil || ev.Approval.Op != proto.OpUpload || ev.Approval.Remote == "" {
		t.Fatalf("upload approval = %+v", ev.Approval)
	}
	ui.decide(t, ev.Approval.ID, true)

	if resp := <-done; resp.Error != nil || resp.Approval != "approved" {
		t.Fatalf("upload after approval = %+v", resp)
	}
}

func TestApprovalFrontEndDetachDenies(t *testing.T) {
	h := newHarnessCfg(t, harnessCfg{approval: "always"})
	ui := attachUI(t, h.ep)

	done := make(chan proto.Response, 1)
	go func() {
		done <- h.call(t, proto.Request{Op: proto.OpExec, Host: "local", Command: []string{"echo", "hi"}})
	}()

	ev := ui.nextEvent(t)
	if ev.Type != proto.EventApprovalRequest {
		t.Fatalf("event = %+v", ev)
	}

	// Closing the window (or locking) must not leave the agent hanging.
	_ = ui.conn.Close()

	select {
	case resp := <-done:
		if resp.Error == nil || resp.Error.Code != proto.ErrApprovalDenied {
			t.Fatalf("expected a prompt denial after detach, got %+v", resp)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the exec kept waiting after the front end went away")
	}
}

// --- interactive sessions (human facing, in-process only) ---------------------

func TestOpenShellEchoesAndResizes(t *testing.T) {
	h := newHarness(t)

	sh, err := h.srv.OpenShell(context.Background(), "local", 80, 24)
	if err != nil {
		t.Fatalf("open shell: %v", err)
	}
	defer sh.Close()

	if _, err := sh.Write([]byte("hello\n")); err != nil {
		t.Fatalf("write: %v", err)
	}

	got := make([]byte, 6)
	if _, err := io.ReadFull(sh, got); err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != "hello\n" {
		t.Fatalf("echo = %q", got)
	}

	if err := sh.Resize(100, 40); err != nil {
		t.Fatalf("resize: %v", err)
	}
	if err := sh.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	// Closing twice must be harmless: the tab and the lock both do it.
	if err := sh.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
}

func TestOpenShellUnknownHost(t *testing.T) {
	h := newHarness(t)
	if _, err := h.srv.OpenShell(context.Background(), "nope", 80, 24); err == nil {
		t.Fatal("expected an error for an unknown host")
	}
}

// --- SFTP file browser (human facing, in-process only) -----------------------

func TestFilesBrowseUploadDownload(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	f, err := h.srv.OpenFiles(ctx, "local")
	if err != nil {
		t.Fatalf("open files: %v", err)
	}
	defer f.Close()

	// The fake SFTP server serves this process's filesystem, so a temp dir is
	// a valid "remote" root for the test.
	root := filepath.ToSlash(t.TempDir())
	if err := f.Mkdir(root + "/sub"); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	entries, err := f.List(root)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(entries) != 1 || !entries[0].Dir || entries[0].Name != "sub" {
		t.Fatalf("entries = %+v", entries)
	}

	src := filepath.Join(t.TempDir(), "payload.txt")
	body := []byte("hello sftp\n")
	if err := os.WriteFile(src, body, 0o640); err != nil {
		t.Fatal(err)
	}

	var lastDone int64
	n, err := f.Upload(ctx, src, root+"/sub/payload.txt", daemon.Transfer{
		Progress: func(done, total int64) {
			lastDone = done
			if total != int64(len(body)) {
				t.Errorf("progress total = %d", total)
			}
		},
	})
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if n != int64(len(body)) || lastDone != n {
		t.Fatalf("uploaded %d, progress %d", n, lastDone)
	}

	list, err := f.List(root + "/sub")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Name != "payload.txt" || list[0].Dir || list[0].Size != int64(len(body)) {
		t.Fatalf("listing = %+v", list)
	}

	if err := f.Rename(root+"/sub/payload.txt", root+"/sub/renamed.txt"); err != nil {
		t.Fatalf("rename: %v", err)
	}

	dst := filepath.Join(t.TempDir(), "back.txt")
	got, err := f.Download(ctx, root+"/sub/renamed.txt", dst, daemon.Transfer{})
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	if got != int64(len(body)) {
		t.Fatalf("downloaded %d bytes", got)
	}
	back, err := os.ReadFile(dst)
	if err != nil || string(back) != string(body) {
		t.Fatalf("downloaded content = %q (%v)", back, err)
	}

	if err := f.Remove(root, true); err != nil {
		t.Fatalf("remove tree: %v", err)
	}
	if _, err := os.Stat(filepath.FromSlash(root)); !os.IsNotExist(err) {
		t.Fatalf("the tree survived: %v", err)
	}
}

func TestFilesHomeUsesWorkdir(t *testing.T) {
	h := newHarnessCfg(t, harnessCfg{workdir: "/srv/app"})
	f, err := h.srv.OpenFiles(context.Background(), "local")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if got := f.Home(); got != "/srv/app" {
		t.Fatalf("home = %q", got)
	}
}

func TestFilesUnknownHost(t *testing.T) {
	h := newHarness(t)
	if _, err := h.srv.OpenFiles(context.Background(), "nope"); err == nil {
		t.Fatal("expected an error for an unknown host")
	}
}

func TestFilesResumeTransfer(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	f, err := h.srv.OpenFiles(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	base := t.TempDir()
	root := filepath.ToSlash(base)

	body := []byte("hello world, this is a resumed file\n")
	src := filepath.Join(t.TempDir(), "src.txt")
	if err := os.WriteFile(src, body, 0o644); err != nil {
		t.Fatal(err)
	}

	// --- upload resumes from the remote size ---
	remotePath := root + "/up.txt"
	if err := os.WriteFile(filepath.FromSlash(remotePath), body[:10], 0o644); err != nil {
		t.Fatal(err)
	}

	var firstDone, lastTotal int64
	n, err := f.Upload(ctx, src, remotePath, daemon.Transfer{
		Resume: true,
		Progress: func(done, total int64) {
			if firstDone == 0 {
				firstDone = done
			}
			lastTotal = total
		},
	})
	if err != nil {
		t.Fatalf("resumed upload: %v", err)
	}
	if firstDone != 10 {
		t.Fatalf("resume did not start at the remote size: first progress = %d", firstDone)
	}
	if lastTotal != int64(len(body)) {
		t.Fatalf("total = %d", lastTotal)
	}
	if n != int64(len(body)) {
		t.Fatalf("reported %d bytes, want the final size %d", n, len(body))
	}
	got, err := os.ReadFile(filepath.FromSlash(remotePath))
	if err != nil || string(got) != string(body) {
		t.Fatalf("resumed upload content = %q (%v)", got, err)
	}

	// Without Resume the file is rewritten from the start.
	if _, err := f.Upload(ctx, src, remotePath, daemon.Transfer{}); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.FromSlash(remotePath)); string(got) != string(body) {
		t.Fatalf("plain upload content = %q", got)
	}

	// --- download resumes from the local size ---
	remoteDown := root + "/down.txt"
	if err := os.WriteFile(filepath.FromSlash(remoteDown), body, 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "dst.txt")
	if err := os.WriteFile(dst, body[:7], 0o644); err != nil {
		t.Fatal(err)
	}

	firstDone = 0
	n, err = f.Download(ctx, remoteDown, dst, daemon.Transfer{
		Resume: true,
		Progress: func(done, total int64) {
			if firstDone == 0 {
				firstDone = done
			}
		},
	})
	if err != nil {
		t.Fatalf("resumed download: %v", err)
	}
	if firstDone != 7 || n != int64(len(body)) {
		t.Fatalf("resumed download: first=%d final=%d", firstDone, n)
	}
	back, err := os.ReadFile(dst)
	if err != nil || string(back) != string(body) {
		t.Fatalf("resumed download content = %q (%v)", back, err)
	}
}

// --- port forwarding (human facing, in-process only) -------------------------

func echoServer(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				_, _ = io.Copy(c, c)
			}()
		}
	}()
	return ln.Addr().String()
}

func TestLocalForward(t *testing.T) {
	h := newHarness(t)
	echo := echoServer(t)

	fwd, err := h.srv.StartForward(context.Background(), "local", daemon.ForwardLocal, "127.0.0.1:0", echo)
	if err != nil {
		t.Fatalf("start forward: %v", err)
	}
	defer fwd.Close()

	conn, err := net.Dial("tcp", fwd.Listen())
	if err != nil {
		t.Fatalf("dial forward: %v", err)
	}
	defer conn.Close()

	msg := []byte("through the tunnel")
	if _, err := conn.Write(msg); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, len(msg))
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(buf) != string(msg) {
		t.Fatalf("echo = %q", buf)
	}
}

func TestDynamicForwardSOCKS5(t *testing.T) {
	h := newHarness(t)
	echo := echoServer(t)

	fwd, err := h.srv.StartForward(context.Background(), "local", daemon.ForwardDynamic, "127.0.0.1:0", "")
	if err != nil {
		t.Fatalf("start forward: %v", err)
	}
	defer fwd.Close()

	conn, err := net.Dial("tcp", fwd.Listen())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))

	// Greeting: version 5, one method (no auth).
	if _, err := conn.Write([]byte{5, 1, 0}); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, 2)
	if _, err := io.ReadFull(conn, reply); err != nil {
		t.Fatal(err)
	}
	if reply[0] != 5 || reply[1] != 0 {
		t.Fatalf("greeting reply = %v", reply)
	}

	host, portStr, _ := net.SplitHostPort(echo)
	port, _ := strconv.Atoi(portStr)
	req := []byte{5, 1, 0, 1}
	req = append(req, net.ParseIP(host).To4()...)
	req = append(req, byte(port>>8), byte(port))
	if _, err := conn.Write(req); err != nil {
		t.Fatal(err)
	}
	head := make([]byte, 10)
	if _, err := io.ReadFull(conn, head); err != nil {
		t.Fatal(err)
	}
	if head[0] != 5 || head[1] != 0 {
		t.Fatalf("connect reply = %v", head)
	}

	msg := []byte("socks payload")
	if _, err := conn.Write(msg); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, len(msg))
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(buf) != string(msg) {
		t.Fatalf("echo = %q", buf)
	}
}

func TestStartForwardValidation(t *testing.T) {
	h := newHarness(t)
	if _, err := h.srv.StartForward(context.Background(), "local", daemon.ForwardLocal, "127.0.0.1:0", ""); err == nil {
		t.Fatal("a local forward without a target must be refused")
	}
	if _, err := h.srv.StartForward(context.Background(), "local", "bogus", "127.0.0.1:0", "x:1"); err == nil {
		t.Fatal("an unknown kind must be refused")
	}
	if _, err := h.srv.StartForward(context.Background(), "nope", daemon.ForwardLocal, "127.0.0.1:0", "x:1"); err == nil {
		t.Fatal("an unknown host must be refused")
	}
}

func TestRunHuman(t *testing.T) {
	h := newHarness(t)

	out, _, err := h.srv.RunHuman(context.Background(), "local", "uptime", 5*time.Second)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if out != "hello\n" {
		t.Fatalf("output = %q", out)
	}
	if _, _, err := h.srv.RunHuman(context.Background(), "nope", "uptime", 5*time.Second); err == nil {
		t.Fatal("an unknown host must be refused")
	}
}

func TestFilesDirectoryTransfer(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	f, err := h.srv.OpenFiles(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	src := t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, "a", "b"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := map[string]string{
		"top.txt":      "top",
		"a/mid.txt":    "mid",
		"a/b/deep.txt": "deep",
	}
	for rel, content := range body {
		if err := os.WriteFile(filepath.Join(src, filepath.FromSlash(rel)), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	remote := filepath.ToSlash(filepath.Join(t.TempDir(), "uploaded"))
	var lastDone, lastTotal int64
	files, n, err := f.UploadDir(ctx, src, remote, daemon.Transfer{
		Progress: func(done, total int64) { lastDone, lastTotal = done, total },
	})
	if err != nil {
		t.Fatalf("upload dir: %v", err)
	}
	if files != 3 || n != 10 {
		t.Fatalf("uploaded %d files, %d bytes", files, n)
	}
	if lastDone != lastTotal || lastTotal != 10 {
		t.Fatalf("progress %d/%d", lastDone, lastTotal)
	}
	for rel, content := range body {
		got, err := os.ReadFile(filepath.Join(filepath.FromSlash(remote), filepath.FromSlash(rel)))
		if err != nil || string(got) != content {
			t.Fatalf("%s = %q (%v)", rel, got, err)
		}
	}

	dst := filepath.Join(t.TempDir(), "back")
	files, n, err = f.DownloadDir(ctx, remote, dst, daemon.Transfer{})
	if err != nil {
		t.Fatalf("download dir: %v", err)
	}
	if files != 3 || n != 10 {
		t.Fatalf("downloaded %d files, %d bytes", files, n)
	}
	for rel, content := range body {
		got, err := os.ReadFile(filepath.Join(dst, filepath.FromSlash(rel)))
		if err != nil || string(got) != content {
			t.Fatalf("back %s = %q (%v)", rel, got, err)
		}
	}
}

// The on-demand agent attaches as a front end too. A desktop window must win:
// the human is already looking at it, and two dialogs for one request would be
// confusing.
func TestApprovalPrefersTheDesktopWindow(t *testing.T) {
	h := newHarnessCfg(t, harnessCfg{approval: "always"})
	agentUI := attachRole(t, h.ep, proto.RoleAgent)
	gui := attachRole(t, h.ep, proto.RoleGUI)

	done := make(chan proto.Response, 1)
	go func() {
		done <- h.call(t, proto.Request{Op: proto.OpExec, Host: "local", Command: []string{"echo", "hi"}})
	}()

	ev := gui.nextEvent(t)
	if ev.Type != proto.EventApprovalRequest || ev.Approval == nil {
		t.Fatalf("the desktop window should get the request, got %+v", ev)
	}
	_ = agentUI.conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	var stray proto.Event
	if err := agentUI.dec.Decode(&stray); err == nil {
		t.Fatalf("the agent side must not draw a duplicate dialog: %+v", stray)
	}

	gui.decide(t, ev.Approval.ID, true)
	select {
	case resp := <-done:
		if resp.Error != nil || resp.Approval != "approved" {
			t.Fatalf("response = %+v", resp)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("exec never returned")
	}
	if closed := gui.nextEvent(t); closed.Type != proto.EventApprovalClosed {
		t.Fatalf("expected the close event, got %+v", closed)
	}
}

// Without a desktop window the on-demand agent is the front end, so approvals
// can still be answered on a machine that only has `ai-ssh mcp` running.
func TestApprovalReachesTheAgentFrontEnd(t *testing.T) {
	h := newHarnessCfg(t, harnessCfg{approval: "always"})
	agentUI := attachRole(t, h.ep, proto.RoleAgent)

	done := make(chan proto.Response, 1)
	go func() {
		done <- h.call(t, proto.Request{Op: proto.OpExec, Host: "local", Command: []string{"echo", "hi"}})
	}()

	ev := agentUI.nextEvent(t)
	if ev.Type != proto.EventApprovalRequest || ev.Approval == nil {
		t.Fatalf("expected an approval request, got %+v", ev)
	}
	agentUI.decide(t, ev.Approval.ID, false)
	select {
	case resp := <-done:
		if resp.Error == nil || resp.Error.Code != proto.ErrApprovalDenied {
			t.Fatalf("response = %+v", resp)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("exec never returned")
	}
}
