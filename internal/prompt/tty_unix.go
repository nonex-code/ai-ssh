//go:build !windows

package prompt

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"
)

// TTYPrompt reads from the controlling terminal, not stdin: the agent may have
// been started with stdin connected to a pipe or nothing at all.
func TTYPrompt(message string) (string, error) {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return "", ErrUnavailable
	}
	defer tty.Close()

	label := "ai-ssh " + strings.TrimSpace(message) + ": "
	if _, err := fmt.Fprint(tty, label); err != nil {
		return "", fmt.Errorf("write prompt: %w", err)
	}
	b, err := term.ReadPassword(int(tty.Fd()))
	fmt.Fprintln(tty)
	if err != nil {
		return "", fmt.Errorf("read password: %w", err)
	}
	if len(b) == 0 {
		return "", errors.New("empty password")
	}
	return string(b), nil
}
