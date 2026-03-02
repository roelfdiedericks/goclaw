package memorygraph

import (
	"fmt"

	"github.com/roelfdiedericks/goclaw/internal/bus"
	"github.com/roelfdiedericks/goclaw/internal/config/forms"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// Config configures the memory graph system
type Config struct {
	Enabled        bool                  `json:"enabled"`        // Enable memory graph
	DBPath         string                `json:"dbPath"`         // Database path (default: ~/.goclaw/memory_graph.db)
	Search         SearchConfig          `json:"search"`         // Search configuration
	Maintenance    MaintenanceConfig     `json:"maintenance"`    // Maintenance configuration
	Ingestion      IngestionConfig       `json:"ingestion"`      // Ingestion configuration
	LiveExtraction LiveExtractionConfig  `json:"liveExtraction"` // Live extraction configuration
}

// LiveExtractionConfig configures automatic memory extraction from conversations
type LiveExtractionConfig struct {
	Enabled         bool     `json:"enabled"`         // Enable live extraction
	IntervalSeconds int      `json:"intervalSeconds"` // Check interval (default: 120)
	MinMessages     int      `json:"minMessages"`     // Minimum messages before extraction (default: 5)
	MaxTurns        int      `json:"maxTurns"`        // Max extraction loop turns (default: 10)
	BatchSize       int      `json:"batchSize"`       // Max messages per batch (default: 50)
	ExcludeSources  []string `json:"excludeSources"`  // Message sources to exclude (default: ["heartbeat", "cron", "delivered"])
}

// DefaultExcludeSources returns the default sources to exclude from extraction.
func DefaultExcludeSources() []string {
	return []string{"heartbeat", "cron", "delivered"}
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
		LiveExtraction: LiveExtractionConfig{
			Enabled:         true,                    // Enabled by default
			IntervalSeconds: 120,                     // Every 2 minutes
			MinMessages:     5,                       // Only extract if 5+ new messages
			MaxTurns:        10,                      // Safety limit
			BatchSize:       50,                      // Max messages per extraction
			ExcludeSources:  DefaultExcludeSources(), // Exclude automated sources
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

// --- Form Definitions ---

// ConfigFormDef returns the form definition for editing memory graph configuration
func ConfigFormDef(cfg Config) forms.FormDef {
	return forms.FormDef{
		Title:       "Memory Graph",
		Description: "Configure memory extraction and graph storage for long-term knowledge retention",
		Sections: []forms.Section{
			{
				Title: "General",
				Fields: []forms.Field{
					{
						Name:  "enabled",
						Title: "Enable Memory Graph",
						Type:  forms.Toggle,
						Desc:  "Master switch for the memory graph system",
					},
					{
						Name:  "dbPath",
						Title: "Database Path",
						Type:  forms.Text,
						Desc:  "Path to memory_graph.db file. Leave empty for default (~/.goclaw/memory_graph.db)",
					},
				},
			},
			{
				Title:     "Live Extraction",
				Collapsed: true,
				FieldName: "LiveExtraction",
				Nested:    ptrFormDef(LiveExtractionFormDef(cfg.LiveExtraction)),
			},
			{
				Title:     "Search Weights",
				Collapsed: true,
				FieldName: "Search",
				Nested:    ptrFormDef(SearchConfigFormDef(cfg.Search)),
			},
		},
		Actions: []forms.ActionDef{
			{Name: "test", Label: "Test Connection", Desc: "Verify database connection and LLM provider availability"},
			{Name: "apply", Label: "Apply Now", Desc: "Apply configuration changes to the running memory graph service"},
			{Name: "stats", Label: "Show Stats", Desc: "Display memory counts, storage size, and extraction statistics"},
		},
	}
}

// LiveExtractionFormDef returns the form definition for live extraction settings
func LiveExtractionFormDef(cfg LiveExtractionConfig) forms.FormDef {
	return forms.FormDef{
		Title: "Live Extraction Settings",
		Sections: []forms.Section{
			{
				Fields: []forms.Field{
					{
						Name:  "enabled",
						Title: "Enable Live Extraction",
						Type:  forms.Toggle,
						Desc:  "Automatically extract memories from conversations in the background",
					},
					{
						Name:    "intervalSeconds",
						Title:   "Extraction Interval",
						Type:    forms.Number,
						Default: 120,
						Min:     30,
						Max:     3600,
						Desc:    "How often to check for new messages to extract (in seconds)",
					},
					{
						Name:    "minMessages",
						Title:   "Minimum Messages",
						Type:    forms.Number,
						Default: 5,
						Min:     1,
						Max:     50,
						Desc:    "Minimum unprocessed messages required before extraction runs",
					},
					{
						Name:    "batchSize",
						Title:   "Batch Size",
						Type:    forms.Number,
						Default: 50,
						Min:     10,
						Max:     200,
						Desc:    "Maximum messages to process in a single extraction run",
					},
					{
						Name: "excludeSources",
						Title: "Exclude Sources",
						Type:  forms.StringList,
						Desc:  "Message sources to skip (e.g., heartbeat, cron, delivered). These appear in transcripts but won't become memories.",
					},
				},
			},
		},
	}
}

// SearchConfigFormDef returns the form definition for search configuration
func SearchConfigFormDef(cfg SearchConfig) forms.FormDef {
	return forms.FormDef{
		Title:       "Search Weights",
		Description: "Configure how memories are ranked in search results (weights are auto-normalized to sum to 1.0)",
		Sections: []forms.Section{
			{
				Fields: []forms.Field{
					{
						Name:    "vectorWeight",
						Title:   "Semantic Weight",
						Type:    forms.Number,
						Default: 0.35,
						Min:     0,
						Max:     1,
						Desc:    "Weight for semantic/meaning similarity (how closely content matches the query)",
					},
					{
						Name:    "ftsWeight",
						Title:   "Keyword Weight",
						Type:    forms.Number,
						Default: 0.25,
						Min:     0,
						Max:     1,
						Desc:    "Weight for exact keyword matches (BM25 text search)",
					},
					{
						Name:    "recencyWeight",
						Title:   "Recency Weight",
						Type:    forms.Number,
						Default: 0.15,
						Min:     0,
						Max:     1,
						Desc:    "Weight for how recently the memory was created or accessed",
					},
					{
						Name:    "graphWeight",
						Title:   "Graph Weight",
						Type:    forms.Number,
						Default: 0.25,
						Min:     0,
						Max:     1,
						Desc:    "Weight for graph-based retrieval (connected memories)",
					},
				},
			},
		},
	}
}

// ptrFormDef is a helper to create pointer to FormDef
func ptrFormDef(f forms.FormDef) *forms.FormDef {
	return &f
}

// --- Bus Commands ---

const configPath = "memorygraph"

// RegisterCommands registers memory graph config command handlers
func RegisterCommands() {
	bus.RegisterCommand(configPath, "apply", handleApply)
	bus.RegisterCommand(configPath, "test", handleTest)
	bus.RegisterCommand(configPath, "stats", handleStats)
}

// UnregisterCommands removes memory graph config command handlers
func UnregisterCommands() {
	bus.UnregisterCommand(configPath, "apply")
	bus.UnregisterCommand(configPath, "test")
	bus.UnregisterCommand(configPath, "stats")
}

// handleApply validates config and publishes event for manager to apply
func handleApply(cmd bus.Command) bus.CommandResult {
	cfg, ok := cmd.Payload.(Config)
	if !ok {
		cfgPtr, okPtr := cmd.Payload.(*Config)
		if okPtr {
			cfg = *cfgPtr
			ok = true
		}
	}
	if !ok {
		return bus.CommandResult{
			Error:   fmt.Errorf("expected Config payload, got %T", cmd.Payload),
			Message: "invalid payload type",
		}
	}

	// Validate and normalize config
	if err := ValidateConfig(&cfg); err != nil {
		return bus.CommandResult{
			Error:   err,
			Message: fmt.Sprintf("config validation failed: %v", err),
		}
	}

	L_info("memorygraph: config applied", "enabled", cfg.Enabled, "liveEnabled", cfg.LiveExtraction.Enabled)
	bus.PublishEvent(configPath+".config.applied", cfg)

	return bus.CommandResult{
		Success: true,
		Message: "Config applied - manager will reload",
	}
}

// handleTest tests database and LLM provider availability
func handleTest(cmd bus.Command) bus.CommandResult {
	// Test LLM provider availability
	provider, err := getExtractionProvider()
	if err != nil {
		return bus.CommandResult{
			Error:   err,
			Message: fmt.Sprintf("LLM provider unavailable: %v", err),
		}
	}

	return bus.CommandResult{
		Success: true,
		Message: fmt.Sprintf("Memory graph ready (provider: %s)", provider.Name()),
	}
}

// handleStats returns memory graph statistics
func handleStats(cmd bus.Command) bus.CommandResult {
	mgr := GetManager()
	if mgr == nil {
		return bus.CommandResult{
			Error:   fmt.Errorf("memory graph manager not initialized"),
			Message: "Manager not available",
		}
	}

	// Get memory counts by type
	db := mgr.DB()
	if db == nil {
		return bus.CommandResult{
			Error:   fmt.Errorf("database not available"),
			Message: "Database not available",
		}
	}

	var totalMemories int
	var totalAssociations int
	var ingestedCount int

	db.QueryRow("SELECT COUNT(*) FROM memories WHERE deleted = 0").Scan(&totalMemories)
	db.QueryRow("SELECT COUNT(*) FROM associations").Scan(&totalAssociations)
	db.QueryRow("SELECT COUNT(*) FROM ingestion_state").Scan(&ingestedCount)

	// Get counts by type
	rows, err := db.Query("SELECT memory_type, COUNT(*) FROM memories WHERE deleted = 0 GROUP BY memory_type ORDER BY COUNT(*) DESC")
	if err == nil {
		defer rows.Close()
		var typeStats []string
		for rows.Next() {
			var memType string
			var count int
			if rows.Scan(&memType, &count) == nil {
				typeStats = append(typeStats, fmt.Sprintf("%s: %d", memType, count))
			}
		}
		if len(typeStats) > 0 {
			return bus.CommandResult{
				Success: true,
				Message: fmt.Sprintf("Memories: %d total, %d associations, %d ingested chunks\nBy type: %v",
					totalMemories, totalAssociations, ingestedCount, typeStats),
			}
		}
	}

	return bus.CommandResult{
		Success: true,
		Message: fmt.Sprintf("Memories: %d total, %d associations, %d ingested chunks",
			totalMemories, totalAssociations, ingestedCount),
	}
}

// --- Validation ---

// ValidateConfig validates and normalizes the configuration
func ValidateConfig(cfg *Config) error {
	// Validate live extraction settings
	if cfg.LiveExtraction.IntervalSeconds < 30 {
		return fmt.Errorf("intervalSeconds must be at least 30")
	}
	if cfg.LiveExtraction.MinMessages < 1 {
		return fmt.Errorf("minMessages must be at least 1")
	}
	if cfg.LiveExtraction.BatchSize < 1 {
		return fmt.Errorf("batchSize must be at least 1")
	}

	// Auto-normalize search weights to sum to 1.0
	NormalizeSearchWeights(&cfg.Search)

	return nil
}

// NormalizeSearchWeights normalizes search weights to sum to 1.0
func NormalizeSearchWeights(s *SearchConfig) {
	sum := s.VectorWeight + s.FTSWeight + s.GraphWeight + s.RecencyWeight
	if sum <= 0 {
		// Reset to defaults
		s.VectorWeight = 0.35
		s.FTSWeight = 0.25
		s.GraphWeight = 0.25
		s.RecencyWeight = 0.15
		return
	}
	// Normalize to sum to 1.0
	s.VectorWeight /= sum
	s.FTSWeight /= sum
	s.GraphWeight /= sum
	s.RecencyWeight /= sum
}
