package daemon

import (
	"errors"
	"fmt"

	"github.com/nonex-code/ai-ssh/internal/proto"
)

// CodedError carries a protocol error code alongside the message.
type CodedError struct {
	Code string
	Msg  string
}

func (e *CodedError) Error() string { return e.Msg }

func errf(code, format string, args ...any) *CodedError {
	return &CodedError{Code: code, Msg: fmt.Sprintf(format, args...)}
}

// codeOf extracts a protocol error code from an error chain.
func codeOf(err error) (string, string) {
	if err == nil {
		return "", ""
	}
	var ce *CodedError
	if errors.As(err, &ce) {
		return ce.Code, ce.Msg
	}
	var hk hostKeyError
	if errors.As(err, &hk) {
		return proto.ErrHostKey, hk.Error()
	}
	var mm *HostKeyMismatchError
	if errors.As(err, &mm) {
		return proto.ErrHostKey, mm.Error()
	}
	return proto.ErrInternal, err.Error()
}

// HostKeyMismatchError is a host presenting a key other than the one the
// connection pinned: the server was reinstalled or re-keyed, the address now
// reaches another machine, or something is intercepting the connection. A front
// end can show both fingerprints and let the human re-pin after checking.
type HostKeyMismatchError struct {
	Host string
	Got  string
	Want string
}

func (e *HostKeyMismatchError) Error() string {
	return fmt.Sprintf("host key mismatch for %s: got %s, want %s", e.Host, e.Got, e.Want)
}

// hostKeyError marks a failure produced by our own host key callback so the
// caller can tell it apart from transport or auth failures.
type hostKeyError struct{ err error }

func (e hostKeyError) Error() string { return e.err.Error() }
func (e hostKeyError) Unwrap() error { return e.err }
