// Package web provides browser-based setup wizard and configuration editor
package web

import (
	"fmt"
	"net/http"

	"github.com/roelfdiedericks/goclaw/internal/configapply"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/paths"
	"github.com/roelfdiedericks/goclaw/internal/user"
)

// HTTPServer interface for integration with main HTTP server
type HTTPServer interface {
	Mux() *http.ServeMux
	WrapHandler(h http.HandlerFunc) http.HandlerFunc
	Users() *user.Registry
}

// RegisterSetupRoutes registers setup wizard and editor routes on the main HTTP server
func RegisterSetupRoutes(srv HTTPServer, configPath string, a2aRuntime A2ARuntimeProvider) error {
	L_info("web: registering setup routes")
	if err := ValidateAllSectionContractsStrict(); err != nil {
		L_error("web: strict contract validation failed", "error", err)
		return fmt.Errorf("setup contract validation failed: %w", err)
	}

	// Create handlers (setupMode = false since integrated with main server)
	handlers, err := NewHandlers(false, a2aRuntime != nil)
	if err != nil {
		return err
	}

	mux := srv.Mux()

	// Owner-only middleware wrapper
	ownerOnly := func(h http.HandlerFunc) http.HandlerFunc {
		return srv.WrapHandler(func(w http.ResponseWriter, r *http.Request) {
			// Get user from context (set by auth middleware)
			u := getUserFromRequest(r, srv.Users())
			if u == nil || !u.IsOwner() {
				http.Error(w, "Owner access required", http.StatusForbidden)
				return
			}
			h(w, r)
		})
	}

	mountSetup(mux, mountOptions{
		configPath:  configPath,
		handlers:    handlers,
		wrap:        ownerOnly,
		applyCaller: configapply.CallerWebIntegrated,
		a2aRuntime:  a2aRuntime,
	})

	L_info("web: setup routes registered")
	return nil
}

// getUserFromRequest extracts the authenticated user from the request context
// This assumes the auth middleware has already run and set the user
func getUserFromRequest(r *http.Request, users *user.Registry) *user.User {
	// The basic auth middleware sets the username in the request context
	// We need to look up the user by username
	username, _, ok := r.BasicAuth()
	if !ok {
		return nil
	}
	return users.Get(username)
}

// DefaultConfigPath returns the default config path
func DefaultConfigPath() string {
	path, _ := paths.DefaultConfigPath()
	return path
}
