package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/creasty/defaults"
	"github.com/roelfdiedericks/goclaw/internal/auth"
	httpconfig "github.com/roelfdiedericks/goclaw/internal/channels/http/config"
	telegramconfig "github.com/roelfdiedericks/goclaw/internal/channels/telegram/config"
	tuiconfig "github.com/roelfdiedericks/goclaw/internal/channels/tui/config"
	whatsappconfig "github.com/roelfdiedericks/goclaw/internal/channels/whatsapp/config"
	"github.com/roelfdiedericks/goclaw/internal/cron"
	gwtypes "github.com/roelfdiedericks/goclaw/internal/gateway/types"
	"github.com/roelfdiedericks/goclaw/internal/hass"
	"github.com/roelfdiedericks/goclaw/internal/llm"
	"github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/media"
	"github.com/roelfdiedericks/goclaw/internal/memory"
	"github.com/roelfdiedericks/goclaw/internal/memorygraph"
	"github.com/roelfdiedericks/goclaw/internal/paths"
	"github.com/roelfdiedericks/goclaw/internal/sandbox"
	"github.com/roelfdiedericks/goclaw/internal/session"
	"github.com/roelfdiedericks/goclaw/internal/skills"
	"github.com/roelfdiedericks/goclaw/internal/stt"
	toolsconfig "github.com/roelfdiedericks/goclaw/internal/tools/config"
	"github.com/roelfdiedericks/goclaw/internal/transcript"
	"github.com/roelfdiedericks/goclaw/internal/user"
)

// LoadResult contains the loaded config and metadata about where it came from
type LoadResult struct {
	Config     *Config
	SourcePath string // Path to goclaw.json that was loaded
}

// isMinimalJSON checks if JSON content is essentially empty (just {} or whitespace)
// Returns false for parse errors so we can give better error messages later
func isMinimalJSON(data []byte) bool {
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		return false // Parse error - let Load() handle it with better error message
	}
	return len(m) == 0
}

// formatJSONError enhances JSON parsing errors with line/column info and context
func formatJSONError(data []byte, err error) error {
	if err == nil {
		return nil
	}

	// Check if it's a syntax error with offset
	if syntaxErr, ok := err.(*json.SyntaxError); ok {
		return formatJSONSyntaxError(data, syntaxErr)
	}

	// Check for unmarshal type errors
	if typeErr, ok := err.(*json.UnmarshalTypeError); ok {
		line, col := offsetToLineCol(data, typeErr.Offset)
		return fmt.Errorf("JSON type error at line %d, column %d: expected %s but got %s for field '%s'",
			line, col, typeErr.Type, typeErr.Value, typeErr.Field)
	}

	return err
}

// formatJSONSyntaxError creates a detailed error message for JSON syntax errors
func formatJSONSyntaxError(data []byte, syntaxErr *json.SyntaxError) error {
	line, col := offsetToLineCol(data, syntaxErr.Offset)

	// Get the problematic line for context
	lines := splitLines(data)
	var context string
	if line > 0 && line <= len(lines) {
		problemLine := lines[line-1]
		// Truncate very long lines
		if len(problemLine) > 80 {
			if col > 40 {
				start := col - 40
				problemLine = "..." + problemLine[start:]
				col = 43 // Adjust for "..."
			}
			if len(problemLine) > 80 {
				problemLine = problemLine[:77] + "..."
			}
		}
		// Build pointer line
		pointer := ""
		for i := 0; i < col-1 && i < len(problemLine); i++ {
			if problemLine[i] == '\t' {
				pointer += "\t"
			} else {
				pointer += " "
			}
		}
		pointer += "^"
		context = fmt.Sprintf("\n  %s\n  %s", problemLine, pointer)
	}

	return fmt.Errorf("JSON syntax error at line %d, column %d: %s%s",
		line, col, syntaxErr.Error(), context)
}

// offsetToLineCol converts a byte offset to line and column numbers (1-indexed)
func offsetToLineCol(data []byte, offset int64) (line, col int) {
	line = 1
	col = 1
	for i := int64(0); i < offset && i < int64(len(data)); i++ {
		if data[i] == '\n' {
			line++
			col = 1
		} else {
			col++
		}
	}
	return line, col
}

// splitLines splits data into lines, preserving empty lines
func splitLines(data []byte) []string {
	var lines []string
	start := 0
	for i := 0; i < len(data); i++ {
		if data[i] == '\n' {
			lines = append(lines, string(data[start:i]))
			start = i + 1
		}
	}
	// Don't forget the last line if it doesn't end with newline
	if start < len(data) {
		lines = append(lines, string(data[start:]))
	}
	return lines
}

// ChannelsConfig aggregates all channel configurations.
// This lives in config package to avoid import cycles (channel packages import gateway).
type ChannelsConfig struct {
	Telegram telegramconfig.Config `json:"telegram"`
	WhatsApp whatsappconfig.Config `json:"whatsapp"`
	HTTP     httpconfig.Config     `json:"http"`
	TUI      tuiconfig.Config      `json:"tui"`
}

// Config represents the merged goclaw configuration
type Config struct {
	Gateway       gwtypes.GatewayConfig       `json:"gateway"`
	Agent         gwtypes.AgentIdentityConfig `json:"agent"`
	LLM           llm.LLMConfig               `json:"llm"`
	HomeAssistant hass.HomeAssistantConfig    `json:"homeassistant"` // Top-level Home Assistant config
	Tools         toolsconfig.ToolsConfig     `json:"tools"`
	Channels      ChannelsConfig              `json:"channels"` // All channel configs (telegram, http, tui)
	Session       session.SessionConfig       `json:"session"`
	Memory        memory.MemorySearchConfig   `json:"memory"`
	MemoryGraph   memorygraph.Config          `json:"memoryGraph"`
	Transcript    transcript.TranscriptConfig `json:"transcript"`
	PromptCache   gwtypes.PromptCacheConfig   `json:"promptCache"`
	Media         media.MediaConfig           `json:"media"`
	STT           stt.Config                  `json:"stt"`
	Skills        skills.SkillsConfig         `json:"skills"`
	Cron          cron.CronConfig             `json:"cron"`
	Supervision   gwtypes.SupervisionConfig   `json:"supervision"`
	Roles         user.RolesConfig            `json:"roles"`    // Role-based access control
	Auth          auth.AuthConfig             `json:"auth"`     // Role elevation authentication
	Sandbox       sandbox.Config               `json:"sandbox"`   // Sandbox and bubblewrap configuration
	Safety        gwtypes.SafetyConfig        `json:"safety"`    // Emergency stop / panic phrase config
	Security      gwtypes.SecurityConfig      `json:"security"`  // Security policies (tool restrictions per purpose)
}

// Load reads configuration from goclaw.json.
// If no config file exists, returns an error directing user to run 'goclaw setup'.
func Load() (*LoadResult, error) {
	home, _ := os.UserHomeDir()
	goclawDir, _ := paths.BaseDir()
	goclawGlobalPath, _ := paths.DefaultConfigPath()
	goclawLocalPath := "goclaw.json" // current working directory

	logging.L_debug("config: checking files", "goclawDir", goclawDir, "cwd", mustGetwd())

	// Determine which goclaw.json to use (local takes priority)
	var goclawPath string
	var goclawData []byte
	var goclawExists bool

	if data, err := os.ReadFile(goclawLocalPath); err == nil {
		absPath, _ := filepath.Abs(goclawLocalPath)
		goclawPath = absPath
		goclawData = data
		goclawExists = true
		logging.L_debug("config: found local goclaw.json", "path", absPath, "size", len(data))
	} else if data, err := os.ReadFile(goclawGlobalPath); err == nil {
		goclawPath = goclawGlobalPath
		goclawData = data
		goclawExists = true
		logging.L_debug("config: found global goclaw.json", "path", goclawGlobalPath, "size", len(data))
	}

	// No config found - tell user to run setup
	if !goclawExists {
		return nil, fmt.Errorf("no goclaw.json configuration found. Run 'goclaw setup' to create one")
	}

	// Check for minimal/empty config
	if isMinimalJSON(goclawData) {
		return nil, fmt.Errorf("goclaw.json is empty or incomplete. Run 'goclaw setup' to configure")
	}

	logging.L_debug("config: loading from goclaw.json")

	// Initialize config with struct tag defaults
	cfg := &Config{}
	if err := defaults.Set(cfg); err != nil {
		return nil, fmt.Errorf("failed to set config defaults: %w", err)
	}

	// Unmarshal JSON - only overwrites fields actually present in the JSON
	if err := json.Unmarshal(goclawData, cfg); err != nil {
		logging.L_error("config: failed to parse goclaw.json", "path", goclawPath, "error", err)
		return nil, formatJSONError(goclawData, err)
	}
	logging.L_debug("config: loaded from goclaw.json", "path", goclawPath)

	// Apply runtime defaults that cannot be struct tags (paths, slices, maps)
	applyRuntimeDefaults(cfg, goclawDir, home)

	// Log final config summary
	agentModel := ""
	if len(cfg.LLM.Agent.Models) > 0 {
		agentModel = cfg.LLM.Agent.Models[0]
	}
	logging.L_debug("config: loaded",
		"agentModel", agentModel,
		"providers", len(cfg.LLM.Providers),
		"telegramEnabled", cfg.Channels.Telegram.Enabled,
		"workingDir", cfg.Gateway.WorkingDir,
	)

	return &LoadResult{
		Config:     cfg,
		SourcePath: goclawPath,
	}, nil
}

// applyRuntimeDefaults sets defaults that cannot be expressed as struct tags
// (file paths, slices, maps, function-derived values)
func applyRuntimeDefaults(cfg *Config, goclawDir, home string) {
	// Path defaults
	if cfg.Gateway.LogFile == "" {
		cfg.Gateway.LogFile = filepath.Join(goclawDir, "goclaw.log")
	}
	if cfg.Gateway.PIDFile == "" {
		cfg.Gateway.PIDFile = filepath.Join(goclawDir, "goclaw.pid")
	}
	if cfg.Gateway.WorkingDir == "" {
		cfg.Gateway.WorkingDir = filepath.Join(goclawDir, "workspace")
	}
	if cfg.Session.StorePath == "" {
		cfg.Session.StorePath = filepath.Join(goclawDir, "sessions.db")
	}
	if cfg.Session.InheritPath == "" {
		cfg.Session.InheritPath = filepath.Join(home, ".openclaw", "agents", "main", "sessions")
	}

	// Map defaults
	if cfg.LLM.Providers == nil {
		cfg.LLM.Providers = map[string]llm.LLMProviderConfig{
			"anthropic": {
				Driver:        "anthropic",
				PromptCaching: true,
			},
		}
	}
	if cfg.LLM.Agent.Models == nil {
		cfg.LLM.Agent.Models = []string{"anthropic/claude-sonnet-4-20250514"}
	}
	if cfg.Tools.Browser.ProfileDomains == nil {
		cfg.Tools.Browser.ProfileDomains = map[string]string{}
	}
	if cfg.Skills.Entries == nil {
		cfg.Skills.Entries = make(map[string]skills.SkillEntryConfig)
	}

	// Slice defaults
	if cfg.Sandbox.Bubblewrap.Volumes == nil {
		cfg.Sandbox.Bubblewrap.Volumes = sandbox.DefaultVolumes()
	}
	if cfg.MemoryGraph.LiveExtraction.ExcludeSources == nil {
		cfg.MemoryGraph.LiveExtraction.ExcludeSources = memorygraph.DefaultExcludeSources()
	}
	if cfg.Session.Summarization.Checkpoint.Thresholds == nil {
		cfg.Session.Summarization.Checkpoint.Thresholds = []int{25, 50, 75}
	}
	if cfg.Session.MemoryFlush.Thresholds == nil {
		cfg.Session.MemoryFlush.Thresholds = []session.FlushThreshold{
			{
				Percent:      50,
				Prompt:       "Context at 50%. Consider noting key decisions to memory.",
				InjectAs:     session.FlushInjectSystem,
				OncePerCycle: true,
			},
			{
				Percent:      75,
				Prompt:       "Context at 75%. Write important context to memory/YYYY-MM-DD.md now.",
				InjectAs:     session.FlushInjectSystem,
				OncePerCycle: true,
			},
			{
				Percent:      90,
				Prompt:       "[Context pressure: 90%] Compaction imminent.\nBefore responding, save important session context to memory/YYYY-MM-DD.md (create memory/ if needed).\nSave: key decisions, user-shared context, current work state.\nSkip: secrets, trivial details, info already in files.\nAfter saving (or if nothing to save), respond to the user's message normally.",
				InjectAs:     session.FlushInjectSystem,
				OncePerCycle: true,
			},
		}
	}
}

// DefaultConfigTemplate is a minimal config struct for template generation.
// Only includes fields that users typically need to customize.
// The full defaults are applied by Load() when merging.
type DefaultConfigTemplate struct {
	LLM      DefaultLLMTemplate      `json:"llm"`
	Gateway  DefaultGatewayTemplate  `json:"gateway,omitempty"`
	Channels DefaultChannelsTemplate `json:"channels,omitempty"`
	Roles    user.RolesConfig        `json:"roles,omitempty"`
}

type DefaultLLMTemplate struct {
	Providers map[string]llm.LLMProviderConfig `json:"providers"`
	Agent     llm.LLMPurposeConfig             `json:"agent"`
}

type DefaultGatewayTemplate struct {
	WorkingDir string `json:"workingDir,omitempty"`
}

type DefaultChannelsTemplate struct {
	HTTP DefaultHTTPTemplate `json:"http,omitempty"`
}

type DefaultHTTPTemplate struct {
	Listen string `json:"listen,omitempty"`
}

// DefaultConfig returns a minimal config template with sensible defaults.
// Only includes fields that users typically need to customize.
// The apiKey field has a placeholder that must be replaced.
func DefaultConfig() *DefaultConfigTemplate {
	return &DefaultConfigTemplate{
		LLM: DefaultLLMTemplate{
			Providers: map[string]llm.LLMProviderConfig{
				"anthropic": {
					Driver:        "anthropic",
					APIKey:        "YOUR_ANTHROPIC_API_KEY",
					PromptCaching: true,
				},
			},
			Agent: llm.LLMPurposeConfig{
				Models: []string{"anthropic/claude-sonnet-4-20250514"},
			},
		},
		Gateway: DefaultGatewayTemplate{
			WorkingDir: "~/.goclaw/workspace",
		},
		Channels: DefaultChannelsTemplate{
			HTTP: DefaultHTTPTemplate{
				Listen: ":1337",
			},
		},
		Roles: user.RolesConfig{
			"owner": user.RoleConfig{
				Tools:       "*",
				Skills:      "*",
				Memory:      "full",
				Transcripts: "all",
				Commands:    true,
			},
			"user": user.RoleConfig{
				Tools:       []interface{}{"read_file", "write_file", "web_search", "web_fetch"},
				Skills:      "*",
				Memory:      "none",
				Transcripts: "own",
				Commands:    true,
			},
		},
	}
}

// mustGetwd returns the current working directory or "unknown" on error
func mustGetwd() string {
	if cwd, err := os.Getwd(); err == nil {
		return cwd
	}
	return "unknown"
}

