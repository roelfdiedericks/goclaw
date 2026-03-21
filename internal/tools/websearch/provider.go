package websearch

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"
)

const (
	providerGrok       = "grok"
	providerBrave      = "brave"
	providerPerplexity = "perplexity"
	providerGemini     = "gemini"
)

var defaultAutoProviderOrder = []string{
	providerGrok,
	providerBrave,
	providerPerplexity,
	providerGemini,
}

type SearchRequest struct {
	Query string
	Count int
}

type SearchItem struct {
	Title   string
	URL     string
	Snippet string
}

type SearchResponse struct {
	Provider  string
	Answer    string
	Items     []SearchItem
	Citations []string
}

type ProviderConfig struct {
	APIKey  string
	BaseURL string
	Model   string
}

type ProviderDriver interface {
	ID() string
	Search(ctx context.Context, req SearchRequest, cfg ProviderConfig) (SearchResponse, error)
}

// ProviderError carries enough metadata to drive retry and fallback decisions.
type ProviderError struct {
	Provider   string
	StatusCode int
	Message    string
	Retryable  bool
	Err        error
}

func (e *ProviderError) Error() string {
	if e == nil {
		return ""
	}
	base := strings.TrimSpace(e.Message)
	if e.StatusCode > 0 {
		if base == "" {
			base = fmt.Sprintf("HTTP %d", e.StatusCode)
		}
		base = fmt.Sprintf("%s (status=%d)", base, e.StatusCode)
	}
	if e.Err != nil {
		if base == "" {
			base = e.Err.Error()
		} else {
			base = base + ": " + e.Err.Error()
		}
	}
	if base == "" {
		base = "provider request failed"
	}
	return base
}

func (e *ProviderError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func newProviderHTTPError(provider string, status int, message string) error {
	retryable := status == http.StatusTooManyRequests || status == http.StatusRequestTimeout || status >= 500
	return &ProviderError{
		Provider:   provider,
		StatusCode: status,
		Message:    message,
		Retryable:  retryable,
	}
}

func newProviderError(provider, message string, retryable bool, err error) error {
	return &ProviderError{
		Provider:  provider,
		Message:   message,
		Retryable: retryable,
		Err:       err,
	}
}

func isRetryableProviderError(err error) bool {
	if err == nil {
		return false
	}

	var pErr *ProviderError
	if errors.As(err, &pErr) {
		return pErr.Retryable
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		return netErr.Timeout() || netErr.Temporary()
	}

	return errors.Is(err, context.DeadlineExceeded)
}

func normalizeCount(count int) int {
	if count <= 0 {
		return 5
	}
	if count > 20 {
		return 20
	}
	return count
}

func newHTTPClient(timeoutSeconds int) *http.Client {
	if timeoutSeconds <= 0 {
		timeoutSeconds = 30
	}
	return &http.Client{Timeout: time.Duration(timeoutSeconds) * time.Second}
}
