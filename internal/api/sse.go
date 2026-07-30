package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/wltechblog/gino/internal/chat"
)

// ============================================================================
// SSE Stream Manager
// ============================================================================
//
// Each SSE connection registers a dispatcher that receives outbound messages
// from the hub. When an agent turn produces output (final reply, tool activity
// notifications, errors), it flows through the hub to the matching dispatcher.
//
// Connection lifecycle:
//  1. Client connects to GET /api/v1/chat (SSE)
//  2. Server creates a dispatcher with a unique connection ID
//  3. Client sends POST /api/v1/chat with the connection ID
//  4. Agent processes the message; output streams via SSE
//  5. Client disconnects → dispatcher is cleaned up

// SSEEvent represents a single Server-Sent Event.
type SSEEvent struct {
	// Event type: "message", "tool", "error", "done", "connected"
	Event string `json:"event"`

	// Data payload (varies by event type).
	Data interface{} `json:"data"`
}

// Dispatcher manages SSE connections and routes hub output to waiting clients.
type Dispatcher struct {
	mu      sync.Mutex
	conns   map[string]*sseConn // connID -> connection
	connSeq int                 // auto-increment for connection IDs
}

// sseConn represents a single SSE client connection.
type sseConn struct {
	id     string
	userID string
	ch     chan chat.Outbound
	hub    *chat.Hub
}

// NewDispatcher creates a new SSE dispatcher.
func NewDispatcher() *Dispatcher {
	return &Dispatcher{
		conns: make(map[string]*sseConn),
	}
}

// register creates a new SSE connection and returns its ID.
// The connection is scoped to the given user ID for security.
func (d *Dispatcher) register(userID string, buf int) *sseConn {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.connSeq++
	id := fmt.Sprintf("conn-%d-%d", d.connSeq, time.Now().UnixNano())
	conn := &sseConn{
		id:     id,
		userID: userID,
		ch:     make(chan chat.Outbound, buf),
	}
	d.conns[id] = conn
	return conn
}

// unregister removes an SSE connection.
func (d *Dispatcher) unregister(id string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if conn, ok := d.conns[id]; ok {
		close(conn.ch)
		delete(d.conns, id)
	}
}

// get returns a connection by ID. Returns nil if not found.
func (d *Dispatcher) get(id string) *sseConn {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.conns[id]
}

// activeCount returns the number of active connections.
func (d *Dispatcher) activeCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.conns)
}

// ─── SSE Writer ──────────────────────────────────────────────────────────────

// SSEWriter wraps an http.ResponseWriter to write SSE-formatted events.
// It handles flush timing, heartbeats, and proper Content-Type headers.
type SSEWriter struct {
	w               http.ResponseWriter
	flusher         http.Flusher
	heartbeatTicker *time.Ticker
}

// NewSSEWriter sets up the response for SSE streaming and returns a writer.
// Call Done() when finished to clean up resources.
func NewSSEWriter(w http.ResponseWriter) *SSEWriter {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // disable nginx buffering

	flusher, _ := w.(http.Flusher)

	return &SSEWriter{
		w:       w,
		flusher: flusher,
	}
}

// WriteEvent writes a single SSE event to the response.
// The data field is JSON-encoded.
func (sw *SSEWriter) WriteEvent(eventType string, data interface{}) error {
	payload, err := json.Marshal(data)
	if err != nil {
		return err
	}

	// SSE format: event: <type>\ndata: <json>\n\n
	var sb strings.Builder
	if eventType != "" {
		sb.WriteString("event: ")
		sb.WriteString(eventType)
		sb.WriteString("\n")
	}
	sb.WriteString("data: ")
	sb.Write(payload)
	sb.WriteString("\n\n")

	_, err = fmt.Fprint(sw.w, sb.String())
	if err != nil {
		return err
	}

	if sw.flusher != nil {
		sw.flusher.Flush()
	}
	return nil
}

// WriteHeartbeat sends a comment line to keep the connection alive.
// Proxies typically close idle connections after 60-120 seconds.
func (sw *SSEWriter) WriteHeartbeat() {
	fmt.Fprint(sw.w, ": heartbeat\n\n")
	if sw.flusher != nil {
		sw.flusher.Flush()
	}
}

// StartHeartbeat sends periodic keepalive comments.
// Returns a stop function that should be called when the stream ends.
func (sw *SSEWriter) StartHeartbeat(interval time.Duration) func() {
	sw.heartbeatTicker = time.NewTicker(interval)
	done := make(chan struct{})

	go func() {
		for {
			select {
			case <-sw.heartbeatTicker.C:
				sw.WriteHeartbeat()
			case <-done:
				sw.heartbeatTicker.Stop()
				return
			}
		}
	}()

	return func() { close(done) }
}
