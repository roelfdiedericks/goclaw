package session

import (
	"sync"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/pkoukk/tiktoken-go"
)

// TokenEstimator provides token estimation for context tracking
type TokenEstimator struct {
	encoding *tiktoken.Tiktoken
	mu       sync.RWMutex
}

// DefaultEncoding is the encoding used by Claude models
const DefaultEncoding = "cl100k_base"

// Model context window sizes (tokens)
var ModelContextWindows = map[string]int{
	"claude-3-opus-20240229":    200000,
	"claude-3-sonnet-20240229":  200000,
	"claude-3-haiku-20240307":   200000,
	"claude-3-5-sonnet-20240620": 200000,
	"claude-3-5-sonnet-20241022": 200000,
	"claude-opus-4-5":           200000,
	"claude-sonnet-4":           200000,
	// Default for unknown models
	"default": 200000,
}

var (
	globalEstimator     *TokenEstimator
	globalEstimatorOnce sync.Once
)

// GetTokenEstimator returns the global token estimator
func GetTokenEstimator() *TokenEstimator {
	globalEstimatorOnce.Do(func() {
		var err error
		globalEstimator, err = NewTokenEstimator()
		if err != nil {
			L_warn("failed to create token estimator, using fallback", "error", err)
			globalEstimator = &TokenEstimator{} // fallback to char-based estimation
		}
	})
	return globalEstimator
}

// NewTokenEstimator creates a new token estimator
func NewTokenEstimator() (*TokenEstimator, error) {
	enc, err := tiktoken.GetEncoding(DefaultEncoding)
	if err != nil {
		return nil, err
	}
	return &TokenEstimator{encoding: enc}, nil
}

// EstimateTokens estimates the token count for a string
func (e *TokenEstimator) EstimateTokens(text string) int {
	if e == nil || e.encoding == nil {
		// Fallback: roughly 4 chars per token
		return len(text) / 4
	}

	e.mu.RLock()
	defer e.mu.RUnlock()

	tokens := e.encoding.Encode(text, nil, nil)
	return len(tokens)
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

// GetContextWindow returns the context window size for a model
func GetContextWindow(modelID string) int {
	if window, ok := ModelContextWindows[modelID]; ok {
		return window
	}
	return ModelContextWindows["default"]
}

// ContextStats holds context usage statistics
type ContextStats struct {
	UsedTokens    int     // Current context size
	MaxTokens     int     // Model's context window
	UsagePercent  float64 // UsedTokens / MaxTokens (0.0 to 1.0)
	MessageCount  int     // Number of messages
	UserCount     int     // Number of user messages
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
