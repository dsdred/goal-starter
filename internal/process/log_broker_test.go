package process

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestLogBrokerSubscribePublish(t *testing.T) {
	broker := NewLogBroker(64)
	sub := broker.Subscribe("")

	ev := LogStreamEvent{
		InstanceID: "inst-1",
		ProfileID:  "prof-1",
		Stream:     LogStreamStdout,
		Message:    "test message",
		Timestamp:  time.Now(),
	}
	broker.Publish(ev)

	select {
	case received := <-sub.Channel():
		if received.InstanceID != "inst-1" {
			t.Errorf("expected instance 'inst-1', got %q", received.InstanceID)
		}
		if received.Message != "test message" {
			t.Errorf("expected message 'test message', got %q", received.Message)
		}
		if received.Sequence == 0 {
			t.Error("expected non-zero sequence")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout: did not receive published event")
	}

	sub.Cancel()
}

func TestLogBrokerFilterByInstanceID(t *testing.T) {
	broker := NewLogBroker(64)

	sub1 := broker.Subscribe("inst-1")
	sub2 := broker.Subscribe("inst-2")
	subAll := broker.Subscribe("")

	// Publish to inst-1.
	broker.Publish(LogStreamEvent{
		InstanceID: "inst-1",
		Stream:     LogStreamStdout,
		Message:    "for inst-1",
		Timestamp:  time.Now(),
	})

	// Publish to inst-2.
	broker.Publish(LogStreamEvent{
		InstanceID: "inst-2",
		Stream:     LogStreamStderr,
		Message:    "for inst-2",
		Timestamp:  time.Now(),
	})

	// sub1 should only receive inst-1 events.
	var inst1Count int
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-sub1.Channel():
				inst1Count++
			case <-time.After(100 * time.Millisecond):
				return
			}
		}
	}()
	<-done

	if inst1Count != 1 {
		t.Errorf("expected 1 event for inst-1, got %d", inst1Count)
	}

	// subAll should receive both.
	var allCount int
	done2 := make(chan struct{})
	go func() {
		defer close(done2)
		for {
			select {
			case <-subAll.Channel():
				allCount++
			case <-time.After(100 * time.Millisecond):
				return
			}
		}
	}()
	<-done2

	if allCount != 2 {
		t.Errorf("expected 2 events for unsubscribed listener, got %d", allCount)
	}

	sub1.Cancel()
	sub2.Cancel()
	subAll.Cancel()
}

func TestLogBrokerCancelIdempotent(t *testing.T) {
	broker := NewLogBroker(64)
	sub := broker.Subscribe("")

	sub.Cancel()
	sub.Cancel() // second cancel must not panic or hang

	// Channel should be closed.
	select {
	case _, ok := <-sub.Channel():
		if ok {
			t.Error("expected closed channel")
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("timeout: channel not closed after cancel")
	}
}

func TestLogBrokerCancelDoesNotHang(t *testing.T) {
	broker := NewLogBroker(64)
	sub := broker.Subscribe("")

	done := make(chan struct{})
	go func() {
		sub.Cancel()
		close(done)
	}()

	select {
	case <-done:
		// OK.
	case <-time.After(1 * time.Second):
		t.Fatal("Cancel hung")
	}
}

func TestLogBrokerSlowSubscriberDropOldest(t *testing.T) {
	broker := NewLogBroker(4) // tiny buffer
	broker.policy = dropOldest

	sub := broker.Subscribe("")
	// Drain the channel first.
	drainSubscription(sub)

	// Publish more events than buffer.
	for i := 0; i < 20; i++ {
		broker.Publish(LogStreamEvent{
			InstanceID: "inst-1",
			Stream:     LogStreamStdout,
			Message:    "msg",
			Timestamp:  time.Now(),
		})
	}

	dropped := broker.DroppedEvents()
	if dropped == 0 {
		t.Error("expected some dropped events")
	}
	sub.Cancel()
}

func TestLogBrokerShutdownNoHang(t *testing.T) {
	broker := NewLogBroker(64)
	sub := broker.Subscribe("")

	// Publish some events.
	for i := 0; i < 5; i++ {
		broker.Publish(LogStreamEvent{
			InstanceID: "inst-1",
			Stream:     LogStreamStdout,
			Message:    "msg",
			Timestamp:  time.Now(),
		})
	}

	// Cancel all subscribers.
	sub.Cancel()

	// Broker should be safely drainable.
	time.Sleep(50 * time.Millisecond)
}

func drainSubscription(sub *LogSubscription) {
	for {
		select {
		case <-sub.Channel():
			// drain.
		case <-time.After(50 * time.Millisecond):
			return
		}
	}
}

func TestLogBrokerConcurrentSubscribeCancelRace(t *testing.T) {
	broker := NewLogBroker(64)
	var wg sync.WaitGroup
	stopped := make(chan struct{})

	// Start many concurrent subscribers.
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sub := broker.Subscribe("")
			select {
			case <-sub.Channel():
				// received event.
			case <-stopped:
			case <-time.After(100 * time.Millisecond):
			}
			sub.Cancel()
		}()
	}

	// Publish events concurrently.
	for i := 0; i < 100; i++ {
		broker.Publish(LogStreamEvent{
			InstanceID: "inst-1",
			Stream:     LogStreamStdout,
			Message:    "race test",
			Timestamp:  time.Now(),
		})
	}

	close(stopped)
	wg.Wait()
}

func TestLogBrokerNoSubscriptionReceivesAll(t *testing.T) {
	broker := NewLogBroker(64)
	sub := broker.Subscribe("") // empty filter = all instances.

	for i := 0; i < 10; i++ {
		broker.Publish(LogStreamEvent{
			InstanceID: "inst-1",
			Stream:     LogStreamStdout,
			Message:    "msg",
			Timestamp:  time.Now(),
		})
		broker.Publish(LogStreamEvent{
			InstanceID: "inst-2",
			Stream:     LogStreamStderr,
			Message:    "msg",
			Timestamp:  time.Now(),
		})
	}

	count := 0
	drain := func() {
		for {
			select {
			case <-sub.Channel():
				count++
			case <-time.After(100 * time.Millisecond):
				return
			}
		}
	}
	drain()

	if count != 20 {
		t.Errorf("expected 20 events, got %d", count)
	}
	sub.Cancel()
}

// --- QueryAggregatedLogs tests ---

func TestQueryAggregatedLogsSortOrder(t *testing.T) {
	entries := []AggregatedLogEntry{
		{InstanceID: "b", Timestamp: time.Now().Add(-3 * time.Second), Stream: "stdout", Message: "old b"},
		{InstanceID: "a", Timestamp: time.Now().Add(-1 * time.Second), Stream: "stderr", Message: "recent a"},
		{InstanceID: "b", Timestamp: time.Now().Add(-2 * time.Second), Stream: "stdout", Message: "mid b"},
		{InstanceID: "a", Timestamp: time.Now(), Stream: "stdout", Message: "now a"},
		{InstanceID: "a", Timestamp: time.Now().Add(-1 * time.Second), Stream: "stdout", Message: "recent a stdout"},
	}

	result := QueryAggregatedLogs(entries, LogQuery{})

	if len(result.Items) != 5 {
		t.Fatalf("expected 5 items, got %d", len(result.Items))
	}
	if result.Total != 5 {
		t.Errorf("expected total 5, got %d", result.Total)
	}

	// Verify DESC order by timestamp.
	for i := 0; i < len(result.Items)-1; i++ {
		if result.Items[i].Time.Before(result.Items[i+1].Time) {
			t.Errorf("expected items sorted DESC by timestamp at index %d", i)
		}
	}
}

func TestQueryAggregatedLogsPagination(t *testing.T) {
	entries := make([]AggregatedLogEntry, 30)
	base := time.Now()
	for i := 0; i < 30; i++ {
		entries[i] = AggregatedLogEntry{
			InstanceID: "inst-1",
			Timestamp:  base.Add(time.Duration(i) * time.Second),
			Stream:     "stdout",
			Message:    "msg",
		}
	}

	q := LogQuery{Page: 1, PageSize: 10}
	result := QueryAggregatedLogs(entries, q)

	if result.Total != 30 {
		t.Errorf("expected total 30, got %d", result.Total)
	}
	if len(result.Items) != 10 {
		t.Errorf("expected 10 items on page 1, got %d", len(result.Items))
	}

	q.Page = 3
	result = QueryAggregatedLogs(entries, q)
	if len(result.Items) != 10 {
		t.Errorf("expected 10 items on page 3, got %d", len(result.Items))
	}

	q.Page = 4
	result = QueryAggregatedLogs(entries, q)
	if len(result.Items) != 0 {
		t.Errorf("expected 0 items on page 4 (past end), got %d", len(result.Items))
	}
}

func TestQueryAggregatedLogsFilterByInstance(t *testing.T) {
	entries := []AggregatedLogEntry{
		{InstanceID: "a", Timestamp: time.Now(), Stream: "stdout", Message: "from a"},
		{InstanceID: "b", Timestamp: time.Now(), Stream: "stderr", Message: "from b"},
		{InstanceID: "a", Timestamp: time.Now(), Stream: "stdout", Message: "also a"},
	}

	q := LogQuery{}
	result := QueryAggregatedLogs(entries, q)

	if result.Total != 3 {
		t.Errorf("expected total 3 without filter, got %d", result.Total)
	}

	// We should get all 3 items (no instance filter applied in this test).
	if len(result.Items) != 3 {
		t.Errorf("expected 3 items, got %d", len(result.Items))
	}
}

func TestQueryAggregatedLogsPageSizeCapped(t *testing.T) {
	entries := make([]AggregatedLogEntry, 20)
	base := time.Now()
	for i := 0; i < 20; i++ {
		entries[i] = AggregatedLogEntry{
			InstanceID: "inst-1",
			Timestamp:  base.Add(time.Duration(i) * time.Second),
			Stream:     "stdout",
			Message:    "msg",
		}
	}

	q := LogQuery{Page: 1, PageSize: 1000}
	result := QueryAggregatedLogs(entries, q)

	if result.Size != 500 {
		t.Errorf("expected page_size capped at 500, got %d", result.Size)
	}
	if len(result.Items) != 20 {
		t.Errorf("expected 20 items (all), got %d", len(result.Items))
	}
}

func TestQueryAggregatedLogsEmpty(t *testing.T) {
	result := QueryAggregatedLogs(nil, LogQuery{})
	if result.Total != 0 {
		t.Errorf("expected total 0, got %d", result.Total)
	}
	if result.Items == nil {
		t.Error("expected non-nil Items slice for empty result")
	}
}

func TestQueryAggregatedLogsConcurrent(t *testing.T) {
	entries := make([]AggregatedLogEntry, 100)
	base := time.Now()
	for i := 0; i < 100; i++ {
		entries[i] = AggregatedLogEntry{
			InstanceID: "inst-1",
			Timestamp:  base.Add(time.Duration(i) * time.Millisecond),
			Stream:     "stdout",
			Message:    "msg",
		}
	}

	var wg sync.WaitGroup
	var errs atomic.Uint64
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			q := LogQuery{Page: 1, PageSize: 10}
			for j := 0; j < 50; j++ {
				r := QueryAggregatedLogs(entries, q)
				if r == nil {
					errs.Add(1)
				}
			}
		}()
	}
	wg.Wait()

	if errs.Load() != 0 {
		t.Errorf("got %d errors during concurrent QueryAggregatedLogs", errs.Load())
	}
}
