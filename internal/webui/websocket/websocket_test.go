package websocket

import (
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// mockUpgrader upgrades connections using a mock listener.
type mockUpgrader struct {
	serverConn net.Conn
	clientConn net.Conn
}

func (m *mockUpgrader) Upgrade(w http.ResponseWriter, r *http.Request) (Conn, error) {
	go func() {
		buf := make([]byte, 4096)
		for {
			_, err := m.clientConn.Read(buf)
			if err != nil {
				return
			}
		}
	}()
	w.WriteHeader(http.StatusOK)
	w.(http.Flusher).Flush()
	return &mockConn{conn: m.serverConn}, nil
}

type mockConn struct {
	conn net.Conn
}

func (m *mockConn) WriteMessage(msgType int, data []byte) error {
	msg := append([]byte{byte(msgType)}, data...)
	_, err := m.conn.Write(msg)
	return err
}

func (m *mockConn) ReadMessage() (int, []byte, error) {
	buf := make([]byte, 4096)
	n, err := m.conn.Read(buf)
	if err != nil {
		return 0, nil, err
	}
	return 1, buf[:n], nil
}

func (m *mockConn) Close() error {
	return m.conn.Close()
}

func TestNewServer(t *testing.T) {
	s := NewServer()
	if s == nil {
		t.Fatal("expected non-nil Server")
	}
	if s.ClientCount() != 0 {
		t.Errorf("expected 0 clients, got %d", s.ClientCount())
	}
}

func TestHandleWebSocket_MethodNotAllowed(t *testing.T) {
	s := NewServer()
	req := httptest.NewRequest(http.MethodPost, "/ws", nil)
	w := httptest.NewRecorder()

	s.HandleWebSocket(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestHandleWebSocket_SendToClient(t *testing.T) {
	s := NewServer()

	// Send to empty client list - should not panic.
	s.Send("test", map[string]string{"key": "value"})

	if s.ClientCount() != 0 {
		t.Fatalf("expected 0 clients, got %d", s.ClientCount())
	}
}

func TestSendRaw(t *testing.T) {
	s := NewServer()

	// Should not panic even with no clients.
	s.SendRaw([]byte(`{"type":"raw"}`))
}

func TestClientCount(t *testing.T) {
	s := NewServer()

	if count := s.ClientCount(); count != 0 {
		t.Errorf("expected 0 clients, got %d", count)
	}

	// ClientCount should be safe to call concurrently.
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = s.ClientCount()
		}()
	}
	wg.Wait()
}

func TestIsWebSocket_True(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Upgrade", "websocket")

	if !IsWebSocket(req) {
		t.Error("expected true for websocket upgrade request")
	}
}

func TestIsWebSocket_False(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Upgrade", "non-websocket")

	if IsWebSocket(req) {
		t.Error("expected false for non-websocket upgrade request")
	}
}

func TestIsWebSocket_NoUpgrade(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	if IsWebSocket(req) {
		t.Error("expected false when no upgrade header")
	}
}

func TestSend_NilData(t *testing.T) {
	s := NewServer()

	// Should not panic with nil data.
	s.Send("ping", nil)
}

func TestSend_Concurrent(t *testing.T) {
	s := NewServer()

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.Send("test", map[string]int{"iteration": i})
		}()
	}
	wg.Wait()
}

func TestNewServer_NoRace(t *testing.T) {
	s := NewServer()

	var wg sync.WaitGroup
	// Simulate concurrent access.
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = s.ClientCount()
			s.Send("data", nil)
		}()
	}
	wg.Wait()
}
