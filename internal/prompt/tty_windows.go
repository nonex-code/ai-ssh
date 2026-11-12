//go:build windows

package prompt

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/windows"
	"golang.org/x/term"
)

// TTYPrompt reads from the console, when the process really has one attached.
//
// The on-demand agent is created with CREATE_NO_WINDOW, and opening CONIN$
// would still find the parent's console and block forever waiting for a key
// nobody sees. GetConsoleMode on the inherited stdin fails in exactly that
// case, so it is the gate.
func TTYPrompt(message string) (string, error) {
	var mode uint32
	if err := windows.GetConsoleMode(windows.Stdin, &mode); err != nil {
		return "", ErrUnavailable
	}
	in := os.Stdin
	label := "ai-ssh " + strings.TrimSpace(message) + ": "
	if out, err := os.OpenFile("CONOUT$", os.O_WRONLY, 0); err == nil {
		fmt.Fprint(out, label)
		out.Close()
	} else {
		fmt.Fprint(os.Stderr, label)
	}
	b, err := term.ReadPassword(int(in.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", fmt.Errorf("read password: %w", err)
	}
	if len(b) == 0 {
		return "", errors.New("empty password")
	}
	return string(b), nil
}
