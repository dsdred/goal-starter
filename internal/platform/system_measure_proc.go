package platform

import (
	"context"
	"os"
	"strconv"
	"strings"
	"time"
)

// Pure Linux measurement logic (ADR 026 RES-03B): /proc and sysfs are plain
// file reads, no child process. The primitives are injected so the logic is
// testable on any OS; the production binding lives in system_linux.go.

type systemFileReader func(path string) ([]byte, error)
type systemSleeper func(ctx context.Context, d time.Duration) error

func measureLinuxSystem(ctx context.Context, read systemFileReader, sleep systemSleeper, interval time.Duration) SystemReport {
	total, available := measureLinuxRAM(read)
	cores := measureLinuxCores(read)
	util := measureLinuxCPUUtil(ctx, read, sleep, interval)
	return systemReportFromCore(total, available, cores, util)
}

func measureLinuxRAM(read systemFileReader) (total, available SystemMetric) {
	total = SystemMetric{Source: "/proc/meminfo MemTotal"}
	available = SystemMetric{Source: "/proc/meminfo MemAvailable"}
	raw, err := read("/proc/meminfo")
	if err != nil {
		reason := "meminfo_read_error"
		addressable := true
		if os.IsNotExist(err) {
			reason = "proc_meminfo_not_found"
			addressable = false
		}
		total.Available, total.Reason = addressable, reason
		available.Available, available.Reason = addressable, reason
		return total, available
	}
	total.Available, available.Available = true, true
	totalKB, availKB, availPresent, parseErr := parseMeminfo(raw)
	if parseErr != nil {
		total.Reason, available.Reason = "meminfo_malformed", "meminfo_malformed"
		return total, available
	}
	total.OK, total.Value = true, totalKB*1024
	if !availPresent {
		// MemAvailable exists since Linux 3.14; on older kernels the metric is
		// honestly unavailable, never a fabricated estimate (D-6).
		available.Reason = "memavailable_not_present"
		return total, available
	}
	available.OK, available.Value = true, availKB*1024
	return total, available
}

func measureLinuxCores(read systemFileReader) SystemMetric {
	m := SystemMetric{Source: "/sys/devices/system/cpu/online"}
	raw, err := read("/sys/devices/system/cpu/online")
	if err == nil {
		n, parseErr := parseCPUOnline(raw)
		if parseErr != nil {
			m.Available, m.Reason = true, "cpu_online_malformed"
			return m
		}
		m.Available, m.OK, m.Value = true, true, int64(n)
		return m
	}
	if !os.IsNotExist(err) {
		m.Available, m.Reason = true, "cpu_online_read_error"
		return m
	}
	// sysfs absent (e.g. a minimal container): /proc/cpuinfo is the fallback.
	m.Source = "/proc/cpuinfo processor"
	raw, err = read("/proc/cpuinfo")
	if err != nil {
		if os.IsNotExist(err) {
			m.Reason = "cpu_cores_source_not_found"
			return m
		}
		m.Available, m.Reason = true, "cpuinfo_read_error"
		return m
	}
	m.Available = true
	n := countCPUInfoProcessors(raw)
	if n == 0 {
		m.Reason = "cpuinfo_malformed"
		return m
	}
	m.OK, m.Value = true, int64(n)
	return m
}

func measureLinuxCPUUtil(ctx context.Context, read systemFileReader, sleep systemSleeper, interval time.Duration) SystemMetric {
	m := SystemMetric{Source: "/proc/stat cpu"}
	raw0, err := read("/proc/stat")
	if err != nil {
		if os.IsNotExist(err) {
			m.Reason = "proc_stat_not_found"
			return m
		}
		m.Available, m.Reason = true, "proc_stat_read_error"
		return m
	}
	m.Available = true
	total0, idle0, parseErr := parseProcStatCPU(raw0)
	if parseErr != nil {
		m.Reason = "proc_stat_malformed"
		return m
	}
	if err := sleep(ctx, interval); err != nil {
		m.Reason = "measurement_timeout"
		return m
	}
	raw1, err := read("/proc/stat")
	if err != nil {
		m.Reason = "proc_stat_read_error"
		return m
	}
	total1, idle1, parseErr := parseProcStatCPU(raw1)
	if parseErr != nil {
		m.Reason = "proc_stat_malformed"
		return m
	}
	util, ok := cpuUtilFromDelta(total0, idle0, total1, idle1)
	if !ok {
		m.Reason = "counter_delta_invalid"
		return m
	}
	m.OK, m.Value = true, util
	return m
}

// parseMeminfo extracts MemTotal (required) and MemAvailable (optional) in kB.
// Any other line is ignored; a missing/malformed MemTotal fails the parse.
func parseMeminfo(raw []byte) (totalKB, availKB int64, availPresent bool, err error) {
	for _, line := range strings.Split(string(raw), "\n") {
		key, val, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		switch key {
		case "MemTotal", "MemAvailable":
			fields := strings.Fields(val)
			if len(fields) == 0 {
				return 0, 0, false, errSystemMalformed
			}
			n, parseErr := strconv.ParseInt(fields[0], 10, 64)
			if parseErr != nil || n < 0 {
				return 0, 0, false, errSystemMalformed
			}
			if key == "MemTotal" {
				totalKB = n
			} else {
				availKB, availPresent = n, true
			}
		}
	}
	if totalKB <= 0 {
		return 0, 0, false, errSystemMalformed
	}
	return totalKB, availKB, availPresent, nil
}

// parseCPUOnline counts the online logical CPU list ("0-7", "0-3,5,7").
func parseCPUOnline(raw []byte) (int, error) {
	s := strings.TrimSpace(string(raw))
	if s == "" {
		return 0, errSystemMalformed
	}
	count := 0
	for _, part := range strings.Split(s, ",") {
		lo, hi, found := strings.Cut(part, "-")
		start, err := strconv.Atoi(strings.TrimSpace(lo))
		if err != nil || start < 0 {
			return 0, errSystemMalformed
		}
		end := start
		if found {
			end, err = strconv.Atoi(strings.TrimSpace(hi))
			if err != nil || end < start {
				return 0, errSystemMalformed
			}
		}
		count += end - start + 1
	}
	return count, nil
}

func countCPUInfoProcessors(raw []byte) int {
	n := 0
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "processor") {
			n++
		}
	}
	return n
}

// parseProcStatCPU reads the aggregate "cpu" line jiffies: total is the sum of
// user..steal (guest/guest_nice are already counted in user/nice); idle is
// idle+iowait.
func parseProcStatCPU(raw []byte) (total, idle uint64, err error) {
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 || fields[0] != "cpu" {
			continue
		}
		if len(fields) < 9 {
			return 0, 0, errSystemMalformed
		}
		var vals [8]uint64
		for i := 0; i < 8; i++ {
			v, parseErr := strconv.ParseUint(fields[i+1], 10, 64)
			if parseErr != nil {
				return 0, 0, errSystemMalformed
			}
			vals[i] = v
		}
		total = vals[0] + vals[1] + vals[2] + vals[3] + vals[4] + vals[5] + vals[6] + vals[7]
		idle = vals[3] + vals[4]
		return total, idle, nil
	}
	return 0, 0, errSystemMalformed
}

// cpuUtilFromDelta computes the integer busy percent between two counter
// reads. A zero or regressing delta yields no value (ok=false), never a
// fabricated number.
func cpuUtilFromDelta(total0, idle0, total1, idle1 uint64) (int64, bool) {
	if total1 <= total0 || idle1 < idle0 {
		return 0, false
	}
	dTotal := total1 - total0
	dIdle := idle1 - idle0
	if dIdle > dTotal {
		return 0, false
	}
	return roundPercent(int64(dTotal-dIdle), int64(dTotal)), true
}

// roundPercent rounds busy/total to the nearest integer percent, clamped to
// 0..100. total must be positive.
func roundPercent(busy, total int64) int64 {
	if total <= 0 {
		return 0
	}
	p := (busy*100 + total/2) / total
	if p < 0 {
		return 0
	}
	if p > 100 {
		return 100
	}
	return p
}
