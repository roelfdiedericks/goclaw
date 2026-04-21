package session

import (
	"testing"
	"time"
)

func mkLeaf(id string, ts time.Time) StoredCompaction {
	return StoredCompaction{
		ID:        id,
		Timestamp: ts,
		Kind:      CompactionKindLeaf,
		Depth:     0,
	}
}

func mkCondensed(id string, ts time.Time, depth int, children ...string) StoredCompaction {
	return StoredCompaction{
		ID:                 id,
		Timestamp:          ts,
		Kind:               CompactionKindCondensed,
		Depth:              depth,
		ChildCompactionIDs: children,
	}
}

func TestPickCondensationBatchReturnsOldestLeafFanout(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	// 237 leaves, oldest-first — the shape the user actually has in prod.
	const n = 237
	compactions := make([]StoredCompaction, 0, n)
	for i := 0; i < n; i++ {
		compactions = append(compactions, mkLeaf(
			formatTestID(i),
			base.Add(time.Duration(i)*time.Minute),
		))
	}

	const leafFanout = 4
	batch, newDepth, remaining := pickCondensationBatch(compactions, leafFanout, 4, 2)
	if len(batch) != leafFanout {
		t.Fatalf("expected exactly %d leaves per batch, got %d", leafFanout, len(batch))
	}
	if newDepth != 1 {
		t.Fatalf("expected newDepth=1 for leaf batch, got %d", newDepth)
	}
	if remaining != n-leafFanout {
		t.Fatalf("expected remaining=%d, got %d", n-leafFanout, remaining)
	}
	for i, comp := range batch {
		if comp.ID != compactions[i].ID {
			t.Fatalf("batch[%d].ID = %q, want oldest %q", i, comp.ID, compactions[i].ID)
		}
	}
}

func TestPickCondensationBatchSkipsParentedLeaves(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	parentedLeaf := mkLeaf("parented-1", base.Add(1*time.Minute))
	compactions := []StoredCompaction{
		parentedLeaf,
		mkLeaf("loose-1", base.Add(2*time.Minute)),
		mkLeaf("loose-2", base.Add(3*time.Minute)),
		mkLeaf("loose-3", base.Add(4*time.Minute)),
		mkLeaf("loose-4", base.Add(5*time.Minute)),
		mkCondensed("c1", base.Add(10*time.Minute), 1, "parented-1"),
	}

	batch, newDepth, remaining := pickCondensationBatch(compactions, 4, 4, 2)
	if len(batch) != 4 {
		t.Fatalf("expected 4-leaf batch, got %d", len(batch))
	}
	if newDepth != 1 {
		t.Fatalf("expected newDepth=1, got %d", newDepth)
	}
	if remaining != 0 {
		t.Fatalf("expected remaining=0 loose leaves after batch, got %d", remaining)
	}
	for _, comp := range batch {
		if comp.ID == parentedLeaf.ID {
			t.Fatalf("batch must not include parented leaf %q", parentedLeaf.ID)
		}
	}
}

func TestPickCondensationBatchSkipsPendingLeaves(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	pending := mkLeaf("pending-1", base.Add(1*time.Minute))
	pending.NeedsSummaryRetry = true
	compactions := []StoredCompaction{
		pending,
		mkLeaf("ok-1", base.Add(2*time.Minute)),
		mkLeaf("ok-2", base.Add(3*time.Minute)),
		mkLeaf("ok-3", base.Add(4*time.Minute)),
		mkLeaf("ok-4", base.Add(5*time.Minute)),
	}

	batch, newDepth, _ := pickCondensationBatch(compactions, 4, 4, 2)
	if len(batch) != 4 {
		t.Fatalf("expected 4-leaf batch, got %d", len(batch))
	}
	if newDepth != 1 {
		t.Fatalf("expected newDepth=1, got %d", newDepth)
	}
	for _, comp := range batch {
		if comp.ID == "pending-1" {
			t.Fatalf("batch must not include pending leaf")
		}
	}
}

func TestPickCondensationBatchReturnsNilWhenBelowLeafFanout(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	compactions := []StoredCompaction{
		mkLeaf("a", base.Add(1*time.Minute)),
		mkLeaf("b", base.Add(2*time.Minute)),
		mkLeaf("c", base.Add(3*time.Minute)),
	}

	batch, newDepth, remaining := pickCondensationBatch(compactions, 4, 4, 2)
	if batch != nil {
		t.Fatalf("expected no batch below fanout, got %d items", len(batch))
	}
	if newDepth != 0 || remaining != 0 {
		t.Fatalf("expected zero newDepth/remaining, got newDepth=%d remaining=%d", newDepth, remaining)
	}
}

func TestPickCondensationBatchPromotesCondensedWhenLeavesExhausted(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	// Five depth-1 condensed nodes, each already covers its leaves. No loose
	// leaves left. With condensedFanout=4 and maxDepth=3, we expect the
	// oldest 4 to be promoted to depth 2.
	compactions := []StoredCompaction{
		mkLeaf("leaf-a", base.Add(1*time.Minute)),
		mkLeaf("leaf-b", base.Add(2*time.Minute)),
		mkCondensed("c1", base.Add(10*time.Minute), 1, "leaf-a", "leaf-b"),
		mkCondensed("c2", base.Add(11*time.Minute), 1),
		mkCondensed("c3", base.Add(12*time.Minute), 1),
		mkCondensed("c4", base.Add(13*time.Minute), 1),
		mkCondensed("c5", base.Add(14*time.Minute), 1),
	}

	batch, newDepth, remaining := pickCondensationBatch(compactions, 4, 4, 3)
	if len(batch) != 4 {
		t.Fatalf("expected 4-condensed batch, got %d", len(batch))
	}
	if newDepth != 2 {
		t.Fatalf("expected newDepth=2 when promoting depth-1 condensed, got %d", newDepth)
	}
	if remaining != 1 {
		t.Fatalf("expected 1 remaining depth-1 condensed after batch, got %d", remaining)
	}
	expected := []string{"c1", "c2", "c3", "c4"}
	for i, comp := range batch {
		if comp.ID != expected[i] {
			t.Fatalf("batch[%d].ID = %q, want %q", i, comp.ID, expected[i])
		}
	}
}

func TestPickCondensationBatchRespectsIncrementalMaxDepth(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	// Depth-2 condensed nodes ready to promote. With maxDepth=2, the loop
	// `for depth := 1; depth < 2` only looks at depth=1 candidates, so it
	// refuses to promote depth-2 -> depth-3. maxDepth=3 would allow it.
	compactions := []StoredCompaction{
		mkCondensed("c2a", base.Add(10*time.Minute), 2),
		mkCondensed("c2b", base.Add(11*time.Minute), 2),
		mkCondensed("c2c", base.Add(12*time.Minute), 2),
		mkCondensed("c2d", base.Add(13*time.Minute), 2),
	}

	batch, _, _ := pickCondensationBatch(compactions, 4, 4, 2)
	if batch != nil {
		t.Fatalf("expected no batch when maxDepth=2 bars depth-2->3 promotion, got %d", len(batch))
	}

	batch, newDepth, _ := pickCondensationBatch(compactions, 4, 4, 3)
	if len(batch) != 4 || newDepth != 3 {
		t.Fatalf("expected 4-element depth-2->3 promotion with maxDepth=3, got len=%d newDepth=%d", len(batch), newDepth)
	}
}

func TestPickCondensationBatchPrefersLeavesOverCondensedPromotion(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	// Both conditions satisfied: enough loose leaves AND enough depth-1
	// condensed. Leaves come first per the selection rules.
	compactions := []StoredCompaction{
		mkLeaf("l1", base.Add(1*time.Minute)),
		mkLeaf("l2", base.Add(2*time.Minute)),
		mkLeaf("l3", base.Add(3*time.Minute)),
		mkLeaf("l4", base.Add(4*time.Minute)),
		mkCondensed("c1", base.Add(10*time.Minute), 1),
		mkCondensed("c2", base.Add(11*time.Minute), 1),
		mkCondensed("c3", base.Add(12*time.Minute), 1),
		mkCondensed("c4", base.Add(13*time.Minute), 1),
	}

	batch, newDepth, _ := pickCondensationBatch(compactions, 4, 4, 3)
	if newDepth != 1 {
		t.Fatalf("expected newDepth=1 (leaf batch preferred), got %d", newDepth)
	}
	if len(batch) != 4 {
		t.Fatalf("expected 4-leaf batch, got %d", len(batch))
	}
	if batch[0].Kind != CompactionKindLeaf {
		t.Fatalf("expected leaf kind in batch, got %q", batch[0].Kind)
	}
}

func TestBuildCompactionDAGStatsCountsUnparented(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	parentedLeafA := mkLeaf("pl-a", base.Add(1*time.Minute))
	parentedLeafB := mkLeaf("pl-b", base.Add(2*time.Minute))
	pendingLeaf := mkLeaf("pending", base.Add(3*time.Minute))
	pendingLeaf.NeedsSummaryRetry = true

	looseLeaf1 := mkLeaf("l1", base.Add(4*time.Minute))
	looseLeaf2 := mkLeaf("l2", base.Add(5*time.Minute))

	parentedD1 := mkCondensed("c1-parented", base.Add(10*time.Minute), 1, "pl-a", "pl-b")
	looseD1A := mkCondensed("c1-a", base.Add(11*time.Minute), 1)
	looseD1B := mkCondensed("c1-b", base.Add(12*time.Minute), 1)
	looseD2 := mkCondensed("c2", base.Add(20*time.Minute), 2, "c1-parented")

	compactions := []StoredCompaction{
		parentedLeafA, parentedLeafB, pendingLeaf,
		looseLeaf1, looseLeaf2,
		parentedD1, looseD1A, looseD1B, looseD2,
	}

	stats := buildCompactionDAGStats(compactions)

	if stats.Leaves != 5 {
		t.Errorf("Leaves = %d, want 5 (total regardless of parent/pending state)", stats.Leaves)
	}
	if stats.Condensed != 4 {
		t.Errorf("Condensed = %d, want 4", stats.Condensed)
	}
	if stats.Pending != 1 {
		t.Errorf("Pending = %d, want 1", stats.Pending)
	}
	if stats.UnparentedLeaves != 2 {
		t.Errorf("UnparentedLeaves = %d, want 2 (parented + pending should be excluded)", stats.UnparentedLeaves)
	}
	if stats.UnparentedCondensedByDepth[1] != 2 {
		t.Errorf("UnparentedCondensedByDepth[1] = %d, want 2", stats.UnparentedCondensedByDepth[1])
	}
	if stats.UnparentedCondensedByDepth[2] != 1 {
		t.Errorf("UnparentedCondensedByDepth[2] = %d, want 1", stats.UnparentedCondensedByDepth[2])
	}
}

func TestAnnotateNextBatchHintLeafPath(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	compactions := []StoredCompaction{
		mkLeaf("l1", base.Add(1*time.Minute)),
		mkLeaf("l2", base.Add(2*time.Minute)),
		mkLeaf("l3", base.Add(3*time.Minute)),
		mkLeaf("l4", base.Add(4*time.Minute)),
	}

	m := &CompactionManager{
		config: &CompactionManagerConfig{
			LeafMinFanout:       4,
			CondensedMinFanout:  4,
			IncrementalMaxDepth: 2,
		},
	}
	stats := buildCompactionDAGStats(compactions)
	m.AnnotateNextBatchHint(&stats, compactions)

	if stats.NextBatchSize != 4 {
		t.Errorf("NextBatchSize = %d, want 4", stats.NextBatchSize)
	}
	if stats.NextBatchNewDepth != 1 {
		t.Errorf("NextBatchNewDepth = %d, want 1", stats.NextBatchNewDepth)
	}
}

func TestAnnotateNextBatchHintIdleWhenBelowFanout(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	compactions := []StoredCompaction{
		mkLeaf("l1", base.Add(1*time.Minute)),
		mkLeaf("l2", base.Add(2*time.Minute)),
	}

	m := &CompactionManager{
		config: &CompactionManagerConfig{
			LeafMinFanout:       4,
			CondensedMinFanout:  4,
			IncrementalMaxDepth: 2,
		},
	}
	stats := buildCompactionDAGStats(compactions)
	m.AnnotateNextBatchHint(&stats, compactions)

	if stats.NextBatchSize != 0 || stats.NextBatchNewDepth != 0 {
		t.Errorf("expected idle hint (0,0), got (%d,%d)", stats.NextBatchSize, stats.NextBatchNewDepth)
	}
}

func formatTestID(i int) string {
	const chars = "0123456789"
	if i == 0 {
		return "id-0"
	}
	out := make([]byte, 0, 10)
	for n := i; n > 0; n /= 10 {
		out = append([]byte{chars[n%10]}, out...)
	}
	return "id-" + string(out)
}
