//go:build windows

package onagent

import (
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// spawn starts `ai-ssh agent --auto` with no console window. The agent is a
// GUI process; it draws the password window itself.
func spawn(o Options) (<-chan error, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(exe, agentArgs(o)...)
	cmd.Env = append(os.Environ(), "AISSH_AGENT_CHILD=1")
	if lf := openAgentLog(); lf != nil {
		cmd.Stdout = lf
		cmd.Stderr = lf
		defer lf.Close()
	}
	// HideWindow must stay false: it sets STARTF_USESHOWWINDOW/SW_HIDE, and
	// Windows applies that to the first ShowWindow call, hiding the password
	// dialog. CREATE_NO_WINDOW already keeps a console from appearing.
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NO_WINDOW | windows.CREATE_NEW_PROCESS_GROUP,
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	return done, nil
}
