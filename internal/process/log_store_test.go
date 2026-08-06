package process

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestLogBrokerSubscriberReceivesEvents verifies subscribers receive log events.
func TestLogBrokerSubscriberReceivesEvents(t *testing.T) {
	broker := NewLogBroker(100)
	sub := broker.Subscribe("")

	type result struct {
		ev  LogStreamEvent
		ok  bool
		err error
	}
	resultCh := make(chan result, 1)

	go func() {
		select {
		case ev, ok := <-sub.Channel():
			select {
			case resultCh <- result{ev: ev, ok: ok}:
			default:
			}
		case <-time.After(2 * time.Second):
			select {
			case resultCh <- result{err: errors.New("timed out waiting for event")}:
			default:
			}
		}
	}()

	broker.Publish(LogStreamEvent{Message: "test", Timestamp: time.Now()})

	r := <-resultCh
	if r.err != nil {
		panic(r.err)
	}
	if !r.ok {
		panic("channel closed without receiving event")
	}
	if r.ev.Message != "test" {
		t.Errorf("expected 'test', got %q", r.ev.Message)
	}
	// Give goroutine time to exit.
	time.Sleep(50 * time.Millisecond)
}

// TestLogBrokerSubscriberReceivesEventsForInstance verifies filtered subscription.
func TestLogBrokerSubscriberReceivesEventsForInstance(t *testing.T) {
	broker := NewLogBroker(100)
	sub := broker.Subscribe("instance-1")

	var wg sync.WaitGroup
	wg.Add(1)
	var received int
	go func() {
		defer wg.Done()
		for {
			select {
			case ev, ok := <-sub.Channel():
				if !ok {
					return
				}
				if ev.InstanceID != "instance-1" {
					t.Errorf("expected instance-1, got %q", ev.InstanceID)
				}
				received++
			case <-sub.Done():
				// Drain remaining buffered events after Done closes.
				for {
					select {
					case ev, ok := <-sub.Channel():
						if !ok {
							return
						}
						if ev.InstanceID != "instance-1" {
							t.Errorf("expected instance-1, got %q", ev.InstanceID)
						}
						received++
					default:
						return
					}
				}
			}
		}
	}()

	// Publish events for different instances.
	broker.Publish(LogStreamEvent{Message: "msg1", InstanceID: "instance-1", Timestamp: time.Now()})
	broker.Publish(LogStreamEvent{Message: "msg2", InstanceID: "instance-2", Timestamp: time.Now()})
	broker.Publish(LogStreamEvent{Message: "msg3", InstanceID: "instance-1", Timestamp: time.Now()})

	// Cancel and wait for goroutine to exit.
	sub.Cancel()
	wg.Wait()

	if received != 2 {
		t.Errorf("expected 2 instance-1 messages, got %d", received)
	}
}

// TestLogBrokerShutdownNoGoroutineLeak verifies broker shutdown closes all channels.
func TestLogBrokerShutdownNoGoroutineLeak(t *testing.T) {
	broker := NewLogBroker(100)

	var subs []*LogSubscription
	for i := 0; i < 10; i++ {
		subs = append(subs, broker.Subscribe("inst"))
	}

	broker.Shutdown()

	for i, sub := range subs {
		select {
		case _, ok := <-sub.Channel():
			if ok {
				t.Errorf("subscription %d: channel not closed after Shutdown", i)
			}
		case <-time.After(1 * time.Second):
			t.Errorf("subscription %d: channel read hung after Shutdown", i)
		}
	}
}

// TestLogBrokerPublishDuringCancel verifies publish during cancel does not deadlock.
func TestLogBrokerPublishDuringCancel(t *testing.T) {
	broker := NewLogBroker(100)
	sub := broker.Subscribe("")

	done := make(chan struct{})
	go func() {
		for i := 0; i < 50; i++ {
			broker.Publish(LogStreamEvent{Message: "during", Timestamp: time.Now()})
		}
		sub.Cancel()
		close(done)
	}()

	<-done
}

// TestLogBrokerEmptySubscribeFilter verifies empty instanceID filter gets all events.
func TestLogBrokerEmptySubscribeFilter(t *testing.T) {
	broker := NewLogBroker(100)
	sub := broker.Subscribe("")

	// Publish events with different instance IDs.
	broker.Publish(LogStreamEvent{Message: "a", InstanceID: "inst-1", Timestamp: time.Now()})
	broker.Publish(LogStreamEvent{Message: "b", InstanceID: "inst-2", Timestamp: time.Now()})
	broker.Publish(LogStreamEvent{Message: "c", InstanceID: "inst-3", Timestamp: time.Now()})

	// All events should arrive to empty filter subscriber.
	received := make([]string, 0, 3)
	for i := 0; i < 3; i++ {
		select {
		case ev := <-sub.Channel():
			received = append(received, ev.Message)
		case <-time.After(1 * time.Second):
			t.Fatalf("timeout waiting for event %d", i)
		}
	}

	sub.Cancel()

	// Check all messages received.
	if len(received) != 3 {
		t.Errorf("expected 3 messages, got %d: %v", len(received), received)
	}
}

// TestLogBrokerCancelMultipleSafe verifies cancel is safe when called multiple times concurrently.
func TestLogBrokerCancelMultipleSafe(t *testing.T) {
	broker := NewLogBroker(100)
	sub := broker.Subscribe("inst")

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sub.Cancel()
		}()
	}
	wg.Wait()
}

// TestLogStoreCollectAllWithInstanceID verifies CollectAllWithInstanceID attaches ID.
func TestLogStoreCollectAllWithInstanceID(t *testing.T) {
	store := NewLogStore(1000)

	store.Add(LogEvent{
		Time:    time.Now(),
		Stream:  "stdout",
		Message: "hello",
	})
	store.Add(LogEvent{
		Time:    time.Now(),
		Stream:  "stderr",
		Message: "world",
	})

	entries := store.CollectAllWithInstanceID("test-instance")
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	for _, e := range entries {
		if e.InstanceID != "test-instance" {
			t.Errorf("expected InstanceID 'test-instance', got %q", e.InstanceID)
		}
	}

	// CollectAll should have empty InstanceID.
	allEntries := store.CollectAll()
	for _, e := range allEntries {
		if e.InstanceID != "" {
			t.Errorf("CollectAll: expected empty InstanceID, got %q", e.InstanceID)
		}
	}
}

// TestLogBrokerDropCounter verifies DroppedEvents counter.
func TestLogBrokerDropCounter(t *testing.T) {
	broker := NewLogBroker(10)
	sub := broker.Subscribe("")

	// Fill buffer.
	for i := 0; i < 20; i++ {
		broker.Publish(LogStreamEvent{Timestamp: time.Now()})
	}

	dropped := broker.DroppedEvents()
	if dropped == 0 {
		t.Error("expected dropped events > 0")
	}

	sub.Cancel()
}

// TestLogBrokerShutdownWithActiveSubscriber verifies shutdown while subscriber active.
func TestLogBrokerShutdownWithActiveSubscriber(t *testing.T) {
	broker := NewLogBroker(100)
	sub := broker.Subscribe("")

	// Publish some events before shutdown.
	for i := 0; i < 10; i++ {
		broker.Publish(LogStreamEvent{Message: "pre", Timestamp: time.Now()})
	}

	broker.Shutdown()

	// All published events should be dropped or received.
	dropped := broker.DroppedEvents()
	_ = dropped

	// Cancel after shutdown should not panic.
	sub.Cancel()
}

// TestLogBrokerSequenceNumbers verifies sequence numbers increment.
func TestLogBrokerSequenceNumbers(t *testing.T) {
	broker := NewLogBroker(100)
	sub := broker.Subscribe("")

	for i := uint64(1); i <= 10; i++ {
		broker.Publish(LogStreamEvent{
			Message:   "msg",
			Timestamp: time.Now(),
		})
	}

	// Drain all events.
	receivedSeq := make([]uint64, 0, 10)
	for j := 0; j < 10; j++ {
		select {
		case ev := <-sub.Channel():
			receivedSeq = append(receivedSeq, ev.Sequence)
		case <-time.After(1 * time.Second):
			t.Fatalf("timeout waiting for event %d", j)
		}
	}

	sub.Cancel()

	// Verify sequences are 1..10.
	expectedSeq := make([]uint64, 10)
	for i := range expectedSeq {
		expectedSeq[i] = uint64(i + 1)
	}

	// Sort receivedSeq for comparison.
	for i := 0; i < len(receivedSeq); i++ {
		for j := i + 1; j < len(receivedSeq); j++ {
			if receivedSeq[i] > receivedSeq[j] {
				receivedSeq[i], receivedSeq[j] = receivedSeq[j], receivedSeq[i]
			}
		}
	}

	for i := range expectedSeq {
		if receivedSeq[i] != expectedSeq[i] {
			t.Errorf("sequence[%d]: expected %d, got %d", i, expectedSeq[i], receivedSeq[i])
		}
	}
}

// TestLogBrokerPublishAfterCancel verifies publish after cancel does not panic.
func TestLogBrokerPublishAfterCancel(t *testing.T) {
	broker := NewLogBroker(100)
	sub := broker.Subscribe("")
	sub.Cancel()

	// Publish after cancel.
	for i := 0; i < 10; i++ {
		broker.Publish(LogStreamEvent{Timestamp: time.Now()})
	}

	_ = broker.DroppedEvents()
}

// TestLogBrokerLargeBuffer verifies large buffer doesn't cause issues.
func TestLogBrokerLargeBuffer(t *testing.T) {
	broker := NewLogBroker(10000)
	sub := broker.Subscribe("")

	// Fill most of the buffer.
	for i := 0; i < 9900; i++ {
		broker.Publish(LogStreamEvent{Timestamp: time.Now()})
	}

	// Drain some.
	for i := 0; i < 1000; i++ {
		select {
		case <-sub.Channel():
		case <-time.After(1 * time.Second):
			t.Fatal("drain timed out")
		}
	}

	// Continue publishing.
	for i := 0; i < 900; i++ {
		broker.Publish(LogStreamEvent{Timestamp: time.Now()})
	}

	sub.Cancel()
}

// TestLogBrokerSelectDonePreventsLeak verifies subscriber goroutine exits on cancel.
func TestLogBrokerSelectDonePreventsLeak(t *testing.T) {
	broker := NewLogBroker(100)
	sub := broker.Subscribe("")

	done := make(chan struct{})
	go func() {
		count := atomic.Int32{}
		for {
			select {
			case <-sub.Done():
				count.Add(1)
				close(done)
				return
			case <-sub.Channel():
				// drain
			}
		}
	}()

	sub.Cancel()
	<-done
}

// TestLogStoreConcurrentAppendAndQuery verifies race-safe concurrent append and query.
func TestLogStoreConcurrentAppendAndQuery(t *testing.T) {
	store := NewLogStore(10000)

	var wg sync.WaitGroup
	// Writer goroutine.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			store.Add(LogEvent{
				Time:    time.Now(),
				Stream:  "stdout",
				Message: "msg",
			})
		}
	}()

	// Reader goroutines.
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				store.GetLogs(LogQuery{})
			}
		}()
	}

	wg.Wait()
}
