package tools

import (
	"time"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/media"
	"github.com/roelfdiedericks/goclaw/internal/memory"
	"github.com/roelfdiedericks/goclaw/internal/skills"
	toolcron "github.com/roelfdiedericks/goclaw/internal/tools/cron"
	"github.com/roelfdiedericks/goclaw/internal/tools/edit"
	"github.com/roelfdiedericks/goclaw/internal/tools/exec"
	"github.com/roelfdiedericks/goclaw/internal/tools/jq"
	"github.com/roelfdiedericks/goclaw/internal/tools/memoryget"
	"github.com/roelfdiedericks/goclaw/internal/tools/memorysearch"
	"github.com/roelfdiedericks/goclaw/internal/tools/read"
	toolskills "github.com/roelfdiedericks/goclaw/internal/tools/skills"
	tooltranscript "github.com/roelfdiedericks/goclaw/internal/tools/transcript"
	"github.com/roelfdiedericks/goclaw/internal/tools/webfetch"
	"github.com/roelfdiedericks/goclaw/internal/tools/websearch"
	"github.com/roelfdiedericks/goclaw/internal/tools/write"
	"github.com/roelfdiedericks/goclaw/internal/transcript"
)

// ToolsConfig holds configuration for tools
type ToolsConfig struct {
	WorkingDir        string
	BraveAPIKey       string
	UseBrowser        string // "auto", "always", "never" for web_fetch browser fallback
	WebProfile        string // browser profile for web_fetch
	WebHeadless       bool   // run web_fetch browser in headless mode (default: true)
	MemoryManager     *memory.Manager
	MediaStore        *media.MediaStore
	SkillsManager     *skills.Manager
	TranscriptManager *transcript.Manager

	// Exec tool configuration
	ExecTimeout    int                   // Timeout in seconds (default: 1800 = 30 min)
	ExecBubblewrap exec.BubblewrapConfig // Bubblewrap sandbox settings
	BubblewrapPath string                // Global path to bwrap binary
}

// RegisterDefaults registers the default set of tools
func RegisterDefaults(reg *Registry, cfg ToolsConfig) {
	// File tools
	reg.Register(read.NewTool(cfg.WorkingDir))
	reg.Register(write.NewTool(cfg.WorkingDir))
	reg.Register(edit.NewTool(cfg.WorkingDir))

	// Create shared exec runner for exec and jq tools
	timeout := 30 * time.Minute // default: 30 minutes
	if cfg.ExecTimeout > 0 {
		timeout = time.Duration(cfg.ExecTimeout) * time.Second
	}
	execRunner := exec.NewRunner(exec.RunnerConfig{
		WorkingDir:     cfg.WorkingDir,
		Timeout:        timeout,
		BubblewrapPath: cfg.BubblewrapPath,
		Bubblewrap:     cfg.ExecBubblewrap,
	})

	// Exec tool
	reg.Register(exec.NewToolWithRunner(execRunner))
	L_debug("tools: exec registered")

	// JQ tool (shares exec runner for sandbox support)
	reg.Register(jq.NewTool(cfg.WorkingDir, execRunner))
	L_debug("tools: jq registered")

	// Web tools
	if cfg.BraveAPIKey != "" {
		reg.Register(websearch.NewTool(cfg.BraveAPIKey))
		L_debug("tools: web_search registered")
	} else {
		L_debug("tools: web_search skipped (no Brave API key)")
	}

	// web_fetch with optional browser fallback
	reg.Register(webfetch.NewToolWithConfig(webfetch.ToolConfig{
		UseBrowser: cfg.UseBrowser,
		Profile:    cfg.WebProfile,
		Headless:   cfg.WebHeadless,
	}))
	L_debug("tools: web_fetch registered", "useBrowser", cfg.UseBrowser, "profile", cfg.WebProfile, "headless", cfg.WebHeadless)

	// Note: Browser tool is registered in main.go using browser.NewTool()

	// Memory search tools
	if cfg.MemoryManager != nil {
		reg.Register(memorysearch.NewTool(cfg.MemoryManager))
		reg.Register(memoryget.NewTool(cfg.MemoryManager))
		L_debug("tools: memory_search and memory_get registered")
	} else {
		L_debug("tools: memory tools skipped (no manager)")
	}

	// Skills tool
	if cfg.SkillsManager != nil {
		reg.Register(toolskills.NewTool(cfg.SkillsManager))
		L_debug("tools: skills registered")
	} else {
		L_debug("tools: skills skipped (no manager)")
	}

	// Cron tool (always register - it handles nil service gracefully via singleton)
	reg.Register(toolcron.NewTool())
	L_debug("tools: cron registered")

	// Transcript search tool
	if cfg.TranscriptManager != nil {
		reg.Register(tooltranscript.NewTool(cfg.TranscriptManager))
		L_debug("tools: transcript registered")
	} else {
		L_debug("tools: transcript skipped (no manager)")
	}
}
