package memorygraph

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// LiveExtractor performs interval-based memory extraction from conversations.
type LiveExtractor struct {
	manager        *Manager
	sessionsDB     *sql.DB
	config         LiveExtractionConfig
	extractionLoop *ExtractionLoop

	stopChan chan struct{}
	syncChan chan struct{}
	syncing  atomic.Bool
	wg       sync.WaitGroup

	// Stats
	lastSync          time.Time
	memoriesExtracted int
}

// ConversationBatch groups messages by session for extraction.
type ConversationBatch struct {
	Username         string
	Channel          string
	SessionKey       string
	Content          string
	MessageIDs       []string
	FirstMessageTime time.Time // Timestamp of the first message in the batch
}

// NewLiveExtractor creates a new live extractor.
func NewLiveExtractor(mgr *Manager, sessionsDB *sql.DB, cfg LiveExtractionConfig) *LiveExtractor {
	return &LiveExtractor{
		manager:    mgr,
		sessionsDB: sessionsDB,
		config:     cfg,
		stopChan:   make(chan struct{}),
		syncChan:   make(chan struct{}, 1),
	}
}

// Start begins the extraction loop.
func (e *LiveExtractor) Start() {
	// Create extraction loop
	loop, err := NewExtractionLoop(e.manager)
	if err != nil {
		L_error("live extractor: failed to create extraction loop", "error", err)
		return
	}
	e.extractionLoop = loop

	e.wg.Add(1)
	go e.loop()
}

// Stop halts the extraction loop.
func (e *LiveExtractor) Stop() {
	close(e.stopChan)
	e.wg.Wait()
}

// TriggerSync requests an immediate sync.
func (e *LiveExtractor) TriggerSync() {
	select {
	case e.syncChan <- struct{}{}:
	default:
	}
}

// UpdateConfig updates the live extraction configuration.
func (e *LiveExtractor) UpdateConfig(cfg LiveExtractionConfig) {
	e.config = cfg
	L_debug("live extractor: config updated",
		"enabled", cfg.Enabled,
		"intervalSeconds", cfg.IntervalSeconds,
		"minMessages", cfg.MinMessages,
	)
}

func (e *LiveExtractor) loop() {
	defer e.wg.Done()

	interval := time.Duration(e.config.IntervalSeconds) * time.Second
	if interval < 30*time.Second {
		interval = 30 * time.Second
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Initial sync after short delay
	time.AfterFunc(10*time.Second, func() { e.TriggerSync() })

	for {
		select {
		case <-e.stopChan:
			return
		case <-ticker.C:
			e.runSync()
		case <-e.syncChan:
			e.runSync()
		}
	}
}

func (e *LiveExtractor) runSync() {
	if e.syncing.Load() {
		return
	}
	e.syncing.Store(true)
	defer e.syncing.Store(false)

	if e.extractionLoop == nil {
		L_warn("live extractor: extraction loop not initialized")
		return
	}

	ctx := context.Background()

	// Get conversations with unextracted messages
	conversations := e.getUnextractedConversations(ctx)
	if len(conversations) == 0 {
		L_debug("live extractor: no new conversations")
		return
	}

	for _, conv := range conversations {
		if len(conv.MessageIDs) < e.config.MinMessages {
			continue // Not enough new messages yet
		}

		ec := LoopExtractionInput{
			Username:         conv.Username,
			Channel:          conv.Channel,
			SessionKey:       conv.SessionKey,
			Conversation:     conv.Content,
			MessageIDs:       conv.MessageIDs,
			SourceType:       "live",
			ConversationTime: conv.FirstMessageTime,
		}

		result, err := e.extractionLoop.Run(ctx, ec)
		if err != nil {
			L_warn("live extraction failed", "session", conv.SessionKey, "error", err)
			continue
		}

		// Mark messages as extracted in ingestion_state
		for _, msgID := range conv.MessageIDs {
			_ = setIngestionState(e.manager.db, &IngestionState{
				SourceType:  "live",
				SourcePath:  msgID,
				ContentHash: HashContent(conv.Content),
				IngestedAt:  time.Now(),
				MemoryCount: result.MemoriesSaved,
			})
		}

		e.memoriesExtracted += result.MemoriesSaved

		L_info("live extraction completed",
			"session", conv.SessionKey,
			"messages", len(conv.MessageIDs),
			"recalls", result.Recalls,
			"memories", result.MemoriesSaved,
		)
	}

	e.lastSync = time.Now()
}

// getUnextractedConversations queries sessions.db for messages not yet extracted.
func (e *LiveExtractor) getUnextractedConversations(ctx context.Context) []ConversationBatch {
	if e.sessionsDB == nil {
		return nil
	}

	// Get excluded sources from config, or use defaults
	excludeSources := e.config.ExcludeSources
	if len(excludeSources) == 0 {
		excludeSources = DefaultExcludeSources()
	}

	// Get already-extracted message IDs from memory_graph.db (ingestion_state is in that DB, not sessions.db)
	extractedIDs := make(map[string]bool)
	extractedRows, err := e.manager.DB().QueryContext(ctx, `SELECT source_path FROM ingestion_state WHERE source_type = 'live'`)
	if err == nil {
		defer extractedRows.Close()
		for extractedRows.Next() {
			var id string
			if err := extractedRows.Scan(&id); err == nil {
				extractedIDs[id] = true
			}
		}
	}

	// Build placeholders for excluded sources
	placeholders := make([]string, len(excludeSources))
	args := make([]interface{}, len(excludeSources)+1)
	for i, src := range excludeSources {
		placeholders[i] = "?"
		args[i] = src
	}

	// Query messages from sessions.db
	// Skip excluded sources (automated/proactive messages)
	query := fmt.Sprintf(`
		SELECT m.id, m.session_key, m.role, m.content, m.user_id, m.source, m.timestamp
		FROM messages m
		WHERE m.role IN ('user', 'assistant')
		  AND m.content != ''
		  AND (m.source IS NULL OR m.source NOT IN (%s))
		ORDER BY m.session_key, m.timestamp
		LIMIT ?
	`, strings.Join(placeholders, ", "))

	args[len(excludeSources)] = e.config.BatchSize * 10
	rows, err := e.sessionsDB.QueryContext(ctx, query, args...)
	if err != nil {
		L_warn("live extractor: query failed", "error", err)
		return nil
	}
	defer rows.Close()

	// Group messages by session
	sessionMessages := make(map[string][]messageRow)
	sessionUsers := make(map[string]string)
	sessionChannels := make(map[string]string)
	sessionFirstTime := make(map[string]time.Time)

	for rows.Next() {
		var m messageRow
		var userID, source sql.NullString
		var timestamp string

		if err := rows.Scan(&m.ID, &m.SessionKey, &m.Role, &m.Content, &userID, &source, &timestamp); err != nil {
			L_warn("live extractor: scan failed", "error", err)
			continue
		}

		// Parse timestamp (RFC3339 format from sessions.db)
		if t, err := time.Parse(time.RFC3339, timestamp); err == nil {
			m.Timestamp = t
		}

		// Skip already-extracted messages
		if extractedIDs[m.ID] {
			continue
		}

		sessionMessages[m.SessionKey] = append(sessionMessages[m.SessionKey], m)

		// Track user and channel for each session
		if userID.Valid && userID.String != "" && sessionUsers[m.SessionKey] == "" {
			sessionUsers[m.SessionKey] = userID.String
		}
		if source.Valid && source.String != "" && sessionChannels[m.SessionKey] == "" {
			sessionChannels[m.SessionKey] = source.String
		}
		// Track first message time per session
		if _, ok := sessionFirstTime[m.SessionKey]; !ok && !m.Timestamp.IsZero() {
			sessionFirstTime[m.SessionKey] = m.Timestamp
		}
	}

	if err := rows.Err(); err != nil {
		L_warn("live extractor: rows error", "error", err)
		return nil
	}

	// Build conversation batches
	var batches []ConversationBatch
	for sessionKey, messages := range sessionMessages {
		if len(messages) < e.config.MinMessages {
			continue
		}

		// Limit to batch size
		if len(messages) > e.config.BatchSize {
			messages = messages[:e.config.BatchSize]
		}

		// Format conversation
		var content strings.Builder
		var messageIDs []string

		for _, m := range messages {
			fmt.Fprintf(&content, "%s: %s\n\n", m.Role, m.Content)
			messageIDs = append(messageIDs, m.ID)
		}

		batches = append(batches, ConversationBatch{
			Username:         sessionUsers[sessionKey],
			Channel:          sessionChannels[sessionKey],
			SessionKey:       sessionKey,
			Content:          content.String(),
			MessageIDs:       messageIDs,
			FirstMessageTime: sessionFirstTime[sessionKey],
		})
	}

	return batches
}

type messageRow struct {
	ID         string
	SessionKey string
	Role       string
	Content    string
	Timestamp  time.Time
}
