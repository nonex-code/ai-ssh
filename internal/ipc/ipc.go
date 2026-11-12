// Package ipc is the platform-neutral transport between the ai-ssh client and
// the ai-ssh daemon. On Unix it is a 0600 UNIX socket, on Windows a named pipe
// with an explicit security descriptor.
package ipc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/nonex-code/ai-ssh/internal/proto"
)

// Endpoint identifies the local daemon.
type Endpoint struct {
	Network string `json:"network"` // "unix" or "pipe"
	Addr    string `json:"addr"`
}

func (e Endpoint) String() string { return e.Network + ":" + e.Addr }

// Default returns the endpoint the client and daemon agree on without any
// configuration file (an endpoint path is not a secret).
func Default() (Endpoint, error) {
	if v := os.Getenv("AISSH_ENDPOINT"); v != "" {
		return Endpoint{Network: networkFor(v), Addr: v}, nil
	}
	ep, err := defaultEndpoint()
	if err != nil {
		return Endpoint{}, err
	}
	ep.Network = networkFor(ep.Addr)
	return ep, nil
}

// FromAddr builds an endpoint from an explicit address, inferring the network.
func FromAddr(addr string) Endpoint {
	return Endpoint{Network: networkFor(addr), Addr: addr}
}

// Listen opens the daemon side of the endpoint.
func (e Endpoint) Listen() (net.Listener, error) {
	ln, err := listen(e)
	if err != nil {
		return nil, err
	}
	return ln, nil
}

// Dial connects to the daemon side of the endpoint.
func (e Endpoint) Dial(ctx context.Context) (net.Conn, error) {
	return dial(ctx, e)
}

// ErrNoDaemon is returned when nothing is listening on the endpoint.
var ErrNoDaemon = errors.New("ai-ssh daemon is not running")

// RoundTrip performs one stateless request/response exchange over a fresh
// connection. Simple beats clever here: no session state to desynchronise.
func RoundTrip(ctx context.Context, ep Endpoint, req proto.Request) (proto.Response, error) {
	conn, err := ep.Dial(ctx)
	if err != nil {
		return proto.Response{}, fmt.Errorf("%w: %v", ErrNoDaemon, err)
	}
	defer conn.Close()

	budget := 30 * time.Second
	if req.Timeout > 0 {
		budget += time.Duration(req.Timeout) * time.Second
	} else {
		budget += 10 * time.Minute
	}
	deadline := time.Now().Add(budget)
	_ = conn.SetDeadline(deadline)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		_ = conn.SetDeadline(d)
	}

	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return proto.Response{}, fmt.Errorf("send request: %w", err)
	}
	var resp proto.Response
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return proto.Response{}, fmt.Errorf("read response: %w", err)
	}
	return resp, nil
}

func networkFor(addr string) string {
	if addr == "" {
		return defaultNetwork
	}
	if len(addr) > 9 && addr[:9] == `\\.\pipe\` {
		return "pipe"
	}
	if string(addr[0]) == "/" {
		return "unix"
	}
	return defaultNetwork
}
