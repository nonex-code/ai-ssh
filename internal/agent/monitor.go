package agent

import (
	"context"
	"time"

	"github.com/nonex-code/ai-ssh/internal/config"
)

// ExecHuman runs one command for the window, without going through the AI's
// protocol. Only the monitor pane uses it today.
func (a *Agent) ExecHuman(ctx context.Context, alias, command string, timeout time.Duration) (string, string, error) {
	return a.srv.RunHuman(ctx, alias, command, timeout)
}

// TestHost dials a host from the connection form without saving it.
func (a *Agent) TestHost(ctx context.Context, h *config.Host) error {
	return a.srv.TestHost(ctx, h)
}
