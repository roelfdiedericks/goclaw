package memorygraph

// Config configures the memory graph system
type Config struct {
	Enabled     bool              `json:"enabled"`     // Enable memory graph
	DBPath      string            `json:"dbPath"`      // Database path (default: ~/.goclaw/memory_graph.db)
	Search      SearchConfig      `json:"search"`      // Search configuration
	Maintenance MaintenanceConfig `json:"maintenance"` // Maintenance configuration
	Ingestion   IngestionConfig   `json:"ingestion"`   // Ingestion configuration
}

// IngestionConfig configures what content to ingest
type IngestionConfig struct {
	// Markdown ingestion patterns (relative to workspace)
	// Include patterns - files matching ANY pattern are included
	// If empty, defaults to ["*.md", "memory/*.md"]
	IncludePatterns []string `json:"includePatterns"`

	// Exclude patterns - files matching ANY pattern are excluded (takes priority over include)
	// Default: ["skills/**", "ref/**", "goclaw/**", ".*/**"]
	ExcludePatterns []string `json:"excludePatterns"`

	// Transcript batching - combine multiple chunks per LLM call
	// Default: 10 (reduces LLM calls by 10x)
	TranscriptBatchSize int `json:"transcriptBatchSize"`
}

// SearchConfig configures hybrid search behavior
type SearchConfig struct {
	MaxResults int `json:"maxResults"` // Maximum results to return (default: 10)

	// RRF parameters
	RRFConstant float64 `json:"rrfConstant"` // k parameter in RRF formula (default: 60)

	// Source weights (should sum to 1.0)
	VectorWeight  float64 `json:"vectorWeight"`  // Weight for semantic/vector search (default: 0.35)
	FTSWeight     float64 `json:"ftsWeight"`     // Weight for keyword/FTS search (default: 0.25)
	GraphWeight   float64 `json:"graphWeight"`   // Weight for graph traversal (default: 0.25)
	RecencyWeight float64 `json:"recencyWeight"` // Weight for time-based retrieval (default: 0.15)
}

// MaintenanceConfig configures background maintenance
type MaintenanceConfig struct {
	Enabled       bool `json:"enabled"`       // Enable background maintenance
	IntervalHours int  `json:"intervalHours"` // Hours between maintenance runs (default: 24)

	// Decay settings
	ImportanceDecayRate float64 `json:"importanceDecayRate"` // Daily decay multiplier (default: 0.995)
	ConfidenceDecayRate float64 `json:"confidenceDecayRate"` // Daily decay for unconfirmed patterns (default: 0.99)
	MinImportance       float64 `json:"minImportance"`       // Minimum importance before soft delete (default: 0.1)
	MinConfidence       float64 `json:"minConfidence"`       // Minimum confidence before invalidation (default: 0.2)

	// Access boost
	AccessBoostAmount float64 `json:"accessBoostAmount"` // Amount to boost on access (default: 0.01)
	MaxImportance     float64 `json:"maxImportance"`     // Cap for importance (default: 1.0)

	// Pruning
	PruneAfterDays int `json:"pruneAfterDays"` // Days to keep forgotten memories before deletion (default: 30)

	// Deduplication
	DuplicateSimilarity float64 `json:"duplicateSimilarity"` // Embedding similarity threshold for duplicates (default: 0.95)
}

// DefaultConfig returns sensible defaults for memory graph configuration
func DefaultConfig() Config {
	return Config{
		Enabled: true, // Enabled by default
		DBPath:  "",   // Will use default path
		Search: SearchConfig{
			MaxResults:    10,
			RRFConstant:   60,
			VectorWeight:  0.35,
			FTSWeight:     0.25,
			GraphWeight:   0.25,
			RecencyWeight: 0.15,
		},
		Maintenance: MaintenanceConfig{
			Enabled:             true,
			IntervalHours:       24,
			ImportanceDecayRate: 0.995,
			ConfidenceDecayRate: 0.99,
			MinImportance:       0.1,
			MinConfidence:       0.2,
			AccessBoostAmount:   0.01,
			MaxImportance:       1.0,
			PruneAfterDays:      30,
			DuplicateSimilarity: 0.95,
		},
		Ingestion: IngestionConfig{
			// Default include: all .md files in workspace root and memory/ directory
			IncludePatterns: []string{
				"*.md",
				"memory/*.md",
				"albums/*.md",
			},
			// Default exclude: skills, reference code, goclaw source, hidden directories
			ExcludePatterns: []string{
				"skills/**",
				"ref/**",
				"goclaw/**",
				".*/**",
			},
			// Batch 25 transcript chunks per LLM call (reduces calls significantly)
			TranscriptBatchSize: 25,
		},
	}
}

// Validate checks the configuration for errors
func (c *Config) Validate() error {
	// Normalize weights if they don't sum to 1.0
	total := c.Search.VectorWeight + c.Search.FTSWeight + c.Search.GraphWeight + c.Search.RecencyWeight
	if total > 0 && (total < 0.99 || total > 1.01) {
		c.Search.VectorWeight /= total
		c.Search.FTSWeight /= total
		c.Search.GraphWeight /= total
		c.Search.RecencyWeight /= total
	}

	// Apply defaults for zero values
	if c.Search.MaxResults <= 0 {
		c.Search.MaxResults = 10
	}
	if c.Search.RRFConstant <= 0 {
		c.Search.RRFConstant = 60
	}
	if c.Maintenance.IntervalHours <= 0 {
		c.Maintenance.IntervalHours = 24
	}
	if c.Maintenance.ImportanceDecayRate <= 0 {
		c.Maintenance.ImportanceDecayRate = 0.995
	}
	if c.Maintenance.ConfidenceDecayRate <= 0 {
		c.Maintenance.ConfidenceDecayRate = 0.99
	}
	if c.Maintenance.MinImportance <= 0 {
		c.Maintenance.MinImportance = 0.1
	}
	if c.Maintenance.MinConfidence <= 0 {
		c.Maintenance.MinConfidence = 0.2
	}
	if c.Maintenance.AccessBoostAmount <= 0 {
		c.Maintenance.AccessBoostAmount = 0.01
	}
	if c.Maintenance.MaxImportance <= 0 {
		c.Maintenance.MaxImportance = 1.0
	}
	if c.Maintenance.PruneAfterDays <= 0 {
		c.Maintenance.PruneAfterDays = 30
	}
	if c.Maintenance.DuplicateSimilarity <= 0 {
		c.Maintenance.DuplicateSimilarity = 0.95
	}

	return nil
}
