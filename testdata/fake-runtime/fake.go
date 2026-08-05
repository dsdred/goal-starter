package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
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
//   - ignored-signal  Ignore SIGTERM, exit 0 on SIGINT
//   - delayed         Wait N seconds then exit with code
//   - exit-code       Exit with specified code immediately
//   - infinite        Run until killed (SIGKILL)

func main() {
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
	case "ignored-signal":
		doIgnoredSignal()
	case "delayed":
		doDelayed()
	case "exit-code":
		doExitCode()
	case "infinite":
		doInfinite()
	default:
		fmt.Fprintf(os.Stderr, "Unknown mode: %s\n", mode)
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
