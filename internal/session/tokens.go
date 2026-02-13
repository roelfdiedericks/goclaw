package session

import (
	"github.com/roelfdiedericks/goclaw/internal/tokens"
)

// TokenEstimator wraps the shared token estimator for session use
type TokenEstimator struct {
	estimator *tokens.Estimator
}

// GetTokenEstimator returns the global token estimator
func GetTokenEstimator() *TokenEstimator {
	return &TokenEstimator{estimator: tokens.Get()}
}

// NewTokenEstimator creates a new token estimator
func NewTokenEstimator() (*TokenEstimator, error) {
	est, err := tokens.New()
	if err != nil {
		return nil, err
	}
	return &TokenEstimator{estimator: est}, nil
}

// EstimateTokens estimates the token count for a string
func (e *TokenEstimator) EstimateTokens(text string) int {
	if e == nil || e.estimator == nil {
		return len(text) / 4
	}
	return e.estimator.Count(text)
}

// EstimateMessageTokens estimates tokens for a session message
func (e *TokenEstimator) EstimateMessageTokens(msg *Message) int {
	// Base overhead for message structure
	overhead := 4 // role, content wrapper, etc.

	contentTokens := e.EstimateTokens(msg.Content)

	// Add tool-related tokens if present
	if msg.ToolName != "" {
		overhead += e.EstimateTokens(msg.ToolName) + 2
	}
	if msg.ToolInput != nil {
		overhead += e.EstimateTokens(string(msg.ToolInput))
	}

	return contentTokens + overhead
}

// EstimateSessionTokens estimates total tokens for a session's messages
func (e *TokenEstimator) EstimateSessionTokens(sess *Session) int {
	sess.mu.RLock()
	defer sess.mu.RUnlock()

	total := 0
	for _, msg := range sess.Messages {
		total += e.EstimateMessageTokens(&msg)
	}
	return total
}

// ContextStats holds context usage statistics
type ContextStats struct {
	UsedTokens   int     // Current context size
	MaxTokens    int     // Model's context window
	UsagePercent float64 // UsedTokens / MaxTokens (0.0 to 1.0)
	MessageCount int     // Number of messages
	UserCount    int     // Number of user messages
}

// GetContextStats returns context statistics for a session
func GetContextStats(sess *Session) ContextStats {
	sess.mu.RLock()
	defer sess.mu.RUnlock()

	userCount := 0
	for _, msg := range sess.Messages {
		if msg.Role == "user" {
			userCount++
		}
	}

	usagePercent := float64(0)
	if sess.MaxTokens > 0 {
		usagePercent = float64(sess.TotalTokens) / float64(sess.MaxTokens)
	}

	return ContextStats{
		UsedTokens:   sess.TotalTokens,
		MaxTokens:    sess.MaxTokens,
		UsagePercent: usagePercent,
		MessageCount: len(sess.Messages),
		UserCount:    userCount,
	}
}

// FormatContextStatus formats context usage for display
func FormatContextStatus(stats ContextStats) string {
	usedK := stats.UsedTokens / 1000
	maxK := stats.MaxTokens / 1000
	percent := int(stats.UsagePercent * 100)

	if stats.MaxTokens == 0 {
		return ""
	}

	return formatContextString(usedK, maxK, percent)
}

// formatContextString creates the context status string
func formatContextString(usedK, maxK, percent int) string {
	return "[Context: " + itoa(usedK) + "k/" + itoa(maxK) + "k tokens (" + itoa(percent) + "%)]"
}

// Simple int to string without fmt
func itoa(i int) string {
	if i == 0 {
		return "0"
	}

	negative := i < 0
	if negative {
		i = -i
	}

	var buf [20]byte
	pos := len(buf)

	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}

	if negative {
		pos--
		buf[pos] = '-'
	}

	return string(buf[pos:])
}

// ShouldWarnContext returns true if context usage is at a warning threshold
func ShouldWarnContext(usagePercent float64) bool {
	return usagePercent >= 0.5
}

// GetWarningLevel returns the warning level for a context usage percentage
// Returns 0 (none), 50, 75, or 90
func GetWarningLevel(usagePercent float64) int {
	if usagePercent >= 0.9 {
		return 90
	}
	if usagePercent >= 0.75 {
		return 75
	}
	if usagePercent >= 0.5 {
		return 50
	}
	return 0
}
