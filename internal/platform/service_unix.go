//go:build !windows

package platform

// unixServiceManager is the bounded stub for non-Windows platforms
// (ADR 011 D1.4): every verb returns ErrServiceUnsupported.
type unixServiceManager struct{}

func newServiceManager() ServiceManager {
	return &unixServiceManager{}
}

func (*unixServiceManager) IsService() bool { return false }

func (*unixServiceManager) RunService(ServiceRunOptions) error {
	return ErrServiceUnsupported
}

func (*unixServiceManager) Install(InstallRequest) error {
	return ErrServiceUnsupported
}

func (*unixServiceManager) Uninstall(string) error {
	return ErrServiceUnsupported
}

func (*unixServiceManager) Start(string) error {
	return ErrServiceUnsupported
}

func (*unixServiceManager) Stop(string) error {
	return ErrServiceUnsupported
}

func (*unixServiceManager) Restart(string) error {
	return ErrServiceUnsupported
}

func (*unixServiceManager) Status(string) (ServiceStatus, error) {
	return ServiceStatus{}, ErrServiceUnsupported
}
