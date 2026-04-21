package session

import (
	"strings"
	"testing"
)

// TestBuildCondensedSummaryPromptDepth1UsesPlanSpecifiedText pins the plan's
// locked depth=1 substitutions into the prompt so future paraphrases fail the
// test. Depth 1 = condensing leaves into the first condensed layer.
func TestBuildCondensedSummaryPromptDepth1UsesPlanSpecifiedText(t *testing.T) {
	t.Parallel()

	prompt := buildCondensedSummaryPrompt("SOURCE_SUMMARIES", 1, 1200)

	required := []string{
		"leaf-level conversation summaries",
		"You are preparing context for a fresh model instance that will continue this conversation.",
		"Specific references (names, paths, URLs, identifiers) needed for continuation.",
		"Transient states that are already resolved; context unchanged across the child summaries.",
		"1200",
		"SOURCE_SUMMARIES",
	}
	for _, snippet := range required {
		if !strings.Contains(prompt, snippet) {
			t.Fatalf("depth=1 prompt missing plan-locked substring %q:\n%s", snippet, prompt)
		}
	}
}

// TestBuildCondensedSummaryPromptDepth2UsesPlanSpecifiedText pins the plan's
// locked depth>=2 substitutions into the prompt. Depth 2 = condensing
// condensed nodes into deeper summaries.
func TestBuildCondensedSummaryPromptDepth2UsesPlanSpecifiedText(t *testing.T) {
	t.Parallel()

	prompt := buildCondensedSummaryPrompt("SOURCE_SUMMARIES", 2, 2000)

	required := []string{
		"multiple session-level summaries",
		"A future model should understand trajectory, not per-session minutiae.",
		"Important relationships between people, systems, or concepts; durable lessons learned.",
		"Session-local operational detail; identifiers that are no longer relevant.",
		"2000",
		"SOURCE_SUMMARIES",
	}
	for _, snippet := range required {
		if !strings.Contains(prompt, snippet) {
			t.Fatalf("depth=2 prompt missing plan-locked substring %q:\n%s", snippet, prompt)
		}
	}
}

func TestBuildCondensedSummaryPromptDefaultTargetTokens(t *testing.T) {
	t.Parallel()

	prompt := buildCondensedSummaryPrompt("x", 1, 0)
	if !strings.Contains(prompt, "1200") {
		t.Fatalf("expected default target tokens 1200 when cfg 0, got:\n%s", prompt)
	}
}
