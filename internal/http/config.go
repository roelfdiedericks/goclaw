package http

import (
	"fmt"
	"net"
	"strings"
	"sync"

	"github.com/roelfdiedericks/goclaw/internal/bus"
	"github.com/roelfdiedericks/goclaw/internal/config/forms"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// HTTPConfig holds configuration for the HTTP server.
type HTTPConfig struct {
	Enabled *bool  `json:"enabled,omitempty"` // Enable HTTP server (default: true if users have passwords)
	Listen  string `json:"listen"`            // Address to listen on (e.g., ":1337", "127.0.0.1:1337")
}

const configPath = "http"

var (
	currentListenAddr string
	currentListenMu   sync.RWMutex
)

// SetCurrentListenAddr updates the tracked listen address (called by server on start)
func SetCurrentListenAddr(addr string) {
	currentListenMu.Lock()
	defer currentListenMu.Unlock()
	currentListenAddr = addr
}

// GetCurrentListenAddr returns the currently bound listen address
func GetCurrentListenAddr() string {
	currentListenMu.RLock()
	defer currentListenMu.RUnlock()
	return currentListenAddr
}

// ConfigFormDef returns the form definition for this component's config.
func ConfigFormDef() forms.FormDef {
	return forms.FormDef{
		Title: "HTTP Server",
		Sections: []forms.Section{
			{
				Title: "Settings",
				Fields: []forms.Field{
					{Name: "Enabled", Title: "Enabled", Type: forms.Toggle, Default: true, Desc: "Enable HTTP server"},
					{Name: "Listen", Title: "Listen Address", Type: forms.Text, Default: ":1337", Desc: "Address to listen on (e.g., :1337 or 127.0.0.1:1337)"},
				},
			},
		},
		Actions: []forms.ActionDef{
			{Name: "test", Label: "Test"},
			{Name: "apply", Label: "Apply"},
		},
	}
}

// RegisterCommands registers bus commands for this component.
func RegisterCommands() {
	bus.RegisterCommand(configPath, "test", handleTest)
	bus.RegisterCommand(configPath, "apply", handleApply)
}

// UnregisterCommands unregisters bus commands for this component.
func UnregisterCommands() {
	bus.UnregisterComponent(configPath)
}

func handleTest(cmd bus.Command) bus.CommandResult {
	cfg, ok := cmd.Payload.(*HTTPConfig)
	if !ok {
		return bus.CommandResult{
			Success: false,
			Error:   fmt.Errorf("expected *http.HTTPConfig, got %T", cmd.Payload),
		}
	}

	listen := cfg.Listen
	if listen == "" {
		listen = ":1337"
	}

	// Validate address format
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return bus.CommandResult{
			Success: false,
			Message: fmt.Sprintf("Invalid address format: %v", err),
		}
	}

	// Validate host if specified
	if host != "" && host != "localhost" {
		ip := net.ParseIP(host)
		if ip == nil {
			return bus.CommandResult{
				Success: false,
				Message: fmt.Sprintf("Invalid host: %s (must be IP address or localhost)", host),
			}
		}
	}

	// Validate port
	if port == "" {
		return bus.CommandResult{
			Success: false,
			Message: "Port is required",
		}
	}

	// Check if we're already bound to this address (skip port availability check)
	currentAddr := GetCurrentListenAddr()
	if currentAddr != "" && normalizeAddr(currentAddr) == normalizeAddr(listen) {
		L_debug("http: test skipped port check (already bound)", "listen", listen)
		return bus.CommandResult{
			Success: true,
			Message: fmt.Sprintf("Address format valid (currently listening on %s)", listen),
		}
	}

	// Try to bind briefly to check availability
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		if strings.Contains(err.Error(), "address already in use") {
			return bus.CommandResult{
				Success: false,
				Message: fmt.Sprintf("Port %s is already in use by another process", port),
			}
		}
		return bus.CommandResult{
			Success: false,
			Message: fmt.Sprintf("Cannot bind to %s: %v", listen, err),
		}
	}
	_ = ln.Close()

	L_debug("http: test passed", "listen", listen)
	return bus.CommandResult{
		Success: true,
		Message: fmt.Sprintf("Address %s is valid and available", listen),
	}
}

func handleApply(cmd bus.Command) bus.CommandResult {
	cfg, ok := cmd.Payload.(*HTTPConfig)
	if !ok {
		return bus.CommandResult{
			Success: false,
			Error:   fmt.Errorf("expected *http.HTTPConfig, got %T", cmd.Payload),
		}
	}

	enabled := cfg.Enabled == nil || *cfg.Enabled
	L_info("http: config applied", "enabled", enabled, "listen", cfg.Listen)
	bus.PublishEvent(configPath+".config.applied", cfg)

	return bus.CommandResult{Success: true, Message: "Config applied"}
}

// normalizeAddr normalizes an address for comparison (handles "" vs "0.0.0.0")
func normalizeAddr(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	if host == "" || host == "0.0.0.0" {
		host = ""
	}
	return host + ":" + port
}
