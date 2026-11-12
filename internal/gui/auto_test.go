package gui

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/nonex-code/ai-ssh/internal/proto"
)

// The on-demand agent draws two windows without the main App: the password
// prompt and the approval dialog. Rendering them proves they fit and are not
// blank, the same way the rest of the UI is tested.
func TestAutoPromptViewRenders(t *testing.T) {
	for _, create := range []bool{false, true} {
		u := &autoUI{prompt: &promptState{create: create, done: make(chan promptAnswer, 1)}}
		tt := ui.NewTesterWith(func(c *ui.Context) { u.promptView(c) }, 460, 280, ui.TesterOptions{})
		img := tt.Image()
		if img == nil {
			t.Fatal("no image")
		}
		if got := distinctColors(img.Pix, 4096); got < 3 {
			t.Fatalf("create=%v: the frame looks blank (%d colours)", create, got)
		}
	}
}

func TestAutoApprovalViewRenders(t *testing.T) {
	u := &autoUI{pending: []proto.Approval{{
		ID:       "a1",
		Op:       proto.OpExec,
		Host:     "prod",
		Command:  "systemctl restart api",
		Elevated: true,
		TimeoutS: 120,
	}}}
	tt := ui.NewTesterWith(u.approvalView, 560, 360, ui.TesterOptions{})
	img := tt.Image()
	if img == nil {
		t.Fatal("no image")
	}
	if got := distinctColors(img.Pix, 4096); got < 3 {
		t.Fatalf("the frame looks blank (%d colours)", got)
	}
}
