// Package client is everything an AI is allowed to see: it turns a high level
// intent into a proto.Request, ships it to the daemon, and prints the reply.
// It contains no credential handling whatsoever.
package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/nonex-code/ai-ssh/internal/ipc"
	"github.com/nonex-code/ai-ssh/internal/proto"
)

// Call sends one request to the daemon.
func Call(ctx context.Context, ep ipc.Endpoint, req proto.Request) proto.Response {
	resp, err := ipc.RoundTrip(ctx, ep, req)
	if err != nil {
		return proto.Response{
			OK:     false,
			Action: req.Op,
			Host:   req.Host,
			Error: &proto.Error{
				Code:    proto.ErrDaemonDown,
				Message: fmt.Sprintf("%v (endpoint %s)", err, ep),
				Host:    req.Host,
			},
		}
	}
	if resp.Action == "" {
		resp.Action = req.Op
	}
	if resp.Host == "" {
		resp.Host = req.Host
	}
	return resp
}

// Print emits a single JSON line, unescaped, so logs containing <>& stay
// readable. stdout carries data only; diagnostics go to stderr.
func Print(w io.Writer, resp proto.Response) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(resp)
}
