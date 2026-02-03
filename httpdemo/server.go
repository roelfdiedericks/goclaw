cat server.go
package httpserver

import (
        "context"
        "fmt"
        "html/template"
        "net/http"
        "os"
        "path/filepath"
        "strings"
        "sync"
        "time"

        . "github.com/roelfdiedericks/goppp/internal/logging"
        "github.com/roelfdiedericks/goppp/internal/sessions"
)

// Server represents the HTTP server
type Server struct {
        mu           sync.RWMutex
        server       *http.Server
        templates    *template.Template
        htmlDir      string
        sessionMgr   *sessions.Manager
        shutdownChan chan struct{}
        wg           sync.WaitGroup
}

// NewServer creates a new HTTP server instance
func NewServer(listenAddr string, htmlDir string, sessionMgr *sessions.Manager) *Server {
        if listenAddr == "" {
                listenAddr = "0.0.0.0:80"
        }

        if htmlDir == "" {
                htmlDir = "./html"
        }

        // Check if HTML directory exists
        if info, err := os.Stat(htmlDir); err != nil || !info.IsDir() {
                L_warn("HTML directory %s does not exist or is not accessible: %v", htmlDir, err)
                // Try to create the directory if it doesn't exist
                if err := os.MkdirAll(htmlDir, 0755); err != nil {
                        L_error("Failed to create HTML directory %s: %v", htmlDir, err)
                } else {
                        L_info("Created HTML directory: %s", htmlDir)
                }
        } else {
                L_info("Using HTML directory: %s", htmlDir)
        }

        s := &Server{
                htmlDir:      htmlDir,
                sessionMgr:   sessionMgr,
                shutdownChan: make(chan struct{}),
        }

        // Create HTTP server
        s.server = &http.Server{
                Addr:         listenAddr,
                Handler:      s.setupRoutes(),
                ReadTimeout:  10 * time.Second,
                WriteTimeout: 10 * time.Second,
                IdleTimeout:  60 * time.Second,
        }

        return s
}

// setupRoutes configures all HTTP routes
func (s *Server) setupRoutes() http.Handler {
        mux := http.NewServeMux()

        // API routes
        mux.HandleFunc("/api/sessions.json", s.logRequest(s.handleAPISessions))
        mux.HandleFunc("/api/stats.json", s.logRequest(s.handleAPIStats))
        mux.HandleFunc("/api/action/terminate", s.logRequest(s.handleAPITerminate))
        mux.HandleFunc("/api/monitorsession", s.logRequest(s.handleAPIMonitorSession))

        // Pool statistics endpoints
        mux.HandleFunc("/api/pools/ipv4.json", s.logRequest(s.handleAPIPoolsIPv4))
        mux.HandleFunc("/api/pools/ipv6_na.json", s.logRequest(s.handleAPIPoolsIPv6NA))
        mux.HandleFunc("/api/pools/ipv6_pd.json", s.logRequest(s.handleAPIPoolsIPv6PD))
        mux.HandleFunc("/api/pools/ipv6_slaac.json", s.logRequest(s.handleAPIPoolsSLAAC))
        mux.HandleFunc("/api/pppoe_interfaces.json", s.logRequest(s.handleAPIPPPoEInterfaces))
        mux.HandleFunc("/api/physical_interfaces.json", s.logRequest(s.handleAPIPhysicalInterfaces))
        mux.HandleFunc("/api/vpp_performance.json", s.logRequest(s.handleAPIVPPPerformance))
        mux.HandleFunc("/api/advisor.json", s.logRequest(s.handleAPIAdvisor))
        mux.HandleFunc("/api/metrics.json", s.logRequest(s.handleAPIMetrics))

        // RADIUS statistics endpoint
        mux.HandleFunc("/api/radius/stats.json", s.logRequest(s.handleAPIRadiusStats))
        mux.HandleFunc("/api/vlan/stats.json", s.logRequest(s.handleAPIVLANStats))
        mux.HandleFunc("/api/l2tp/tunnels.json", s.logRequest(s.handleAPIL2TPTunnels))
        mux.HandleFunc("/api/relays.json", s.logRequest(s.handleAPIRelays))

        // Session logging endpoints
        mux.HandleFunc("/api/logging/enable", s.logRequest(s.handleAPILoggingEnable))
        mux.HandleFunc("/api/logging/disable", s.logRequest(s.handleAPILoggingDisable))
        mux.HandleFunc("/api/logging/list", s.logRequest(s.handleAPILoggingList))

        // Static file server with custom handler for templates
        mux.HandleFunc("/", s.logRequest(s.handleStaticFiles))

        return mux
}

// logRequest wraps an HTTP handler to log requests
func (s *Server) logRequest(handler http.HandlerFunc) http.HandlerFunc {
        return func(w http.ResponseWriter, r *http.Request) {
                // Create a custom response writer to capture status code
                lw := &loggingResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}

                // Call the actual handler
                handler(lw, r)

                // Log the request at debug level
                L_trace("HTTP %s %s", r.Method, r.URL.Path)
        }
}

// loggingResponseWriter wraps ResponseWriter to capture status code
type loggingResponseWriter struct {
        http.ResponseWriter
        statusCode int
}

func (lw *loggingResponseWriter) WriteHeader(code int) {
        lw.statusCode = code
        lw.ResponseWriter.WriteHeader(code)
}

// Start starts the HTTP server
func (s *Server) Start() error {
        // Load templates
        if err := s.loadTemplates(); err != nil {
                return fmt.Errorf("failed to load templates: %w", err)
        }

        // Start server in background
        s.wg.Add(1)
        go func() {
                defer s.wg.Done()
                L_info("HTTP server starting on %s", s.server.Addr)

                err := s.server.ListenAndServe()
                if err != nil && err != http.ErrServerClosed {
                        L_error("HTTP server error: %v", err)
                }
        }()

        return nil
}

// Stop gracefully shuts down the HTTP server
func (s *Server) Stop() error {
        close(s.shutdownChan)

        ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
        defer cancel()

        if err := s.server.Shutdown(ctx); err != nil {
                L_error("HTTP server shutdown error: %v", err)
                return err
        }

        s.wg.Wait()
        L_info("HTTP server stopped")
        return nil
}

// loadTemplates loads all HTML templates
func (s *Server) loadTemplates() error {
        templatePath := filepath.Join(s.htmlDir, "*.html")

        // Check if directory exists
        if _, err := os.Stat(s.htmlDir); os.IsNotExist(err) {
                L_warn("HTML directory %s does not exist", s.htmlDir)
                return nil
        }

        // Load all templates
        tmpl, err := template.ParseGlob(templatePath)
        if err != nil {
                // If no templates found, create an empty template set
                tmpl = template.New("")
                L_warn("No HTML templates found in %s", s.htmlDir)
        }

        s.templates = tmpl
        L_debug("Loaded HTML templates from %s", s.htmlDir)
        return nil
}

// handleStaticFiles serves static files and renders templates
func (s *Server) handleStaticFiles(w http.ResponseWriter, r *http.Request) {
        // Clean the path
        path := r.URL.Path
        if path == "/" {
                path = "/index.html"
        }

        // Remove leading slash
        filename := strings.TrimPrefix(path, "/")
        fullPath := filepath.Join(s.htmlDir, filename)

        L_trace("HTTP request for file: %s (full path: %s)", filename, fullPath)

        // Security check - ensure we're not serving files outside htmlDir
        fullPath, err := filepath.Abs(fullPath)
        if err != nil {
                L_error("Invalid path %s: %v", path, err)
                http.Error(w, "Invalid path", http.StatusBadRequest)
                return
        }

        absHtmlDir, _ := filepath.Abs(s.htmlDir)
        if !strings.HasPrefix(fullPath, absHtmlDir) {
                L_warn("Access denied for path outside html directory: %s", fullPath)
                http.Error(w, "Access denied", http.StatusForbidden)
                return
        }

        // Check if file exists
        info, err := os.Stat(fullPath)
        if os.IsNotExist(err) {
                L_warn("File not found: %s", fullPath)
                http.Error(w, "File not found", http.StatusNotFound)
                return
        }

        // Don't serve directories
        if info.IsDir() {
                L_warn("Attempted to serve directory: %s", fullPath)
                http.Error(w, "Access denied", http.StatusForbidden)
                return
        }

        // If it's an HTML file, parse and execute as template
        if strings.HasSuffix(filename, ".html") {
                L_debug("Serving HTML template: %s", filename)
                s.serveTemplate(w, r, filename)
                return
        }

        // Otherwise, serve as static file
        L_debug("Serving static file: %s", filename)
        http.ServeFile(w, r, fullPath)
}

// serveTemplate renders an HTML template
func (s *Server) serveTemplate(w http.ResponseWriter, r *http.Request, filename string) {
        // Check if the requested template file exists
        tmplPath := filepath.Join(s.htmlDir, filename)
        if _, err := os.Stat(tmplPath); os.IsNotExist(err) {
                L_error("Template file doesn't exist: %s", tmplPath)
                http.Error(w, "Template not found", http.StatusNotFound)
                return
        }

        // Parse ALL templates in the directory together
        // This allows any template to reference any other template without worrying about order
        pattern := filepath.Join(s.htmlDir, "*.html")
        tmpl, err := template.ParseGlob(pattern)
        if err != nil {
                L_error("Failed to parse templates: %v", err)
                http.Error(w, "Template error", http.StatusInternalServerError)
                return
        }

        // Prepare template data
        data := struct {
                Title        string
                Timestamp    time.Time
                SessionCount int
        }{
                Title:        "GoPPP",
                Timestamp:    time.Now(),
                SessionCount: s.sessionMgr.GetActiveSessionCount(),
        }

        L_debug("Executing template %s with data: SessionCount=%d", filename, data.SessionCount)
        L_debug("Available templates: %v", tmpl.DefinedTemplates())

        // Execute the specific template by its base name
        // Since we parsed all files, we need to execute by the specific template name
        // For files without {{define}}, the template name is the base filename
        w.Header().Set("Content-Type", "text/html; charset=utf-8")

        // Try to execute as the base filename first (for files without {{define}})
        baseName := filepath.Base(tmplPath)
        if err := tmpl.ExecuteTemplate(w, baseName, data); err != nil {
                // If that fails, try executing the default template (for backward compatibility)
                if err := tmpl.Execute(w, data); err != nil {
                        L_error("Failed to execute template %s: %v", filename, err)
                        // Try to write error to response if headers haven't been sent
                        if !isHeaderWritten(w) {
                                http.Error(w, "Template execution error", http.StatusInternalServerError)
                        }
                } else {
                        L_debug("Successfully served template %s (as default)", filename)
                }
        } else {
                L_debug("Successfully served template %s", filename)
        }
}

// isHeaderWritten checks if response headers have been written
func isHeaderWritten(w http.ResponseWriter) bool {
        // This is a bit of a hack, but works for our logging response writer
        if lw, ok := w.(*loggingResponseWriter); ok {
                return lw.statusCode != http.StatusOK
        }
        return false
}

