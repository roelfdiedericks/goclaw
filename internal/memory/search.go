package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/roelfdiedericks/goclaw/internal/llm"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// SearchResult represents a single search result
type SearchResult struct {
	Path      string  `json:"path"`
	StartLine int     `json:"startLine"`
	EndLine   int     `json:"endLine"`
	Score     float64 `json:"score"`
	Snippet   string  `json:"snippet"`
}

// SearchOptions configures search behavior
type SearchOptions struct {
	MaxResults    int
	MinScore      float64
	VectorWeight  float64
	KeywordWeight float64
}

// DefaultSearchOptions returns default search options
func DefaultSearchOptions() SearchOptions {
	return SearchOptions{
		MaxResults:    6,
		MinScore:      0.35,
		VectorWeight:  0.7,
		KeywordWeight: 0.3,
	}
}

// searchResult is an internal result with both scores
type searchResult struct {
	ID           string
	Path         string
	StartLine    int
	EndLine      int
	Text         string
	VectorScore  float64
	KeywordScore float64
	FinalScore   float64
}

// Search performs hybrid search over the memory index
func Search(ctx context.Context, db *sql.DB, provider llm.EmbeddingProvider, query string, opts SearchOptions) ([]SearchResult, error) {
	if query == "" {
		return nil, nil
	}

	L_debug("memory: searching", "query", truncateForLog(query, 50), "maxResults", opts.MaxResults)

	// Candidate pool multiplier (search more than we need, then filter)
	candidateMultiplier := 4
	candidateLimit := opts.MaxResults * candidateMultiplier

	// Run keyword search (FTS5/BM25)
	keywordResults, err := searchKeyword(db, query, candidateLimit)
	if err != nil {
		L_warn("memory: keyword search failed", "error", err)
		// Continue with empty keyword results
		keywordResults = nil
	}

	// Run vector search if provider is available
	var vectorResults map[string]float64
	if provider != nil && provider.Available() {
		vectorResults, err = searchVector(ctx, db, provider, query, candidateLimit)
		if err != nil {
			L_warn("memory: vector search failed", "error", err)
			// Continue with empty vector results
			vectorResults = nil
		}
	}

	// Merge results
	merged := mergeResults(db, keywordResults, vectorResults, opts)

	// Filter by min score and limit
	var results []SearchResult
	for _, r := range merged {
		if r.FinalScore < opts.MinScore {
			continue
		}
		results = append(results, SearchResult{
			Path:      r.Path,
			StartLine: r.StartLine,
			EndLine:   r.EndLine,
			Score:     r.FinalScore,
			Snippet:   truncateSnippet(r.Text, snippetMaxChars),
		})
		if len(results) >= opts.MaxResults {
			break
		}
	}

	L_debug("memory: search completed",
		"query", truncateForLog(query, 30),
		"keywordHits", len(keywordResults),
		"vectorHits", len(vectorResults),
		"results", len(results),
	)

	return results, nil
}

// searchKeyword performs FTS5 keyword search with BM25 ranking
func searchKeyword(db *sql.DB, query string, limit int) (map[string]float64, error) {
	// Clean up query for FTS5
	ftsQuery := buildFTSQuery(query)
	if ftsQuery == "" {
		return nil, nil
	}

	L_trace("memory: keyword search", "ftsQuery", ftsQuery, "limit", limit)

	// FTS5 BM25 search
	// BM25 returns negative scores (lower is better), so we negate
	rows, err := db.Query(`
		SELECT id, bm25(memory_fts) as rank
		FROM memory_fts
		WHERE memory_fts MATCH ?
		ORDER BY rank
		LIMIT ?
	`, ftsQuery, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	results := make(map[string]float64)
	for rows.Next() {
		var id string
		var rank float64
		if err := rows.Scan(&id, &rank); err != nil {
			continue
		}
		// Convert BM25 rank to 0-1 score
		// BM25 returns negative values, lower (more negative) is better
		// Convert to positive score: score = 1 / (1 + abs(rank))
		score := 1.0 / (1.0 + math.Abs(rank))
		results[id] = score
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate rows: %w", err)
	}

	L_trace("memory: keyword search results", "count", len(results))
	return results, nil
}

// buildFTSQuery converts a natural query to FTS5 query syntax
func buildFTSQuery(query string) string {
	// Split into words and join with implicit AND
	words := strings.Fields(query)
	if len(words) == 0 {
		return ""
	}

	// Escape special FTS5 characters and build query
	var parts []string
	for _, word := range words {
		// Remove FTS5 special characters
		word = strings.ReplaceAll(word, "*", "")
		word = strings.ReplaceAll(word, "\"", "")
		word = strings.ReplaceAll(word, "'", "")
		word = strings.TrimSpace(word)
		if word != "" {
			// Add prefix matching for better recall
			parts = append(parts, word+"*")
		}
	}

	return strings.Join(parts, " ")
}

// searchVector performs vector similarity search
func searchVector(ctx context.Context, db *sql.DB, provider llm.EmbeddingProvider, query string, limit int) (map[string]float64, error) {
	// Generate query embedding
	queryEmbedding, err := provider.EmbedQuery(ctx, query)
	if err != nil {
		return nil, err
	}
	if queryEmbedding == nil || len(queryEmbedding) == 0 {
		return nil, nil
	}

	L_trace("memory: vector search", "queryEmbeddingDims", len(queryEmbedding), "limit", limit)

	// Load all embeddings from database
	// Note: For large datasets, this should use sqlite-vec or similar
	rows, err := db.Query(`
		SELECT id, embedding
		FROM memory_chunks
		WHERE embedding IS NOT NULL AND embedding != ''
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type embeddingEntry struct {
		id        string
		embedding []float32
	}
	var entries []embeddingEntry

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
		entries = append(entries, embeddingEntry{id: id, embedding: embedding})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate rows: %w", err)
	}

	if len(entries) == 0 {
		return nil, nil
	}

	// Compute cosine similarity for each entry
	type scored struct {
		id    string
		score float64
	}
	var scores []scored

	for _, entry := range entries {
		sim := cosineSimilarity(queryEmbedding, entry.embedding)
		if sim > 0 {
			scores = append(scores, scored{id: entry.id, score: sim})
		}
	}

	// Sort by score descending
	sort.Slice(scores, func(i, j int) bool {
		return scores[i].score > scores[j].score
	})

	// Take top N
	results := make(map[string]float64)
	for i, s := range scores {
		if i >= limit {
			break
		}
		results[s.id] = s.score
	}

	L_trace("memory: vector search results", "total", len(entries), "matched", len(results))
	return results, nil
}

// cosineSimilarity computes cosine similarity between two vectors
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

// mergeResults merges keyword and vector results with weighted scoring
func mergeResults(db *sql.DB, keywordResults, vectorResults map[string]float64, opts SearchOptions) []searchResult {
	// Collect all unique IDs
	ids := make(map[string]bool)
	for id := range keywordResults {
		ids[id] = true
	}
	for id := range vectorResults {
		ids[id] = true
	}

	// Build merged results
	var merged []searchResult

	for id := range ids {
		keywordScore := keywordResults[id]
		vectorScore := vectorResults[id]

		// Compute final score
		var finalScore float64
		if vectorResults != nil && keywordResults != nil {
			// Hybrid: weighted combination
			finalScore = opts.VectorWeight*vectorScore + opts.KeywordWeight*keywordScore
		} else if vectorResults != nil {
			// Vector only
			finalScore = vectorScore
		} else {
			// Keyword only
			finalScore = keywordScore
		}

		// Look up chunk details
		var path, text string
		var startLine, endLine int
		err := db.QueryRow(`
			SELECT path, start_line, end_line, text
			FROM memory_chunks
			WHERE id = ?
		`, id).Scan(&path, &startLine, &endLine, &text)
		if err != nil {
			continue
		}

		merged = append(merged, searchResult{
			ID:           id,
			Path:         path,
			StartLine:    startLine,
			EndLine:      endLine,
			Text:         text,
			VectorScore:  vectorScore,
			KeywordScore: keywordScore,
			FinalScore:   finalScore,
		})
	}

	// Sort by final score descending
	sort.Slice(merged, func(i, j int) bool {
		return merged[i].FinalScore > merged[j].FinalScore
	})

	return merged
}

// truncateSnippet truncates text to max chars, trying to break at word boundaries
func truncateSnippet(text string, maxChars int) string {
	if len(text) <= maxChars {
		return text
	}

	// Find last space before maxChars
	truncated := text[:maxChars]
	if lastSpace := strings.LastIndex(truncated, " "); lastSpace > maxChars/2 {
		truncated = truncated[:lastSpace]
	}

	return truncated + "..."
}

// truncateForLog truncates text for logging purposes
func truncateForLog(text string, maxLen int) string {
	text = strings.ReplaceAll(text, "\n", " ")
	if len(text) <= maxLen {
		return text
	}
	return text[:maxLen] + "..."
}
