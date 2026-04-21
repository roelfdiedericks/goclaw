package session

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestAppendCompactionPersistsDAGFieldsAsRawJSON asserts that a compaction
// with DAG metadata (kind/depth/source_message_ids/child_compaction_ids/
// earliest/latest timestamps/source token count) round-trips through SQLite
// and that the list fields survive as raw JSON arrays in their columns.
func TestAppendCompactionPersistsDAGFieldsAsRawJSON(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "session.db")
	store, err := NewSQLiteStore(StoreConfig{
		Type:       "sqlite",
		Path:       dbPath,
		WALMode:    true,
		LCMEnabled: true,
	})
	if err != nil {
		t.Fatalf("NewSQLiteStore failed: %v", err)
	}
	defer store.Close()

	now := time.Now().UTC().Truncate(time.Second)
	sessionKey := PrimarySession
	if err := store.CreateSession(ctx, &StoredSession{
		Key:       sessionKey,
		ID:        "session-persist",
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}

	earliest := now.Add(-30 * time.Minute)
	latest := now.Add(-5 * time.Minute)
	comp := &StoredCompaction{
		ID:                 "1790000000000_000001",
		SessionKey:         sessionKey,
		Timestamp:          now,
		Summary:            "persisted summary\nExpand for details about: round-trip",
		Kind:               CompactionKindCondensed,
		Depth:              2,
		SourceMessageIDs:   []string{"1790000000001_000001", "1790000000002_000002"},
		ChildCompactionIDs: []string{"1790000000000_0000a1", "1790000000000_0000a2"},
		EarliestMessageAt:  &earliest,
		LatestMessageAt:    &latest,
		SourceTokenCount:   2048,
		FirstKeptEntryID:   "tail-1",
	}

	if err := store.AppendCompaction(ctx, sessionKey, comp); err != nil {
		t.Fatalf("AppendCompaction failed: %v", err)
	}

	var (
		kind          string
		depth         int
		sourceIDsJSON string
		childIDsJSON  string
		earliestUnix  int64
		latestUnix    int64
		sourceTokens  int
	)
	err = store.DB().QueryRow(`
		SELECT kind, depth, source_message_ids, child_compaction_ids,
			earliest_message_at, latest_message_at, source_token_count
		FROM compactions WHERE id = ?
	`, comp.ID).Scan(&kind, &depth, &sourceIDsJSON, &childIDsJSON, &earliestUnix, &latestUnix, &sourceTokens)
	if err != nil {
		t.Fatalf("raw row query failed: %v", err)
	}

	if kind != string(CompactionKindCondensed) {
		t.Fatalf("expected raw kind=condensed, got %q", kind)
	}
	if depth != 2 {
		t.Fatalf("expected raw depth=2, got %d", depth)
	}
	if sourceTokens != 2048 {
		t.Fatalf("expected raw source_token_count=2048, got %d", sourceTokens)
	}
	if earliestUnix != earliest.Unix() || latestUnix != latest.Unix() {
		t.Fatalf("expected raw earliest/latest unix timestamps, got %d/%d", earliestUnix, latestUnix)
	}

	if !strings.HasPrefix(sourceIDsJSON, "[") || !strings.HasSuffix(sourceIDsJSON, "]") {
		t.Fatalf("expected source_message_ids to be a JSON array, got %q", sourceIDsJSON)
	}
	var decodedSourceIDs []string
	if err := json.Unmarshal([]byte(sourceIDsJSON), &decodedSourceIDs); err != nil {
		t.Fatalf("unmarshal source_message_ids: %v", err)
	}
	if len(decodedSourceIDs) != 2 || decodedSourceIDs[0] != comp.SourceMessageIDs[0] || decodedSourceIDs[1] != comp.SourceMessageIDs[1] {
		t.Fatalf("source_message_ids round-trip mismatch: %#v", decodedSourceIDs)
	}

	var decodedChildIDs []string
	if err := json.Unmarshal([]byte(childIDsJSON), &decodedChildIDs); err != nil {
		t.Fatalf("unmarshal child_compaction_ids: %v", err)
	}
	if len(decodedChildIDs) != 2 || decodedChildIDs[0] != comp.ChildCompactionIDs[0] || decodedChildIDs[1] != comp.ChildCompactionIDs[1] {
		t.Fatalf("child_compaction_ids round-trip mismatch: %#v", decodedChildIDs)
	}

	got, err := store.GetCompaction(ctx, comp.ID)
	if err != nil {
		t.Fatalf("GetCompaction failed: %v", err)
	}
	if got == nil {
		t.Fatalf("expected compaction to be readable after append")
	}
	if got.Kind != CompactionKindCondensed || got.Depth != 2 {
		t.Fatalf("unexpected kind/depth after read: kind=%q depth=%d", got.Kind, got.Depth)
	}
	if got.SourceTokenCount != 2048 {
		t.Fatalf("expected SourceTokenCount=2048 after read, got %d", got.SourceTokenCount)
	}
	if len(got.SourceMessageIDs) != 2 || len(got.ChildCompactionIDs) != 2 {
		t.Fatalf("expected 2 source and child IDs, got %d/%d", len(got.SourceMessageIDs), len(got.ChildCompactionIDs))
	}
	if got.EarliestMessageAt == nil || !got.EarliestMessageAt.Equal(earliest) {
		t.Fatalf("unexpected earliest timestamp: %v", got.EarliestMessageAt)
	}
	if got.LatestMessageAt == nil || !got.LatestMessageAt.Equal(latest) {
		t.Fatalf("unexpected latest timestamp: %v", got.LatestMessageAt)
	}
}
