package gui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/nonex-code/ai-ssh/internal/ipc"
	"github.com/nonex-code/ai-ssh/internal/proto"
)

func testEndpoint(t *testing.T) ipc.Endpoint {
	t.Helper()
	if runtime.GOOS == "windows" {
		return ipc.FromAddr(fmt.Sprintf(`\\.\pipe\ai-ssh-guitest-%d`, time.Now().UnixNano()))
	}
	return ipc.FromAddr(filepath.Join(t.TempDir(), "agent.sock"))
}

// fakeFrontEndServer accepts one attach, pushes an approval and records the
// decision the app sends back.
func fakeFrontEndServer(t *testing.T, decisions chan<- proto.Decision) ipc.Endpoint {
	t.Helper()
	ep := testEndpoint(t)
	ln, err := ep.Listen()
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		dec := json.NewDecoder(conn)
		var req proto.Request
		if err := dec.Decode(&req); err != nil || req.Op != proto.OpAttach {
			return
		}
		enc := json.NewEncoder(conn)
		_ = enc.Encode(proto.Response{OK: true, Action: proto.OpAttach})
		_ = enc.Encode(proto.Event{
			Type: proto.EventApprovalRequest,
			Approval: &proto.Approval{
				ID: "ap-9", Op: proto.OpExec, Host: "prod",
				Command: "systemctl restart nginx", TimeoutS: 120,
			},
		})
		var d proto.Decision
		if err := dec.Decode(&d); err == nil {
			decisions <- d
		}
	}()
	return ep
}

func TestAttachReceivesAndAnswersApprovals(t *testing.T) {
	decisions := make(chan proto.Decision, 1)
	ep := fakeFrontEndServer(t, decisions)

	a := readyApp(demoHosts...)
	a.opts.Endpoint = ep

	conn := a.attach(context.Background())
	if conn == nil {
		t.Fatal("attach returned no connection")
	}
	a.decider = a.sendOn(conn)
	t.Cleanup(func() { _ = conn.Close() })

	// The event arrives on the reader goroutine.
	deadline := time.Now().Add(5 * time.Second)
	for {
		a.mu.Lock()
		n := len(a.pending)
		a.mu.Unlock()
		if n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("approval never showed up")
		}
		time.Sleep(10 * time.Millisecond)
	}

	a.mu.Lock()
	ap := a.pending[0]
	a.mu.Unlock()
	if ap.ID != "ap-9" || ap.Host != "prod" {
		t.Fatalf("pending = %+v", ap)
	}

	a.decide(ap.ID, true)

	select {
	case d := <-decisions:
		if d.ID != "ap-9" || !d.Allow || d.Op != proto.OpDecide {
			t.Fatalf("decision = %+v", d)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the decision never reached the daemon")
	}

	a.mu.Lock()
	left := len(a.pending)
	a.mu.Unlock()
	if left != 0 {
		t.Fatalf("pending = %d", left)
	}
}

func TestAttachFailsWithoutDaemon(t *testing.T) {
	a := readyApp(demoHosts...)
	a.opts.Endpoint = testEndpoint(t) // nothing listening
	if conn := a.attach(context.Background()); conn != nil {
		conn.Close()
		t.Fatal("expected no connection")
	}
}

func TestAuditLoopIgnoresMissingFile(t *testing.T) {
	a := readyApp(demoHosts...)
	a.auditPath = filepath.Join(t.TempDir(), "nope.log")
	if got := readAudit(a.auditPath, 10); got != nil {
		t.Fatalf("got %+v", got)
	}
	_ = os.Remove(a.auditPath)
}
