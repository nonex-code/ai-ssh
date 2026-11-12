package gui

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

const monitorSample = `## uptime
 15:42:01 up 42 days,  3:12,  2 users,  load average: 0.42, 0.38, 0.35
## load
0.42 0.38 0.35 2/345 6789
## cores
8
## memory
              total        used        free      shared  buff/cache   available
Mem:          15884        4210        9876         123        1797        9900
Swap:          2047           0        2047
## disk
Filesystem      Size  Used Avail Use% Mounted on
/dev/sda1        98G   42G   51G  46% /
/dev/sdb1       1.8T  1.2T  500G  72% /data
## topcpu
    PID COMMAND         %CPU %MEM
   1234 nginx            2.1  0.5
   5678 firefox         55.0 12.3
## who
alice    pts/0        10.0.0.5         15:20
`

func TestParseMonitor(t *testing.T) {
	d := parseMonitor(monitorSample)
	if d.empty() {
		t.Fatal("the sample parsed to nothing")
	}
	if len(d.load) != 3 || d.load[0] != "0.42" || d.cores != 8 {
		t.Fatalf("load = %v, cores = %d", d.load, d.cores)
	}
	if d.memTotal != 15884 || d.memUsed != 4210 || d.memAvail != 9900 {
		t.Fatalf("memory = %d/%d/%d", d.memUsed, d.memTotal, d.memAvail)
	}
	if len(d.disks) != 2 || d.disks[0].pct != 46 || d.disks[1].mount != "/data" {
		t.Fatalf("disks = %+v", d.disks)
	}
	if len(d.procs) != 2 || d.procs[1].comm != "firefox" || d.procs[1].cpu != "55.0" {
		t.Fatalf("procs = %+v", d.procs)
	}
	if len(d.users) != 1 || d.uptime == "" {
		t.Fatalf("users = %v, uptime = %q", d.users, d.uptime)
	}
}

func TestParseMonitorFallsBackToRaw(t *testing.T) {
	d := parseMonitor("something else entirely\nno sections here\n")
	if !d.empty() || d.raw == "" {
		t.Fatalf("unparsed output must keep its raw text: %+v", d)
	}
}

func TestUsageColor(t *testing.T) {
	light := ui.LightTheme()
	if got := usageColor(10, light); got != light.Text {
		t.Fatalf("low usage = %v", got)
	}
	if got := usageColor(85, light); got != light.Warning {
		t.Fatalf("85%% = %v", got)
	}
	if got := usageColor(95, light); got != light.Danger {
		t.Fatalf("95%% = %v", got)
	}
}
