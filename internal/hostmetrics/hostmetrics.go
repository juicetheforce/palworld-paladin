// Package hostmetrics reads host-level metrics from /proc and /sys using
// the standard library only — no lm-sensors, no cgo, no external deps
// (DESIGN.md §6.5, §6.6a). Everything degrades gracefully: a source that
// doesn't exist (e.g. thermal sensors on a VM) yields a zero/absent field,
// never an error, so the same binary works on bare metal and in a guest.
package hostmetrics

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"time"
)

// Snapshot is a point-in-time host reading. Fields that couldn't be read
// on this host are left zero and flagged via the *Available booleans, so
// the UI can hide what isn't there rather than show a misleading zero.
type Snapshot struct {
	Time time.Time `json:"time"`

	// CPU (static identity)
	CPUModel string  `json:"cpu_model"`
	CPUCores int     `json:"cpu_cores"`
	CPUMHz   float64 `json:"cpu_mhz"` // current, first core

	// CPU (live) — percentages 0..100
	CPUUsage     float64 `json:"cpu_usage"`        // overall
	CPUHottest   float64 `json:"cpu_hottest_core"` // busiest single core
	CPUSteal     float64 `json:"cpu_steal"`        // %st — VM contention signal
	IsVM         bool    `json:"is_vm"`            // hypervisor flag in /proc/cpuinfo
	CPUAvailable bool    `json:"cpu_available"`

	// Memory (bytes)
	MemTotal     uint64 `json:"mem_total"`
	MemAvailable uint64 `json:"mem_available"`
	MemUsed      uint64 `json:"mem_used"`
	SwapTotal    uint64 `json:"swap_total"`
	SwapUsed     uint64 `json:"swap_used"`

	// Temperatures (°C). Absent on most VMs — TempAvailable=false then.
	CPUTemp       float64 `json:"cpu_temp"`
	TempAvailable bool    `json:"temp_available"`

	// Network throughput on the primary interface (bytes/sec, derived).
	NetInterface     string  `json:"net_interface"`
	NetRxBytesPerSec float64 `json:"net_rx_bps"`
	NetTxBytesPerSec float64 `json:"net_tx_bps"`
	NetAvailable     bool    `json:"net_available"`
}

// Reader holds the state needed to compute rate-based metrics (CPU % and
// network throughput both need two samples over time). Not safe for
// concurrent Read calls; the caller (one sampler) drives it serially.
type Reader struct {
	lastCPU    []cpuTimes
	lastCPUAll cpuTimes
	lastNet    map[string]netCounters
	lastTime   time.Time
}

func NewReader() *Reader { return &Reader{} }

// Read returns a fresh Snapshot. The first call establishes baselines for
// rate metrics (CPU %, net throughput read as 0 on the very first call);
// subsequent calls compute deltas since the previous Read.
func (r *Reader) Read() Snapshot {
	now := time.Now()
	s := Snapshot{Time: now}
	dt := now.Sub(r.lastTime).Seconds()

	readCPUStatic(&s)
	r.readCPULive(&s, dt)
	readMemory(&s)
	readTemp(&s)
	r.readNetwork(&s, dt)

	r.lastTime = now
	return s
}

// ---- CPU static (/proc/cpuinfo) ----

func readCPUStatic(s *Snapshot) {
	f, err := os.Open("/proc/cpuinfo")
	if err != nil {
		return
	}
	defer f.Close()
	cores := 0
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		switch k {
		case "processor":
			cores++
		case "model name":
			if s.CPUModel == "" {
				s.CPUModel = v
			}
		case "cpu MHz":
			if s.CPUMHz == 0 {
				s.CPUMHz, _ = strconv.ParseFloat(v, 64)
			}
		case "flags":
			// The hypervisor flag marks a guest: steal time is a real
			// measurement there. Bare metal never sets it — steal is
			// structurally zero, so the UI hides it entirely.
			if !s.IsVM {
				for _, fl := range strings.Fields(v) {
					if fl == "hypervisor" {
						s.IsVM = true
						break
					}
				}
			}
		}
	}
	s.CPUCores = cores
}

// ---- CPU live (/proc/stat) ----

type cpuTimes struct {
	user, nice, system, idle, iowait, irq, softirq, steal uint64
}

func (c cpuTimes) totalIdle() (total, idle uint64) {
	idle = c.idle + c.iowait
	total = c.user + c.nice + c.system + c.idle + c.iowait + c.irq + c.softirq + c.steal
	return
}

func parseCPULine(fields []string) (cpuTimes, bool) {
	if len(fields) < 8 {
		return cpuTimes{}, false
	}
	n := make([]uint64, 8)
	for i := 0; i < 8; i++ {
		n[i], _ = strconv.ParseUint(fields[i+1], 10, 64)
	}
	return cpuTimes{n[0], n[1], n[2], n[3], n[4], n[5], n[6], n[7]}, true
}

func (r *Reader) readCPULive(s *Snapshot, dt float64) {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return
	}
	defer f.Close()

	var all cpuTimes
	var perCore []cpuTimes
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) == 0 || !strings.HasPrefix(fields[0], "cpu") {
			if len(perCore) > 0 {
				break // past the cpu lines
			}
			continue
		}
		ct, ok := parseCPULine(fields)
		if !ok {
			continue
		}
		if fields[0] == "cpu" {
			all = ct
		} else {
			perCore = append(perCore, ct)
		}
	}

	// Need a prior sample to compute a rate.
	if !r.lastTime.IsZero() {
		s.CPUUsage = cpuBusyPct(r.lastCPUAll, all)
		if r.lastCPUAll.steal+all.steal > 0 {
			s.CPUSteal = deltaPct(all.steal-r.lastCPUAll.steal, r.lastCPUAll, all)
		}
		var hottest float64
		for i := range perCore {
			if i < len(r.lastCPU) {
				if p := cpuBusyPct(r.lastCPU[i], perCore[i]); p > hottest {
					hottest = p
				}
			}
		}
		s.CPUHottest = hottest
		s.CPUAvailable = true
	}
	r.lastCPUAll = all
	r.lastCPU = perCore
	_ = dt
}

func cpuBusyPct(prev, cur cpuTimes) float64 {
	pt, pi := prev.totalIdle()
	ct, ci := cur.totalIdle()
	dtotal := float64(ct - pt)
	didle := float64(ci - pi)
	if dtotal <= 0 {
		return 0
	}
	pct := (dtotal - didle) / dtotal * 100
	return clamp(pct)
}

func deltaPct(delta uint64, prev, cur cpuTimes) float64 {
	pt, _ := prev.totalIdle()
	ct, _ := cur.totalIdle()
	dtotal := float64(ct - pt)
	if dtotal <= 0 {
		return 0
	}
	return clamp(float64(delta) / dtotal * 100)
}

// ---- Memory (/proc/meminfo) ----

func readMemory(s *Snapshot) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return
	}
	defer f.Close()
	vals := map[string]uint64{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		fields := strings.Fields(strings.TrimSpace(v))
		if len(fields) == 0 {
			continue
		}
		n, _ := strconv.ParseUint(fields[0], 10, 64)
		vals[k] = n * 1024 // kB → bytes
	}
	s.MemTotal = vals["MemTotal"]
	s.MemAvailable = vals["MemAvailable"]
	if s.MemTotal >= s.MemAvailable {
		s.MemUsed = s.MemTotal - s.MemAvailable
	}
	s.SwapTotal = vals["SwapTotal"]
	if free := vals["SwapFree"]; s.SwapTotal >= free {
		s.SwapUsed = s.SwapTotal - free
	}
}

// ---- Temperature (/sys/class/hwmon) ----

func readTemp(s *Snapshot) {
	// Walk hwmon devices; prefer a sensor named like a CPU package.
	base := "/sys/class/hwmon"
	entries, err := os.ReadDir(base)
	if err != nil {
		return
	}
	best := -1.0
	for _, e := range entries {
		dir := base + "/" + e.Name()
		// temp1_input in millidegrees C; label helps pick the CPU one.
		for i := 1; i <= 8; i++ {
			p := dir + "/temp" + strconv.Itoa(i) + "_input"
			raw, err := os.ReadFile(p)
			if err != nil {
				continue
			}
			milli, err := strconv.ParseFloat(strings.TrimSpace(string(raw)), 64)
			if err != nil {
				continue
			}
			c := milli / 1000
			label := ""
			if lb, err := os.ReadFile(dir + "/temp" + strconv.Itoa(i) + "_label"); err == nil {
				label = strings.ToLower(strings.TrimSpace(string(lb)))
			}
			// Prefer a labelled package/core temp; else keep the hottest.
			if strings.Contains(label, "package") || strings.Contains(label, "tctl") ||
				strings.Contains(label, "cpu") {
				s.CPUTemp = c
				s.TempAvailable = true
				return
			}
			if c > best {
				best = c
			}
		}
	}
	if best > 0 {
		s.CPUTemp = best
		s.TempAvailable = true
	}
}

// ---- Network (/sys/class/net/*/statistics) ----

type netCounters struct{ rx, tx uint64 }

func (r *Reader) readNetwork(s *Snapshot, dt float64) {
	base := "/sys/class/net"
	entries, err := os.ReadDir(base)
	if err != nil {
		return
	}
	if r.lastNet == nil {
		r.lastNet = map[string]netCounters{}
	}
	// Pick the primary non-loopback interface with the most traffic.
	var bestIf string
	var bestTotal uint64
	cur := map[string]netCounters{}
	for _, e := range entries {
		name := e.Name()
		if name == "lo" {
			continue
		}
		rx := readUint(base + "/" + name + "/statistics/rx_bytes")
		tx := readUint(base + "/" + name + "/statistics/tx_bytes")
		cur[name] = netCounters{rx, tx}
		if rx+tx > bestTotal {
			bestTotal, bestIf = rx+tx, name
		}
	}
	if bestIf != "" && dt > 0 {
		if prev, ok := r.lastNet[bestIf]; ok {
			s.NetInterface = bestIf
			if c := cur[bestIf]; c.rx >= prev.rx && c.tx >= prev.tx {
				s.NetRxBytesPerSec = float64(c.rx-prev.rx) / dt
				s.NetTxBytesPerSec = float64(c.tx-prev.tx) / dt
				s.NetAvailable = true
			}
		}
	}
	r.lastNet = cur
}

// ---- helpers ----

func readUint(path string) uint64 {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	n, _ := strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 64)
	return n
}

func clamp(f float64) float64 {
	if f < 0 {
		return 0
	}
	if f > 100 {
		return 100
	}
	return f
}
