// Package web provides browser-based setup wizard and configuration editor
package web

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/roelfdiedericks/goclaw/internal/configapply"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/webview"
)

// Server is the ephemeral HTTP server for standalone setup wizard
type Server struct {
	listener   net.Listener
	httpServer *http.Server
	done       chan struct{}
	once       sync.Once
}

// NewServer creates a new ephemeral setup server
func NewServer(configPath string) (*Server, error) {
	if err := ValidateAllSectionContractsStrict(); err != nil {
		L_error("web: strict contract validation failed", "error", err)
		return nil, fmt.Errorf("setup contract validation failed: %w", err)
	}

	// Bind to localhost only for security
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("failed to bind: %w", err)
	}

	mux := http.NewServeMux()

	// Setup handlers (setupMode = true)
	handlers, err := NewHandlers(true, false)
	if err != nil {
		if closeErr := listener.Close(); closeErr != nil {
			L_warn("web: failed to close listener after handler init error", "error", closeErr)
		}
		return nil, fmt.Errorf("failed to create handlers: %w", err)
	}

	// Shutdown endpoint
	done := make(chan struct{})
	shutdownHandler := func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, APIResponse{
			Success: true,
			Message: "Shutting down",
		})
		go func() {
			time.Sleep(100 * time.Millisecond)
			close(done)
		}()
	}

	mountSetup(mux, mountOptions{
		configPath:     configPath,
		handlers:       handlers,
		applyCaller:    configapply.CallerWebStandalone,
		enableShutdown: true,
		shutdown:       shutdownHandler,
	})

	srv := &Server{
		listener: listener,
		httpServer: &http.Server{
			Handler:           mux,
			ReadHeaderTimeout: 5 * time.Second,
		},
		done: done,
	}

	return srv, nil
}

// URL returns the server URL
func (s *Server) URL() string {
	return fmt.Sprintf("http://%s", s.listener.Addr().String())
}

// Start begins serving requests
func (s *Server) Start() error {
	L_info("web: starting ephemeral server", "url", s.URL())
	return s.httpServer.Serve(s.listener)
}

// WaitForCompletion blocks until the server is signaled to shut down
func (s *Server) WaitForCompletion() {
	<-s.done
}

// Shutdown gracefully shuts down the server
func (s *Server) Shutdown(ctx context.Context) error {
	var err error
	s.once.Do(func() {
		err = s.httpServer.Shutdown(ctx)
	})
	return err
}

// ErrNoUIAvailable is returned when neither webview nor browser is available
var ErrNoUIAvailable = fmt.Errorf("no UI available")

// RunWebWizard runs the browser-based setup wizard
// Returns ErrNoUIAvailable if neither webview nor browser works
func RunWebWizard(configPath string) error {
	return RunWebWizardWithOptions(configPath, false)
}

// RunWebWizardWithOptions runs the browser-based setup wizard with options
func RunWebWizardWithOptions(configPath string, devMode bool) error {
	srv, err := NewServer(configPath)
	if err != nil {
		return err
	}

	url := srv.URL() + "/setup/wizard"

	// Channel to signal server is ready
	serverReady := make(chan struct{})

	// Start server in goroutine
	go func() {
		close(serverReady)
		if err := srv.Start(); err != http.ErrServerClosed {
			L_error("web: server error", "error", err)
		}
	}()

	// Try webview first, fall back to browser
	usedWebview, err := webview.Open(url, webview.Options{
		Title:   "GoClaw Setup Wizard",
		Width:   1100,
		Height:  800,
		DevMode: devMode,
		OnReady: serverReady,
	})

	if err != nil {
		if shutdownErr := srv.Shutdown(context.Background()); shutdownErr != nil {
			L_warn("web: shutdown after failed UI launch", "error", shutdownErr)
		}
		return ErrNoUIAvailable
	}

	if usedWebview {
		// Webview closed - we're done
		if shutdownErr := srv.Shutdown(context.Background()); shutdownErr != nil {
			L_warn("web: shutdown after webview close", "error", shutdownErr)
		}
		fmt.Println("\nSetup complete! Start GoClaw with:")
		fmt.Println("  goclaw start       (recommended - runs with supervisor)")
		fmt.Println("  goclaw gateway     (runs in foreground)")
		return nil
	}

	// Browser was opened - need to wait
	fmt.Printf("\nSetup wizard available at: %s\n", url)
	fmt.Println("Setup wizard opened in your browser.")
	fmt.Println("Press Ctrl+C when done, or close this terminal.")

	// Wait for user to complete wizard
	srv.WaitForCompletion()

	// Shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if shutdownErr := srv.Shutdown(ctx); shutdownErr != nil {
		L_warn("web: graceful shutdown after wizard completion failed", "error", shutdownErr)
	}

	fmt.Println("\nSetup complete! Start GoClaw with:")
	fmt.Println("  goclaw start       (recommended - runs with supervisor)")
	fmt.Println("  goclaw gateway     (runs in foreground)")

	return nil
}

// RunWebEditor runs the browser-based config editor
// Returns ErrNoUIAvailable if neither webview nor browser works
func RunWebEditor(configPath string) error {
	return RunWebEditorWithOptions(configPath, false)
}

// RunWebEditorWithOptions runs the browser-based config editor with options
func RunWebEditorWithOptions(configPath string, devMode bool) error {
	srv, err := NewServer(configPath)
	if err != nil {
		return err
	}

	url := srv.URL() + "/setup/edit"

	// Channel to signal server is ready
	serverReady := make(chan struct{})

	// Start server in goroutine
	go func() {
		close(serverReady)
		if err := srv.Start(); err != http.ErrServerClosed {
			L_error("web: server error", "error", err)
		}
	}()

	// Try webview first, fall back to browser
	usedWebview, err := webview.Open(url, webview.Options{
		Title:   "GoClaw Configuration",
		Width:   1100,
		Height:  800,
		DevMode: devMode,
		OnReady: serverReady,
	})

	if err != nil {
		if shutdownErr := srv.Shutdown(context.Background()); shutdownErr != nil {
			L_warn("web: shutdown after failed editor UI launch", "error", shutdownErr)
		}
		return ErrNoUIAvailable
	}

	if usedWebview {
		// Webview closed - we're done
		if shutdownErr := srv.Shutdown(context.Background()); shutdownErr != nil {
			L_warn("web: shutdown after editor webview close", "error", shutdownErr)
		}
		fmt.Println("\nConfiguration editor closed.")
		return nil
	}

	// Browser was opened - need to wait for Ctrl+C
	fmt.Printf("\nConfiguration editor available at: %s\n", url)
	fmt.Println("Configuration editor opened in your browser.")
	fmt.Println("Press Ctrl+C when done.")

	// Wait for signal (Ctrl+C)
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	<-sigCh

	// Shutdown
	fmt.Println("\nShutting down editor...")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if shutdownErr := srv.Shutdown(ctx); shutdownErr != nil {
		L_warn("web: graceful shutdown after editor completion failed", "error", shutdownErr)
	}

	return nil
}
