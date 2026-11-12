package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nonex-code/ai-ssh/internal/appdir"
	"github.com/nonex-code/ai-ssh/internal/config"
	"github.com/nonex-code/ai-ssh/internal/ipc"
	"github.com/nonex-code/ai-ssh/internal/proto"
)

// Options configures a daemon instance.
type Options struct {
	Config    *config.Config
	Endpoint  ipc.Endpoint
	VaultPath string
	Logger    *log.Logger
	AuditPath string
	// SessionInit enables the small shell setup that colours ls (an alias,
	// only when ls is not aliased already). Off in tests and for the CLI,
	// which never opens a shell.
	SessionInit bool
	// MaxConcurrent caps simultaneous remote operations.
	MaxConcurrent int
	// OnListen runs once the endpoint is accepting connections.
	OnListen func()
}

// Server holds the only copy of the credentials in the whole system. The client
// process never sees them: it can only submit proto.Request values.
type Server struct {
	cfgMu    sync.RWMutex
	cfg      *config.Config
	ep       ipc.Endpoint
	log      *log.Logger
	ln       net.Listener
	idle     time.Duration
	sem      chan struct{}
	lastUse  atomic.Int64
	closed   chan struct{}
	closeOne sync.Once
	audit    *auditLog
	hub      *approvalHub
	// sessionInit adds the ls alias to an interactive shell when it starts;
	// the probe's answer is cached per host.
	sessionInit bool
	initMu      sync.Mutex
	initCache   map[string]string
	onListen    func()

	protected []string
	secrets   []string
}

// New builds a daemon server. It does not start listening.
func New(opts Options) (*Server, error) {
	if opts.Config == nil {
		return nil, errors.New("daemon: nil config")
	}
	if opts.Logger == nil {
		opts.Logger = log.New(os.Stderr, "ai-ssh[d] ", log.LstdFlags)
	}
	max := opts.MaxConcurrent
	if max <= 0 {
		max = 4
	}

	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	// With no explicit roots, the daemon only guards its own start directory.
	if len(opts.Config.Defaults.UploadRoots) == 0 {
		opts.Config.Defaults.UploadRoots = []string{cwd}
	}
	if len(opts.Config.Defaults.DownloadRoots) == 0 {
		opts.Config.Defaults.DownloadRoots = []string{cwd}
	}

	s := &Server{
		cfg:      opts.Config,
		onListen: opts.OnListen,
		ep:       opts.Endpoint,
		log:      opts.Logger,
		idle:     opts.Config.Defaults.IdleLock.Or(config.DefaultIdleLock),
		sem:      make(chan struct{}, max),
		closed:   make(chan struct{}),
		secrets:  append([]string{}, opts.Config.Secrets()...),
	}

	auditPath := opts.AuditPath
	if auditPath == "" {
		auditPath = opts.Config.Defaults.AuditLog
	}
	if auditPath == "" {
		if p, err := appdir.AuditLogPath(); err == nil {
			auditPath = p
		}
	}
	s.protected = protectedPaths(opts.VaultPath, auditPath)
	if home, err := os.UserHomeDir(); err == nil {
		s.protected = append(s.protected, filepath.Join(home, ".ssh"))
	}

	s.sessionInit = opts.SessionInit
	s.initCache = map[string]string{}
	s.audit = newAuditLog(auditPath, s.log)
	s.hub = newApprovalHub(opts.Config.ApprovalWait(), s.log)

	s.lastUse.Store(time.Now().UnixNano())
	return s, nil
}

// protectedPaths is the deny-list for client supplied local paths. It is
// deliberately narrow: the vault itself (plus the files the operator tools use
// next to it) and the user's SSH directory. Protecting the whole config
// directory would break anyone who keeps their vault inside a project checkout.
func protectedPaths(vaultPath, auditPath string) []string {
	var out []string
	if vaultPath != "" {
		out = append(out,
			vaultPath,
			vaultPath+".tmp",
			vaultPath+".edit",
		)
	}
	if auditPath != "" {
		out = append(out, auditPath)
	}
	if home, err := os.UserHomeDir(); err == nil {
		out = append(out, filepath.Join(home, ".ssh"))
	}
	return out
}

// config is the live host catalogue. It can be swapped while the daemon runs,
// which is what lets the window add a host without restarting the agent.
func (s *Server) config() *config.Config {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	return s.cfg
}

// SetConfig replaces the catalogue. In-flight requests keep the pointer they
// took, which is fine: it stays valid, just older.
func (s *Server) SetConfig(cfg *config.Config) {
	s.cfgMu.Lock()
	s.cfg = cfg
	s.cfgMu.Unlock()
}

// TestHost dials a host that is not (or not yet) in the catalogue, as the
// connection form's test button does: TCP, host key and authentication.
func (s *Server) TestHost(ctx context.Context, h *config.Host) error {
	if h == nil {
		return errors.New("daemon: nil host")
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
	}
	client, err := s.dial(ctx, h)
	if err != nil {
		return err
	}
	return client.Close()
}

// RunHuman runs one command for the window, in-process: the monitor pane and
// other human-only views use it. The AI's socket protocol does not reach it.
func (s *Server) RunHuman(ctx context.Context, alias, command string, timeout time.Duration) (string, string, error) {
	h, err := s.config().Host(alias)
	if err != nil {
		return "", "", err
	}
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	client, err := s.dial(ctx, h)
	if err != nil {
		return "", "", err
	}
	defer client.Close()

	res := s.runCommand(ctx, client, command, "", false, 256<<10, false)
	if res.timedOut {
		return res.stdout, res.stderr, fmt.Errorf("timed out after %s", timeout)
	}
	if res.runErr != nil {
		return res.stdout, res.stderr, res.runErr
	}
	return res.stdout, res.stderr, nil
}

// Serve listens and handles requests until the context is cancelled, a lock or
// shutdown request arrives, or the idle timer fires.
func (s *Server) Serve(ctx context.Context) error {
	ln, err := s.ep.Listen()
	if err != nil {
		return fmt.Errorf("listen on %s: %w", s.ep, err)
	}
	s.ln = ln
	if s.onListen != nil {
		s.onListen()
	}

	go func() {
		<-ctx.Done()
		s.Shutdown()
	}()
	go s.watchIdle()

	var wg sync.WaitGroup
	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-s.closed:
				wg.Wait()
				return nil
			default:
			}
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			wg.Wait()
			return err
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.serveConn(conn)
		}()
	}
}

// Shutdown stops accepting connections and removes the endpoint.
func (s *Server) Shutdown() {
	s.closeOne.Do(func() {
		close(s.closed)
		if s.ln != nil {
			_ = s.ln.Close()
		}
		if s.hub != nil {
			s.hub.closeAll()
		}
	})
}

func (s *Server) watchIdle() {
	if s.idle <= 0 {
		return
	}
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-s.closed:
			return
		case <-t.C:
			last := time.Unix(0, s.lastUse.Load())
			if time.Since(last) >= s.idle {
				s.log.Printf("idle for %s, locking and exiting", s.idle.Round(time.Second))
				s.Shutdown()
				return
			}
		}
	}
}

func (s *Server) serveConn(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Hour))

	var req proto.Request
	if err := json.NewDecoder(conn).Decode(&req); err != nil {
		// A bare connect-and-close is how clients check "is an agent
		// running?"; it is not an error worth logging.
		if !errors.Is(err, io.EOF) {
			s.log.Printf("malformed request: %v", err)
		}
		return
	}
	s.lastUse.Store(time.Now().UnixNano())

	// A front end (the GUI) attaches once and keeps the connection open for
	// approval events, so it gets no deadline.
	if req.Op == proto.OpAttach {
		_ = conn.SetDeadline(time.Time{})
		s.hub.serve(conn, req.Role)
		return
	}

	s.sem <- struct{}{}
	resp := s.handle(conn, req)
	<-s.sem

	enc := json.NewEncoder(conn)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(resp); err != nil {
		s.log.Printf("write response: %v", err)
	}
}

func (s *Server) handle(conn net.Conn, req proto.Request) proto.Response {
	start := time.Now()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// If the client goes away (Ctrl-C, crashed agent), stop the remote work.
	watchDone := make(chan struct{})
	defer close(watchDone)
	go func() {
		buf := make([]byte, 1)
		for {
			if _, err := conn.Read(buf); err != nil {
				cancel()
				return
			}
			select {
			case <-watchDone:
				return
			default:
			}
		}
	}()

	resp := s.dispatch(ctx, req)
	resp.Action = req.Op
	if resp.Host == "" {
		resp.Host = req.Host
	}
	resp.DurationMs = time.Since(start).Milliseconds()
	s.redactResponse(&resp)
	s.record(req, resp)
	return resp
}

func (s *Server) dispatch(ctx context.Context, req proto.Request) (resp proto.Response) {
	defer func() {
		if r := recover(); r != nil {
			resp = proto.Response{Error: &proto.Error{
				Code:    proto.ErrInternal,
				Message: fmt.Sprintf("daemon panic: %v", r),
				Host:    req.Host,
			}}
		}
	}()

	switch req.Op {
	case proto.OpExec:
		return s.handleExec(ctx, req)
	case proto.OpUpload:
		return s.handleUpload(ctx, req)
	case proto.OpDownload:
		return s.handleDownload(ctx, req)
	case proto.OpHosts:
		return s.handleHosts()
	case proto.OpPing:
		return s.handlePing(ctx, req)
	case proto.OpLock, proto.OpShutdown:
		go func() {
			time.Sleep(50 * time.Millisecond)
			s.Shutdown()
		}()
		return ok(req.Op, req.Host)
	default:
		return fail(proto.ErrUsage, fmt.Sprintf("unknown op %q", req.Op), req.Host)
	}
}

func (s *Server) handleHosts() proto.Response {
	aliases := s.config().Aliases()
	sort.Strings(aliases)
	out := make([]proto.HostInfo, 0, len(aliases))
	for _, a := range aliases {
		h := s.config().Hosts[a]
		elevate := ""
		if h.Elevate != nil {
			elevate = h.Elevate.Mode
		}
		out = append(out, proto.HostInfo{
			Alias:    h.Alias,
			Addr:     h.Addr,
			User:     h.User,
			Auth:     h.Auth.Type,
			HostKey:  h.HostKey,
			Workdir:  h.Workdir,
			Elevate:  elevate,
			Group:    h.Group,
			Favorite: h.Favorite,
		})
	}
	return proto.Response{OK: true, Hosts: out}
}

func (s *Server) handlePing(ctx context.Context, req proto.Request) proto.Response {
	h, err := s.config().Host(req.Host)
	if err != nil {
		return fail(proto.ErrUnknownHost, err.Error(), req.Host)
	}
	ctx, cancel := context.WithTimeout(ctx, s.config().Timeout(h))
	defer cancel()

	client, err := s.dial(ctx, h)
	if err != nil {
		code, msg := codeOf(err)
		return fail(code, msg, req.Host)
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return fail(proto.ErrRemote, err.Error(), req.Host)
	}
	defer session.Close()

	// Cheap round trip proving the channel works, no output required.
	if _, err := session.Output("true"); err != nil {
		return fail(proto.ErrRemote, err.Error(), req.Host)
	}
	resp := ok(proto.OpPing, req.Host)
	resp.Remote = h.Addr
	return resp
}

func ok(action, host string) proto.Response {
	return proto.Response{OK: true, Action: action, Host: host}
}

func fail(code, msg, host string) proto.Response {
	return proto.Response{
		OK:    false,
		Host:  host,
		Error: &proto.Error{Code: code, Message: strings.TrimSpace(msg), Host: host},
	}
}

// redactResponse scrubs every known secret from anything leaving the daemon.
func (s *Server) redactResponse(r *proto.Response) {
	if r.Error != nil {
		r.Error.Message = s.redact(r.Error.Message)
	}
	r.Stdout = s.redact(r.Stdout)
	r.Stderr = s.redact(r.Stderr)
	r.Local = s.redact(r.Local)
	r.Remote = s.redact(r.Remote)
}

func (s *Server) redact(in string) string {
	for _, sec := range s.secrets {
		if sec != "" {
			in = strings.ReplaceAll(in, sec, "***")
		}
	}
	return in
}

func (s *Server) record(req proto.Request, resp proto.Response) {
	code := 0
	if resp.ExitCode != nil {
		code = *resp.ExitCode
	}
	errCode := ""
	if resp.Error != nil {
		errCode = resp.Error.Code
	}
	s.log.Printf("op=%s host=%q elevated=%v ok=%v exit=%d err=%q dur=%dms bytes=%d",
		req.Op, req.Host, resp.Elevated, resp.OK, code, errCode, resp.DurationMs, resp.Bytes)

	s.audit.write(auditEntry{
		Time:       time.Now().UTC().Format(time.RFC3339Nano),
		Op:         req.Op,
		Host:       req.Host,
		Command:    s.redact(strings.Join(req.Command, " ")),
		Local:      s.redact(req.Local),
		Remote:     s.redact(req.Remote),
		Elevated:   resp.Elevated,
		Approval:   resp.Approval,
		OK:         resp.OK,
		ExitCode:   code,
		ErrorCode:  errCode,
		DurationMs: resp.DurationMs,
		Bytes:      resp.Bytes,
	})
}
