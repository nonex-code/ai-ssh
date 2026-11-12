// Package gui is the desktop front end: a mygo window around the same agent
// core the CLI uses. It holds the credentials, and the AI reaches them through
// the MCP server or the CLI, never through this window.
package gui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"

	"github.com/nonex-code/ai-ssh/internal/agent"
	"github.com/nonex-code/ai-ssh/internal/appdir"
	"github.com/nonex-code/ai-ssh/internal/client"
	"github.com/nonex-code/ai-ssh/internal/ipc"
	"github.com/nonex-code/ai-ssh/internal/localshell"
	"github.com/nonex-code/ai-ssh/internal/proto"
	"github.com/nonex-code/ai-ssh/internal/update"
)

// Options configures the window.
type Options struct {
	VaultPath string
	Endpoint  ipc.Endpoint
	Version   string
}

type flashStyle int

const (
	flashInfo flashStyle = iota
	flashWarning
	flashSuccess
)

type phase int

const (
	phaseLocked phase = iota
	phaseUnlocking
	phaseReady
)

// App is the window's state. Views read it on the main thread; goroutines
// change it through the mutex and then ask for a frame.
type App struct {
	opts Options

	win   *mygo.Window
	popup *mygo.Window
	quit  chan struct{}

	mu         sync.Mutex
	phase      phase
	needsVault bool
	errMsg     string
	hosts      []proto.HostInfo
	auditPath  string
	activity   []auditEntry
	copied     bool
	pending    []proto.Approval

	// main-thread only
	password string
	confirm  string

	// Terminal tabs. Main thread only.
	tabs      []*terminalTab
	activeTab int // -1 shows the start page
	nextTab   int
	// tabDrop is the insertion point while an open tab is being dragged.
	tabDrop tabDropTarget

	// Connection editor. Main thread only.
	editor    *mygo.Window
	editAlias string
	editErr   string
	saving    bool
	draft     editorDraft

	// Quick connect and import, on the start page.
	quick         string
	flash         string
	flashStyle    flashStyle
	connectOnSave bool

	// The connection form's test button state. Main thread only.
	testing bool
	testMsg string
	testOK  bool

	// prefs is the appearance the user chose; main thread only, like the
	// settings window it edits.
	prefs       themePrefs
	settings    *mygo.Window
	settingsTab string
	activityWin *mygo.Window
	servers     *mygo.Window
	serversErr  string
	hostKeyWin  *mygo.Window
	hostKeyReq  *hostKeyRequest
	confirmWin  *mygo.Window
	confirmReq  *confirmRequest
	serverQuery string

	// Changing the master password, inline in the settings window. Main thread
	// only.
	passwdOld    string
	passwdNew    string
	passwdAgain  string
	passwdErr    string
	passwdDone   bool
	passwdSaving bool

	// Server groups. collapsedGroups keeps the manager's sections folded; the
	// group dialog creates or renames one. Main thread only.
	collapsedGroups map[string]bool
	groupWin        *mygo.Window
	groupDlg        groupDialog

	// Update state, main thread only. The check and download run in goroutines.
	upd               *update.Release
	updChecking       bool
	updInstalling     bool
	updDone           bool
	updErr            string
	updLastCheck      time.Time
	autoCheckUpdate   bool
	autoInstallUpdate bool

	localShells []localshell.Shell

	// recent is the home page's connection history, newest first. Main thread
	// only; saved in gui.json.
	recent []recentConn

	ag     *agent.Agent
	cancel context.CancelFunc

	// Transfers run one at a time, across tabs.
	q           *transferQueue
	queueCancel context.CancelFunc
	libReady    bool
	lastTitle   string
	lastDrop    *dragSelection

	uiMu    sync.Mutex
	uiConn  net.Conn
	decider func(proto.Decision)

	// copyText is swapped in tests so no system clipboard is needed.
	copyText func(string)
}

// Run opens the window and blocks until the app quits.
func Run(opts Options) error {
	if opts.Endpoint.Addr == "" {
		ep, err := ipc.Default()
		if err != nil {
			return err
		}
		opts.Endpoint = ep
	}
	a := &App{
		opts:      opts,
		quit:      make(chan struct{}),
		activeTab: -1,
		q:         newTransferQueue(),
	}
	a.needsVault = needsBootstrap(opts.VaultPath)
	a.copyText = func(s string) {
		_ = mygo.Clipboard.Write(transfer.TextData(s))
	}

	state := loadWindowState(statePath(opts.VaultPath))
	a.prefs = state.themePrefs()
	setLanguage(a.prefs.Language)
	a.recent = state.Recent
	a.autoCheckUpdate = state.autoCheckUpdates()
	a.autoInstallUpdate = state.AutoInstallUpdate
	a.updLastCheck = state.LastUpdateCheck
	// A saved appearance must be in effect before the first window exists.
	mygo.Theme.SetSource(mygo.ThemeSource(a.prefs.source()))
	wopts := mygo.WindowOptions{
		Title:     "ai-ssh",
		Width:     state.Width,
		Height:    state.Height,
		X:         state.X,
		Y:         state.Y,
		MinWidth:  minWidth,
		MinHeight: minHeight,
		// The tab strip is the title bar: the native bar is hidden, and its
		// buttons stay over the strip (Windows 11 draws them, snap layouts
		// included). The view keeps clear of them via Context.TitleBar.
		TitleBarStyle:  mygo.TitleBarHidden,
		TitleBarHeight: int(titleBarH),
		Content:        ui.View(a.view),
	}

	update.CleanupOld()
	mygo.App.WhenReady(func() {
		// The app's locale is available now; resolve "follow system" again.
		setLanguage(a.prefs.Language)
		setDockIcon()
		win := mygo.NewWindow(wopts)
		a.setWin(win)
		setWindowIcon(win)
		win.OnClose(func(e *mygo.CloseEvent) {
			a.saveGeometry()
			// Closing the main window is a lock: keys go, the agent stops.
			a.lock()
		})
		go a.auditLoop()
		go a.warmTerminalLibrary()
		go a.detectLocalShells()
		a.startUpdateCheck()
	})
	return mygo.App.Run()
}

func (a *App) setWin(w *mygo.Window) {
	a.mu.Lock()
	a.win = w
	a.mu.Unlock()
}

func (a *App) window() *mygo.Window {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.win
}

// saveGeometry remembers the window for the next run.
func (a *App) saveGeometry() {
	w := a.window()
	if w == nil {
		return
	}
	width, height := w.Size()
	x, y := w.Position()
	st := windowState{
		Width:     width,
		Height:    height,
		X:         x,
		Y:         y,
		Maximized: w.IsMaximized(),
	}
	st.setThemePrefs(a.prefs)
	st.Recent = a.recent
	st.AutoCheckUpdate = &a.autoCheckUpdate
	st.AutoInstallUpdate = a.autoInstallUpdate
	st.LastUpdateCheck = a.updLastCheck
	if st.Width < minWidth {
		st.Width = defaultWidth
	}
	if st.Height < minHeight {
		st.Height = defaultHeight
	}
	if err := saveWindowState(statePath(a.opts.VaultPath), st); err != nil {
		// Geometry is a nicety; never block closing on it.
		_ = err
	}
}

// invalidate asks for a frame. It is a no-op before the window exists, which
// is what makes the views testable headless.
func (a *App) invalidate() {
	w := a.window()
	if w == nil {
		return
	}
	mygo.RunOnMain(func() { w.Invalidate() })
}

// onMain runs fn on the main thread in the real app, and inline when there is
// no window (tests).
func (a *App) onMain(fn func()) {
	if a.window() == nil {
		fn()
		return
	}
	mygo.RunOnMain(fn)
}

// --- state accessors ---------------------------------------------------------

type uiState struct {
	phase      phase
	needsVault bool
	copied     bool
	errMsg     string
	hosts      []proto.HostInfo
	groups     []string
	activity   []auditEntry
	auditPath  string
	serversErr string
	recent     []recentConn
	pending    []proto.Approval
}

func (a *App) snapshot() uiState {
	a.mu.Lock()
	defer a.mu.Unlock()
	var groups []string
	if a.ag != nil {
		groups = append(groups, a.ag.Config().Defaults.Groups...)
	}
	return uiState{
		phase:      a.phase,
		needsVault: a.needsVault,
		copied:     a.copied,
		errMsg:     a.errMsg,
		hosts:      append([]proto.HostInfo(nil), a.hosts...),
		groups:     groups,
		activity:   append([]auditEntry(nil), a.activity...),
		auditPath:  a.auditPath,
		serversErr: a.serversErr,
		recent:     append([]recentConn(nil), a.recent...),
		pending:    append([]proto.Approval(nil), a.pending...),
	}
}

func (a *App) curPhase() phase {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.phase
}

func (a *App) setPhase(p phase) {
	a.mu.Lock()
	a.phase = p
	a.mu.Unlock()
}

func (a *App) setErr(msg string) {
	a.mu.Lock()
	a.errMsg = msg
	a.mu.Unlock()
}

// --- unlock / lock -----------------------------------------------------------

// unlock runs the (slow) key derivation off the main thread. The vault is only
// ever opened here, inside the GUI process.
func (a *App) unlock() {
	if a.curPhase() == phaseUnlocking {
		return
	}
	pw := a.password
	confirm := a.confirm
	if strings.TrimSpace(pw) == "" {
		a.setErr(tr("请输入主密码"))
		a.invalidate()
		return
	}
	a.setPhase(phaseUnlocking)
	a.setErr("")
	a.invalidate()

	go func() {
		ag, err := agent.Open(agent.Options{
			VaultPath: a.opts.VaultPath,
			Endpoint:  a.opts.Endpoint,
			Logger:    log.New(io.Discard, "", 0),
			Prompt: func(msg string) (string, error) {
				if strings.Contains(msg, "repeat") {
					return confirm, nil
				}
				return pw, nil
			},
		})
		if err != nil {
			a.onMain(func() {
				a.setPhase(phaseLocked)
				a.setErr(humanError(err))
				a.invalidate()
			})
			return
		}

		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			// If an on-demand agent is already serving this endpoint (the CLI
			// started one while the window was locked), the window takes over:
			// it is the front end the human is looking at, and two listeners
			// cannot share one endpoint. OpLock is answered by any agent.
			a.takeOverEndpoint(ctx)
			_ = ag.Serve(ctx)
		}()

		qctx, qcancel := context.WithCancel(context.Background())
		go a.runQueue(qctx)

		hosts, audit := a.bootstrapInfo(ctx, ag)
		if conn := a.attach(ctx); conn != nil {
			a.onMain(func() {
				a.uiMu.Lock()
				a.uiConn = conn
				a.decider = a.sendOn(conn)
				a.uiMu.Unlock()
			})
		}
		a.onMain(func() {
			a.mu.Lock()
			a.ag = ag
			a.cancel = cancel
			a.queueCancel = qcancel
			a.hosts = hosts
			a.auditPath = audit
			a.errMsg = ""
			a.phase = phaseReady
			a.mu.Unlock()
			a.password, a.confirm = "", ""
			a.invalidate()
		})
	}()
}

// bootstrapInfo asks the freshly started agent for its host list and works out
// where its audit log lives.
func (a *App) bootstrapInfo(ctx context.Context, ag *agent.Agent) ([]proto.HostInfo, string) {
	select {
	case <-ag.Ready():
	case <-time.After(5 * time.Second):
		return nil, ""
	}
	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	resp := client.Call(pingCtx, a.opts.Endpoint, proto.Request{Op: proto.OpHosts})

	audit := ag.Config().Defaults.AuditLog
	if audit == "" {
		if p, err := appdir.AuditLogPath(); err == nil {
			audit = p
		}
	}
	return resp.Hosts, audit
}

// lock drops the keys and the socket, and puts the window back to the unlock
// screen.
func (a *App) lock() {
	// Answer whatever is waiting before the connection goes away: the daemon
	// must not leave the agent blocked on a prompt that no longer exists.
	a.denyAllPending()

	a.mu.Lock()
	cancel, ag := a.cancel, a.ag
	qcancel := a.queueCancel
	a.cancel, a.ag, a.queueCancel = nil, nil, nil
	a.phase = phaseLocked
	a.hosts = nil
	a.activity = nil
	a.pending = nil
	a.errMsg = ""
	a.mu.Unlock()

	if qcancel != nil {
		qcancel()
	}
	if cancel != nil {
		cancel()
	}
	if ag != nil {
		go ag.Lock()
	}
	a.uiMu.Lock()
	if a.uiConn != nil {
		_ = a.uiConn.Close()
	}
	a.uiConn, a.decider = nil, nil
	a.uiMu.Unlock()

	// Sessions are the human's, not the agent's: locking drops the keys and
	// the endpoint but leaves open shells running, so a long task is not
	// killed by a lock. Their tabs stay (behind the unlock screen).
	a.password, a.confirm = "", ""
	a.closeActivity()
	a.closeServers()
	a.closeHostKey()
	a.closeConfirm()
	a.closeGroupDialog()
	a.closeSettings()
	a.resetPasswd()
	a.syncApprovalWindow()
	a.invalidate()
}

// takeOverEndpoint asks an already-running agent to stop, then waits for the
// endpoint to disappear so this process's Serve can bind it.
func (a *App) takeOverEndpoint(ctx context.Context) {
	dctx, dcancel := context.WithTimeout(ctx, 600*time.Millisecond)
	conn, err := a.opts.Endpoint.Dial(dctx)
	dcancel()
	if err != nil {
		return
	}
	_ = json.NewEncoder(conn).Encode(proto.Request{Op: proto.OpLock})
	_ = conn.Close()
	log.Printf("asked the running agent to step aside for the window")
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		dctx, dcancel := context.WithTimeout(ctx, 300*time.Millisecond)
		c, derr := a.opts.Endpoint.Dial(dctx)
		dcancel()
		if derr != nil {
			return
		}
		_ = c.Close()
		select {
		case <-ctx.Done():
			return
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// --- approval channel --------------------------------------------------------

// attach opens the long-lived event stream the daemon pushes approvals on.
func (a *App) attach(ctx context.Context) net.Conn {
	dialCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	conn, err := a.opts.Endpoint.Dial(dialCtx)
	if err != nil {
		return nil
	}
	if err := json.NewEncoder(conn).Encode(proto.Request{Op: proto.OpAttach}); err != nil {
		_ = conn.Close()
		return nil
	}
	dec := json.NewDecoder(conn)
	var ack proto.Response
	if err := dec.Decode(&ack); err != nil || !ack.OK {
		_ = conn.Close()
		return nil
	}
	go a.readEvents(conn, dec)
	return conn
}

func (a *App) readEvents(conn net.Conn, dec *json.Decoder) {
	defer conn.Close()
	for {
		var ev proto.Event
		if err := dec.Decode(&ev); err != nil {
			return
		}
		switch ev.Type {
		case proto.EventApprovalRequest:
			if ev.Approval != nil {
				ap := *ev.Approval
				a.onMain(func() {
					a.mu.Lock()
					a.pending = append(a.pending, ap)
					a.mu.Unlock()
					a.syncApprovalWindow()
				})
			}
		case proto.EventApprovalClosed:
			id := ev.ID
			a.onMain(func() { a.dropPending(id) })
		}
	}
}

// sendOn writes decisions back on the attached connection.
func (a *App) sendOn(conn net.Conn) func(proto.Decision) {
	var mu sync.Mutex
	return func(d proto.Decision) {
		mu.Lock()
		defer mu.Unlock()
		enc := json.NewEncoder(conn)
		enc.SetEscapeHTML(false)
		_ = enc.Encode(d)
	}
}

func (a *App) decide(id string, allow bool) {
	a.uiMu.Lock()
	send := a.decider
	a.uiMu.Unlock()
	if send != nil {
		send(proto.Decision{Op: proto.OpDecide, ID: id, Allow: allow})
	}
	a.dropPending(id)
}

func (a *App) dropPending(id string) {
	a.mu.Lock()
	out := a.pending[:0]
	for _, ap := range a.pending {
		if ap.ID != id {
			out = append(out, ap)
		}
	}
	a.pending = out
	a.mu.Unlock()
	a.syncApprovalWindow()
	a.invalidate()
}

// --- host actions ------------------------------------------------------------

func (a *App) copyMCPConfig() {
	snippet := a.mcpSnippet()
	a.copyText(snippet)
	a.mu.Lock()
	a.copied = true
	a.mu.Unlock()
	a.invalidate()
	go func() {
		time.Sleep(2 * time.Second)
		a.onMain(func() {
			a.mu.Lock()
			a.copied = false
			a.mu.Unlock()
			a.invalidate()
		})
	}()
}

func (a *App) mcpSnippet() string {
	exe, err := os.Executable()
	if err != nil {
		exe = "ai-ssh"
	}
	return fmt.Sprintf(`{
  "mcpServers": {
    "ssh": {
      "command": %q,
      "args": ["mcp"]
    }
  }
}`, exe)
}

// --- audit ---------------------------------------------------------------

// auditLoop refreshes the activity panel while the vault is unlocked.
func (a *App) auditLoop() {
	ticker := time.NewTicker(1500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-a.quit:
			return
		case <-ticker.C:
		}
		a.mu.Lock()
		ready := a.phase == phaseReady
		path := a.auditPath
		a.mu.Unlock()
		if !ready || path == "" {
			continue
		}
		entries := readAudit(path, 80)
		a.mu.Lock()
		changed := len(entries) != len(a.activity)
		if !changed && len(entries) > 0 {
			changed = entries[len(entries)-1].Time != a.activity[len(a.activity)-1].Time ||
				entries[len(entries)-1].Command != a.activity[len(a.activity)-1].Command
		}
		a.activity = entries
		a.mu.Unlock()
		if changed {
			a.invalidate()
			a.invalidateActivity()
			a.invalidateServers()
		}
	}
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// needsBootstrap reports whether the window must offer to create the vault.
func needsBootstrap(vaultPath string) bool { return !fileExists(vaultPath) }

func humanError(err error) string {
	switch {
	case strings.Contains(err.Error(), "wrong master password"):
		return tr("主密码不正确")
	case strings.Contains(err.Error(), "do not match"):
		return tr("两次输入的主密码不一致")
	case strings.Contains(err.Error(), "at least 8"):
		return tr("主密码至少 8 位")
	case strings.Contains(err.Error(), "same as the old"):
		return tr("新主密码不能与当前密码相同")
	}
	return err.Error()
}
