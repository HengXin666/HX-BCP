package terminal

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// HostSnapshot is a low-cardinality resource sample of the machine and the
// monitored local processes (control plane + data plane). All CPU numbers are
// percentages of one logical core; divide by logical CPU count for share.
type HostSnapshot struct {
	Timestamp time.Time `json:"timestamp"`

	// System totals.
	CPUUsagePct       float64 `json:"cpu_usage_pct"`
	CPUCount          int     `json:"cpu_count"`
	Load1             float64 `json:"load1"`
	Load5             float64 `json:"load5"`
	Load15            float64 `json:"load15"`
	MemoryTotalBytes  uint64  `json:"memory_total_bytes"`
	MemoryUsedBytes   uint64  `json:"memory_used_bytes"`
	MemoryCachedBytes uint64  `json:"memory_cached_bytes"`
	SwapTotalBytes    uint64  `json:"swap_total_bytes"`
	SwapUsedBytes     uint64  `json:"swap_used_bytes"`

	// Network throughput since the previous snapshot (bytes/sec).
	NetRxBytesPerSec int64 `json:"net_rx_bytes_per_sec"`
	NetTxBytesPerSec int64 `json:"net_tx_bytes_per_sec"`

	// Per-process resource usage. PIDs of 0 mean the process is not running.
	Processes []ProcessResource `json:"processes"`
}

// ProcessResource is the resource usage of one monitored process.
type ProcessResource struct {
	Name           string  `json:"name"`
	PID            int     `json:"pid"`
	CPUUsagePct    float64 `json:"cpu_usage_pct"`
	MemoryRSSBytes uint64  `json:"memory_rss_bytes"`
}

type hostCollector struct {
	mu sync.Mutex

	// previous total/busy jiffies for system CPU accounting
	prevTotal uint64
	prevBusy  uint64
	// previous per-process utime+stime ticks
	prevProcTicks map[int]uint64
	// previous net counters
	prevNetRx uint64
	prevNetTx uint64
	prevTime  time.Time

	clockHz uint64
}

func newHostCollector() *hostCollector {
	return &hostCollector{
		clockHz:       uint64(sysconfClkTck()),
		prevProcTicks: make(map[int]uint64),
	}
}

// Snapshot samples the system and the provided PIDs. It is safe to call from
// a single goroutine (the terminal editor ticker). Names parallel PIDs.
func (h *hostCollector) Snapshot(pids map[int]string, dataplanePID int) HostSnapshot {
	now := time.Now()
	out := HostSnapshot{Timestamp: now, CPUCount: runtime.NumCPU()}
	h.readLoadAvg(&out)
	h.readMemInfo(&out)
	h.readCPU(now, &out)
	h.readNet(now, &out)

	known := make(map[int]string, len(pids)+1)
	for pid, name := range pids {
		known[pid] = name
	}
	if dataplanePID > 0 {
		if _, ok := known[dataplanePID]; !ok {
			known[dataplanePID] = "mihomo"
		}
	}
	out.Processes = h.readProcesses(known)
	return out
}

func (h *hostCollector) readLoadAvg(out *HostSnapshot) {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return
	}
	fields := strings.Fields(string(data))
	if len(fields) >= 3 {
		out.Load1, _ = strconv.ParseFloat(fields[0], 64)
		out.Load5, _ = strconv.ParseFloat(fields[1], 64)
		out.Load15, _ = strconv.ParseFloat(fields[2], 64)
	}
}

func (h *hostCollector) readMemInfo(out *HostSnapshot) {
	file, err := os.Open("/proc/meminfo")
	if err != nil {
		return
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		key, rest, _ := strings.Cut(line, ":")
		rest = strings.TrimSpace(rest)
		num, unit, _ := strings.Cut(rest, " ")
		value, _ := strconv.ParseUint(num, 10, 64)
		if unit == "kB" {
			value *= 1024
		}
		switch key {
		case "MemTotal":
			out.MemoryTotalBytes = value
		case "MemAvailable":
			if out.MemoryTotalBytes > value {
				out.MemoryUsedBytes = out.MemoryTotalBytes - value
			}
		case "Cached":
			out.MemoryCachedBytes = value
		case "SwapTotal":
			out.SwapTotalBytes = value
		case "SwapFree":
			out.SwapUsedBytes = out.SwapTotalBytes - value
		}
	}
}

func (h *hostCollector) readCPU(now time.Time, out *HostSnapshot) {
	h.mu.Lock()
	defer h.mu.Unlock()
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return
	}
	first := strings.SplitN(strings.TrimSpace(string(data)), "\n", 2)[0]
	fields := strings.Fields(first)
	if len(fields) < 5 || fields[0] != "cpu" {
		return
	}
	// user nice system idle iowait irq softirq steal guest ...
	var jiffies uint64
	for _, f := range fields[1:] {
		n, _ := strconv.ParseUint(f, 10, 64)
		jiffies += n
	}
	// idle + iowait are "not busy"
	idle, _ := strconv.ParseUint(fields[4], 10, 64)
	busy := jiffies
	if len(fields) > 5 {
		iow, _ := strconv.ParseUint(fields[5], 10, 64)
		idle += iow
		busy -= idle
	} else {
		busy -= idle
	}
	if h.prevTotal > 0 {
		deltaTotal := jiffies - h.prevTotal
		deltaBusy := busy - h.prevBusy
		if deltaTotal > 0 {
			out.CPUUsagePct = float64(deltaBusy) / float64(deltaTotal) * 100
		}
	} else {
		out.CPUUsagePct = float64(busy) / float64(jiffies) * 100
	}
	h.prevTotal = jiffies
	h.prevBusy = busy
	_ = now
}

func (h *hostCollector) readNet(now time.Time, out *HostSnapshot) {
	data, err := os.ReadFile("/proc/net/dev")
	if err != nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	var rx, tx uint64
	for _, line := range strings.Split(string(data), "\n")[2:] {
		fields := strings.Fields(line)
		if len(fields) < 10 {
			continue
		}
		iface := strings.TrimSuffix(fields[0], ":")
		if iface == "lo" {
			continue
		}
		r, _ := strconv.ParseUint(fields[1], 10, 64)
		t, _ := strconv.ParseUint(fields[9], 10, 64)
		rx += r
		tx += t
	}
	if !h.prevTime.IsZero() {
		elapsed := now.Sub(h.prevTime).Seconds()
		if elapsed > 0 {
			out.NetRxBytesPerSec = int64(float64(rx-h.prevNetRx) / elapsed)
			out.NetTxBytesPerSec = int64(float64(tx-h.prevNetTx) / elapsed)
		}
	}
	h.prevNetRx = rx
	h.prevNetTx = tx
	h.prevTime = now
}

func (h *hostCollector) readProcesses(known map[int]string) []ProcessResource {
	h.mu.Lock()
	prev := h.prevProcTicks
	h.prevProcTicks = make(map[int]uint64, len(known))
	defer h.mu.Unlock()
	hz := h.clockHz
	if hz == 0 {
		hz = 100
	}
	result := make([]ProcessResource, 0, len(known))
	for pid, name := range known {
		statRaw, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		if err != nil {
			continue
		}
		utime, stime, rssPages, err := parseProcStat(statRaw)
		if err != nil {
			continue
		}
		ticks := utime + stime
		resource := ProcessResource{Name: name, PID: pid, MemoryRSSBytes: rssPages * uint64(os.Getpagesize())}
		if last, ok := prev[pid]; ok && hz > 0 {
			// CPU since last sample as share of one core (100 = 1 core).
			resource.CPUUsagePct = float64(ticks-last) / float64(hz) * 100
		}
		h.prevProcTicks[pid] = ticks
		result = append(result, resource)
	}
	return result
}

// parseProcStat reads utime, stime (in clock ticks) and rss (in pages) from a
// /proc/<pid>/stat line. The comm field may contain spaces and parentheses.
func parseProcStat(raw []byte) (utime, stime, rss uint64, err error) {
	// comm is within the first '(' ... last ')'
	last := -1
	first := -1
	for i, b := range raw {
		if b == '(' && first < 0 {
			first = i
		}
		if b == ')' {
			last = i
		}
	}
	if first < 0 || last < 0 {
		return 0, 0, 0, errors.New("parse stat: missing comm")
	}
	after := strings.Fields(string(raw[last+1:]))
	if len(after) < 22 {
		return 0, 0, 0, errors.New("parse stat: too short")
	}
	// state = after[0]; then ppid ppid pgrp session tty tpgid flags minflt cminflt majflt cmajflt
	// index 11=utime 12=stime 13=cutime 14=cstime 15=priority 16=nice 17=num_threads 18=itrealvalue 19=starttime 20=vsize 21=rss
	utime, err = strconv.ParseUint(after[11], 10, 64)
	if err != nil {
		return 0, 0, 0, err
	}
	stime, err = strconv.ParseUint(after[12], 10, 64)
	if err != nil {
		return 0, 0, 0, err
	}
	rss, err = strconv.ParseUint(after[21], 10, 64)
	return utime, stime, rss, err
}

func sysconfClkTck() int {
	// SC_CLK_TCK is 100 on essentially every Linux distribution.
	return 100
}
