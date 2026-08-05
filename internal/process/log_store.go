package process

import (
	"strings"
	"sync"
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
		// Remove oldest events to maintain max size.
		removed := len(s.events) - s.maxSize
		s.events = s.events[removed:]
	}
}

// GetLogs returns filtered and paginated log events.
func (s *LogStore) GetLogs(query LogQuery) *LogResult {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Parse time filters if provided.
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

	// Apply filters.
	var filtered []LogEvent
	for _, event := range s.events {
		// Filter by stream.
		if query.Stream != "" && event.Stream != query.Stream {
			continue
		}

		// Filter by time range.
		if hasFrom && event.Time.Before(fromTime) {
			continue
		}
		if hasTo && event.Time.After(toTime) {
			continue
		}

		// Filter by search substring (case-insensitive).
		if query.Search != "" {
			if !strings.Contains(strings.ToLower(event.Message), strings.ToLower(query.Search)) {
				continue
			}
		}

		filtered = append(filtered, event)
	}

	total := len(filtered)

	// Pagination: calculate start and end indices.
	pageSize := query.PageSize
	if pageSize <= 0 {
		pageSize = 50 // default page size
	}
	if pageSize > 500 {
		pageSize = 500 // max page size
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

// Clear removes all events from the store.
func (s *LogStore) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = make([]LogEvent, 0, s.maxSize)
}
