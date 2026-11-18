package daemon

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/nonex-code/ai-ssh/internal/config"
	"github.com/nonex-code/ai-ssh/internal/proto"
)

// dial opens an authenticated SSH connection. human says whether the caller can
// put a host key question in front of a person (the window's own calls) or
// whether it must trust an unpinned key on first use (anything over the socket).
// The returned client owns the underlying TCP connection and must be closed by
// the caller.
func (s *Server) dial(ctx context.Context, h *config.Host, human bool) (*ssh.Client, error) {
	auth, err := s.authMethods(h)
	if err != nil {
		return nil, err
	}
	hostKey, err := s.hostKeyCallback(h, human)
	if err != nil {
		return nil, err
	}

	cfg := &ssh.ClientConfig{
		User:            h.User,
		Auth:            auth,
		HostKeyCallback: hostKey,
		Timeout:         time.Duration(s.config().Defaults.ConnectTimeout),
	}

	d := net.Dialer{Timeout: time.Duration(s.config().Defaults.ConnectTimeout)}
	raw, err := d.DialContext(ctx, "tcp", h.Addr)
	if err != nil {
		return nil, errf(proto.ErrConnect, "connect %s: %v", h.Addr, err)
	}

	// Cancelling the context must tear down the socket, otherwise a hung
	// handshake outlives the command timeout.
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = raw.Close()
		case <-done:
		}
	}()

	conn, chans, reqs, err := ssh.NewClientConn(raw, h.Addr, cfg)
	if err != nil {
		_ = raw.Close()
		return nil, classifyHandshake(h, err)
	}
	return ssh.NewClient(conn, chans, reqs), nil
}

func classifyHandshake(h *config.Host, err error) error {
	var hk hostKeyError
	if errors.As(err, &hk) {
		return hk
	}
	var mm *HostKeyMismatchError
	if errors.As(err, &mm) {
		return mm
	}
	var unk *UnknownHostKeyError
	if errors.As(err, &unk) {
		return unk
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "unable to authenticate"),
		strings.Contains(msg, "no supported methods remain"):
		return errf(proto.ErrAuth, "%s: authentication failed for user %q: %v", h.Addr, h.User, err)
	case strings.Contains(msg, "i/o timeout"), errors.Is(err, context.DeadlineExceeded):
		return errf(proto.ErrTimeout, "%s: handshake timed out: %v", h.Addr, err)
	default:
		return errf(proto.ErrConnect, "%s: %v", h.Addr, err)
	}
}

func (s *Server) authMethods(h *config.Host) ([]ssh.AuthMethod, error) {
	switch h.Auth.Type {
	case "key":
		signer, err := loadSigner(h.Auth.Key, h.Auth.Passphrase)
		if err != nil {
			return nil, errf(proto.ErrConfig, "host %q: %v", h.Alias, err)
		}
		if h.Auth.Cert != "" {
			signer, err = loadCertSigner(h.Auth.Cert, signer)
			if err != nil {
				return nil, errf(proto.ErrConfig, "host %q: %v", h.Alias, err)
			}
		}
		return []ssh.AuthMethod{ssh.PublicKeys(signer)}, nil
	case "password":
		return []ssh.AuthMethod{ssh.Password(h.Auth.Password)}, nil
	default:
		return nil, errf(proto.ErrConfig, "host %q: unsupported auth type %q", h.Alias, h.Auth.Type)
	}
}

func loadSigner(path, passphrase string) (ssh.Signer, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read private key: %w", err)
	}
	if passphrase != "" {
		signer, err := ssh.ParsePrivateKeyWithPassphrase(b, []byte(passphrase))
		if err != nil {
			return nil, fmt.Errorf("parse encrypted private key: %w", err)
		}
		return signer, nil
	}
	signer, err := ssh.ParsePrivateKey(b)
	if err != nil {
		var missing *ssh.PassphraseMissingError
		if errors.As(err, &missing) {
			return nil, errors.New("private key is encrypted: set auth.passphrase (env: or file: indirection recommended)")
		}
		return nil, fmt.Errorf("parse private key: %w", err)
	}
	return signer, nil
}

func loadCertSigner(certPath string, key ssh.Signer) (ssh.Signer, error) {
	b, err := os.ReadFile(certPath)
	if err != nil {
		return nil, fmt.Errorf("read certificate: %w", err)
	}
	pub, _, _, _, err := ssh.ParseAuthorizedKey(b)
	if err != nil {
		return nil, fmt.Errorf("parse certificate: %w", err)
	}
	cert, ok := pub.(*ssh.Certificate)
	if !ok {
		return nil, errors.New("auth.cert is not an SSH certificate")
	}
	return ssh.NewCertSigner(cert, key)
}

// UnknownHostKeyError is a first meeting: the host presents a key that is not
// pinned anywhere yet. A front end can show the fingerprint and, if the human
// agrees, pin it into the host's host_key.
type UnknownHostKeyError struct {
	Host        string
	Fingerprint string
}

func (e *UnknownHostKeyError) Error() string {
	return fmt.Sprintf("first connection to %s: host key is not pinned yet (presented %s)", e.Host, e.Fingerprint)
}

// hostKeyCallback builds the verification policy for a host. The modes are the
// ones config documents; the important one is the default, TOFU: a host nobody
// has verified yet is either shown to the human at the window or, when no window
// is in the loop, trusted on first use and recorded. Either way a machine with
// no ~/.ssh and no known_hosts connects.
//
// human says whether the caller can ask a person (the window's own dialogs go
// through OpenShell, OpenFiles, StartForward, RunHuman and TestHost); anything
// that arrives over the socket cannot, and is an AI or a script.
func (s *Server) hostKeyCallback(h *config.Host, human bool) (ssh.HostKeyCallback, error) {
	mode := strings.TrimSpace(h.HostKey)
	unknown := s.firstMeeting(h, human)
	switch {
	case mode == config.HostKeyInsecure:
		s.log.Printf("WARNING: host %q has host_key=insecure, verification is disabled", h.Alias)
		return ssh.InsecureIgnoreHostKey(), nil

	case strings.HasPrefix(mode, "SHA256:"):
		return pinnedCallback(splitPins(mode)), nil

	case mode == "" || mode == config.HostKeyTOFU:
		return knownHostsCallback(nil, unknown), nil

	default:
		path := mode
		if path == config.HostKeyKnownHosts {
			path = s.config().Defaults.KnownHosts
		}
		if path == "" {
			return knownHostsCallback(nil, unknown), nil
		}
		if _, err := os.Stat(path); err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				return nil, errf(proto.ErrHostKey, "stat %s: %v", path, err)
			}
			// No catalogue yet: nothing is pinned, so this is a first meeting.
			return knownHostsCallback(nil, unknown), nil
		}
		inner, err := knownhosts.New(path)
		if err != nil {
			return nil, errf(proto.ErrHostKey, "parse %s: %v", path, err)
		}
		return knownHostsCallback(inner, unknown), nil
	}
}

// firstMeeting decides what an unpinned host means. With a human in the loop the
// connection stops and the window offers the fingerprint for pinning; without
// one the key is accepted on first use and recorded, otherwise an AI could never
// work on a machine nobody is sitting at.
func (s *Server) firstMeeting(h *config.Host, human bool) func(hostname string, key ssh.PublicKey) error {
	return func(hostname string, key ssh.PublicKey) error {
		fingerprint := ssh.FingerprintSHA256(key)
		if human {
			return &UnknownHostKeyError{Host: hostname, Fingerprint: fingerprint}
		}
		s.trustOnFirstUse(h, fingerprint)
		return nil
	}
}

// trustOnFirstUse records a key trusted on first use. The audit trail always
// gets a line, and a TOFU host keeps the pin in the catalogue so the next
// connection is verified instead of trusted again. A known_hosts host is left
// alone: that file belongs to OpenSSH, so the acceptance lasts one connection.
func (s *Server) trustOnFirstUse(h *config.Host, fingerprint string) {
	s.audit.write(auditEntry{
		Time: time.Now().UTC().Format(time.RFC3339Nano),
		Op:   "host_key_trust", Host: h.Alias, Detail: fingerprint, OK: true,
	})
	s.log.Printf("WARNING: host %q was trusted on first use (%s)", h.Alias, fingerprint)
	if mode := strings.TrimSpace(h.HostKey); mode != "" && mode != config.HostKeyTOFU {
		return
	}
	if s.pinHostKey == nil {
		return
	}
	if err := s.pinHostKey(h.Alias, fingerprint); err != nil {
		s.log.Printf("could not save the host key for %q (%v); the next connection trusts it again", h.Alias, err)
	}
}

// knownHostsCallback turns a known_hosts verifier into the structured errors a
// front end acts on. A nil inner verifier means "nothing pinned yet", which is
// what unknown handles.
func knownHostsCallback(inner ssh.HostKeyCallback, unknown func(string, ssh.PublicKey) error) ssh.HostKeyCallback {
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		if inner == nil {
			return unknown(hostname, key)
		}
		err := inner(hostname, remote, key)
		if err == nil {
			return nil
		}
		var ke *knownhosts.KeyError
		if errors.As(err, &ke) {
			if len(ke.Want) == 0 {
				return unknown(hostname, key)
			}
			return &HostKeyMismatchError{
				Host: hostname,
				Got:  ssh.FingerprintSHA256(key),
				Want: ssh.FingerprintSHA256(ke.Want[0].Key),
			}
		}
		return hostKeyError{err}
	}
}

// pinnedCallback accepts any of the pinned fingerprints. Several pins are kept
// because a host may present a different key type (ed25519, rsa) on different
// connections, and every type has its own fingerprint.
func pinnedCallback(pins []string) ssh.HostKeyCallback {
	return func(hostname string, _ net.Addr, key ssh.PublicKey) error {
		got := ssh.FingerprintSHA256(key)
		for _, want := range pins {
			if subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1 {
				return nil
			}
		}
		return &HostKeyMismatchError{Host: hostname, Got: got, Want: strings.Join(pins, ", ")}
	}
}

// splitPins parses a comma separated host_key pin list, dropping blanks.
func splitPins(v string) []string {
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
