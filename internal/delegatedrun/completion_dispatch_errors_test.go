package delegatedrun

import (
	"errors"
	"testing"
)

func TestIsRetryableCompletionDispatchError_Classified(t *testing.T) {
	nonRetry := NewNonRetryableDispatchError(DispatchErrPathUnavailable, DispatchPathDirect, "channel delivery unavailable", nil)
	if IsRetryableCompletionDispatchError(nonRetry) {
		t.Fatalf("expected non-retryable for classified non-retry error")
	}

	retry := NewCompletionDispatchError(DispatchErrPathFailed, DispatchPathQueue, true, "temporary failure", nil)
	if !IsRetryableCompletionDispatchError(retry) {
		t.Fatalf("expected retryable for classified retry error")
	}
}

func TestWrapDispatchPathError_PreservesClassification(t *testing.T) {
	typed := NewNonRetryableDispatchError(DispatchErrDirectChannelUnreachable, DispatchPathDirect, "offline", nil)
	wrapped := WrapDispatchPathError(DispatchPathDirect, typed)
	if wrapped == nil {
		t.Fatalf("expected wrapped error")
	}

	var out *CompletionDispatchError
	if !AsCompletionDispatchError(wrapped, &out) {
		t.Fatalf("expected completion dispatch error type")
	}
	if out.Code != DispatchErrDirectChannelUnreachable {
		t.Fatalf("expected code %s, got %s", DispatchErrDirectChannelUnreachable, out.Code)
	}
	if out.Retryable {
		t.Fatalf("expected non-retryable wrapped typed error")
	}
}

func TestWrapDispatchPathError_DefaultsToRetryableForUnknownErrors(t *testing.T) {
	raw := errors.New("temporary transport failure")
	wrapped := WrapDispatchPathError(DispatchPathQueue, raw)

	var out *CompletionDispatchError
	if !AsCompletionDispatchError(wrapped, &out) {
		t.Fatalf("expected completion dispatch error type")
	}
	if out.Code != DispatchErrPathFailed {
		t.Fatalf("expected path_failed code, got %s", out.Code)
	}
	if !out.Retryable {
		t.Fatalf("expected retryable classification for unknown error")
	}
}
