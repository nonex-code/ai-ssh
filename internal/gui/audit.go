package gui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// auditEntry mirrors the JSONL the daemon appends. The GUI only reads it: the
// daemon owns the file, so an agent cannot rewrite history.
type auditEntry struct {
	Time       string `json:"ts"`
	Op         string `json:"op"`
	Host       string `json:"host"`
	Command    string `json:"command"`
	Local      string `json:"local"`
	Remote     string `json:"remote"`
	Elevated   bool   `json:"elevated"`
	OK         bool   `json:"ok"`
	ExitCode   int    `json:"exit_code"`
	ErrorCode  string `json:"error_code"`
	DurationMs int64  `json:"duration_ms"`
	Bytes      int64  `json:"bytes"`
}

// readAudit returns the last n entries, reading only the tail of the file so a
// long-lived audit log stays cheap to poll.
func readAudit(path string, n int) []auditEntry {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return nil
	}
	const tailBytes = 256 << 10
	midFile := false
	if fi.Size() > tailBytes {
		if _, err := f.Seek(fi.Size()-tailBytes, io.SeekStart); err != nil {
			return nil
		}
		midFile = true
	}
	data, err := io.ReadAll(io.LimitReader(f, tailBytes))
	if err != nil {
		return nil
	}

	lines := bytes.Split(data, []byte("\n"))
	if midFile && len(lines) > 0 {
		lines = lines[1:] // the first one is the tail of a record
	}

	out := make([]auditEntry, 0, n)
	for _, line := range lines {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var e auditEntry
		if json.Unmarshal(line, &e) != nil {
			continue
		}
		out = append(out, e)
	}
	if len(out) > n {
		out = out[len(out)-n:]
	}
	return out
}

// summary renders one line for the activity panel.
func (e auditEntry) summary() string {
	ts := e.Time
	if t, err := time.Parse(time.RFC3339Nano, e.Time); err == nil {
		ts = t.Local().Format("15:04:05")
	}
	host := e.Host
	if host == "" {
		host = "-"
	}
	status := "ok"
	if !e.OK {
		status = e.ErrorCode
		if status == "" {
			status = fmt.Sprintf("exit=%d", e.ExitCode)
		}
	}
	what := e.Command
	if what == "" && e.Local != "" {
		what = strings.TrimSpace(e.Local + " → " + e.Remote)
	}
	if len([]rune(what)) > 130 {
		what = string([]rune(what)[:130]) + "…"
	}
	flag := " "
	if e.Elevated {
		flag = "sudo"
	}
	return fmt.Sprintf("%s  %-10s %-8s %-5s %-12s %6dms  %s",
		ts, host, e.Op, flag, status, e.DurationMs, what)
}
