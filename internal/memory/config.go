package memory

import (
	"fmt"

	"github.com/roelfdiedericks/goclaw/internal/bus"
	"github.com/roelfdiedericks/goclaw/internal/config/forms"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// MemorySearchConfig configures the memory search tool
type MemorySearchConfig struct {
	Enabled bool                    `json:"enabled"` // Enable memory search tools
	DbPath  string                  `json:"dbPath"`  // Database path (default: ~/.goclaw/memory.db)
	Query   MemorySearchQueryConfig `json:"query"`   // Search query settings
	Paths   []string                `json:"paths"`   // Additional paths to index (besides memory/ and MEMORY.md)
}

// MemorySearchQueryConfig configures search query behavior
type MemorySearchQueryConfig struct {
	MaxResults    int     `json:"maxResults"`    // Maximum number of results to return (default: 6)
	MinScore      float64 `json:"minScore"`      // Minimum score threshold (default: 0.35)
	VectorWeight  float64 `json:"vectorWeight"`  // Weight for vector/semantic search (default: 0.7)
	KeywordWeight float64 `json:"keywordWeight"` // Weight for keyword/FTS search (default: 0.3)
}

// MConfig is an alias for MemorySearchConfig for convenience
// (Cannot use "Config" due to dot-import conflict with logging.Config)
type MConfig = MemorySearchConfig

// MQueryConfig is an alias for MemorySearchQueryConfig
type MQueryConfig = MemorySearchQueryConfig

// ConfigFormDef returns the form definition for editing MemorySearchConfig
func ConfigFormDef(cfg MConfig) forms.FormDef {
	return forms.FormDef{
		Title:       "Memory Search",
		Description: "Configure workspace memory indexing and semantic search",
		Sections: []forms.Section{
			{
				Title: "General",
				Fields: []forms.Field{
					{
						Name:  "enabled",
						Title: "Enable Memory Search",
						Desc:  "Index workspace memory files for semantic search",
						Type:  forms.Toggle,
					},
					{
						Name:  "dbPath",
						Title: "Database Path",
						Desc:  "Path to SQLite database (default: ~/.goclaw/memory.db)",
						Type:  forms.Text,
					},
				},
			},
			{
				Title:     "Indexed Paths",
				Desc:      "Additional paths to index (memory/ and MEMORY.md are always indexed)",
				Collapsed: true,
				Fields: []forms.Field{
					{
						Name:  "paths",
						Title: "Extra Paths",
						Desc:  "Additional file/directory paths to index (comma-separated)",
						Type:  forms.StringList,
					},
				},
			},
			{
				Title:     "Search Settings",
				Desc:      "Configure search query behavior",
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
			{
				Name:  "reindex",
				Label: "Reindex All",
				Desc:  "Force re-index of all memory files",
			},
		},
	}
}

// QueryConfigFormDef returns the form definition for MemorySearchQueryConfig
func QueryConfigFormDef(cfg MQueryConfig) forms.FormDef {
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
						Default: 6,
						Min:     1,
						Max:     50,
					},
					{
						Name:    "minScore",
						Title:   "Min Score",
						Desc:    "Minimum similarity threshold (0-1)",
						Type:    forms.Number,
						Default: 0.35,
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

// ValidateConfig validates a MemorySearchConfig
func ValidateConfig(cfg MConfig) error {
	if err := ValidateQueryConfig(cfg.Query); err != nil {
		return fmt.Errorf("query: %w", err)
	}
	return nil
}

// ValidateQueryConfig validates a MemorySearchQueryConfig
func ValidateQueryConfig(cfg MQueryConfig) error {
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

// DefaultMConfig returns a MemorySearchConfig with default values
func DefaultMConfig() MConfig {
	return MConfig{
		Enabled: true,
		Query:   DefaultMQueryConfig(),
	}
}

// DefaultMQueryConfig returns a QueryConfig with default values
func DefaultMQueryConfig() MQueryConfig {
	return MQueryConfig{
		MaxResults:    6,
		MinScore:      0.35,
		VectorWeight:  0.7,
		KeywordWeight: 0.3,
	}
}

// helper to create pointer to FormDef
func ptrFormDef(f forms.FormDef) *forms.FormDef {
	return &f
}

const configPath = "memory"

// RegisterCommands registers config commands for memory.
// Note: Operational commands (test, stats, reindex) are registered by Manager.
func RegisterCommands() {
	bus.RegisterCommand(configPath, "apply", handleApply)
}

// UnregisterCommands unregisters config commands.
func UnregisterCommands() {
	bus.UnregisterCommand(configPath, "apply")
}

// handleApply validates config and publishes event for manager to apply.
func handleApply(cmd bus.Command) bus.CommandResult {
	cfg, ok := cmd.Payload.(MemorySearchConfig)
	if !ok {
		cfgPtr, okPtr := cmd.Payload.(*MemorySearchConfig)
		if okPtr {
			cfg = *cfgPtr
			ok = true
		}
	}
	if !ok {
		return bus.CommandResult{
			Error:   fmt.Errorf("expected MemorySearchConfig payload, got %T", cmd.Payload),
			Message: "invalid payload type",
		}
	}

	// Validate config before applying
	if err := ValidateConfig(cfg); err != nil {
		return bus.CommandResult{
			Error:   err,
			Message: fmt.Sprintf("config validation failed: %v", err),
		}
	}

	L_info("memory: config applied", "enabled", cfg.Enabled, "dbPath", cfg.DbPath)
	bus.PublishEvent(configPath+".config.applied", cfg)

	return bus.CommandResult{
		Success: true,
		Message: "Config applied - manager will reload",
	}
}
