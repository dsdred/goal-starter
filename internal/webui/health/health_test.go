package health

import (
	"net"
	"testing"
	"time"
)

func TestNewHealthChecker(t *testing.T) {
	h := NewHealthChecker()
	if h == nil {
		t.Fatal("expected HealthChecker")
	}
}

func TestCheckAll_NoRuntimes(t *testing.T) {
	h := NewHealthChecker()
	results := h.CheckAll()
	if results != nil {
		t.Fatalf("expected nil results, got %d", len(results))
	}
}

func TestCheckAll_SingleHealthyRuntime(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("unable to listen: %v", err)
	}
	defer ln.Close()

	port := ln.Addr().(*net.TCPAddr).Port
	h := NewHealthChecker()
	h.UpdateRuntimes([]RuntimeDef{
		{ID: "rt1", Name: "test-runtime", Host: "127.0.0.1", Port: port},
	})

	results := h.CheckAll()
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	r := results[0]
	if !r.Healthy {
		t.Error("expected runtime to be healthy")
	}
	if r.ID != "rt1" {
		t.Errorf("expected ID rt1, got %s", r.ID)
	}
	if r.Name != "test-runtime" {
		t.Errorf("expected name test-runtime, got %s", r.Name)
	}
	if r.Latency <= 0 {
		t.Error("expected positive latency")
	}
}

func TestCheckAll_SingleUnhealthyRuntime(t *testing.T) {
	h := NewHealthChecker()
	h.UpdateRuntimes([]RuntimeDef{
		{ID: "rt2", Name: "bad-runtime", Host: "127.0.0.1", Port: 1},
	})

	results := h.CheckAll()
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	r := results[0]
	if r.Healthy {
		t.Error("expected runtime to be unhealthy")
	}
	if r.Error == "" {
		t.Error("expected error message")
	}
}

func TestGetResults_Empty(t *testing.T) {
	h := NewHealthChecker()
	results := h.GetResults()
	if results == nil {
		t.Fatal("expected empty slice, got nil")
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results, got %d", len(results))
	}
}

func TestGetResults_Populated(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("unable to listen: %v", err)
	}
	defer ln.Close()

	port := ln.Addr().(*net.TCPAddr).Port
	h := NewHealthChecker()
	h.UpdateRuntimes([]RuntimeDef{
		{ID: "rt3", Name: "test", Host: "127.0.0.1", Port: port},
	})

	_ = h.CheckAll()

	results := h.GetResults()
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if !results[0].Healthy {
		t.Error("expected runtime to be healthy")
	}
}

func TestCheckRuntime_Found(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("unable to listen: %v", err)
	}
	defer ln.Close()

	port := ln.Addr().(*net.TCPAddr).Port
	h := NewHealthChecker()
	h.UpdateRuntimes([]RuntimeDef{
		{ID: "rt4", Name: "test-runtime", Host: "127.0.0.1", Port: port},
	})

	result, err := h.CheckRuntime("rt4")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected result, got nil")
	}
	if !result.Healthy {
		t.Error("expected runtime to be healthy")
	}
}

func TestCheckRuntime_NotFound(t *testing.T) {
	h := NewHealthChecker()
	h.UpdateRuntimes([]RuntimeDef{
		{ID: "rt5", Name: "test", Host: "127.0.0.1", Port: 8080},
	})

	result, err := h.CheckRuntime("rt_nonexistent")
	if err == nil {
		t.Fatal("expected error for non-existent runtime")
	}
	if result != nil {
		t.Errorf("expected nil result, got %+v", result)
	}
}

func TestCheckRuntime_Unhealthy(t *testing.T) {
	h := NewHealthChecker()
	h.UpdateRuntimes([]RuntimeDef{
		{ID: "rt6", Name: "bad", Host: "127.0.0.1", Port: 1},
	})

	result, err := h.CheckRuntime("rt6")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected result, got nil")
	}
	if result.Healthy {
		t.Error("expected runtime to be unhealthy")
	}
	if result.Error == "" {
		t.Error("expected error message for unhealthy runtime")
	}
}

func TestUpdateRuntimes(t *testing.T) {
	h := NewHealthChecker()

	// Empty.
	h.UpdateRuntimes(nil)

	// Add runtimes.
	h.UpdateRuntimes([]RuntimeDef{
		{ID: "rt7", Name: "test1", Host: "127.0.0.1", Port: 8080},
		{ID: "rt8", Name: "test2", Host: "127.0.0.1", Port: 8081},
	})

	// CheckAll with 2 unhealthy.
	results := h.CheckAll()
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	for i, r := range results {
		if r.Healthy {
			t.Errorf("result %d: expected unhealthy (address %s)", i, r.Address)
		}
	}
}

func TestCheckResult_JSONFields(t *testing.T) {
	r := &CheckResult{
		ID:      "test-id",
		Name:    "test-name",
		Healthy: true,
		Address: "127.0.0.1:8080",
		Latency: 5 * time.Millisecond,
	}
	if r.ID != "test-id" {
		t.Errorf("expected test-id, got %s", r.ID)
	}
	if r.Name != "test-name" {
		t.Errorf("expected test-name, got %s", r.Name)
	}
	if !r.Healthy {
		t.Error("expected Healthy true")
	}
	if r.Address != "127.0.0.1:8080" {
		t.Errorf("expected 127.0.0.1:8080, got %s", r.Address)
	}
}

func TestCheckResult_EmptyError(t *testing.T) {
	r := &CheckResult{
		ID:      "test",
		Name:    "test",
		Healthy: true,
	}
	if r.Error != "" {
		t.Error("expected empty error for healthy result")
	}
}
