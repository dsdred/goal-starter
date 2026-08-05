package process

import (
	"testing"
	"time"
)

func TestNewLogStore(t *testing.T) {
	store := NewLogStore(100)
	if store == nil {
		t.Fatal("NewLogStore returned nil")
	}
	if store.maxSize != 100 {
		t.Errorf("expected maxSize 100, got %d", store.maxSize)
	}
}

func TestNewLogStoreDefaultSize(t *testing.T) {
	store := NewLogStore(0)
	if store.maxSize != 10000 {
		t.Errorf("expected default maxSize 10000, got %d", store.maxSize)
	}
}

func TestLogStoreAddAndGetLogs(t *testing.T) {
	store := NewLogStore(100)

	// Add some log events.
	baseTime := time.Now()
	events := []LogEvent{
		{Time: baseTime, Stream: "stdout", Message: "line 1"},
		{Time: baseTime.Add(time.Second), Stream: "stderr", Message: "error line"},
		{Time: baseTime.Add(2 * time.Second), Stream: "stdout", Message: "line 2"},
		{Time: baseTime.Add(3 * time.Second), Stream: "system", Message: "system info"},
		{Time: baseTime.Add(4 * time.Second), Stream: "stdout", Message: "line 3"},
	}

	for _, ev := range events {
		store.Add(ev)
	}

	// Get all logs.
	query := LogQuery{}
	result := store.GetLogs(query)

	if result.Total != 5 {
		t.Errorf("expected 5 total logs, got %d", result.Total)
	}
	if len(result.Items) != 5 {
		t.Errorf("expected 5 items, got %d", len(result.Items))
	}
}

func TestLogStoreFilterByStream(t *testing.T) {
	store := NewLogStore(100)

	baseTime := time.Now()
	store.Add(LogEvent{Time: baseTime, Stream: "stdout", Message: "out"})
	store.Add(LogEvent{Time: baseTime.Add(time.Second), Stream: "stderr", Message: "err"})
	store.Add(LogEvent{Time: baseTime.Add(2 * time.Second), Stream: "stdout", Message: "out2"})

	// Filter stdout only.
	query := LogQuery{
		LogFilter: LogFilter{Stream: "stdout"},
	}
	result := store.GetLogs(query)

	if result.Total != 2 {
		t.Errorf("expected 2 stdout logs, got %d", result.Total)
	}
	if len(result.Items) != 2 {
		t.Errorf("expected 2 items, got %d", len(result.Items))
	}
}

func TestLogStoreFilterBySearch(t *testing.T) {
	store := NewLogStore(100)

	baseTime := time.Now()
	store.Add(LogEvent{Time: baseTime, Stream: "stdout", Message: "hello world"})
	store.Add(LogEvent{Time: baseTime.Add(time.Second), Stream: "stdout", Message: "foo bar"})
	store.Add(LogEvent{Time: baseTime.Add(2 * time.Second), Stream: "stdout", Message: "hello again"})

	// Search for "hello" (case-insensitive).
	query := LogQuery{
		LogFilter: LogFilter{Search: "hello"},
	}
	result := store.GetLogs(query)

	if result.Total != 2 {
		t.Errorf("expected 2 logs matching 'hello', got %d", result.Total)
	}
}

func TestLogStoreFilterByTimeRange(t *testing.T) {
	store := NewLogStore(100)

	baseTime := time.Now()
	store.Add(LogEvent{Time: baseTime, Stream: "stdout", Message: "early"})
	store.Add(LogEvent{Time: baseTime.Add(5 * time.Second), Stream: "stdout", Message: "middle"})
	store.Add(LogEvent{Time: baseTime.Add(10 * time.Second), Stream: "stdout", Message: "late"})

	// Filter by time range.
	from := baseTime.Add(4 * time.Second).UTC().Format(time.RFC3339)
	to := baseTime.Add(6 * time.Second).UTC().Format(time.RFC3339)
	query := LogQuery{
		LogFilter: LogFilter{From: from, To: to},
	}
	result := store.GetLogs(query)

	if result.Total != 1 {
		t.Errorf("expected 1 log in time range, got %d", result.Total)
	}
}

func TestLogStorePagination(t *testing.T) {
	store := NewLogStore(100)

	baseTime := time.Now()
	for i := 0; i < 20; i++ {
		store.Add(LogEvent{
			Time:    baseTime.Add(time.Duration(i) * time.Second),
			Stream:  "stdout",
			Message: "line " + string(rune('0'+i)),
		})
	}

	// Page 1, size 5.
	query := LogQuery{
		Page:     1,
		PageSize: 5,
	}
	result := store.GetLogs(query)

	if result.Total != 20 {
		t.Errorf("expected total 20, got %d", result.Total)
	}
	if result.Page != 1 {
		t.Errorf("expected page 1, got %d", result.Page)
	}
	if result.Size != 5 {
		t.Errorf("expected page_size 5, got %d", result.Size)
	}
	if len(result.Items) != 5 {
		t.Errorf("expected 5 items on page 1, got %d", len(result.Items))
	}

	// Page 2, size 5.
	query.Page = 2
	result = store.GetLogs(query)

	if len(result.Items) != 5 {
		t.Errorf("expected 5 items on page 2, got %d", len(result.Items))
	}

	// Page 5, size 5 (last page).
	query.Page = 5
	result = store.GetLogs(query)

	if len(result.Items) != 0 {
		t.Errorf("expected 0 items on page 5 (empty), got %d", len(result.Items))
	}
}

func TestLogStoreMaxSize(t *testing.T) {
	store := NewLogStore(3)

	baseTime := time.Now()
	for i := 0; i < 10; i++ {
		store.Add(LogEvent{
			Time:    baseTime.Add(time.Duration(i) * time.Second),
			Stream:  "stdout",
			Message: "line",
		})
	}

	// Should only have 3 events (maxSize).
	if store.Count() != 3 {
		t.Errorf("expected 3 events after overflow, got %d", store.Count())
	}
}

func TestLogStoreClear(t *testing.T) {
	store := NewLogStore(100)

	baseTime := time.Now()
	store.Add(LogEvent{Time: baseTime, Stream: "stdout", Message: "test"})

	if store.Count() != 1 {
		t.Error("expected 1 event before clear")
	}

	store.Clear()

	if store.Count() != 0 {
		t.Error("expected 0 events after clear")
	}
}

func TestLogStoreCombinedFilters(t *testing.T) {
	store := NewLogStore(100)

	baseTime := time.Now()
	store.Add(LogEvent{Time: baseTime, Stream: "stdout", Message: "error: something failed"})
	store.Add(LogEvent{Time: baseTime.Add(time.Second), Stream: "stderr", Message: "error: another failure"})
	store.Add(LogEvent{Time: baseTime.Add(2 * time.Second), Stream: "stdout", Message: "info: success"})
	store.Add(LogEvent{Time: baseTime.Add(3 * time.Second), Stream: "stdout", Message: "warning: low memory"})

	// Filter stdout + search "error".
	query := LogQuery{
		LogFilter: LogFilter{Stream: "stdout", Search: "error"},
	}
	result := store.GetLogs(query)

	if result.Total != 1 {
		t.Errorf("expected 1 log (stdout + error), got %d", result.Total)
	}
}

func TestLogStoreInvalidPage(t *testing.T) {
	store := NewLogStore(100)

	store.Add(LogEvent{
		Time:    time.Now(),
		Stream:  "stdout",
		Message: "test",
	})

	// Page 0 should default to page 1.
	query := LogQuery{
		Page:     0,
		PageSize: 10,
	}
	result := store.GetLogs(query)

	// Should get 1 item (page 1 has 1 item).
	if len(result.Items) != 1 {
		t.Errorf("expected 1 item for page 0 (defaults to 1), got %d", len(result.Items))
	}
}

func TestLogStoreMaxPageSize(t *testing.T) {
	store := NewLogStore(100)

	baseTime := time.Now()
	for i := 0; i < 10; i++ {
		store.Add(LogEvent{
			Time:    baseTime.Add(time.Duration(i) * time.Second),
			Stream:  "stdout",
			Message: "line",
		})
	}

	// Request page size 1000 (should be capped at 500).
	query := LogQuery{
		Page:     1,
		PageSize: 1000,
	}
	result := store.GetLogs(query)

	// PageSize in response should be capped at 500, but we requested 1000.
	// Since we only have 10 items and page size is capped at 500, we get all 10.
	if len(result.Items) != 10 {
		t.Errorf("expected 10 items, got %d", len(result.Items))
	}
}
