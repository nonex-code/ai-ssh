package mcp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/nonex-code/ai-ssh/internal/ipc"
	"github.com/nonex-code/ai-ssh/internal/mcp"
	"github.com/nonex-code/ai-ssh/internal/proto"
)

// --- a fake agent on the other end of the socket -----------------------------

func testEndpoint(t *testing.T) ipc.Endpoint {
	t.Helper()
	unique := fmt.Sprintf("%d-%d", os.Getpid(), time.Now().UnixNano())
	if runtime.GOOS == "windows" {
		return ipc.FromAddr(`\\.\pipe\ai-ssh-mcptest-` + unique)
	}
	return ipc.FromAddr(filepath.Join(t.TempDir(), "agent.sock"))
}

type fakeAgent struct {
	ep       ipc.Endpoint
	requests []proto.Request
}

func startFakeAgent(t *testing.T, handler func(proto.Request) proto.Response) *fakeAgent {
	t.Helper()
	fa := &fakeAgent{ep: testEndpoint(t)}
	ln, err := fa.ep.Listen()
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				_ = c.SetDeadline(time.Now().Add(10 * time.Second))
				var req proto.Request
				if err := json.NewDecoder(c).Decode(&req); err != nil {
					return
				}
				fa.requests = append(fa.requests, req)
				_ = json.NewEncoder(c).Encode(handler(req))
			}(conn)
		}
	}()
	return fa
}

// run feeds input to the MCP server and returns the decoded output lines.
func run(t *testing.T, cfg mcp.Config, input string) []map[string]any {
	t.Helper()
	var out bytes.Buffer
	srv := mcp.New(cfg)
	if err := srv.Serve(context.Background(), strings.NewReader(input), &out); err != nil {
		t.Fatalf("serve: %v", err)
	}
	var lines []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("bad JSON line %q: %v", line, err)
		}
		lines = append(lines, m)
	}
	return lines
}

func textOf(t *testing.T, resp map[string]any) string {
	t.Helper()
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("no result in %v", resp)
	}
	content, ok := result["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatalf("no content in %v", result)
	}
	first := content[0].(map[string]any)
	return first["text"].(string)
}

func isError(t *testing.T, resp map[string]any) bool {
	t.Helper()
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("no result in %v", resp)
	}
	b, _ := result["isError"].(bool)
	return b
}

// --- protocol ----------------------------------------------------------------

func TestInitialize(t *testing.T) {
	lines := run(t, mcp.Config{}, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"x"}}}
`)
	if len(lines) != 1 {
		t.Fatalf("expected 1 response, got %d", len(lines))
	}
	result := lines[0]["result"].(map[string]any)
	if result["protocolVersion"] != "2025-03-26" {
		t.Fatalf("protocolVersion = %v", result["protocolVersion"])
	}
	info := result["serverInfo"].(map[string]any)
	if info["name"] != "ai-ssh" {
		t.Fatalf("serverInfo = %v", info)
	}
	caps := result["capabilities"].(map[string]any)
	if _, ok := caps["tools"]; !ok {
		t.Fatalf("tools capability missing: %v", caps)
	}
}

func TestInitializeUnknownVersionFallsBack(t *testing.T) {
	lines := run(t, mcp.Config{}, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"1999-01-01"}}`+"\n")
	result := lines[0]["result"].(map[string]any)
	if result["protocolVersion"] == "1999-01-01" {
		t.Fatal("must not echo an unsupported protocol version")
	}
}

func TestNotificationsProduceNoOutput(t *testing.T) {
	lines := run(t, mcp.Config{}, `{"jsonrpc":"2.0","method":"notifications/initialized"}
{"jsonrpc":"2.0","id":2,"method":"ping"}
`)
	if len(lines) != 1 {
		t.Fatalf("expected only the ping reply, got %d lines", len(lines))
	}
	if lines[0]["id"].(float64) != 2 {
		t.Fatalf("unexpected reply %v", lines[0])
	}
}

func TestParseError(t *testing.T) {
	lines := run(t, mcp.Config{}, "{not json}\n")
	if len(lines) != 1 {
		t.Fatalf("expected one error, got %d", len(lines))
	}
	errObj := lines[0]["error"].(map[string]any)
	if errObj["code"].(float64) != -32700 {
		t.Fatalf("code = %v", errObj["code"])
	}
}

func TestToolsList(t *testing.T) {
	lines := run(t, mcp.Config{}, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`+"\n")
	result := lines[0]["result"].(map[string]any)
	tools := result["tools"].([]any)
	want := map[string]bool{
		"ssh_list_hosts": false, "ssh_exec": false, "ssh_upload": false,
		"ssh_download": false, "ssh_ping": false,
	}
	for _, raw := range tools {
		tool := raw.(map[string]any)
		name := tool["name"].(string)
		if _, ok := want[name]; !ok {
			t.Fatalf("unexpected tool %q", name)
		}
		want[name] = true
		schema := tool["inputSchema"].(map[string]any)
		if schema["type"] != "object" {
			t.Fatalf("%s: bad schema %v", name, schema)
		}
		if tool["description"] == "" {
			t.Fatalf("%s: empty description", name)
		}
	}
	for name, found := range want {
		if !found {
			t.Fatalf("missing tool %q", name)
		}
	}
}

// --- tools -------------------------------------------------------------------

func TestExecForwardsToAgent(t *testing.T) {
	fa := startFakeAgent(t, func(req proto.Request) proto.Response {
		code := 0
		return proto.Response{
			OK: true, Action: req.Op, Host: req.Host, ExitCode: &code,
			Stdout: "up 3 days\n", StdoutBytes: 11,
		}
	})

	call := `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"ssh_exec","arguments":{"host":"prod","command":"uptime","elevate":true,"timeout_s":15}}}`
	lines := run(t, mcp.Config{Endpoint: fa.ep}, call+"\n")
	if len(lines) != 1 {
		t.Fatalf("expected 1 reply, got %d", len(lines))
	}
	if isError(t, lines[0]) {
		t.Fatalf("unexpected error: %v", lines[0])
	}

	text := textOf(t, lines[0])
	var body map[string]any
	if err := json.Unmarshal([]byte(text), &body); err != nil {
		t.Fatalf("tool text is not JSON: %q", text)
	}
	if body["stdout"] != "up 3 days\n" {
		t.Fatalf("stdout = %v", body["stdout"])
	}

	if len(fa.requests) != 1 {
		t.Fatalf("expected 1 forwarded request, got %d", len(fa.requests))
	}
	got := fa.requests[0]
	if got.Op != proto.OpExec || got.Host != "prod" || got.Timeout != 15 || !got.Elevate {
		t.Fatalf("forwarded request = %+v", got)
	}
	if len(got.Command) != 1 || got.Command[0] != "uptime" {
		t.Fatalf("command = %v", got.Command)
	}
}

func TestExecRemoteFailureIsNotToolError(t *testing.T) {
	fa := startFakeAgent(t, func(req proto.Request) proto.Response {
		code := 2
		return proto.Response{OK: false, Host: req.Host, ExitCode: &code, Stderr: "nope\n"}
	})
	call := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ssh_exec","arguments":{"host":"h","command":"false"}}}`
	lines := run(t, mcp.Config{Endpoint: fa.ep}, call+"\n")
	if isError(t, lines[0]) {
		t.Fatal("a non-zero remote exit code must not be reported as a tool error")
	}
}

func TestPolicyDenialIsToolError(t *testing.T) {
	fa := startFakeAgent(t, func(req proto.Request) proto.Response {
		return proto.Response{
			OK:    false,
			Host:  req.Host,
			Error: &proto.Error{Code: proto.ErrBlocked, Message: "deny rule"},
		}
	})
	call := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ssh_exec","arguments":{"host":"h","command":"reboot"}}}`
	lines := run(t, mcp.Config{Endpoint: fa.ep}, call+"\n")
	if !isError(t, lines[0]) {
		t.Fatal("policy denial must set isError")
	}
	if !strings.Contains(textOf(t, lines[0]), "blocked") {
		t.Fatalf("text = %q", textOf(t, lines[0]))
	}
}

func TestListHostsHidesAddressByDefault(t *testing.T) {
	fa := startFakeAgent(t, func(req proto.Request) proto.Response {
		return proto.Response{
			OK: true,
			Hosts: []proto.HostInfo{
				{Alias: "prod", User: "deploy", Addr: "10.0.0.5:22", Auth: "key", Elevate: "sudo"},
			},
		}
	})

	call := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ssh_list_hosts","arguments":{}}}`
	text := textOf(t, run(t, mcp.Config{Endpoint: fa.ep}, call+"\n")[0])
	if strings.Contains(text, "10.0.0.5") {
		t.Fatalf("address leaked by default: %s", text)
	}
	if !strings.Contains(text, `"elevate":"sudo"`) {
		t.Fatalf("elevate flag missing: %s", text)
	}

	text = textOf(t, run(t, mcp.Config{Endpoint: fa.ep, ExposeAddr: true}, call+"\n")[0])
	if !strings.Contains(text, "10.0.0.5") {
		t.Fatalf("--expose-addr did not include the address: %s", text)
	}
}

func TestUploadAndDownloadForwardPaths(t *testing.T) {
	fa := startFakeAgent(t, func(req proto.Request) proto.Response {
		return proto.Response{OK: true, Action: req.Op, Host: req.Host, Bytes: 42, Sha256: "abc"}
	})

	lines := run(t, mcp.Config{Endpoint: fa.ep}, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ssh_upload","arguments":{"host":"h","local":"/tmp/a","remote":"/srv/a","mkdir":true,"verify":true}}}
{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"ssh_download","arguments":{"host":"h","remote":"/var/log/x","local":"/tmp/x"}}}
`)
	if len(lines) != 2 {
		t.Fatalf("expected 2 replies, got %d", len(lines))
	}
	if fa.requests[0].Op != proto.OpUpload || !fa.requests[0].Mkdir || !fa.requests[0].Verify {
		t.Fatalf("upload request = %+v", fa.requests[0])
	}
	if fa.requests[1].Op != proto.OpDownload || fa.requests[1].Remote != "/var/log/x" {
		t.Fatalf("download request = %+v", fa.requests[1])
	}
}

func TestDaemonDownIsActionable(t *testing.T) {
	ep := testEndpoint(t) // nothing listening
	call := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ssh_exec","arguments":{"host":"h","command":"id"}}}`
	lines := run(t, mcp.Config{Endpoint: ep}, call+"\n")
	if !isError(t, lines[0]) {
		t.Fatal("expected a tool error")
	}
	text := textOf(t, lines[0])
	if !strings.Contains(text, "unlock") {
		t.Fatalf("message should tell the agent to ask the user: %q", text)
	}
}

func TestUnknownToolIsInvalidParams(t *testing.T) {
	lines := run(t, mcp.Config{}, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ssh_reboot","arguments":{}}}`+"\n")
	if len(lines) != 1 || lines[0]["error"] == nil {
		t.Fatalf("expected a JSON-RPC error, got %v", lines)
	}
	errObj := lines[0]["error"].(map[string]any)
	if errObj["code"].(float64) != -32602 {
		t.Fatalf("code = %v", errObj["code"])
	}
}

func TestMissingArgumentsAreUsageErrors(t *testing.T) {
	lines := run(t, mcp.Config{}, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ssh_exec","arguments":{"host":"h"}}}`+"\n")
	if !isError(t, lines[0]) {
		t.Fatal("expected isError for missing command")
	}
}
