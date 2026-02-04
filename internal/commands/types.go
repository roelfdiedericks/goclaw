package commands

import (
	"context"

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
