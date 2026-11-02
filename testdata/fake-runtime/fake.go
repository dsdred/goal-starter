package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// fake-runtime is a test executable for Process Manager integration tests.
//
// Usage: fake-runtime <mode> [options]
//
// Modes:
//   - stdout          Print lines to stdout and exit 0
//   - stderr          Print lines to stderr and exit 0
//   - child           Spawn a child process and wait for it
//   - graceful        Exit 0 on SIGTERM or SIGINT
//   - ignored-signal  Ignore SIGTERM, exit 0 on SIGINT
//   - delayed         Wait N seconds then exit with code
//   - exit-code       Exit with specified code immediately
//   - infinite        Run until killed (SIGKILL)
//   - flood [ms] [n]  Print numbered lines every ms (default 10) until n lines
//                     are printed (0 or omitted = run until killed)
//   - echo            Print all remaining arguments and wait briefly
//   - argv-file       Write the remaining arguments (after the output path),
//                     one per line, to the given file, then stay alive
//   - env-file        Write selected environment values to a file and exit
//   - signal-delay <marker-dir> [exit-delay-ms]
//                     Write ready.json after signal registration, then on the
//                     real OS stop signal write signal.json atomically, sleep
//                     exit-delay-ms (default 4000) and exit 0
//   - signal-hold <marker-dir> [safety-ms]
//                     Write ready.json after signal registration, then on the
//                     real OS stop signal write signal.json atomically and stay
//                     alive until release.json appears, then write released.json
//                     atomically and exit 0. safety-ms (default 120000) bounds a
//                     stale manual run only; it is not a synchronization
//                     mechanism, and its expiry exits 1.

func main() {
	// Check for -sleep <duration> flag (used by supervisor tests).
	sleepSeconds := 0
	if len(os.Args) >= 3 && os.Args[1] == "-sleep" {
		if n, err := strconv.Atoi(os.Args[2]); err == nil && n >= 0 {
			sleepSeconds = n
		} else if err == nil {
			os.Exit(1)
		}
	}
	if sleepSeconds > 0 {
		time.Sleep(time.Duration(sleepSeconds) * time.Second)
		os.Exit(0)
	}

	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: fake-runtime <mode> [options]\n")
		os.Exit(1)
	}

	mode := os.Args[1]

	switch mode {
	case "stdout":
		doStdout()
	case "stderr":
		doStderr()
	case "child":
		doChild()
	case "graceful":
		doGraceful()
	case "ignored-signal":
		doIgnoredSignal()
	case "delayed":
		doDelayed()
	case "exit-code":
		doExitCode()
	case "infinite":
		doInfinite()
	case "flood":
		doFlood()
	case "echo":
		fmt.Println(strings.Join(os.Args[2:], " "))
		time.Sleep(2 * time.Second)
	case "argv-file":
		doArgvFile()
	case "env-file":
		doEnvFile()
	case "signal-delay":
		doSignalDelay()
	case "signal-hold":
		doSignalHold()
	default:
		fmt.Fprintf(os.Stderr, "Unknown mode: %s\n", mode)
		os.Exit(1)
	}
}

// doArgvFile writes the arguments that follow the output path (os.Args[3:]),
// one per line, to the file named by os.Args[2]. It then blocks forever so the
// supervisor sees a running instance. This exposes the exact argv tokens the
// child process received after the OS command-line round-trip, which is what a
// tokenizer regression must guarantee.
func doArgvFile() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "argv-file mode: need an output path")
		os.Exit(1)
	}
	rest := os.Args[3:]
	if err := writeFileAtomic(os.Args[2], []byte(strings.Join(rest, "\n"))); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	// Stay alive until the supervisor stops the process. A bare `select {}`
	// on the main goroutine is killed by the Go runtime deadlock detector
	// ("all goroutines are asleep", exit 2) whenever no other runtime
	// activity is in flight — a timing-dependent flake. A pending timer plus
	// a signal handler thread keeps the runtime alive deterministically
	// (same pattern as "infinite"/"graceful" modes); this mode stays silent.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	for {
		select {
		case <-sigCh:
			os.Exit(0)
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func doEnvFile() {
	if len(os.Args) < 4 {
		fmt.Fprintln(os.Stderr, "env-file mode: need output path and at least one environment key")
		os.Exit(1)
	}
	lines := make([]string, 0, len(os.Args)-3)
	for _, key := range os.Args[3:] {
		lines = append(lines, key+"="+os.Getenv(key))
	}
	if err := writeFileAtomic(os.Args[2], []byte(strings.Join(lines, "\n"))); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// writeFileAtomic replaces path via a same-directory temp file, so readers that
// poll for a rewrite never observe the truncated state of an incomplete write.
func writeFileAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".fake-runtime-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

const signalDelayDefaultMS = 4000

// signalDelayMarker is the JSON marker shared by the two lifecycle fixtures,
// signal-delay and signal-hold. ExitDelayMS is empty for the hold mode.
type signalDelayMarker struct {
	Event       string `json:"event"`
	PID         int    `json:"pid"`
	Executable  string `json:"executable"`
	Signal      string `json:"signal,omitempty"`
	ExitDelayMS int    `json:"exit_delay_ms,omitempty"`
	Timestamp   string `json:"timestamp"`
}

// doSignalDelay provides a controlled graceful-shutdown dwell for lifecycle
// tests: the process acknowledges a real OS stop signal, records that receipt,
// stays alive for the configured delay, then exits normally.
func doSignalDelay() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "signal-delay mode: need a marker directory")
		os.Exit(1)
	}
	markerDir := os.Args[2]
	exitDelayMS := signalDelayDefaultMS
	if len(os.Args) > 3 {
		n, err := strconv.Atoi(os.Args[3])
		if err != nil || n < 0 {
			fmt.Fprintln(os.Stderr, "signal-delay mode: exit delay must be a non-negative integer of milliseconds")
			os.Exit(1)
		}
		exitDelayMS = n
	}
	if err := os.MkdirAll(markerDir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	executable, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	ready := signalDelayMarker{
		Event:      "ready",
		PID:        os.Getpid(),
		Executable: executable,
		Timestamp:  time.Now().UTC().Format(time.RFC3339Nano),
	}
	if err := writeSignalDelayMarker(filepath.Join(markerDir, "ready.json"), ready); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	sig := <-sigCh
	received := signalDelayMarker{
		Event:       "signal",
		PID:         os.Getpid(),
		Executable:  executable,
		Signal:      sig.String(),
		ExitDelayMS: exitDelayMS,
		Timestamp:   time.Now().UTC().Format(time.RFC3339Nano),
	}
	if err := writeSignalDelayMarker(filepath.Join(markerDir, "signal.json"), received); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	time.Sleep(time.Duration(exitDelayMS) * time.Millisecond)
}

func writeSignalDelayMarker(path string, marker signalDelayMarker) error {
	data, err := json.Marshal(marker)
	if err != nil {
		return err
	}
	return writeFileAtomic(path, append(data, '\n'))
}

const signalHoldDefaultSafetyMS = 120000

// doSignalHold provides a release-held graceful-shutdown window for lifecycle
// tests. Unlike signal-delay it does not end on a clock: after the real OS stop
// signal it records receipt and then stays alive until the test writes
// release.json, so the interval between "stopping" being published and the
// process exiting is a barrier rather than a race.
//
// Marker protocol, each written atomically:
//
//	ready.json     signal handlers registered, process alive
//	signal.json    real OS stop signal received
//	released.json  release.json observed, process exiting 0
//
// The optional third argument is a safety bound for a stale manual run
// (default 120000 ms). It is NOT a synchronization mechanism: the test holds
// the process through release.json, and an expired bound exits 1 so the stop
// and the test fail loudly instead of observing a truncated window.
func doSignalHold() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "signal-hold mode: need a marker directory")
		os.Exit(1)
	}
	markerDir := os.Args[2]
	safetyMS := signalHoldDefaultSafetyMS
	if len(os.Args) > 3 {
		n, err := strconv.Atoi(os.Args[3])
		if err != nil || n <= 0 {
			fmt.Fprintln(os.Stderr, "signal-hold mode: safety bound must be a positive integer of milliseconds")
			os.Exit(1)
		}
		safetyMS = n
	}
	if err := os.MkdirAll(markerDir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	executable, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	timestamp := func() string { return time.Now().UTC().Format(time.RFC3339Nano) }

	ready := signalDelayMarker{
		Event:      "ready",
		PID:        os.Getpid(),
		Executable: executable,
		Timestamp:  timestamp(),
	}
	if err := writeSignalDelayMarker(filepath.Join(markerDir, "ready.json"), ready); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	sig := <-sigCh
	received := signalDelayMarker{
		Event:      "signal",
		PID:        os.Getpid(),
		Executable: executable,
		Signal:     sig.String(),
		Timestamp:  timestamp(),
	}
	if err := writeSignalDelayMarker(filepath.Join(markerDir, "signal.json"), received); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	releasePath := filepath.Join(markerDir, "release.json")
	deadline := time.Now().Add(time.Duration(safetyMS) * time.Millisecond)
	for {
		if _, statErr := os.Stat(releasePath); statErr == nil {
			break
		} else if !os.IsNotExist(statErr) {
			fmt.Fprintln(os.Stderr, statErr)
			os.Exit(1)
		}
		if !time.Now().Before(deadline) {
			fmt.Fprintf(os.Stderr, "signal-hold mode: no release.json within %d ms\n", safetyMS)
			os.Exit(1)
		}
		time.Sleep(5 * time.Millisecond)
	}

	released := signalDelayMarker{
		Event:      "released",
		PID:        os.Getpid(),
		Executable: executable,
		Timestamp:  timestamp(),
	}
	if err := writeSignalDelayMarker(filepath.Join(markerDir, "released.json"), released); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func doStdout() {
	for i := 0; i < 10; i++ {
		fmt.Println("fake-output-line-" + strconv.Itoa(i))
		time.Sleep(100 * time.Millisecond)
	}
}

func doStderr() {
	for i := 0; i < 10; i++ {
		fmt.Fprintln(os.Stderr, "fake-error-line-"+strconv.Itoa(i))
		time.Sleep(100 * time.Millisecond)
	}
}

func doChild() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "child mode: need child command")
		os.Exit(1)
	}
	cmd := exec.Command(os.Args[2], os.Args[3:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func doGraceful() {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	<-sigCh
}

func doIgnoredSignal() {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM)
	signal.Notify(sigCh, syscall.SIGINT)

	for {
		select {
		case sig := <-sigCh:
			if sig == syscall.SIGINT {
				os.Exit(0)
			}
			// SIGTERM — ignore
		case <-time.After(200 * time.Millisecond):
			fmt.Println("alive")
		}
	}
}

func doDelayed() {
	seconds := 2 // default
	if len(os.Args) > 2 {
		if n, err := strconv.Atoi(os.Args[2]); err == nil {
			seconds = n
		}
	}
	exitCode := 0
	if len(os.Args) > 3 {
		if n, err := strconv.Atoi(os.Args[3]); err == nil {
			exitCode = n
		}
	}

	time.Sleep(time.Duration(seconds) * time.Second)
	os.Exit(exitCode)
}

func doExitCode() {
	code := 42
	if len(os.Args) > 2 {
		if n, err := strconv.Atoi(os.Args[2]); err == nil {
			code = n
		}
	}
	os.Exit(code)
}

func doInfinite() {
	if len(os.Args) > 2 {
		fmt.Println(strings.Join(os.Args[2:], " "))
	}
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM)
	signal.Notify(sigCh, syscall.SIGINT)

	for {
		select {
		case sig := <-sigCh:
			_ = sig
			// May be killed before signal arrives
		case <-time.After(200 * time.Millisecond):
			fmt.Println("running")
		}
	}
}

// doFlood prints numbered lines at a fixed interval to build a large log
// history quickly for stress tests. Args: [intervalMs] [totalLines].
// intervalMs default 10; totalLines 0 or omitted = run until killed.
func doFlood() {
	intervalMs := 10
	total := 0
	if len(os.Args) > 2 {
		if n, err := strconv.Atoi(os.Args[2]); err == nil && n >= 0 {
			intervalMs = n
		}
	}
	if len(os.Args) > 3 {
		if n, err := strconv.Atoi(os.Args[3]); err == nil && n > 0 {
			total = n
		}
	}
	for i := 0; ; i++ {
		fmt.Println("flood-line-" + strconv.Itoa(i))
		if total > 0 && i+1 >= total {
			break
		}
		if intervalMs > 0 {
			time.Sleep(time.Duration(intervalMs) * time.Millisecond)
		}
	}
}
