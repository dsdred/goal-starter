//go:build windows

package platform

import (
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

type windowsControl struct {
	mu          sync.Mutex
	job         windows.Handle
	proc        *os.Process
	procGroup   []uint32 // PIDs in the job for cleanup tracking
	once        sync.Once
	closed      bool
	killedByJob bool // true if killed via Job Object kill-on-close
}

var procSetJobInformation = windows.NewLazyDLL("kernel32.dll").NewProc("SetInformationJobObject")

const jobObjectExtendedLimitInformation = 9

func prepare(cmd *exec.Cmd) (ProcessControl, error) {
	// Create Job Object.
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("create job object: %w", err)
	}

	// Set kill-on-close using JOBOBJECT_EXTENDED_LIMIT_INFORMATION.
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}

	h := uintptr(job)
	ret, _, err := procSetJobInformation.Call(
		h,
		jobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uintptr(unsafe.Sizeof(info)),
	)
	if ret == 0 {
		windows.CloseHandle(job)
		return nil, fmt.Errorf("SetInformationJobObject: %w", err)
	}

	// Store job in SysProcAttr so it's inherited by child processes.
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_UNICODE_ENVIRONMENT,
	}

	return &windowsControl{
		job: job,
	}, nil
}

func (w *windowsControl) AfterStart(pid int) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	proc, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("find process %d: %w", pid, err)
	}

	// Open a real handle to the process for AssignProcessToJobObject.
	h, err := windows.OpenProcess(windows.PROCESS_ALL_ACCESS|windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return fmt.Errorf("open process %d: %w", pid, err)
	}

	// Assign process to the job.
	if err := windows.AssignProcessToJobObject(w.job, h); err != nil {
		windows.CloseHandle(h)
		return fmt.Errorf("assign process %d to job: %w", pid, err)
	}
	w.proc = proc
	w.procGroup = append(w.procGroup, uint32(pid))
	return nil
}

var procGenerateConsoleCtrlEvent = windows.NewLazySystemDLL("kernel32.dll").NewProc("GenerateConsoleCtrlEvent")

func (w *windowsControl) GracefulStop() error {
	w.mu.Lock()
	proc := w.proc
	pid := 0
	if proc != nil {
		pid = proc.Pid
	}
	w.mu.Unlock()

	if proc == nil {
		return nil
	}

	// Send CTRL_BREAK_EVENT to the child's process group. The process group ID
	// on Windows equals the PID of the process group leader.  GenerateConsoleCtrlEvent
	// returns non-zero on success.
	const ctrlBreakEvent = 1
	ret, _, _ := procGenerateConsoleCtrlEvent.Call(
		uintptr(ctrlBreakEvent),
		uintptr(pid),
	)
	if ret != 0 {
		return nil
	}

	// Fallback: signal the main process directly if GenerateConsoleCtrlEvent failed.
	return proc.Signal(os.Interrupt)
}

func (w *windowsControl) ForceKill() error {
	w.mu.Lock()
	job := w.job
	proc := w.proc
	w.mu.Unlock()

	if job == 0 {
		w.mu.Lock()
		w.killedByJob = true
		w.mu.Unlock()
		return nil
	}

	// Close job to trigger kill-on-close for all processes in the job.
	w.mu.Lock()
	if !w.closed {
		windows.CloseHandle(job)
		w.closed = true
		w.killedByJob = true
	}
	w.mu.Unlock()

	// Fallback: terminate the main process if it's still alive.
	if proc != nil {
		p2, openErr := os.FindProcess(proc.Pid)
		if openErr == nil {
			h, handleErr := windows.OpenProcess(windows.PROCESS_TERMINATE, false, uint32(proc.Pid))
			if handleErr == nil {
				windows.TerminateProcess(h, 1)
				windows.CloseHandle(h)
			}
			p2.Release()
		}
	}
	return nil
}

func (w *windowsControl) WasSignaled() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.killedByJob
}

func (w *windowsControl) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.once.Do(func() {
		w.closed = true
		if w.job != 0 {
			windows.CloseHandle(w.job)
			w.job = 0
		}
		if w.proc != nil {
			w.proc.Release()
			w.proc = nil
		}
	})
	return nil
}
