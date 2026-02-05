package config

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"dario.cat/mergo"
	"github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/sandbox"
)

// ConfigBackupCount is the number of backup versions to keep
const ConfigBackupCount = 5

// LoadResult contains the loaded config and metadata about where it came from
type LoadResult struct {
	Config       *Config
	SourcePath   string // Path to goclaw.json that was found/created
	Bootstrapped bool   // True if config was bootstrapped from openclaw.json
}

// isMinimalJSON checks if JSON content is essentially empty (just {} or whitespace)
func isMinimalJSON(data []byte) bool {
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		return true // Can't parse = treat as empty
	}
	return len(m) == 0
}

// Config represents the merged goclaw configuration
type Config struct {
	Gateway      GatewayConfig         `json:"gateway"`
	Agent        AgentIdentityConfig   `json:"agent"`
	LLM          LLMConfig             `json:"llm"`
	Tools        ToolsConfig           `json:"tools"`
	Telegram     TelegramConfig        `json:"telegram"`
	HTTP         HTTPConfig            `json:"http"`
	Session      SessionConfig         `json:"session"`
	MemorySearch MemorySearchConfig    `json:"memorySearch"`
	Transcript   TranscriptConfig      `json:"transcript"`
	PromptCache  PromptCacheConfig     `json:"promptCache"`
	Media        MediaConfig           `json:"media"`
	TUI          TUIConfig             `json:"tui"`
	Skills       SkillsConfig          `json:"skills"`
	Cron         CronConfig            `json:"cron"`
}

// AgentIdentityConfig configures the agent's display identity
type AgentIdentityConfig struct {
	Name   string `json:"name"`   // Agent's display name (default: "GoClaw")
	Emoji  string `json:"emoji"`  // Optional emoji prefix (default: "")
	Typing string `json:"typing"` // Custom typing indicator text (default: derived from Name)
}

// DisplayName returns the agent name with emoji prefix if configured
func (c *AgentIdentityConfig) DisplayName() string {
	if c.Emoji != "" {
		return c.Emoji + " " + c.Name
	}
	return c.Name
}

// TypingText returns the typing indicator text
func (c *AgentIdentityConfig) TypingText() string {
	if c.Typing != "" {
		return c.Typing
	}
	return c.Name + " is typing..."
}

// HTTPConfig configures the HTTP server
// HTTP is enabled by default if any user has HTTP credentials configured
type HTTPConfig struct {
	Enabled *bool  `json:"enabled,omitempty"` // Enable HTTP server (default: true if users have passwords)
	Listen  string `json:"listen"`            // Address to listen on (default: ":1337")
}

// CronConfig configures the cron scheduler
type CronConfig struct {
	Enabled           bool            `json:"enabled"`           // Enable cron scheduler (default: true)
	JobTimeoutMinutes int             `json:"jobTimeoutMinutes"` // Timeout for job execution in minutes (default: 30, 0 = no timeout)
	Heartbeat         HeartbeatConfig `json:"heartbeat"`         // Heartbeat configuration
}

// HeartbeatConfig configures the periodic heartbeat system
type HeartbeatConfig struct {
	Enabled         bool   `json:"enabled"`         // Enable heartbeat (default: true)
	IntervalMinutes int    `json:"intervalMinutes"` // Interval in minutes (default: 30)
	Prompt          string `json:"prompt"`          // Custom heartbeat prompt (optional)
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
	// Storage backend: "sqlite" (default) or "jsonl"
	Store     string `json:"store"`
	StorePath string `json:"storePath"` // SQLite DB path (when store="sqlite")

	// OpenClaw session inheritance
	InheritPath string `json:"inheritPath"` // Path to OpenClaw sessions directory
	Inherit     bool   `json:"inherit"`     // Inherit from OpenClaw session
	InheritFrom string `json:"inheritFrom"` // Session key to inherit from

	// Features
	Summarization SummarizationConfig `json:"summarization"`
	MemoryFlush   MemoryFlushConfig   `json:"memoryFlush"`
}

// SummarizationConfig configures LLM-based summarization for checkpoints and compaction
type SummarizationConfig struct {
	// LLM Configuration
	Ollama        OllamaLLMConfig `json:"ollama"`        // Primary: local Ollama model
	FallbackModel string          `json:"fallbackModel"` // Fallback: Anthropic model (e.g., "claude-3-haiku-20240307")

	// Failover settings
	FailureThreshold int `json:"failureThreshold"` // Fall back after N consecutive Ollama failures (default: 3)
	ResetMinutes     int `json:"resetMinutes"`     // Reset failure count after N minutes (default: 30)

	// Retry settings
	RetryIntervalSeconds int `json:"retryIntervalSeconds"` // Background retry interval for pending summaries (default: 60)

	// Sub-features
	Checkpoint CheckpointSubConfig `json:"checkpoint"`
	Compaction CompactionSubConfig `json:"compaction"`
}

// CheckpointSubConfig configures rolling checkpoint generation
type CheckpointSubConfig struct {
	Enabled         bool  `json:"enabled"`
	Thresholds      []int `json:"thresholds"`      // Token usage percents to trigger checkpoint (e.g., [25, 50, 75])
	TurnThreshold   int   `json:"turnThreshold"`   // Generate every N user messages
	MinTokensForGen int   `json:"minTokensForGen"` // Don't checkpoint if < N tokens
}

// CompactionSubConfig configures context compaction
type CompactionSubConfig struct {
	ReserveTokens    int  `json:"reserveTokens"`    // Tokens to reserve before compaction (default: 4000)
	MaxMessages      int  `json:"maxMessages"`      // Trigger compaction if messages exceed this (default: 500, 0 = disabled)
	PreferCheckpoint bool `json:"preferCheckpoint"` // Use existing checkpoint for summary if available
	KeepPercent      int  `json:"keepPercent"`      // Percent of messages to keep after compaction (default: 50)
	MinMessages      int  `json:"minMessages"`      // Minimum messages to always keep (default: 20)
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
	Enabled bool                    `json:"enabled"` // Enable memory search tools
	Query   MemorySearchQueryConfig `json:"query"`   // Search query settings
	Paths   []string                `json:"paths"`   // Additional paths to index (besides memory/ and MEMORY.md)
}

// MemorySearchQueryConfig configures search query behavior
type MemorySearchQueryConfig struct {
	MaxResults    int     `json:"maxResults"`    // Maximum number of results to return (default: 6)
	MinScore      float64 `json:"minScore"`      // Minimum score threshold (default: 0.35)
	VectorWeight  float64 `json:"vectorWeight"`  // Weight for vector/semantic search (default: 0.7)
	KeywordWeight float64 `json:"keywordWeight"` // Weight for keyword/FTS search (default: 0.3)
}

// TranscriptConfig configures transcript indexing and search
type TranscriptConfig struct {
	Enabled bool `json:"enabled"` // Enable transcript indexing (default: true)

	// Indexing settings
	IndexIntervalSeconds   int `json:"indexIntervalSeconds"`   // How often to check for new messages (default: 30)
	BatchSize              int `json:"batchSize"`              // Max messages to process per batch (default: 100)
	MaxGroupGapSeconds     int `json:"maxGroupGapSeconds"`     // Max time gap between messages in a chunk (default: 300 = 5 min)
	MaxMessagesPerChunk    int `json:"maxMessagesPerChunk"`    // Max messages per conversation chunk (default: 8)
	MaxEmbeddingContentLen int `json:"maxEmbeddingContentLen"` // Max chars to embed per chunk (default: 16000)

	// Search settings (similar to memory search)
	Query TranscriptQueryConfig `json:"query"`
}

// TranscriptQueryConfig configures transcript search behavior
type TranscriptQueryConfig struct {
	MaxResults    int     `json:"maxResults"`    // Maximum results to return (default: 10)
	MinScore      float64 `json:"minScore"`      // Minimum score threshold (default: 0.3)
	VectorWeight  float64 `json:"vectorWeight"`  // Weight for vector search (default: 0.7)
	KeywordWeight float64 `json:"keywordWeight"` // Weight for keyword search (default: 0.3)
}

// GatewayConfig contains gateway server settings
type GatewayConfig struct {
	LogFile    string `json:"logFile"`
	PIDFile    string `json:"pidFile"`
	WorkingDir string `json:"workingDir"`
}

// LLMConfig contains LLM provider settings
// LLMConfig configures LLM providers and model selection.
// Providers are aliased instances; models reference them via "alias/model" format.
// LLMConfig configures LLM providers and model selection.
// Providers are aliased instances; models reference them via "alias/model" format.
type LLMConfig struct {
	Providers     map[string]LLMProviderConfig `json:"providers"`
	Agent         LLMPurposeConfig             `json:"agent"`         // Main chat
	Summarization LLMPurposeConfig             `json:"summarization"` // Checkpoint/compaction
	Embeddings    LLMPurposeConfig             `json:"embeddings"`    // Memory/transcript
	SystemPrompt  string                       `json:"systemPrompt"`  // System prompt for agent
}

// LLMProviderConfig is the configuration for a single provider instance
type LLMProviderConfig struct {
	Type           string `json:"type"`                     // "anthropic", "openai", "ollama"
	APIKey         string `json:"apiKey,omitempty"`         // For cloud providers
	BaseURL        string `json:"baseURL,omitempty"`        // For OpenAI-compatible endpoints
	URL            string `json:"url,omitempty"`            // For Ollama
	MaxTokens      int    `json:"maxTokens,omitempty"`      // Default output limit
	TimeoutSeconds int    `json:"timeoutSeconds,omitempty"` // Request timeout
	PromptCaching  bool   `json:"promptCaching,omitempty"`  // Anthropic-specific
}

// LLMPurposeConfig defines the model chain for a specific purpose
type LLMPurposeConfig struct {
	Models    []string `json:"models"`              // First = primary, rest = fallbacks
	MaxTokens int      `json:"maxTokens,omitempty"` // Output limit override (0 = use model default)
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

// ToolsConfig contains tool-specific settings
type ToolsConfig struct {
	Web     WebToolsConfig     `json:"web"`
	Browser BrowserToolsConfig `json:"browser"`
}

// WebToolsConfig contains web tool settings
type WebToolsConfig struct {
	BraveAPIKey string `json:"braveApiKey"`
	UseBrowser  string `json:"useBrowser"` // Browser fallback: "auto" (on 403/bot), "always", "never" (default: "auto")
	Profile     string `json:"profile"`    // Browser profile for web_fetch (default: "default")
	Headless    *bool  `json:"headless"`   // Run browser headless (default: true, set false for debugging)
}

// BrowserToolsConfig contains browser tool settings
type BrowserToolsConfig struct {
	Enabled        bool              `json:"enabled"`        // Enable headless browser tool (requires Chrome/Chromium)
	Dir            string            `json:"dir"`            // Browser data directory (empty = ~/.openclaw/goclaw/browser)
	AutoDownload   bool              `json:"autoDownload"`   // Download Chromium if missing (default: true)
	Revision       string            `json:"revision"`       // Chromium revision (empty = latest)
	Headless       bool              `json:"headless"`       // Run browser in headless mode (default: true)
	NoSandbox      bool              `json:"noSandbox"`      // Disable Chrome sandbox (needed for Docker/root)
	DefaultProfile string            `json:"defaultProfile"` // Default profile name (default: "default")
	Timeout        string            `json:"timeout"`        // Default action timeout (default: "30s")
	Stealth        bool              `json:"stealth"`        // Enable stealth mode (default: true)
	Device         string            `json:"device"`         // Device emulation: "clear", "laptop", "iphone-x", etc. (default: "clear")
	ProfileDomains map[string]string `json:"profileDomains"` // Domain → profile mapping for auto-selection
}

// Load reads configuration from goclaw.json.
//
// Bootstrap mode (first run):
//   - If no goclaw.json exists OR it's empty, extract config from openclaw.json
//   - Write complete goclaw.json with defaults + openclaw values
//   - From then on, goclaw.json is authoritative
//
// Normal mode (subsequent runs):
//   - Load only from goclaw.json, ignore openclaw.json entirely
//   - goclaw.json is the single source of truth
func Load() (*LoadResult, error) {
	home, _ := os.UserHomeDir()
	openclawDir := filepath.Join(home, ".openclaw")

	goclawGlobalPath := filepath.Join(openclawDir, "goclaw.json")
	goclawLocalPath := "goclaw.json" // current working directory
	openclawPath := filepath.Join(openclawDir, "openclaw.json")

	logging.L_debug("config: checking files", "openclawDir", openclawDir, "cwd", mustGetwd())

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

	// Determine if we need bootstrap mode
	needsBootstrap := !goclawExists || isMinimalJSON(goclawData)

	if needsBootstrap {
		logging.L_info("config: bootstrap mode - will extract from openclaw.json and write goclaw.json")
	} else {
		logging.L_debug("config: normal mode - using goclaw.json only")
	}

	// Build defaults
	cfg := &Config{
		Gateway: GatewayConfig{
			LogFile:    filepath.Join(openclawDir, "goclaw.log"),
			PIDFile:    filepath.Join(openclawDir, "goclaw.pid"),
			WorkingDir: filepath.Join(openclawDir, "workspace"),
		},
		Agent: AgentIdentityConfig{
			Name:  "GoClaw",
			Emoji: "",
		},
		LLM: LLMConfig{
			Providers: map[string]LLMProviderConfig{
				"anthropic": {
					Type:          "anthropic",
					PromptCaching: true,
				},
			},
			Agent: LLMPurposeConfig{
				Models:    []string{"anthropic/claude-sonnet-4-20250514"},
				MaxTokens: 8192,
			},
			Summarization: LLMPurposeConfig{
				Models:    []string{}, // Empty = use agent fallback
				MaxTokens: 4096,
			},
			Embeddings: LLMPurposeConfig{
				Models: []string{}, // Empty = disabled
			},
		},
		Tools: ToolsConfig{
			Browser: BrowserToolsConfig{
				Enabled:        true,
				Dir:            "",        // Default: ~/.openclaw/goclaw/browser
				AutoDownload:   true,
				Revision:       "",        // Latest
				Headless:       true,
				NoSandbox:      false,
				DefaultProfile: "default",
				Timeout:        "30s",
				Stealth:        true,
				Device:         "clear",   // No viewport emulation, fills window
				ProfileDomains: map[string]string{},
			},
		},
		Session: SessionConfig{
			Store:       "sqlite", // Default to SQLite
			StorePath:   filepath.Join(openclawDir, "goclaw", "sessions.db"),
			InheritPath: filepath.Join(openclawDir, "agents", "main", "sessions"), // OpenClaw sessions directory
			Inherit:     true,
			InheritFrom: "agent:main:main",
			Summarization: SummarizationConfig{
				Ollama: OllamaLLMConfig{
					URL:            "", // Empty = use fallback model only
					Model:          "",
					TimeoutSeconds: 120,
					ContextTokens:  0, // Auto-detect
				},
				FallbackModel:        "claude-3-haiku-20240307",
				FailureThreshold:     3,
				ResetMinutes:         30,
				RetryIntervalSeconds: 60,
				Checkpoint: CheckpointSubConfig{
					Enabled:         true,
					Thresholds:      []int{25, 50, 75},
					TurnThreshold:   15,
					MinTokensForGen: 10000,
				},
				Compaction: CompactionSubConfig{
					ReserveTokens:    4000,
					MaxMessages:      500, // Trigger compaction if > 500 messages
					PreferCheckpoint: true,
					KeepPercent:      50, // Keep 50% of messages
					MinMessages:      20, // Never drop below 20 messages
				},
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
		},
		MemorySearch: MemorySearchConfig{
			Enabled: true, // Memory search enabled by default
			Query: MemorySearchQueryConfig{
				MaxResults:    6,
				MinScore:      0.35,
				VectorWeight:  0.7,
				KeywordWeight: 0.3,
			},
			Paths: []string{}, // Only memory/ and MEMORY.md by default
		},
		Transcript: TranscriptConfig{
			Enabled:                true,  // Transcript indexing enabled by default
			IndexIntervalSeconds:   30,    // Check every 30 seconds
			BatchSize:              100,   // Process up to 100 messages per batch
			MaxGroupGapSeconds:     300,   // 5 minute gap = new conversation chunk
			MaxMessagesPerChunk:    8,     // Keep chunks focused
			MaxEmbeddingContentLen: 16000, // Conservative for nomic-embed-text
			Query: TranscriptQueryConfig{
				MaxResults:    10,
				MinScore:      0.3,
				VectorWeight:  0.7,
				KeywordWeight: 0.3,
			},
		},
		PromptCache: PromptCacheConfig{
			PollInterval: 60, // Check file hashes every 60 seconds as fallback
		},
		Media: MediaConfig{
			Dir:     "media", // Relative to workspace (resolved in gateway)
			TTL:     600,     // 10 minutes (more generous than OpenClaw's 2 min)
			MaxSize: 5 * 1024 * 1024, // 5MB
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
			Enabled:           true, // Cron enabled by default
			JobTimeoutMinutes: 5,    // Default 5 minute timeout for jobs
			Heartbeat: HeartbeatConfig{
				Enabled:         true,
				IntervalMinutes: 30,
			},
		},
	}

	if needsBootstrap {
		// BOOTSTRAP MODE: Extract from openclaw.json, then write goclaw.json

		// Load from openclaw.json if it exists
		if data, err := os.ReadFile(openclawPath); err == nil {
			logging.L_debug("config: loading openclaw.json for bootstrap", "path", openclawPath, "size", len(data))
			var base map[string]interface{}
			if err := json.Unmarshal(data, &base); err == nil {
				cfg.mergeOpenclawConfig(base, openclawDir)
			} else {
				logging.L_warn("config: failed to parse openclaw.json", "error", err)
			}
		} else {
			logging.L_debug("config: openclaw.json not found, using defaults only", "path", openclawPath)
		}

		// Apply environment variable fallbacks
		applyEnvFallbacks(cfg)

		// Determine where to write goclaw.json
		// If local goclaw.json existed (even if empty), write there; otherwise use global
		if goclawPath == "" {
			// No goclaw.json found anywhere - create in current directory
			goclawPath, _ = filepath.Abs(goclawLocalPath)
		}

		// Write the bootstrapped config
		if err := WriteConfigWithBackup(goclawPath, cfg); err != nil {
			logging.L_error("config: failed to write bootstrapped config", "path", goclawPath, "error", err)
			// Non-fatal - continue with in-memory config
		} else {
			logging.L_info("config: bootstrapped from openclaw.json", "path", goclawPath)
		}

		return &LoadResult{
			Config:       cfg,
			SourcePath:   goclawPath,
			Bootstrapped: true,
		}, nil
	}

	// NORMAL MODE: Load only from goclaw.json, ignore openclaw.json

	if err := mergeJSONConfig(cfg, goclawData); err != nil {
		logging.L_error("config: failed to parse goclaw.json", "path", goclawPath, "error", err)
		return nil, err
	}
	logging.L_debug("config: loaded from goclaw.json", "path", goclawPath)

	// Apply environment variable fallbacks (for secrets not in config file)
	applyEnvFallbacks(cfg)

	// Log final config summary
	agentModel := ""
	if len(cfg.LLM.Agent.Models) > 0 {
		agentModel = cfg.LLM.Agent.Models[0]
	}
	logging.L_debug("config: loaded",
		"agentModel", agentModel,
		"providers", len(cfg.LLM.Providers),
		"telegramEnabled", cfg.Telegram.Enabled,
		"workingDir", cfg.Gateway.WorkingDir,
	)

	return &LoadResult{
		Config:       cfg,
		SourcePath:   goclawPath,
		Bootstrapped: false,
	}, nil
}

// applyEnvFallbacks applies environment variable fallbacks for secrets
func applyEnvFallbacks(cfg *Config) {
	// Apply ANTHROPIC_API_KEY to anthropic provider if not already set
	if key := os.Getenv("ANTHROPIC_API_KEY"); key != "" {
		if prov, ok := cfg.LLM.Providers["anthropic"]; ok && prov.APIKey == "" {
			logging.L_debug("config: using ANTHROPIC_API_KEY from environment")
			prov.APIKey = key
			cfg.LLM.Providers["anthropic"] = prov
		}
	}
	if cfg.Tools.Web.BraveAPIKey == "" {
		if key := os.Getenv("BRAVE_API_KEY"); key != "" {
			logging.L_debug("config: using BRAVE_API_KEY from environment")
			cfg.Tools.Web.BraveAPIKey = key
		}
	}
	if cfg.Telegram.BotToken == "" {
		if token := os.Getenv("TELEGRAM_BOT_TOKEN"); token != "" {
			logging.L_debug("config: using TELEGRAM_BOT_TOKEN from environment")
			cfg.Telegram.BotToken = token
		}
	}
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
			// Extract model - use as first agent model
			if model, ok := defaults["model"].(map[string]interface{}); ok {
				if primary, ok := model["primary"].(string); ok {
					// Keep full "provider/model" format for new config
					c.LLM.Agent.Models = []string{primary}
					logging.L_debug("config: extracted model from agents.defaults.model.primary", "model", primary)
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
			c.Tools.Browser.DefaultProfile = profile
			logging.L_debug("config: browser defaultProfile", "profile", profile)
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
						// Apply to anthropic provider
						if prov, ok := c.LLM.Providers["anthropic"]; ok {
							prov.APIKey = key
							c.LLM.Providers["anthropic"] = prov
						}
					}
				}
			}
		}
	} else {
		logging.L_trace("config: auth-profiles.json not found", "path", authProfilesPath)
	}

}

// GetStoreType returns the effective store type ("jsonl" or "sqlite")
func (s *SessionConfig) GetStoreType() string {
	if s.Store != "" {
		return s.Store
	}
	return "sqlite" // default
}

// GetStorePath returns the path for the storage backend
func (s *SessionConfig) GetStorePath() string {
	if s.StorePath != "" {
		return s.StorePath
	}
	// Default SQLite path
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".openclaw", "goclaw", "sessions.db")
}

// mustGetwd returns the current working directory or "unknown" on error
func mustGetwd() string {
	if cwd, err := os.Getwd(); err == nil {
		return cwd
	}
	return "unknown"
}

// rotateBackups rotates config backup files.
// Keeps up to ConfigBackupCount versions:
//   - .bak.4 gets deleted (oldest)
//   - .bak.3 → .bak.4
//   - .bak.2 → .bak.3
//   - .bak.1 → .bak.2
//   - .bak → .bak.1
func rotateBackups(configPath string) {
	if ConfigBackupCount <= 1 {
		return
	}

	backupBase := configPath + ".bak"
	maxIndex := ConfigBackupCount - 1 // 4

	// Delete oldest
	oldestPath := fmt.Sprintf("%s.%d", backupBase, maxIndex)
	if err := os.Remove(oldestPath); err != nil && !os.IsNotExist(err) {
		logging.L_trace("config: failed to remove oldest backup", "path", oldestPath, "error", err)
	}

	// Rotate: .bak.3 → .bak.4, .bak.2 → .bak.3, .bak.1 → .bak.2
	for i := maxIndex - 1; i >= 1; i-- {
		src := fmt.Sprintf("%s.%d", backupBase, i)
		dst := fmt.Sprintf("%s.%d", backupBase, i+1)
		if err := os.Rename(src, dst); err != nil && !os.IsNotExist(err) {
			logging.L_trace("config: failed to rotate backup", "src", src, "dst", dst, "error", err)
		}
	}

	// .bak → .bak.1
	if err := os.Rename(backupBase, backupBase+".1"); err != nil && !os.IsNotExist(err) {
		logging.L_trace("config: failed to rotate .bak to .bak.1", "error", err)
	}
}

// copyFile copies a file from src to dst
func copyFile(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	// Get source file info for permissions
	srcInfo, err := srcFile.Stat()
	if err != nil {
		return err
	}

	dstFile, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, srcInfo.Mode().Perm())
	if err != nil {
		return err
	}
	defer dstFile.Close()

	_, err = io.Copy(dstFile, srcFile)
	return err
}

// WriteConfigWithBackup writes the config to the specified path with backup rotation.
// 1. Rotates existing backups
// 2. Copies current config to .bak
// 3. Writes new config atomically
func WriteConfigWithBackup(path string, cfg *Config) error {
	// Rotate existing backups
	rotateBackups(path)

	// Copy current to .bak if it exists
	if _, err := os.Stat(path); err == nil {
		backupPath := path + ".bak"
		if err := copyFile(path, backupPath); err != nil {
			logging.L_warn("config: failed to create backup", "path", backupPath, "error", err)
			// Continue anyway - backup is best-effort
		} else {
			logging.L_trace("config: created backup", "path", backupPath)
		}
	}

	// Marshal config with indentation
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	// Add trailing newline
	data = append(data, '\n')

	// Write atomically
	if err := sandbox.AtomicWriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	logging.L_info("config: written with defaults", "path", path, "size", len(data))
	return nil
}

// mergeJSONConfig deep-merges JSON data into an existing config.
// Only fields actually present in the JSON override the existing config.
// This prevents partial configs from wiping out defaults for unspecified fields.
func mergeJSONConfig(dst *Config, jsonData []byte) error {
	// First, parse JSON to a map to see what fields are actually specified
	var rawMap map[string]interface{}
	if err := json.Unmarshal(jsonData, &rawMap); err != nil {
		return fmt.Errorf("parse JSON: %w", err)
	}

	// Re-marshal only the specified fields, then unmarshal to a Config
	// This preserves only what was explicitly in the JSON
	specifiedJSON, err := json.Marshal(rawMap)
	if err != nil {
		return fmt.Errorf("re-marshal specified fields: %w", err)
	}

	var src Config
	if err := json.Unmarshal(specifiedJSON, &src); err != nil {
		return fmt.Errorf("parse to config: %w", err)
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
	if _, ok := rawMap["llm"]; ok {
		if err := mergo.Merge(&dst.LLM, src.LLM, mergo.WithOverride); err != nil {
			return err
		}
	}
	if _, ok := rawMap["tools"]; ok {
		if err := mergo.Merge(&dst.Tools, src.Tools, mergo.WithOverride); err != nil {
			return err
		}
	}
	if _, ok := rawMap["telegram"]; ok {
		if err := mergo.Merge(&dst.Telegram, src.Telegram, mergo.WithOverride); err != nil {
			return err
		}
	}
	if _, ok := rawMap["http"]; ok {
		if err := mergo.Merge(&dst.HTTP, src.HTTP, mergo.WithOverride); err != nil {
			return err
		}
	}
	if sessionMap, ok := rawMap["session"].(map[string]interface{}); ok {
		// Session needs nested selective merge
		mergeSessionSelective(&dst.Session, &src.Session, sessionMap)
	}
	if _, ok := rawMap["memorySearch"]; ok {
		if err := mergo.Merge(&dst.MemorySearch, src.MemorySearch, mergo.WithOverride); err != nil {
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
	if _, ok := rawMap["tui"]; ok {
		if err := mergo.Merge(&dst.TUI, src.TUI, mergo.WithOverride); err != nil {
			return err
		}
	}
	if _, ok := rawMap["cron"]; ok {
		if err := mergo.Merge(&dst.Cron, src.Cron, mergo.WithOverride); err != nil {
			return err
		}
	}

	return nil
}

// mergeSessionSelective handles the session config which has multiple sub-structs
// that need individual presence checking
func mergeSessionSelective(dst, src *SessionConfig, rawMap map[string]interface{}) {
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
	// Inherit is a bool, tricky - only set if explicitly in JSON
	// We can't easily detect this without more work, so skip for now

	// Sub-structs - only merge if present in JSON
	if _, ok := rawMap["summarization"]; ok {
		mergo.Merge(&dst.Summarization, src.Summarization, mergo.WithOverride)
	}
	if _, ok := rawMap["memoryFlush"]; ok {
		mergo.Merge(&dst.MemoryFlush, src.MemoryFlush, mergo.WithOverride)
	}
}
