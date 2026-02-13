package session

import (
	"fmt"
	"strings"
	"time"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// FlushInjectType determines how the memory flush prompt is delivered
type FlushInjectType string

const (
	FlushInjectSystem FlushInjectType = "system" // Inject into system prompt
	FlushInjectUser   FlushInjectType = "user"   // Inject as user message
)

// FlushThreshold defines a memory flush threshold
type FlushThreshold struct {
	Percent      int             `json:"percent"`      // Context usage percentage (e.g., 50, 75, 90)
	Prompt       string          `json:"prompt"`       // Prompt to deliver
	InjectAs     FlushInjectType `json:"injectAs"`     // "system" or "user"
	OncePerCycle bool            `json:"oncePerCycle"` // Only fire once until context drops
}

// MemoryFlushConfig holds memory flush settings
type MemoryFlushConfig struct {
	Enabled            bool             `json:"enabled"`
	ShowInSystemPrompt bool             `json:"showInSystemPrompt"` // Show context % in system prompt
	Thresholds         []FlushThreshold `json:"thresholds"`
}

// DefaultMemoryFlushConfig returns the default memory flush configuration
func DefaultMemoryFlushConfig() *MemoryFlushConfig {
	return &MemoryFlushConfig{
		Enabled:            true,
		ShowInSystemPrompt: true,
		Thresholds: []FlushThreshold{
			{
				Percent:      50,
				Prompt:       "Context at 50%. Consider noting key decisions to memory.",
				InjectAs:     FlushInjectSystem,
				OncePerCycle: true,
			},
			{
				Percent:      75,
				Prompt:       "Context at 75%. Write important context to memory/YYYY-MM-DD.md now.",
				InjectAs:     FlushInjectSystem,
				OncePerCycle: true,
			},
		{
			Percent: 90,
			Prompt: `[Context pressure: 90%] Compaction imminent.
Before responding, save important session context to memory/YYYY-MM-DD.md (create memory/ if needed).
Save: key decisions, user-shared context, current work state.
Skip: secrets, trivial details, info already in files.
After saving (or if nothing to save), respond to the user's message normally.`,
			InjectAs:     FlushInjectSystem,
			OncePerCycle: true,
		},
		},
	}
}

// MemoryFlushResult represents the result of checking memory flush thresholds
type MemoryFlushResult struct {
	ShouldFlush     bool
	Threshold       *FlushThreshold
	SystemPromptAdd string // Text to add to system prompt
	UserMessage     string // User message to inject
}

// CheckMemoryFlushThresholds checks if any memory flush thresholds should fire
func CheckMemoryFlushThresholds(sess *Session, cfg *MemoryFlushConfig) *MemoryFlushResult {
	if cfg == nil || !cfg.Enabled {
		return nil
	}

	usage := sess.GetContextUsage()
	if usage == 0 {
		return nil
	}

	// Check thresholds in descending order (highest first)
	for i := len(cfg.Thresholds) - 1; i >= 0; i-- {
		threshold := &cfg.Thresholds[i]
		thresholdPercent := float64(threshold.Percent) / 100.0

		if usage >= thresholdPercent {
			// Check if already fired this cycle
			if threshold.OncePerCycle && sess.HasFlushedThreshold(threshold.Percent) {
				continue
			}

			// This threshold should fire
			result := &MemoryFlushResult{
				ShouldFlush: true,
				Threshold:   threshold,
			}

			// Prepare the prompt with date substitution
			prompt := substitutePromptDate(threshold.Prompt)

			if threshold.InjectAs == FlushInjectUser {
				result.UserMessage = prompt
			} else {
				result.SystemPromptAdd = prompt
			}

			L_info("memory flush threshold triggered",
				"percent", threshold.Percent,
				"usage", fmt.Sprintf("%.1f%%", usage*100),
				"injectAs", threshold.InjectAs)

			return result
		}
	}

	return nil
}

// MarkThresholdFired marks a threshold as fired for a session
func MarkThresholdFired(sess *Session, percent int) {
	sess.MarkThresholdFlushed(percent)
	L_debug("marked threshold as fired", "percent", percent)
}

// ShouldResetThresholds returns true if context has dropped enough to reset thresholds
func ShouldResetThresholds(sess *Session) bool {
	// Reset if context drops below 25%
	return sess.GetContextUsage() < 0.25
}

// ResetThresholdsIfNeeded resets flushed thresholds if context has dropped
func ResetThresholdsIfNeeded(sess *Session) {
	if ShouldResetThresholds(sess) {
		sess.ResetFlushedThresholds()
		L_debug("reset memory flush thresholds due to context drop")
	}
}

// TrackFlushAction checks if the agent wrote to memory files and updates metadata
func TrackFlushAction(sess *Session, toolCalls []ToolCallInfo) bool {
	for _, tc := range toolCalls {
		if tc.Name == "write" || tc.Name == "edit" {
			if strings.HasPrefix(tc.Path, "memory/") {
				sess.FlushActioned = true
				L_info("memory flush actioned", "tool", tc.Name, "path", tc.Path)
				return true
			}
		}
	}
	return false
}

// ToolCallInfo contains minimal info about a tool call for flush tracking
type ToolCallInfo struct {
	Name string
	Path string // For write/edit tools
}

// substitutePromptDate replaces YYYY-MM-DD with today's date
func substitutePromptDate(prompt string) string {
	today := time.Now().Format("2006-01-02")
	return strings.ReplaceAll(prompt, "YYYY-MM-DD", today)
}

// BuildFlushSystemPromptHint builds a system prompt hint for pending memory flush
func BuildFlushSystemPromptHint(threshold *FlushThreshold) string {
	if threshold == nil || threshold.InjectAs != FlushInjectSystem {
		return ""
	}
	return substitutePromptDate(threshold.Prompt)
}
