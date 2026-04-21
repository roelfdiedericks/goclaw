package session

import (
	"strings"
	"testing"
	"time"
)

func TestBuildLCMSummaryContextAllModePreservesAllBlocks(t *testing.T) {
	t.Parallel()

	compactions := testLCMCompactions()
	contextText, stats := BuildLCMSummaryContext(compactions, LCMSummaryInjectionModeAll, 1)

	if got := strings.Count(contextText, `<summary id="sum_`); got != 4 {
		t.Fatalf("expected all four summaries in all mode, got %d\n%s", got, contextText)
	}
	if !strings.Contains(contextText, `id="sum_cond-1"`) || !strings.Contains(contextText, `id="sum_leaf-1"`) || !strings.Contains(contextText, `id="sum_leaf-3"`) {
		t.Fatalf("expected all-mode context to preserve existing blocks, got %s", contextText)
	}
	if !strings.HasSuffix(contextText, "\n\n<fresh_tail>") {
		t.Fatalf("expected fresh tail marker, got %q", contextText)
	}
	if stats.Condensed != 1 || stats.Leaves != 3 || stats.MaxDepth != 1 {
		t.Fatalf("unexpected DAG stats: %+v", stats)
	}
}

func TestBuildLCMSummaryContextFrontierExcludesCoveredDescendants(t *testing.T) {
	t.Parallel()

	compactions := testLCMCompactions()
	contextText, _ := BuildLCMSummaryContext(compactions, LCMSummaryInjectionModeFrontier, 4096)

	if got := strings.Count(contextText, `<summary id="sum_`); got != 2 {
		t.Fatalf("expected condensed ancestor plus one uncovered leaf, got %d\n%s", got, contextText)
	}
	if !strings.Contains(contextText, `id="sum_cond-1"`) || !strings.Contains(contextText, `id="sum_leaf-3"`) {
		t.Fatalf("expected frontier to include condensed ancestor and uncovered recent leaf, got %s", contextText)
	}
	if strings.Contains(contextText, `<summary id="sum_leaf-1"`) || strings.Contains(contextText, `<summary id="sum_leaf-2"`) {
		t.Fatalf("expected covered descendant leaves to be omitted from frontier, got %s", contextText)
	}
}

// TestBuildLCMSummaryContextFrontierPrefersRecent verifies the recent-first
// fit policy: with a budget that can only accommodate one block, the newest
// frontier block is kept and older blocks are dropped. This replaces the
// earlier oldest-first behavior that hid recent summaries under tight budgets.
func TestBuildLCMSummaryContextFrontierPrefersRecent(t *testing.T) {
	t.Parallel()

	compactions := testLCMCompactions()
	byID := make(map[string]StoredCompaction, len(compactions))
	for _, comp := range compactions {
		byID[comp.ID] = comp
	}
	// Budget sized to fit exactly the newest block (leaf-3) plus fresh-tail marker.
	recentLeaf := byID["leaf-3"]
	budget := estimateLCMTextTokens(renderSummaryXML(recentLeaf, byID, map[string]int{})) + estimateLCMTextTokens("\n\n<fresh_tail>")

	contextText, _ := BuildLCMSummaryContext(compactions, LCMSummaryInjectionModeFrontier, budget)

	if got := strings.Count(contextText, `<summary id="sum_`); got != 1 {
		t.Fatalf("expected budgeted frontier to inject exactly one block, got %d\n%s", got, contextText)
	}
	if !strings.Contains(contextText, `id="sum_leaf-3"`) {
		t.Fatalf("expected budgeted frontier to keep the newest uncovered leaf, got %s", contextText)
	}
	if strings.Contains(contextText, `id="sum_cond-1"`) {
		t.Fatalf("expected older condensed ancestor to be dropped under tight budget, got %s", contextText)
	}
}

// TestBuildLCMSummaryContextFrontierOversizedGuardPicksNewest verifies that
// when even the newest single block exceeds the budget, it is still injected
// (empty-context guard) — and it is the newest one, not the oldest.
func TestBuildLCMSummaryContextFrontierOversizedGuardPicksNewest(t *testing.T) {
	t.Parallel()

	compactions := testLCMCompactions()
	contextText, _ := BuildLCMSummaryContext(compactions, LCMSummaryInjectionModeFrontier, 1)

	if got := strings.Count(contextText, `<summary id="sum_`); got != 1 {
		t.Fatalf("expected oversized guard to inject exactly one block, got %d\n%s", got, contextText)
	}
	if !strings.Contains(contextText, `id="sum_leaf-3"`) {
		t.Fatalf("expected oversized guard to inject the newest block, got %s", contextText)
	}
}

func testLCMCompactions() []StoredCompaction {
	base := time.Date(2026, time.April, 21, 12, 0, 0, 0, time.UTC)
	t0 := base
	t1 := base.Add(1 * time.Minute)
	t2 := base.Add(2 * time.Minute)
	t3 := base.Add(3 * time.Minute)
	t4 := base.Add(4 * time.Minute)
	t5 := base.Add(5 * time.Minute)
	t6 := base.Add(6 * time.Minute)

	return []StoredCompaction{
		{
			ID:                "leaf-1",
			Timestamp:         t2,
			Summary:           "leaf one summary\nExpand for details about: leaf one",
			Kind:              CompactionKindLeaf,
			Depth:             0,
			SourceMessageIDs:  []string{"msg-1", "msg-2"},
			EarliestMessageAt: &t0,
			LatestMessageAt:   &t1,
			SourceTokenCount:  400,
		},
		{
			ID:                "leaf-2",
			Timestamp:         t4,
			Summary:           "leaf two summary\nExpand for details about: leaf two",
			Kind:              CompactionKindLeaf,
			Depth:             0,
			SourceMessageIDs:  []string{"msg-3", "msg-4"},
			EarliestMessageAt: &t2,
			LatestMessageAt:   &t3,
			SourceTokenCount:  420,
		},
		{
			ID:                 "cond-1",
			Timestamp:          t5,
			Summary:            "condensed coverage over older leaves\nExpand for details about: older work",
			Kind:               CompactionKindCondensed,
			Depth:              1,
			ChildCompactionIDs: []string{"leaf-1", "leaf-2"},
			EarliestMessageAt:  &t0,
			LatestMessageAt:    &t3,
			SourceTokenCount:   820,
		},
		{
			ID:                "leaf-3",
			Timestamp:         t6,
			Summary:           strings.Repeat("recent uncovered detail ", 50) + "\nExpand for details about: recent work",
			Kind:              CompactionKindLeaf,
			Depth:             0,
			SourceMessageIDs:  []string{"msg-5", "msg-6"},
			EarliestMessageAt: &t4,
			LatestMessageAt:   &t5,
			SourceTokenCount:  460,
		},
	}
}
