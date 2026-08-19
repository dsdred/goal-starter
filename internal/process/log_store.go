package process

import (
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// LogFilter represents criteria for filtering log events.
type LogFilter struct {
	Stream   string `json:"stream"`    // "stdout", "stderr", "system", or "" for all
	Search   string `json:"search"`    // substring to search in message
	MinLevel string `json:"min_level"` // "info", "warn", "error", or "" for all
	From     string `json:"from"`      // ISO 8601 timestamp
	To       string `json:"to"`        // ISO 8601 timestamp
}

// LogQuery represents a paginated log query with filters.
type LogQuery struct {
	LogFilter
	Page     int `json:"page"`
	PageSize int `json:"page_size"`
}

// LogResult holds filtered and paginated log events.
type LogResult struct {
	Total int        `json:"total"`
	Page  int        `json:"page"`
	Size  int        `json:"page_size"`
	Items []LogEvent `json:"items"`
}

// LogStore stores recent log events and provides filtering and pagination.
// Each LogStore has its own monotonic sequence counter — sequence values
// are local to this store and must be combined with InstanceID for global ordering.
type LogStore struct {
	mu       sync.RWMutex
	events   []LogEvent
	maxSize  int
	sequence atomic.Uint64 // monotonically increasing per-store sequence
}

// NewLogStore creates a new LogStore with the given maximum capacity.
func NewLogStore(maxSize int) *LogStore {
	if maxSize <= 0 {
		maxSize = 10000 // default
	}
	return &LogStore{
		events:  make([]LogEvent, 0, maxSize),
		maxSize: maxSize,
	}
}

// Add adds a log event to the store, assigning a local sequence number and evicting oldest if full.
// The sequence is assigned at append time — before eviction — ensuring every event
// gets a unique monotonic local sequence. Sequence values are local to this LogStore.
// For global ordering, combine with InstanceID via AggregateLogs.
func (s *LogStore) Add(event LogEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Assign local monotonic sequence at append time.
	event.Sequence = s.sequence.Add(1)
	// If LogEvent doesn't have Sequence field, use a separate tracking.
	// Since LogEvent struct has Time, Stream, Message — store sequence in events slice.

	s.events = append(s.events, event)
	if len(s.events) > s.maxSize {
		removed := len(s.events) - s.maxSize
		s.events = s.events[removed:]
	}
}

// GetLogs returns filtered and paginated log events.
func (s *LogStore) GetLogs(query LogQuery) *LogResult {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var fromTime, toTime time.Time
	var hasFrom, hasTo bool
	if query.From != "" {
		if t, err := time.Parse(time.RFC3339, query.From); err == nil {
			fromTime = t
			hasFrom = true
		}
	}
	if query.To != "" {
		if t, err := time.Parse(time.RFC3339, query.To); err == nil {
			toTime = t
			hasTo = true
		}
	}

	var filtered []LogEvent
	for _, event := range s.events {
		if query.Stream != "" && event.Stream != query.Stream {
			continue
		}
		if hasFrom && event.Time.Before(fromTime) {
			continue
		}
		if hasTo && event.Time.After(toTime) {
			continue
		}
		if query.Search != "" {
			if !strings.Contains(strings.ToLower(event.Message), strings.ToLower(query.Search)) {
				continue
			}
		}
		filtered = append(filtered, event)
	}

	total := len(filtered)

	pageSize := query.PageSize
	if pageSize <= 0 {
		pageSize = 50
	}
	if pageSize > 500 {
		pageSize = 500
	}

	page := query.Page
	if page < 1 {
		page = 1
	}

	start := (page - 1) * pageSize
	if start >= total {
		return &LogResult{
			Total: total,
			Page:  page,
			Size:  pageSize,
			Items: []LogEvent{},
		}
	}

	end := start + pageSize
	if end > total {
		end = total
	}

	items := filtered[start:end]
	if items == nil {
		items = []LogEvent{}
	}

	return &LogResult{
		Total: total,
		Page:  page,
		Size:  pageSize,
		Items: items,
	}
}

// Count returns the total number of events in the store.
func (s *LogStore) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.events)
}

// CollectAll returns all events as AggregatedLogEntry (without InstanceID).
func (s *LogStore) CollectAll() []AggregatedLogEntry {
	return s.CollectAllWithInstanceID("")
}

// CollectAllWithInstanceID returns all events as AggregatedLogEntry with
// the given instanceID attached to each entry.
func (s *LogStore) CollectAllWithInstanceID(instanceID string) []AggregatedLogEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	entries := make([]AggregatedLogEntry, 0, len(s.events))
	for _, event := range s.events {
		entries = append(entries, AggregatedLogEntry{
			InstanceID: instanceID,
			Timestamp:  event.Time,
			Stream:     event.Stream,
			Message:    event.Message,
		})
	}
	return entries
}

// Clear removes all events from the store.
func (s *LogStore) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = make([]LogEvent, 0, s.maxSize)
}

// ===== Log Broker for multi-instance subscriptions =====

// LogStream represents a log stream type.
type LogStream string

const (
	LogStreamStdout LogStream = "stdout"
	LogStreamStderr LogStream = "stderr"
	LogStreamSystem LogStream = "system"
)

// LogStreamEvent represents an SSE log event with instance context.
type LogStreamEvent struct {
	Sequence   uint64    `json:"sequence"`
	Timestamp  time.Time `json:"time"`
	InstanceID string    `json:"instance_id"`
	ModelID    string    `json:"model_id"`
	Stream     LogStream `json:"stream"`
	Message    string    `json:"message"`
}

// LogBroker manages multi-instance log subscriptions.
//
// Ownership model:
//   - Broker owns all subscriber data channels.
//   - Publish() takes a read lock, copies subscriber pointers, releases lock,
//     then attempts non-blocking sends.
//   - Cancel() takes a write lock, marks subscriber as closed, removes from map,
//     releases lock, then closes done channel (not data channel).
//   - Data channel is closed ONLY by Shutdown(), guaranteeing no send-on-closed-channel.
//   - Shutdown() takes a write lock, copies and clears the map, releases lock,
//     then closes all data channels and done channels.
//   - Data channel close is owned by the broker (in Shutdown) or by a cancelled
//     subscriber (in Cancel). Only one of them will close it, guaranteed by
//     closed.Swap(true).
//
// Slow subscriber policy: dropNewest — when the buffer is full, the event is
// dropped for that subscriber (counted in DroppedEvents).
type LogBroker struct {
	mu          sync.RWMutex
	subscribers map[*logSubscriber]struct{}
	seq         atomic.Uint64
	dropped     atomic.Uint64
	bufferSize  int
	policy      dropPolicy
}

type dropPolicy int

const (
	dropNewest dropPolicy = iota
)

// logSubscriber holds subscriber state.
// The broker owns the data channel; only close() can terminate the subscriber.
// close() is called by Cancel(), slow-drop, and Shutdown().
// close() is guaranteed to be called exactly once via sync.Once.
type logSubscriber struct {
	ch         chan LogStreamEvent
	instanceID string
	closed     atomic.Bool // true after close() completes
	done       chan struct{}
	closeOnce  sync.Once // ensures exactly-one close
}

// close terminates the subscriber.
// It closes done exactly once, sets closed flag, and removes from broker map.
// Safe for concurrent use — only the first call takes effect.
// All other calls become no-ops.
//
// Contract:
//   - close() is the sole owner of done channel closure.
//   - Cancel(), slow-drop, and Shutdown() all delegate to close().
//   - Data channel is NEVER closed by close() — lifecycle managed by GC.
//   - After close(), ls.closed is true and done is closed.
//   - No send-on-closed-channel: Publish() checks closed.Load() before send.
func (s *logSubscriber) close() {
	if s == nil {
		return
	}

	s.closeOnce.Do(func() {
		s.closed.Store(true)
		close(s.done)
	})
}

// NewLogBroker creates a new LogBroker.
func NewLogBroker(bufferSize int) *LogBroker {
	if bufferSize <= 0 {
		bufferSize = 4096
	}
	return &LogBroker{
		subscribers: make(map[*logSubscriber]struct{}),
		bufferSize:  bufferSize,
		policy:      dropNewest,
	}
}

// Subscribe creates a new log subscription filtered by instanceID.
// Returns a LogSubscription; Cancel() is idempotent and safe.
// The returned subscription's done channel is closed when Cancel() is called,
// when the subscriber is dropped for being slow, or when Shutdown() is called.
func (b *LogBroker) Subscribe(instanceID string) *LogSubscription {
	done := make(chan struct{})
	lsub := &logSubscriber{
		instanceID: instanceID,
		ch:         make(chan LogStreamEvent, b.bufferSize),
		done:       done,
	}

	b.mu.Lock()
	b.subscribers[lsub] = struct{}{}
	b.mu.Unlock()

	return &LogSubscription{
		ch:     lsub.ch,
		done:   done,
		broker: b,
		lsub:   lsub,
	}
}

// Publish sends a log event to matching subscribers.
// Safe for concurrent use. No recover() — the closed flag ensures the
// narrow race window between snapshot-iteration and Cancel() is handled
// without send-on-closed-channel panic.
//
// Slow subscriber policy: dropNewest — when buffer is full, the event is
// dropped for that subscriber and counted in DroppedEvents().
func (b *LogBroker) Publish(ev LogStreamEvent) {
	seq := b.seq.Add(1)
	ev.Sequence = seq

	b.mu.RLock()
	subs := make([]*logSubscriber, 0, len(b.subscribers))
	for ls := range b.subscribers {
		subs = append(subs, ls)
	}
	b.mu.RUnlock()

	dropped := 0
	for _, ls := range subs {
		// Fast path: skip already-closed subscribers.
		if ls.closed.Load() {
			continue
		}
		// Filter by instanceID if subscriber has a filter.
		if ls.instanceID != "" && ev.InstanceID != ls.instanceID {
			continue
		}
		// Non-blocking send. On full buffer, drop the event (dropNewest policy).
		select {
		case ls.ch <- ev:
			// Delivered.
		default:
			dropped++
		}
	}

	if dropped > 0 {
		b.dropped.Add(uint64(dropped))
	}
}

// DroppedEvents returns the number of dropped events.
func (b *LogBroker) DroppedEvents() uint64 {
	return b.dropped.Load()
}

// LogSubscription represents an active log subscription.
type LogSubscription struct {
	closeOnce sync.Once
	ch        chan LogStreamEvent
	done      chan struct{}
	broker    *LogBroker
	lsub      *logSubscriber // shared with the one returned by Subscribe (has done channel)
}

// Channel returns the log subscription channel.
func (s *LogSubscription) Channel() <-chan LogStreamEvent {
	return s.ch
}

// Done returns the done channel.
func (s *LogSubscription) Done() <-chan struct{} {
	return s.done
}

// Cancel safely cancels the subscription. Idempotent and safe against concurrent publish.
//
// Contract:
//   - Cancel() is idempotent — only the first call takes effect.
//   - Concurrent Cancel() calls are safe.
//   - Cancel() simultaneously with Publish() is safe — Publish takes an RLock snapshot;
//     events already in the snapshot may be delivered, events added after cancel are skipped
//     because the subscriber is removed from the broker's map and ls.closed is true.
//   - Cancel() simultaneously with Shutdown() is safe — Cancel removes from map,
//     Shutdown calls close() which is guarded by sync.Once.
//   - Done() is closed exactly once (by Cancel or Shutdown).
//   - Data channel is NEVER closed — lifecycle managed by GC.
//   - No goroutine leaks — Cancel() removes the subscriber from the broker's map.
//   - No send-on-closed-channel — after close(), ls.closed is true and Publish() skips.
//   - No events after cancel — the subscriber is removed from the broker's subscriber map.
//
// close() is the sole owner of subscriber termination:
//   - Sets closed=true
//   - Closes done channel
//   - Protected by sync.Once
//
// Cancel() calls only close() — no separate closed.Swap(true).
func (s *LogSubscription) Cancel() {
	if s.lsub == nil {
		s.closeOnce.Do(func() {
			close(s.done)
		})
		return
	}
	// Remove from broker's subscriber map under lock.
	s.broker.mu.Lock()
	delete(s.broker.subscribers, s.lsub)
	s.broker.mu.Unlock()
	// close() is the only owner: sets closed=true and closes done.
	s.lsub.close()
}

// Shutdown marks all subscribers as closed and clears the subscriber map.
// Data channels are NOT closed — they are left for GC to reclaim after
// consumers detect completion via done channel or closed flag.
//
// Shutdown is the final lifecycle step: after Shutdown(), no new events
// can be delivered to any subscriber. Cancel() is safe concurrently with
// Shutdown() because both operate on ls.closed (atomic) and the subscriber map
// (protected by the same mutex).
//
// Shutdown contract:
//   - Data channels are NEVER closed — lifecycle managed by GC after consumer stops.
//   - Done channels are closed exactly once (by Cancel or Shutdown).
//   - After Shutdown(), ls.closed is true for all subscribers.
//   - No send-on-closed-channel: Publish() checks ls.closed before sending;
//     if Shutdown() sets ls.closed=true first, Publish() skips the send entirely.
//   - No double-close: Shutdown() delegates to close() which uses sync.Once;
//     if Cancel() ran first, close() is a no-op (closed already true).
//
// TOCTOU safety: Shutdown() acquires the broker mutex to copy+clear the map.
// Publish() acquires the read lock for snapshot. Because Shutdown() sets
// closed=true (via close()) BEFORE releasing any synchronization, any concurrent
// Publish() that acquires the read lock BEFORE Shutdown clears the map will see
// the subscriber in the snapshot. If that subscriber's closed was already true
// (Cancel ran first), Publish skips it. If closed is false but Shutdown set it
// true between snapshot and send, the send may still occur — but since data
// channels are never closed, no panic happens. The event is delivered to a
// subscriber whose done is now closed; the consumer will detect this on its
// next select.
func (b *LogBroker) Shutdown() {
	b.mu.Lock()
	subs := make([]*logSubscriber, 0, len(b.subscribers))
	for ls := range b.subscribers {
		subs = append(subs, ls)
	}
	b.subscribers = make(map[*logSubscriber]struct{})
	b.mu.Unlock()

	for _, ls := range subs {
		// Delegate to close() — guarantees exactly-one done closure and closed=true.
		// If Cancel() already closed done (sync.Once prevents double-close),
		// close() is a no-op. If Shutdown() runs first, close() closes done
		// and sets closed=true atomically.
		ls.close()
	}
}

// ===== Multi-instance log aggregation for QueryLogs =====

// AggregatedLogEntry is a unified log entry from multiple instances.
type AggregatedLogEntry struct {
	Sequence   uint64
	InstanceID string
	Timestamp  time.Time
	Stream     string
	Message    string
}

// AggregateLogs collects log events from multiple log stores into unified sorted entries.
// Each LogStore assigns a local monotonic sequence at Append time via Add().
// This function preserves the existing Sequence values from LogEvent items.
// If Sequence is zero (legacy), a local monotonic sequence is assigned.
//
// Sort order (strict total order):
//  1. Timestamp DESC
//  2. InstanceID ASC
//  3. LocalSequence DESC (within same instance, later events first)
//  4. Stream ASC
//  5. Message ASC
//
// This ensures deterministic ordering across multiple runs and instances.
func AggregateLogs(instances map[string]*LogStore, instanceIDFilter string) []AggregatedLogEntry {
	var all []AggregatedLogEntry
	var mu sync.Mutex
	var wg sync.WaitGroup

	for instID, store := range instances {
		wg.Add(1)
		go func(id string, s *LogStore) {
			defer wg.Done()
			q := LogQuery{}
			res := s.GetLogs(q)
			if res == nil || len(res.Items) == 0 {
				return
			}
			mu.Lock()
			defer mu.Unlock()
			for _, item := range res.Items {
				if instanceIDFilter != "" && id != instanceIDFilter {
					continue
				}
				all = append(all, AggregatedLogEntry{
					Sequence:   item.Sequence, // Preserve local sequence from LogEvent
					InstanceID: id,
					Timestamp:  item.Time,
					Stream:     item.Stream,
					Message:    item.Message,
				})
			}
		}(instID, store)
	}

	wg.Wait()

	// Assign stable global sequence numbers if not already set.
	// Sequence is monotonically increasing — identical timestamps do not affect order.
	seqCounter := uint64(1)
	for i := range all {
		if all[i].Sequence == 0 {
			all[i].Sequence = seqCounter
			seqCounter++
		}
	}

	// Sort by strict total order:
	// Timestamp DESC → InstanceID ASC → LocalSequence DESC → Stream ASC → Message ASC
	sort.SliceStable(all, func(i, j int) bool {
		if !all[i].Timestamp.Equal(all[j].Timestamp) {
			return all[i].Timestamp.After(all[j].Timestamp)
		}
		if all[i].InstanceID != all[j].InstanceID {
			return all[i].InstanceID < all[j].InstanceID
		}
		if all[i].Sequence != all[j].Sequence {
			return all[i].Sequence > all[j].Sequence
		}
		if all[i].Stream != all[j].Stream {
			return all[i].Stream < all[j].Stream
		}
		return all[i].Message < all[j].Message
	})

	return all
}

// QueryAggregatedLogs performs a filtered, paginated query on aggregated log entries.
// Entries are sorted by strict total order:
//  1. DESC timestamp
//  2. ASC instance ID
//  3. DESC local sequence (within same instance)
//  4. ASC stream
//  5. ASC message
//
// This ensures deterministic ordering across multiple instances and runs.
func QueryAggregatedLogs(entries []AggregatedLogEntry, query LogQuery) *LogResult {
	// Parse time bounds.
	var fromTime, toTime time.Time
	if query.From != "" {
		if t, err := time.Parse(time.RFC3339, query.From); err == nil {
			fromTime = t
		}
	}
	if query.To != "" {
		if t, err := time.Parse(time.RFC3339, query.To); err == nil {
			toTime = t
		}
	}

	// Apply filters.
	var filtered []AggregatedLogEntry
	for _, e := range entries {
		if query.Stream != "" && e.Stream != query.Stream {
			continue
		}
		if !fromTime.IsZero() && e.Timestamp.Before(fromTime) {
			continue
		}
		if !toTime.IsZero() && e.Timestamp.After(toTime) {
			continue
		}
		if query.Search != "" {
			if !strings.Contains(strings.ToLower(e.Message), strings.ToLower(query.Search)) {
				continue
			}
		}
		filtered = append(filtered, e)
	}

	// Sort deterministically by strict total order:
	// Timestamp DESC → InstanceID ASC → LocalSequence DESC → Stream ASC → Message ASC
	sort.SliceStable(filtered, func(i, j int) bool {
		if !filtered[i].Timestamp.Equal(filtered[j].Timestamp) {
			return filtered[i].Timestamp.After(filtered[j].Timestamp)
		}
		if filtered[i].InstanceID != filtered[j].InstanceID {
			return filtered[i].InstanceID < filtered[j].InstanceID
		}
		if filtered[i].Sequence != filtered[j].Sequence {
			return filtered[i].Sequence > filtered[j].Sequence
		}
		if filtered[i].Stream != filtered[j].Stream {
			return filtered[i].Stream < filtered[j].Stream
		}
		return filtered[i].Message < filtered[j].Message
	})

	total := len(filtered)

	pageSize := query.PageSize
	if pageSize <= 0 {
		pageSize = 50
	}
	if pageSize > 500 {
		pageSize = 500
	}

	page := query.Page
	if page < 1 {
		page = 1
	}

	start := (page - 1) * pageSize
	if start >= total {
		return &LogResult{
			Total: total,
			Page:  page,
			Size:  pageSize,
			Items: []LogEvent{},
		}
	}

	end := start + pageSize
	if end > total {
		end = total
	}

	items := make([]LogEvent, 0, end-start)
	for _, e := range filtered[start:end] {
		items = append(items, LogEvent{
			Sequence: e.Sequence,
			Time:     e.Timestamp,
			Stream:   e.Stream,
			Message:  e.Message,
		})
	}

	return &LogResult{
		Total: total,
		Page:  page,
		Size:  pageSize,
		Items: items,
	}
}
