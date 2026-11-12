package gui

import (
	"context"
	"fmt"
	"path"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/nonex-code/ai-ssh/internal/agent"
)

// transferJob is one queued upload or download.
type transferJob struct {
	id     int
	up     bool // local -> remote when true
	local  string
	remote string

	resume bool
	dir    bool   // the job moves a whole directory
	count  int64  // files moved, for a directory
	state  string // queued | running | done | failed
	done   int64
	total  int64
	err    string

	files *agent.Files
}

// transferQueue moves files one at a time, so a batch of uploads does not open
// a dozen concurrent channels on the same session.
type transferQueue struct {
	mu   sync.Mutex
	jobs []*transferJob
	ch   chan *transferJob
	next int
}

func newTransferQueue() *transferQueue {
	return &transferQueue{ch: make(chan *transferJob, 64)}
}

// add queues a job and returns its id.
func (q *transferQueue) add(j *transferJob) int {
	q.mu.Lock()
	q.next++
	j.id = q.next
	j.state = "queued"
	q.jobs = append(q.jobs, j)
	if len(q.jobs) > 40 {
		q.jobs = q.jobs[len(q.jobs)-40:]
	}
	q.mu.Unlock()
	q.ch <- j
	return j.id
}

func (q *transferQueue) snapshot(n int) []*transferJob {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.jobs) <= n {
		out := make([]*transferJob, len(q.jobs))
		copy(out, q.jobs)
		return out
	}
	out := make([]*transferJob, n)
	copy(out, q.jobs[len(q.jobs)-n:])
	return out
}

func (q *transferQueue) active() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	n := 0
	for _, j := range q.jobs {
		if j.state == "queued" || j.state == "running" {
			n++
		}
	}
	return n
}

// run is the worker. It stops when ctx is cancelled.
func (a *App) runQueue(ctx context.Context) {
	q := a.q
	for {
		select {
		case <-ctx.Done():
			return
		case job := <-q.ch:
			a.runJob(ctx, job)
		}
	}
}

func (a *App) runJob(ctx context.Context, job *transferJob) {
	a.onMain(func() {
		job.state = "running"
		a.invalidate()
	})

	progress := func(done, total int64) {
		last := -1
		if job.total > 0 {
			last = int(job.done * 100 / job.total)
		}
		now := 0
		if total > 0 {
			now = int(done * 100 / total)
		}
		if now == last && done != total {
			return
		}
		a.onMain(func() {
			job.done, job.total = done, total
			a.invalidate()
		})
	}

	var err error
	var files int64
	switch {
	case job.dir && job.up:
		files, _, err = job.files.UploadDir(ctx, job.local, job.remote, agent.Transfer{
			Progress: progress,
			Resume:   job.resume,
		})
	case job.dir:
		files, _, err = job.files.DownloadDir(ctx, job.remote, job.local, agent.Transfer{
			Progress: progress,
			Resume:   job.resume,
		})
	case job.up:
		_, err = job.files.Upload(ctx, job.local, job.remote, agent.Transfer{
			Progress: progress,
			Resume:   job.resume,
		})
		files = 1
	default:
		_, err = job.files.Download(ctx, job.remote, job.local, agent.Transfer{
			Progress: progress,
			Resume:   job.resume,
		})
		files = 1
	}

	a.onMain(func() {
		job.count = files
		if err != nil {
			job.state = "failed"
			job.err = err.Error()
		} else {
			job.state = "done"
			job.done = job.total
		}
		a.invalidate()
	})
}

// queueView draws the recent jobs above the panes.
func (a *App) queueView(c *ui.Context, t *ui.Theme) {
	if a.q == nil {
		return
	}
	jobs := a.q.snapshot(5)
	if len(jobs) == 0 {
		return
	}
	ui.Column(c).FillWidth().Gap(gapTight).Children(func() {
		ui.Row(c).Gap(gapSmall).Children(func() {
			ui.Textf(c, tr("传输队列 · %d"), a.q.active()).FontSize(fontMicro).TextColor(t.TextMuted)
			ui.Spacer(c)
		})
		for _, j := range jobs {
			arrow := "↑"
			if !j.up {
				arrow = "↓"
			}
			state := j.state
			switch j.state {
			case "running":
				pct := 0
				if j.total > 0 {
					pct = int(j.done * 100 / j.total)
				}
				state = fmt.Sprintf("%d%%", pct)
			case "done":
				state = tr("完成")
			case "failed":
				state = tr("失败")
			case "queued":
				state = tr("排队")
			}
			name := baseOf(j.local, false)
			if !j.up {
				name = baseOf(j.remote, true)
			}
			color := t.TextMuted
			if j.state == "failed" {
				color = t.Danger
			}
			line := fmt.Sprintf("%s %s  %s", arrow, name, state)
			if j.dir {
				line = fmt.Sprintf("%s %s/  %s", arrow, name, state)
				if j.count > 0 {
					line += fmt.Sprintf(tr("（%d 个文件）"), j.count)
				}
			}
			if j.err != "" {
				line += "  " + j.err
			}
			ui.Text(c, line).Font(mono).FontSize(fontMicro).TextColor(color)
		}
		ui.Divider(c)
	})
}

var _ = time.Now
var _ = path.Base
