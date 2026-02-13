// Package tokens provides token estimation utilities using tiktoken.
package tokens

import (
	"sync"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/pkoukk/tiktoken-go"
)

// Estimator provides token estimation using tiktoken
type Estimator struct {
	encoding *tiktoken.Tiktoken
	mu       sync.RWMutex
}

// DefaultEncoding is cl100k_base, used by GPT-4 and Claude models
const DefaultEncoding = "cl100k_base"

var (
	globalEstimator     *Estimator
	globalEstimatorOnce sync.Once
)

// Get returns the global token estimator (singleton)
func Get() *Estimator {
	globalEstimatorOnce.Do(func() {
		var err error
		globalEstimator, err = New()
		if err != nil {
			L_warn("tokens: failed to create estimator, using fallback", "error", err)
			globalEstimator = &Estimator{} // fallback to char-based estimation
		}
	})
	return globalEstimator
}

// New creates a new token estimator
func New() (*Estimator, error) {
	enc, err := tiktoken.GetEncoding(DefaultEncoding)
	if err != nil {
		return nil, err
	}
	return &Estimator{encoding: enc}, nil
}

// Count returns the token count for a string.
// Falls back to chars/4 if tiktoken unavailable.
func (e *Estimator) Count(text string) int {
	if e == nil || e.encoding == nil {
		return len(text) / 4
	}

	e.mu.RLock()
	defer e.mu.RUnlock()

	tokens := e.encoding.Encode(text, nil, nil)
	return len(tokens)
}

// CountWithOverhead returns token count plus per-message overhead.
// Useful for estimating message tokens (role, structure, etc).
func (e *Estimator) CountWithOverhead(text string, overhead int) int {
	return e.Count(text) + overhead
}

// Estimate is a convenience function using the global estimator.
func Estimate(text string) int {
	return Get().Count(text)
}

// SafetyMargin accounts for tokenizer inaccuracies across different models.
// tiktoken (cl100k_base) may undercount tokens for non-OpenAI models.
// 1.2 = 20% buffer, same as OpenClaw's approach.
const SafetyMargin = 1.2

// CapMaxTokens calculates a safe max_tokens value that won't exceed context.
// Applies SafetyMargin to estimatedInput to account for tokenizer variance.
// Returns min(requestedMax, contextWindow - safeInput - buffer).
func CapMaxTokens(requestedMax, contextWindow, estimatedInput, buffer int) int {
	if contextWindow <= 0 {
		return requestedMax // No context info, use requested
	}

	// Apply safety margin to input estimate
	safeInput := int(float64(estimatedInput) * SafetyMargin)
	available := contextWindow - safeInput - buffer
	if available < 100 {
		available = 100 // Minimum output
	}

	if requestedMax > 0 && requestedMax < available {
		return requestedMax
	}
	return available
}
