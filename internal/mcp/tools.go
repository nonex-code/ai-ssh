package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/nonex-code/ai-ssh/internal/client"
	"github.com/nonex-code/ai-ssh/internal/proto"
)

type tool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema any    `json:"inputSchema"`
}

func object(props map[string]any, required ...string) map[string]any {
	if props == nil {
		props = map[string]any{}
	}
	m := map[string]any{
		"type":                 "object",
		"properties":           props,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		m["required"] = required
	}
	return m
}

func str(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}

func toolDefs() []tool {
	return []tool{
		{
			Name:        "ssh_list_hosts",
			Description: "List the servers the user has made available to you. Returns aliases, login user and whether sudo is available. Addresses are hidden unless the user enabled them.",
			InputSchema: object(nil),
		},
		{
			Name: "ssh_exec",
			Description: "Run a shell command on a configured server. Returns JSON with ok, exit_code, stdout, stderr, " +
				"stdout_bytes, truncated and duration_ms. A non-zero exit_code means the command ran and failed; " +
				"an error object means it did not run (unknown host, policy denial, timeout, daemon locked).",
			InputSchema: object(map[string]any{
				"host":      str("Server alias from ssh_list_hosts."),
				"command":   str("Shell command line, run by the remote user's shell. Pipes and redirections work."),
				"timeout_s": map[string]any{"type": "integer", "description": "Abort after this many seconds (server default when omitted)."},
				"workdir":   str("Directory to cd into before running, on the remote host."),
				"elevate":   map[string]any{"type": "boolean", "description": "Run through the server's configured sudo/su policy. Only works if the user enabled it."},
				"tail":      map[string]any{"type": "boolean", "description": "Keep the tail instead of the head when output is truncated (useful for logs)."},
				"max_bytes": map[string]any{"type": "integer", "description": "Cap on bytes returned per stream. Defaults to the server setting."},
			}, "host", "command"),
		},
		{
			Name:        "ssh_upload",
			Description: "Upload one local file to a server over SFTP. The local path must be inside the directory the user allowed.",
			InputSchema: object(map[string]any{
				"host":   str("Server alias."),
				"local":  str("Local file path on the machine running the agent."),
				"remote": str("Absolute destination path on the server."),
				"mkdir":  map[string]any{"type": "boolean", "description": "Create missing remote parent directories."},
				"mode":   str("Octal file mode, e.g. 0644."),
				"verify": map[string]any{"type": "boolean", "description": "Re-read the remote file and compare sha256."},
			}, "host", "local", "remote"),
		},
		{
			Name:        "ssh_download",
			Description: "Download one remote file over SFTP. Prefer this over dumping large output through ssh_exec.",
			InputSchema: object(map[string]any{
				"host":   str("Server alias."),
				"remote": str("Absolute source path on the server."),
				"local":  str("Destination path on the machine running the agent."),
				"mode":   str("Octal file mode, e.g. 0644."),
				"verify": map[string]any{"type": "boolean", "description": "Compare sha256 after the transfer."},
			}, "host", "remote", "local"),
		},
		{
			Name:        "ssh_ping",
			Description: "Check that a server is reachable and that authentication and host key verification succeed.",
			InputSchema: object(map[string]any{
				"host":      str("Server alias."),
				"timeout_s": map[string]any{"type": "integer", "description": "Abort after this many seconds."},
			}, "host"),
		},
	}
}

func decodeArgs(raw json.RawMessage, into any) *rpcError {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	if err := json.Unmarshal(raw, into); err != nil {
		return &rpcError{Code: codeInvalidParams, Message: "invalid arguments: " + err.Error()}
	}
	return nil
}

func (s *Server) callTool(ctx context.Context, raw json.RawMessage) (any, *rpcError) {
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: "invalid tools/call params"}
	}

	switch p.Name {
	case "ssh_list_hosts", "ssh_exec", "ssh_upload", "ssh_download", "ssh_ping":
		if s.cfg.Ensure != nil {
			if err := s.cfg.Ensure(ctx); err != nil {
				return textResult(marshal(map[string]any{
					"ok": false,
					"error": map[string]any{
						"code":    proto.ErrUnlockRequired,
						"message": err.Error(),
					},
				}), true), nil
			}
		}
	}

	switch p.Name {
	case "ssh_list_hosts":
		return s.listHosts(ctx), nil
	case "ssh_exec":
		return s.exec(ctx, p.Arguments)
	case "ssh_upload":
		return s.upload(ctx, p.Arguments)
	case "ssh_download":
		return s.download(ctx, p.Arguments)
	case "ssh_ping":
		return s.ping(ctx, p.Arguments)
	default:
		return nil, &rpcError{Code: codeInvalidParams, Message: fmt.Sprintf("unknown tool %q", p.Name)}
	}
}

// --- tools -------------------------------------------------------------------

type hostView struct {
	Alias   string `json:"alias"`
	User    string `json:"user"`
	Auth    string `json:"auth,omitempty"`
	Addr    string `json:"addr,omitempty"`
	Elevate string `json:"elevate,omitempty"`
	Workdir string `json:"workdir,omitempty"`
}

func (s *Server) listHosts(ctx context.Context) any {
	resp := client.Call(ctx, s.cfg.Endpoint, proto.Request{Op: proto.OpHosts})

	views := make([]hostView, 0, len(resp.Hosts))
	for _, h := range resp.Hosts {
		v := hostView{
			Alias:   h.Alias,
			User:    h.User,
			Auth:    h.Auth,
			Elevate: h.Elevate,
			Workdir: h.Workdir,
		}
		if s.cfg.ExposeAddr {
			v.Addr = h.Addr
		}
		views = append(views, v)
	}

	if resp.Error != nil {
		return s.renderError(resp)
	}
	return textResult(marshal(map[string]any{
		"ok":    true,
		"hosts": views,
		"count": len(views),
	}), false)
}

func (s *Server) exec(ctx context.Context, raw json.RawMessage) (any, *rpcError) {
	var a struct {
		Host     string `json:"host"`
		Command  string `json:"command"`
		Timeout  int    `json:"timeout_s"`
		Workdir  string `json:"workdir"`
		Elevate  bool   `json:"elevate"`
		Tail     bool   `json:"tail"`
		MaxBytes int64  `json:"max_bytes"`
	}
	if rerr := decodeArgs(raw, &a); rerr != nil {
		return nil, rerr
	}
	if strings.TrimSpace(a.Host) == "" || strings.TrimSpace(a.Command) == "" {
		return usageResult("host and command are required"), nil
	}
	resp := client.Call(ctx, s.cfg.Endpoint, proto.Request{
		Op:       proto.OpExec,
		Host:     a.Host,
		Command:  []string{a.Command},
		Timeout:  a.Timeout,
		Workdir:  a.Workdir,
		Elevate:  a.Elevate,
		Tail:     a.Tail,
		MaxBytes: a.MaxBytes,
	})
	return s.render(resp), nil
}

func (s *Server) upload(ctx context.Context, raw json.RawMessage) (any, *rpcError) {
	var a struct {
		Host   string `json:"host"`
		Local  string `json:"local"`
		Remote string `json:"remote"`
		Mkdir  bool   `json:"mkdir"`
		Mode   string `json:"mode"`
		Verify bool   `json:"verify"`
	}
	if rerr := decodeArgs(raw, &a); rerr != nil {
		return nil, rerr
	}
	if a.Host == "" || a.Local == "" || a.Remote == "" {
		return usageResult("host, local and remote are required"), nil
	}
	resp := client.Call(ctx, s.cfg.Endpoint, proto.Request{
		Op:     proto.OpUpload,
		Host:   a.Host,
		Local:  a.Local,
		Remote: a.Remote,
		Mkdir:  a.Mkdir,
		Mode:   a.Mode,
		Verify: a.Verify,
	})
	return s.render(resp), nil
}

func (s *Server) download(ctx context.Context, raw json.RawMessage) (any, *rpcError) {
	var a struct {
		Host   string `json:"host"`
		Remote string `json:"remote"`
		Local  string `json:"local"`
		Mode   string `json:"mode"`
		Verify bool   `json:"verify"`
	}
	if rerr := decodeArgs(raw, &a); rerr != nil {
		return nil, rerr
	}
	if a.Host == "" || a.Local == "" || a.Remote == "" {
		return usageResult("host, remote and local are required"), nil
	}
	resp := client.Call(ctx, s.cfg.Endpoint, proto.Request{
		Op:     proto.OpDownload,
		Host:   a.Host,
		Remote: a.Remote,
		Local:  a.Local,
		Mode:   a.Mode,
		Verify: a.Verify,
	})
	return s.render(resp), nil
}

func (s *Server) ping(ctx context.Context, raw json.RawMessage) (any, *rpcError) {
	var a struct {
		Host    string `json:"host"`
		Timeout int    `json:"timeout_s"`
	}
	if rerr := decodeArgs(raw, &a); rerr != nil {
		return nil, rerr
	}
	if a.Host == "" {
		return usageResult("host is required"), nil
	}
	resp := client.Call(ctx, s.cfg.Endpoint, proto.Request{
		Op: proto.OpPing, Host: a.Host, Timeout: a.Timeout,
	})
	return s.render(resp), nil
}

// --- rendering ---------------------------------------------------------------

func (s *Server) render(resp proto.Response) any {
	if resp.Error != nil && resp.Error.Code == proto.ErrDaemonDown {
		resp.Error.Message = "the ai-ssh agent stopped or the vault is locked. " +
			"Ask the user to unlock it (ai-ssh starts the agent on demand). " + resp.Error.Message
	}
	// A non-zero remote exit code is a legitimate result, not a tool failure:
	// only infrastructure or policy errors set isError.
	return textResult(marshal(resp), resp.Error != nil)
}

func (s *Server) renderError(resp proto.Response) any {
	return textResult(marshal(resp), true)
}

func usageResult(msg string) any {
	body := marshal(map[string]any{
		"ok":    false,
		"error": map[string]any{"code": proto.ErrUsage, "message": msg},
	})
	return textResult(body, true)
}

func textResult(text string, isError bool) map[string]any {
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": text}},
		"isError": isError,
	}
}

// marshal renders compact JSON without HTML escaping so logs stay readable in
// the model's context.
func marshal(v any) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return fmt.Sprintf(`{"ok":false,"error":{"code":"internal","message":%q}}`, err.Error())
	}
	return strings.TrimRight(b.String(), "\n")
}
