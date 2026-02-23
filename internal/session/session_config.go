package session

import (
	"github.com/roelfdiedericks/goclaw/internal/paths"
)

// SessionConfig configures session management
// Note: This was previously config.SessionConfig, moved here to avoid import cycles
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

// OllamaLLMConfig configures an Ollama model for LLM tasks (compaction, checkpoints)
type OllamaLLMConfig struct {
	URL            string `json:"url"`            // Ollama API URL (e.g., "http://localhost:11434")
	Model          string `json:"model"`          // LLM model for chat completion (e.g., "qwen2.5:14b" for 128k context)
	TimeoutSeconds int    `json:"timeoutSeconds"` // Request timeout in seconds (default: 300 = 5 min)
	ContextTokens  int    `json:"contextTokens"`  // Override context window (0 = auto-detect from model)
}
