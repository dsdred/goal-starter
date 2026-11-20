package platform

import (
	"context"
	"time"
)

// Pure Windows measurement logic (ADR 026 RES-03B): GlobalMemoryStatusEx for
// RAM, GetActiveProcessorCount for logical cores, GetSystemTimes twice for the
// CPU rate. The kernel32 sources are addressable by construction on Windows,
// so every metric is Available; a failed call only makes it not sampled (D-3).
// The primitives are injected so the logic is testable on any OS; the real
// syscall binding lives in system_windows.go.

type windowsMemStatusFunc func() (totalPhys, availPhys uint64, err error)
type windowsCPUTimesFunc func() (idle, kernel, user uint64, err error)
type windowsCPUCountFunc func() (uint32, error)

func measureWindowsSystem(ctx context.Context, mem windowsMemStatusFunc, times windowsCPUTimesFunc, cores windowsCPUCountFunc, sleep systemSleeper, interval time.Duration) SystemReport {
	total := SystemMetric{Available: true, Source: "GlobalMemoryStatusEx.ullTotalPhys"}
	available := SystemMetric{Available: true, Source: "GlobalMemoryStatusEx.ullAvailPhys"}
	if t, a, err := mem(); err != nil {
		total.Reason, available.Reason = "globalmemorystatusex_failed", "globalmemorystatusex_failed"
	} else {
		total.OK, total.Value = true, int64(t)
		available.OK, available.Value = true, int64(a)
	}

	coreMetric := SystemMetric{Available: true, Source: "GetActiveProcessorCount(ALL_PROCESSOR_GROUPS)"}
	if n, err := cores(); err != nil {
		coreMetric.Reason = "getactiveprocessorcount_failed"
	} else {
		coreMetric.OK, coreMetric.Value = true, int64(n)
	}

	util := measureWindowsCPUUtil(ctx, times, sleep, interval)
	return systemReportFromCore(total, available, coreMetric, util)
}

func measureWindowsCPUUtil(ctx context.Context, times windowsCPUTimesFunc, sleep systemSleeper, interval time.Duration) SystemMetric {
	m := SystemMetric{Available: true, Source: "GetSystemTimes"}
	idle0, kernel0, user0, err := times()
	if err != nil {
		m.Reason = "getsystemtimes_failed"
		return m
	}
	if err := sleep(ctx, interval); err != nil {
		m.Reason = "measurement_timeout"
		return m
	}
	idle1, kernel1, user1, err := times()
	if err != nil {
		m.Reason = "getsystemtimes_failed"
		return m
	}
	// GetSystemTimes kernel time INCLUDES idle: total = kernel+user,
	// busy = total - idle. FILETIME deltas are 100ns units.
	total0, total1 := kernel0+user0, kernel1+user1
	util, ok := cpuUtilFromDelta(total0, idle0, total1, idle1)
	if !ok {
		m.Reason = "counter_delta_invalid"
		return m
	}
	m.OK, m.Value = true, util
	return m
}
