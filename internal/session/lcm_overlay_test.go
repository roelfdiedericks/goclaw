package session

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRefreshSessionCompactionContextReplacesLiveLCMOverlay(t *testing.T) {
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

	now := time.Now().UTC()
	sessionKey := PrimarySession
	if err := store.CreateSession(ctx, &StoredSession{
		Key:       sessionKey,
		ID:        "session-1",
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}

	compactionID := "1776771934789_000004"
	if err := store.AppendCompaction(ctx, sessionKey, &StoredCompaction{
		ID:               compactionID,
		SessionKey:       sessionKey,
		Timestamp:        now,
		Summary:          "[Summary pending - 2 messages compacted at 13:45:34]",
		FirstKeptEntryID: "tail-1",
		TokensBefore:     19031,
		TokensAfter:      13140,
		MessagesRemoved:  2,
	}); err != nil {
		t.Fatalf("AppendCompaction failed: %v", err)
	}

	if err := store.UpdateCompactionDAG(ctx, compactionID, CompactionDAGUpdate{
		Kind:             CompactionKindLeaf,
		Depth:            0,
		SourceMessageIDs: []string{"1776771900000_000001", "1776771901000_000002"},
		SourceTokenCount: 512,
	}); err != nil {
		t.Fatalf("UpdateCompactionDAG failed: %v", err)
	}

	sess := NewSession("session-1")
	sess.Key = sessionKey
	sess.Messages = []Message{
		{
			ID:        "tail-1",
			Role:      "user",
			Content:   "Newest kept message",
			Source:    "telegram",
			Timestamp: now.Add(time.Second),
		},
	}

	if err := refreshSessionCompactionContext(ctx, sess, store, sessionKey, LCMSummaryInjectionModeFrontier, 4096); err != nil {
		t.Fatalf("refreshSessionCompactionContext failed: %v", err)
	}

	firstPass := sess.GetMessages()
	if len(firstPass) != 2 {
		t.Fatalf("expected 2 messages after first refresh, got %d", len(firstPass))
	}
	if !strings.Contains(firstPass[0].Content, `<summary id="sum_`+compactionID+`"`) {
		t.Fatalf("expected LCM summary block in first refresh, got %q", firstPass[0].Content)
	}
	if strings.Count(firstPass[0].Content, `<summary id="sum_`) != 1 {
		t.Fatalf("expected single summary block, got %q", firstPass[0].Content)
	}

	finalSummary := "final compacted summary\nExpand for details about: live refresh"
	if err := store.UpdateCompactionSummary(ctx, compactionID, finalSummary); err != nil {
		t.Fatalf("UpdateCompactionSummary failed: %v", err)
	}

	if err := refreshSessionCompactionContext(ctx, sess, store, sessionKey, LCMSummaryInjectionModeFrontier, 4096); err != nil {
		t.Fatalf("refreshSessionCompactionContext second pass failed: %v", err)
	}

	secondPass := sess.GetMessages()
	if len(secondPass) != 2 {
		t.Fatalf("expected 2 messages after second refresh, got %d", len(secondPass))
	}
	if !strings.Contains(secondPass[0].Content, "final compacted summary") || !strings.Contains(secondPass[0].Content, "Expand for details about: live refresh") {
		t.Fatalf("expected refreshed summary content, got %q", secondPass[0].Content)
	}
	if strings.Contains(secondPass[0].Content, "[Summary pending") {
		t.Fatalf("expected placeholder summary to be replaced, got %q", secondPass[0].Content)
	}
	if strings.Count(secondPass[0].Content, `<summary id="sum_`) != 1 {
		t.Fatalf("expected refreshed overlay to remain deduplicated, got %q", secondPass[0].Content)
	}
	if secondPass[1].ID != "tail-1" {
		t.Fatalf("expected kept tail message to remain after refresh, got %q", secondPass[1].ID)
	}
}
