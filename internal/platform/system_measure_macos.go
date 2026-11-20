package platform

import (
	"context"
	"math"
	"strconv"
	"strings"
)

// Pure macOS measurement logic (ADR 026 RES-03B). Per the ADR Consequences,
// mach/IOKit counters are not reachable in pure Go without cgo, so RAM total
// and core count use the sysctl syscall (no child), while RAM available and
// CPU utilization use bounded read-only subprocess fallbacks: vm_stat and
// `top -l 2 -n 0` (top takes its own two-sample interval inside the child).
// The primitives are injected so the logic is testable on any OS; the real
// binding lives in system_darwin.go.

type darwinSysctlFunc func(name string) (uint64, error)
type darwinLookPathFunc func(name string) (string, error)
type darwinChildRunner func(ctx context.Context, path string, args ...string) ([]byte, string)

func measureDarwinSystem(ctx context.Context, sysctl darwinSysctlFunc, look darwinLookPathFunc, run darwinChildRunner) SystemReport {
	total := SystemMetric{Available: true, Source: "sysctl hw.memsize"}
	if v, err := sysctl("hw.memsize"); err != nil {
		total.Reason = "sysctl_hw_memsize_failed"
	} else {
		total.OK, total.Value = true, int64(v)
	}

	cores := SystemMetric{Available: true, Source: "sysctl hw.logicalcpu"}
	if v, err := sysctl("hw.logicalcpu"); err != nil {
		cores.Reason = "sysctl_hw_logicalcpu_failed"
	} else {
		cores.OK, cores.Value = true, int64(v)
	}

	available := measureDarwinRAMAvailable(ctx, look, run)
	util := measureDarwinCPUUtil(ctx, look, run)
	return systemReportFromCore(total, available, cores, util)
}

func measureDarwinRAMAvailable(ctx context.Context, look darwinLookPathFunc, run darwinChildRunner) SystemMetric {
	m := SystemMetric{
		Source:    "vm_stat (free+inactive+speculative)*page_size",
		Estimated: true,
		Reason:    "estimated: (free+inactive+speculative)*page_size",
	}
	path, err := look("vm_stat")
	if err != nil || path == "" {
		m.Reason = "vm_stat_not_found"
		return m
	}
	m.Available = true
	out, token := run(ctx, path)
	if token != "" {
		m.Reason = token
		return m
	}
	v, parseErr := parseVMStatAvailable(out)
	if parseErr != nil {
		m.Reason = "vm_stat_malformed"
		return m
	}
	m.OK, m.Value = true, v
	return m
}

func measureDarwinCPUUtil(ctx context.Context, look darwinLookPathFunc, run darwinChildRunner) SystemMetric {
	m := SystemMetric{Source: "top CPU usage (100-idle)"}
	path, err := look("top")
	if err != nil || path == "" {
		m.Reason = "top_not_found"
		return m
	}
	m.Available = true
	// -l 2: two samples (the second is the interval rate, not the since-boot
	// average); -n 0: no process rows, summary only.
	out, token := run(ctx, path, "-l", "2", "-n", "0")
	if token != "" {
		m.Reason = token
		return m
	}
	v, parseErr := parseTopCPUUtil(out)
	if parseErr != nil {
		m.Reason = "top_output_malformed"
		return m
	}
	m.OK, m.Value = true, v
	return m
}

// parseVMStatAvailable computes (free+inactive+speculative)*page_size from
// vm_stat output. This is an estimate of allocatable memory, not a direct
// "available" counter — the caller labels it estimated (D-6).
func parseVMStatAvailable(raw []byte) (int64, error) {
	var pageSize int64
	var free, inactive, speculative int64
	var haveFree, haveInactive, haveSpeculative bool
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.Contains(line, "page size of") {
			_, rest, found := strings.Cut(line, "page size of")
			if !found {
				return 0, errSystemMalformed
			}
			fields := strings.Fields(rest)
			if len(fields) == 0 {
				return 0, errSystemMalformed
			}
			n, err := strconv.ParseInt(fields[0], 10, 64)
			if err != nil || n <= 0 {
				return 0, errSystemMalformed
			}
			pageSize = n
			continue
		}
		key, val, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		var target *int64
		var seen *bool
		switch strings.TrimSpace(key) {
		case "Pages free":
			target, seen = &free, &haveFree
		case "Pages inactive":
			target, seen = &inactive, &haveInactive
		case "Pages speculative":
			target, seen = &speculative, &haveSpeculative
		default:
			continue
		}
		fields := strings.Fields(strings.TrimSpace(val))
		if len(fields) == 0 {
			return 0, errSystemMalformed
		}
		n, err := strconv.ParseInt(strings.TrimSuffix(fields[0], "."), 10, 64)
		if err != nil || n < 0 {
			return 0, errSystemMalformed
		}
		*target, *seen = n, true
	}
	if pageSize <= 0 || !haveFree || !haveInactive || !haveSpeculative {
		return 0, errSystemMalformed
	}
	return (free + inactive + speculative) * pageSize, nil
}

// parseTopCPUUtil reads the LAST "CPU usage: X% user, Y% sys, Z% idle" line
// (the second sample of `top -l 2`) and returns round(100 - idle).
func parseTopCPUUtil(raw []byte) (int64, error) {
	last := ""
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "CPU usage:") {
			last = line
		}
	}
	if last == "" {
		return 0, errSystemMalformed
	}
	_, rest, found := strings.Cut(last, "CPU usage:")
	if !found {
		return 0, errSystemMalformed
	}
	for _, part := range strings.Split(rest, ",") {
		part = strings.TrimSpace(part)
		if !strings.HasSuffix(part, "idle") {
			continue
		}
		pct := strings.TrimSpace(strings.TrimSuffix(part, "idle"))
		pct = strings.TrimSpace(strings.TrimSuffix(pct, "%"))
		v, err := strconv.ParseFloat(pct, 64)
		if err != nil || v < 0 || v > 100 {
			return 0, errSystemMalformed
		}
		return int64(math.Round(100 - v)), nil
	}
	return 0, errSystemMalformed
}
