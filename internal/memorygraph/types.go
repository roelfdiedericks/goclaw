package memorygraph

import (
	"time"
)

// Type represents the semantic category of a memory
type Type string

const (
	// Core types (from Spacebot)
	TypeIdentity    Type = "identity"    // Who the user/agent is (never decays)
	TypeFact        Type = "fact"        // Things that are true
	TypePreference  Type = "preference"  // Likes, dislikes, preferences
	TypeDecision    Type = "decision"    // Choices that were made
	TypeEvent       Type = "event"       // Things that happened
	TypeObservation Type = "observation" // Things noticed
	TypeGoal        Type = "goal"        // Objectives, aspirations
	TypeTodo        Type = "todo"        // Actionable tasks

	// Anticipatory Intelligence types
	TypeRoutine     Type = "routine"     // Validated pattern with action
	TypeFeedback    Type = "feedback"    // User reaction to agent action
	TypeAnomaly     Type = "anomaly"     // Deviation from expected pattern
	TypeCorrelation Type = "correlation" // Observed relationship (not causal)
	TypePrediction  Type = "prediction"  // Forward-looking anticipation
)

// DefaultImportance returns the default importance for a memory type
var DefaultImportance = map[Type]float32{
	TypeIdentity:    1.0,
	TypeGoal:        0.9,
	TypeRoutine:     0.85,
	TypeDecision:    0.8,
	TypeTodo:        0.8,
	TypePreference:  0.7,
	TypeFact:        0.6,
	TypeCorrelation: 0.5,
	TypePrediction:  0.5,
	TypeEvent:       0.4,
	TypeFeedback:    0.4,
	TypeObservation: 0.3,
	TypeAnomaly:     0.3,
}

// ConfidenceNotApplicable is the sentinel value for memories where confidence doesn't apply
const ConfidenceNotApplicable float32 = -1

// RelationType represents the type of relationship between two memories
type RelationType string

const (
	// Core relations (from Spacebot)
	RelationRelatedTo   RelationType = "related_to"   // General semantic connection
	RelationUpdates     RelationType = "updates"      // Newer version of same info
	RelationContradicts RelationType = "contradicts"  // Conflicting information
	RelationCausedBy    RelationType = "caused_by"    // Causal relationship
	RelationResultOf    RelationType = "result_of"    // Effect relationship
	RelationPartOf      RelationType = "part_of"      // Hierarchical/containment

	// Anticipatory Intelligence relations
	RelationTriggers   RelationType = "triggers"   // Routine → Todo/Action
	RelationReinforces RelationType = "reinforces" // Feedback(+) → Routine
	RelationWeakens    RelationType = "weakens"    // Feedback(-) → Routine
	RelationViolated   RelationType = "violated"   // Anomaly → Routine
	RelationPredicts   RelationType = "predicts"   // Correlation → Routine
	RelationConfirmed  RelationType = "confirmed"  // Event → Prediction
	RelationOverrides  RelationType = "overrides"  // Preference → Routine
)

// DefaultDirected returns whether a relation type is directed by default
var DefaultDirected = map[RelationType]bool{
	RelationRelatedTo:   false, // Bidirectional
	RelationUpdates:     true,  // Newer → Older
	RelationContradicts: false, // Bidirectional
	RelationCausedBy:    true,  // Effect → Cause
	RelationResultOf:    true,  // Result → Action
	RelationPartOf:      true,  // Part → Whole
	RelationTriggers:    true,  // Routine → Action
	RelationReinforces:  true,  // Feedback → Routine
	RelationWeakens:     true,  // Feedback → Routine
	RelationViolated:    true,  // Anomaly → Routine
	RelationPredicts:    true,  // Correlation → Routine
	RelationConfirmed:   true,  // Event → Prediction
	RelationOverrides:   true,  // Preference → Routine
}

// Memory represents a single memory node in the graph
type Memory struct {
	ID             int64     `json:"-"`                            // Internal SQLite rowid
	UUID           string    `json:"id"`                           // External ID (ULID)
	Content        string    `json:"content"`                      // Memory content
	Type           Type      `json:"type"`                         // Memory type
	Importance     float32   `json:"importance"`                   // 0.0-1.0, affects recall priority
	Confidence     float32   `json:"confidence"`                   // 0.0-1.0 or -1 for not applicable
	CreatedAt      time.Time `json:"created_at"`                   // When created
	UpdatedAt      time.Time `json:"updated_at"`                   // When last updated
	LastAccessedAt time.Time `json:"last_accessed_at"`             // When last accessed
	AccessCount    int64     `json:"access_count"`                 // Number of times accessed
	NextTriggerAt  *time.Time `json:"next_trigger_at,omitempty"`   // For routines/predictions
	Source         string    `json:"source,omitempty"`             // Origin: "conversation", "extraction", "manual", "import"
	SourceSession  string    `json:"source_session,omitempty"`     // Session key
	SourceMessage  string    `json:"source_message,omitempty"`     // Message ID
	Username       string    `json:"username,omitempty"`           // User.ID (username)
	Channel        string    `json:"channel,omitempty"`            // Channel name
	ChatID         string    `json:"chat_id,omitempty"`            // Channel-specific chat ID
	Forgotten      bool      `json:"forgotten"`                    // Soft delete flag
	Embedding      []float32 `json:"-"`                            // Vector embedding
	EmbeddingModel string    `json:"-"`                            // Model that generated embedding
}

// Association represents a directed or undirected edge between two memories
type Association struct {
	ID           string       `json:"id"`            // ULID
	SourceID     string       `json:"source_id"`     // Source memory UUID
	TargetID     string       `json:"target_id"`     // Target memory UUID
	RelationType RelationType `json:"relation_type"` // Type of relation
	Weight       float32      `json:"weight"`        // 0.0-1.0
	Directed     bool         `json:"directed"`      // If true, edge only flows Source→Target
	CreatedAt    time.Time    `json:"created_at"`    // When created
}

// RoutineMetadata contains metadata specific to routine memories
type RoutineMetadata struct {
	MemoryUUID       string     `json:"memory_uuid"`
	TriggerType      string     `json:"trigger_type"`      // "time", "event", "context", "sequence"
	TriggerCron      string     `json:"trigger_cron"`      // Cron expression for time triggers
	TriggerEvent     string     `json:"trigger_event"`     // Event name for event triggers
	TriggerCondition string     `json:"trigger_condition"` // Condition for context triggers
	Action           string     `json:"action"`            // Tool/action to execute
	ActionEntity     string     `json:"action_entity"`     // Primary entity/target
	ActionExtra      string     `json:"action_extra"`      // Additional params
	Autonomy         string     `json:"autonomy"`          // "observe", "suggest", "confirm", "auto", "silent"
	Observations     int64      `json:"observations"`
	Suggestions      int64      `json:"suggestions"`
	Acceptances      int64      `json:"acceptances"`
	Rejections       int64      `json:"rejections"`
	AutoRuns         int64      `json:"auto_runs"`
	LastTriggeredAt  *time.Time `json:"last_triggered_at"`
}

// FeedbackMetadata contains metadata specific to feedback memories
type FeedbackMetadata struct {
	MemoryUUID   string `json:"memory_uuid"`
	RoutineUUID  string `json:"routine_uuid"`  // UUID of routine this feedback is about
	FeedbackType string `json:"feedback_type"` // "accept", "reject", "modify", "praise"
	ContextDay   string `json:"context_day"`   // Day of week
	ContextTime  string `json:"context_time"`  // Time of day
	UserNote     string `json:"user_note"`     // User's explanation
}

// AnomalyMetadata contains metadata specific to anomaly memories
type AnomalyMetadata struct {
	MemoryUUID  string `json:"memory_uuid"`
	RoutineUUID string `json:"routine_uuid"` // UUID of routine that was violated
	Expected    string `json:"expected"`     // What was expected
	Actual      string `json:"actual"`       // What actually happened
	Severity    string `json:"severity"`     // "low", "medium", "high"
}

// CorrelationMetadata contains metadata specific to correlation memories
type CorrelationMetadata struct {
	MemoryUUID     string     `json:"memory_uuid"`
	Condition      string     `json:"condition"`        // e.g., "sensor.temperature > 28"
	Outcome        string     `json:"outcome"`          // e.g., "user requests aircon"
	Strength       float32    `json:"strength"`         // Correlation strength 0.0-1.0
	Observations   int64      `json:"observations"`
	LastObservedAt *time.Time `json:"last_observed_at"`
}

// PredictionMetadata contains metadata specific to prediction memories
type PredictionMetadata struct {
	MemoryUUID             string    `json:"memory_uuid"`
	RoutineUUID            string    `json:"routine_uuid"`            // UUID of routine this prediction is for
	PredictedTime          time.Time `json:"predicted_time"`
	Action                 string    `json:"action"`
	Outcome                string    `json:"outcome"`                  // "", "confirmed", "missed", "rejected"
	ConfidenceAtPrediction float32   `json:"confidence_at_prediction"`
}

// SearchResult represents a memory returned from a search with scoring info
type SearchResult struct {
	Memory       Memory             `json:"memory"`
	Score        float32            `json:"score"`
	Rank         int                `json:"rank"`
	SourceScores map[string]float32 `json:"source_scores,omitempty"` // Scores from each retriever
}

// ExtractedMemory represents a memory extracted by the LLM
type ExtractedMemory struct {
	Content      string            `json:"content"`
	Type         Type              `json:"type"`
	Importance   float32           `json:"importance"`
	Confidence   *float32          `json:"confidence,omitempty"`
	Associations []AssociationHint `json:"associations,omitempty"`
}

// AssociationHint is a suggestion for an association from the LLM
type AssociationHint struct {
	TargetID     string       `json:"target_id"`
	RelationType RelationType `json:"relation_type"`
}

// MaintenanceReport summarizes the results of a maintenance run
type MaintenanceReport struct {
	ImportanceDecayed int `json:"importance_decayed"`
	ConfidenceDecayed int `json:"confidence_decayed"`
	Boosted           int `json:"boosted"`
	Pruned            int `json:"pruned"`
	Merged            int `json:"merged"`
	TriggersUpdated   int `json:"triggers_updated"`
}
