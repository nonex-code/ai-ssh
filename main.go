// Command ai-ssh is one program: the desktop window, the AI-facing CLI, the
// MCP server and the credential-holding agent. Which one runs depends on the
// arguments; the window opens when there are none.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/nonex-code/ai-ssh/internal/agent"
	"github.com/nonex-code/ai-ssh/internal/client"
	"github.com/nonex-code/ai-ssh/internal/gui"
	"github.com/nonex-code/ai-ssh/internal/ipc"
	"github.com/nonex-code/ai-ssh/internal/onagent"
	"github.com/nonex-code/ai-ssh/internal/proto"
	"github.com/nonex-code/ai-ssh/internal/version"
	"github.com/nonex-code/ai-ssh/internal/vtlib"
)

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		// Double-clicking the binary opens the window, with no console.
		os.Exit(cmdGUI(nil))
	}
	if args[0] != "gui" {
		attachConsole()
	}
	os.Exit(run(args))
}

func run(args []string) int {
	if len(args) == 0 {
		return cmdGUI(nil)
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "gui":
		return cmdGUI(rest)
	case "exec":
		return cmdExec(rest)
	case "upload":
		return cmdUpload(rest)
	case "download":
		return cmdDownload(rest)
	case "hosts":
		return cmdHosts(rest)
	case "ping":
		return cmdPing(rest)
	case "agent":
		return cmdAgent(rest)
	case "daemon":
		return cmdDaemon(rest)
	case "mcp":
		return cmdMCP(rest)
	case "init":
		return cmdInit(rest)
	case "passwd":
		return cmdPasswd(rest)
	case "config":
		return cmdConfig(rest)
	case "version", "--version", "-v":
		fmt.Printf("ai-ssh %s\n", version.Value)
		return 0
	case "help", "--help", "-h":
		usage(os.Stdout)
		return 0
	default:
		usage(os.Stderr)
		return failResp(cmd, proto.ErrUsage, fmt.Sprintf("unknown command %q", cmd))
	}
}

func usage(w *os.File) {
	fmt.Fprint(w, `ai-ssh `+version.Value+` - SSH access for AI agents, credentials never leave the agent.

  ai-ssh
        Open the desktop window (same as "ai-ssh gui"). This is the only
        program to install: the window, the CLI, MCP and the agent are one
        binary.

  ai-ssh gui [--vault PATH] [--endpoint ADDR]
        Open the desktop window explicitly.

  ai-ssh mcp [--endpoint ADDR] [--expose-addr]
        Speak the Model Context Protocol on stdin/stdout and forward tool calls
        to the agent. Contains no credentials: safe for an AI host to spawn.
        If no agent is running, one is started and the user is asked for the
        master password. stdout carries the protocol only; logs go to stderr.

  ai-ssh init [--vault PATH] [--force]
        Create the encrypted vault now (first unlock does this automatically).

  ai-ssh passwd [--vault PATH] [--endpoint ADDR]
        Change the master password. Lock the window or stop "ai-ssh daemon"
        first: a running agent keeps the old password and would undo the change
        on its next save. The window's 设置 › 安全 does it without locking.

  ai-ssh config show [--vault PATH]
        Print the decrypted catalogue as JSON, with literal secrets masked.
  ai-ssh config edit [--vault PATH] [--editor CMD]
        Edit the catalogue in $EDITOR; it is re-sealed when you save and quit.
  ai-ssh config import <file|-> [--vault PATH]
        Replace the catalogue with a JSON file (stdin when the file is -).

  ai-ssh hosts [--endpoint ADDR]
        List configured aliases (no credentials).

  ai-ssh ping <host> [--timeout SECONDS]
        Verify connectivity, authentication and host key.

  ai-ssh exec <host> [--workdir DIR] [--timeout SECONDS] [--max-bytes N]
                          [--tail] [--sudo] [--pty] [--endpoint ADDR]
                          -- <command...>
        Run a command. Output is JSON; nothing is ever written to a TTY.
        --sudo runs it through the host's configured sudo/su policy; the
        credential never leaves the agent. --pty forces a terminal (su, or
        sudo with requiretty) and merges stderr into stdout.

  ai-ssh upload <host> <local> <remote> [--mkdir] [--mode 0644] [--verify]
  ai-ssh download <host> <remote> <local> [--mode 0644] [--verify]
        Transfer a single file over SFTP.

  ai-ssh daemon [--vault PATH] [--endpoint ADDR] [--idle-lock 30m]
        Run the agent in the foreground yourself, for servers and debugging.
        Normally the CLI/MCP start it on demand instead.

  ai-ssh agent --auto
        Internal: the on-demand agent started by the CLI/MCP. It asks for the
        master password (window, terminal, or askpass) and exits when idle.

Exit codes: 0 ok, 1 usage, 2 unlock/connect/auth (including a locked vault),
            3 remote command failed, 4 transfer failed, 5 blocked by policy,
            6 daemon unavailable, 124 timeout.

Every reply is one JSON object on stdout. Diagnostics go to stderr.
`)
}

// emit prints the response and converts it to a process exit code.
func emit(resp proto.Response) int {
	if err := client.Print(os.Stdout, resp); err != nil {
		fmt.Fprintf(os.Stderr, "ai-ssh: write response: %v\n", err)
		return proto.ExitUsage
	}
	return resp.ExitCodeFor()
}

func failResp(op, code, msg string) int {
	return emit(proto.Response{
		OK:     false,
		Action: op,
		Error:  &proto.Error{Code: code, Message: msg},
	})
}

func endpointFor(f *flags) (ipc.Endpoint, error) {
	if v := f.str("endpoint", ""); v != "" {
		return ipc.FromAddr(v), nil
	}
	return ipc.Default()
}

var endpointSpec = map[string]bool{"endpoint": true}

// ensureAgent starts the agent when nothing is listening. It never unlocks:
// the agent asks the human, and this process stays credential free.
func ensureAgent(ctx context.Context, ep ipc.Endpoint) error {
	vp, err := agent.DefaultVaultPath("")
	if err != nil {
		return err
	}
	return onagent.Ensure(ctx, onagent.Options{
		VaultPath: vp,
		Endpoint:  ep,
		Logger:    log.New(os.Stderr, "ai-ssh: ", log.LstdFlags),
	})
}

// callAgent is the one path from a CLI command to the agent: make sure one is
// listening, then send the request.
func callAgent(ctx context.Context, ep ipc.Endpoint, req proto.Request) proto.Response {
	if err := ensureAgent(ctx, ep); err != nil {
		return proto.Response{
			OK:     false,
			Action: req.Op,
			Host:   req.Host,
			Error: &proto.Error{
				Code:    proto.ErrUnlockRequired,
				Message: unlockMessage(err),
				Host:    req.Host,
			},
		}
	}
	return client.Call(ctx, ep, req)
}

func unlockMessage(err error) string {
	switch {
	case errors.Is(err, onagent.ErrNoAutostart):
		return "vault 已锁定，且自动启动 agent 被禁用 (AISSH_NO_AUTOSTART)：请先运行 ai-ssh daemon"
	case errors.Is(err, onagent.ErrTimeout):
		return "等待解锁超时：没有 agent 在运行，用户也没有完成主密码输入。请运行 ai-ssh daemon，或设置 AISSH_MASTER_PASSWORD / AISSH_ASKPASS"
	}
	return fmt.Sprintf("vault 已锁定，且无法拉起解锁窗口：%v。请运行 ai-ssh daemon，或设置 AISSH_MASTER_PASSWORD / AISSH_ASKPASS", err)
}

// cmdGUI opens the desktop window. It never attaches to a console: launching
// from Explorer must not flash one.
func cmdGUI(args []string) int {
	spec := map[string]bool{"vault": true, "endpoint": true, "version": true}
	f, err := parseFlags(args, spec)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ai-ssh: %v\n", err)
		return 1
	}
	if len(f.pos) > 0 {
		fmt.Fprintf(os.Stderr, "ai-ssh: unexpected argument %q\n", f.pos[0])
		return 1
	}
	vp, err := agent.DefaultVaultPath(f.str("vault", ""))
	if err != nil {
		fmt.Fprintf(os.Stderr, "ai-ssh: %v\n", err)
		return 1
	}
	var ep ipc.Endpoint
	if v := f.str("endpoint", ""); v != "" {
		ep = ipc.FromAddr(v)
	}
	// The terminal library is embedded in self-contained builds (make build /
	// make dist); extracting it here keeps the first session instant instead of
	// downloading at that moment.
	if err := vtlib.Ensure(); err != nil {
		log.Printf("terminal library: %v", err)
	}
	if err := gui.Run(gui.Options{
		VaultPath: vp,
		Endpoint:  ep,
		Version:   f.str("version", version.Value),
	}); err != nil {
		fmt.Fprintf(os.Stderr, "ai-ssh: %v\n", err)
		return 1
	}
	return 0
}

// cmdAgent is the on-demand agent. It is started by ensureAgent, not by hand.
func cmdAgent(args []string) int {
	spec := map[string]bool{
		"auto": false, "vault": true, "endpoint": true,
		"audit-log": true, "idle-lock": true, "no-idle-lock": false,
	}
	f, err := parseFlags(args, spec)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ai-ssh: %v\n", err)
		return 1
	}
	if !f.bool("auto") {
		fmt.Fprintln(os.Stderr, "ai-ssh: the agent starts on demand; use \"ai-ssh daemon\" for a foreground daemon")
		return proto.ExitUsage
	}
	vp, err := agent.DefaultVaultPath(f.str("vault", ""))
	if err != nil {
		fmt.Fprintf(os.Stderr, "ai-ssh: %v\n", err)
		return 1
	}
	ep, err := endpointFor(f)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ai-ssh: %v\n", err)
		return 1
	}
	var idle time.Duration
	if v := f.str("idle-lock", ""); v != "" {
		d, perr := time.ParseDuration(v)
		if perr != nil {
			fmt.Fprintf(os.Stderr, "ai-ssh: --idle-lock: %v\n", perr)
			return 1
		}
		idle = d
	}
	err = gui.RunAuto(gui.AutoOptions{
		VaultPath:       vp,
		Endpoint:        ep,
		AuditPath:       f.str("audit-log", ""),
		IdleLock:        idle,
		DisableIdleLock: f.bool("no-idle-lock"),
		Logger:          log.New(os.Stderr, "ai-ssh agent: ", log.LstdFlags),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "ai-ssh agent: %v\n", err)
		return 1
	}
	return 0
}

func cmdHosts(args []string) int {
	f, err := parseFlags(args, endpointSpec)
	if err != nil {
		return failResp(proto.OpHosts, proto.ErrUsage, err.Error())
	}
	ep, err := endpointFor(f)
	if err != nil {
		return failResp(proto.OpHosts, proto.ErrConfig, err.Error())
	}
	return emit(callAgent(context.Background(), ep, proto.Request{Op: proto.OpHosts}))
}

func cmdPing(args []string) int {
	spec := map[string]bool{"endpoint": true, "timeout": true}
	f, err := parseFlags(args, spec)
	if err != nil {
		return failResp(proto.OpPing, proto.ErrUsage, err.Error())
	}
	if len(f.pos) != 1 {
		return failResp(proto.OpPing, proto.ErrUsage, "usage: ai-ssh ping <host>")
	}
	ep, err := endpointFor(f)
	if err != nil {
		return failResp(proto.OpPing, proto.ErrConfig, err.Error())
	}
	req := proto.Request{Op: proto.OpPing, Host: f.pos[0], Timeout: f.int("timeout", 0)}
	return emit(callAgent(context.Background(), ep, req))
}

func cmdExec(args []string) int {
	spec := map[string]bool{
		"endpoint": true, "timeout": true, "max-bytes": true,
		"workdir": true, "tail": false, "sudo": false, "pty": false,
	}
	f, err := parseFlags(args, spec)
	if err != nil {
		return failResp(proto.OpExec, proto.ErrUsage, err.Error())
	}
	if len(f.pos) < 2 {
		return failResp(proto.OpExec, proto.ErrUsage,
			"usage: ai-ssh exec <host> [flags] -- <command...>")
	}
	ep, err := endpointFor(f)
	if err != nil {
		return failResp(proto.OpExec, proto.ErrConfig, err.Error())
	}
	req := proto.Request{
		Op:       proto.OpExec,
		Host:     f.pos[0],
		Command:  f.pos[1:],
		Workdir:  f.str("workdir", ""),
		Timeout:  f.int("timeout", 0),
		MaxBytes: f.int64("max-bytes", 0),
		Tail:     f.bool("tail"),
		Elevate:  f.bool("sudo"),
		PTY:      f.bool("pty"),
	}
	return emit(callAgent(context.Background(), ep, req))
}

func cmdUpload(args []string) int {
	spec := map[string]bool{
		"endpoint": true, "mkdir": false, "mode": true, "verify": false,
	}
	f, err := parseFlags(args, spec)
	if err != nil {
		return failResp(proto.OpUpload, proto.ErrUsage, err.Error())
	}
	if len(f.pos) != 3 {
		return failResp(proto.OpUpload, proto.ErrUsage,
			"usage: ai-ssh upload <host> <local> <remote> [--mkdir] [--mode 0644] [--verify]")
	}
	ep, err := endpointFor(f)
	if err != nil {
		return failResp(proto.OpUpload, proto.ErrConfig, err.Error())
	}
	req := proto.Request{
		Op:     proto.OpUpload,
		Host:   f.pos[0],
		Local:  f.pos[1],
		Remote: f.pos[2],
		Mkdir:  f.bool("mkdir"),
		Mode:   f.str("mode", ""),
		Verify: f.bool("verify"),
	}
	return emit(callAgent(context.Background(), ep, req))
}

func cmdDownload(args []string) int {
	spec := map[string]bool{"endpoint": true, "mode": true, "verify": false}
	f, err := parseFlags(args, spec)
	if err != nil {
		return failResp(proto.OpDownload, proto.ErrUsage, err.Error())
	}
	if len(f.pos) != 3 {
		return failResp(proto.OpDownload, proto.ErrUsage,
			"usage: ai-ssh download <host> <remote> <local> [--mode 0644] [--verify]")
	}
	ep, err := endpointFor(f)
	if err != nil {
		return failResp(proto.OpDownload, proto.ErrConfig, err.Error())
	}
	req := proto.Request{
		Op:     proto.OpDownload,
		Host:   f.pos[0],
		Local:  f.pos[2],
		Remote: f.pos[1],
		Mode:   f.str("mode", ""),
		Verify: f.bool("verify"),
	}
	return emit(callAgent(context.Background(), ep, req))
}
