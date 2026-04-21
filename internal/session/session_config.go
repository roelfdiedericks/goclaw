package session

import (
	"github.com/roelfdiedericks/goclaw/internal/paths"
)

// SessionConfig configures session management
// Note: This was previously config.SessionConfig, moved here to avoid import cycles
type SessionConfig struct {
	// Storage backend: "sqlite" (only supported backend)
	Store     string `json:"store" default:"sqlite"`
	StorePath string `json:"storePath"` // SQLite DB path (runtime default)

	// OpenClaw session inheritance
	InheritPath string `json:"inheritPath"`                           // Path to OpenClaw sessions directory (runtime default)
	Inherit     bool   `json:"inherit" default:"true"`                // Inherit from OpenClaw session
	InheritFrom string `json:"inheritFrom" default:"agent:main:main"` // Session key to inherit from

	// Features
	Summarization SummarizationConfig `json:"summarization"`
	MemoryFlush   MemoryFlushConfig   `json:"memoryFlush"`
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
	p, _ := paths.DataPath("sessions.db")
	return p
}

// SummarizationConfig configures LLM-based summarization for checkpoints and compaction
type SummarizationConfig struct {
	// LLM Configuration
	Ollama        OllamaLLMConfig `json:"ollama"`                                          // Primary: local Ollama model
	FallbackModel string          `json:"fallbackModel" default:"claude-3-haiku-20240307"` // Fallback: Anthropic model

	// Failover settings
	FailureThreshold int `json:"failureThreshold" default:"3"` // Fall back after N consecutive Ollama failures
	ResetMinutes     int `json:"resetMinutes" default:"30"`    // Reset failure count after N minutes

	// Retry settings
	RetryIntervalSeconds int `json:"retryIntervalSeconds" default:"60"` // Background retry interval for pending summaries

	// Sub-features
	Checkpoint CheckpointSubConfig `json:"checkpoint"`
	Compaction CompactionSubConfig `json:"compaction"`
}

// CheckpointSubConfig configures rolling checkpoint generation
type CheckpointSubConfig struct {
	Enabled         bool  `json:"enabled" default:"true"`
	Thresholds      []int `json:"thresholds"`                      // Token usage percents (runtime default: [25,50,75])
	TurnThreshold   int   `json:"turnThreshold" default:"15"`      // Generate every N user messages
	MinTokensForGen int   `json:"minTokensForGen" default:"10000"` // Don't checkpoint if < N tokens
}

// CompactionSubConfig configures context compaction
type CompactionSubConfig struct {
	ReserveTokens      int  `json:"reserveTokens" default:"4000"`      // Tokens to reserve before compaction
	MaxMessages        int  `json:"maxMessages" default:"500"`         // Trigger compaction if messages exceed this (0 = disabled)
	PreferCheckpoint   bool `json:"preferCheckpoint" default:"true"`   // Use existing checkpoint for summary if available
	KeepPercent        int  `json:"keepPercent" default:"50"`          // Percent of messages to keep after compaction
	MinMessages        int  `json:"minMessages" default:"20"`          // Minimum messages to always keep
	FreshTailCount     int  `json:"freshTailCount" default:"10"`       // When > 0, keep this many newest messages instead of KeepPercent
	FreshTailMaxTokens int  `json:"freshTailMaxTokens" default:"4000"` // Optional extra cap for the fresh tail token budget

	LeafMinFanout         int `json:"leafMinFanout" default:"4"`            // Minimum depth-0 leaves before condensation
	CondensedMinFanout    int `json:"condensedMinFanout" default:"4"`       // Minimum condensed children before further condensation
	IncrementalMaxDepth   int `json:"incrementalMaxDepth" default:"2"`      // Maximum summary depth built incrementally
	LeafTargetTokens      int `json:"leafTargetTokens" default:"800"`       // Target output tokens for leaf summaries
	CondensedTargetTokens int `json:"condensedTargetTokens" default:"1200"` // Target output tokens for condensed summaries

	// LCM values are normalized via NormalizeSessionConfig at load and save.
	// Downstream reads may trust the fields directly; there is no per-site
	// NormalizeLCMConfig call required.
	LCM LCMConfig `json:"lcm"`
}

// LCMConfig controls lossless context management features layered on top of compaction.
type LCMConfig struct {
	Preset                   string  `json:"preset"`
	Enabled                  bool    `json:"enabled" default:"true"`
	SummaryInjectionMode     string  `json:"summaryInjectionMode" default:"frontier"`
	MaxInjectedSummaryTokens int     `json:"maxInjectedSummaryTokens" default:"4000"`
	SummaryMaxOverageFactor  float64 `json:"summaryMaxOverageFactor" default:"3"`
}

// OllamaLLMConfig configures an Ollama model for LLM tasks (compaction, checkpoints)
type OllamaLLMConfig struct {
	URL            string `json:"url"`                          // Ollama API URL (e.g., "http://localhost:11434")
	Model          string `json:"model"`                        // LLM model for chat completion
	TimeoutSeconds int    `json:"timeoutSeconds" default:"120"` // Request timeout in seconds
	ContextTokens  int    `json:"contextTokens"`                // Override context window (0 = auto-detect)
}
