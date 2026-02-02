// Package commands provides unified command handling across all channels.
package commands

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/roelfdiedericks/goclaw/internal/session"
)

// SessionProvider provides session information
type SessionProvider interface {
	GetSessionInfoForCommands(ctx context.Context, sessionKey string) (*SessionInfo, error)
	ForceCompact(ctx context.Context, sessionKey string) (*session.CompactionResult, error)
	ResetSession(sessionKey string) error
	GetCompactionStatus(ctx context.Context) session.CompactionStatus
}

// SessionInfo contains session status (mirrors gateway.SessionInfo)
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
	Markdown string // Markdown formatted output (for Telegram, etc.)
	Error    error  // Error if command failed
}

// Handler handles slash commands across all channels
type Handler struct {
	provider SessionProvider
}

// NewHandler creates a new command handler
func NewHandler(provider SessionProvider) *Handler {
	return &Handler{
		provider: provider,
	}
}

// Execute runs a command and returns the result
func (h *Handler) Execute(ctx context.Context, cmd string, sessionKey string) *CommandResult {
	// Normalize command
	cmd = strings.TrimSpace(strings.ToLower(cmd))

	switch cmd {
	case "/status":
		return h.handleStatus(ctx, sessionKey)
	case "/compact":
		return h.handleCompact(ctx, sessionKey)
	case "/clear", "/reset":
		return h.handleClear(sessionKey)
	case "/help":
		return h.handleHelp()
	default:
		return &CommandResult{
			Text:     fmt.Sprintf("Unknown command: %s\nType /help for available commands.", cmd),
			Markdown: fmt.Sprintf("Unknown command: `%s`\nType /help for available commands.", cmd),
		}
	}
}

// handleStatus returns session status and compaction health
func (h *Handler) handleStatus(ctx context.Context, sessionKey string) *CommandResult {
	info, err := h.provider.GetSessionInfoForCommands(ctx, sessionKey)
	if err != nil {
		return &CommandResult{
			Text:  fmt.Sprintf("Error getting session info: %s", err),
			Error: err,
		}
	}

	compStatus := h.provider.GetCompactionStatus(ctx)

	// Build plain text output
	var text strings.Builder
	text.WriteString("Session Status\n")
	text.WriteString(fmt.Sprintf("  Messages: %d\n", info.Messages))
	text.WriteString(fmt.Sprintf("  Tokens: %d / %d (%.1f%%)\n", info.TotalTokens, info.MaxTokens, info.UsagePercent))
	text.WriteString(fmt.Sprintf("  Compactions: %d\n", info.CompactionCount))

	text.WriteString("\nCompaction Health\n")
	if compStatus.OllamaConfigured {
		ollamaHealth := "healthy"
		if compStatus.UsingFallback {
			ollamaHealth = "degraded"
		} else if !compStatus.OllamaAvailable {
			ollamaHealth = "unavailable"
		}
		text.WriteString(fmt.Sprintf("  Ollama: %s (%d/%d failures)\n",
			ollamaHealth, compStatus.OllamaFailures, compStatus.OllamaThreshold))

		if compStatus.UsingFallback {
			text.WriteString("  Mode: fallback to main model\n")
			if compStatus.MinutesUntilReset > 0 {
				text.WriteString(fmt.Sprintf("  Reset in: %d min\n", compStatus.MinutesUntilReset))
			}
		} else {
			text.WriteString("  Mode: normal\n")
		}

		if !compStatus.LastOllamaAttempt.IsZero() {
			ago := time.Since(compStatus.LastOllamaAttempt)
			text.WriteString(fmt.Sprintf("  Last attempt: %s ago\n", formatDuration(ago)))
		}
	} else {
		text.WriteString("  Ollama: not configured\n")
		text.WriteString("  Mode: main model only\n")
	}

	if compStatus.PendingRetries > 0 {
		text.WriteString(fmt.Sprintf("  Pending retries: %d\n", compStatus.PendingRetries))
	}

	if compStatus.RetryInProgress {
		text.WriteString("  Status: compaction in progress\n")
	}

	// Build markdown output
	var md strings.Builder
	md.WriteString("*Session Status*\n")
	md.WriteString(fmt.Sprintf("Messages: %d\n", info.Messages))
	md.WriteString(fmt.Sprintf("Tokens: %d / %d (%.1f%%)\n", info.TotalTokens, info.MaxTokens, info.UsagePercent))
	md.WriteString(fmt.Sprintf("Compactions: %d\n", info.CompactionCount))

	md.WriteString("\n*Compaction Health*\n")
	if compStatus.OllamaConfigured {
		ollamaHealth := "healthy"
		if compStatus.UsingFallback {
			ollamaHealth = "degraded"
		} else if !compStatus.OllamaAvailable {
			ollamaHealth = "unavailable"
		}
		md.WriteString(fmt.Sprintf("Ollama: %s (%d/%d failures)\n",
			ollamaHealth, compStatus.OllamaFailures, compStatus.OllamaThreshold))

		if compStatus.UsingFallback {
			md.WriteString("Mode: _fallback to main model_\n")
			if compStatus.MinutesUntilReset > 0 {
				md.WriteString(fmt.Sprintf("Reset in: %d min\n", compStatus.MinutesUntilReset))
			}
		} else {
			md.WriteString("Mode: normal\n")
		}
	} else {
		md.WriteString("Ollama: _not configured_\n")
		md.WriteString("Mode: main model only\n")
	}

	if compStatus.PendingRetries > 0 {
		md.WriteString(fmt.Sprintf("Pending retries: %d\n", compStatus.PendingRetries))
	}

	// Add last compaction info if available
	if info.LastCompaction != nil {
		text.WriteString(fmt.Sprintf("\nLast Compaction (%s)\n", info.LastCompaction.Timestamp.Format("2006-01-02 15:04")))
		text.WriteString(fmt.Sprintf("  Tokens before: %d\n", info.LastCompaction.TokensBefore))

		md.WriteString(fmt.Sprintf("\n*Last Compaction* (%s)\n", info.LastCompaction.Timestamp.Format("2006-01-02 15:04")))
		md.WriteString(fmt.Sprintf("Tokens before: %d\n", info.LastCompaction.TokensBefore))

		// Truncate summary if too long
		summary := info.LastCompaction.Summary
		if len(summary) > 500 {
			summary = summary[:500] + "..."
		}
		if summary != "" {
			text.WriteString(fmt.Sprintf("  Summary: %s\n", summary))
			md.WriteString(fmt.Sprintf("Summary: %s\n", summary))
		}
	}

	return &CommandResult{
		Text:     text.String(),
		Markdown: md.String(),
	}
}

// handleCompact forces compaction
func (h *Handler) handleCompact(ctx context.Context, sessionKey string) *CommandResult {
	result, err := h.provider.ForceCompact(ctx, sessionKey)
	if err != nil {
		return &CommandResult{
			Text:     fmt.Sprintf("Compaction failed: %s", err),
			Markdown: fmt.Sprintf("Compaction failed: `%s`", err),
			Error:    err,
		}
	}

	var text strings.Builder
	text.WriteString("Compaction completed!\n")
	text.WriteString(fmt.Sprintf("  Tokens before: %d\n", result.TokensBefore))

	source := "LLM"
	if result.FromCheckpoint {
		source = "checkpoint"
	} else if result.EmergencyTruncation {
		source = "emergency truncation"
	} else if result.UsedFallback {
		source = "fallback model"
	}
	text.WriteString(fmt.Sprintf("  Summary source: %s\n", source))

	var md strings.Builder
	md.WriteString("*Compaction completed!*\n")
	md.WriteString(fmt.Sprintf("Tokens before: %d\n", result.TokensBefore))
	md.WriteString(fmt.Sprintf("Summary source: _%s_\n", source))

	return &CommandResult{
		Text:     text.String(),
		Markdown: md.String(),
	}
}

// handleClear resets the session
func (h *Handler) handleClear(sessionKey string) *CommandResult {
	err := h.provider.ResetSession(sessionKey)
	if err != nil {
		return &CommandResult{
			Text:     fmt.Sprintf("Failed to clear session: %s", err),
			Markdown: fmt.Sprintf("Failed to clear session: `%s`", err),
			Error:    err,
		}
	}

	return &CommandResult{
		Text:     "Session cleared.",
		Markdown: "Session cleared.",
	}
}

// handleHelp returns available commands
func (h *Handler) handleHelp() *CommandResult {
	text := `Available commands:
  /status  - Show session info and compaction health
  /compact - Force context compaction
  /clear   - Clear conversation history
  /help    - Show this help`

	md := `*Available commands:*
/status - Show session info and compaction health
/compact - Force context compaction
/clear - Clear conversation history
/help - Show this help`

	return &CommandResult{
		Text:     text,
		Markdown: md,
	}
}

// IsCommand checks if text is a command
func IsCommand(text string) bool {
	return strings.HasPrefix(strings.TrimSpace(text), "/")
}

// formatDuration formats a duration in a human-readable way
func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%d sec", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%d min", int(d.Minutes()))
	}
	return fmt.Sprintf("%d hr", int(d.Hours()))
}
