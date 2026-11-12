//go:build windows

package ipc

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/Microsoft/go-winio"
)

const defaultNetwork = "pipe"

// pipeSDDL grants full access to the pipe owner (the user running the daemon),
// SYSTEM and Administrators, and nothing to anybody else. A named pipe is not a
// filesystem object, so this is the only way to express the ACL.
const pipeSDDL = "D:P(A;;GA;;;OW)(A;;GA;;;SY)(A;;GA;;;BA)"

func defaultEndpoint() (Endpoint, error) {
	name := `\\.\pipe\ai-ssh-agent`
	if user := os.Getenv("USERNAME"); user != "" {
		name += "-" + sanitize(user)
	}
	return Endpoint{Addr: name}, nil
}

func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		default:
			return '_'
		}
	}, s)
}

// normalizePipe accepts the single-backslash form that shells and hand-edited
// config files often produce.
func normalizePipe(p string) string {
	lower := strings.ToLower(p)
	if strings.HasPrefix(lower, `\\.\pipe\`) {
		return p
	}
	if strings.HasPrefix(lower, `\.\pipe\`) {
		return `\` + p
	}
	return p
}

func listen(e Endpoint) (net.Listener, error) {
	e.Addr = normalizePipe(e.Addr)
	if !strings.HasPrefix(strings.ToLower(e.Addr), `\\.\pipe\`) {
		return nil, fmt.Errorf("windows endpoint must be a named pipe, got %q", e.Addr)
	}
	ln, err := winio.ListenPipe(e.Addr, &winio.PipeConfig{
		SecurityDescriptor: pipeSDDL,
		MessageMode:        false,
	})
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", e.Addr, err)
	}
	return ln, nil
}

func dial(ctx context.Context, e Endpoint) (net.Conn, error) {
	return winio.DialPipeContext(ctx, normalizePipe(e.Addr))
}
