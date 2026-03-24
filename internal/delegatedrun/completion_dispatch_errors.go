package delegatedrun

import (
	"context"
	"fmt"
	"strings"
)

type CompletionDispatchErrorCode string

const (
	DispatchErrRunIDRequired               CompletionDispatchErrorCode = "run_id_required"
	DispatchErrAdapterMisconfigured        CompletionDispatchErrorCode = "adapter_misconfigured"
	DispatchErrPrimaryPathNone             CompletionDispatchErrorCode = "primary_path_none"
	DispatchErrFallbackDuplicatesPrimary   CompletionDispatchErrorCode = "fallback_duplicates_primary"
	DispatchErrPathIneligible              CompletionDispatchErrorCode = "path_ineligible"
	DispatchErrDirectChannelUnreachable    CompletionDispatchErrorCode = "direct_channel_unreachable"
	DispatchErrPathUnavailable             CompletionDispatchErrorCode = "path_unavailable"
	DispatchErrUnknownPath                 CompletionDispatchErrorCode = "unknown_path"
	DispatchErrPathFailed                  CompletionDispatchErrorCode = "path_failed"
)

// CompletionDispatchError carries retryability classification for completion dispatch failures.
type CompletionDispatchError struct {
	Code      CompletionDispatchErrorCode
	Path      DispatchPath
	Retryable bool
	Detail    string
	Cause     error
}

func (e *CompletionDispatchError) Error() string {
	parts := make([]string, 0, 4)
	if strings.TrimSpace(string(e.Code)) != "" {
		parts = append(parts, "code="+string(e.Code))
	}
	if e.Path != "" && e.Path != DispatchPathNone {
		parts = append(parts, "path="+string(e.Path))
	}
	if strings.TrimSpace(e.Detail) != "" {
		parts = append(parts, "detail="+strings.TrimSpace(e.Detail))
	}
	if e.Cause != nil {
		parts = append(parts, "cause="+e.Cause.Error())
	}
	if len(parts) == 0 {
		return "completion dispatch error"
	}
	return "completion dispatch error: " + strings.Join(parts, " ")
}

func (e *CompletionDispatchError) Unwrap() error { return e.Cause }

func NewCompletionDispatchError(code CompletionDispatchErrorCode, path DispatchPath, retryable bool, detail string, cause error) error {
	return &CompletionDispatchError{
		Code:      code,
		Path:      path,
		Retryable: retryable,
		Detail:    strings.TrimSpace(detail),
		Cause:     cause,
	}
}

func NewNonRetryableDispatchError(code CompletionDispatchErrorCode, path DispatchPath, detail string, cause error) error {
	return NewCompletionDispatchError(code, path, false, detail, cause)
}

func WrapDispatchPathError(path DispatchPath, err error) error {
	if err == nil {
		return nil
	}
	var classified *CompletionDispatchError
	if AsCompletionDispatchError(err, &classified) {
		return err
	}
	return NewCompletionDispatchError(DispatchErrPathFailed, path, true, "dispatch path execution failed", err)
}

func AsCompletionDispatchError(err error, target **CompletionDispatchError) bool {
	if err == nil {
		return false
	}
	current := err
	for current != nil {
		if typed, ok := current.(*CompletionDispatchError); ok {
			*target = typed
			return true
		}
		type unwrapper interface {
			Unwrap() error
		}
		u, ok := current.(unwrapper)
		if !ok {
			return false
		}
		current = u.Unwrap()
	}
	return false
}

func IsRetryableCompletionDispatchError(err error) bool {
	if err == nil {
		return false
	}
	var classified *CompletionDispatchError
	if AsCompletionDispatchError(err, &classified) {
		return classified.Retryable
	}
	// Context cancellations should never be retried.
	if err == context.Canceled || err == context.DeadlineExceeded {
		return false
	}
	return true
}

func FormatPrimaryFallbackDispatchError(primary DispatchPath, primaryErr error, fallback DispatchPath, fallbackErr error) error {
	detail := fmt.Sprintf("primary=%s fallback=%s", primary, fallback)
	return NewCompletionDispatchError(
		DispatchErrPathFailed,
		fallback,
		IsRetryableCompletionDispatchError(primaryErr) || IsRetryableCompletionDispatchError(fallbackErr),
		detail,
		fmt.Errorf("primary=%s: %w; fallback=%s: %w", primary, primaryErr, fallback, fallbackErr),
	)
}
