package daemon

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const transferTimeout = 30 * time.Minute

type auditEntry struct {
	Time       string `json:"ts"`
	Op         string `json:"op"`
	Host       string `json:"host,omitempty"`
	Command    string `json:"command,omitempty"`
	Local      string `json:"local,omitempty"`
	Remote     string `json:"remote,omitempty"`
	Elevated   bool   `json:"elevated,omitempty"`
	Approval   string `json:"approval,omitempty"`
	OK         bool   `json:"ok"`
	ExitCode   int    `json:"exit_code,omitempty"`
	ErrorCode  string `json:"error_code,omitempty"`
	DurationMs int64  `json:"duration_ms"`
	Bytes      int64  `json:"bytes,omitempty"`
}

// auditLog is a best-effort append-only JSONL trail the agent cannot rewrite
// (the daemon owns the file and the client has no path into it).
type auditLog struct {
	mu   sync.Mutex
	path string
	log  *log.Logger
	warn bool
}

func newAuditLog(path string, lg *log.Logger) *auditLog {
	return &auditLog{path: path, log: lg}
}

func (a *auditLog) write(e auditEntry) {
	if a.path == "" {
		return
	}
	line, err := json.Marshal(e)
	if err != nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(a.path), 0o700); err != nil {
		a.warnOnce(err)
		return
	}
	f, err := os.OpenFile(a.path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		a.warnOnce(err)
		return
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		a.warnOnce(err)
	}
}

func (a *auditLog) warnOnce(err error) {
	if a.warn {
		return
	}
	a.warn = true
	if a.log != nil {
		a.log.Printf("audit log disabled (%s): %v", a.path, err)
	}
}
