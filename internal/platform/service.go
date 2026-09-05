package platform

import (
	"context"
	"errors"
	"time"
)

// ErrServiceUnsupported is returned by every ServiceManager verb on platforms
// where service mode does not exist (ADR 011 D1.4: systemd owns the lifecycle
// on Linux, so the binary stays single and has no in-service mode).
var ErrServiceUnsupported = errors.New("service mode is not supported on this platform")

// DefaultStopTimeout is the registered SCM outer stop/wait budget
// (ADR 011 D6.3): strictly greater than the 30 s application shutdown
// budget so the application always finishes first — including the Job Object
// force-kill last resort — before the SCM can hard-kill.
const DefaultStopTimeout = 45 * time.Second

// ServiceStartType selects the SCM start type at install time (ADR 011 D5).
type ServiceStartType int

const (
	// StartTypeAuto starts the service on boot (default).
	StartTypeAuto ServiceStartType = iota
	// StartTypeManual starts the service only on explicit demand.
	StartTypeManual
)

// InstallRequest describes the service image to register (ADR 011 D2/D4/D5/D6).
type InstallRequest struct {
	Name        string // service name, e.g. GoAl
	DisplayName string
	Description string
	// ExePath is the absolute, cleaned path of the goal binary (D2).
	ExePath string
	// ConfigPath is the absolute, cleaned config path embedded in the
	// registered service image (D2).
	ConfigPath string
	StartType  ServiceStartType
	// StopTimeout is the SCM outer stop/wait budget (45 s default), strictly
	// greater than the 30 s application shutdown budget (ADR 011 D6.3).
	StopTimeout time.Duration
}

// ServiceStatus is the observed SCM state (ADR 011 D6.5).
type ServiceStatus struct {
	State     string
	PID       uint32
	Uptime    time.Duration
	Win32Exit uint32
}

// ServiceRunOptions configures the in-process SCM entrypoint
// (goal --service run, ADR 011 D1.2/D6.1).
type ServiceRunOptions struct {
	// Name is the registered service name used with the SCM dispatcher.
	Name string
	// RunApp executes the shared application lifecycle (the exact foreground
	// sequence) under ctx and blocks until the SCM stop request cancels ctx
	// or the application exits on its own. A non-nil return is a failure.
	RunApp func(ctx context.Context) error
	// ServeAddr is the "host:port" the application binds; the handler reports
	// Running to the SCM only after this address accepts connections.
	ServeAddr string
	// StatusText returns Supervisor-derived state (instance counts per state)
	// for the SCM Interrogate response (ADR 011 D6.4).
	StatusText func() string
}

// ServiceManager abstracts Windows SCM operations behind a single interface so
// the management verbs live in tested Go code and CI can fake them
// (ADR 011 D1.4, acceptance item 10).
type ServiceManager interface {
	// IsService reports whether the current process was launched by the SCM.
	IsService() bool

	// RunService blocks as the SCM service until stopped (the run verb).
	RunService(opts ServiceRunOptions) error

	// Install registers the service without starting it (ADR 011 D5).
	Install(req InstallRequest) error

	// Uninstall stops a running service via the graceful D6.2 path before
	// deleting the registration; user data is never touched (ADR 011 D9).
	Uninstall(name string) error

	// Start requests an SCM start and waits until the SCM reports Running.
	Start(name string) error

	// Stop requests an SCM stop and waits until the SCM reports Stopped.
	Stop(name string) error

	// Restart performs Stop → wait Stopped → Start → wait Running
	// (ADR 011 D7). No self-reexec, no second process.
	Restart(name string) error

	// Status returns the SCM state, PID and uptime (ADR 011 D6.5).
	Status(name string) (ServiceStatus, error)
}

// NewServiceManager returns the platform ServiceManager implementation.
func NewServiceManager() ServiceManager {
	return newServiceManager()
}
