//go:build !windows

package platform

import (
	"context"
	"errors"
	"testing"
)

// TestUnixServiceManagerStub verifies that every verb on a non-Windows
// platform returns the bounded "not supported" error (ADR 011 D1.4,
// acceptance item 10).
func TestUnixServiceManagerStub(t *testing.T) {
	m := NewServiceManager()

	if m.IsService() {
		t.Fatal("IsService must be false on non-Windows platforms")
	}

	if err := m.RunService(ServiceRunOptions{Name: "GoAl", RunApp: func(ctx context.Context) error { return nil }}); !errors.Is(err, ErrServiceUnsupported) {
		t.Errorf("RunService: %v, want ErrServiceUnsupported", err)
	}
	if err := m.Install(InstallRequest{Name: "GoAl"}); !errors.Is(err, ErrServiceUnsupported) {
		t.Errorf("Install: %v, want ErrServiceUnsupported", err)
	}
	if err := m.Uninstall("GoAl"); !errors.Is(err, ErrServiceUnsupported) {
		t.Errorf("Uninstall: %v, want ErrServiceUnsupported", err)
	}
	if err := m.Start("GoAl"); !errors.Is(err, ErrServiceUnsupported) {
		t.Errorf("Start: %v, want ErrServiceUnsupported", err)
	}
	if err := m.Stop("GoAl"); !errors.Is(err, ErrServiceUnsupported) {
		t.Errorf("Stop: %v, want ErrServiceUnsupported", err)
	}
	if err := m.Restart("GoAl"); !errors.Is(err, ErrServiceUnsupported) {
		t.Errorf("Restart: %v, want ErrServiceUnsupported", err)
	}
	if _, err := m.Status("GoAl"); !errors.Is(err, ErrServiceUnsupported) {
		t.Errorf("Status: %v, want ErrServiceUnsupported", err)
	}
}
