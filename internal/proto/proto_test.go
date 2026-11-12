package proto

import "testing"

func TestExitCodeContract(t *testing.T) {
	cases := map[string]int{
		ErrUsage:               ExitUsage,
		ErrConfig:              ExitUsage,
		ErrUnknownHost:         ExitUsage,
		ErrDaemonDown:          ExitDaemon,
		ErrLocked:              ExitConnect,
		ErrHostKey:             ExitConnect,
		ErrAuth:                ExitConnect,
		ErrConnect:             ExitConnect,
		ErrUnlockRequired:      ExitConnect,
		ErrElevation:           ExitConnect,
		ErrBlocked:             ExitBlocked,
		ErrPathDenied:          ExitBlocked,
		ErrElevDenied:          ExitBlocked,
		ErrApprovalDenied:      ExitBlocked,
		ErrApprovalUnavailable: ExitBlocked,
		ErrTransfer:            ExitTransfer,
		ErrTimeout:             ExitTimeout,
		ErrApprovalTimeout:     ExitTimeout,
	}
	for code, want := range cases {
		resp := Response{Error: &Error{Code: code}}
		if got := resp.ExitCodeFor(); got != want {
			t.Fatalf("%s: exit %d, want %d", code, got, want)
		}
	}
	if got := (Response{}).ExitCodeFor(); got != ExitOK {
		t.Fatalf("ok: exit %d", got)
	}
	rc := 7
	if got := (Response{ExitCode: &rc}).ExitCodeFor(); got != ExitRemote {
		t.Fatalf("remote: exit %d, want %d", got, ExitRemote)
	}
}
