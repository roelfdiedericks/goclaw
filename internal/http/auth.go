package http

import (
	"context"
	"net/http"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/user"
)

// contextKey is used for storing values in request context
type contextKey string

const userContextKey contextKey = "user"

// basicAuth middleware enforces HTTP Basic Authentication
func (s *Server) basicAuth(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Get client IP for rate limiting
		clientIP := getClientIP(r)

		// Check if rate limited
		if s.rateLimiter.IsLimited(clientIP) {
			L_warn("http: rate limited", "ip", clientIP)
			w.Header().Set("WWW-Authenticate", `Basic realm="GoClaw"`)
			http.Error(w, "Too many failed attempts. Try again later.", http.StatusTooManyRequests)
			return
		}

		// Parse Basic Auth
		username, password, ok := r.BasicAuth()
		if !ok {
			w.Header().Set("WWW-Authenticate", `Basic realm="GoClaw"`)
			http.Error(w, "Authentication required", http.StatusUnauthorized)
			return
		}

		// Look up user
		u := s.users.Get(username)
		if u == nil {
			s.rateLimiter.RecordFailure(clientIP)
			L_warn("http: auth failed - user not found", "username", username, "ip", clientIP)
			w.Header().Set("WWW-Authenticate", `Basic realm="GoClaw"`)
			http.Error(w, "Invalid credentials", http.StatusUnauthorized)
			return
		}

		// Verify password
		if !u.VerifyHTTPPassword(password) {
			s.rateLimiter.RecordFailure(clientIP)
			L_warn("http: auth failed - bad password", "username", username, "ip", clientIP)
			w.Header().Set("WWW-Authenticate", `Basic realm="GoClaw"`)
			http.Error(w, "Invalid credentials", http.StatusUnauthorized)
			return
		}

		// Success - clear any rate limit
		s.rateLimiter.ClearFailure(clientIP)

		L_debug("http: auth success", "username", username, "ip", clientIP)

		// Store user in request context
		ctx := r.Context()
		ctx = setUserInContext(ctx, u)
		r = r.WithContext(ctx)

		handler(w, r)
	}
}

// getClientIP extracts the client IP from the request
func getClientIP(r *http.Request) string {
	// Check X-Forwarded-For first (if behind reverse proxy)
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// Take the first IP in the list
		return xff
	}
	// Check X-Real-IP
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return xri
	}
	// Fall back to RemoteAddr
	return r.RemoteAddr
}

// getUserFromContext retrieves the authenticated user from request context
func getUserFromContext(r *http.Request) *user.User {
	if u, ok := r.Context().Value(userContextKey).(*user.User); ok {
		return u
	}
	return nil
}

// setUserInContext stores the user in the context
func setUserInContext(ctx context.Context, u *user.User) context.Context {
	return context.WithValue(ctx, userContextKey, u)
}
