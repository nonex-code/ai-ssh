package daemon

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/nonex-code/ai-ssh/internal/config"
	"github.com/nonex-code/ai-ssh/internal/proto"
)

// dial opens an authenticated SSH connection. The returned client owns the
// underlying TCP connection and must be closed by the caller.
func (s *Server) dial(ctx context.Context, h *config.Host) (*ssh.Client, error) {
	auth, err := s.authMethods(h)
	if err != nil {
		return nil, err
	}
	hostKey, err := s.hostKeyCallback(h)
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

// UnknownHostKeyError is a first meeting: the host is not in known_hosts. A
// front end can show the fingerprint and, if the human agrees, pin it.
type UnknownHostKeyError struct {
	Host        string
	Fingerprint string
}

func (e *UnknownHostKeyError) Error() string {
	return fmt.Sprintf("%s is not in known_hosts (presented %s)", e.Host, e.Fingerprint)
}

func (s *Server) hostKeyCallback(h *config.Host) (ssh.HostKeyCallback, error) {
	mode := h.HostKey
	switch {
	case mode == "insecure":
		s.log.Printf("WARNING: host %q has host_key=insecure, verification is disabled", h.Alias)
		return ssh.InsecureIgnoreHostKey(), nil

	case strings.HasPrefix(mode, "SHA256:"):
		want := strings.TrimSpace(mode)
		return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
			got := ssh.FingerprintSHA256(key)
			if subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
				return &HostKeyMismatchError{Host: hostname, Got: got, Want: want}
			}
			return nil
		}, nil

	default:
		path := mode
		if path == "known_hosts" || path == "" {
			path = s.config().Defaults.KnownHosts
		}
		if _, err := os.Stat(path); err != nil {
			return nil, errf(proto.ErrHostKey,
				"known_hosts file %s is unavailable (%v); set host_key to a SHA256:... pin instead", path, err)
		}
		inner, err := knownhosts.New(path)
		if err != nil {
			return nil, errf(proto.ErrHostKey, "parse %s: %v", path, err)
		}
		return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
			err := inner(hostname, remote, key)
			if err == nil {
				return nil
			}
			var ke *knownhosts.KeyError
			if errors.As(err, &ke) {
				if len(ke.Want) == 0 {
					return &UnknownHostKeyError{Host: hostname, Fingerprint: ssh.FingerprintSHA256(key)}
				}
				return &HostKeyMismatchError{
					Host: hostname,
					Got:  ssh.FingerprintSHA256(key),
					Want: ssh.FingerprintSHA256(ke.Want[0].Key),
				}
			}
			return hostKeyError{err}
		}, nil
	}
}
