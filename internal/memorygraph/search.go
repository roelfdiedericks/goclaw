package memorygraph

import (
	"context"
	"database/sql"
	"encoding/json"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/roelfdiedericks/goclaw/internal/llm"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// Searcher handles hybrid search across the memory graph
type Searcher struct {
	db       *sql.DB
	provider llm.EmbeddingProvider
	config   SearchConfig
}

// NewSearcher creates a new memory graph searcher
func NewSearcher(db *sql.DB, provider llm.EmbeddingProvider, config SearchConfig) *Searcher {
	return &Searcher{
		db:       db,
		provider: provider,
		config:   config,
	}
}

// SetProvider updates the embedding provider
func (s *Searcher) SetProvider(provider llm.EmbeddingProvider) {
	s.provider = provider
}

// SearchOptions configures a search query
type SearchOptions struct {
	Query          string   // Search query
	Username       string   // Filter by username
	Channel        string   // Filter by channel
	Types          []Type   // Filter by memory types
	MinImportance  float32  // Minimum importance threshold
	MaxResults     int      // Maximum results to return
	ContextMemory  string   // Optional: UUID of a memory to use as context for graph search
}

// Search performs hybrid search and returns ranked results
func (s *Searcher) Search(ctx context.Context, opts SearchOptions) ([]SearchResult, error) {
	if opts.MaxResults <= 0 {
		opts.MaxResults = s.config.MaxResults
	}
	if opts.MaxResults <= 0 {
		opts.MaxResults = 10
	}

	// Use larger candidate pool for fusion
	candidateLimit := opts.MaxResults * 4

	// Collect results from all sources
	sources := make(map[string]map[string]float64) // source_name -> uuid -> score

	// 1. Vector search (semantic similarity)
	if s.provider != nil && s.provider.Available() && s.config.VectorWeight > 0 {
		vectorResults, err := s.vectorSearch(ctx, opts, candidateLimit)
		if err != nil {
			L_warn("memorygraph: vector search failed", "error", err)
		} else {
			sources["vector"] = vectorResults
		}
	}

	// 2. FTS search (keyword matching)
	if s.config.FTSWeight > 0 {
		ftsResults, err := s.ftsSearch(ctx, opts, candidateLimit)
		if err != nil {
			L_warn("memorygraph: FTS search failed", "error", err)
		} else {
			sources["fts"] = ftsResults
		}
	}

	// 3. Graph search (related memories)
	if s.config.GraphWeight > 0 && opts.ContextMemory != "" {
		graphResults, err := s.graphSearch(ctx, opts, candidateLimit)
		if err != nil {
			L_warn("memorygraph: graph search failed", "error", err)
		} else {
			sources["graph"] = graphResults
		}
	}

	// 4. Time/recency search
	if s.config.RecencyWeight > 0 {
		recencyResults, err := s.recencySearch(ctx, opts, candidateLimit)
		if err != nil {
			L_warn("memorygraph: recency search failed", "error", err)
		} else {
			sources["recency"] = recencyResults
		}
	}

	// 5. Fuse results using RRF
	fused := s.fuseResults(sources)

	// 6. Fetch full memories and build results
	results := make([]SearchResult, 0, len(fused))
	for _, item := range fused {
		if len(results) >= opts.MaxResults {
			break
		}

		mem, err := (&Store{db: s.db}).GetMemory(item.uuid)
		if err != nil || mem == nil || mem.Forgotten {
			continue
		}

		results = append(results, SearchResult{
			Memory:       *mem,
			Score:        float32(item.score),
			Rank:         len(results) + 1,
			SourceScores: item.sourceScores,
		})
	}

	L_debug("memorygraph: search completed",
		"query", truncate(opts.Query, 50),
		"results", len(results),
		"sources", len(sources),
	)

	return results, nil
}

// vectorSearch performs semantic similarity search using embeddings
func (s *Searcher) vectorSearch(ctx context.Context, opts SearchOptions, limit int) (map[string]float64, error) {
	// Generate query embedding
	queryEmbedding, err := s.provider.EmbedQuery(ctx, opts.Query)
	if err != nil {
		return nil, err
	}
	if queryEmbedding == nil {
		return make(map[string]float64), nil
	}

	// Build query with filters
	query := `
		SELECT uuid, embedding FROM memories 
		WHERE embedding IS NOT NULL AND forgotten = 0
	`
	var args []interface{}

	if opts.Username != "" {
		query += " AND username = ?"
		args = append(args, opts.Username)
	}
	if opts.Channel != "" {
		query += " AND channel = ?"
		args = append(args, opts.Channel)
	}
	if len(opts.Types) > 0 {
		placeholders := make([]string, len(opts.Types))
		for i, t := range opts.Types {
			placeholders[i] = "?"
			args = append(args, string(t))
		}
		query += " AND memory_type IN (" + strings.Join(placeholders, ", ") + ")"
	}
	if opts.MinImportance > 0 {
		query += " AND importance >= ?"
		args = append(args, opts.MinImportance)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// Calculate similarities
	type scoredResult struct {
		uuid  string
		score float64
	}
	var results []scoredResult

	for rows.Next() {
		var uuid string
		var embeddingBlob []byte

		if err := rows.Scan(&uuid, &embeddingBlob); err != nil {
			continue
		}

		var embedding []float32
		if err := json.Unmarshal(embeddingBlob, &embedding); err != nil {
			continue
		}

		similarity := cosineSimilarity(queryEmbedding, embedding)
		if similarity > 0 {
			results = append(results, scoredResult{uuid: uuid, score: similarity})
		}
	}

	// Sort by similarity and take top N
	sort.Slice(results, func(i, j int) bool {
		return results[i].score > results[j].score
	})

	scores := make(map[string]float64)
	for i, r := range results {
		if i >= limit {
			break
		}
		scores[r.uuid] = r.score
	}

	return scores, rows.Err()
}

// ftsSearch performs keyword search using FTS5
func (s *Searcher) ftsSearch(ctx context.Context, opts SearchOptions, limit int) (map[string]float64, error) {
	// Sanitize query for FTS
	ftsQuery := sanitizeFTSQuery(opts.Query)
	if ftsQuery == "" {
		return make(map[string]float64), nil
	}

	// FTS5 search with BM25 scoring
	query := `
		SELECT m.uuid, bm25(memories_fts) as score
		FROM memories_fts f
		JOIN memories m ON m.id = f.rowid
		WHERE memories_fts MATCH ? AND m.forgotten = 0
	`
	args := []interface{}{ftsQuery}

	if opts.Username != "" {
		query += " AND m.username = ?"
		args = append(args, opts.Username)
	}
	if opts.Channel != "" {
		query += " AND m.channel = ?"
		args = append(args, opts.Channel)
	}
	if len(opts.Types) > 0 {
		placeholders := make([]string, len(opts.Types))
		for i, t := range opts.Types {
			placeholders[i] = "?"
			args = append(args, string(t))
		}
		query += " AND m.memory_type IN (" + strings.Join(placeholders, ", ") + ")"
	}
	if opts.MinImportance > 0 {
		query += " AND m.importance >= ?"
		args = append(args, opts.MinImportance)
	}

	query += " ORDER BY score LIMIT ?"
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	scores := make(map[string]float64)
	var minScore, maxScore float64
	type rawResult struct {
		uuid  string
		score float64
	}
	var rawResults []rawResult

	for rows.Next() {
		var uuid string
		var score float64
		if err := rows.Scan(&uuid, &score); err != nil {
			continue
		}
		// BM25 returns negative scores, more negative = better match
		score = -score // Convert to positive (higher = better)
		rawResults = append(rawResults, rawResult{uuid: uuid, score: score})
		if score < minScore || minScore == 0 {
			minScore = score
		}
		if score > maxScore {
			maxScore = score
		}
	}

	// Normalize scores to 0-1 range
	scoreRange := maxScore - minScore
	if scoreRange > 0 {
		for _, r := range rawResults {
			scores[r.uuid] = (r.score - minScore) / scoreRange
		}
	} else {
		for _, r := range rawResults {
			scores[r.uuid] = 1.0
		}
	}

	return scores, rows.Err()
}

// graphSearch finds memories related to the context memory
func (s *Searcher) graphSearch(ctx context.Context, opts SearchOptions, limit int) (map[string]float64, error) {
	if opts.ContextMemory == "" {
		return make(map[string]float64), nil
	}

	// Get memories connected via associations (1-hop)
	query := `
		SELECT DISTINCT 
			CASE 
				WHEN a.source_uuid = ? THEN a.target_uuid 
				ELSE a.source_uuid 
			END as neighbor_uuid,
			a.weight
		FROM associations a
		JOIN memories m ON m.uuid = (
			CASE 
				WHEN a.source_uuid = ? THEN a.target_uuid 
				ELSE a.source_uuid 
			END
		)
		WHERE (a.source_uuid = ? OR (a.target_uuid = ? AND a.directed = 0))
		AND m.forgotten = 0
	`
	args := []interface{}{opts.ContextMemory, opts.ContextMemory, opts.ContextMemory, opts.ContextMemory}

	if opts.Username != "" {
		query += " AND m.username = ?"
		args = append(args, opts.Username)
	}
	if opts.Channel != "" {
		query += " AND m.channel = ?"
		args = append(args, opts.Channel)
	}
	if len(opts.Types) > 0 {
		placeholders := make([]string, len(opts.Types))
		for i, t := range opts.Types {
			placeholders[i] = "?"
			args = append(args, string(t))
		}
		query += " AND m.memory_type IN (" + strings.Join(placeholders, ", ") + ")"
	}

	query += " LIMIT ?"
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	scores := make(map[string]float64)
	for rows.Next() {
		var uuid string
		var weight float64
		if err := rows.Scan(&uuid, &weight); err != nil {
			continue
		}
		scores[uuid] = weight
	}

	return scores, rows.Err()
}

// recencySearch returns recently accessed/created memories
func (s *Searcher) recencySearch(ctx context.Context, opts SearchOptions, limit int) (map[string]float64, error) {
	query := `
		SELECT uuid, created_at, last_accessed_at, importance
		FROM memories 
		WHERE forgotten = 0
	`
	var args []interface{}

	if opts.Username != "" {
		query += " AND username = ?"
		args = append(args, opts.Username)
	}
	if opts.Channel != "" {
		query += " AND channel = ?"
		args = append(args, opts.Channel)
	}
	if len(opts.Types) > 0 {
		placeholders := make([]string, len(opts.Types))
		for i, t := range opts.Types {
			placeholders[i] = "?"
			args = append(args, string(t))
		}
		query += " AND memory_type IN (" + strings.Join(placeholders, ", ") + ")"
	}
	if opts.MinImportance > 0 {
		query += " AND importance >= ?"
		args = append(args, opts.MinImportance)
	}

	// Order by most recent activity (either creation or access)
	query += ` ORDER BY MAX(created_at, last_accessed_at) DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	now := time.Now()
	scores := make(map[string]float64)

	for rows.Next() {
		var uuid, createdAt, lastAccessedAt string
		var importance float64

		if err := rows.Scan(&uuid, &createdAt, &lastAccessedAt, &importance); err != nil {
			continue
		}

		// Parse times
		created, _ := time.Parse(time.RFC3339, createdAt)
		accessed, _ := time.Parse(time.RFC3339, lastAccessedAt)

		// Use the more recent of created or accessed
		mostRecent := created
		if accessed.After(created) {
			mostRecent = accessed
		}

		// Calculate recency score with exponential decay
		// Score decreases by ~50% every 7 days
		daysSince := now.Sub(mostRecent).Hours() / 24
		recencyScore := math.Exp(-0.1 * daysSince) // ~0.5 at 7 days, ~0.25 at 14 days

		// Boost by importance
		finalScore := recencyScore * (0.5 + 0.5*importance)
		scores[uuid] = finalScore
	}

	return scores, rows.Err()
}

// fusedResult represents a result after RRF fusion
type fusedResult struct {
	uuid         string
	score        float64
	sourceScores map[string]float32
}

// fuseResults combines ranked results from multiple sources using Reciprocal Rank Fusion
func (s *Searcher) fuseResults(sources map[string]map[string]float64) []fusedResult {
	k := s.config.RRFConstant
	if k <= 0 {
		k = 60
	}

	// Get weights
	weights := map[string]float64{
		"vector":  s.config.VectorWeight,
		"fts":     s.config.FTSWeight,
		"graph":   s.config.GraphWeight,
		"recency": s.config.RecencyWeight,
	}

	// Convert scores to ranks for each source
	ranks := make(map[string]map[string]int) // source -> uuid -> rank
	for sourceName, scoreMap := range sources {
		// Sort by score descending
		type item struct {
			uuid  string
			score float64
		}
		items := make([]item, 0, len(scoreMap))
		for uuid, score := range scoreMap {
			items = append(items, item{uuid: uuid, score: score})
		}
		sort.Slice(items, func(i, j int) bool {
			return items[i].score > items[j].score
		})

		// Assign ranks
		rankMap := make(map[string]int)
		for i, it := range items {
			rankMap[it.uuid] = i + 1 // 1-indexed
		}
		ranks[sourceName] = rankMap
	}

	// Calculate RRF score for each unique UUID
	rrfScores := make(map[string]float64)
	sourceScores := make(map[string]map[string]float32)

	for sourceName, rankMap := range ranks {
		weight := weights[sourceName]
		if weight <= 0 {
			continue
		}

		for uuid, rank := range rankMap {
			rrfScore := weight * (1.0 / (k + float64(rank)))
			rrfScores[uuid] += rrfScore

			// Track source scores for debugging
			if sourceScores[uuid] == nil {
				sourceScores[uuid] = make(map[string]float32)
			}
			sourceScores[uuid][sourceName] = float32(sources[sourceName][uuid])
		}
	}

	// Sort by RRF score
	results := make([]fusedResult, 0, len(rrfScores))
	for uuid, score := range rrfScores {
		results = append(results, fusedResult{
			uuid:         uuid,
			score:        score,
			sourceScores: sourceScores[uuid],
		})
	}
	sort.Slice(results, func(i, j int) bool {
		return results[i].score > results[j].score
	})

	return results
}

// cosineSimilarity calculates the cosine similarity between two vectors
func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}

	var dotProduct, normA, normB float64
	for i := range a {
		dotProduct += float64(a[i] * b[i])
		normA += float64(a[i] * a[i])
		normB += float64(b[i] * b[i])
	}

	if normA == 0 || normB == 0 {
		return 0
	}

	return dotProduct / (math.Sqrt(normA) * math.Sqrt(normB))
}

// sanitizeFTSQuery prepares a query string for FTS5
func sanitizeFTSQuery(query string) string {
	// Remove FTS5 special characters
	// FTS5 operators: AND OR NOT NEAR " * ^ - + : ( ) . ,
	replacer := strings.NewReplacer(
		"\"", "",
		"'", "",
		"*", "",
		"(", "",
		")", "",
		":", "",
		"^", "",
		"-", " ",
		"+", " ",
		".", " ",
		",", " ",
		";", " ",
		"[", "",
		"]", "",
		"{", "",
		"}", "",
		"<", "",
		">", "",
		"/", " ",
		"\\", " ",
		"@", "",
		"#", "",
		"$", "",
		"%", "",
		"&", "",
		"!", "",
		"?", "",
		"~", "",
		"`", "",
		"|", " ",
	)
	cleaned := replacer.Replace(query)
	cleaned = strings.TrimSpace(cleaned)

	// Convert to OR query (match any word)
	words := strings.Fields(cleaned)
	if len(words) == 0 {
		return ""
	}

	// Filter out FTS5 keywords and very short words
	filtered := make([]string, 0, len(words))
	for _, w := range words {
		w = strings.ToLower(w)
		// Skip FTS5 keywords
		if w == "and" || w == "or" || w == "not" || w == "near" {
			continue
		}
		// Skip very short words (less than 2 chars)
		if len(w) < 2 {
			continue
		}
		filtered = append(filtered, w)
	}

	if len(filtered) == 0 {
		return ""
	}

	// Use prefix matching for each word
	for i, w := range filtered {
		filtered[i] = w + "*"
	}

	return strings.Join(filtered, " OR ")
}

// truncate shortens a string for logging
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
