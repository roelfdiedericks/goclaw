package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"dario.cat/mergo"
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
		return false // Parse error - let mergeJSONConfig handle it with better error message
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

	// Build defaults
	cfg := &Config{
		Gateway: gwtypes.GatewayConfig{
			LogFile:    filepath.Join(goclawDir, "goclaw.log"),
			PIDFile:    filepath.Join(goclawDir, "goclaw.pid"),
			WorkingDir: filepath.Join(goclawDir, "workspace"),
		},
		Agent: gwtypes.AgentIdentityConfig{
			Name:  "GoClaw",
			Emoji: "",
		},
		LLM: llm.LLMConfig{
			Providers: map[string]llm.LLMProviderConfig{
				"anthropic": {
					Driver:        "anthropic",
					PromptCaching: true,
				},
			},
			Agent: llm.LLMPurposeConfig{
				Models: []string{"anthropic/claude-sonnet-4-20250514"},
			},
			Summarization: llm.LLMPurposeConfig{
				Models: []string{}, // Empty = use agent fallback
			},
			Embeddings: llm.LLMPurposeConfig{
				Models: []string{}, // Empty = disabled
			},
			Thinking: llm.ThinkingConfig{
				BudgetTokens: 10000, // Default budget for extended thinking
			},
		},
		HomeAssistant: hass.HomeAssistantConfig{
			Enabled:          false, // Disabled by default - requires manual configuration
			Timeout:          "10s",
			EventPrefix:      "[HomeAssistant Event]",
			SubscriptionFile: "hass-subscriptions.json",
			ReconnectDelay:   "5s",
		},
		Tools: toolsconfig.ToolsConfig{
			Browser: toolsconfig.BrowserToolsConfig{
				Enabled:        true,
				Dir:            "", // Default: ~/.goclaw/browser
				AutoDownload:   true,
				Revision:       "", // Latest
				Headless:       true,
				NoSandbox:      false,
				DefaultProfile: "default",
				Timeout:        "30s",
				Stealth:        true,
				Device:         "clear", // No viewport emulation, fills window
				ProfileDomains: map[string]string{},
				Bubblewrap: toolsconfig.BrowserBubblewrapConfig{
					Enabled:     true,
					ExtraRoBind: []string{},
					ExtraBind:   []string{},
					GPU:         true, // GPU enabled by default when sandbox is used
				},
			},
			Exec: toolsconfig.ExecToolsConfig{
				Timeout: 1800, // 30 minutes (matches OpenClaw)
				Bubblewrap: toolsconfig.ExecBubblewrapConfig{
					Enabled:      true,
					ExtraRoBind:  []string{},
					ExtraBind:    []string{},
					ExtraEnv:     map[string]string{},
					AllowNetwork: true, // Network allowed by default
					ClearEnv:     true, // Clear env by default for security
				},
			},
			},
		Sandbox: sandbox.Config{
			Bubblewrap: sandbox.BubblewrapConfig{
				Volumes: sandbox.DefaultVolumes(),
			},
		},
		Session: session.SessionConfig{
			Store:       "sqlite", // Default to SQLite
			StorePath:   filepath.Join(goclawDir, "sessions.db"),
			InheritPath: filepath.Join(home, ".openclaw", "agents", "main", "sessions"), // OpenClaw sessions (for inherit)
			Inherit:     true,
			InheritFrom: "agent:main:main",
			Summarization: session.SummarizationConfig{
				Ollama: session.OllamaLLMConfig{
					URL:            "", // Empty = use fallback model only
					Model:          "",
					TimeoutSeconds: 120,
					ContextTokens:  0, // Auto-detect
				},
				FallbackModel:        "claude-3-haiku-20240307",
				FailureThreshold:     3,
				ResetMinutes:         30,
				RetryIntervalSeconds: 60,
				Checkpoint: session.CheckpointSubConfig{
					Enabled:         true,
					Thresholds:      []int{25, 50, 75},
					TurnThreshold:   15,
					MinTokensForGen: 10000,
				},
				Compaction: session.CompactionSubConfig{
					ReserveTokens:    4000,
					MaxMessages:      500, // Trigger compaction if > 500 messages
					PreferCheckpoint: true,
					KeepPercent:      50, // Keep 50% of messages
					MinMessages:      20, // Never drop below 20 messages
				},
			},
			MemoryFlush: session.MemoryFlushConfig{
				Enabled:            true,
				ShowInSystemPrompt: true,
				Thresholds: []session.FlushThreshold{
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
				},
			},
		},
		Memory: memory.MemorySearchConfig{
			Enabled: true, // Memory search enabled by default
			Query: memory.MemorySearchQueryConfig{
				MaxResults:    6,
				MinScore:      0.35,
				VectorWeight:  0.7,
				KeywordWeight: 0.3,
			},
			Paths: []string{}, // Only memory/ and MEMORY.md by default
		},
		Transcript: transcript.TranscriptConfig{
			Enabled:                true,
			IndexIntervalSeconds:   30,
			BatchSize:              100,
			BackfillBatchSize:      10,
			MaxGroupGapSeconds:     300,
			MaxMessagesPerChunk:    8,
			MaxEmbeddingContentLen: 16000,
			Query: transcript.TranscriptQueryConfig{
				MaxResults:    10,
				MinScore:      0.3,
				VectorWeight:  0.7,
				KeywordWeight: 0.3,
			},
		},
		PromptCache: gwtypes.PromptCacheConfig{
			PollInterval: 60, // Check file hashes every 60 seconds as fallback
		},
		Media: media.MediaConfig{
			Dir:     "media",         // Relative to workspace (resolved in gateway)
			TTL:     600,             // 10 minutes (more generous than OpenClaw's 2 min)
			MaxSize: 5 * 1024 * 1024, // 5MB
		},
		STT: stt.Config{
			Provider: "whispercpp", // Whisper.cpp enabled by default (requires model to work)
			WhisperCpp: stt.WhisperCppConfig{
				ModelsDir: "~/.goclaw/stt/whisper", // Default models directory
				Model:     "",                      // Select via 'goclaw setup edit'
				Language:  "en",
			},
		},
		Channels: ChannelsConfig{
			TUI: tuiconfig.Config{
				ShowLogs: true, // Show logs panel by default
			},
			// Telegram and HTTP are disabled by default (zero values)
		},
		Skills: skills.SkillsConfig{
			Enabled:       true,
			Watch:         true,
			WatchDebounce: 500,
			Entries:       make(map[string]skills.SkillEntryConfig),
		},
		Cron: cron.CronConfig{
			Enabled:           true, // Cron enabled by default
			JobTimeoutMinutes: 5,    // Default 5 minute timeout for jobs
			Heartbeat: cron.HeartbeatConfig{
				Enabled:         true,
				IntervalMinutes: 30,
			},
		},
		Supervision: gwtypes.SupervisionConfig{
			Guidance: gwtypes.GuidanceConfig{
				Prefix:     "[Supervisor]: ",
				SystemNote: "",
			},
			Ghostwriting: gwtypes.GhostwritingConfig{
				TypingDelayMs: 500,
			},
		},
	}

	// Load from goclaw.json
	if err := mergeJSONConfig(cfg, goclawData); err != nil {
		logging.L_error("config: failed to parse goclaw.json", "path", goclawPath, "error", err)
		return nil, err
	}
	logging.L_debug("config: loaded from goclaw.json", "path", goclawPath)

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

// mergeJSONConfig deep-merges JSON data into an existing config.
// Only fields actually present in the JSON override the existing config.
// This prevents partial configs from wiping out defaults for unspecified fields.
func mergeJSONConfig(dst *Config, jsonData []byte) error {
	// First, parse JSON to a map to see what fields are actually specified
	var rawMap map[string]interface{}
	if err := json.Unmarshal(jsonData, &rawMap); err != nil {
		return formatJSONError(jsonData, err)
	}

	// Re-marshal only the specified fields, then unmarshal to a Config
	// This preserves only what was explicitly in the JSON
	specifiedJSON, err := json.Marshal(rawMap)
	if err != nil {
		return fmt.Errorf("re-marshal specified fields: %w", err)
	}

	var src Config
	if err := json.Unmarshal(specifiedJSON, &src); err != nil {
		// This is re-marshaled JSON, so type errors are more likely than syntax
		return formatJSONError(specifiedJSON, err)
	}

	// Use custom merge that only overwrites if the source struct was actually
	// present in the JSON (non-empty in the raw map)
	return mergeConfigSelective(dst, &src, rawMap)
}

// mergeConfigSelective merges src into dst, but only for top-level fields
// that were present in the raw JSON map. This prevents zero-value structs
// from overwriting defaults.
func mergeConfigSelective(dst, src *Config, rawMap map[string]interface{}) error {
	// For each top-level field, only merge if it was in the JSON
	if _, ok := rawMap["gateway"]; ok {
		if err := mergo.Merge(&dst.Gateway, src.Gateway, mergo.WithOverride); err != nil {
			return err
		}
	}
	if _, ok := rawMap["agent"]; ok {
		if err := mergo.Merge(&dst.Agent, src.Agent, mergo.WithOverride); err != nil {
			return err
		}
	}
	if _, ok := rawMap["llm"]; ok {
		if err := mergo.Merge(&dst.LLM, src.LLM, mergo.WithOverride); err != nil {
			return err
		}
	}
	if _, ok := rawMap["homeassistant"]; ok {
		if err := mergo.Merge(&dst.HomeAssistant, src.HomeAssistant, mergo.WithOverride); err != nil {
			return err
		}
	}
	if _, ok := rawMap["tools"]; ok {
		if err := mergo.Merge(&dst.Tools, src.Tools, mergo.WithOverride); err != nil {
			return err
		}
	}
	// Handle channels - either nested under "channels" key or as legacy top-level keys
	if channelsMap, ok := rawMap["channels"].(map[string]interface{}); ok {
		if _, ok := channelsMap["telegram"]; ok {
			if err := mergo.Merge(&dst.Channels.Telegram, src.Channels.Telegram, mergo.WithOverride); err != nil {
				return err
			}
		}
		if _, ok := channelsMap["whatsapp"]; ok {
			if err := mergo.Merge(&dst.Channels.WhatsApp, src.Channels.WhatsApp, mergo.WithOverride); err != nil {
				return err
			}
		}
		if _, ok := channelsMap["http"]; ok {
			if err := mergo.Merge(&dst.Channels.HTTP, src.Channels.HTTP, mergo.WithOverride); err != nil {
				return err
			}
		}
		if _, ok := channelsMap["tui"]; ok {
			if err := mergo.Merge(&dst.Channels.TUI, src.Channels.TUI, mergo.WithOverride); err != nil {
				return err
			}
		}
	}
	if sessionMap, ok := rawMap["session"].(map[string]interface{}); ok {
		// Session needs nested selective merge
		mergeSessionSelective(&dst.Session, &src.Session, sessionMap)
	}
	if _, ok := rawMap["memory"]; ok {
		if err := mergo.Merge(&dst.Memory, src.Memory, mergo.WithOverride); err != nil {
			return err
		}
	}
	if _, ok := rawMap["transcript"]; ok {
		if err := mergo.Merge(&dst.Transcript, src.Transcript, mergo.WithOverride); err != nil {
			return err
		}
	}
	if _, ok := rawMap["promptCache"]; ok {
		if err := mergo.Merge(&dst.PromptCache, src.PromptCache, mergo.WithOverride); err != nil {
			return err
		}
	}
	if _, ok := rawMap["skills"]; ok {
		if err := mergo.Merge(&dst.Skills, src.Skills, mergo.WithOverride); err != nil {
			return err
		}
	}
	if _, ok := rawMap["media"]; ok {
		if err := mergo.Merge(&dst.Media, src.Media, mergo.WithOverride); err != nil {
			return err
		}
	}
	if _, ok := rawMap["stt"]; ok {
		if err := mergo.Merge(&dst.STT, src.STT, mergo.WithOverride); err != nil {
			return err
		}
	}
	if _, ok := rawMap["cron"]; ok {
		if err := mergo.Merge(&dst.Cron, src.Cron, mergo.WithOverride); err != nil {
			return err
		}
	}
	if _, ok := rawMap["supervision"]; ok {
		if err := mergo.Merge(&dst.Supervision, src.Supervision, mergo.WithOverride); err != nil {
			return err
		}
	}
	if _, ok := rawMap["roles"]; ok {
		// Roles is a map, just assign directly (mergo doesn't handle maps well)
		dst.Roles = src.Roles
	}
	if _, ok := rawMap["auth"]; ok {
		if err := mergo.Merge(&dst.Auth, src.Auth, mergo.WithOverride); err != nil {
			return err
		}
	}

	return nil
}

// mergeSessionSelective handles the session config which has multiple sub-structs
// that need individual presence checking
func mergeSessionSelective(dst, src *session.SessionConfig, rawMap map[string]interface{}) {
	// Simple fields - always merge if session was specified
	if src.Store != "" {
		dst.Store = src.Store
	}
	if src.StorePath != "" {
		dst.StorePath = src.StorePath
	}
	if src.InheritPath != "" {
		dst.InheritPath = src.InheritPath
	}
	if _, ok := rawMap["inherit"]; ok {
		dst.Inherit = src.Inherit
	}
	if src.InheritFrom != "" {
		dst.InheritFrom = src.InheritFrom
	}

	// Sub-structs - only merge if present in JSON
	if _, ok := rawMap["summarization"]; ok {
		mergo.Merge(&dst.Summarization, src.Summarization, mergo.WithOverride) //nolint:errcheck // mergo.Merge rarely fails
	}
	if _, ok := rawMap["memoryFlush"]; ok {
		mergo.Merge(&dst.MemoryFlush, src.MemoryFlush, mergo.WithOverride) //nolint:errcheck // mergo.Merge rarely fails
	}
}
