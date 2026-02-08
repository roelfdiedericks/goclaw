package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"dario.cat/mergo"
	"github.com/roelfdiedericks/goclaw/internal/logging"
)

// LoadResult contains the loaded config and metadata about where it came from
type LoadResult struct {
	Config     *Config
	SourcePath string // Path to goclaw.json that was loaded
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
	Gateway       GatewayConfig         `json:"gateway"`
	Agent         AgentIdentityConfig   `json:"agent"`
	LLM           LLMConfig             `json:"llm"`
	HomeAssistant HomeAssistantConfig   `json:"homeassistant"` // Top-level Home Assistant config
	Tools         ToolsConfig           `json:"tools"`
	Telegram      TelegramConfig        `json:"telegram"`
	HTTP          HTTPConfig            `json:"http"`
	Session       SessionConfig         `json:"session"`
	MemorySearch  MemorySearchConfig    `json:"memorySearch"`
	Transcript    TranscriptConfig      `json:"transcript"`
	PromptCache   PromptCacheConfig     `json:"promptCache"`
	Media         MediaConfig           `json:"media"`
	TUI           TUIConfig             `json:"tui"`
	Skills        SkillsConfig          `json:"skills"`
	Cron          CronConfig            `json:"cron"`
	Supervision   SupervisionConfig     `json:"supervision"`
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
	Listen  string `json:"listen"`            // Address to listen on (e.g., ":1337", "127.0.0.1:1337")
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

// SupervisionConfig configures session supervision features
type SupervisionConfig struct {
	Guidance     GuidanceConfig     `json:"guidance"`
	Ghostwriting GhostwritingConfig `json:"ghostwriting"`
}

// GuidanceConfig configures supervisor guidance injection
type GuidanceConfig struct {
	// Prefix prepended to guidance messages (default: "[Supervisor]: ")
	// The LLM sees this prefix and knows the message is from the supervisor
	Prefix string `json:"prefix"`

	// SystemNote is an optional system message injected with guidance (future use)
	// Could contain instructions like "Respond to this guidance naturally"
	SystemNote string `json:"systemNote,omitempty"`
}

// GhostwritingConfig configures supervisor ghostwriting
type GhostwritingConfig struct {
	// TypingDelayMs is the delay before delivering the message (default: 500)
	// Simulates natural typing so message doesn't appear instantly
	TypingDelayMs int `json:"typingDelayMs"`
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
	Dir     string `json:"dir"`     // Base directory (empty = <workspace>/media/)
	TTL     int    `json:"ttl"`     // TTL in seconds (default: 600 = 10 min)
	MaxSize int    `json:"maxSize"` // Max file size in bytes (default: 5MB)
}

// HomeAssistantConfig configures Home Assistant integration (REST + WebSocket)
type HomeAssistantConfig struct {
	Enabled          bool   `json:"enabled"`                    // Enable Home Assistant integration
	URL              string `json:"url"`                        // HA base URL (e.g., "https://home.example.com:8123")
	Token            string `json:"token"`                      // Long-lived access token
	Insecure         bool   `json:"insecure,omitempty"`         // Skip TLS verification for self-signed certs
	Timeout          string `json:"timeout,omitempty"`          // Request timeout (default: "10s")
	EventPrefix      string `json:"eventPrefix,omitempty"`      // Prefix for injected events (default: "[HomeAssistant Event]")
	SubscriptionFile string `json:"subscriptionFile,omitempty"` // Subscription persistence file (default: "hass-subscriptions.json")
	ReconnectDelay   string `json:"reconnectDelay,omitempty"`   // WebSocket reconnect delay (default: "5s")
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
	DbPath  string                  `json:"dbPath"`  // Database path (default: ~/.goclaw/memory.db)
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
	BackfillBatchSize      int `json:"backfillBatchSize"`      // Max chunks to backfill per interval (default: 10)
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
// Providers are aliased instances; models reference them via "alias/model" format.
type LLMConfig struct {
	Providers     map[string]LLMProviderConfig `json:"providers"`
	Agent         LLMPurposeConfig             `json:"agent"`         // Main chat
	Summarization LLMPurposeConfig             `json:"summarization"` // Checkpoint/compaction
	Embeddings    LLMPurposeConfig             `json:"embeddings"`    // Memory/transcript
	Thinking      ThinkingConfig               `json:"thinking"`      // Extended thinking settings
	SystemPrompt  string                       `json:"systemPrompt"`  // System prompt for agent
}

// ThinkingConfig configures extended thinking for models that support it
type ThinkingConfig struct {
	BudgetTokens int `json:"budgetTokens"` // Token budget for thinking (default: 10000)
}

// LLMProviderConfig is the configuration for a single provider instance
type LLMProviderConfig struct {
	Type           string `json:"type"`                     // "anthropic", "openai", "ollama"
	APIKey         string `json:"apiKey,omitempty"`         // For cloud providers
	BaseURL        string `json:"baseURL,omitempty"`        // For OpenAI-compatible endpoints
	URL            string `json:"url,omitempty"`            // For Ollama
	MaxTokens      int    `json:"maxTokens,omitempty"`      // Default output limit
	ContextTokens  int    `json:"contextTokens,omitempty"`  // Context window override (0 = auto-detect)
	TimeoutSeconds int    `json:"timeoutSeconds,omitempty"` // Request timeout
	PromptCaching  bool   `json:"promptCaching,omitempty"`  // Anthropic-specific
	EmbeddingOnly  bool   `json:"embeddingOnly,omitempty"`  // For embedding-only models
}

// LLMPurposeConfig defines the model chain for a specific purpose
type LLMPurposeConfig struct {
	Models         []string `json:"models"`                    // First = primary, rest = fallbacks
	MaxTokens      int      `json:"maxTokens,omitempty"`       // Output limit override (0 = use model default)
	MaxInputTokens int      `json:"maxInputTokens,omitempty"`  // Input limit for summarization (0 = use model context - buffer)
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
	Web        WebToolsConfig         `json:"web"`
	Browser    BrowserToolsConfig     `json:"browser"`
	Exec       ExecToolsConfig        `json:"exec"`
	Bubblewrap BubblewrapGlobalConfig `json:"bubblewrap"`
}

// BubblewrapGlobalConfig contains global bubblewrap settings
type BubblewrapGlobalConfig struct {
	Path string `json:"path"` // Custom path to bwrap binary (empty = search PATH)
}

// ExecToolsConfig contains exec tool settings
type ExecToolsConfig struct {
	Timeout    int                    `json:"timeout"`    // Timeout in seconds (default: 1800 = 30 min, 0 = no timeout)
	Bubblewrap ExecBubblewrapConfig   `json:"bubblewrap"` // Sandbox settings
}

// ExecBubblewrapConfig contains bubblewrap settings for exec tool
type ExecBubblewrapConfig struct {
	Enabled      bool              `json:"enabled"`      // Enable sandboxing (default: false)
	ExtraRoBind  []string          `json:"extraRoBind"`  // Additional read-only bind mounts
	ExtraBind    []string          `json:"extraBind"`    // Additional read-write bind mounts
	ExtraEnv     map[string]string `json:"extraEnv"`     // Additional environment variables
	AllowNetwork bool              `json:"allowNetwork"` // Allow network access (default: true)
	ClearEnv     bool              `json:"clearEnv"`     // Clear environment before setting defaults (default: true)
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
	Enabled        bool                     `json:"enabled"`        // Enable headless browser tool (requires Chrome/Chromium)
	Dir            string                   `json:"dir"`            // Browser data directory (empty = ~/.goclaw/browser)
	AutoDownload   bool                     `json:"autoDownload"`   // Download Chromium if missing (default: true)
	Revision       string                   `json:"revision"`       // Chromium revision (empty = latest)
	Headless       bool                     `json:"headless"`       // Run browser in headless mode (default: true)
	NoSandbox      bool                     `json:"noSandbox"`      // Disable Chrome sandbox (needed for Docker/root)
	DefaultProfile string                   `json:"defaultProfile"` // Default profile name (default: "default")
	Timeout        string                   `json:"timeout"`        // Default action timeout (default: "30s")
	Stealth        bool                     `json:"stealth"`        // Enable stealth mode (default: true)
	Device         string                   `json:"device"`         // Device emulation: "clear", "laptop", "iphone-x", etc. (default: "clear")
	ProfileDomains map[string]string        `json:"profileDomains"` // Domain → profile mapping for auto-selection
	Bubblewrap     BrowserBubblewrapConfig  `json:"bubblewrap"`     // Sandbox settings
}

// BrowserBubblewrapConfig contains bubblewrap settings for browser tool
type BrowserBubblewrapConfig struct {
	Enabled     bool     `json:"enabled"`     // Enable sandboxing (default: false)
	ExtraRoBind []string `json:"extraRoBind"` // Additional read-only bind mounts
	ExtraBind   []string `json:"extraBind"`   // Additional read-write bind mounts
	GPU         bool     `json:"gpu"`         // Enable GPU acceleration (default: true)
}

// Load reads configuration from goclaw.json.
// If no config file exists, returns an error directing user to run 'goclaw setup'.
func Load() (*LoadResult, error) {
	home, _ := os.UserHomeDir()
	goclawDir := filepath.Join(home, ".goclaw")

	goclawGlobalPath := filepath.Join(goclawDir, "goclaw.json")
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
		Gateway: GatewayConfig{
			LogFile:    filepath.Join(goclawDir, "goclaw.log"),
			PIDFile:    filepath.Join(goclawDir, "goclaw.pid"),
			WorkingDir: filepath.Join(goclawDir, "workspace"),
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
				Models: []string{}, // Empty = use agent fallback
			},
			Embeddings: LLMPurposeConfig{
				Models: []string{}, // Empty = disabled
			},
			Thinking: ThinkingConfig{
				BudgetTokens: 10000, // Default budget for extended thinking
			},
		},
		HomeAssistant: HomeAssistantConfig{
			Enabled:          false,                        // Disabled by default - requires manual configuration
			Timeout:          "10s",
			EventPrefix:      "[HomeAssistant Event]",
			SubscriptionFile: "hass-subscriptions.json",
			ReconnectDelay:   "5s",
		},
		Tools: ToolsConfig{
			Browser: BrowserToolsConfig{
				Enabled:        true,
				Dir:            "",        // Default: ~/.goclaw/browser
				AutoDownload:   true,
				Revision:       "",        // Latest
				Headless:       true,
				NoSandbox:      false,
				DefaultProfile: "default",
				Timeout:        "30s",
				Stealth:        true,
				Device:         "clear",   // No viewport emulation, fills window
				ProfileDomains: map[string]string{},
				Bubblewrap: BrowserBubblewrapConfig{
					Enabled:     false, // Disabled by default
					ExtraRoBind: []string{},
					ExtraBind:   []string{},
					GPU:         true,  // GPU enabled by default when sandbox is used
				},
			},
			Exec: ExecToolsConfig{
				Timeout: 1800, // 30 minutes (matches OpenClaw)
				Bubblewrap: ExecBubblewrapConfig{
					Enabled:      false, // Disabled by default
					ExtraRoBind:  []string{},
					ExtraBind:    []string{},
					ExtraEnv:     map[string]string{},
					AllowNetwork: true, // Network allowed by default
					ClearEnv:     true, // Clear env by default for security
				},
			},
			Bubblewrap: BubblewrapGlobalConfig{
				Path: "", // Empty = search PATH
			},
		},
		Session: SessionConfig{
			Store:       "sqlite", // Default to SQLite
			StorePath:   filepath.Join(goclawDir, "sessions.db"),
			InheritPath: filepath.Join(home, ".openclaw", "agents", "main", "sessions"), // OpenClaw sessions (for inherit)
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
		Supervision: SupervisionConfig{
			Guidance: GuidanceConfig{
				Prefix:     "[Supervisor]: ",
				SystemNote: "",
			},
			Ghostwriting: GhostwritingConfig{
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
		Config:     cfg,
		SourcePath: goclawPath,
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
	return filepath.Join(home, ".goclaw", "sessions.db")
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
	if _, ok := rawMap["supervision"]; ok {
		if err := mergo.Merge(&dst.Supervision, src.Supervision, mergo.WithOverride); err != nil {
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
