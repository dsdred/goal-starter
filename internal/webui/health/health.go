package health

import (
	"errors"
	"fmt"
	"net"
	"sync"
	"time"
)

// CheckResult represents the result of a single runtime health check.
type CheckResult struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Healthy bool   `json:"healthy"`
	Error   string `json:"error,omitempty"`
	// Address is the host:port that was checked.
	Address string `json:"address,omitempty"`
	// Latency is the time taken for the check (0 if unhealthy or unavailable).
	Latency time.Duration `json:"-"`
}

// HealthChecker checks health of runtimes by connecting to their HTTP endpoints.
type HealthChecker struct {
	mu     sync.RWMutex
	runs   []RuntimeDef
	result []*CheckResult
}

// RuntimeDef holds the definition of a runtime for health checking.
type RuntimeDef struct {
	ID   string
	Name string
	Host string
	Port int
}

// NewHealthChecker creates a new HealthChecker.
func NewHealthChecker() *HealthChecker {
	return &HealthChecker{}
}

// UpdateRuntimes updates the list of runtimes to check.
func (h *HealthChecker) UpdateRuntimes(defs []RuntimeDef) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.runs = defs
}

// GetResults returns the latest health check results.
func (h *HealthChecker) GetResults() []*CheckResult {
	h.mu.RLock()
	defer h.mu.RUnlock()
	result := make([]*CheckResult, len(h.result))
	copy(result, h.result)
	return result
}

// CheckAll performs health checks on all registered runtimes.
func (h *HealthChecker) CheckAll() []*CheckResult {
	h.mu.RLock()
	defs := h.runs
	h.mu.RUnlock()

	if len(defs) == 0 {
		return nil
	}

	results := make([]*CheckResult, len(defs))
	var wg sync.WaitGroup

	for i, def := range defs {
		wg.Add(1)
		results[i] = &CheckResult{ID: def.ID, Name: def.Name}
		go func(idx int, d RuntimeDef) {
			defer wg.Done()
			start := time.Now()
			address := net.JoinHostPort(d.Host, fmt.Sprintf("%d", d.Port))
			results[idx].Address = address

			conn, err := net.DialTimeout("tcp", address, 3*time.Second)
			if err != nil {
				results[idx].Healthy = false
				results[idx].Error = err.Error()
				return
			}
			defer conn.Close()

			results[idx].Latency = positiveLatency(time.Since(start))
			results[idx].Healthy = true
		}(i, def)
	}

	wg.Wait()

	h.mu.Lock()
	h.result = results
	h.mu.Unlock()

	return results
}

// CheckRuntime performs a health check on a single runtime by ID.
func (h *HealthChecker) CheckRuntime(id string) (*CheckResult, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for _, def := range h.runs {
		if def.ID == id {
			address := net.JoinHostPort(def.Host, fmt.Sprintf("%d", def.Port))
			start := time.Now()

			conn, err := net.DialTimeout("tcp", address, 3*time.Second)
			if err != nil {
				return &CheckResult{
					ID:      id,
					Name:    def.Name,
					Healthy: false,
					Address: address,
					Error:   err.Error(),
				}, nil
			}
			conn.Close()

			return &CheckResult{
				ID:      id,
				Name:    def.Name,
				Healthy: true,
				Address: address,
				Latency: positiveLatency(time.Since(start)),
			}, nil
		}
	}

	return nil, errors.New("runtime not found")
}

func positiveLatency(elapsed time.Duration) time.Duration {
	if elapsed <= 0 {
		return time.Nanosecond
	}
	return elapsed
}
