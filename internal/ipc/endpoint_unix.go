//go:build !windows

package ipc

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
)

const defaultNetwork = "unix"

func defaultEndpoint() (Endpoint, error) {
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		dir = os.TempDir()
	}
	base := filepath.Join(dir, fmt.Sprintf("ai-ssh-%d", os.Getuid()))
	return Endpoint{Addr: filepath.Join(base, "agent.sock")}, nil
}

func listen(e Endpoint) (net.Listener, error) {
	dir := filepath.Dir(e.Addr)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create runtime dir: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, fmt.Errorf("secure runtime dir: %w", err)
	}
	// Remove a stale socket left by an unclean shutdown.
	if fi, err := os.Lstat(e.Addr); err == nil {
		if fi.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("%s exists and is not a socket", e.Addr)
		}
		_ = os.Remove(e.Addr)
	}
	ln, err := net.Listen("unix", e.Addr)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(e.Addr, 0o600); err != nil {
		ln.Close()
		return nil, fmt.Errorf("secure socket: %w", err)
	}
	return ln, nil
}

func dial(ctx context.Context, e Endpoint) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, "unix", e.Addr)
}
