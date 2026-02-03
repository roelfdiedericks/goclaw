package http

import (
	"net/http"
	"sync"
	"time"
)

// RateLimiter tracks failed auth attempts and blocks IPs temporarily
type RateLimiter struct {
	failures map[string]time.Time // IP -> time of last failure
	mu       sync.RWMutex
	delay    time.Duration // How long to block after failure
}

// NewRateLimiter creates a new rate limiter
func NewRateLimiter(delay time.Duration) *RateLimiter {
	return &RateLimiter{
		failures: make(map[string]time.Time),
		delay:    delay,
	}
}

// RecordFailure records a failed auth attempt for an IP
func (r *RateLimiter) RecordFailure(ip string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.failures[ip] = time.Now()
}

// ClearFailure clears the failure record for an IP (on successful auth)
func (r *RateLimiter) ClearFailure(ip string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.failures, ip)
}

// IsLimited returns true if the IP is currently rate limited
func (r *RateLimiter) IsLimited(ip string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	failTime, exists := r.failures[ip]
	if !exists {
		return false
	}

	// Check if delay has passed
	if time.Since(failTime) > r.delay {
		return false
	}

	return true
}

// rateLimit middleware applies rate limiting
func (s *Server) rateLimit(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Rate limiting is handled in basicAuth middleware
		// This middleware is a placeholder for future enhancements
		handler(w, r)
	}
}
