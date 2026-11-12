package agent

import (
	"context"

	"github.com/nonex-code/ai-ssh/internal/daemon"
)

// Forward is one port forward. Like Session and Files, it is for the window
// only: an AI cannot open a tunnel through the agent.
type Forward = daemon.Forward

// Forward kinds.
const (
	ForwardLocal   = daemon.ForwardLocal
	ForwardRemote  = daemon.ForwardRemote
	ForwardDynamic = daemon.ForwardDynamic
)

// StartForward opens a tunnel for a tab.
func (a *Agent) StartForward(ctx context.Context, alias, kind, listen, target string) (*Forward, error) {
	return a.srv.StartForward(ctx, alias, kind, listen, target)
}
