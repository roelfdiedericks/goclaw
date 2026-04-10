package localllm

import (
	"context"
	"testing"
)

func TestLatestRuntimeVersionUsesInjectedFetcher(t *testing.T) {
	orig := fetchLatestVersionFunc
	t.Cleanup(func() {
		fetchLatestVersionFunc = orig
	})

	fetchLatestVersionFunc = func(_ context.Context) (string, error) {
		return "b9999", nil
	}

	got, err := LatestRuntimeVersion(context.Background())
	if err != nil {
		t.Fatalf("LatestRuntimeVersion returned error: %v", err)
	}
	if got != "b9999" {
		t.Fatalf("expected injected version, got %q", got)
	}
}
