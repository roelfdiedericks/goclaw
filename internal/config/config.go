package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/roelfdiedericks/goclaw/internal/logging"
)

// Config represents the merged goclaw configuration
type Config struct {
	Gateway      GatewayConfig         `json:"gateway"`
	LLM          LLMConfig             `json:"llm"`
	Users        map[string]UserConfig `json:"users"`
	Mirroring    MirroringConfig       `json:"mirroring"`
	Tools        ToolsConfig           `json:"tools"`
	Telegram     TelegramConfig        `json:"telegram"`
	Session      SessionConfig         `json:"session"`
	MemorySearch MemorySearchConfig    `json:"memorySearch"`
	PromptCache  PromptCacheConfig     `json:"promptCache"`
	Media        MediaConfig           `json:"media"`
	TUI          TUIConfig             `json:"tui"`
	Skills       SkillsConfig          `json:"skills"`
	Cron         CronConfig            `json:"cron"`
}

// CronConfig configures the cron scheduler
type CronConfig struct {
	Enabled bool `json:"enabled"` // Enable cron scheduler (default: false)
}

// SkillsConfig configures the skills system
type SkillsConfig struct {
	Enabled       bool                        `json:"enabled"`
	BundledDir    string                      `json:"bundledDir"`    // Override bundled skills path
	ManagedDir    string                      `json:"managedDir"`    // Override managed skills path
	WorkspaceDir  string                      `json:"workspaceDir"`  // Override workspace skills path
	ExtraDirs     []string                    `json:"extraDirs"`     // Additional skill directories
	Watch         bool                        `json:"watch"`         // Watch for file changes
	WatchDebounce int                         `json:"watchDebounceMs"` // Debounce interval in ms
	Entries       map[string]SkillEntryConfig `json:"entries"`       // Per-skill configuration
}

// SkillEntryConfig holds per-skill configuration
type SkillEntryConfig struct {
	Enabled bool              `json:"enabled"`
	APIKey  string            `json:"apiKey,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	Config  map[string]any    `json:"config,omitempty"`
}

// MediaConfig configures media file storage
type MediaConfig struct {
	Dir     string `json:"dir"`     // Base directory (default: ~/.openclaw/media)
	TTL     int    `json:"ttl"`     // TTL in seconds (default: 600 = 10 min)
	MaxSize int    `json:"maxSize"` // Max file size in bytes (default: 5MB)
}

// SessionConfig contains session persistence and context management settings
type SessionConfig struct {
	// Storage backend: "jsonl" (default) or "sqlite"
	Store     string `json:"store"`
	StorePath string `json:"storePath"` // SQLite DB path (when store="sqlite")

	// Legacy field (use Store instead)
	Storage string `json:"storage"` // Deprecated: use "store"

	// JSONL settings
	Path        string `json:"path"`        // Sessions directory path (JSONL)
	Inherit     bool   `json:"inherit"`     // Inherit from OpenClaw session
	InheritFrom string `json:"inheritFrom"` // Session key to inherit from
	WriteToKey  string `json:"writeToKey"`  // Session key to write to

	// Features
	Checkpoint  CheckpointConfig  `json:"checkpoint"`
	MemoryFlush MemoryFlushConfig `json:"memoryFlush"`
	Compaction  CompactionConfig  `json:"compaction"`
}

// CheckpointConfig configures rolling checkpoint generation
type CheckpointConfig struct {
	Enabled               bool   `json:"enabled"`
	Model                 string `json:"model"`                 // Cheaper model for checkpoints
	FallbackToMain        bool   `json:"fallbackToMain"`        // Use main model if checkpoint model unavailable
	TokenThresholdPercents []int  `json:"tokenThresholdPercents"` // e.g., [25, 50, 75]
	TurnThreshold         int    `json:"turnThreshold"`         // Generate every N user messages
	MinTokensForGen       int    `json:"minTokensForGen"`       // Don't checkpoint if < N tokens
}

// MemoryFlushConfig configures memory flush prompting
type MemoryFlushConfig struct {
	Enabled            bool                   `json:"enabled"`
	ShowInSystemPrompt bool                   `json:"showInSystemPrompt"`
	Thresholds         []FlushThresholdConfig `json:"thresholds"`
}

// FlushThresholdConfig defines a memory flush threshold
type FlushThresholdConfig struct {
	Percent      int    `json:"percent"`
	Prompt       string `json:"prompt"`
	InjectAs     string `json:"injectAs"`     // "system" or "user"
	OncePerCycle bool   `json:"oncePerCycle"`
}

// CompactionConfig configures context compaction
type CompactionConfig struct {
	ReserveTokens          int             `json:"reserveTokens"`          // Tokens to reserve before compaction (default: 30000)
	PreferCheckpoint       bool            `json:"preferCheckpoint"`       // Use checkpoint for summary if available
	Ollama                 OllamaLLMConfig `json:"ollama"`                 // Optional Ollama model for compaction summaries
	RetryIntervalSeconds   int             `json:"retryIntervalSeconds"`   // Background retry interval (default: 60, 0 = disabled)
	OllamaFailureThreshold int             `json:"ollamaFailureThreshold"` // Fall back to main model after N consecutive Ollama failures (default: 3)
	OllamaResetMinutes     int             `json:"ollamaResetMinutes"`     // Try Ollama again after N minutes (default: 30)
}

// OllamaLLMConfig configures an Ollama model for LLM tasks (compaction, checkpoints)
type OllamaLLMConfig struct {
	URL            string `json:"url"`            // Ollama API URL (e.g., "http://localhost:11434")
	Model          string `json:"model"`          // LLM model for chat completion (e.g., "qwen2.5:14b" for 128k context)
	TimeoutSeconds int    `json:"timeoutSeconds"` // Request timeout in seconds (default: 300 = 5 min)
	ContextTokens  int    `json:"contextTokens"`  // Override context window (0 = auto-detect from model)
}

// PromptCacheConfig configures system prompt caching
type PromptCacheConfig struct {
	PollInterval int `json:"pollInterval"` // Hash poll interval in seconds (default: 60, 0 = disabled)
}

// TUIConfig configures the terminal user interface
type TUIConfig struct {
	ShowLogs bool `json:"showLogs"` // Show logs panel by default (default: true)
}

// MemorySearchConfig configures the memory search tool
type MemorySearchConfig struct {
	Enabled bool                     `json:"enabled"` // Enable memory search tools
	Ollama  OllamaConfig             `json:"ollama"`  // Ollama embedding provider settings
	Query   MemorySearchQueryConfig  `json:"query"`   // Search query settings
	Paths   []string                 `json:"paths"`   // Additional paths to index (besides memory/ and MEMORY.md)
}

// OllamaConfig configures the Ollama embedding provider
type OllamaConfig struct {
	URL   string `json:"url"`   // Ollama API URL (e.g., "http://localhost:11434")
	Model string `json:"model"` // Embedding model (e.g., "nomic-embed-text")
}

// MemorySearchQueryConfig configures search query behavior
type MemorySearchQueryConfig struct {
	MaxResults    int     `json:"maxResults"`    // Maximum number of results to return (default: 6)
	MinScore      float64 `json:"minScore"`      // Minimum score threshold (default: 0.35)
	VectorWeight  float64 `json:"vectorWeight"`  // Weight for vector/semantic search (default: 0.7)
	KeywordWeight float64 `json:"keywordWeight"` // Weight for keyword/FTS search (default: 0.3)
}

// GatewayConfig contains gateway server settings
type GatewayConfig struct {
	Port       int    `json:"port"`
	LogFile    string `json:"logFile"`
	PIDFile    string `json:"pidFile"`
	WorkingDir string `json:"workingDir"`
}

// LLMConfig contains LLM provider settings
type LLMConfig struct {
	Provider      string `json:"provider"` // "anthropic"
	Model         string `json:"model"`
	APIKey        string `json:"apiKey"`
	SystemPrompt  string `json:"systemPrompt"`
	MaxTokens     int    `json:"maxTokens"`
	PromptCaching bool   `json:"promptCaching"` // Enable prompt caching (reduces costs by up to 90%)
}

// TelegramConfig contains Telegram channel settings
type TelegramConfig struct {
	Enabled  bool   `json:"enabled"`
	BotToken string `json:"botToken"`
}

// UserConfig represents a user who can interact with the agent
type UserConfig struct {
	Name        string             `json:"name"`
	Role        string             `json:"role"` // "owner" or "user"
	Identities  []IdentityConfig   `json:"identities"`
	Credentials []CredentialConfig `json:"credentials,omitempty"`
	Permissions []string           `json:"permissions,omitempty"` // tool whitelist for non-owners
}

// IdentityConfig maps external identities to users
type IdentityConfig struct {
	Provider string `json:"provider"` // "telegram", "local", "apikey"
	ID       string `json:"id"`       // telegram user ID, "owner" for local, etc.
}

// CredentialConfig stores hashed credentials for challenge auth
type CredentialConfig struct {
	Type  string `json:"type"`  // "apikey", "password"
	Hash  string `json:"hash"`  // argon2/bcrypt hash
	Label string `json:"label"` // "laptop-key", etc.
}

// MirroringConfig controls cross-channel mirroring
type MirroringConfig struct {
	Enabled  bool                     `json:"enabled"`
	Channels map[string]ChannelMirror `json:"channels"`
}

// ChannelMirror controls mirroring for a specific channel
type ChannelMirror struct {
	Mirror bool `json:"mirror"` // receives mirrors from other channels
}

// ToolsConfig contains tool-specific settings
type ToolsConfig struct {
	Web     WebToolsConfig     `json:"web"`
	Browser BrowserToolsConfig `json:"browser"`
}

// WebToolsConfig contains web tool settings
type WebToolsConfig struct {
	BraveAPIKey string `json:"braveApiKey"`
}

// BrowserToolsConfig contains browser tool settings
type BrowserToolsConfig struct {
	Enabled   bool   `json:"enabled"`   // Enable headless browser tool (requires Chrome/Chromium)
	Headless  bool   `json:"headless"`  // Run browser in headless mode (default: true)
	NoSandbox bool   `json:"noSandbox"` // Disable Chrome sandbox (needed for Docker/root)
	Profile   string `json:"profile"`   // Browser profile name (default: "openclaw")
}

// Load reads configuration from goclaw.json, falling back to openclaw.json
// goclaw.json values override openclaw.json values
func Load() (*Config, error) {
	home, _ := os.UserHomeDir()
	openclawDir := filepath.Join(home, ".openclaw")

	cfg := &Config{
		Gateway: GatewayConfig{
			Port:       1337, // different from openclaw (18789) to allow coexistence
			LogFile:    filepath.Join(openclawDir, "goclaw.log"),
			PIDFile:    filepath.Join(openclawDir, "goclaw.pid"),
			WorkingDir: filepath.Join(openclawDir, "workspace"),
		},
		LLM: LLMConfig{
			Provider:      "anthropic",
			Model:         "claude-sonnet-4-20250514",
			MaxTokens:     8192,
			PromptCaching: true, // Enabled by default - saves up to 90% on system prompt tokens
		},
		Users:     make(map[string]UserConfig),
		Mirroring: MirroringConfig{
			Enabled:  true, // mirroring on by default
			Channels: make(map[string]ChannelMirror),
		},
		Tools: ToolsConfig{
			Browser: BrowserToolsConfig{
				Headless: true,           // default headless
				Profile:  "openclaw",     // default profile
			},
		},
		Session: SessionConfig{
			Store:       "sqlite", // Default to SQLite
			StorePath:   filepath.Join(openclawDir, "goclaw", "sessions.db"),
			Storage:     "jsonl", // Legacy field (ignored when Store is set)
			Path:        filepath.Join(openclawDir, "agents", "main", "sessions"),
			Inherit:     true,
			InheritFrom: "agent:main:main",
			WriteToKey:  "goclaw:main:main",
			Checkpoint: CheckpointConfig{
				Enabled:               true,
				Model:                 "claude-3-haiku-20240307",
				FallbackToMain:        true,
				TokenThresholdPercents: []int{25, 50, 75},
				TurnThreshold:         15,
				MinTokensForGen:       10000,
			},
			MemoryFlush: MemoryFlushConfig{
				Enabled:            true,
				ShowInSystemPrompt: true,
				Thresholds: []FlushThresholdConfig{
					{
						Percent:      50,
						Prompt:       "Context at 50%. Consider noting key decisions to memory.",
						InjectAs:     "system",
						OncePerCycle: true,
					},
					{
						Percent:      75,
						Prompt:       "Context at 75%. Write important context to memory/YYYY-MM-DD.md now.",
						InjectAs:     "system",
						OncePerCycle: true,
					},
					{
						Percent:      90,
						Prompt:       "[SYSTEM: pre-compaction memory flush]\nContext at 90%. Compaction imminent.\nStore durable memories now (use memory/YYYY-MM-DD.md; create memory/ if needed).\nIf nothing to store, reply with NO_REPLY.",
						InjectAs:     "user",
						OncePerCycle: true,
					},
				},
			},
			Compaction: CompactionConfig{
				ReserveTokens:    4000,
				PreferCheckpoint: true,
			},
		},
		MemorySearch: MemorySearchConfig{
			Enabled: true, // Memory search enabled by default
			Ollama: OllamaConfig{
				URL:   "", // Empty = keyword-only mode (no embeddings)
				Model: "nomic-embed-text",
			},
			Query: MemorySearchQueryConfig{
				MaxResults:    6,
				MinScore:      0.35,
				VectorWeight:  0.7,
				KeywordWeight: 0.3,
			},
			Paths: []string{}, // Only memory/ and MEMORY.md by default
		},
		PromptCache: PromptCacheConfig{
			PollInterval: 60, // Check file hashes every 60 seconds as fallback
		},
		Media: MediaConfig{
			Dir:     "~/.openclaw/media", // Shared with OpenClaw
			TTL:     600,                 // 10 minutes (more generous than OpenClaw's 2 min)
			MaxSize: 5 * 1024 * 1024,     // 5MB
		},
		TUI: TUIConfig{
			ShowLogs: true, // Show logs panel by default
		},
		Skills: SkillsConfig{
			Enabled:       true,
			Watch:         true,
			WatchDebounce: 500,
			Entries:       make(map[string]SkillEntryConfig),
		},
		Cron: CronConfig{
			Enabled: true, // Cron enabled by default
		},
	}

	openclawPath := filepath.Join(openclawDir, "openclaw.json")
	goclawGlobalPath := filepath.Join(openclawDir, "goclaw.json")
	goclawLocalPath := "goclaw.json" // current working directory

	logging.L_debug("config: checking files", "openclawDir", openclawDir, "cwd", mustGetwd())

	// Load base config from openclaw.json if it exists
	if data, err := os.ReadFile(openclawPath); err == nil {
		logging.L_debug("config: loading openclaw.json", "path", openclawPath, "size", len(data))
		var base map[string]interface{}
		if err := json.Unmarshal(data, &base); err == nil {
			cfg.mergeOpenclawConfig(base, openclawDir)
		} else {
			logging.L_warn("config: failed to parse openclaw.json", "error", err)
		}
	} else {
		logging.L_debug("config: openclaw.json not found", "path", openclawPath)
	}

	// Override with goclaw.json from ~/.openclaw if it exists
	if data, err := os.ReadFile(goclawGlobalPath); err == nil {
		logging.L_debug("config: loading global goclaw.json", "path", goclawGlobalPath, "size", len(data))
		if err := json.Unmarshal(data, cfg); err != nil {
			logging.L_error("config: failed to parse global goclaw.json", "error", err)
			return nil, err
		}
		logging.L_debug("config: global goclaw.json merged")
	} else {
		logging.L_debug("config: global goclaw.json not found", "path", goclawGlobalPath)
	}

	// Override with goclaw.json from current directory (highest priority)
	if data, err := os.ReadFile(goclawLocalPath); err == nil {
		absPath, _ := filepath.Abs(goclawLocalPath)
		logging.L_debug("config: loading local goclaw.json", "path", absPath, "size", len(data))
		if err := json.Unmarshal(data, cfg); err != nil {
			logging.L_error("config: failed to parse local goclaw.json", "error", err)
			return nil, err
		}
		logging.L_debug("config: local goclaw.json merged")
	} else {
		logging.L_trace("config: local goclaw.json not found", "path", goclawLocalPath)
	}

	// Environment variable fallbacks
	if cfg.LLM.APIKey == "" {
		if key := os.Getenv("ANTHROPIC_API_KEY"); key != "" {
			logging.L_debug("config: using ANTHROPIC_API_KEY from environment")
			cfg.LLM.APIKey = key
		}
	}
	if key := os.Getenv("BRAVE_API_KEY"); key != "" && cfg.Tools.Web.BraveAPIKey == "" {
		logging.L_debug("config: using BRAVE_API_KEY from environment")
		cfg.Tools.Web.BraveAPIKey = key
	}
	if token := os.Getenv("TELEGRAM_BOT_TOKEN"); token != "" && cfg.Telegram.BotToken == "" {
		logging.L_debug("config: using TELEGRAM_BOT_TOKEN from environment")
		cfg.Telegram.BotToken = token
	}

	// Log final config summary
	logging.L_debug("config: loaded",
		"users", len(cfg.Users),
		"model", cfg.LLM.Model,
		"telegramEnabled", cfg.Telegram.Enabled,
		"workingDir", cfg.Gateway.WorkingDir,
	)

	return cfg, nil
}

// mergeOpenclawConfig extracts relevant settings from openclaw.json
func (c *Config) mergeOpenclawConfig(base map[string]interface{}, openclawDir string) {
	logging.L_trace("config: parsing openclaw.json structure")

	// Extract workspace from agents.defaults
	if agents, ok := base["agents"].(map[string]interface{}); ok {
		if defaults, ok := agents["defaults"].(map[string]interface{}); ok {
			if workspace, ok := defaults["workspace"].(string); ok {
				logging.L_debug("config: extracted workspace from agents.defaults.workspace", "workspace", workspace)
				c.Gateway.WorkingDir = workspace
			}
			// Extract model
			if model, ok := defaults["model"].(map[string]interface{}); ok {
				if primary, ok := model["primary"].(string); ok {
					// Convert "anthropic/claude-opus-4-5" to "claude-opus-4-5"
					originalModel := primary
					if len(primary) > 10 && primary[:10] == "anthropic/" {
						c.LLM.Model = primary[10:]
					} else {
						c.LLM.Model = primary
					}
					logging.L_debug("config: extracted model from agents.defaults.model.primary", "original", originalModel, "model", c.LLM.Model)
				}
			}
		}
	}

	// Extract Telegram settings from channels.telegram (object, not array)
	if channels, ok := base["channels"].(map[string]interface{}); ok {
		if telegram, ok := channels["telegram"].(map[string]interface{}); ok {
			logging.L_debug("config: found channels.telegram section")
			if enabled, ok := telegram["enabled"].(bool); ok {
				logging.L_debug("config: telegram enabled", "enabled", enabled)
				c.Telegram.Enabled = enabled
			}
			if token, ok := telegram["botToken"].(string); ok {
				logging.L_debug("config: telegram botToken found", "length", len(token))
				c.Telegram.BotToken = token
			}
			// Extract allowed users and create user configs
			if allowFrom, ok := telegram["allowFrom"].([]interface{}); ok {
				logging.L_debug("config: found telegram allowFrom list", "count", len(allowFrom))
				for i, u := range allowFrom {
					var userID string
					switch v := u.(type) {
					case float64:
						userID = fmt.Sprintf("%.0f", v)
					case string:
						userID = v
					}
					if userID == "" {
						continue
					}

					// First user is owner, rest are regular users
					role := "user"
					if i == 0 {
						role = "owner"
					}

					// Create user config if not exists
					configKey := "telegram_" + userID
					if _, exists := c.Users[configKey]; !exists {
						logging.L_debug("config: creating user from telegram allowFrom",
							"configKey", configKey,
							"telegramID", userID,
							"role", role,
							"index", i,
						)
						c.Users[configKey] = UserConfig{
							Name: "User " + userID,
							Role: role,
							Identities: []IdentityConfig{
								{Provider: "telegram", ID: userID},
							},
						}

						// Owner also gets local identity
						if role == "owner" {
							user := c.Users[configKey]
							user.Identities = append(user.Identities, IdentityConfig{
								Provider: "local",
								ID:       "owner",
							})
							c.Users[configKey] = user
							logging.L_debug("config: owner also gets local identity", "configKey", configKey)
						}
					}
				}
			}
		} else {
			logging.L_trace("config: no channels.telegram section found")
		}
	}

	// Extract tools.web settings
	if tools, ok := base["tools"].(map[string]interface{}); ok {
		if web, ok := tools["web"].(map[string]interface{}); ok {
			if search, ok := web["search"].(map[string]interface{}); ok {
				if key, ok := search["apiKey"].(string); ok {
					c.Tools.Web.BraveAPIKey = key
				}
			}
		}
	}

	// Extract browser settings from top-level browser config
	if browser, ok := base["browser"].(map[string]interface{}); ok {
		logging.L_debug("config: found browser section in openclaw.json")
		if enabled, ok := browser["enabled"].(bool); ok {
			c.Tools.Browser.Enabled = enabled
			logging.L_debug("config: browser enabled", "enabled", enabled)
		}
		if headless, ok := browser["headless"].(bool); ok {
			c.Tools.Browser.Headless = headless
			logging.L_debug("config: browser headless", "headless", headless)
		}
		if noSandbox, ok := browser["noSandbox"].(bool); ok {
			c.Tools.Browser.NoSandbox = noSandbox
			logging.L_debug("config: browser noSandbox", "noSandbox", noSandbox)
		}
		if profile, ok := browser["defaultProfile"].(string); ok {
			c.Tools.Browser.Profile = profile
			logging.L_debug("config: browser profile", "profile", profile)
		}
	}

	// Load API key from auth-profiles.json
	authProfilesPath := filepath.Join(openclawDir, "agents", "main", "agent", "auth-profiles.json")
	if data, err := os.ReadFile(authProfilesPath); err == nil {
		logging.L_debug("config: loading auth-profiles.json", "path", authProfilesPath)
		var authProfiles map[string]interface{}
		if err := json.Unmarshal(data, &authProfiles); err == nil {
			if profiles, ok := authProfiles["profiles"].(map[string]interface{}); ok {
				if anthropic, ok := profiles["anthropic:default"].(map[string]interface{}); ok {
					if key, ok := anthropic["key"].(string); ok {
						logging.L_debug("config: extracted API key from auth-profiles.json", "keyLength", len(key))
						c.LLM.APIKey = key
					}
				}
			}
		}
	} else {
		logging.L_trace("config: auth-profiles.json not found", "path", authProfilesPath)
	}

	// Set up mirroring channels
	c.Mirroring.Channels["telegram"] = ChannelMirror{Mirror: true}
	c.Mirroring.Channels["tui"] = ChannelMirror{Mirror: true}
}

// GetStoreType returns the effective store type ("jsonl" or "sqlite")
func (s *SessionConfig) GetStoreType() string {
	if s.Store != "" {
		return s.Store
	}
	// Legacy: check Storage field
	if s.Storage == "memory" {
		return "jsonl" // memory mode not supported, fall back to jsonl
	}
	return "jsonl" // default
}

// GetStorePath returns the path for the storage backend
func (s *SessionConfig) GetStorePath() string {
	switch s.GetStoreType() {
	case "sqlite":
		if s.StorePath != "" {
			return s.StorePath
		}
		// Default SQLite path
		home, _ := os.UserHomeDir()
		return filepath.Join(home, ".openclaw", "goclaw", "sessions.db")
	default:
		// JSONL uses the sessions directory Path
		return s.Path
	}
}

// GetOwnerID returns the owner user's ID (first user configured)
func (c *Config) GetOwnerID() string {
	for id, user := range c.Users {
		if user.Role == "owner" {
			return id
		}
	}
	return ""
}

// mustGetwd returns the current working directory or "unknown" on error
func mustGetwd() string {
	if cwd, err := os.Getwd(); err == nil {
		return cwd
	}
	return "unknown"
}
