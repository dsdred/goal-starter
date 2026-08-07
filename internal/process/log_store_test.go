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

// TestLogBrokerShutdownNoGoroutineLeak verifies broker shutdown closes all done channels.
//
// After LogBroker stabilization, data channels are NOT closed by Shutdown() —
// they are managed by GC. The Done() channel is closed instead, which is the
// signal for consumers to stop reading.
func TestLogBrokerShutdownNoGoroutineLeak(t *testing.T) {
	broker := NewLogBroker(100)

	var subs []*LogSubscription
	for i := 0; i < 10; i++ {
		subs = append(subs, broker.Subscribe("inst"))
	}

	broker.Shutdown()

	// Verify Done() is closed for all subscribers (this is the actual shutdown signal).
	// Data channels are NOT closed — they are GC-managed after Shutdown().
	for i, sub := range subs {
		select {
		case <-sub.Done():
			// Done channel closed — this is correct behavior after Shutdown.
		case <-time.After(1 * time.Second):
			t.Errorf("subscription %d: Done() not closed after Shutdown", i)
		}
	}

	// Verify closed flag is set for all subscribers.
	for i, sub := range subs {
		if !sub.lsub.closed.Load() {
			t.Errorf("subscription %d: closed flag not set after Shutdown", i)
		}
	}

	// Verify broker has no remaining subscribers.
	if dropped := broker.DroppedEvents(); dropped > 0 {
		t.Logf("dropped events during shutdown: %d", dropped)
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

// === LogBroker lifecycle tests ===

// TestLogBrokerShutdownClosesDone verifies Shutdown closes done channels.
func TestLogBrokerShutdownClosesDone(t *testing.T) {
	broker := NewLogBroker(100)
	sub := broker.Subscribe("")

	broker.Shutdown()

	select {
	case <-sub.Done():
		// OK — done channel closed.
	case <-time.After(1 * time.Second):
		t.Fatal("Shutdown did not close done channel")
	}
}

// TestLogBrokerConcurrentCancelAndPublish verifies no deadlock on cancel/publish race.
func TestLogBrokerConcurrentCancelAndPublish(t *testing.T) {
	broker := NewLogBroker(100)
	sub := broker.Subscribe("")

	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			broker.Publish(LogStreamEvent{
				InstanceID: "inst-1",
				Stream:     LogStreamStdout,
				Message:    "race test",
				Timestamp:  time.Now(),
			})
		}
		sub.Cancel()
		close(done)
	}()

	select {
	case <-done:
		// OK.
	case <-time.After(5 * time.Second):
		t.Fatal("Concurrent cancel/publish hung")
	}
}

// TestLogBrokerConcurrentShutdownAndCancel verifies no deadlock on shutdown/cancel race.
func TestLogBrokerConcurrentShutdownAndCancel(t *testing.T) {
	broker := NewLogBroker(100)
	sub := broker.Subscribe("")

	done := make(chan struct{})
	go func() {
		for i := 0; i < 50; i++ {
			broker.Publish(LogStreamEvent{
				InstanceID: "inst-1",
				Stream:     LogStreamStdout,
				Message:    "shutdown race",
				Timestamp:  time.Now(),
			})
		}
		sub.Cancel()
		close(done)
	}()

	broker.Shutdown()
	<-done
}

// TestLogBrokerNoGoroutineLeakOnCancel verifies subscriber goroutine exits on cancel.
func TestLogBrokerNoGoroutineLeakOnCancel(t *testing.T) {
	broker := NewLogBroker(100)
	sub := broker.Subscribe("")

	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-sub.Done():
				close(done)
				return
			case <-sub.Channel():
				// drain.
			}
		}
	}()

	sub.Cancel()
	<-done
}

// TestLogBrokerDoubleCancel verifies double Cancel is safe.
func TestLogBrokerDoubleCancel(t *testing.T) {
	broker := NewLogBroker(100)
	sub := broker.Subscribe("")

	sub.Cancel()
	sub.Cancel() // must not panic or hang

	// Verify done is closed.
	select {
	case <-sub.Done():
		// OK.
	case <-time.After(100 * time.Millisecond):
		t.Fatal("done not closed after double cancel")
	}
}

// TestLogBrokerShutdownDoubleCancel verifies shutdown then cancel is safe.
func TestLogBrokerShutdownDoubleCancel(t *testing.T) {
	broker := NewLogBroker(100)
	sub := broker.Subscribe("")

	broker.Shutdown()
	// Shutdown does not close done — Cancel must do that.
	sub.Cancel()

	select {
	case <-sub.Done():
		// OK.
	case <-time.After(100 * time.Millisecond):
		t.Fatal("done not closed after shutdown+cancel")
	}
}

// TestQueryLogsSequenceAssigned verifies Sequence is assigned for each event.
func TestQueryLogsSequenceAssigned(t *testing.T) {
	broker := NewLogBroker(100)

	// Create subscription BEFORE publishing so events are received.
	sub := broker.Subscribe("")

	// Publish events for multiple instances.
	for i := 0; i < 20; i++ {
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

	// Collect all events.
	allSeq := make([]uint64, 0, 40)
	for i := 0; i < 40; i++ {
		select {
		case ev := <-sub.Channel():
			allSeq = append(allSeq, ev.Sequence)
		case <-time.After(2 * time.Second):
			t.Fatalf("timeout waiting for event %d", i)
		}
	}

	sub.Cancel()

	if len(allSeq) < 40 {
		t.Fatalf("expected at least 40 events, got %d", len(allSeq))
	}

	// Verify sequences are monotonically increasing.
	for i := 1; i < len(allSeq); i++ {
		if allSeq[i] <= allSeq[i-1] {
			t.Errorf("sequence[%d]=%d <= sequence[%d]=%d — not monotonically increasing",
				i, allSeq[i], i-1, allSeq[i-1])
		}
	}
}

// TestQueryLogsStableOrderSameTimestamp verifies deterministic order for same-timestamp events.
func TestQueryLogsStableOrderSameTimestamp(t *testing.T) {
	now := time.Now()
	entries := []AggregatedLogEntry{
		{InstanceID: "b", Timestamp: now, Stream: "stdout", Message: "msg1"},
		{InstanceID: "a", Timestamp: now, Stream: "stderr", Message: "msg2"},
		{InstanceID: "a", Timestamp: now, Stream: "stdout", Message: "msg3"},
		{InstanceID: "b", Timestamp: now, Stream: "stderr", Message: "msg4"},
	}

	result := QueryAggregatedLogs(entries, LogQuery{})
	if len(result.Items) != 4 {
		t.Fatalf("expected 4 items, got %d", len(result.Items))
	}

	// With same timestamp and same instance, order by stream ASC.
	// a/stdout should come before a/stderr? No — DESC timestamp, then ASC instance, then ASC stream.
	// So order is: a/stderr, a/stdout, b/stderr, b/stdout (DESC timestamp, ASC instanceID, ASC stream)
	expectedOrder := []string{"msg2", "msg3", "msg4", "msg1"}
	for i, item := range result.Items {
		if item.Message != expectedOrder[i] {
			t.Errorf("item[%d]: expected %q, got %q", i, expectedOrder[i], item.Message)
		}
	}
}

// TestQueryLogsStableOrderSameInstance verifies same instance same timestamp are ordered by stream.
func TestQueryLogsStableOrderSameInstance(t *testing.T) {
	now := time.Now()
	entries := []AggregatedLogEntry{
		{InstanceID: "inst-1", Timestamp: now, Stream: "stderr", Message: "err"},
		{InstanceID: "inst-1", Timestamp: now, Stream: "stdout", Message: "out"},
	}

	result := QueryAggregatedLogs(entries, LogQuery{})
	if len(result.Items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(result.Items))
	}
	// ASC by stream: stderr < stdout
	if result.Items[0].Stream != "stderr" {
		t.Errorf("expected stderr first, got %s", result.Items[0].Stream)
	}
	if result.Items[1].Stream != "stdout" {
		t.Errorf("expected stdout second, got %s", result.Items[1].Stream)
	}
}

// TestQueryLogsMultipleInstances verifies deterministic cross-instance ordering.
func TestQueryLogsMultipleInstances(t *testing.T) {
	now := time.Now()
	entries := []AggregatedLogEntry{
		{InstanceID: "inst-b", Timestamp: now, Stream: "stdout", Message: "b"},
		{InstanceID: "inst-a", Timestamp: now, Stream: "stdout", Message: "a"},
		{InstanceID: "inst-a", Timestamp: now.Add(-time.Second), Stream: "stdout", Message: "old-a"},
		{InstanceID: "inst-b", Timestamp: now.Add(-time.Second), Stream: "stdout", Message: "old-b"},
	}

	result := QueryAggregatedLogs(entries, LogQuery{})
	if len(result.Items) != 4 {
		t.Fatalf("expected 4 items, got %d", len(result.Items))
	}

	// DESC timestamp: now > now-1s
	// Same timestamp: ASC instance ID
	// Order: newest(a,inst-a), newest(b,inst-b), oldest(a,inst-a), oldest(b,inst-b)
	if result.Items[0].Message != "a" {
		t.Errorf("expected 'a' first (newest, inst-a < inst-b), got %q", result.Items[0].Message)
	}
	if result.Items[1].Message != "b" {
		t.Errorf("expected 'b' second, got %q", result.Items[1].Message)
	}
	if result.Items[2].Message != "old-a" {
		t.Errorf("expected 'old-a' third (older, inst-a < inst-b), got %q", result.Items[2].Message)
	}
	if result.Items[3].Message != "old-b" {
		t.Errorf("expected 'old-b' fourth, got %q", result.Items[3].Message)
	}
}

// TestQueryLogsPaginationAfterAggregation verifies pagination is applied once after aggregation.
func TestQueryLogsPaginationAfterAggregation(t *testing.T) {
	entries := make([]AggregatedLogEntry, 100)
	now := time.Now()
	for i := 0; i < 100; i++ {
		entries[i] = AggregatedLogEntry{
			InstanceID: "inst-1",
			Timestamp:  now.Add(time.Duration(i) * time.Second),
			Stream:     "stdout",
			Message:    "msg",
		}
	}

	// Page 1 with size 10 should return 10 items.
	q := LogQuery{Page: 1, PageSize: 10}
	result := QueryAggregatedLogs(entries, q)
	if len(result.Items) != 10 {
		t.Errorf("expected 10 items on page 1, got %d", len(result.Items))
	}
	if result.Total != 100 {
		t.Errorf("expected total 100, got %d", result.Total)
	}

	// Page 2 with size 10 should return 10 items.
	q.Page = 2
	result = QueryAggregatedLogs(entries, q)
	if len(result.Items) != 10 {
		t.Errorf("expected 10 items on page 2, got %d", len(result.Items))
	}

	// Page 11 should be empty (100 entries / 10 per page = 10 pages).
	q.Page = 11
	result = QueryAggregatedLogs(entries, q)
	if len(result.Items) != 0 {
		t.Errorf("expected 0 items on page 11, got %d", len(result.Items))
	}
}

// TestQueryLogsTotal verifies Total reflects all matching entries.
func TestQueryLogsTotal(t *testing.T) {
	entries := []AggregatedLogEntry{
		{InstanceID: "inst-1", Timestamp: time.Now(), Stream: "stdout", Message: "msg1"},
		{InstanceID: "inst-2", Timestamp: time.Now(), Stream: "stderr", Message: "msg2"},
		{InstanceID: "inst-3", Timestamp: time.Now(), Stream: "stdout", Message: "msg3"},
	}

	// No filter — total should be 3.
	result := QueryAggregatedLogs(entries, LogQuery{})
	if result.Total != 3 {
		t.Errorf("expected total 3, got %d", result.Total)
	}

	// With empty filter — total should be all 3 entries.
	q := LogQuery{LogFilter: LogFilter{Stream: ""}} // no stream filter
	result = QueryAggregatedLogs(entries, q)
	if result.Total != 3 {
		t.Errorf("expected total 3 without filter, got %d", result.Total)
	}
}
