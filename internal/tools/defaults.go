package tools

import (
	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/media"
	"github.com/roelfdiedericks/goclaw/internal/memory"
	"github.com/roelfdiedericks/goclaw/internal/skills"
	"github.com/roelfdiedericks/goclaw/internal/transcript"
)

// ToolsConfig holds configuration for tools
type ToolsConfig struct {
	WorkingDir        string
	BraveAPIKey       string
	UseBrowser        string // "auto", "always", "never" for web_fetch browser fallback
	WebProfile        string // browser profile for web_fetch
	MemoryManager     *memory.Manager
	MediaStore        *media.MediaStore
	SkillsManager     *skills.Manager
	TranscriptManager *transcript.Manager
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

	// web_fetch with optional browser fallback
	reg.Register(NewWebFetchToolWithConfig(WebFetchConfig{
		UseBrowser: cfg.UseBrowser,
		Profile:    cfg.WebProfile,
	}))
	L_debug("tools: web_fetch registered", "useBrowser", cfg.UseBrowser, "profile", cfg.WebProfile)

	// Note: Browser tool is registered in main.go using browser.NewTool()

	// Memory search tools
	if cfg.MemoryManager != nil {
		reg.Register(NewMemorySearchTool(cfg.MemoryManager))
		reg.Register(NewMemoryGetTool(cfg.MemoryManager))
		L_debug("tools: memory_search and memory_get registered")
	} else {
		L_debug("tools: memory tools skipped (no manager)")
	}

	// Skills tool
	if cfg.SkillsManager != nil {
		reg.Register(NewSkillsTool(cfg.SkillsManager))
		L_debug("tools: skills registered")
	} else {
		L_debug("tools: skills skipped (no manager)")
	}

	// Cron tool (always register - it handles nil service gracefully via singleton)
	reg.Register(NewCronTool())
	L_debug("tools: cron registered")

	// Transcript search tool
	if cfg.TranscriptManager != nil {
		reg.Register(NewTranscriptTool(cfg.TranscriptManager))
		L_debug("tools: transcript registered")
	} else {
		L_debug("tools: transcript skipped (no manager)")
	}
}
