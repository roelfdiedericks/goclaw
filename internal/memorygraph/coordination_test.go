package memorygraph

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/roelfdiedericks/goclaw/internal/types"
	"github.com/roelfdiedericks/goclaw/internal/user"
	_ "github.com/mattn/go-sqlite3"
)

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	cfg := Config{
		Enabled: true,
		DBPath:  filepath.Join(t.TempDir(), "memory_graph.db"),
		LiveExtraction: LiveExtractionConfig{
			Enabled:             true,
			AgentExtraction:     true,
			IntervalSeconds:     120,
			HandoffDelaySeconds: 90,
			MinMessages:         1,
			BatchSize:           50,
		},
	}
	mgr, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}
	return mgr
}

func newSessionsDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("failed to open sessions db: %v", err)
	}
	schema := `
	CREATE TABLE messages (
		id TEXT PRIMARY KEY,
		session_key TEXT,
		role TEXT,
		content TEXT,
		user_id TEXT,
		source TEXT,
		timestamp TEXT
	);`
	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("failed to init sessions schema: %v", err)
	}
	return db
}

func insertSessionMessage(t *testing.T, db *sql.DB, id, sessionKey, role, content, userID, source string, ts time.Time) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO messages (id, session_key, role, content, user_id, source, timestamp) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, sessionKey, role, content, userID, source, ts.Format(time.RFC3339)); err != nil {
		t.Fatalf("failed to insert session message: %v", err)
	}
}

func TestStoreToolMarksAgentIngestionStateAndSourceMessage(t *testing.T) {
	mgr := newTestManager(t)
	defer mgr.Close()

	tool := NewStoreTool(mgr)
	ctx := types.WithSessionContext(context.Background(), &types.SessionContext{
		Channel:           "telegram",
		SessionKey:        "session-1",
		User:              &user.User{ID: "rodent", Role: user.RoleOwner},
		CurrentMessageIDs: []string{"msg-1", "msg-2"},
	})
	res, err := tool.Execute(ctx, json.RawMessage(`{
		"content":"User prefers simpler setups for notifications.",
		"memory_type":"preference",
		"reasoning":"This is a durable user preference."
	}`))
	if err != nil {
		t.Fatalf("unexpected execute error: %v", err)
	}
	if res == nil || res.GetText() == "" {
		t.Fatalf("expected store result text")
	}

	state1, err := getIngestionState(mgr.DB(), "agent", "msg-1")
	if err != nil || state1 == nil {
		t.Fatalf("expected ingestion state for msg-1, got state=%#v err=%v", state1, err)
	}
	state2, err := getIngestionState(mgr.DB(), "agent", "msg-2")
	if err != nil || state2 == nil {
		t.Fatalf("expected ingestion state for msg-2, got state=%#v err=%v", state2, err)
	}

	rows, err := mgr.DB().Query(`SELECT source_message FROM memories ORDER BY id DESC LIMIT 1`)
	if err != nil {
		t.Fatalf("failed to query memory source_message: %v", err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatalf("expected created memory row")
	}
	var sourceMessage string
	if err := rows.Scan(&sourceMessage); err != nil {
		t.Fatalf("failed to scan source_message: %v", err)
	}
	if sourceMessage != "msg-1,msg-2" {
		t.Fatalf("expected source_message to contain both IDs, got %q", sourceMessage)
	}
}

func TestLiveExtractorSkipsAgentMarkedMessages(t *testing.T) {
	mgr := newTestManager(t)
	defer mgr.Close()
	sessionsDB := newSessionsDB(t)
	defer sessionsDB.Close()

	oldTime := time.Now().Add(-5 * time.Minute)
	insertSessionMessage(t, sessionsDB, "msg-1", "session-a", "user", "Important user preference", "rodent", "telegram", oldTime)
	insertSessionMessage(t, sessionsDB, "msg-2", "session-a", "assistant", "Thanks, I'll remember that.", "rodent", "telegram", oldTime.Add(time.Second))

	if err := setIngestionState(mgr.DB(), &IngestionState{
		SourceType:  "agent",
		SourcePath:  "msg-1",
		ContentHash: HashContent("Important user preference"),
		IngestedAt:  time.Now(),
		MemoryCount: 1,
	}); err != nil {
		t.Fatalf("failed to seed agent ingestion state msg-1: %v", err)
	}
	if err := setIngestionState(mgr.DB(), &IngestionState{
		SourceType:  "agent",
		SourcePath:  "msg-2",
		ContentHash: HashContent("Thanks, I'll remember that."),
		IngestedAt:  time.Now(),
		MemoryCount: 1,
	}); err != nil {
		t.Fatalf("failed to seed agent ingestion state msg-2: %v", err)
	}

	extractor := NewLiveExtractor(mgr, sessionsDB, mgr.Config().LiveExtraction)
	batches := extractor.getUnextractedConversations(context.Background())
	if len(batches) != 0 {
		t.Fatalf("expected no batches because messages were already agent-marked, got %#v", batches)
	}
}

func TestLiveExtractorRespectsHandoffDelayForRecentMessages(t *testing.T) {
	mgr := newTestManager(t)
	defer mgr.Close()
	sessionsDB := newSessionsDB(t)
	defer sessionsDB.Close()

	recent := time.Now().Add(-30 * time.Second)
	insertSessionMessage(t, sessionsDB, "msg-recent-1", "session-b", "user", "Recent message one", "rodent", "telegram", recent)
	insertSessionMessage(t, sessionsDB, "msg-recent-2", "session-b", "assistant", "Recent message two", "rodent", "telegram", recent.Add(time.Second))

	cfg := mgr.Config().LiveExtraction
	cfg.MinMessages = 1
	cfg.HandoffDelaySeconds = 90
	extractor := NewLiveExtractor(mgr, sessionsDB, cfg)
	batches := extractor.getUnextractedConversations(context.Background())
	if len(batches) != 0 {
		t.Fatalf("expected recent messages to be delayed from background extraction, got %#v", batches)
	}

	cfg.HandoffDelaySeconds = 0
	extractor = NewLiveExtractor(mgr, sessionsDB, cfg)
	batches = extractor.getUnextractedConversations(context.Background())
	if len(batches) != 1 {
		t.Fatalf("expected one batch with zero handoff delay, got %#v", batches)
	}
}

