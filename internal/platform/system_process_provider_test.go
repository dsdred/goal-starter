package platform

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// A-4 provider semantics: one in-flight measurement, no result cache, the
// caller deadline respected, and a cancelled waiter never wedges the leader.

func TestProcessMemProviderNoCachePerRequest(t *testing.T) {
	var calls int32
	measure := func(_ context.Context, _ []TrackedProcess) ProcessMemReport {
		atomic.AddInt32(&calls, 1)
		return processMemOK(nil)
	}
	p := NewProcessMemProviderWithMeasure(time.Second, measure)
	p.Sample(context.Background(), nil)
	p.Sample(context.Background(), nil)
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("measure calls = %d, want 2 (sequential requests each sample fresh; no cache)", got)
	}
}

func TestProcessMemProviderSingleFlight(t *testing.T) {
	var calls int32
	release := make(chan struct{})
	started := make(chan struct{})
	var once sync.Once
	measure := func(_ context.Context, _ []TrackedProcess) ProcessMemReport {
		atomic.AddInt32(&calls, 1)
		once.Do(func() { close(started) })
		<-release
		return processMemOK(nil)
	}
	p := NewProcessMemProviderWithMeasure(10*time.Second, measure)

	const waiters = 5
	var wg sync.WaitGroup
	sampledAt := make([]string, waiters)
	for i := 0; i < waiters; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rep := p.Sample(context.Background(), nil)
			sampledAt[i] = rep.SampledAt
		}(i)
	}
	<-started
	time.Sleep(30 * time.Millisecond) // give any racing leader time to start a second measurement
	close(release)
	wg.Wait()

	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("measure calls = %d, want 1 while a sample is in flight (single-flight)", got)
	}
	for i, s := range sampledAt {
		if s != sampledAt[0] || s == "" {
			t.Fatalf("waiter %d SampledAt = %q, want the identical leader stamp %q", i, s, sampledAt[0])
		}
	}
}

func TestProcessMemProviderCancelledWaiter(t *testing.T) {
	var calls int32
	release := make(chan struct{})
	started := make(chan struct{})
	var once sync.Once
	measure := func(_ context.Context, _ []TrackedProcess) ProcessMemReport {
		atomic.AddInt32(&calls, 1)
		once.Do(func() { close(started) })
		<-release
		return processMemOK([]ProcessMemEntry{processMemMeasured(TrackedProcess{InstanceID: "a"}, 1024, "fake")})
	}
	p := NewProcessMemProviderWithMeasure(10*time.Second, measure)

	leaderDone := make(chan ProcessMemReport, 1)
	go func() { leaderDone <- p.Sample(context.Background(), nil) }()
	<-started

	cctx, cancel := context.WithCancel(context.Background())
	cancel()
	bw := p.Sample(cctx, nil)
	if bw.State != ProcessMemStateFailed || bw.Error != "request_cancelled" {
		t.Fatalf("cancelled waiter = %+v, want measurement_failed request_cancelled", bw)
	}

	close(release)
	leader := <-leaderDone
	if leader.State != ProcessMemStateOK || len(leader.Entries) != 1 {
		t.Fatalf("leader report = %+v, cancelled waiter must not abort it", leader)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("measure calls = %d, want the leader measurement undisturbed", got)
	}

	// The slot is released: a later request measures again.
	deadline := time.Now().Add(2 * time.Second)
	var after ProcessMemReport
	for time.Now().Before(deadline) {
		if after = p.Sample(context.Background(), nil); after.State == ProcessMemStateOK {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if after.State != ProcessMemStateOK {
		t.Fatalf("post-flight sample never completed: %+v", after)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("measure calls = %d, want 2 after the flight completed (slot released)", got)
	}
}

func TestProcessMemProviderDeadlineRespected(t *testing.T) {
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	measure := func(ctx context.Context, _ []TrackedProcess) ProcessMemReport {
		<-ctx.Done()
		return processMemFailed("measurement_timeout")
	}
	p := NewProcessMemProviderWithMeasure(40*time.Millisecond, measure)

	done := make(chan ProcessMemReport, 1)
	go func() { done <- p.Sample(context.Background(), nil) }()
	select {
	case rep := <-done:
		if rep.State != ProcessMemStateFailed || rep.Error != "measurement_timeout" {
			t.Fatalf("report = %+v, want measurement_failed measurement_timeout", rep)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("provider did not honor its measurement deadline")
	}
}

// A caller that arrives with no in-flight sample BECOMES the leader: its own
// cancellation must not divert or abort the shared sample (A-4) — the leader
// rides its own fixed deadline, not any single caller's context.
func TestProcessMemProviderLeaderIgnoresCallerCancellation(t *testing.T) {
	measure := func(ctx context.Context, _ []TrackedProcess) ProcessMemReport {
		<-ctx.Done()
		return processMemFailed("measurement_timeout")
	}
	p := NewProcessMemProviderWithMeasure(80*time.Millisecond, measure)

	cctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	rep := p.Sample(cctx, nil)
	if rep.State != ProcessMemStateFailed || rep.Error != "measurement_timeout" {
		t.Fatalf("leader report = %+v, want the shared sample completed on its own deadline, not request_cancelled", rep)
	}
	if elapsed := time.Since(start); elapsed < 60*time.Millisecond {
		t.Fatalf("leader returned after %v, before the measurement window elapsed", elapsed)
	}
}
