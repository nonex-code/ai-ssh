package daemon

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log"
	"net"
	"sync"
	"time"

	"github.com/nonex-code/ai-ssh/internal/proto"
)

// approvalHub is the human-in-the-loop channel: front ends (the GUI) attach
// once and receive approval requests; the AI's call blocks until one answers.
//
// The daemon does not trust an attached front end for anything except a yes or
// a no, and it never sends it a credential.
type approvalHub struct {
	mu      sync.Mutex
	clients map[*approvalClient]struct{}
	pending map[string]*pendingApproval
	timeout time.Duration
	log     *log.Logger
}

type approvalClient struct {
	conn net.Conn
	send chan proto.Event
	role string
}

// pendingApproval remembers who was asked, so the close event goes to the
// same window and a late-attached front end is not left with a stale dialog.
type pendingApproval struct {
	ch       chan proto.Decision
	audience []*approvalClient
}

func newApprovalHub(timeout time.Duration, lg *log.Logger) *approvalHub {
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	return &approvalHub{
		clients: map[*approvalClient]struct{}{},
		pending: map[string]*pendingApproval{},
		timeout: timeout,
		log:     lg,
	}
}

// serve runs one attached connection until it closes. role is proto.RoleGUI
// or proto.RoleAgent; empty means GUI.
func (h *approvalHub) serve(conn net.Conn, role string) {
	if role == "" {
		role = proto.RoleGUI
	}
	c := &approvalClient{conn: conn, send: make(chan proto.Event, 16), role: role}
	h.mu.Lock()
	h.clients[c] = struct{}{}
	n := len(h.clients)
	h.mu.Unlock()
	if h.log != nil {
		h.log.Printf("front end attached (%d connected)", n)
	}

	defer func() {
		h.mu.Lock()
		delete(h.clients, c)
		left := len(h.clients)
		h.mu.Unlock()
		_ = conn.Close()
		if left == 0 {
			// Nobody can answer any more: refuse instead of leaving the agent
			// waiting for the timeout.
			h.abortAll()
		}
		if h.log != nil {
			h.log.Printf("front end detached (%d connected)", left)
		}
	}()

	enc := json.NewEncoder(conn)
	enc.SetEscapeHTML(false)
	// Acknowledge so the client knows the stream is live.
	_ = enc.Encode(proto.Response{OK: true, Action: proto.OpAttach})

	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		dec := json.NewDecoder(conn)
		for {
			var d proto.Decision
			if err := dec.Decode(&d); err != nil {
				return
			}
			if d.Op == proto.OpDecide {
				h.answer(d)
			}
		}
	}()

	for {
		select {
		case ev := <-c.send:
			_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := enc.Encode(ev); err != nil {
				return
			}
		case <-readerDone:
			// Deliver anything still queued before leaving.
			for {
				select {
				case ev := <-c.send:
					_ = enc.Encode(ev)
				default:
					return
				}
			}
		}
	}
}

// ask publishes an approval request and waits for a decision, a timeout or the
// caller giving up. It returns the protocol error code when refused.
//
// The request goes to the desktop window when one is attached: its window is
// the human's, and the on-demand agent only draws dialogs when no GUI is
// running. With no front end at all the answer is immediate.
func (h *approvalHub) ask(ctx context.Context, ap proto.Approval) (bool, string) {
	h.mu.Lock()
	audience := h.audienceLocked()
	if len(audience) == 0 {
		h.mu.Unlock()
		return false, proto.ErrApprovalUnavailable
	}
	ch := make(chan proto.Decision, 1)
	h.pending[ap.ID] = &pendingApproval{ch: ch, audience: audience}
	h.mu.Unlock()

	defer func() {
		h.mu.Lock()
		delete(h.pending, ap.ID)
		h.mu.Unlock()
		h.sendTo(audience, proto.Event{Type: proto.EventApprovalClosed, ID: ap.ID})
	}()

	h.sendTo(audience, proto.Event{Type: proto.EventApprovalRequest, Approval: &ap})

	timer := time.NewTimer(h.timeout)
	defer timer.Stop()

	select {
	case d := <-ch:
		if d.Allow {
			return true, ""
		}
		return false, proto.ErrApprovalDenied
	case <-timer.C:
		if h.log != nil {
			h.log.Printf("approval %s for %s timed out after %s", ap.ID, ap.Host, h.timeout)
		}
		return false, proto.ErrApprovalTimeout
	case <-ctx.Done():
		return false, proto.ErrApprovalTimeout
	}
}

// abortAll denies every pending request.
func (h *approvalHub) abortAll() {
	h.mu.Lock()
	chs := make([]chan proto.Decision, 0, len(h.pending))
	for _, p := range h.pending {
		chs = append(chs, p.ch)
	}
	h.mu.Unlock()
	for _, ch := range chs {
		select {
		case ch <- proto.Decision{Allow: false}:
		default:
		}
	}
}

func (h *approvalHub) answer(d proto.Decision) {
	h.mu.Lock()
	p, ok := h.pending[d.ID]
	h.mu.Unlock()
	if !ok {
		return
	}
	select {
	case p.ch <- d:
	default: // already answered
	}
}

// audienceLocked returns the front ends that should see a new approval: every
// attached GUI, or every agent when no GUI is attached. h.mu must be held.
func (h *approvalHub) audienceLocked() []*approvalClient {
	var guis, agents []*approvalClient
	for c := range h.clients {
		if c.role == proto.RoleAgent {
			agents = append(agents, c)
		} else {
			guis = append(guis, c)
		}
	}
	if len(guis) > 0 {
		return guis
	}
	return agents
}

func (h *approvalHub) sendTo(clients []*approvalClient, ev proto.Event) {
	for _, c := range clients {
		select {
		case c.send <- ev:
		default: // a stalled front end must not block the daemon
		}
	}
}

// closeAll drops every front end, which is what locking does.
func (h *approvalHub) closeAll() {
	h.mu.Lock()
	clients := make([]*approvalClient, 0, len(h.clients))
	for c := range h.clients {
		clients = append(clients, c)
	}
	h.mu.Unlock()
	for _, c := range clients {
		_ = c.conn.Close()
	}
}

func newID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return hex.EncodeToString([]byte(time.Now().Format(time.RFC3339Nano)))
	}
	return hex.EncodeToString(b[:])
}
