package gui

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/nonex-code/ai-ssh/internal/agent"
	"github.com/nonex-code/ai-ssh/internal/ipc"
	"github.com/nonex-code/ai-ssh/internal/prompt"
	"github.com/nonex-code/ai-ssh/internal/proto"
)

// AutoOptions configures the on-demand agent (`ai-ssh agent --auto`).
//
// The agent is started by a client (CLI or MCP) when nothing is listening, asks
// the human for the master password with the app's own window, serves the
// request, and exits when the idle timeout fires.
type AutoOptions struct {
	VaultPath       string
	Endpoint        ipc.Endpoint
	AuditPath       string
	IdleLock        time.Duration
	DisableIdleLock bool
	Logger          *log.Logger
}

// errUserCanceled closes the prompt without a password.
var errUserCanceled = errors.New("unlock canceled")

// RunAuto runs an on-demand agent. It returns when the agent stops (idle,
// lock, or an unlock failure).
//
// Order of prompts, as everywhere else: AISSH_MASTER_PASSWORD, the app window
// when a display exists, the controlling terminal, an askpass helper. The
// window is the process's main window loop, so this function must run on the
// main goroutine (as gui.Run does).
func RunAuto(opts AutoOptions) error {
	if opts.Logger == nil {
		opts.Logger = log.New(os.Stderr, "ai-ssh agent: ", log.LstdFlags)
	}
	if opts.Endpoint.Addr == "" {
		ep, err := ipc.Default()
		if err != nil {
			return err
		}
		opts.Endpoint = ep
	}
	if pw := os.Getenv("AISSH_MASTER_PASSWORD"); pw != "" {
		return serveAutoHeadless(opts, func(string) (string, error) { return pw, nil })
	}
	// AISSH_HEADLESS is for servers where a display exists but nobody is
	// looking at it (X11 forwarding, CI) and for testing the terminal chain.
	if guiAvailable() && os.Getenv("AISSH_HEADLESS") == "" {
		served, err := runAutoGUI(opts)
		if served {
			return err
		}
		if err != nil {
			opts.Logger.Printf("window unavailable (%v); asking the terminal instead", err)
		}
	}
	return serveAutoHeadless(opts, agent.Prompter(prompt.Chain()))
}

// serveAutoHeadless is the no-window agent: it still never exposes a
// credential to the client, and approvals are unavailable unless a desktop
// window attaches.
func serveAutoHeadless(opts AutoOptions, p agent.Prompter) error {
	ag, err := agent.Open(agent.Options{
		VaultPath:       opts.VaultPath,
		Endpoint:        opts.Endpoint,
		Logger:          opts.Logger,
		Prompt:          p,
		AuditPath:       opts.AuditPath,
		IdleLock:        opts.IdleLock,
		DisableIdleLock: opts.DisableIdleLock,
	})
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return ag.Serve(ctx)
}

// runAutoGUI starts the mygo loop. served is false only when the platform
// could not start the app at all (no display, missing GTK), which lets the
// caller fall back to a terminal prompt in the same process.
func runAutoGUI(opts AutoOptions) (served bool, err error) {
	u := &autoUI{opts: opts}
	st := loadWindowState(statePath(opts.VaultPath))
	u.prefs = st.themePrefs()
	setLanguage(u.prefs.Language)
	mygo.Theme.SetSource(mygo.ThemeSource(u.prefs.source()))
	// Closing the password window must not quit: the agent lives on to serve
	// the request and to draw approval dialogs.
	mygo.App.OnWindowAllClosed(func() {})

	ready := false
	done := make(chan error, 1)
	mygo.App.WhenReady(func() {
		ready = true
		opts.Logger.Printf("window loop ready")
		go func() { done <- u.run() }()
	})
	runErr := mygo.App.Run()
	opts.Logger.Printf("window loop stopped: %v (ready=%v)", runErr, ready)
	if !ready {
		return false, runErr
	}
	select {
	case e := <-done:
		return true, e
	case <-time.After(5 * time.Second):
		return true, runErr
	}
}

// autoUI owns the windows of the on-demand agent. Main thread only, except
// where a mutex says otherwise.
type autoUI struct {
	opts  AutoOptions
	prefs themePrefs

	mu       sync.Mutex
	ag       *agent.Agent
	cancel   context.CancelFunc
	conn     net.Conn
	encMu    sync.Mutex
	pending  []proto.Approval
	approval *mygo.Window

	prompt *promptState
}

type promptState struct {
	create   bool
	password string
	confirm  string
	errMsg   string
	win      *mygo.Window
	done     chan promptAnswer
	settled  bool
}

type promptAnswer struct {
	password string
	err      error
}

func (u *autoUI) run() error {
	vp := u.opts.VaultPath
	if needsBootstrap(vp) {
		pw, err := u.ask(true)
		if err != nil {
			u.fail(err)
			return err
		}
		if err := agent.CreateVault(vp, func(string) (string, error) { return pw, nil }); err != nil {
			u.fail(err)
			return err
		}
	}

	ag, err := agent.Open(agent.Options{
		VaultPath:       vp,
		Endpoint:        u.opts.Endpoint,
		Logger:          u.opts.Logger,
		Prompt:          u.askUnlock,
		AuditPath:       u.opts.AuditPath,
		IdleLock:        u.opts.IdleLock,
		DisableIdleLock: u.opts.DisableIdleLock,
	})
	if err != nil {
		u.fail(err)
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	u.mu.Lock()
	u.ag, u.cancel = ag, cancel
	u.mu.Unlock()
	defer cancel()

	// The front-end stream can only attach once the endpoint listens; the
	// daemon tells us through Ready. Serve must run first: Open only prepares
	// the listener.
	serveErr := make(chan error, 1)
	go func() { serveErr <- ag.Serve(ctx) }()
	select {
	case <-ag.Ready():
	case <-time.After(5 * time.Second):
		u.opts.Logger.Printf("endpoint did not become ready")
		mygo.App.Quit()
		return errors.New("agent endpoint did not become ready")
	}

	if conn, dec := u.attach(ctx); conn != nil {
		u.mu.Lock()
		u.conn = conn
		u.mu.Unlock()
		defer conn.Close()
		go u.readEvents(dec)
	}

	err = <-serveErr
	mygo.App.Quit()
	return err
}

func (u *autoUI) askUnlock(string) (string, error) { return u.ask(false) }

func (u *autoUI) ask(create bool) (string, error) {
	ch := make(chan promptAnswer, 1)
	if u.opts.Logger != nil {
		u.opts.Logger.Printf("asking the master password (create=%v)", create)
	}
	mygo.RunOnMain(func() { u.showPrompt(create, ch) })
	a := <-ch
	return a.password, a.err
}

// --- password window ---------------------------------------------------------

func (u *autoUI) showPrompt(create bool, ch chan promptAnswer) {
	st := &promptState{create: create, done: ch}
	u.prompt = st
	title := tr("解锁 ai-ssh")
	if create {
		title = tr("创建 ai-ssh vault")
	}
	win := mygo.NewWindow(mygo.WindowOptions{
		Title:         title,
		Width:         460,
		Height:        280,
		MinWidth:      420,
		MinHeight:     240,
		DisableResize: true,
		AlwaysOnTop:   true,
		Content:       ui.View(u.promptView),
	})
	st.win = win
	setWindowIcon(win)
	win.OnClosed(func() {
		if u.prompt == st {
			u.prompt = nil
		}
		if !st.settled {
			st.settled = true
			st.done <- promptAnswer{err: errUserCanceled}
		}
	})
	win.Center()
	// ShowWindow must be called again: when the client was started with
	// SW_HIDE (a hidden PowerShell window, an AI host), the first show of this
	// process inherits that startup state and the dialog would stay invisible.
	win.Show()
	win.Focus()
}

func (u *autoUI) promptView(c *ui.Context) {
	ensureFonts()
	t := appTheme(c.Theme())
	c.SetTheme(t)
	st := u.prompt
	if st == nil {
		return
	}
	c.OnWindowShortcut(0, ui.KeyEscape, func() {
		u.settlePrompt(st, promptAnswer{err: errUserCanceled})
	})

	title := tr("解锁 vault")
	sub := tr("主密码只用于在本机解开加密的服务器配置，不会发送给任何模型。")
	label := tr("解锁")
	if st.create {
		title = tr("创建 vault")
		sub = tr("首次启动会直接创建加密的 vault，不会写出任何明文配置。")
		label = tr("创建并解锁")
	}

	ui.Column(c).Fill().Background(t.Background).Padding(paddingSheet).Gap(gapMid).Children(func() {
		ui.Row(c).Gap(gapSmall).AlignItems(ui.Center).Children(func() {
			logoMark(c, 30)
			ui.Text(c, title).FontSize(fontPage).Bold()
		})
		ui.Text(c, sub).FontSize(fontBody).TextColor(t.TextMuted)

		var pw, confirm ui.Element
		pw = ui.TextInput(c.Key("pw"), &st.password).
			Placeholder(tr("主密码")).Password().AutoFocus()
		if st.create {
			confirm = ui.TextInput(c.Key("pw2"), &st.confirm).
				Placeholder(tr("再次输入主密码")).Password()
		}

		if st.errMsg != "" {
			ui.Text(c, st.errMsg).FontSize(fontBody).TextColor(t.Danger)
		}

		ui.Row(c).Gap(gapMid).Children(func() {
			if ui.PrimaryButton(c.Key("ok"), label).Clicked() {
				u.submitPrompt(st)
			}
		})
		if pw.Submitted() || confirm.Submitted() {
			u.submitPrompt(st)
		}
	})
}

func (u *autoUI) submitPrompt(st *promptState) {
	pw := st.password
	if strings.TrimSpace(pw) == "" {
		st.errMsg = tr("请输入主密码")
		if st.win != nil {
			st.win.Invalidate()
		}
		return
	}
	if st.create {
		if len(pw) < 8 {
			st.errMsg = tr("主密码至少 8 位")
			if st.win != nil {
				st.win.Invalidate()
			}
			return
		}
		if pw != st.confirm {
			st.errMsg = tr("两次输入的主密码不一致")
			if st.win != nil {
				st.win.Invalidate()
			}
			return
		}
	}
	u.settlePrompt(st, promptAnswer{password: pw})
}

func (u *autoUI) settlePrompt(st *promptState, a promptAnswer) {
	if st.settled {
		return
	}
	st.settled = true
	st.done <- a
	if st.win != nil {
		st.win.Close()
	}
}

// --- approval windows --------------------------------------------------------

func (u *autoUI) attach(ctx context.Context) (net.Conn, *json.Decoder) {
	dctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	conn, err := u.opts.Endpoint.Dial(dctx)
	if err != nil {
		return nil, nil
	}
	if err := json.NewEncoder(conn).Encode(proto.Request{Op: proto.OpAttach, Role: proto.RoleAgent}); err != nil {
		conn.Close()
		return nil, nil
	}
	dec := json.NewDecoder(conn)
	var ack proto.Response
	if err := dec.Decode(&ack); err != nil || !ack.OK {
		conn.Close()
		return nil, nil
	}
	return conn, dec
}

func (u *autoUI) readEvents(dec *json.Decoder) {
	for {
		var ev proto.Event
		if err := dec.Decode(&ev); err != nil {
			return
		}
		switch ev.Type {
		case proto.EventApprovalRequest:
			if ev.Approval != nil {
				ap := *ev.Approval
				mygo.RunOnMain(func() { u.pushApproval(ap) })
			}
		case proto.EventApprovalClosed:
			id := ev.ID
			mygo.RunOnMain(func() { u.dropApproval(id) })
		}
	}
}

func (u *autoUI) pushApproval(ap proto.Approval) {
	u.mu.Lock()
	u.pending = append(u.pending, ap)
	u.mu.Unlock()
	u.syncApprovalWindow()
}

func (u *autoUI) dropApproval(id string) {
	u.mu.Lock()
	out := u.pending[:0]
	for _, ap := range u.pending {
		if ap.ID != id {
			out = append(out, ap)
		}
	}
	u.pending = out
	u.mu.Unlock()
	u.syncApprovalWindow()
}

func (u *autoUI) decide(id string, allow bool) {
	u.mu.Lock()
	conn := u.conn
	pending := append([]proto.Approval(nil), u.pending...)
	u.mu.Unlock()
	if conn != nil {
		u.encMu.Lock()
		enc := json.NewEncoder(conn)
		enc.SetEscapeHTML(false)
		_ = enc.Encode(proto.Decision{Op: proto.OpDecide, ID: id, Allow: allow})
		u.encMu.Unlock()
	}
	out := pending[:0]
	for _, ap := range pending {
		if ap.ID != id {
			out = append(out, ap)
		}
	}
	u.mu.Lock()
	u.pending = out
	u.mu.Unlock()
	u.syncApprovalWindow()
}

// syncApprovalWindow keeps one dialog for the oldest pending request. Main
// thread only.
func (u *autoUI) syncApprovalWindow() {
	u.mu.Lock()
	n := len(u.pending)
	u.mu.Unlock()

	if n == 0 {
		if u.approval != nil {
			win := u.approval
			u.approval = nil
			time.AfterFunc(10*time.Millisecond, func() {
				mygo.RunOnMain(func() { win.Close() })
			})
		}
		return
	}
	if u.approval != nil {
		u.approval.Invalidate()
		return
	}

	win := mygo.NewWindow(mygo.WindowOptions{
		Title:         tr("ai-ssh 授权"),
		Width:         560,
		Height:        360,
		MinWidth:      480,
		MinHeight:     300,
		DisableResize: true,
		DisableClose:  true,
		AlwaysOnTop:   true,
		Content:       ui.View(u.approvalView),
	})
	setWindowIcon(win)
	u.approval = win
	win.OnClosed(func() {
		if u.approval == win {
			u.approval = nil
		}
	})
	win.Center()
	time.AfterFunc(150*time.Millisecond, func() {
		mygo.RunOnMain(func() {
			win.Show()
			win.Focus()
		})
	})
}

func (u *autoUI) approvalView(c *ui.Context) {
	ensureFonts()
	t := appTheme(c.Theme())
	c.SetTheme(t)

	u.mu.Lock()
	pending := append([]proto.Approval(nil), u.pending...)
	u.mu.Unlock()

	ui.Column(c).Fill().Padding(22).Gap(14).Background(t.Background).Children(func() {
		if len(pending) == 0 {
			ui.Text(c, tr("已处理")).FontSize(fontBody).TextColor(t.TextMuted)
			return
		}
		ap := pending[0]
		if c.Shortcut(0, ui.KeyEnter) {
			u.decide(ap.ID, true)
		}
		if c.Shortcut(0, ui.KeyEscape) {
			u.decide(ap.ID, false)
		}

		ui.Row(c).Gap(gapMid).Children(func() {
			ui.Text(c, tr("AI 请求授权")).FontSize(fontPage).Bold().TextColor(t.Warning)
			ui.Spacer(c)
			if len(pending) > 1 {
				ui.Textf(c, tr("还有 %d 个"), len(pending)-1).FontSize(fontBody).TextColor(t.TextMuted)
			}
		})
		ui.Text(c, approvalTitle(ap)).FontSize(fontCard)
		ui.Column(c).FillWidth().Padding(12).Radius(8).Gap(gapSmall).Background(t.Surface).Children(func() {
			if ap.Command != "" {
				ui.Text(c, ap.Command).Font(mono).FontSize(fontBody)
			}
			if ap.Local != "" || ap.Remote != "" {
				ui.Textf(c, "%s → %s", ap.Local, ap.Remote).Font(mono).FontSize(fontBody)
			}
			if ap.Reason != "" {
				ui.Text(c, ap.Reason).FontSize(fontSmall).TextColor(t.TextMuted)
			}
		})
		ui.Spacer(c)
		ui.Row(c).Gap(gapMid).Children(func() {
			if ui.PrimaryButton(c.Key("approve"), tr("允许一次")).Clicked() {
				u.decide(ap.ID, true)
			}
			if ui.Button(c.Key("reject"), tr("拒绝")).Clicked() {
				u.decide(ap.ID, false)
			}
			ui.Spacer(c)
			if ap.TimeoutS > 0 {
				ui.Textf(c, tr("%d 秒内无响应将自动拒绝"), ap.TimeoutS).FontSize(fontSmall).TextColor(t.TextMuted)
			}
		})
	})
}

// fail shows why the agent could not start, then quits. The client that
// spawned the agent reports its own error; this window is for the human.
func (u *autoUI) fail(err error) {
	if u.opts.Logger != nil {
		u.opts.Logger.Printf("%v", err)
	}
	// Closing the prompt is not a failure worth another window: the CLI
	// already reports that the user canceled.
	if errors.Is(err, errUserCanceled) {
		mygo.App.Quit()
		return
	}
	msg := humanError(err)
	mygo.RunOnMain(func() {
		var win *mygo.Window
		win = mygo.NewWindow(mygo.WindowOptions{
			Title:         "ai-ssh",
			Width:         480,
			Height:        260,
			MinWidth:      420,
			MinHeight:     220,
			DisableResize: true,
			AlwaysOnTop:   true,
			Content: ui.View(func(c *ui.Context) {
				t := appTheme(c.Theme())
				c.SetTheme(t)
				ui.Column(c).Fill().Background(t.Background).Padding(24).Gap(gapMid).Children(func() {
					ui.Text(c, tr("无法启动 ai-ssh")).FontSize(fontPage).Bold()
					ui.Text(c, msg).FontSize(fontBody).TextColor(t.Danger)
					ui.Text(c, tr("如果密码连续输错，请重新从 AI 侧发起请求。")).FontSize(fontSmall).TextColor(t.TextMuted)
					ui.Spacer(c)
					if ui.PrimaryButton(c.Key("ok"), tr("关闭")).Clicked() {
						win.Close()
					}
				})
			}),
		})
		win.OnClosed(func() { mygo.App.Quit() })
		setWindowIcon(win)
		win.Center()
		win.Show()
		win.Focus()
	})
}
