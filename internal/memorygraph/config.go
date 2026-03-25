package memorygraph

import (
	"fmt"

	"github.com/roelfdiedericks/goclaw/internal/bus"
	"github.com/roelfdiedericks/goclaw/internal/config/forms"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// Config configures the memory graph system
type Config struct {
	Enabled        bool                 `json:"enabled" default:"true"` // Enable memory graph
	DBPath         string               `json:"dbPath"`                 // Database path (default: ~/.goclaw/memory_graph.db)
	Search         SearchConfig         `json:"search"`                 // Search configuration
	Maintenance    MaintenanceConfig    `json:"maintenance"`            // Maintenance configuration
	Ingestion      IngestionConfig      `json:"ingestion"`              // Ingestion configuration
	LiveExtraction LiveExtractionConfig `json:"liveExtraction"`         // Live extraction configuration
	Bulletin       BulletinConfig       `json:"bulletin"`               // Bulletin injection configuration
}

// LiveExtractionConfig configures automatic memory extraction from conversations
type LiveExtractionConfig struct {
	Enabled             bool     `json:"enabled" default:"true"`           // Enable live extraction
	AgentExtraction     bool     `json:"agentExtraction" default:"true"`   // Enable agent-driven extraction
	IntervalSeconds     int      `json:"intervalSeconds" default:"120"`    // Check interval
	HandoffDelaySeconds int      `json:"handoffDelaySeconds" default:"90"` // Age threshold before background picks up unmarked messages
	MinMessages         int      `json:"minMessages" default:"5"`          // Minimum messages before extraction
	MaxTurns            int      `json:"maxTurns" default:"10"`            // Max extraction loop turns
	BatchSize           int      `json:"batchSize" default:"50"`           // Max messages per batch
	ExcludeSources      []string `json:"excludeSources"`                   // Message sources to exclude (runtime default)
}

// DefaultExcludeSources returns the default sources to exclude from extraction.
func DefaultExcludeSources() []string {
	return []string{"heartbeat", "cron", "delivered"}
}

// BulletinConfig configures bulletin injection into agent context
type BulletinConfig struct {
	// General settings
	Enabled          bool   `json:"enabled" default:"true"`             // Master switch for bulletin injection
	TTLMinutes       int    `json:"ttlMinutes" default:"5"`             // Cache TTL in minutes
	MemoryInjection  string `json:"memoryInjection" default:"prompt"`   // "prompt" or "message"
	ContextInjection string `json:"contextInjection" default:"message"` // "prompt" or "message"
	Deduplicate      bool   `json:"deduplicate" default:"true"`         // Skip items already shown in earlier sections

	// Injection context controls
	InjectForHeartbeat bool `json:"injectForHeartbeat"`           // Inject for heartbeat sessions
	InjectForCron      bool `json:"injectForCron" default:"true"` // Inject for cron sessions

	// Memory bulletin section limits (0 = disabled)
	IdentityLimit         int     `json:"identityLimit" default:"3"`           // Identity items
	HighPriorityLimit     int     `json:"highPriorityLimit" default:"3"`       // High importance items
	HighPriorityThreshold float64 `json:"highPriorityThreshold" default:"0.8"` // Importance threshold for high priority
	RecentEventsLimit     int     `json:"recentEventsLimit" default:"5"`       // Recent event items
	RecentEventsDays      int     `json:"recentEventsDays" default:"7"`        // Time bound for recent events
	DecisionsLimit        int     `json:"decisionsLimit" default:"3"`          // Decision items
	DecisionsDays         int     `json:"decisionsDays" default:"14"`          // Time bound for decisions
	PreferencesLimit      int     `json:"preferencesLimit" default:"3"`        // Preference items
	GoalsLimit            int     `json:"goalsLimit" default:"3"`              // Goal items
	UpcomingEventsLimit   int     `json:"upcomingEventsLimit" default:"5"`     // Upcoming scheduled items
	UpcomingEventsDays    int     `json:"upcomingEventsDays" default:"30"`     // Future time bound for scheduled items

	// Context bulletin section limits (0 = disabled)
	RoutinesLimit     int `json:"routinesLimit" default:"5"`     // Routine items
	PredictionsLimit  int `json:"predictionsLimit" default:"3"`  // Prediction items
	CorrelationsLimit int `json:"correlationsLimit" default:"3"` // Correlation items
	AnomaliesLimit    int `json:"anomaliesLimit" default:"3"`    // Anomaly items
	TodosLimit        int `json:"todosLimit" default:"10"`       // Todo items

	// Chat context section (query-driven, not cached)
	ChatContextEnabled     bool   `json:"chatContextEnabled" default:"true"`  // Enable chat context section
	ChatContextLimit       int    `json:"chatContextLimit" default:"3"`       // Max items from FTS query
	ChatContextLanguage    string `json:"chatContextLanguage" default:"en"`   // Stopwords language ISO 639-1
	ChatContextMaxKeywords int    `json:"chatContextMaxKeywords" default:"8"` // Max keywords to extract from message
}

// IngestionConfig configures what content to ingest
type IngestionConfig struct {
	// Markdown ingestion patterns (relative to workspace)
	// Include patterns - files matching ANY pattern are included
	// Runtime default: ["*.md", "memory/*.md", "albums/*.md"]
	IncludePatterns []string `json:"includePatterns"`

	// Exclude patterns - files matching ANY pattern are excluded (takes priority over include)
	// Runtime default: ["skills/**", "ref/**", "goclaw/**", ".*/**"]
	ExcludePatterns []string `json:"excludePatterns"`

	// Transcript batching - combine multiple chunks per LLM call
	TranscriptBatchSize int `json:"transcriptBatchSize" default:"25"`
}

// SearchConfig configures hybrid search behavior
type SearchConfig struct {
	MaxResults int `json:"maxResults" default:"10"` // Maximum results to return

	// RRF parameters
	RRFConstant float64 `json:"rrfConstant" default:"60"` // k parameter in RRF formula

	// Source weights (should sum to 1.0)
	VectorWeight  float64 `json:"vectorWeight" default:"0.35"`  // Weight for semantic/vector search
	FTSWeight     float64 `json:"ftsWeight" default:"0.25"`     // Weight for keyword/FTS search
	GraphWeight   float64 `json:"graphWeight" default:"0.25"`   // Weight for graph traversal
	RecencyWeight float64 `json:"recencyWeight" default:"0.15"` // Weight for time-based retrieval
}

// MaintenanceConfig configures background maintenance
type MaintenanceConfig struct {
	Enabled       bool `json:"enabled" default:"true"`     // Enable background maintenance
	IntervalHours int  `json:"intervalHours" default:"24"` // Hours between maintenance runs

	// Decay settings
	ImportanceDecayRate float64 `json:"importanceDecayRate" default:"0.995"` // Daily decay multiplier
	ConfidenceDecayRate float64 `json:"confidenceDecayRate" default:"0.99"`  // Daily decay for unconfirmed patterns
	MinImportance       float64 `json:"minImportance" default:"0.1"`         // Minimum importance before soft delete
	MinConfidence       float64 `json:"minConfidence" default:"0.2"`         // Minimum confidence before invalidation

	// Access boost
	AccessBoostAmount float64 `json:"accessBoostAmount" default:"0.01"` // Amount to boost on access
	MaxImportance     float64 `json:"maxImportance" default:"1.0"`      // Cap for importance

	// Pruning
	PruneAfterDays int `json:"pruneAfterDays" default:"30"` // Days to keep forgotten memories before deletion

	// Deduplication
	DuplicateSimilarity float64 `json:"duplicateSimilarity" default:"0.95"` // Embedding similarity threshold for duplicates
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

// ConfigFormDef returns the form definition for editing memory graph configuration (zero-argument for web)
func ConfigFormDef() forms.FormDef {
	return ConfigFormDefWithValues(Config{})
}

// ConfigFormDefWithValues returns the form definition with config values for nested sections
func ConfigFormDefWithValues(cfg Config) forms.FormDef {
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
				FieldName: "liveExtraction",
				Nested:    ptrFormDef(LiveExtractionFormDef(cfg.LiveExtraction)),
			},
			{
				Title:     "Bulletin Injection",
				Collapsed: true,
				FieldName: "bulletin",
				Nested:    ptrFormDef(BulletinFormDef(cfg.Bulletin)),
			},
			{
				Title:     "Chat Context (Query-Driven)",
				Collapsed: false,
				Fields: []forms.Field{
					{
						Name:    "bulletin.chatContextEnabled",
						Title:   "Enabled",
						Type:    forms.Toggle,
						Default: true,
						Desc:    "Query memories relevant to current user message using FTS (not cached)",
					},
					{
						Name:    "bulletin.chatContextLimit",
						Title:   "Max Items",
						Type:    forms.Number,
						Default: 3,
						Min:     0,
						Max:     10,
						Desc:    "Number of relevant memories to surface per turn",
					},
					{
						Name:    "bulletin.chatContextMaxKeywords",
						Title:   "Max Keywords",
						Type:    forms.Number,
						Default: 8,
						Min:     1,
						Max:     20,
						Desc:    "Max keywords to extract from user message (longest words kept)",
					},
					{
						Name:    "bulletin.chatContextLanguage",
						Title:   "Stopwords Language",
						Type:    forms.Select,
						Default: "en",
						Options: []forms.Option{
							{Value: "ar", Label: "Arabic"},
							{Value: "bg", Label: "Bulgarian"},
							{Value: "ca", Label: "Catalan"},
							{Value: "cs", Label: "Czech"},
							{Value: "da", Label: "Danish"},
							{Value: "de", Label: "German"},
							{Value: "el", Label: "Greek"},
							{Value: "en", Label: "English"},
							{Value: "es", Label: "Spanish"},
							{Value: "fa", Label: "Persian"},
							{Value: "fi", Label: "Finnish"},
							{Value: "fr", Label: "French"},
							{Value: "hu", Label: "Hungarian"},
							{Value: "id", Label: "Indonesian"},
							{Value: "it", Label: "Italian"},
							{Value: "ja", Label: "Japanese"},
							{Value: "km", Label: "Khmer"},
							{Value: "lv", Label: "Latvian"},
							{Value: "nl", Label: "Dutch"},
							{Value: "no", Label: "Norwegian"},
							{Value: "pl", Label: "Polish"},
							{Value: "pt", Label: "Portuguese"},
							{Value: "ro", Label: "Romanian"},
							{Value: "ru", Label: "Russian"},
							{Value: "sk", Label: "Slovak"},
							{Value: "sv", Label: "Swedish"},
							{Value: "th", Label: "Thai"},
							{Value: "tr", Label: "Turkish"},
							{Value: "zu", Label: "Zulu"},
						},
						Desc: "Language for stopword removal from user messages",
					},
				},
			},
			{
				Title:     "Search Weights",
				Collapsed: true,
				FieldName: "search",
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
						Name:    "agentExtraction",
						Title:   "Agent-Driven Extraction",
						Type:    forms.Toggle,
						Default: true,
						Desc:    "Allow agents to store memories during conversation. Background extractor skips messages already processed by agents.",
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
						Name:    "handoffDelaySeconds",
						Title:   "Agent Handoff Delay",
						Type:    forms.Number,
						Default: 90,
						Min:     0,
						Max:     3600,
						Desc:    "How long to wait before the background extractor treats recent unmarked messages as its responsibility",
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
						Name:  "excludeSources",
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

// BulletinFormDef returns the form definition for bulletin injection settings
func BulletinFormDef(cfg BulletinConfig) forms.FormDef {
	return forms.FormDef{
		Title:       "Bulletin Injection",
		Description: "Configure how memory bulletins are injected into agent context",
		Sections: []forms.Section{
			{
				Title: "General",
				Fields: []forms.Field{
					{
						Name:    "enabled",
						Title:   "Enable Bulletin Injection",
						Type:    forms.Toggle,
						Default: true,
						Desc:    "Master switch for injecting memory bulletins into agent context",
					},
					{
						Name:    "ttlMinutes",
						Title:   "Cache TTL (minutes)",
						Type:    forms.Number,
						Default: 5,
						Min:     1,
						Max:     60,
						Desc:    "How long to cache bulletins before regenerating",
					},
					{
						Name:    "memoryInjection",
						Title:   "Memory Bulletin Injection",
						Type:    forms.Select,
						Default: "prompt",
						Options: []forms.Option{
							{Value: "prompt", Label: "System Prompt"},
							{Value: "message", Label: "Ephemeral Message"},
						},
						Desc: "Where to inject the memory bulletin",
					},
					{
						Name:    "contextInjection",
						Title:   "Context Bulletin Injection",
						Type:    forms.Select,
						Default: "message",
						Options: []forms.Option{
							{Value: "prompt", Label: "System Prompt"},
							{Value: "message", Label: "Ephemeral Message"},
						},
						Desc: "Where to inject the context bulletin",
					},
					{
						Name:    "deduplicate",
						Title:   "Deduplicate Items",
						Type:    forms.Toggle,
						Default: true,
						Desc:    "Skip items already shown in earlier sections",
					},
				},
			},
			{
				Title: "Injection Context",
				Fields: []forms.Field{
					{
						Name:    "injectForHeartbeat",
						Title:   "Inject for Heartbeats",
						Type:    forms.Toggle,
						Default: false,
						Desc:    "Include bulletins for heartbeat sessions (usually not needed)",
					},
					{
						Name:    "injectForCron",
						Title:   "Inject for Cron Jobs",
						Type:    forms.Toggle,
						Default: true,
						Desc:    "Include bulletins for cron job executions",
					},
				},
			},
			{
				Title: "Memory Bulletin Limits",
				Fields: []forms.Field{
					{
						Name:    "identityLimit",
						Title:   "Identity Items",
						Type:    forms.Number,
						Default: 3,
						Min:     0,
						Max:     10,
						Desc:    "Number of identity facts to include (0 = disabled)",
					},
					{
						Name:    "highPriorityLimit",
						Title:   "High Priority Items",
						Type:    forms.Number,
						Default: 3,
						Min:     0,
						Max:     10,
						Desc:    "Number of high-importance items to include (0 = disabled)",
					},
					{
						Name:    "highPriorityThreshold",
						Title:   "High Priority Threshold",
						Type:    forms.Number,
						Default: 0.8,
						Min:     0.5,
						Max:     1.0,
						Desc:    "Minimum importance score for high priority items",
					},
					{
						Name:    "goalsLimit",
						Title:   "Goals",
						Type:    forms.Number,
						Default: 3,
						Min:     0,
						Max:     10,
						Desc:    "Number of active goals to include (0 = disabled)",
					},
					{
						Name:    "preferencesLimit",
						Title:   "Preferences",
						Type:    forms.Number,
						Default: 3,
						Min:     0,
						Max:     10,
						Desc:    "Number of preferences to include (0 = disabled)",
					},
					{
						Name:    "upcomingEventsLimit",
						Title:   "Upcoming Events",
						Type:    forms.Number,
						Default: 5,
						Min:     0,
						Max:     20,
						Desc:    "Number of scheduled events, deadlines, or plans to include (0 = disabled)",
					},
					{
						Name:    "upcomingEventsDays",
						Title:   "Upcoming Events (days)",
						Type:    forms.Number,
						Default: 30,
						Min:     1,
						Max:     365,
						Desc:    "How many days ahead to look for scheduled events and deadlines",
					},
					{
						Name:    "recentEventsLimit",
						Title:   "Recent Events",
						Type:    forms.Number,
						Default: 5,
						Min:     0,
						Max:     20,
						Desc:    "Number of recent events to include (0 = disabled)",
					},
					{
						Name:    "recentEventsDays",
						Title:   "Recent Events (days)",
						Type:    forms.Number,
						Default: 7,
						Min:     1,
						Max:     30,
						Desc:    "How many days back to look for recent events",
					},
					{
						Name:    "decisionsLimit",
						Title:   "Decisions",
						Type:    forms.Number,
						Default: 3,
						Min:     0,
						Max:     10,
						Desc:    "Number of recent decisions to include (0 = disabled)",
					},
					{
						Name:    "decisionsDays",
						Title:   "Decisions (days)",
						Type:    forms.Number,
						Default: 14,
						Min:     1,
						Max:     60,
						Desc:    "How many days back to look for decisions",
					},
				},
			},
			{
				Title: "Context Bulletin Limits",
				Fields: []forms.Field{
					{
						Name:    "routinesLimit",
						Title:   "Routines",
						Type:    forms.Number,
						Default: 5,
						Min:     0,
						Max:     10,
						Desc:    "Number of active routines to include (0 = disabled)",
					},
					{
						Name:    "predictionsLimit",
						Title:   "Predictions",
						Type:    forms.Number,
						Default: 3,
						Min:     0,
						Max:     10,
						Desc:    "Number of upcoming predictions to include (0 = disabled)",
					},
					{
						Name:    "correlationsLimit",
						Title:   "Correlations",
						Type:    forms.Number,
						Default: 3,
						Min:     0,
						Max:     10,
						Desc:    "Number of known correlations to include (0 = disabled)",
					},
					{
						Name:    "anomaliesLimit",
						Title:   "Anomalies",
						Type:    forms.Number,
						Default: 3,
						Min:     0,
						Max:     10,
						Desc:    "Number of recent anomalies to include (0 = disabled)",
					},
					{
						Name:    "todosLimit",
						Title:   "Todos",
						Type:    forms.Number,
						Default: 3,
						Min:     0,
						Max:     10,
						Desc:    "Number of pending todos to include (0 = disabled)",
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

	_ = db.QueryRow("SELECT COUNT(*) FROM memories WHERE deleted = 0").Scan(&totalMemories)
	_ = db.QueryRow("SELECT COUNT(*) FROM associations").Scan(&totalAssociations)
	_ = db.QueryRow("SELECT COUNT(*) FROM ingestion_state").Scan(&ingestedCount)

	// Get counts by type
	rows, err := db.Query("SELECT memory_type, COUNT(*) FROM memories WHERE deleted = 0 GROUP BY memory_type ORDER BY COUNT(*) DESC") //nolint:rowserrcheck // stats query, errors logged below
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
	if cfg.LiveExtraction.HandoffDelaySeconds < 0 {
		return fmt.Errorf("handoffDelaySeconds must be 0 or greater")
	}
	if cfg.LiveExtraction.MinMessages < 1 {
		return fmt.Errorf("minMessages must be at least 1")
	}
	if cfg.LiveExtraction.BatchSize < 1 {
		return fmt.Errorf("batchSize must be at least 1")
	}

	// Auto-normalize search weights to sum to 1.0
	NormalizeSearchWeights(&cfg.Search)

	// Apply defaults for bulletin config
	NormalizeBulletinConfig(&cfg.Bulletin)

	return nil
}

// NormalizeBulletinConfig applies defaults for zero/invalid values
func NormalizeBulletinConfig(b *BulletinConfig) {
	// Apply defaults for zero values
	if b.TTLMinutes <= 0 {
		b.TTLMinutes = 5
	}
	if b.MemoryInjection == "" {
		b.MemoryInjection = "prompt"
	}
	if b.ContextInjection == "" {
		b.ContextInjection = "message"
	}
	// Validate injection modes
	if b.MemoryInjection != "prompt" && b.MemoryInjection != "message" {
		b.MemoryInjection = "prompt"
	}
	if b.ContextInjection != "prompt" && b.ContextInjection != "message" {
		b.ContextInjection = "message"
	}
	// Apply threshold default
	if b.HighPriorityThreshold <= 0 || b.HighPriorityThreshold > 1 {
		b.HighPriorityThreshold = 0.8
	}
	// Apply time bound defaults
	if b.RecentEventsDays <= 0 {
		b.RecentEventsDays = 7
	}
	if b.DecisionsDays <= 0 {
		b.DecisionsDays = 14
	}
	if b.UpcomingEventsDays <= 0 {
		b.UpcomingEventsDays = 30
	}
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
