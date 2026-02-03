package transcript

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/memory"
)

// SearchOptions configures search behavior
type SearchOptions struct {
	MaxResults    int
	MinScore      float64
	VectorWeight  float64
	KeywordWeight float64
	SessionKey    string // Optional: limit to specific session
}

// DefaultSearchOptions returns sensible defaults
func DefaultSearchOptions() SearchOptions {
	return SearchOptions{
		MaxResults:    10,
		MinScore:      0.3,
		VectorWeight:  0.7,
		KeywordWeight: 0.3,
	}
}

// SearchResult represents a search result
type SearchResult struct {
	ChunkID        string    `json:"chunkId"`
	SessionKey     string    `json:"sessionKey"`
	UserID         string    `json:"userId"`
	Content        string    `json:"content"`
	TimestampStart time.Time `json:"timestampStart"`
	TimestampEnd   time.Time `json:"timestampEnd"`
	Score          float64   `json:"score"`
	MatchType      string    `json:"matchType"` // "semantic", "keyword", "hybrid"
}

// Searcher handles transcript search operations
type Searcher struct {
	db       *sql.DB
	provider memory.EmbeddingProvider
}

// NewSearcher creates a new transcript searcher
func NewSearcher(db *sql.DB, provider memory.EmbeddingProvider) *Searcher {
	return &Searcher{
		db:       db,
		provider: provider,
	}
}

// Search performs hybrid search with user scoping
func (s *Searcher) Search(ctx context.Context, query string, userID string, isOwner bool, opts SearchOptions) ([]SearchResult, error) {
	if opts.MaxResults <= 0 {
		opts.MaxResults = 10
	}
	if opts.VectorWeight <= 0 {
		opts.VectorWeight = 0.7
	}
	if opts.KeywordWeight <= 0 {
		opts.KeywordWeight = 0.3
	}

	// Use larger candidate pool for merging
	candidateLimit := opts.MaxResults * 4

	// Run keyword search
	keywordResults, err := s.keywordSearch(ctx, query, userID, isOwner, candidateLimit, opts.SessionKey)
	if err != nil {
		L_warn("transcript: keyword search failed", "error", err)
		keywordResults = make(map[string]float64)
	}

	// Run vector search if provider available
	var vectorResults map[string]float64
	if s.provider != nil && s.provider.Available() {
		vectorResults, err = s.vectorSearch(ctx, query, userID, isOwner, candidateLimit, opts.SessionKey)
		if err != nil {
			L_warn("transcript: vector search failed", "error", err)
			vectorResults = make(map[string]float64)
		}
	} else {
		vectorResults = make(map[string]float64)
	}

	// Merge results
	merged := s.mergeResults(keywordResults, vectorResults, opts.VectorWeight, opts.KeywordWeight)

	// Fetch full chunk data for top results
	results, err := s.fetchChunks(ctx, merged, opts.MaxResults, opts.MinScore)
	if err != nil {
		return nil, fmt.Errorf("fetch chunks: %w", err)
	}

	L_debug("transcript: search completed",
		"query", truncateString(query, 50),
		"keywordHits", len(keywordResults),
		"vectorHits", len(vectorResults),
		"results", len(results),
	)

	return results, nil
}

// keywordSearch performs FTS5 keyword search
func (s *Searcher) keywordSearch(ctx context.Context, query string, userID string, isOwner bool, limit int, sessionKey string) (map[string]float64, error) {
	// Build FTS query
	ftsQuery := buildFTSQuery(query)
	if ftsQuery == "" {
		return make(map[string]float64), nil
	}

	// Build WHERE clause for user scoping
	whereClause := ""
	args := []interface{}{ftsQuery}

	if !isOwner && userID != "" {
		whereClause = " AND user_id = ?"
		args = append(args, userID)
	}
	if sessionKey != "" {
		whereClause += " AND session_key = ?"
		args = append(args, sessionKey)
	}

	args = append(args, limit)

	sqlQuery := fmt.Sprintf(`
		SELECT id, bm25(transcript_fts) as rank
		FROM transcript_fts
		WHERE transcript_fts MATCH ?%s
		ORDER BY rank
		LIMIT ?
	`, whereClause)

	rows, err := s.db.QueryContext(ctx, sqlQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("FTS query: %w", err)
	}
	defer rows.Close()

	results := make(map[string]float64)
	for rows.Next() {
		var id string
		var rank float64
		if err := rows.Scan(&id, &rank); err != nil {
			continue
		}
		// Convert BM25 rank to 0-1 score (BM25 returns negative values, more negative = better)
		score := 1.0 / (1.0 + math.Abs(rank))
		results[id] = score
	}

	return results, rows.Err()
}

// vectorSearch performs embedding-based semantic search
func (s *Searcher) vectorSearch(ctx context.Context, query string, userID string, isOwner bool, limit int, sessionKey string) (map[string]float64, error) {
	// Generate query embedding
	queryEmbedding, err := s.provider.EmbedQuery(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}

	// Build WHERE clause for user scoping
	whereClause := "WHERE embedding IS NOT NULL"
	var args []interface{}

	if !isOwner && userID != "" {
		whereClause += " AND user_id = ?"
		args = append(args, userID)
	}
	if sessionKey != "" {
		whereClause += " AND session_key = ?"
		args = append(args, sessionKey)
	}

	// Load all embeddings (for small-medium scale; for large scale would use sqlite-vec)
	sqlQuery := fmt.Sprintf(`
		SELECT id, embedding FROM transcript_chunks %s
	`, whereClause)

	rows, err := s.db.QueryContext(ctx, sqlQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("load embeddings: %w", err)
	}
	defer rows.Close()

	type scoredChunk struct {
		id    string
		score float64
	}
	var scored []scoredChunk

	for rows.Next() {
		var id string
		var embeddingBlob []byte
		if err := rows.Scan(&id, &embeddingBlob); err != nil {
			continue
		}

		var embedding []float32
		if err := json.Unmarshal(embeddingBlob, &embedding); err != nil {
			continue
		}

		score := cosineSimilarity(queryEmbedding, embedding)
		scored = append(scored, scoredChunk{id: id, score: score})
	}

	// Sort by score descending and take top results
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})

	results := make(map[string]float64)
	for i, s := range scored {
		if i >= limit {
			break
		}
		results[s.id] = s.score
	}

	return results, nil
}

// mergeResults combines keyword and vector results with weighted scoring
func (s *Searcher) mergeResults(keyword, vector map[string]float64, vectorWeight, keywordWeight float64) map[string]float64 {
	merged := make(map[string]float64)

	// Collect all unique IDs
	allIDs := make(map[string]bool)
	for id := range keyword {
		allIDs[id] = true
	}
	for id := range vector {
		allIDs[id] = true
	}

	// Calculate weighted scores
	for id := range allIDs {
		keyScore := keyword[id]
		vecScore := vector[id]

		// If only one method has results, use that
		if len(keyword) == 0 {
			merged[id] = vecScore
		} else if len(vector) == 0 {
			merged[id] = keyScore
		} else {
			// Weighted combination
			merged[id] = vectorWeight*vecScore + keywordWeight*keyScore
		}
	}

	return merged
}

// fetchChunks fetches full chunk data for the top scored results
func (s *Searcher) fetchChunks(ctx context.Context, scores map[string]float64, maxResults int, minScore float64) ([]SearchResult, error) {
	// Sort by score
	type scored struct {
		id    string
		score float64
	}
	var sorted []scored
	for id, score := range scores {
		if score >= minScore {
			sorted = append(sorted, scored{id: id, score: score})
		}
	}
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].score > sorted[j].score
	})

	// Take top N
	if len(sorted) > maxResults {
		sorted = sorted[:maxResults]
	}

	if len(sorted) == 0 {
		return nil, nil
	}

	// Fetch chunk data
	var results []SearchResult
	for _, sc := range sorted {
		row := s.db.QueryRowContext(ctx, `
			SELECT id, session_key, user_id, content, timestamp_start, timestamp_end
			FROM transcript_chunks WHERE id = ?
		`, sc.id)

		var chunk SearchResult
		var userID sql.NullString
		var tsStart, tsEnd int64
		if err := row.Scan(&chunk.ChunkID, &chunk.SessionKey, &userID, &chunk.Content, &tsStart, &tsEnd); err != nil {
			continue
		}

		if userID.Valid {
			chunk.UserID = userID.String
		}
		chunk.TimestampStart = time.Unix(tsStart, 0)
		chunk.TimestampEnd = time.Unix(tsEnd, 0)
		chunk.Score = sc.score

		// Determine match type
		if s.provider != nil && s.provider.Available() {
			chunk.MatchType = "hybrid"
		} else {
			chunk.MatchType = "keyword"
		}

		results = append(results, chunk)
	}

	return results, nil
}

// cosineSimilarity calculates cosine similarity between two vectors
func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}

	var dotProduct, normA, normB float64
	for i := range a {
		dotProduct += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}

	if normA == 0 || normB == 0 {
		return 0
	}

	return dotProduct / (math.Sqrt(normA) * math.Sqrt(normB))
}

// buildFTSQuery builds an FTS5 query string from user input
func buildFTSQuery(query string) string {
	// Normalize and split into words
	words := strings.Fields(strings.ToLower(query))
	if len(words) == 0 {
		return ""
	}

	// Build prefix matching query for each word
	var parts []string
	for _, word := range words {
		// Remove special characters that break FTS5
		cleaned := strings.Map(func(r rune) rune {
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
				return r
			}
			return -1
		}, word)
		if cleaned != "" {
			parts = append(parts, cleaned+"*")
		}
	}

	if len(parts) == 0 {
		return ""
	}

	return strings.Join(parts, " ")
}

// truncateString truncates a string for logging
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
