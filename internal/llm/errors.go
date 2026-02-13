// Package llm provides LLM provider implementations and utilities.
package llm

import (
	"strings"
)

// IsContextOverflowError checks if an error indicates context window exceeded.
// Works across different providers (LM Studio, OpenAI, Anthropic, Ollama, etc).
func IsContextOverflowError(err error) bool {
	if err == nil {
		return false
	}
	return IsContextOverflowMessage(err.Error())
}

// IsContextOverflowMessage checks if an error message indicates context overflow.
// Use this when you have a string instead of an error.
func IsContextOverflowMessage(msg string) bool {
	if msg == "" {
		return false
	}
	lower := strings.ToLower(msg)

	// LM Studio
	if strings.Contains(lower, "context size has been exceeded") {
		return true
	}

	// OpenAI / OpenRouter
	if strings.Contains(lower, "context_length_exceeded") {
		return true
	}

	// Anthropic
	if strings.Contains(lower, "context length exceeded") {
		return true
	}

	// Common patterns
	if strings.Contains(lower, "maximum context length") ||
		strings.Contains(lower, "prompt is too long") ||
		strings.Contains(lower, "request_too_large") ||
		strings.Contains(lower, "request exceeds the maximum size") ||
		strings.Contains(lower, "exceeds model context window") ||
		strings.Contains(lower, "context overflow") ||
		strings.Contains(lower, "exceeded model token limit") { // Kimi
		return true
	}

	// HTTP 413 with size indication
	if strings.Contains(lower, "413") && strings.Contains(lower, "too large") {
		return true
	}

	// Request size + context combination
	if strings.Contains(lower, "request size exceeds") && strings.Contains(lower, "context") {
		return true
	}

	return false
}
