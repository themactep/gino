package api

// ============================================================================
// API Request/Response Types
// ============================================================================
//
// These types define the public API contract. They are designed to be stable
// across versions and self-documenting for client SDKs.
//
// All API endpoints accept and return JSON unless otherwise noted.
// Streaming endpoints use Server-Sent Events (SSE).

// ChatRequest is the payload for POST /api/v1/chat (streaming) and
// POST /api/v1/chat/sync (non-streaming).
type ChatRequest struct {
	// Message is the user's input text. Required.
	Message string `json:"message"`

	// Session is an optional session identifier for multi-turn conversations.
	// If omitted, the server generates one and returns it in the response.
	// Use the same session value across requests to maintain conversation
	// context. Sessions are scoped to the authenticated user.
	Session string `json:"session,omitempty"`

	// Media is an optional list of base64-encoded images or files.
	// Each entry should be a data URL: data:image/png;base64,iVBOR...
	Media []string `json:"media,omitempty"`
}

// ChatSyncResponse is the JSON response for POST /api/v1/chat/sync.
// It returns the complete agent response after all tool calls finish.
type ChatSyncResponse struct {
	// Response is the agent's final text reply.
	Response string `json:"response"`

	// Session is the session identifier used for this request.
	// Echoed back so clients can store it for multi-turn conversations.
	Session string `json:"session"`

	// Iterations is the number of LLM round-trips used to produce the answer.
	Iterations int `json:"iterations"`

	// ToolCalls is a summary of tools executed during this turn.
	ToolCalls []ToolCallSummary `json:"toolCalls,omitempty"`
}

// ToolCallSummary describes a single tool invocation for the client.
type ToolCallSummary struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments,omitempty"` // truncated for readability
	Duration  string `json:"duration,omitempty"`  // human-readable
	Success   bool   `json:"success"`
}

// SessionInfo describes a saved conversation session.
type SessionInfo struct {
	Key       string `json:"key"`
	Title     string `json:"title,omitempty"`
	Messages  int    `json:"messages"`
	CreatedAt string `json:"createdAt,omitempty"`
	UpdatedAt string `json:"updatedAt,omitempty"`
}

// SessionListResponse is returned by GET /api/v1/sessions.
type SessionListResponse struct {
	Sessions []SessionInfo `json:"sessions"`
}

// SessionCreateRequest creates or switches to a session.
type SessionCreateRequest struct {
	// Key is an optional custom session key. If omitted, one is generated.
	Key string `json:"key,omitempty"`
}

// SessionCreateResponse is returned when creating a session.
type SessionCreateResponse struct {
	Key string `json:"key"`
}

// HealthResponse is returned by GET /api/v1/health.
type HealthResponse struct {
	Status   string `json:"status"`   // "ok" or "degraded"
	Version  string `json:"version"`
	Uptime   string `json:"uptime"`
	Active   int    `json:"activeTurns"` // currently processing turns
}

// InfoResponse is returned by GET /api/v1/info.
// Describes server capabilities so clients can adapt their UI.
type InfoResponse struct {
	Model        string   `json:"model"`
	Tools        []string `json:"tools"`
	MaxIterations int     `json:"maxIterations"`
	Vision       bool     `json:"visionSupported"`
	Version      string   `json:"version"`
}

// ErrorResponse is returned for all non-2xx responses.
type ErrorResponse struct {
	Error string `json:"error"`
	Code  string `json:"code,omitempty"`
}
