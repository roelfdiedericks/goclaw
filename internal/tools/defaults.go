package tools

import (
	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/media"
	"github.com/roelfdiedericks/goclaw/internal/memory"
)

// ToolsConfig holds configuration for tools
type ToolsConfig struct {
	WorkingDir     string
	BraveAPIKey    string
	BrowserPool    *BrowserPool
	BrowserEnabled bool
	MemoryManager  *memory.Manager
	MediaStore     *media.MediaStore
}

// RegisterDefaults registers the default set of tools
func RegisterDefaults(reg *Registry, cfg ToolsConfig) {
	// File tools
	reg.Register(NewReadTool(cfg.WorkingDir))
	reg.Register(NewWriteTool(cfg.WorkingDir))
	reg.Register(NewEditTool(cfg.WorkingDir))

	// Exec tool
	reg.Register(NewExecTool(cfg.WorkingDir))

	// Web tools
	if cfg.BraveAPIKey != "" {
		reg.Register(NewWebSearchTool(cfg.BraveAPIKey))
		L_debug("tools: web_search registered")
	} else {
		L_debug("tools: web_search skipped (no Brave API key)")
	}

	// web_fetch doesn't need an API key
	reg.Register(NewWebFetchTool())
	L_debug("tools: web_fetch registered")

	// Browser tool (headless browser for JS-rendered pages)
	if cfg.BrowserEnabled && cfg.BrowserPool != nil && cfg.MediaStore != nil {
		reg.Register(NewBrowserTool(cfg.BrowserPool, cfg.MediaStore))
		L_debug("tools: browser registered")
	} else {
		L_debug("tools: browser skipped (not enabled, no pool, or no media store)")
	}

	// Memory search tools
	if cfg.MemoryManager != nil {
		reg.Register(NewMemorySearchTool(cfg.MemoryManager))
		reg.Register(NewMemoryGetTool(cfg.MemoryManager))
		L_debug("tools: memory_search and memory_get registered")
	} else {
		L_debug("tools: memory tools skipped (no manager)")
	}
}
