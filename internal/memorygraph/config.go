package memorygraph

import (
	"fmt"

	"github.com/roelfdiedericks/goclaw/internal/bus"
	"github.com/roelfdiedericks/goclaw/internal/config/forms"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// Config configures the memory graph system
type Config struct {
	Enabled        bool                 `json:"enabled"`        // Enable memory graph
	DBPath         string               `json:"dbPath"`         // Database path (default: ~/.goclaw/memory_graph.db)
	Search         SearchConfig         `json:"search"`         // Search configuration
	Maintenance    MaintenanceConfig    `json:"maintenance"`    // Maintenance configuration
	Ingestion      IngestionConfig      `json:"ingestion"`      // Ingestion configuration
	LiveExtraction LiveExtractionConfig `json:"liveExtraction"` // Live extraction configuration
	Bulletin       BulletinConfig       `json:"bulletin"`       // Bulletin injection configuration
}

// LiveExtractionConfig configures automatic memory extraction from conversations
type LiveExtractionConfig struct {
	Enabled         bool     `json:"enabled"`         // Enable live extraction
	AgentExtraction bool     `json:"agentExtraction"` // Enable agent-driven extraction (default: true)
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

// BulletinConfig configures bulletin injection into agent context
type BulletinConfig struct {
	// General settings
	Enabled          bool   `json:"enabled"`          // Master switch for bulletin injection (default: true)
	TTLMinutes       int    `json:"ttlMinutes"`       // Cache TTL in minutes (default: 5)
	MemoryInjection  string `json:"memoryInjection"`  // "prompt" or "message" (default: "prompt")
	ContextInjection string `json:"contextInjection"` // "prompt" or "message" (default: "message")
	Deduplicate      bool   `json:"deduplicate"`      // Skip items already shown in earlier sections (default: true)

	// Injection context controls
	InjectForHeartbeat bool `json:"injectForHeartbeat"` // Inject for heartbeat sessions (default: false)
	InjectForCron      bool `json:"injectForCron"`      // Inject for cron sessions (default: true)

	// Memory bulletin section limits (0 = disabled)
	IdentityLimit         int     `json:"identityLimit"`         // Identity items (default: 3)
	HighPriorityLimit     int     `json:"highPriorityLimit"`     // High importance items (default: 3)
	HighPriorityThreshold float64 `json:"highPriorityThreshold"` // Importance threshold for high priority (default: 0.8)
	RecentEventsLimit     int     `json:"recentEventsLimit"`     // Recent event items (default: 5)
	RecentEventsDays      int     `json:"recentEventsDays"`      // Time bound for recent events (default: 7)
	DecisionsLimit        int     `json:"decisionsLimit"`        // Decision items (default: 3)
	DecisionsDays         int     `json:"decisionsDays"`         // Time bound for decisions (default: 14)
	PreferencesLimit      int     `json:"preferencesLimit"`      // Preference items (default: 3)
	GoalsLimit            int     `json:"goalsLimit"`            // Goal items (default: 3)

	// Context bulletin section limits (0 = disabled)
	RoutinesLimit     int `json:"routinesLimit"`     // Routine items (default: 5)
	PredictionsLimit  int `json:"predictionsLimit"`  // Prediction items (default: 3)
	CorrelationsLimit int `json:"correlationsLimit"` // Correlation items (default: 3)
	AnomaliesLimit    int `json:"anomaliesLimit"`    // Anomaly items (default: 3)
	TodosLimit        int `json:"todosLimit"`        // Todo items (default: 3)

	// Chat context section (query-driven, not cached)
	ChatContextEnabled     *bool  `json:"chatContextEnabled"`     // Enable chat context section (default: true)
	ChatContextLimit       int    `json:"chatContextLimit"`       // Max items from FTS query (default: 3)
	ChatContextLanguage    string `json:"chatContextLanguage"`    // Stopwords language ISO 639-1 (default: "en")
	ChatContextMaxKeywords int    `json:"chatContextMaxKeywords"` // Max keywords to extract from message (default: 8)
}

// GetChatContextEnabled returns whether chat context is enabled (defaults to true)
func (c *BulletinConfig) GetChatContextEnabled() bool {
	if c.ChatContextEnabled != nil {
		return *c.ChatContextEnabled
	}
	return true
}

// ApplyDefaults sets defaults for nil pointer fields
func (c *BulletinConfig) ApplyDefaults() {
	if c.ChatContextEnabled == nil {
		val := true
		c.ChatContextEnabled = &val
	}
	if c.ChatContextLimit <= 0 {
		c.ChatContextLimit = 3
	}
	if c.ChatContextMaxKeywords <= 0 {
		c.ChatContextMaxKeywords = 8
	}
	if c.ChatContextLanguage == "" {
		c.ChatContextLanguage = "en"
	}
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
			AgentExtraction: true,                    // Allow agents to store memories directly
			IntervalSeconds: 120,                     // Every 2 minutes
			MinMessages:     5,                       // Only extract if 5+ new messages
			MaxTurns:        10,                      // Safety limit
			BatchSize:       50,                      // Max messages per extraction
			ExcludeSources:  DefaultExcludeSources(), // Exclude automated sources
		},
		Bulletin: BulletinConfig{
			Enabled:               true,      // Enabled by default
			TTLMinutes:            5,         // 5 minute cache
			MemoryInjection:       "prompt",  // Memory bulletin in system prompt
			ContextInjection:      "message", // Context bulletin as ephemeral message
			Deduplicate:           true,      // Skip duplicates across sections
			InjectForHeartbeat:    false,     // Skip for heartbeats
			InjectForCron:         true,      // Include for cron jobs
			IdentityLimit:         3,         // Top 3 identity items
			HighPriorityLimit:     3,         // Top 3 high importance items
			HighPriorityThreshold: 0.8,       // 80% importance threshold
			RecentEventsLimit:     5,         // Last 5 events
			RecentEventsDays:      7,         // Within 7 days
			DecisionsLimit:        3,         // Last 3 decisions
			DecisionsDays:         14,        // Within 14 days
			PreferencesLimit:      3,         // Top 3 preferences
			GoalsLimit:            3,         // Top 3 goals
			RoutinesLimit:         5,         // Top 5 routines
			PredictionsLimit:      3,         // Next 3 predictions
			CorrelationsLimit:     3,         // Top 3 correlations
			AnomaliesLimit:        3,         // Last 3 anomalies
			TodosLimit:             10,       // Top 10 todos
			ChatContextLimit:       3,        // Top 3 chat context items
			ChatContextLanguage:    "en",     // English stopwords
			ChatContextMaxKeywords: 8,        // Top 8 keywords from message
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
				Title:     "Bulletin Injection",
				Collapsed: true,
				FieldName: "Bulletin",
				Nested:    ptrFormDef(BulletinFormDef(cfg.Bulletin)),
			},
			{
				Title:     "Chat Context (Query-Driven)",
				Collapsed: false,
				Fields: []forms.Field{
					{
						Name:    "Bulletin.chatContextEnabled",
						Title:   "Enabled",
						Type:    forms.Toggle,
						Default: true,
						Desc:    "Query memories relevant to current user message using FTS (not cached)",
					},
					{
						Name:    "Bulletin.chatContextLimit",
						Title:   "Max Items",
						Type:    forms.Number,
						Default: 3,
						Min:     0,
						Max:     10,
						Desc:    "Number of relevant memories to surface per turn",
					},
					{
						Name:    "Bulletin.chatContextMaxKeywords",
						Title:   "Max Keywords",
						Type:    forms.Number,
						Default: 8,
						Min:     1,
						Max:     20,
						Desc:    "Max keywords to extract from user message (longest words kept)",
					},
					{
						Name:    "Bulletin.chatContextLanguage",
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
