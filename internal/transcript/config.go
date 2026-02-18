package transcript

import (
	"fmt"

	"github.com/roelfdiedericks/goclaw/internal/config"
	"github.com/roelfdiedericks/goclaw/internal/config/forms"
)

// TConfig is an alias for config.TranscriptConfig for convenience
// (Cannot use "Config" due to dot-import conflict with logging.Config)
type TConfig = config.TranscriptConfig

// TQueryConfig is an alias for config.TranscriptQueryConfig
type TQueryConfig = config.TranscriptQueryConfig

// ConfigFormDef returns the form definition for editing TranscriptConfig
func ConfigFormDef(cfg TConfig) forms.FormDef {
	return forms.FormDef{
		Title:       "Transcript Indexing",
		Description: "Configure how conversation transcripts are indexed and searched",
		Sections: []forms.Section{
			{
				Title: "General",
				Fields: []forms.Field{
					{
						Name:  "enabled",
						Title: "Enable Indexing",
						Desc:  "Index conversation transcripts for semantic search",
						Type:  forms.Toggle,
					},
				},
			},
			{
				Title:     "Indexing Settings",
				Desc:      "Control how messages are processed and grouped",
				Collapsed: true,
				Fields: []forms.Field{
					{
						Name:    "indexIntervalSeconds",
						Title:   "Index Interval (seconds)",
						Desc:    "How often to check for new messages",
						Type:    forms.Number,
						Default: 30,
						Min:     5,
						Max:     3600,
					},
					{
						Name:    "batchSize",
						Title:   "Batch Size",
						Desc:    "Max messages to process per batch",
						Type:    forms.Number,
						Default: 100,
						Min:     10,
						Max:     1000,
					},
					{
						Name:    "backfillBatchSize",
						Title:   "Backfill Batch Size",
						Desc:    "Max chunks to backfill per interval",
						Type:    forms.Number,
						Default: 10,
						Min:     1,
						Max:     100,
					},
					{
						Name:    "maxGroupGapSeconds",
						Title:   "Max Group Gap (seconds)",
						Desc:    "Max time gap between messages in a chunk",
						Type:    forms.Number,
						Default: 300,
						Min:     60,
						Max:     3600,
					},
					{
						Name:    "maxMessagesPerChunk",
						Title:   "Max Messages Per Chunk",
						Desc:    "Maximum messages per conversation chunk",
						Type:    forms.Number,
						Default: 8,
						Min:     1,
						Max:     50,
					},
					{
						Name:    "maxEmbeddingContentLen",
						Title:   "Max Embedding Length",
						Desc:    "Max characters to embed per chunk",
						Type:    forms.Number,
						Default: 16000,
						Min:     1000,
						Max:     32000,
					},
				},
			},
			{
				Title:     "Search Settings",
				Desc:      "Configure transcript search behavior",
				Collapsed: true,
				FieldName: "Query",
				Nested:    ptrFormDef(QueryConfigFormDef(cfg.Query)),
			},
		},
		Actions: []forms.ActionDef{
			{
				Name:  "test",
				Label: "Test Connection",
				Desc:  "Verify database and embedding provider",
			},
			{
				Name:  "apply",
				Label: "Apply Now",
				Desc:  "Apply changes to running service",
			},
			{
				Name:  "stats",
				Label: "Show Stats",
				Desc:  "Display indexing statistics",
			},
		},
	}
}

// QueryConfigFormDef returns the form definition for TranscriptQueryConfig
func QueryConfigFormDef(cfg TQueryConfig) forms.FormDef {
	return forms.FormDef{
		Title: "Search Settings",
		Sections: []forms.Section{
			{
				Title: "Query Parameters",
				Fields: []forms.Field{
					{
						Name:    "maxResults",
						Title:   "Max Results",
						Desc:    "Maximum results to return",
						Type:    forms.Number,
						Default: 10,
						Min:     1,
						Max:     100,
					},
					{
						Name:    "minScore",
						Title:   "Min Score",
						Desc:    "Minimum similarity threshold (0-1)",
						Type:    forms.Number,
						Default: 0.3,
						Min:     0,
						Max:     1,
						Step:    0.05,
					},
					{
						Name:    "vectorWeight",
						Title:   "Vector Weight",
						Desc:    "Weight for semantic/vector search",
						Type:    forms.Number,
						Default: 0.7,
						Min:     0,
						Max:     1,
						Step:    0.1,
					},
					{
						Name:    "keywordWeight",
						Title:   "Keyword Weight",
						Desc:    "Weight for keyword/FTS search",
						Type:    forms.Number,
						Default: 0.3,
						Min:     0,
						Max:     1,
						Step:    0.1,
					},
				},
			},
		},
		Actions: []forms.ActionDef{
			{
				Name:  "test_search",
				Label: "Test Search",
				Desc:  "Run a test query to verify settings",
			},
		},
	}
}

// ValidateConfig validates a TranscriptConfig
func ValidateConfig(cfg TConfig) error {
	if cfg.IndexIntervalSeconds < 5 {
		return fmt.Errorf("indexIntervalSeconds must be at least 5")
	}
	if cfg.BatchSize < 1 {
		return fmt.Errorf("batchSize must be at least 1")
	}
	if cfg.MaxMessagesPerChunk < 1 {
		return fmt.Errorf("maxMessagesPerChunk must be at least 1")
	}
	if err := ValidateQueryConfig(cfg.Query); err != nil {
		return fmt.Errorf("query: %w", err)
	}
	return nil
}

// ValidateQueryConfig validates a TranscriptQueryConfig
func ValidateQueryConfig(cfg TQueryConfig) error {
	if cfg.MaxResults < 1 {
		return fmt.Errorf("maxResults must be at least 1")
	}
	if cfg.MinScore < 0 || cfg.MinScore > 1 {
		return fmt.Errorf("minScore must be between 0 and 1")
	}
	if cfg.VectorWeight < 0 || cfg.VectorWeight > 1 {
		return fmt.Errorf("vectorWeight must be between 0 and 1")
	}
	if cfg.KeywordWeight < 0 || cfg.KeywordWeight > 1 {
		return fmt.Errorf("keywordWeight must be between 0 and 1")
	}
	return nil
}

// DefaultTConfig returns a TranscriptConfig with default values
func DefaultTConfig() TConfig {
	return TConfig{
		Enabled:                true,
		IndexIntervalSeconds:   30,
		BatchSize:              100,
		BackfillBatchSize:      10,
		MaxGroupGapSeconds:     300,
		MaxMessagesPerChunk:    8,
		MaxEmbeddingContentLen: 16000,
		Query:                  DefaultTQueryConfig(),
	}
}

// DefaultTQueryConfig returns a QueryConfig with default values
func DefaultTQueryConfig() TQueryConfig {
	return TQueryConfig{
		MaxResults:    10,
		MinScore:      0.3,
		VectorWeight:  0.7,
		KeywordWeight: 0.3,
	}
}

// helper to create pointer to FormDef
func ptrFormDef(f forms.FormDef) *forms.FormDef {
	return &f
}
