//go:build !windows

package onagent

import (
	"os"
	"os/exec"
	"syscall"
)

// spawn starts `ai-ssh agent --auto` detached from the client.
//
// When the client has a controlling terminal, the agent keeps the same
// session: that is what lets it prompt on /dev/tty when there is no display.
// Headless callers (an AI host spawning mcp) get a new session, so closing
// their terminal cannot take the agent down mid-request.
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
	cmd.SysProcAttr = &syscall.SysProcAttr{}
	if !ttyAvailable() {
		cmd.SysProcAttr.Setsid = true
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	return done, nil
}

func ttyAvailable() bool {
	f, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return false
	}
	_ = f.Close()
	return true
}
