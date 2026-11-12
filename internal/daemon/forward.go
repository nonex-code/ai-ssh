package daemon

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/nonex-code/ai-ssh/internal/proto"
)

// Forward kinds.
const (
	ForwardLocal   = "local"   // -L: listen here, dial from the server
	ForwardRemote  = "remote"  // -R: listen on the server, dial here
	ForwardDynamic = "dynamic" // -D: a SOCKS5 proxy over the session
)

// Forward is one port forward owned by a window tab. Like Shell and Files it is
// not reachable through the request protocol: an AI cannot open a tunnel.
type Forward struct {
	srv    *Server
	host   string
	kind   string
	listen string
	target string

	client *ssh.Client
	ln     net.Listener
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu     sync.Mutex
	closed bool
}

// StartForward opens a tunnel. listen is host:port (the host defaults to
// 127.0.0.1); target is host:port for local and remote forwards, ignored by a
// dynamic one.
func (s *Server) StartForward(ctx context.Context, alias, kind, listen, target string) (*Forward, error) {
	h, err := s.config().Host(alias)
	if err != nil {
		return nil, errf(proto.ErrUnknownHost, "%v", err)
	}

	listen, err = normalizeListen(listen)
	if err != nil {
		return nil, errf(proto.ErrUsage, "%v", err)
	}
	switch kind {
	case ForwardLocal, ForwardRemote, ForwardDynamic:
	default:
		return nil, errf(proto.ErrUsage, "unknown forward kind %q", kind)
	}
	if kind != ForwardDynamic {
		if target == "" {
			return nil, errf(proto.ErrUsage, "a %s forward needs a target host:port", kind)
		}
	}

	client, err := s.dial(ctx, h)
	if err != nil {
		return nil, err
	}

	f := &Forward{srv: s, host: h.Alias, kind: kind, target: target, client: client}
	runCtx, cancel := context.WithCancel(context.Background())
	f.cancel = cancel

	switch kind {
	case ForwardRemote:
		ln, err := client.Listen("tcp", listen)
		if err != nil {
			_ = client.Close()
			cancel()
			return nil, errf(proto.ErrRemote, "remote listen %s: %v", listen, err)
		}
		f.ln = ln
		f.listen = ln.Addr().String()
	case ForwardLocal, ForwardDynamic:
		ln, err := net.Listen("tcp", listen)
		if err != nil {
			_ = client.Close()
			cancel()
			return nil, errf(proto.ErrConnect, "listen %s: %v", listen, err)
		}
		f.ln = ln
		f.listen = ln.Addr().String()
	}

	f.wg.Add(1)
	go f.acceptLoop(runCtx)

	s.audit.write(auditEntry{
		Time: time.Now().UTC().Format(time.RFC3339Nano),
		Op:   "forward", Host: h.Alias, Remote: kind + " " + f.listen + " → " + target, OK: true,
	})
	s.log.Printf("op=forward host=%q kind=%s listen=%s target=%s", h.Alias, kind, f.listen, target)
	return f, nil
}

func (f *Forward) Kind() string   { return f.kind }
func (f *Forward) Listen() string { return f.listen }
func (f *Forward) Target() string { return f.target }

func (f *Forward) acceptLoop(ctx context.Context) {
	defer f.wg.Done()
	for {
		conn, err := f.ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
			}
			// The listener was closed from under us.
			return
		}
		f.wg.Add(1)
		go func() {
			defer f.wg.Done()
			f.handle(ctx, conn)
		}()
	}
}

func (f *Forward) handle(ctx context.Context, conn net.Conn) {
	defer conn.Close()

	var upstream net.Conn
	var err error
	if f.kind == ForwardDynamic {
		upstream, err = f.socksConnect(conn)
		if err != nil {
			return
		}
	} else {
		upstream, err = f.client.Dial("tcp", f.target)
		if err != nil {
			return
		}
	}
	defer upstream.Close()

	proxy(ctx, conn, upstream)
}

// socksConnect performs the minimal SOCKS5 handshake (no auth, CONNECT only)
// and returns the tunnelled connection.
func (f *Forward) socksConnect(conn net.Conn) (net.Conn, error) {
	head := make([]byte, 2)
	if _, err := io.ReadFull(conn, head); err != nil {
		return nil, err
	}
	if head[0] != 5 {
		return nil, fmt.Errorf("socks: version %d", head[0])
	}
	methods := make([]byte, int(head[1]))
	if _, err := io.ReadFull(conn, methods); err != nil {
		return nil, err
	}
	if _, err := conn.Write([]byte{5, 0}); err != nil { // no authentication
		return nil, err
	}

	req := make([]byte, 4)
	if _, err := io.ReadFull(conn, req); err != nil {
		return nil, err
	}
	if req[0] != 5 || req[1] != 1 { // CONNECT only
		_, _ = conn.Write([]byte{5, 7, 0, 1, 0, 0, 0, 0, 0, 0})
		return nil, fmt.Errorf("socks: unsupported request %v", req)
	}

	var host string
	switch req[3] {
	case 1:
		b := make([]byte, 4)
		if _, err := io.ReadFull(conn, b); err != nil {
			return nil, err
		}
		host = net.IP(b).String()
	case 3:
		n := make([]byte, 1)
		if _, err := io.ReadFull(conn, n); err != nil {
			return nil, err
		}
		b := make([]byte, int(n[0]))
		if _, err := io.ReadFull(conn, b); err != nil {
			return nil, err
		}
		host = string(b)
	case 4:
		b := make([]byte, 16)
		if _, err := io.ReadFull(conn, b); err != nil {
			return nil, err
		}
		host = net.IP(b).String()
	default:
		_, _ = conn.Write([]byte{5, 8, 0, 1, 0, 0, 0, 0, 0, 0})
		return nil, fmt.Errorf("socks: address type %d", req[3])
	}

	portBuf := make([]byte, 2)
	if _, err := io.ReadFull(conn, portBuf); err != nil {
		return nil, err
	}
	target := net.JoinHostPort(host, fmt.Sprint(binary.BigEndian.Uint16(portBuf)))

	upstream, err := f.client.Dial("tcp", target)
	if err != nil {
		// 0x05: connection refused
		_, _ = conn.Write([]byte{5, 5, 0, 1, 0, 0, 0, 0, 0, 0})
		return nil, err
	}
	if _, err := conn.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0}); err != nil {
		upstream.Close()
		return nil, err
	}
	return upstream, nil
}

// proxy copies both ways until either side closes.
func proxy(ctx context.Context, a, b net.Conn) {
	done := make(chan struct{}, 2)
	cp := func(dst, src net.Conn) {
		_, _ = io.Copy(dst, src)
		if cw, ok := dst.(interface{ CloseWrite() error }); ok {
			_ = cw.CloseWrite()
		}
		done <- struct{}{}
	}
	go cp(a, b)
	go cp(b, a)

	select {
	case <-ctx.Done():
	case <-done:
	}
}

// Close stops the tunnel. Safe twice.
func (f *Forward) Close() error {
	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		return nil
	}
	f.closed = true
	f.mu.Unlock()

	if f.cancel != nil {
		f.cancel()
	}
	if f.ln != nil {
		_ = f.ln.Close()
	}
	if f.client != nil {
		_ = f.client.Close()
	}
	f.wg.Wait()

	if f.srv != nil {
		f.srv.audit.write(auditEntry{
			Time: time.Now().UTC().Format(time.RFC3339Nano),
			Op:   "forward", Host: f.host, Remote: f.kind + " " + f.listen + " stopped", OK: true,
		})
	}
	return nil
}

// normalizeListen accepts "8080" and "127.0.0.1:8080".
func normalizeListen(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", fmt.Errorf("listen address is required")
	}
	if _, _, err := net.SplitHostPort(s); err != nil {
		if strings.Contains(s, ":") {
			return "", fmt.Errorf("listen %q must be host:port", s)
		}
		return net.JoinHostPort("127.0.0.1", s), nil
	}
	host, port, _ := net.SplitHostPort(s)
	if host == "" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port), nil
}
