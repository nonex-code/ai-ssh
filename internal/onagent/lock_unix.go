//go:build !windows

package onagent

import (
	"os"
	"syscall"
)

// lock is an advisory file lock that disappears with the process, so a crash
// never leaves a stale "somebody is unlocking" marker.
type lock struct{ f *os.File }

func tryLock(path string) (*lock, bool) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, false
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, false
	}
	return &lock{f: f}, true
}

func (l *lock) Close() {
	if l == nil || l.f == nil {
		return
	}
	_ = syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	_ = l.f.Close()
	l.f = nil
}
