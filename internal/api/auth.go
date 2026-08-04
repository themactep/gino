package api

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"
)

// ============================================================================
// Authentication
// ============================================================================
//
// The API uses bearer token authentication. Tokens are configured in the
// server config under api.tokens (a list of valid tokens) or api.token
// (a single token). Each token maps to a user identifier for session scoping.
//
// Clients send the token in the Authorization header:
//
//	Authorization: Bearer <token>
//
// For development/testing, token auth can be disabled with api.allowAnon=true.
// In that case, all requests are attributed to user "anonymous".

// AuthConfig holds authentication settings for the API server.
type AuthConfig struct {
	// Tokens maps API tokens to user identifiers.
	// e.g., {"abc123secret": "josh", "user2token": "alice"}
	Tokens map[string]string

	// AllowAnon, when true, skips token validation and attributes all
	// requests to user "anonymous". For development only.
	AllowAnon bool
}

// authenticate extracts and validates the bearer token from the request.
// Returns the user identifier and true on success, or "" and false on failure.
func (c AuthConfig) authenticate(r *http.Request) (userID string, ok bool) {
	if c.AllowAnon {
		return "anonymous", true
	}

	auth := r.Header.Get("Authorization")
	if auth == "" {
		return "", false
	}

	// Expect "Bearer <token>"
	parts := strings.SplitN(auth, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
		return "", false
	}

	token := strings.TrimSpace(parts[1])
	if token == "" {
		return "", false
	}

	// Constant-time comparison to prevent timing attacks.
	for knownToken, uid := range c.Tokens {
		if subtle.ConstantTimeCompare([]byte(token), []byte(knownToken)) == 1 {
			return uid, true
		}
	}

	return "", false
}

// authMiddleware wraps a handler with bearer token authentication.
func (s *Server) authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid, ok := s.auth.authenticate(r)
		if !ok {
			s.writeError(w, http.StatusUnauthorized, "Missing or invalid authentication token")
			return
		}
		ctx := context.WithValue(r.Context(), userIDKey, uid)
		r = r.WithContext(ctx)
		next(w, r)
	}
}

// ─── Context helpers ─────────────────────────────────────────────────────────

type contextKey int

const (
	userIDKey contextKey = iota
	rateLimitEndKey
)

// userIDFromRequest extracts the authenticated user ID from the request context.
func userIDFromRequest(r *http.Request) string {
	if v, ok := r.Context().Value(userIDKey).(string); ok {
		return v
	}
	return ""
}
