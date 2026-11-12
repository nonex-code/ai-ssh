package gui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

// monitorCommand is what the详情 pane samples. One shell snippet, printed raw:
// no parsing means no surprises across distributions.
const monitorCommand = `echo "## uptime"; uptime 2>/dev/null
echo "## load"; cat /proc/loadavg 2>/dev/null
echo "## cores"; nproc 2>/dev/null
echo "## memory"; free -m 2>/dev/null
echo "## disk"; df -hP -x tmpfs -x devtmpfs 2>/dev/null | head -8
echo "## topcpu"; ps -eo pid,comm,%cpu,%mem --sort=-%cpu 2>/dev/null | head -8
echo "## who"; who 2>/dev/null | head -5`

// monitorPane samples a host while it is visible.
type monitorPane struct {
	open   bool
	text   string
	err    string
	busy   bool
	at     time.Time
	cancel context.CancelFunc
}

func (a *App) showMonitor(tb *terminalTab) {
	mp := &tb.monitorPane
	tb.filesPane.open = false
	tb.forwardPane.open = false
	mp.open = true
	a.invalidate()

	if mp.cancel != nil {
		return // already sampling
	}
	ctx, cancel := context.WithCancel(context.Background())
	mp.cancel = cancel
	go a.monitorLoop(ctx, tb)
}

func (a *App) stopMonitor(tb *terminalTab) {
	if tb.monitorPane.cancel != nil {
		tb.monitorPane.cancel()
		tb.monitorPane.cancel = nil
	}
	tb.monitorPane.open = false
}

// monitorLoop samples every few seconds until the pane closes or the tab does.
func (a *App) monitorLoop(ctx context.Context, tb *terminalTab) {
	a.onMain(func() {
		if tb.monitorPane.open {
			a.refreshMonitor(ctx, tb)
		}
	})
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.onMain(func() {
				if tb.monitorPane.open && tb.monitorAuto {
					a.refreshMonitor(ctx, tb)
				}
			})
		}
	}
}

func (a *App) refreshMonitor(ctx context.Context, tb *terminalTab) {
	ag := a.agentRef()
	if ag == nil {
		return
	}
	mp := &tb.monitorPane
	// Called on the main thread (from showMonitor or the ticker's hop):
	// setting the flag here keeps the click that opened the pane from
	// waiting on another turn of the loop.
	mp.busy = true
	a.invalidate()

	go func() {
		out, stderr, err := ag.ExecHuman(ctx, tb.host, monitorCommand, 12*time.Second)
		a.onMain(func() {
			mp.busy = false
			mp.at = time.Now()
			if err != nil {
				mp.err = err.Error()
				if out == "" {
					out = stderr
				}
				mp.text = out
			} else {
				mp.err = ""
				mp.text = out
			}
			a.invalidate()
		})
	}()
}

// monitorData is a parsed sample. raw keeps the output for a machine whose
// tools print something unexpected, so the pane never shows nothing.
type monitorData struct {
	uptime                      string
	load                        []string
	cores                       int
	memTotal, memUsed, memAvail int
	disks                       []diskStat
	procs                       []procStat
	users                       []string
	raw                         string
}

type diskStat struct {
	mount, size, used, avail string
	pct                      int
}

type procStat struct {
	pid, comm, cpu, mem string
}

func parseMonitor(output string) *monitorData {
	d := &monitorData{raw: strings.TrimRight(output, "\n")}
	sections := map[string][]string{}
	cur := ""
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.HasPrefix(line, "## ") {
			cur = strings.TrimSpace(line[3:])
			continue
		}
		if cur != "" && strings.TrimSpace(line) != "" {
			sections[cur] = append(sections[cur], line)
		}
	}
	if ls := sections["uptime"]; len(ls) > 0 {
		d.uptime = strings.TrimSpace(ls[0])
	}
	if ls := sections["load"]; len(ls) > 0 {
		if f := strings.Fields(ls[0]); len(f) >= 3 {
			d.load = f[:3]
		}
	}
	if ls := sections["cores"]; len(ls) > 0 {
		d.cores, _ = strconv.Atoi(strings.TrimSpace(ls[0]))
	}
	for _, line := range sections["memory"] {
		f := strings.Fields(line)
		if len(f) >= 7 && f[0] == "Mem:" {
			d.memTotal, _ = strconv.Atoi(f[1])
			d.memUsed, _ = strconv.Atoi(f[2])
			d.memAvail, _ = strconv.Atoi(f[6])
			break
		}
	}
	for i, line := range sections["disk"] {
		if i == 0 {
			continue // the header
		}
		f := strings.Fields(line)
		if len(f) < 6 {
			continue
		}
		pct, err := strconv.Atoi(strings.TrimSuffix(f[4], "%"))
		if err != nil {
			continue
		}
		d.disks = append(d.disks, diskStat{
			mount: f[5], size: f[1], used: f[2], avail: f[3], pct: pct,
		})
	}
	if ls := sections["topcpu"]; len(ls) > 1 {
		for _, line := range ls[1:] {
			f := strings.Fields(line)
			if len(f) < 4 {
				continue
			}
			d.procs = append(d.procs, procStat{pid: f[0], comm: f[1], cpu: f[2], mem: f[3]})
		}
	}
	d.users = sections["who"]
	return d
}

func (d *monitorData) empty() bool {
	return d.uptime == "" && len(d.load) == 0 && d.memTotal == 0 &&
		len(d.disks) == 0 && len(d.procs) == 0 && len(d.users) == 0
}

// usageColor marks a percentage as it fills up.
func usageColor(pct int, t *ui.Theme) ui.Color {
	switch {
	case pct >= 90:
		return t.Danger
	case pct >= 80:
		return t.Warning
	}
	return t.Text
}

func (a *App) monitorView(c *ui.Context, t *ui.Theme, tb *terminalTab) {
	mp := &tb.monitorPane
	d := parseMonitor(mp.text)

	ui.Column(c).Grow(1).FillWidth().Padding(gapWide).Gap(gapMid).Children(func() {
		ui.Row(c).Gap(gapMid).AlignItems(ui.Center).Children(func() {
			ui.Text(c, tr("主机概览 · ")+tb.host).FontSize(fontCard).Bold()
			if mp.busy {
				ui.Text(c, tr("采样中…")).FontSize(fontMicro).TextColor(t.TextMuted)
			} else if !mp.at.IsZero() {
				ui.Textf(c, tr("更新于 %s"), mp.at.Format("15:04:05")).FontSize(fontMicro).TextColor(t.TextMuted)
			}
			ui.Spacer(c)
			ui.Checkbox(c.Key("mon:auto"), &tb.monitorAuto, tr("每 5 秒刷新"))
			a.stripButton(c, t, "mon:refresh", "refresh", tr("立即刷新"), t.TextMuted, func() {
				a.refreshMonitor(context.Background(), tb)
			})
		})
		if mp.err != "" {
			ui.Text(c, mp.err).FontSize(fontSmall).TextColor(t.Danger)
		}
		if mp.text == "" {
			ui.Text(c, tr("还没有数据。")).FontSize(fontSmall).TextColor(t.TextMuted)
			return
		}

		ui.Scroll(c).Grow(1).Gap(gapMid).Children(func() {
			if d.empty() {
				// A machine whose tools print something else: show its
				// output as it came, colouring what can be recognised.
				for _, line := range strings.Split(strings.TrimRight(mp.text, "\n"), "\n") {
					ui.Text(c, line).Font(mono).FontSize(fontSmall).TextColor(monitorColor(line, t))
				}
				return
			}
			a.metricCards(c, t, d)
			if d.uptime != "" || len(d.users) > 0 {
				ui.Textf(c, tr("%s · %d 个登录用户"), strings.TrimSpace(d.uptime), len(d.users)).
					FontSize(fontSmall).TextColor(t.TextMuted)
			}
			if len(d.disks) > 1 {
				section(c, t, tr("文件系统"))
				for _, dk := range d.disks {
					a.diskRow(c, t, dk)
				}
			}
			if len(d.procs) > 0 {
				section(c, t, tr("进程（按 CPU）"))
				a.procTable(c, t, d.procs)
			}
			if len(d.users) > 0 {
				section(c, t, tr("登录用户"))
				for _, u := range d.users {
					ui.Text(c, u).Font(mono).FontSize(fontSmall).TextColor(t.TextMuted)
				}
			}
		})
	})
}

// metricCards is the top row: load, memory, the busiest disk.
func (a *App) metricCards(c *ui.Context, t *ui.Theme, d *monitorData) {
	ui.Row(c).FillWidth().Gap(gapMid).Children(func() {
		// Load, against the core count.
		loadPct, loadValue, loadSub := -1, "—", ""
		if len(d.load) > 0 {
			loadValue = d.load[0]
			loadSub = "5m " + d.load[1] + " · 15m " + d.load[2]
			if d.cores > 0 {
				if f, err := strconv.ParseFloat(d.load[0], 64); err == nil {
					loadPct = int(f / float64(d.cores) * 100)
				}
				loadSub = strconv.Itoa(d.cores) + tr(" 核 · ") + loadSub
			}
		}
		a.metricCard(c, t, tr("负载"), loadValue, loadSub, loadPct)

		memPct, memValue, memSub := -1, "—", ""
		if d.memTotal > 0 {
			memPct = d.memUsed * 100 / d.memTotal
			memValue = fmt.Sprintf("%.1f / %.1f GiB",
				float64(d.memUsed)/1024, float64(d.memTotal)/1024)
			memSub = fmt.Sprintf(tr("可用 %.1f GiB"), float64(d.memAvail)/1024)
		}
		a.metricCard(c, t, tr("内存"), memValue, memSub, memPct)

		diskPct, diskValue, diskSub := -1, "—", ""
		if len(d.disks) > 0 {
			dk := d.disks[0]
			for _, other := range d.disks {
				if other.pct > dk.pct {
					dk = other
				}
			}
			diskPct, diskValue, diskSub = dk.pct, strconv.Itoa(dk.pct)+"%", dk.mount
		}
		a.metricCard(c, t, tr("磁盘"), diskValue, diskSub, diskPct)
	})
}

func (a *App) metricCard(c *ui.Context, t *ui.Theme, title, value, sub string, pct int) {
	ui.Column(c).Grow(1).Padding(14).Radius(radiusCard).Gap(6).
		Background(t.Surface).Border(1, t.Border).Children(func() {
		ui.Text(c, title).FontSize(fontSmall).TextColor(t.TextMuted)
		ui.Text(c, value).FontSize(fontPage).Bold()
		if pct >= 0 {
			a.meter(c, t, pct, usageColor(pct, t))
		}
		if sub != "" {
			ui.Text(c, sub).Font(mono).FontSize(fontMicro).TextColor(t.TextMuted)
		}
	})
}

// meter is a 6px bar, filled by pct.
func (a *App) meter(c *ui.Context, t *ui.Theme, pct int, color ui.Color) {
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	ui.Row(c).FillWidth().Height(6).Radius(3).Background(t.SurfacePressed).Children(func() {
		w := float32(pct)
		if pct > 0 && w < 2 {
			w = 2
		}
		ui.Box(c).WidthPercent(w).Height(6).Radius(3).Background(color)
	})
}

func (a *App) diskRow(c *ui.Context, t *ui.Theme, dk diskStat) {
	ui.Column(c).Gap(4).Children(func() {
		ui.Row(c).Gap(gapSmall).Children(func() {
			ui.Text(c, dk.mount).Font(mono).FontSize(fontSmall)
			ui.Spacer(c)
			ui.Textf(c, "%s / %s", dk.used, dk.size).Font(mono).FontSize(fontMicro).TextColor(t.TextMuted)
			ui.Textf(c, "%d%%", dk.pct).Font(mono).FontSize(fontSmall).TextColor(usageColor(dk.pct, t))
		})
		a.meter(c, t, dk.pct, usageColor(dk.pct, t))
	})
}

func (a *App) procTable(c *ui.Context, t *ui.Theme, procs []procStat) {
	row := func(pid, comm, cpu, mem string, color ui.Color) {
		ui.Row(c).Gap(gapSmall).Children(func() {
			ui.Text(c, pid).Font(mono).FontSize(fontSmall).Width(56).TextColor(t.TextMuted)
			ui.Text(c, comm).Font(mono).FontSize(fontSmall).Grow(1).TextColor(color)
			ui.Text(c, cpu).Font(mono).FontSize(fontSmall).Width(56).TextAlign(ui.End).TextColor(t.Text)
			ui.Text(c, mem).Font(mono).FontSize(fontSmall).Width(56).TextAlign(ui.End).TextColor(t.TextMuted)
		})
	}
	ui.Column(c).Gap(2).Children(func() {
		row("PID", "COMMAND", "CPU%", "MEM%", t.TextMuted)
		ui.Box(c).Height(1).FillWidth().Background(t.Border)
		for _, p := range procs {
			color := t.Text
			if f, err := strconv.ParseFloat(p.cpu, 64); err == nil && f >= 50 {
				color = t.Warning
			}
			row(p.pid, p.comm, p.cpu, p.mem, color)
		}
	})
}

// monitorColor paints a sampled line: section headers muted, a nearly full disk
// or memory red, a filling one amber.
func monitorColor(line string, t *ui.Theme) ui.Color {
	trimmed := strings.TrimSpace(line)
	switch {
	case strings.HasPrefix(trimmed, "## "):
		return t.TextMuted
	case strings.HasPrefix(trimmed, "Mem:"):
		pct := memPercent(trimmed)
		if pct >= 90 {
			return t.Danger
		}
		if pct >= 80 {
			return t.Warning
		}
	case strings.HasPrefix(trimmed, "/"):
		pct := lastPercent(trimmed)
		if pct >= 90 {
			return t.Danger
		}
		if pct >= 80 {
			return t.Warning
		}
	}
	return t.Text
}

// lastPercent reads the last "N%" of a line, -1 when there is none.
func lastPercent(s string) int {
	end := strings.LastIndexByte(s, '%')
	if end <= 0 {
		return -1
	}
	start := end
	for start > 0 && s[start-1] >= '0' && s[start-1] <= '9' {
		start--
	}
	n, err := strconv.Atoi(s[start:end])
	if err != nil {
		return -1
	}
	return n
}

// memPercent reads free(1)'s "Mem: total used ..." line.
func memPercent(line string) int {
	fields := strings.Fields(line)
	if len(fields) < 3 {
		return -1
	}
	total, err1 := strconv.ParseFloat(fields[1], 64)
	used, err2 := strconv.ParseFloat(fields[2], 64)
	if err1 != nil || err2 != nil || total <= 0 {
		return -1
	}
	return int(used * 100 / total)
}
