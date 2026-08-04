package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/wltechblog/gino/internal/chat"
)

// ============================================================================
// HTTP Handlers
// ============================================================================
//
// All handlers assume authentication has already been performed by middleware.
// The user ID is available via userIDFromRequest(r).

// ─── GET /api/v1/health ──────────────────────────────────────────────────────

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	s.writeJSON(w, http.StatusOK, HealthResponse{
		Status:  "ok",
		Version: s.version,
		Uptime:  time.Since(s.startTime).Round(time.Second).String(),
		Active:  s.dispatcher.activeCount(),
	})
}

// ─── GET /api/v1/info ────────────────────────────────────────────────────────

func (s *Server) handleInfo(w http.ResponseWriter, r *http.Request) {
	s.writeJSON(w, http.StatusOK, InfoResponse{
		Model:         s.model,
		Tools:         s.toolNames(),
		MaxIterations: s.maxIterations,
		Vision:        s.visionSupported,
		Version:       s.version,
	})
}

// ─── POST /api/v1/chat/sync ──────────────────────────────────────────────────
//
// Non-streaming chat endpoint. Sends a message to the agent and waits for
// the complete response. For real-time output, use the streaming endpoint
// (GET /api/v1/chat) instead.
//
// This works by registering a temporary hub subscription for a unique chatID,
// injecting the message into the hub, and waiting for the agent's response.

func (s *Server) handleChatSync(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "Use POST")
		return
	}

	var req ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}

	if strings.TrimSpace(req.Message) == "" {
		s.writeError(w, http.StatusBadRequest, "message is required")
		return
	}

	userID := userIDFromRequest(r)
	sessionKey := s.buildSessionKey(userID, req.Session)

	// Generate a unique chatID for this request. The hub router dispatches
	// by channel name, and our routeHubResponses goroutine dispatches by
	// ChatID. We register a temporary SSE connection to receive the reply.
	conn := s.dispatcher.register(userID, 5)
	defer s.dispatcher.unregister(conn.id)

	// Inject the message into the hub
	s.hub.In <- chat.Inbound{
		Channel:   "api",
		SenderID:  userID,
		ChatID:    conn.id, // response will route back to this connection
		Content:   req.Message,
		Timestamp: time.Now(),
		Media:     req.Media,
		Metadata: map[string]interface{}{
			"session_key": sessionKey,
		},
	}

	// Wait for the response with a timeout
	timeout := time.Duration(s.cfg.RequestTimeoutS) * time.Second
	if timeout <= 0 {
		timeout = 120 * time.Second
	}

	var response chat.Outbound
	var ok bool
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case response, ok = <-conn.ch:
		if !ok {
			s.writeError(w, http.StatusInternalServerError, "Connection closed unexpectedly")
			return
		}
	case <-timer.C:
		s.writeError(w, http.StatusGatewayTimeout, "Agent did not respond within timeout")
		return
	case <-r.Context().Done():
		return // client disconnected
	}

	respSession := req.Session
	if respSession == "" {
		respSession = "default"
	}

	s.writeJSON(w, http.StatusOK, ChatSyncResponse{
		Response:   response.Content,
		Session:    respSession,
		Iterations: 0, // not available via hub in current architecture
	})
}

// ─── GET /api/v1/chat (SSE Streaming) ────────────────────────────────────────
//
// Opens a streaming SSE connection. The client sends messages via
// POST /api/v1/chat/stream with the connection ID received in the
// "connected" event.
//
// Flow:
//  1. Client opens GET /api/v1/chat (SSE)
//  2. Server sends "connected" event with connectionId
//  3. Client sends POST /api/v1/chat/stream with message + connectionId
//  4. Agent processes message; output streams as SSE events
//  5. Agent finishes → server sends "done" event
//  6. Client can send more messages or disconnect

func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromRequest(r)

	// Create a dedicated SSE connection
	conn := s.dispatcher.register(userID, 50)
	defer s.dispatcher.unregister(conn.id)

	sw := NewSSEWriter(w)
	stopHeartbeat := sw.StartHeartbeat(30 * time.Second)
	defer stopHeartbeat()

	// Send the connection established event with the connection ID
	if err := sw.WriteEvent("connected", map[string]string{
		"connectionId": conn.id,
		"server":       s.version,
	}); err != nil {
		return // client already gone
	}

	log.Printf("API: SSE connection %s opened for user %s", conn.id, userID)

	// Listen for outbound messages and stream them
	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			log.Printf("API: SSE connection %s closed by client", conn.id)
			return
		case out, ok := <-conn.ch:
			if !ok {
				return
			}

			// Determine event type from metadata
			eventType := "message"
			if out.Metadata != nil {
				if _, isTool := out.Metadata["tool_activity"]; isTool {
					eventType = "tool"
				}
				if _, isErr := out.Metadata["error"]; isErr {
					eventType = "error"
				}
				if _, isDone := out.Metadata["turn_done"]; isDone {
					eventType = "done"
				}
			}

			if err := sw.WriteEvent(eventType, map[string]interface{}{
				"content":    out.Content,
				"replyTo":    out.ReplyTo,
				"media":      out.Media,
				"metadata":   out.Metadata,
				"timestamp":  time.Now().Format(time.RFC3339),
			}); err != nil {
				log.Printf("API: SSE write error on connection %s: %v", conn.id, err)
				return
			}
		}
	}
}

// ─── POST /api/v1/chat/stream ─────────────────────────────────────────────────
//
// Sends a message to the agent. The response will be streamed to the SSE
// connection associated with the provided connectionId.

func (s *Server) handleStreamSend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "Use POST")
		return
	}

	var req struct {
		Message      string   `json:"message"`
		ConnectionID string   `json:"connectionId"`
		Session      string   `json:"session,omitempty"`
		Media        []string `json:"media,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}

	if strings.TrimSpace(req.Message) == "" {
		s.writeError(w, http.StatusBadRequest, "message is required")
		return
	}

	conn := s.dispatcher.get(req.ConnectionID)
	if conn == nil {
		s.writeError(w, http.StatusNotFound, "Invalid or expired connection ID")
		return
	}

	userID := userIDFromRequest(r)
	if conn.userID != userID {
		s.writeError(w, http.StatusForbidden, "Connection does not belong to this user")
		return
	}

	sessionKey := s.buildSessionKey(userID, req.Session)

	// Inject the message into the hub
	s.hub.In <- chat.Inbound{
		Channel:   "api",
		SenderID:  userID,
		ChatID:    conn.id, // route response back to this SSE connection
		Content:   req.Message,
		Timestamp: time.Now(),
		Media:     req.Media,
		Metadata: map[string]interface{}{
			"session_key":   sessionKey,
			"connection_id": conn.id,
		},
	}

	s.writeJSON(w, http.StatusAccepted, map[string]string{
		"status": "queued",
	})
}

// ─── Session Management ──────────────────────────────────────────────────────
//
// Sessions are namespaced per user: api:<userID>:<session>
// This ensures complete isolation between users.

func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromRequest(r)

	switch r.Method {
	case http.MethodGet:
		sessions := s.sessionList(userID)
		s.writeJSON(w, http.StatusOK, SessionListResponse{Sessions: sessions})

	case http.MethodPost:
		var req SessionCreateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			s.writeError(w, http.StatusBadRequest, "Invalid JSON body")
			return
		}
		key := req.Key
		if key == "" {
			key = fmt.Sprintf("session-%d", time.Now().UnixNano())
		}
		fullKey := s.buildSessionKey(userID, key)
		s.writeJSON(w, http.StatusCreated, SessionCreateResponse{Key: fullKey})

	default:
		s.writeError(w, http.StatusMethodNotAllowed, "Use GET or POST")
	}
}

func (s *Server) handleSessionByID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		s.writeError(w, http.StatusMethodNotAllowed, "Use DELETE")
		return
	}

	userID := userIDFromRequest(r)
	rawID := r.PathValue("id")
	if rawID == "" {
		// Fallback: extract from URL path
		path := strings.TrimPrefix(r.URL.Path, "/api/v1/sessions/")
		rawID = strings.TrimSpace(path)
	}
	if rawID == "" {
		s.writeError(w, http.StatusBadRequest, "session id is required")
		return
	}

	log.Printf("api: delete session %s for user %s", rawID, userID)
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

func (s *Server) buildSessionKey(userID, session string) string {
	if session == "" {
		session = "default"
	}
	return fmt.Sprintf("api:%s:%s", userID, session)
}

func (s *Server) sessionList(userID string) []SessionInfo {
	// Session listing requires integration with the session manager.
	// This will be wired up when the server is integrated into the gateway.
	return []SessionInfo{}
}

func (s *Server) toolNames() []string {
	names := make([]string, 0, len(s.tools))
	for name := range s.tools {
		names = append(names, name)
	}
	return names
}

// ─── JSON Helpers ────────────────────────────────────────────────────────────

func (s *Server) writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func (s *Server) writeError(w http.ResponseWriter, status int, msg string) {
	log.Printf("api: error %d — %s", status, msg)
	s.writeJSON(w, status, ErrorResponse{Error: msg})
}
