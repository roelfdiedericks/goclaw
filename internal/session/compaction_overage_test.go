package session

import (
	"strings"
	"testing"
)

func TestCapSummaryToMaxTokensPreservesExpandTrailer(t *testing.T) {
	t.Parallel()

	summary := strings.Repeat("detail ", 300) + "\n\nExpand for details about: alpha, beta"
	capped, changed, estimatedTokens := capSummaryToMaxTokens(summary, 80)

	if !changed {
		t.Fatalf("expected summary to be capped")
	}
	if estimatedTokens <= 80 {
		t.Fatalf("expected original summary to exceed cap, got %d tokens", estimatedTokens)
	}
	if !strings.Contains(capped, "[summary truncated by configured overage cap]") {
		t.Fatalf("expected cap marker in summary, got %q", capped)
	}
	if !strings.Contains(capped, "Expand for details about: alpha, beta") {
		t.Fatalf("expected expand trailer to be preserved, got %q", capped)
	}
	if got := estimateLCMTextTokens(capped); got > 80 {
		t.Fatalf("expected capped summary to fit budget, got %d tokens\n%s", got, capped)
	}
}

func TestCapSummaryToMaxTokensLeavesShortSummaryUntouched(t *testing.T) {
	t.Parallel()

	summary := "short summary\nExpand for details about: short"
	capped, changed, estimatedTokens := capSummaryToMaxTokens(summary, 80)

	if changed {
		t.Fatalf("expected short summary to remain unchanged, got %q", capped)
	}
	if capped != summary {
		t.Fatalf("expected unchanged summary, got %q", capped)
	}
	if estimatedTokens <= 0 {
		t.Fatalf("expected positive estimated token count, got %d", estimatedTokens)
	}
}
