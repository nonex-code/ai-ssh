package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/nonex-code/ai-ssh/internal/mcp"
	"github.com/nonex-code/ai-ssh/internal/version"
)

// cmdMCP is the AI-facing entry point. It is spawned by the AI host and must
// stay credential free: it only forwards tool calls to the running agent.
func cmdMCP(args []string) int {
	spec := map[string]bool{"endpoint": true, "expose-addr": false}
	f, err := parseFlags(args, spec)
	if err != nil {
		fatalf("%v", err)
	}
	if len(f.pos) > 0 {
		fatalf("unexpected argument %q", f.pos[0])
	}
	ep, err := endpointFor(f)
	if err != nil {
		fatalf("%v", err)
	}

	// stdout is the protocol channel; every diagnostic goes to stderr.
	lg := log.New(os.Stderr, "ai-ssh mcp: ", log.LstdFlags)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	srv := mcp.New(mcp.Config{
		Endpoint:   ep,
		ExposeAddr: f.bool("expose-addr"),
		Version:    version.Value,
		Log:        lg,
		Ensure: func(ctx context.Context) error {
			if err := ensureAgent(ctx, ep); err != nil {
				return fmt.Errorf("%s", unlockMessage(err))
			}
			return nil
		},
	})
	if err := srv.Serve(ctx, os.Stdin, os.Stdout); err != nil {
		lg.Printf("stopped: %v", err)
		return 1
	}
	return 0
}
