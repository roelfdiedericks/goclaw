package commands

import (
	"context"
	"time"

	"github.com/roelfdiedericks/goclaw/internal/session"
)

// SessionProvider provides session information for commands
type SessionProvider interface {
	GetSessionInfoForCommands(ctx context.Context, sessionKey string) (*SessionInfo, error)
	ForceCompact(ctx context.Context, sessionKey string) (*session.CompactionResult, error)
	ResetSession(sessionKey string) error
	CleanOrphanedToolMessages(ctx context.Context, sessionKey string) (int, error)
	GetCompactionStatus(ctx context.Context) session.CompactionStatus
	GetSkillsStatusSection() string
	GetSkillsListForCommand() *SkillsListResult
	TriggerHeartbeat(ctx context.Context) error

	// Emergency stop
	StopAllUserSessions(userID string) (int, error)

	// HASS commands
	GetHassInfo() *HassInfo
	SetHassDebug(enabled bool)
	ListHassSubscriptions() []HassSubscriptionInfo

	// LLM provider commands
	GetLLMProviderStatus() *LLMProviderStatusResult
	ResetLLMCooldowns() int

	// Embeddings commands
	GetEmbeddingsStatus() *EmbeddingsStatusResult
	TriggerEmbeddingsRebuild() error
}

// SkillsListResult contains skill listing for /skills command
type SkillsListResult struct {
	Total       int
	Eligible    int
	Ineligible  int
	Flagged     int
	Whitelisted int
	Skills      []SkillInfo
}

// SkillInfo contains info about a single skill
type SkillInfo struct {
	Name        string
	Description string
	Emoji       string
	Source      string // "bundled", "managed", "workspace"
	Status      string // "ready", "ineligible", "flagged"
	Reason      string // Why ineligible/flagged
}

// SessionInfo contains session status
type SessionInfo struct {
	SessionKey      string
	Messages        int
	TotalTokens     int
	MaxTokens       int
	UsagePercent    float64
	CompactionCount int
	LastCompaction  *session.StoredCompaction
}

// CommandResult contains the result of a command execution
type CommandResult struct {
	Text     string // Plain text output
	Markdown string // Markdown formatted output
	Error    error  // Error if command failed
	ExitCode int    // For CLI usage (0 = success)
}

// HassInfo contains Home Assistant connection status
type HassInfo struct {
	Configured    bool
	State         string // "disconnected", "connecting", "connected"
	Endpoint      string
	Uptime        time.Duration
	LastError     string
	Reconnects    int
	Subscriptions int
	Debug         bool
}

// HassSubscriptionInfo contains info about a HASS subscription
type HassSubscriptionInfo struct {
	ID       string
	Pattern  string
	Regex    string
	Prompt   string
	Wake     bool
	Interval int
	Debounce int
	Enabled  bool
}

// LLMProviderStatusResult contains status of all LLM providers
type LLMProviderStatusResult struct {
	Providers          []LLMProviderInfo
	AgentChain         []string
	SummarizationChain []string
}

// LLMProviderInfo contains info about a single LLM provider
type LLMProviderInfo struct {
	Alias      string
	InCooldown bool
	Until      time.Time
	Reason     string
	ErrorCount int
}

// EmbeddingsStatusResult contains embeddings status info
type EmbeddingsStatusResult struct {
	Configured             bool
	PrimaryModel           string
	AutoRebuild            bool
	TranscriptTotal        int
	TranscriptPrimary      int
	TranscriptNeedsRebuild int
	MemoryTotal            int
	MemoryPrimary          int
	MemoryNeedsRebuild     int
	Models                 []EmbeddingsModelInfo
}

// EmbeddingsModelInfo contains info about embedding model usage
type EmbeddingsModelInfo struct {
	Model     string
	Count     int
	IsPrimary bool
}
