package websocket

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// UpgraderFunc is called to get an upgrader; allows test injection.
var UpgraderFunc = func() Upgrader {
	return &netUpgrader{}
}

// Upgrader upgrades an HTTP connection to WebSocket.
type Upgrader interface {
	Upgrade(w http.ResponseWriter, r *http.Request) (Conn, error)
}

type netUpgrader struct{}

func (u *netUpgrader) Upgrade(w http.ResponseWriter, r *http.Request) (Conn, error) {
	hj, ok := w.(http.Hijacker)
	if !ok {
		return nil, fmt.Errorf("hijack not supported")
	}
	conn, _, err := hj.Hijack()
	if err != nil {
		return nil, err
	}
	return &wsConn{conn: conn}, nil
}

// Conn is a WebSocket-like connection.
type Conn interface {
	WriteMessage(msgType int, data []byte) error
	ReadMessage() (int, []byte, error)
	Close() error
}

type wsConn struct {
	conn net.Conn
}

const (
	MessageText   = 1
	MessageBinary = 9
)

func (c *wsConn) WriteMessage(msgType int, data []byte) error {
	// Simple text frame.
	frame := make([]byte, 2+len(data))
	frame[0] = byte(msgType << 4)
	frame[1] = byte(len(data))
	copy(frame[2:], data)
	_, err := c.conn.Write(frame)
	return err
}

func (c *wsConn) ReadMessage() (int, []byte, error) {
	buf := make([]byte, 65536)
	n, err := c.conn.Read(buf)
	if err != nil {
		return 0, nil, err
	}
	return MessageText, buf[:n], nil
}

func (c *wsConn) Close() error {
	return c.conn.Close()
}

// Server manages WebSocket connections for log streaming.
type Server struct {
	mu      sync.RWMutex
	clients map[Conn]bool
}

// NewServer creates a new WebSocket Server.
func NewServer() *Server {
	return &Server{
		clients: make(map[Conn]bool),
	}
}

// HandleWebSocket handles WebSocket upgrade and log streaming.
func (s *Server) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	upgrader := UpgraderFunc()
	conn, err := upgrader.Upgrade(w, r)
	if err != nil {
		http.Error(w, "upgrade failed", http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	s.clients[conn] = true
	s.mu.Unlock()

	// Send welcome message.
	welcome := map[string]any{
		"type":    "welcome",
		"message": "WebSocket log stream connected",
		"time":    time.Now().UTC().Format(time.RFC3339),
	}
	data, _ := json.Marshal(welcome)
	_ = conn.WriteMessage(MessageText, data)

	// Read messages from client (for keepalive or commands).
	go func() {
		defer s.remove(conn)
		for {
			_, _, err := conn.ReadMessage()
			if err != nil {
				return
			}
		}
	}()
}

// Send broadcasts a message to all connected clients.
func (s *Server) Send(msgType string, data any) {
	s.mu.RLock()
	clients := make([]Conn, 0, len(s.clients))
	for c := range s.clients {
		clients = append(clients, c)
	}
	s.mu.RUnlock()

	payload := map[string]any{
		"type": msgType,
		"time": time.Now().UTC().Format(time.RFC3339),
	}
	if data != nil {
		payload["data"] = data
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return
	}

	for _, conn := range clients {
		_ = conn.WriteMessage(MessageText, jsonData)
	}
}

// SendRaw sends raw JSON to all clients.
func (s *Server) SendRaw(jsonData []byte) {
	s.mu.RLock()
	clients := make([]Conn, 0, len(s.clients))
	for c := range s.clients {
		clients = append(clients, c)
	}
	s.mu.RUnlock()

	for _, conn := range clients {
		_ = conn.WriteMessage(MessageText, jsonData)
	}
}

// ClientCount returns the number of connected clients.
func (s *Server) ClientCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.clients)
}

func (s *Server) remove(conn Conn) {
	s.mu.Lock()
	delete(s.clients, conn)
	s.mu.Unlock()
	_ = conn.Close()
}

// IsWebSocket checks if the request is a WebSocket upgrade request.
func IsWebSocket(r *http.Request) bool {
	connHeaders := r.Header.Get("Upgrade")
	return strings.EqualFold(connHeaders, "websocket")
}
