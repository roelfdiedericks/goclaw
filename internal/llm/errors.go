// Package llm provides LLM provider implementations and utilities.
package llm

import (
	"fmt"
	"strings"
)

// ErrorType categorizes LLM errors for failover and user messaging decisions.
type ErrorType string

const (
	ErrorTypeUnknown         ErrorType = "unknown"
	ErrorTypeContextOverflow ErrorType = "context_overflow"
	ErrorTypeRateLimit       ErrorType = "rate_limit"
	ErrorTypeOverloaded      ErrorType = "overloaded"
	ErrorTypeAuth            ErrorType = "auth"
	ErrorTypeBilling         ErrorType = "billing"
	ErrorTypeTimeout         ErrorType = "timeout"
	ErrorTypeFormat          ErrorType = "format"
)

// IsContextOverflowError checks if an error indicates context window exceeded.
// Works across different providers (LM Studio, OpenAI, Anthropic, Ollama, etc).
func IsContextOverflowError(err error) bool {
	if err == nil {
		return false
	}
	return IsContextOverflowMessage(err.Error())
}

// IsRateLimitError checks if an error indicates rate limiting.
func IsRateLimitError(err error) bool {
	if err == nil {
		return false
	}
	return IsRateLimitMessage(err.Error())
}

// IsOverloadedError checks if an error indicates the service is overloaded.
func IsOverloadedError(err error) bool {
	if err == nil {
		return false
	}
	return IsOverloadedMessage(err.Error())
}

// IsAuthError checks if an error indicates authentication failure.
func IsAuthError(err error) bool {
	if err == nil {
		return false
	}
	return IsAuthMessage(err.Error())
}

// IsBillingError checks if an error indicates billing/payment issues.
func IsBillingError(err error) bool {
	if err == nil {
		return false
	}
	return IsBillingMessage(err.Error())
}

// IsTimeoutError checks if an error indicates a timeout.
func IsTimeoutError(err error) bool {
	if err == nil {
		return false
	}
	return IsTimeoutMessage(err.Error())
}

// IsFormatError checks if an error indicates invalid request format.
func IsFormatError(err error) bool {
	if err == nil {
		return false
	}
	return IsFormatMessage(err.Error())
}

// ClassifyError determines the error type from an error message.
// Returns ErrorTypeUnknown if the error doesn't match any known pattern.
func ClassifyError(msg string) ErrorType {
	if msg == "" {
		return ErrorTypeUnknown
	}
	// Check in order of specificity
	if IsContextOverflowMessage(msg) {
		return ErrorTypeContextOverflow
	}
	if IsRateLimitMessage(msg) {
		return ErrorTypeRateLimit
	}
	if IsOverloadedMessage(msg) {
		return ErrorTypeOverloaded
	}
	if IsBillingMessage(msg) {
		return ErrorTypeBilling
	}
	if IsAuthMessage(msg) {
		return ErrorTypeAuth
	}
	if IsTimeoutMessage(msg) {
		return ErrorTypeTimeout
	}
	if IsFormatMessage(msg) {
		return ErrorTypeFormat
	}
	return ErrorTypeUnknown
}

// IsFailoverError returns true if the error type should trigger model failover.
// Failover errors: rate_limit, auth, billing, timeout, overloaded
// Non-failover: context_overflow (needs compaction), format (session corruption), unknown
func IsFailoverError(errType ErrorType) bool {
	switch errType {
	case ErrorTypeRateLimit, ErrorTypeAuth, ErrorTypeBilling, ErrorTypeTimeout, ErrorTypeOverloaded:
		return true
	default:
		return false
	}
}

// FormatErrorForUser returns a user-friendly error message based on error type.
func FormatErrorForUser(msg string, errType ErrorType) string {
	switch errType {
	case ErrorTypeContextOverflow:
		return "Context overflow: prompt too large for the model. Try /new or wait for auto-compaction."
	case ErrorTypeRateLimit:
		return "Rate limited - too many requests. Please wait a moment and try again."
	case ErrorTypeOverloaded:
		return "The AI service is temporarily overloaded. Please try again in a moment."
	case ErrorTypeAuth:
		return "Authentication failed. Check your API key configuration."
	case ErrorTypeBilling:
		return "Billing issue with the AI provider. Check your account credits/plan."
	case ErrorTypeTimeout:
		return "Request timed out. Please try again."
	case ErrorTypeFormat:
		return "Message format error - session may be corrupted. Try /new to start fresh."
	default:
		// For unknown errors, include the original message
		return fmt.Sprintf("LLM error: %s", msg)
	}
}

// CheckResponseBodyForContextOverflow checks if the captured HTTP response body
// contains a context overflow error. Some providers (like LM Studio) return errors
// as SSE events that client libraries fail to parse properly, resulting in cryptic
// errors like "unexpected end of JSON input". This function checks the raw response.
//
// Returns: enhanced error if context overflow detected, otherwise original error.
// Deprecated: Use CheckResponseBody instead which detects all error types.
func CheckResponseBodyForContextOverflow(originalErr error, respBody []byte) error {
	return CheckResponseBody(originalErr, respBody)
}

// CheckResponseBody checks if the captured HTTP response body contains a known
// error pattern. Some providers (like LM Studio) return errors as SSE events that
// client libraries fail to parse properly, resulting in cryptic errors like
// "unexpected end of JSON input". This function checks the raw response.
//
// Returns: enhanced error with clear message if a known error pattern detected,
// otherwise returns the original error.
func CheckResponseBody(originalErr error, respBody []byte) error {
	if len(respBody) == 0 || originalErr == nil {
		return originalErr
	}

	body := string(respBody)
	errType := ClassifyError(body)

	// If we found a known error type in the response body, return a clearer error
	switch errType {
	case ErrorTypeContextOverflow:
		return fmt.Errorf("context size has been exceeded (original error: %v)", originalErr)
	case ErrorTypeRateLimit:
		return fmt.Errorf("rate limit exceeded (original error: %v)", originalErr)
	case ErrorTypeOverloaded:
		return fmt.Errorf("service overloaded (original error: %v)", originalErr)
	case ErrorTypeAuth:
		return fmt.Errorf("authentication failed (original error: %v)", originalErr)
	case ErrorTypeBilling:
		return fmt.Errorf("billing error (original error: %v)", originalErr)
	case ErrorTypeTimeout:
		return fmt.Errorf("request timed out (original error: %v)", originalErr)
	case ErrorTypeFormat:
		return fmt.Errorf("invalid request format (original error: %v)", originalErr)
	default:
		return originalErr
	}
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

// IsRateLimitMessage checks if a message indicates rate limiting.
func IsRateLimitMessage(msg string) bool {
	if msg == "" {
		return false
	}
	lower := strings.ToLower(msg)

	// HTTP 429
	if strings.Contains(lower, "429") {
		return true
	}

	// Common patterns
	if strings.Contains(lower, "rate_limit") ||
		strings.Contains(lower, "rate limit") ||
		strings.Contains(lower, "too many requests") ||
		strings.Contains(lower, "exceeded your current quota") ||
		strings.Contains(lower, "quota exceeded") ||
		strings.Contains(lower, "resource_exhausted") ||
		strings.Contains(lower, "resource has been exhausted") ||
		strings.Contains(lower, "usage limit") ||
		strings.Contains(lower, "requests per minute") ||
		strings.Contains(lower, "requests per day") {
		return true
	}

	return false
}

// IsOverloadedMessage checks if a message indicates the service is overloaded.
func IsOverloadedMessage(msg string) bool {
	if msg == "" {
		return false
	}
	lower := strings.ToLower(msg)

	// HTTP 503
	if strings.Contains(lower, "503") && (strings.Contains(lower, "service") || strings.Contains(lower, "unavailable")) {
		return true
	}

	// Common patterns
	if strings.Contains(lower, "overloaded_error") ||
		strings.Contains(lower, "overloaded") ||
		strings.Contains(lower, "server is busy") ||
		strings.Contains(lower, "temporarily unavailable") ||
		strings.Contains(lower, "capacity") {
		return true
	}

	return false
}

// IsAuthMessage checks if a message indicates authentication failure.
func IsAuthMessage(msg string) bool {
	if msg == "" {
		return false
	}
	lower := strings.ToLower(msg)

	// HTTP 401, 403
	if strings.Contains(lower, "401") || strings.Contains(lower, "403") {
		return true
	}

	// Common patterns
	if strings.Contains(lower, "invalid api key") ||
		strings.Contains(lower, "invalid_api_key") ||
		strings.Contains(lower, "incorrect api key") ||
		strings.Contains(lower, "unauthorized") ||
		strings.Contains(lower, "forbidden") ||
		strings.Contains(lower, "access denied") ||
		strings.Contains(lower, "token has expired") ||
		strings.Contains(lower, "authentication") ||
		strings.Contains(lower, "no api key found") ||
		strings.Contains(lower, "api key not found") ||
		strings.Contains(lower, "invalid credentials") {
		return true
	}

	return false
}

// IsBillingMessage checks if a message indicates billing/payment issues.
func IsBillingMessage(msg string) bool {
	if msg == "" {
		return false
	}
	lower := strings.ToLower(msg)

	// HTTP 402
	if strings.Contains(lower, "402") {
		return true
	}

	// Common patterns
	if strings.Contains(lower, "payment required") ||
		strings.Contains(lower, "insufficient credits") ||
		strings.Contains(lower, "credit balance") ||
		strings.Contains(lower, "plans & billing") ||
		strings.Contains(lower, "billing") ||
		strings.Contains(lower, "insufficient_quota") ||
		strings.Contains(lower, "account balance") {
		return true
	}

	return false
}

// IsTimeoutMessage checks if a message indicates a timeout.
func IsTimeoutMessage(msg string) bool {
	if msg == "" {
		return false
	}
	lower := strings.ToLower(msg)

	// HTTP 408, 504
	if strings.Contains(lower, "408") || strings.Contains(lower, "504") {
		return true
	}

	// Common patterns
	if strings.Contains(lower, "timeout") ||
		strings.Contains(lower, "timed out") ||
		strings.Contains(lower, "deadline exceeded") ||
		strings.Contains(lower, "context deadline exceeded") ||
		strings.Contains(lower, "request cancelled") ||
		strings.Contains(lower, "connection reset") {
		return true
	}

	return false
}

// IsFormatMessage checks if a message indicates invalid request format.
func IsFormatMessage(msg string) bool {
	if msg == "" {
		return false
	}
	lower := strings.ToLower(msg)

	// Common patterns
	if strings.Contains(lower, "invalid request format") ||
		strings.Contains(lower, "roles must alternate") ||
		strings.Contains(lower, "incorrect role information") ||
		strings.Contains(lower, "tool_use.id") ||
		strings.Contains(lower, "messages.*.content") ||
		strings.Contains(lower, "invalid_request_error") ||
		strings.Contains(lower, "malformed") ||
		strings.Contains(lower, "schema validation") {
		return true
	}

	return false
}
