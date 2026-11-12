//go:build windows

package onagent

import (
	"os"

	"golang.org/x/sys/windows"
)

// lock is an advisory file lock that disappears with the process, so a crash
// never leaves a stale "somebody is unlocking" marker.
type lock struct{ f *os.File }

func tryLock(path string) (*lock, bool) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, false
	}
	var ol windows.Overlapped
	err = windows.LockFileEx(windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, 1, 0, &ol)
	if err != nil {
		f.Close()
		return nil, false
	}
	return &lock{f: f}, true
}

func (l *lock) Close() {
	if l == nil || l.f == nil {
		return
	}
	var ol windows.Overlapped
	_ = windows.UnlockFileEx(windows.Handle(l.f.Fd()), 0, 1, 0, &ol)
	_ = l.f.Close()
	l.f = nil
}
