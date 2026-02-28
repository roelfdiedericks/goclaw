package memorygraph

import (
	"context"
	"database/sql"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// TranscriptIngester scans transcript_chunks table for ingestion
type TranscriptIngester struct {
	db           *sql.DB // sessions.db connection
	username     string  // Filter by user_id
	minTimestamp int64   // Minimum timestamp_start (0 = no filter)
}

// NewTranscriptIngester creates a new transcript ingester
// db is the sessions.db connection containing transcript_chunks
// username filters chunks by user_id
func NewTranscriptIngester(db *sql.DB, username string) *TranscriptIngester {
	return &TranscriptIngester{
		db:       db,
		username: username,
	}
}

// NewTranscriptIngesterWithAge creates a transcript ingester with age filtering
// minTimestamp filters out chunks older than this timestamp (in milliseconds)
func NewTranscriptIngesterWithAge(db *sql.DB, username string, minTimestamp int64) *TranscriptIngester {
	return &TranscriptIngester{
		db:           db,
		username:     username,
		minTimestamp: minTimestamp,
	}
}

// Type returns the source type identifier
func (t *TranscriptIngester) Type() string {
	return "transcript"
}

// Scan queries transcript_chunks and returns items for ingestion
func (t *TranscriptIngester) Scan(ctx context.Context) (<-chan IngestItem, error) {
	ch := make(chan IngestItem)

	go func() {
		defer close(ch)

		L_info("memorygraph: scanning transcript chunks", "username", t.username, "minTimestamp", t.minTimestamp)

		// Query chunks for the specified user (also include chunks with empty user_id)
		var query string
		var args []interface{}
		if t.minTimestamp > 0 {
			query = `
				SELECT id, session_key, content, timestamp_start
				FROM transcript_chunks
				WHERE (user_id = ? OR user_id = '' OR user_id IS NULL) AND timestamp_start >= ?
				ORDER BY timestamp_start ASC
			`
			args = []interface{}{t.username, t.minTimestamp}
		} else {
			query = `
				SELECT id, session_key, content, timestamp_start
				FROM transcript_chunks
				WHERE user_id = ? OR user_id = '' OR user_id IS NULL
				ORDER BY timestamp_start ASC
			`
			args = []interface{}{t.username}
		}

		rows, err := t.db.QueryContext(ctx, query, args...)
		if err != nil {
			L_warn("memorygraph: failed to query transcript chunks", "error", err)
			return
		}
		defer rows.Close()

		count := 0
		for rows.Next() {
			select {
			case <-ctx.Done():
				return
			default:
			}

			var id, sessionKey, content string
			var timestampStart int64

			if err := rows.Scan(&id, &sessionKey, &content, &timestampStart); err != nil {
				L_warn("memorygraph: failed to scan chunk", "error", err)
				continue
			}

			// Skip empty chunks
			if content == "" {
				continue
			}

			item := IngestItem{
				SourcePath:  id, // Use chunk ID as path
				Content:     content,
				ContentHash: HashContent(content),
				Metadata: map[string]string{
					"session":   sessionKey,
					"timestamp": string(rune(timestampStart)),
					"channel":   "transcript",
				},
			}

			select {
			case ch <- item:
				count++
				L_debug("memorygraph: found transcript chunk", "id", id, "session", sessionKey)
			case <-ctx.Done():
				return
			}
		}

		if err := rows.Err(); err != nil {
			L_warn("memorygraph: row iteration error", "error", err)
		}

		L_info("memorygraph: transcript scan complete", "count", count)
	}()

	return ch, nil
}
