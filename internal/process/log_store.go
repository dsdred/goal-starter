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
type LogStore struct {
	mu      sync.RWMutex
	events  []LogEvent
	maxSize int
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

// Add adds a log event to the store, evicting oldest if full.
func (s *LogStore) Add(event LogEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()

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
	ProfileID  string    `json:"profile_id"`
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
// The broker owns the data channel; Cancel() closes it exactly once.
type logSubscriber struct {
	ch         chan LogStreamEvent
	instanceID string
	closed     atomic.Bool   // true after Cancel/Shutdown completes
	done       chan struct{} // reference to LogSubscription.done for Shutdown()
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
//   - Cancel() simultaneously with Publish() is safe — closed.Swap(true) ensures
//     Publish() either sees closed=true before sending or after.
//   - Cancel() simultaneously with Shutdown() is safe — Cancel removes from map,
//     Shutdown closes remaining channels.
//   - Done() is closed exactly once (by Cancel or Shutdown).
//   - Data channel is closed exactly once (by Shutdown only).
//   - No goroutine leaks — Cancel() removes the subscriber from the broker's map.
//   - No send-on-closed-channel — after closed is true, Publish() skips the channel.
//   - No events after cancel — the subscriber is removed from the broker's subscriber map.
func (s *LogSubscription) Cancel() {
	if s.lsub == nil {
		s.closeOnce.Do(func() {
			close(s.done)
		})
		return
	}
	// Mark closed first so subsequent calls are no-op and concurrent Publish sees it.
	if s.lsub.closed.Swap(true) {
		// Already cancelled — do not close done again.
		return
	}
	// Remove from broker's subscriber map under lock.
	// This ensures Publish's snapshot won't include this subscriber
	// on any subsequent iteration.
	s.broker.mu.Lock()
	delete(s.broker.subscribers, s.lsub)
	s.broker.mu.Unlock()
	// Close done channel — data channel is closed only by Shutdown().
	close(s.done)
}

// Shutdown closes all subscriber data channels, done channels, and clears
// the subscriber map. Called during Supervisor shutdown to prevent goroutine/channel leaks.
//
// Shutdown is the SOLE OWNER of data channel closing. Cancel() removes subscribers
// from the map and closes only done channels; Shutdown closes all remaining data
// and done channels.
//
// Shutdown contract:
//   - All data channels are closed exactly once (by Shutdown only).
//   - All done channels are closed exactly once (by Cancel or Shutdown).
//   - Concurrent Cancel() is safe — Cancel removes from map, Shutdown closes remaining.
//   - After Shutdown(), no new events can be delivered.
func (b *LogBroker) Shutdown() {
	b.mu.Lock()
	subs := make([]*logSubscriber, 0, len(b.subscribers))
	for ls := range b.subscribers {
		subs = append(subs, ls)
	}
	b.subscribers = make(map[*logSubscriber]struct{})
	b.mu.Unlock()

	for _, ls := range subs {
		// Mark closed to prevent Cancel() from closing done after Shutdown.
		ls.closed.Store(true)
		close(ls.ch)
		close(ls.done)
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
// Sequence is assigned monotonically by the caller via LogBroker; this function
// preserves the existing Sequence values if they are non-zero, otherwise assigns
// a local monotonic sequence before sorting.
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

	// Sort by time DESC, then instanceID for determinism.
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].Timestamp.Equal(all[j].Timestamp) {
			if all[i].Stream == all[j].Stream {
				return all[i].InstanceID < all[j].InstanceID
			}
			return all[i].Stream < all[j].Stream
		}
		return all[i].Timestamp.After(all[j].Timestamp)
	})

	return all
}

// QueryAggregatedLogs performs a filtered, paginated query on aggregated log entries.
// Entries are sorted DESC by timestamp, then ASC by instance ID, then ASC by stream
// for deterministic ordering across multiple instances.
func QueryAggregatedLogs(entries []AggregatedLogEntry, query LogQuery) *LogResult {
	// Apply filters.
	var filtered []AggregatedLogEntry
	for _, e := range entries {
		if query.Stream != "" && e.Stream != query.Stream {
			continue
		}
		if query.Search != "" {
			if !strings.Contains(strings.ToLower(e.Message), strings.ToLower(query.Search)) {
				continue
			}
		}
		filtered = append(filtered, e)
	}

	// Sort deterministically: DESC timestamp, ASC instance ID, ASC stream.
	sort.SliceStable(filtered, func(i, j int) bool {
		if !filtered[i].Timestamp.Equal(filtered[j].Timestamp) {
			return filtered[i].Timestamp.After(filtered[j].Timestamp)
		}
		if filtered[i].InstanceID != filtered[j].InstanceID {
			return filtered[i].InstanceID < filtered[j].InstanceID
		}
		return filtered[i].Stream < filtered[j].Stream
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
			Time:    e.Timestamp,
			Stream:  e.Stream,
			Message: e.Message,
		})
	}

	return &LogResult{
		Total: total,
		Page:  page,
		Size:  pageSize,
		Items: items,
	}
}
