package agent

import (
	"context"

	"github.com/nonex-code/ai-ssh/internal/daemon"
)

// Session is an interactive shell on a host, for the window (the human), not
// for the protocol. An AI cannot ask for one: it has no operation that reaches
// the agent's Session methods.
type Session = daemon.Shell

// OpenSession starts a login shell in a PTY. Only the desktop front end calls
// this, in-process.
func (a *Agent) OpenSession(ctx context.Context, alias string, cols, rows int) (*Session, error) {
	return a.srv.OpenShell(ctx, alias, cols, rows)
}
