package onagent

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/nonex-code/ai-ssh/internal/ipc"
)

func testEndpoint(t *testing.T) ipc.Endpoint {
	t.Helper()
	if runtime.GOOS == "windows" {
		return ipc.FromAddr(fmt.Sprintf(`\\.\pipe\ai-ssh-test-%d`, rand.Int63()))
	}
	return ipc.FromAddr(filepath.Join(t.TempDir(), "agent.sock"))
}

func TestEnsureFindsARunningAgent(t *testing.T) {
	ep := testEndpoint(t)
	ln, err := ep.Listen()
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := Ensure(ctx, Options{Endpoint: ep, NoAutostart: true}); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
}

func TestEnsureWithoutAutostartFailsFast(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	start := time.Now()
	err := Ensure(ctx, Options{Endpoint: testEndpoint(t), NoAutostart: true, Timeout: time.Minute})
	if !errors.Is(err, ErrNoAutostart) {
		t.Fatalf("expected ErrNoAutostart, got %v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatalf("probe took %s", time.Since(start))
	}
}

func TestUnlockLockIsExclusive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.lock")
	a, ok := tryLock(path)
	if !ok {
		t.Fatal("first lock failed")
	}
	if _, ok := tryLock(path); ok {
		t.Fatal("the second lock must not be granted")
	}
	a.Close()
	b, ok := tryLock(path)
	if !ok {
		t.Fatal("the lock was not released")
	}
	b.Close()
}

func TestSpawnLockPathDiffersPerVault(t *testing.T) {
	a, err := spawnLockPath("/tmp/a")
	if err != nil {
		t.Fatal(err)
	}
	b, err := spawnLockPath("/tmp/b")
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatalf("two vaults share %s", a)
	}
}

func TestAgentArgs(t *testing.T) {
	ep := testEndpoint(t)
	got := agentArgs(Options{VaultPath: "/tmp/v", Endpoint: ep})
	want := []string{"agent", "--auto", "--vault", "/tmp/v", "--endpoint", ep.Addr}
	if len(got) != len(want) {
		t.Fatalf("args = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("args[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
