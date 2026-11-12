package agent

import (
	"context"

	"github.com/nonex-code/ai-ssh/internal/daemon"
)

// Files is a remote file browser. Like Session, it is for the window only: no
// protocol operation reaches it, so an AI cannot browse or delete files behind
// the command policy's back.
type Files = daemon.Files

// FileEntry is one row of a remote listing.
type FileEntry = daemon.FileEntry

// Progress reports bytes moved so far.
type Progress = daemon.Progress

// Transfer tunes one upload or download.
type Transfer = daemon.Transfer

// OpenFiles starts an SFTP session for a tab.
func (a *Agent) OpenFiles(ctx context.Context, alias string) (*Files, error) {
	return a.srv.OpenFiles(ctx, alias)
}
