// Package mcp exposes the ai-ssh agent over the Model Context Protocol.
//
// This process is deliberately dumb: it is spawned by the AI host, so it lives
// in the AI's trust domain and must never see a credential. It only translates
// MCP tool calls into the local IPC protocol and back.
//
// stdout is the protocol channel: every log line goes to stderr.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"

	"github.com/nonex-code/ai-ssh/internal/ipc"
)

// protocolVersion is what we advertise when the client does not name a version
// we know.
const protocolVersion = "2025-06-18"

var supportedVersions = map[string]bool{
	"2024-11-05": true,
	"2025-03-26": true,
	"2025-06-18": true,
}

// JSON-RPC error codes we actually use.
const (
	codeParseError     = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternalError  = -32603
)

// Config wires the server to a running agent.
type Config struct {
	// Endpoint is the daemon's socket. It is not a secret.
	Endpoint ipc.Endpoint
	// ExposeAddr includes server addresses in ssh_list_hosts. Off by default:
	// the AI's context may be sent to a third party model provider.
	ExposeAddr bool
	// Ensure is called before each tool call to make sure the agent is
	// listening. It may start the on-demand agent, which asks the human to
	// unlock. It runs in this process but never sees the password.
	Ensure func(context.Context) error
	// Version is reported as the server version.
	Version string
	// Log receives diagnostics; never stdout.
	Log *log.Logger
}

// Server is a single MCP stdio session.
type Server struct {
	cfg Config
}

// New builds a server.
func New(cfg Config) *Server {
	if cfg.Version == "" {
		cfg.Version = "dev"
	}
	if cfg.Log == nil {
		cfg.Log = log.New(io.Discard, "", 0)
	}
	return &Server{cfg: cfg}
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// Serve reads newline delimited JSON-RPC from in and writes replies to out.
// It returns when the input closes or the context is cancelled.
func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	enc := json.NewEncoder(out)
	enc.SetEscapeHTML(false)

	reader := bufio.NewReader(in)
	for {
		if err := ctx.Err(); err != nil {
			return nil
		}
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			s.handleLine(ctx, enc, line)
		}
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

func (s *Server) handleLine(ctx context.Context, enc *json.Encoder, line []byte) {
	var req rpcRequest
	if err := json.Unmarshal(line, &req); err != nil {
		s.write(enc, rpcResponse{
			JSONRPC: "2.0",
			ID:      json.RawMessage("null"),
			Error:   &rpcError{Code: codeParseError, Message: "invalid JSON"},
		})
		return
	}
	if req.Method == "" {
		s.write(enc, rpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &rpcError{Code: codeInvalidRequest, Message: "missing method"},
		})
		return
	}

	// No id means notification: handle it, never answer.
	if isNotification(req.ID) {
		s.handleNotification(req)
		return
	}

	resp := rpcResponse{JSONRPC: "2.0", ID: req.ID}
	switch req.Method {
	case "initialize":
		resp.Result = s.initialize(req.Params)
	case "ping":
		resp.Result = map[string]any{}
	case "tools/list":
		resp.Result = map[string]any{"tools": toolDefs()}
	case "tools/call":
		resp.Result, resp.Error = s.callTool(ctx, req.Params)
	case "resources/list":
		resp.Result = map[string]any{"resources": []any{}}
	case "prompts/list":
		resp.Result = map[string]any{"prompts": []any{}}
	default:
		resp.Error = &rpcError{Code: codeMethodNotFound, Message: fmt.Sprintf("unknown method %q", req.Method)}
	}
	s.write(enc, resp)
}

func (s *Server) handleNotification(req rpcRequest) {
	switch req.Method {
	case "notifications/initialized", "notifications/cancelled", "logging/setLevel":
		// Nothing to do: every call is stateless.
	default:
		s.cfg.Log.Printf("ignoring notification %q", req.Method)
	}
}

func (s *Server) initialize(params json.RawMessage) any {
	var p struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	_ = json.Unmarshal(params, &p)

	version := protocolVersion
	if supportedVersions[p.ProtocolVersion] {
		version = p.ProtocolVersion
	}

	return map[string]any{
		"protocolVersion": version,
		"capabilities": map[string]any{
			"tools": map[string]any{},
		},
		"serverInfo": map[string]any{
			"name":    "ai-ssh",
			"version": s.cfg.Version,
		},
		"instructions": "SSH access on behalf of the user. Credentials are held by the ai-ssh agent, " +
			"a separate process; you can only ask it to run commands and move files. " +
			"If no agent is running, the first call starts one and may open a window asking the user " +
			"for the master password: wait for that call instead of retrying. Call ssh_list_hosts first. " +
			"Results are JSON with ok/exit_code/stdout/stderr/truncated/approval. " +
			"If truncated is true, narrow the command or use ssh_download instead of dumping big output. " +
			"Some hosts require the user to approve each action: a call then waits for the human and may " +
			"return approval_denied, approval_timeout or approval_unavailable. Do not retry those in a loop; " +
			"tell the user what you wanted to do and why.",
	}
}

func (s *Server) write(enc *json.Encoder, resp rpcResponse) {
	if err := enc.Encode(resp); err != nil {
		s.cfg.Log.Printf("write response: %v", err)
	}
}

func isNotification(id json.RawMessage) bool {
	if len(id) == 0 {
		return true
	}
	return string(id) == "null"
}
