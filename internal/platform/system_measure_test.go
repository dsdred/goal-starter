package platform

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"testing"
	"time"
)

// The measure logic for every OS is pure code over injected primitives, so all
// of it is tested here on the test runner's own GOOS (ADR 026 RES-03B C-1).

type fakeFS struct {
	files     map[string][]byte
	errs      map[string]error
	statReads []string
	statN     int
}

func (f *fakeFS) read(path string) ([]byte, error) {
	if path == "/proc/stat" && f.statReads != nil {
		i := f.statN
		f.statN++
		if i >= len(f.statReads) {
			i = len(f.statReads) - 1
		}
		return []byte(f.statReads[i]), nil
	}
	if err, ok := f.errs[path]; ok {
		return nil, err
	}
	if b, ok := f.files[path]; ok {
		return b, nil
	}
	return nil, os.ErrNotExist
}

func noSleep(context.Context, time.Duration) error { return nil }

func failSleep(context.Context, time.Duration) error { return context.DeadlineExceeded }

const (
	linuxMeminfo = "MemTotal:       16384000 kB\nMemFree:         1000000 kB\nMemAvailable:    8192000 kB\nBuffers:          200000 kB\n"
	linuxStat0   = "cpu  100 0 50 800 50 0 0 0\nctxt 123\n"
	linuxStat1   = "cpu  200 0 100 850 50 0 0 0\nctxt 456\n"
)

func linuxOKFiles() *fakeFS {
	return &fakeFS{
		files: map[string][]byte{
			"/proc/meminfo":                  []byte(linuxMeminfo),
			"/sys/devices/system/cpu/online": []byte("0-7\n"),
		},
		statReads: []string{linuxStat0, linuxStat1},
	}
}

func TestMeasureLinuxFullOK(t *testing.T) {
	r := measureLinuxSystem(context.Background(), linuxOKFiles().read, noSleep, time.Millisecond)
	if r.State != SystemStateOK {
		t.Fatalf("state = %q, want ok (error=%q)", r.State, r.Error)
	}
	if !r.RAMTotal.OK || r.RAMTotal.Value != 16384000*1024 {
		t.Fatalf("ram total = %+v", r.RAMTotal)
	}
	if !r.RAMAvailable.OK || r.RAMAvailable.Value != 8192000*1024 || r.RAMAvailable.Estimated {
		t.Fatalf("ram available = %+v (must be measured MemAvailable)", r.RAMAvailable)
	}
	if !r.CPUCores.OK || r.CPUCores.Value != 8 {
		t.Fatalf("cores = %+v", r.CPUCores)
	}
	// dTotal=200, dIdle=50 => 75% busy.
	if !r.CPUUtil.OK || r.CPUUtil.Value != 75 {
		t.Fatalf("util = %+v, want 75", r.CPUUtil)
	}
}

func TestMeasureLinuxMemAvailableAbsent(t *testing.T) {
	f := linuxOKFiles()
	f.files["/proc/meminfo"] = []byte("MemTotal:       16384000 kB\nMemFree:  1000000 kB\n")
	r := measureLinuxSystem(context.Background(), f.read, noSleep, time.Millisecond)
	if r.State != SystemStateOK {
		t.Fatalf("state = %q, want ok (optional absence never downgrades)", r.State)
	}
	if r.RAMAvailable.OK || r.RAMAvailable.Reason != "memavailable_not_present" {
		t.Fatalf("available = %+v, want unavailable/memavailable_not_present", r.RAMAvailable)
	}
}

func TestMeasureLinuxMeminfoMissing(t *testing.T) {
	f := linuxOKFiles()
	delete(f.files, "/proc/meminfo")
	r := measureLinuxSystem(context.Background(), f.read, noSleep, time.Millisecond)
	if r.State != SystemStateUnavailable || r.Error != "proc_meminfo_not_found" {
		t.Fatalf("state/error = %q/%q, want unavailable/proc_meminfo_not_found", r.State, r.Error)
	}
	if r.RAMTotal.Available {
		t.Fatalf("absent source must not be addressable: %+v", r.RAMTotal)
	}
}

func TestMeasureLinuxMeminfoReadError(t *testing.T) {
	f := linuxOKFiles()
	f.errs = map[string]error{"/proc/meminfo": errors.New("io error")}
	r := measureLinuxSystem(context.Background(), f.read, noSleep, time.Millisecond)
	if r.State != SystemStateFailed || r.Error != "meminfo_read_error" {
		t.Fatalf("state/error = %q/%q, want measurement_failed/meminfo_read_error", r.State, r.Error)
	}
	if !r.RAMTotal.Available {
		t.Fatalf("present-but-broken source stays addressable: %+v", r.RAMTotal)
	}
}

func TestMeasureLinuxMeminfoMalformed(t *testing.T) {
	f := linuxOKFiles()
	f.files["/proc/meminfo"] = []byte("MemFree: 100 kB\n")
	r := measureLinuxSystem(context.Background(), f.read, noSleep, time.Millisecond)
	if r.State != SystemStateFailed || r.Error != "meminfo_malformed" {
		t.Fatalf("state/error = %q/%q, want measurement_failed/meminfo_malformed", r.State, r.Error)
	}
}

func TestMeasureLinuxCoresCPUInfoFallback(t *testing.T) {
	f := linuxOKFiles()
	delete(f.files, "/sys/devices/system/cpu/online")
	f.files["/proc/cpuinfo"] = []byte("processor\t: 0\nmodel name\t: X\n\nprocessor\t: 1\n\nprocessor\t: 2\n\nprocessor\t: 3\n")
	r := measureLinuxSystem(context.Background(), f.read, noSleep, time.Millisecond)
	if r.State != SystemStateOK {
		t.Fatalf("state = %q, want ok", r.State)
	}
	if !r.CPUCores.OK || r.CPUCores.Value != 4 || r.CPUCores.Source != "/proc/cpuinfo processor" {
		t.Fatalf("cores = %+v, want 4 from cpuinfo fallback", r.CPUCores)
	}
}

func TestMeasureLinuxCoresNoSource(t *testing.T) {
	f := linuxOKFiles()
	delete(f.files, "/sys/devices/system/cpu/online")
	r := measureLinuxSystem(context.Background(), f.read, noSleep, time.Millisecond)
	if r.State != SystemStateOK {
		t.Fatalf("state = %q, want ok (optional metric)", r.State)
	}
	if r.CPUCores.OK || r.CPUCores.Available || r.CPUCores.Reason != "cpu_cores_source_not_found" {
		t.Fatalf("cores = %+v", r.CPUCores)
	}
}

func TestMeasureLinuxCoresOnlineMalformed(t *testing.T) {
	f := linuxOKFiles()
	f.files["/sys/devices/system/cpu/online"] = []byte("abc\n")
	r := measureLinuxSystem(context.Background(), f.read, noSleep, time.Millisecond)
	if r.State != SystemStateOK {
		t.Fatalf("state = %q, want ok", r.State)
	}
	if r.CPUCores.OK || r.CPUCores.Reason != "cpu_online_malformed" {
		t.Fatalf("cores = %+v", r.CPUCores)
	}
}

func TestMeasureLinuxCoresOnlineList(t *testing.T) {
	f := linuxOKFiles()
	f.files["/sys/devices/system/cpu/online"] = []byte("0-3,5,7\n")
	r := measureLinuxSystem(context.Background(), f.read, noSleep, time.Millisecond)
	if !r.CPUCores.OK || r.CPUCores.Value != 6 {
		t.Fatalf("cores = %+v, want 6", r.CPUCores)
	}
}

func TestMeasureLinuxUtilStatMissing(t *testing.T) {
	f := linuxOKFiles()
	f.statReads = nil
	r := measureLinuxSystem(context.Background(), f.read, noSleep, time.Millisecond)
	if r.State != SystemStateOK {
		t.Fatalf("state = %q, want ok", r.State)
	}
	if r.CPUUtil.OK || r.CPUUtil.Reason != "proc_stat_not_found" {
		t.Fatalf("util = %+v", r.CPUUtil)
	}
}

func TestMeasureLinuxUtilWindowTimeout(t *testing.T) {
	r := measureLinuxSystem(context.Background(), linuxOKFiles().read, failSleep, time.Millisecond)
	if r.State != SystemStateOK {
		t.Fatalf("state = %q, want ok (optional metric)", r.State)
	}
	if r.CPUUtil.OK || r.CPUUtil.Reason != "measurement_timeout" {
		t.Fatalf("util = %+v", r.CPUUtil)
	}
}

func TestMeasureLinuxUtilZeroDelta(t *testing.T) {
	f := linuxOKFiles()
	f.statReads = []string{linuxStat0, linuxStat0}
	r := measureLinuxSystem(context.Background(), f.read, noSleep, time.Millisecond)
	if r.CPUUtil.OK || r.CPUUtil.Reason != "counter_delta_invalid" {
		t.Fatalf("util = %+v, want unavailable/counter_delta_invalid", r.CPUUtil)
	}
}

func TestMeasureLinuxUtilRegressingDelta(t *testing.T) {
	f := linuxOKFiles()
	f.statReads = []string{linuxStat1, linuxStat0}
	r := measureLinuxSystem(context.Background(), f.read, noSleep, time.Millisecond)
	if r.CPUUtil.OK || r.CPUUtil.Reason != "counter_delta_invalid" {
		t.Fatalf("util = %+v", r.CPUUtil)
	}
}

// --- Windows logic ---

func windowsTimesFunc(seq [][3]uint64, errAt int) windowsCPUTimesFunc {
	n := 0
	return func() (uint64, uint64, uint64, error) {
		i := n
		n++
		if i == errAt {
			return 0, 0, 0, errors.New("GetSystemTimes failed")
		}
		if i >= len(seq) {
			i = len(seq) - 1
		}
		v := seq[i]
		return v[0], v[1], v[2], nil
	}
}

func windowsOKPrimitives() (windowsMemStatusFunc, windowsCPUTimesFunc, windowsCPUCountFunc) {
	mem := func() (uint64, uint64, error) { return 16 * 1024 * 1024 * 1024, 8 * 1024 * 1024 * 1024, nil }
	// idle, kernel, user; kernel includes idle. total0=1000, total1=1200,
	// dIdle=50 => 75% busy.
	times := windowsTimesFunc([][3]uint64{{800, 900, 100}, {850, 1050, 150}}, -1)
	cores := func() (uint32, error) { return 8, nil }
	return mem, times, cores
}

func TestMeasureWindowsFullOK(t *testing.T) {
	mem, times, cores := windowsOKPrimitives()
	r := measureWindowsSystem(context.Background(), mem, times, cores, noSleep, time.Millisecond)
	if r.State != SystemStateOK {
		t.Fatalf("state = %q, want ok (error=%q)", r.State, r.Error)
	}
	if !r.RAMTotal.OK || r.RAMTotal.Value != 16*1024*1024*1024 {
		t.Fatalf("total = %+v", r.RAMTotal)
	}
	if !r.RAMAvailable.OK || r.RAMAvailable.Value != 8*1024*1024*1024 || r.RAMAvailable.Estimated {
		t.Fatalf("available = %+v (must be measured ullAvailPhys)", r.RAMAvailable)
	}
	if !r.CPUCores.OK || r.CPUCores.Value != 8 {
		t.Fatalf("cores = %+v", r.CPUCores)
	}
	if !r.CPUUtil.OK || r.CPUUtil.Value != 75 {
		t.Fatalf("util = %+v, want 75", r.CPUUtil)
	}
}

func TestMeasureWindowsMemCallFails(t *testing.T) {
	_, times, cores := windowsOKPrimitives()
	mem := func() (uint64, uint64, error) { return 0, 0, errors.New("GlobalMemoryStatusEx failed") }
	r := measureWindowsSystem(context.Background(), mem, times, cores, noSleep, time.Millisecond)
	if r.State != SystemStateFailed || r.Error != "globalmemorystatusex_failed" {
		t.Fatalf("state/error = %q/%q", r.State, r.Error)
	}
	if !r.RAMTotal.Available || r.RAMTotal.OK {
		t.Fatalf("kernel32 stays addressable on a failed call: %+v", r.RAMTotal)
	}
}

func TestMeasureWindowsCoresCallFails(t *testing.T) {
	mem, times, _ := windowsOKPrimitives()
	cores := func() (uint32, error) { return 0, errors.New("GetActiveProcessorCount failed") }
	r := measureWindowsSystem(context.Background(), mem, times, cores, noSleep, time.Millisecond)
	if r.State != SystemStateOK {
		t.Fatalf("state = %q, want ok (optional metric)", r.State)
	}
	if r.CPUCores.OK || r.CPUCores.Reason != "getactiveprocessorcount_failed" {
		t.Fatalf("cores = %+v", r.CPUCores)
	}
}

func TestMeasureWindowsTimesFirstCallFails(t *testing.T) {
	mem, _, cores := windowsOKPrimitives()
	times := windowsTimesFunc([][3]uint64{{800, 900, 100}}, 0)
	r := measureWindowsSystem(context.Background(), mem, times, cores, noSleep, time.Millisecond)
	if r.State != SystemStateOK {
		t.Fatalf("state = %q, want ok", r.State)
	}
	if r.CPUUtil.OK || r.CPUUtil.Reason != "getsystemtimes_failed" {
		t.Fatalf("util = %+v", r.CPUUtil)
	}
}

func TestMeasureWindowsTimesSecondCallFails(t *testing.T) {
	mem, _, cores := windowsOKPrimitives()
	times := windowsTimesFunc([][3]uint64{{800, 900, 100}, {850, 1050, 150}}, 1)
	r := measureWindowsSystem(context.Background(), mem, times, cores, noSleep, time.Millisecond)
	if r.CPUUtil.OK || r.CPUUtil.Reason != "getsystemtimes_failed" {
		t.Fatalf("util = %+v", r.CPUUtil)
	}
}

func TestMeasureWindowsUtilZeroDelta(t *testing.T) {
	mem, _, cores := windowsOKPrimitives()
	times := windowsTimesFunc([][3]uint64{{800, 900, 100}, {800, 900, 100}}, -1)
	r := measureWindowsSystem(context.Background(), mem, times, cores, noSleep, time.Millisecond)
	if r.CPUUtil.OK || r.CPUUtil.Reason != "counter_delta_invalid" {
		t.Fatalf("util = %+v", r.CPUUtil)
	}
}

func TestMeasureWindowsUtilWindowTimeout(t *testing.T) {
	mem, times, cores := windowsOKPrimitives()
	r := measureWindowsSystem(context.Background(), mem, times, cores, failSleep, time.Millisecond)
	if r.State != SystemStateOK {
		t.Fatalf("state = %q, want ok", r.State)
	}
	if r.CPUUtil.OK || r.CPUUtil.Reason != "measurement_timeout" {
		t.Fatalf("util = %+v", r.CPUUtil)
	}
}

// --- macOS logic ---

const (
	fakeVMStat = "Mach Virtual Memory Statistics: (page size of 4096 bytes)\n" +
		"Pages free:                             100000.\n" +
		"Pages active:                           200000.\n" +
		"Pages inactive:                         150000.\n" +
		"Pages speculative:                       50000.\n"
	fakeTop = "Processes: 500 total, 2 running\n" +
		"CPU usage: 10.0% user, 5.0% sys, 85.0% idle\n" +
		"CPU usage: 12.5% user, 7.5% sys, 80.0% idle\n"
)

type childResult struct {
	out   string
	token string
}

func darwinOKPrimitives() (darwinSysctlFunc, darwinLookPathFunc, darwinChildRunner) {
	sysctl := func(name string) (uint64, error) {
		switch name {
		case "hw.memsize":
			return 16 * 1024 * 1024 * 1024, nil
		case "hw.logicalcpu":
			return 8, nil
		}
		return 0, errors.New("unknown sysctl")
	}
	look := func(name string) (string, error) {
		if name == "vm_stat" || name == "top" {
			return "/usr/bin/" + name, nil
		}
		return "", exec.ErrNotFound
	}
	run := func(ctx context.Context, path string, args ...string) ([]byte, string) {
		switch path {
		case "/usr/bin/vm_stat":
			return []byte(fakeVMStat), ""
		case "/usr/bin/top":
			return []byte(fakeTop), ""
		}
		return nil, "child_exec_error"
	}
	return sysctl, look, run
}

func TestMeasureDarwinFullOK(t *testing.T) {
	sysctl, look, run := darwinOKPrimitives()
	r := measureDarwinSystem(context.Background(), sysctl, look, run)
	if r.State != SystemStateOK {
		t.Fatalf("state = %q, want ok (error=%q)", r.State, r.Error)
	}
	if !r.RAMTotal.OK || r.RAMTotal.Value != 16*1024*1024*1024 || r.RAMTotal.Estimated {
		t.Fatalf("total = %+v (sysctl is measured)", r.RAMTotal)
	}
	// (100000+150000+50000)*4096 = 1228800000, labeled estimated.
	if !r.RAMAvailable.OK || r.RAMAvailable.Value != 1228800000 || !r.RAMAvailable.Estimated {
		t.Fatalf("available = %+v, want the vm_stat estimate", r.RAMAvailable)
	}
	if !r.CPUCores.OK || r.CPUCores.Value != 8 {
		t.Fatalf("cores = %+v", r.CPUCores)
	}
	// Last CPU usage line: 80% idle => 20% busy.
	if !r.CPUUtil.OK || r.CPUUtil.Value != 20 {
		t.Fatalf("util = %+v, want 20 from the second top sample", r.CPUUtil)
	}
}

func TestMeasureDarwinVMStatMissing(t *testing.T) {
	sysctl, _, run := darwinOKPrimitives()
	look := func(name string) (string, error) {
		if name == "top" {
			return "/usr/bin/top", nil
		}
		return "", exec.ErrNotFound
	}
	r := measureDarwinSystem(context.Background(), sysctl, look, run)
	if r.State != SystemStateOK {
		t.Fatalf("state = %q, want ok (optional metric)", r.State)
	}
	if r.RAMAvailable.OK || r.RAMAvailable.Available || r.RAMAvailable.Reason != "vm_stat_not_found" {
		t.Fatalf("available = %+v", r.RAMAvailable)
	}
}

func TestMeasureDarwinTopMissing(t *testing.T) {
	sysctl, _, run := darwinOKPrimitives()
	look := func(name string) (string, error) {
		if name == "vm_stat" {
			return "/usr/bin/vm_stat", nil
		}
		return "", exec.ErrNotFound
	}
	r := measureDarwinSystem(context.Background(), sysctl, look, run)
	if r.State != SystemStateOK {
		t.Fatalf("state = %q, want ok", r.State)
	}
	if r.CPUUtil.OK || r.CPUUtil.Available || r.CPUUtil.Reason != "top_not_found" {
		t.Fatalf("util = %+v", r.CPUUtil)
	}
}

func TestMeasureDarwinVMStatMalformed(t *testing.T) {
	sysctl, look, _ := darwinOKPrimitives()
	run := func(ctx context.Context, path string, args ...string) ([]byte, string) {
		if path == "/usr/bin/vm_stat" {
			return []byte("garbage output"), ""
		}
		return []byte(fakeTop), ""
	}
	r := measureDarwinSystem(context.Background(), sysctl, look, run)
	if r.State != SystemStateOK {
		t.Fatalf("state = %q, want ok", r.State)
	}
	if r.RAMAvailable.OK || r.RAMAvailable.Reason != "vm_stat_malformed" {
		t.Fatalf("available = %+v", r.RAMAvailable)
	}
}

func TestMeasureDarwinTopMalformed(t *testing.T) {
	sysctl, look, _ := darwinOKPrimitives()
	run := func(ctx context.Context, path string, args ...string) ([]byte, string) {
		if path == "/usr/bin/top" {
			return []byte("no cpu line here"), ""
		}
		return []byte(fakeVMStat), ""
	}
	r := measureDarwinSystem(context.Background(), sysctl, look, run)
	if r.State != SystemStateOK {
		t.Fatalf("state = %q, want ok", r.State)
	}
	if r.CPUUtil.OK || r.CPUUtil.Reason != "top_output_malformed" {
		t.Fatalf("util = %+v", r.CPUUtil)
	}
}

func TestMeasureDarwinChildFailureToken(t *testing.T) {
	sysctl, look, _ := darwinOKPrimitives()
	run := func(ctx context.Context, path string, args ...string) ([]byte, string) {
		return nil, "measurement_timeout"
	}
	r := measureDarwinSystem(context.Background(), sysctl, look, run)
	if r.State != SystemStateOK {
		t.Fatalf("state = %q, want ok", r.State)
	}
	if r.RAMAvailable.OK || r.RAMAvailable.Reason != "measurement_timeout" {
		t.Fatalf("available = %+v (child token must propagate)", r.RAMAvailable)
	}
	if r.CPUUtil.OK || r.CPUUtil.Reason != "measurement_timeout" {
		t.Fatalf("util = %+v", r.CPUUtil)
	}
}

func TestMeasureDarwinMemsizeFails(t *testing.T) {
	_, look, run := darwinOKPrimitives()
	sysctl := func(name string) (uint64, error) {
		if name == "hw.logicalcpu" {
			return 8, nil
		}
		return 0, errors.New("sysctl failed")
	}
	r := measureDarwinSystem(context.Background(), sysctl, look, run)
	if r.State != SystemStateFailed || r.Error != "sysctl_hw_memsize_failed" {
		t.Fatalf("state/error = %q/%q", r.State, r.Error)
	}
}

// --- parser units ---

func TestParseMeminfo(t *testing.T) {
	total, avail, present, err := parseMeminfo([]byte(linuxMeminfo))
	if err != nil || total != 16384000 || avail != 8192000 || !present {
		t.Fatalf("parse = %d/%d/%v/%v", total, avail, present, err)
	}
	_, _, present, err = parseMeminfo([]byte("MemTotal: 100 kB\n"))
	if err != nil || present {
		t.Fatalf("absent MemAvailable: present=%v err=%v", present, err)
	}
	for _, bad := range []string{"", "MemFree: 1 kB\n", "MemTotal: abc kB\n", "MemTotal: -5 kB\n", "MemTotal:\n"} {
		if _, _, _, err := parseMeminfo([]byte(bad)); err != errSystemMalformed {
			t.Fatalf("parseMeminfo(%q) err = %v, want malformed", bad, err)
		}
	}
}

func TestParseCPUOnline(t *testing.T) {
	cases := []struct {
		in   string
		want int
		bad  bool
	}{
		{"0-7\n", 8, false},
		{"0-3,5,7", 6, false},
		{"0", 1, false},
		{"", 0, true},
		{"abc", 0, true},
		{"7-3", 0, true},
		{"-1", 0, true},
		{"0-", 0, true},
	}
	for _, tc := range cases {
		n, err := parseCPUOnline([]byte(tc.in))
		if tc.bad {
			if err != errSystemMalformed {
				t.Fatalf("parseCPUOnline(%q) err = %v, want malformed", tc.in, err)
			}
			continue
		}
		if err != nil || n != tc.want {
			t.Fatalf("parseCPUOnline(%q) = %d, %v; want %d", tc.in, n, err, tc.want)
		}
	}
}

func TestParseProcStatCPU(t *testing.T) {
	total, idle, err := parseProcStatCPU([]byte(linuxStat0))
	if err != nil || total != 1000 || idle != 850 {
		t.Fatalf("parse = %d/%d/%v, want 1000/850", total, idle, err)
	}
	for _, bad := range []string{"", "cpu  1 2 3\n", "cpu  1 2 3 4 5 6 7 x\n", "cpu0 1 2 3 4 5 6 7 8\n"} {
		if _, _, err := parseProcStatCPU([]byte(bad)); err != errSystemMalformed {
			t.Fatalf("parseProcStatCPU(%q) err = %v, want malformed", bad, err)
		}
	}
}

func TestCPUUtilFromDelta(t *testing.T) {
	if v, ok := cpuUtilFromDelta(1000, 850, 1200, 900); !ok || v != 75 {
		t.Fatalf("delta = %d/%v, want 75/true", v, ok)
	}
	if _, ok := cpuUtilFromDelta(1000, 850, 1000, 850); ok {
		t.Fatal("zero delta must yield no value")
	}
	if _, ok := cpuUtilFromDelta(1000, 850, 900, 800); ok {
		t.Fatal("regressing total must yield no value")
	}
	if _, ok := cpuUtilFromDelta(1000, 850, 1200, 800); ok {
		t.Fatal("regressing idle must yield no value")
	}
	if _, ok := cpuUtilFromDelta(1000, 100, 1100, 300); ok {
		t.Fatal("idle delta larger than total delta must yield no value")
	}
	if v, ok := cpuUtilFromDelta(0, 0, 100, 100); !ok || v != 0 {
		t.Fatalf("fully idle = %d/%v, want 0/true", v, ok)
	}
	if v, ok := cpuUtilFromDelta(0, 0, 100, 0); !ok || v != 100 {
		t.Fatalf("fully busy = %d/%v, want 100/true", v, ok)
	}
}

func TestRoundPercent(t *testing.T) {
	cases := []struct {
		busy, total, want int64
	}{
		{1, 3, 33}, {2, 3, 67}, {1, 200, 1}, {0, 10, 0}, {10, 10, 100},
		{20, 10, 100}, {-5, 10, 0}, {1, 0, 0},
	}
	for _, tc := range cases {
		if got := roundPercent(tc.busy, tc.total); got != tc.want {
			t.Fatalf("roundPercent(%d,%d) = %d, want %d", tc.busy, tc.total, got, tc.want)
		}
	}
}

func TestParseVMStatAvailable(t *testing.T) {
	v, err := parseVMStatAvailable([]byte(fakeVMStat))
	if err != nil || v != 1228800000 {
		t.Fatalf("parse = %d/%v, want 1228800000", v, err)
	}
	for _, bad := range []string{
		"",
		"garbage",
		"Mach Virtual Memory Statistics: (page size of 4096 bytes)\nPages free: 1.\nPages inactive: 2.\n", // no speculative
		"Pages free: 1.\nPages inactive: 2.\nPages speculative: 3.\n",                                     // no page size
		"Mach Virtual Memory Statistics: (page size of 0 bytes)\nPages free: 1.\nPages inactive: 2.\nPages speculative: 3.\n",
	} {
		if _, err := parseVMStatAvailable([]byte(bad)); err != errSystemMalformed {
			t.Fatalf("parseVMStatAvailable(%q) err = %v, want malformed", bad, err)
		}
	}
}

func TestParseTopCPUUtil(t *testing.T) {
	v, err := parseTopCPUUtil([]byte(fakeTop))
	if err != nil || v != 20 {
		t.Fatalf("parse = %d/%v, want 20 (last sample)", v, err)
	}
	v, err = parseTopCPUUtil([]byte("CPU usage: 0.0% user, 0.0% sys, 99.6% idle\n"))
	if err != nil || v != 0 {
		t.Fatalf("round(100-99.6) = %d/%v, want 0", v, err)
	}
	for _, bad := range []string{
		"",
		"no cpu line",
		"CPU usage: 10% user, 5% sys\n", // no idle part
		"CPU usage: 10% user, 5% sys, 150% idle\n",
		"CPU usage: 10% user, 5% sys, abc% idle\n",
	} {
		if _, err := parseTopCPUUtil([]byte(bad)); err != errSystemMalformed {
			t.Fatalf("parseTopCPUUtil(%q) err = %v, want malformed", bad, err)
		}
	}
}
