package delegatedrun

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

type SQLiteRegistry struct {
	db *sql.DB
}

type RunEvent struct {
	ID        int64     `json:"id"`
	RunID     string    `json:"runId"`
	EventType string    `json:"eventType"`
	Payload   any       `json:"payload"`
	Timestamp time.Time `json:"timestamp"`
}

func NewSQLiteRegistry(path string) (*SQLiteRegistry, error) {
	if path == "" {
		return nil, fmt.Errorf("sqlite registry path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		return nil, fmt.Errorf("create sqlite registry dir: %w", err)
	}
	db, err := sql.Open("sqlite3", path+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("open sqlite registry: %w", err)
	}
	r := &SQLiteRegistry{db: db}
	if err := r.ensureSchema(); err != nil {
		_ = db.Close()
		return nil, err
	}
	L_info("delegatedrun: sqlite registry opened", "path", path)
	return r, nil
}

func (r *SQLiteRegistry) Close() error {
	return r.db.Close()
}

func (r *SQLiteRegistry) ensureSchema() error {
	schema := `
	CREATE TABLE IF NOT EXISTS delegated_runs (
		run_id TEXT PRIMARY KEY,
		parent_run_id TEXT,
		requester_type TEXT,
		requester_id TEXT,
		requester_session_key TEXT,
		session_key TEXT,
		purpose TEXT,
		result_mode TEXT,
		dispatch_order TEXT,
		fallback_mode TEXT,
		inject_mode TEXT,
		completion_dispatch_key TEXT,
		completion_dispatch_seq INTEGER DEFAULT 0,
		state TEXT,
		started_at INTEGER,
		finished_at INTEGER,
		result_json TEXT
	);
	CREATE INDEX IF NOT EXISTS idx_delegated_runs_parent ON delegated_runs(parent_run_id);
	CREATE INDEX IF NOT EXISTS idx_delegated_runs_state ON delegated_runs(state);
	CREATE INDEX IF NOT EXISTS idx_delegated_runs_started ON delegated_runs(started_at);

	CREATE TABLE IF NOT EXISTS delegated_run_events (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		run_id TEXT NOT NULL,
		event_type TEXT NOT NULL,
		payload_json TEXT NOT NULL,
		ts INTEGER NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_delegated_run_events_run_id ON delegated_run_events(run_id, id);
	`
	_, err := r.db.Exec(schema)
	if err != nil {
		return fmt.Errorf("create delegated run schema: %w", err)
	}
	// Best-effort additive migrations for existing DBs.
	_ = addColumnIfMissing(r.db, "delegated_runs", "requester_session_key", "TEXT")
	_ = addColumnIfMissing(r.db, "delegated_runs", "result_mode", "TEXT")
	_ = addColumnIfMissing(r.db, "delegated_runs", "dispatch_order", "TEXT")
	_ = addColumnIfMissing(r.db, "delegated_runs", "fallback_mode", "TEXT")
	_ = addColumnIfMissing(r.db, "delegated_runs", "inject_mode", "TEXT")
	_ = addColumnIfMissing(r.db, "delegated_runs", "completion_dispatch_key", "TEXT")
	_ = addColumnIfMissing(r.db, "delegated_runs", "completion_dispatch_seq", "INTEGER DEFAULT 0")
	return nil
}

func (r *SQLiteRegistry) Create(record RunRecord) error {
	b, _ := json.Marshal(record.Result)
	_, err := r.db.Exec(
		`INSERT OR REPLACE INTO delegated_runs
		(run_id, parent_run_id, requester_type, requester_id, requester_session_key, session_key, purpose, result_mode, dispatch_order, fallback_mode, inject_mode, completion_dispatch_key, completion_dispatch_seq, state, started_at, finished_at, result_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		record.RunID,
		record.ParentRunID,
		record.RequesterType,
		record.RequesterID,
		record.RequesterSessionKey,
		record.SessionKey,
		record.Purpose,
		record.ResultMode,
		record.DispatchOrder,
		record.FallbackMode,
		record.InjectMode,
		record.CompletionDispatchKey,
		record.CompletionDispatchSeq,
		string(record.State),
		record.StartedAt.UnixMilli(),
		nil,
		string(b),
	)
	if err != nil {
		return err
	}
	return r.appendEvent(record.RunID, "started", record)
}

func (r *SQLiteRegistry) UpdateState(runID string, state RunState) error {
	_, err := r.db.Exec(`UPDATE delegated_runs SET state = ? WHERE run_id = ?`, string(state), runID)
	if err != nil {
		return err
	}
	return r.appendEvent(runID, "state", map[string]any{"state": state})
}

func (r *SQLiteRegistry) Complete(runID string, result RunResult, state RunState) error {
	b, _ := json.Marshal(result)
	now := time.Now().UnixMilli()
	_, err := r.db.Exec(
		`UPDATE delegated_runs
		SET state = ?, finished_at = ?, result_json = ?
		WHERE run_id = ?`,
		string(state), now, string(b), runID,
	)
	if err != nil {
		return err
	}
	return r.appendEvent(runID, "completed", map[string]any{"state": state, "result": result})
}

func (r *SQLiteRegistry) MarkCompletionDispatched(runID string, dispatchKey string) error {
	seq := 0
	if parsed, err := dispatchSeqFromKey(runID, dispatchKey); err == nil {
		seq = parsed
	}
	_, err := r.db.Exec(`UPDATE delegated_runs SET completion_dispatch_key = ?, completion_dispatch_seq = ? WHERE run_id = ?`, dispatchKey, seq, runID)
	if err != nil {
		return err
	}
	return r.appendEvent(runID, "dispatch_marked", map[string]any{"completionDispatchKey": dispatchKey})
}

func (r *SQLiteRegistry) AdvanceCompletionDispatchSeq(runID string) (int, error) {
	_, err := r.db.Exec(
		`UPDATE delegated_runs
		 SET completion_dispatch_seq = CASE
		   WHEN completion_dispatch_seq IS NULL OR completion_dispatch_seq <= 0 THEN 2
		   ELSE completion_dispatch_seq + 1
		 END
		 WHERE run_id = ?`,
		runID,
	)
	if err != nil {
		return 0, err
	}
	rec, ok := r.Get(runID)
	if !ok {
		return 0, ErrRunNotFound
	}
	_ = r.appendEvent(runID, "dispatch_seq_advanced", map[string]any{"completionDispatchSeq": rec.CompletionDispatchSeq})
	return rec.CompletionDispatchSeq, nil
}

func (r *SQLiteRegistry) RecordDispatchPhase(runID, phase, status, detail string) error {
	return r.appendEvent(runID, "dispatch_phase", map[string]any{
		"phase":  phase,
		"status": status,
		"detail": detail,
	})
}

func (r *SQLiteRegistry) Get(runID string) (RunRecord, bool) {
	var rec RunRecord
	var state string
	var startedAt int64
	var finishedAt sql.NullInt64
	var resultJSON sql.NullString
	err := r.db.QueryRow(
		`SELECT run_id, parent_run_id, requester_type, requester_id, requester_session_key, session_key, purpose, result_mode, dispatch_order, fallback_mode, inject_mode, completion_dispatch_key, completion_dispatch_seq, state, started_at, finished_at, result_json
		FROM delegated_runs WHERE run_id = ?`,
		runID,
	).Scan(
		&rec.RunID,
		&rec.ParentRunID,
		&rec.RequesterType,
		&rec.RequesterID,
		&rec.RequesterSessionKey,
		&rec.SessionKey,
		&rec.Purpose,
		&rec.ResultMode,
		&rec.DispatchOrder,
		&rec.FallbackMode,
		&rec.InjectMode,
		&rec.CompletionDispatchKey,
		&rec.CompletionDispatchSeq,
		&state,
		&startedAt,
		&finishedAt,
		&resultJSON,
	)
	if err != nil {
		return RunRecord{}, false
	}
	rec.State = RunState(state)
	rec.StartedAt = time.UnixMilli(startedAt)
	if finishedAt.Valid {
		t := time.UnixMilli(finishedAt.Int64)
		rec.FinishedAt = &t
	}
	if resultJSON.Valid && resultJSON.String != "" {
		_ = json.Unmarshal([]byte(resultJSON.String), &rec.Result)
	}
	return rec, true
}

func (r *SQLiteRegistry) List() []RunRecord {
	rows, err := r.db.Query(
		`SELECT run_id, parent_run_id, requester_type, requester_id, requester_session_key, session_key, purpose, result_mode, dispatch_order, fallback_mode, inject_mode, completion_dispatch_key, completion_dispatch_seq, state, started_at, finished_at, result_json
		FROM delegated_runs ORDER BY started_at DESC`,
	)
	if err != nil {
		return nil
	}
	defer rows.Close()

	out := make([]RunRecord, 0, 32)
	for rows.Next() {
		var rec RunRecord
		var state string
		var startedAt int64
		var finishedAt sql.NullInt64
		var resultJSON sql.NullString
		if err := rows.Scan(
			&rec.RunID,
			&rec.ParentRunID,
			&rec.RequesterType,
			&rec.RequesterID,
			&rec.RequesterSessionKey,
			&rec.SessionKey,
			&rec.Purpose,
			&rec.ResultMode,
			&rec.DispatchOrder,
			&rec.FallbackMode,
			&rec.InjectMode,
			&rec.CompletionDispatchKey,
			&rec.CompletionDispatchSeq,
			&state,
			&startedAt,
			&finishedAt,
			&resultJSON,
		); err != nil {
			continue
		}
		rec.State = RunState(state)
		rec.StartedAt = time.UnixMilli(startedAt)
		if finishedAt.Valid {
			t := time.UnixMilli(finishedAt.Int64)
			rec.FinishedAt = &t
		}
		if resultJSON.Valid && resultJSON.String != "" {
			_ = json.Unmarshal([]byte(resultJSON.String), &rec.Result)
		}
		out = append(out, rec)
	}
	return out
}

func addColumnIfMissing(db *sql.DB, table, column, columnType string) error {
	_, err := db.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, column, columnType))
	if err != nil {
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "duplicate column name") {
			return nil
		}
		return err
	}
	return nil
}

func (r *SQLiteRegistry) appendEvent(runID, eventType string, payload any) error {
	b, _ := json.Marshal(payload)
	_, err := r.db.Exec(
		`INSERT INTO delegated_run_events (run_id, event_type, payload_json, ts) VALUES (?, ?, ?, ?)`,
		runID, eventType, string(b), time.Now().UnixMilli(),
	)
	return err
}

func (r *SQLiteRegistry) ListEventsSince(sinceID int64, limit int) []RunEvent {
	if limit <= 0 {
		limit = 200
	}
	rows, err := r.db.Query(
		`SELECT id, run_id, event_type, payload_json, ts
		 FROM delegated_run_events
		 WHERE id > ?
		 ORDER BY id ASC
		 LIMIT ?`,
		sinceID, limit,
	)
	if err != nil {
		return nil
	}
	defer rows.Close()

	out := make([]RunEvent, 0, limit)
	for rows.Next() {
		var ev RunEvent
		var payloadJSON string
		var ts int64
		if err := rows.Scan(&ev.ID, &ev.RunID, &ev.EventType, &payloadJSON, &ts); err != nil {
			continue
		}
		ev.Timestamp = time.UnixMilli(ts)
		if payloadJSON != "" {
			var payload any
			if err := json.Unmarshal([]byte(payloadJSON), &payload); err == nil {
				ev.Payload = payload
			} else {
				ev.Payload = map[string]any{"raw": payloadJSON}
			}
		}
		out = append(out, ev)
	}
	return out
}

