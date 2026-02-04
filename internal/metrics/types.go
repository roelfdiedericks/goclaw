package metrics

import (
	"sync"
	"time"
)

// MetricType represents the type of metric
type MetricType string

const (
	TypeTiming      MetricType = "timing"
	TypeHitMiss     MetricType = "hit_miss"
	TypeCounter     MetricType = "counter"
	TypeGauge       MetricType = "gauge"
	TypeSuccessFail MetricType = "success_fail"
	TypeOutcome     MetricType = "outcome"
	TypeError       MetricType = "error"
	TypeCondition   MetricType = "condition"
	TypeThreshold   MetricType = "threshold"
)

// HealthStatus represents the health of a metric
type HealthStatus int

const (
	HealthGood     HealthStatus = iota // Green
	HealthWarning                      // Yellow
	HealthCritical                     // Red
)

// TimingMetric tracks timing statistics
type TimingMetric struct {
	mu        sync.RWMutex
	Count     int64
	Total     time.Duration
	Min       time.Duration
	Max       time.Duration
	Last      time.Duration
	samples   []time.Duration // Ring buffer for percentiles
	sampleIdx int
}

// HitMissMetric tracks cache hit/miss statistics
type HitMissMetric struct {
	mu      sync.RWMutex
	Hits    int64
	Misses  int64
	LastHit time.Time
}

// CounterMetric tracks incrementing values
type CounterMetric struct {
	mu    sync.RWMutex
	Value int64
	Last  time.Time
}

// GaugeMetric tracks values that can go up or down
type GaugeMetric struct {
	mu    sync.RWMutex
	Value int64
	Min   int64
	Max   int64
	Last  time.Time
}

// MetricNode represents a node in the metric tree
type MetricNode struct {
	Name     string
	Path     string
	Type     MetricType
	Children map[string]*MetricNode
	Metric   interface{} // Points to actual metric (TimingMetric, HitMissMetric, etc.)
}

// MetricSnapshot represents a point-in-time view of a metric
type MetricSnapshot struct {
	Path   string       `json:"path"`
	Type   MetricType   `json:"type"`
	Health HealthStatus `json:"health"`
	Data   interface{}  `json:"data"`
}

// TimingSnapshot for JSON serialization
type TimingSnapshot struct {
	Count  int64   `json:"count"`
	AvgMs  float64 `json:"avg_ms"`
	MinMs  float64 `json:"min_ms"`
	MaxMs  float64 `json:"max_ms"`
	LastMs float64 `json:"last_ms"`
	P95Ms  float64 `json:"p95_ms,omitempty"`
	P99Ms  float64 `json:"p99_ms,omitempty"`
}

// HitMissSnapshot for JSON serialization
type HitMissSnapshot struct {
	Hits    int64   `json:"hits"`
	Misses  int64   `json:"misses"`
	HitRate float64 `json:"hit_rate"`
}

// CounterSnapshot for JSON serialization
type CounterSnapshot struct {
	Value int64 `json:"value"`
}

// GaugeSnapshot for JSON serialization
type GaugeSnapshot struct {
	Value int64 `json:"value"`
	Min   int64 `json:"min"`
	Max   int64 `json:"max"`
}

// SuccessFailMetric tracks success and failure counts
type SuccessFailMetric struct {
	mu             sync.RWMutex
	Success        int64
	Failures       int64
	LastSuccess    time.Time
	LastFailure    time.Time
	FailureReasons map[string]int64 // reason -> count
	// Sliding window for recent rate calculation (last 100 operations)
	recentWindow [100]bool
	windowIndex  int
	windowSize   int
}

// OutcomeMetric tracks multiple possible outcomes
type OutcomeMetric struct {
	mu          sync.RWMutex
	Outcomes    map[string]int64 // outcome -> count
	LastOutcome string
	LastTime    time.Time
	Total       int64
}

// ErrorMetric tracks errors by type
type ErrorMetric struct {
	mu            sync.RWMutex
	ErrorsByType  map[string]int64
	TotalErrors   int64
	LastError     string
	LastErrorType string
	LastErrorTime time.Time
}

// ConditionMetric tracks boolean conditions
type ConditionMetric struct {
	mu           sync.RWMutex
	CurrentValue bool
	TrueCount    int64
	FalseCount   int64
	LastChange   time.Time
	LastChecked  time.Time
}

// ThresholdMetric tracks values against thresholds
type ThresholdMetric struct {
	mu           sync.RWMutex
	Value        float64
	Threshold    float64
	ExceedCount  int64 // Times value exceeded threshold
	TotalChecks  int64
	LastExceeded time.Time
	LastChecked  time.Time
	IsExceeded   bool
}

// SuccessFailSnapshot for JSON serialization
type SuccessFailSnapshot struct {
	Success        int64            `json:"success"`
	Failures       int64            `json:"failures"`
	SuccessRate    float64          `json:"success_rate"`
	RecentRate     float64          `json:"recent_rate"` // Rate from sliding window
	FailureReasons map[string]int64 `json:"failure_reasons,omitempty"`
}

// OutcomeSnapshot for JSON serialization
type OutcomeSnapshot struct {
	Outcomes    map[string]int64    `json:"outcomes"`
	Total       int64               `json:"total"`
	TopOutcomes []OutcomeStatistics `json:"top_outcomes"`
}

// OutcomeStatistics for top outcomes
type OutcomeStatistics struct {
	Name       string  `json:"name"`
	Count      int64   `json:"count"`
	Percentage float64 `json:"percentage"`
}

// ErrorSnapshot for JSON serialization
type ErrorSnapshot struct {
	ErrorsByType  map[string]int64 `json:"errors_by_type"`
	TotalErrors   int64            `json:"total_errors"`
	LastError     string           `json:"last_error,omitempty"`
	LastErrorType string           `json:"last_error_type,omitempty"`
	ErrorRate     float64          `json:"error_rate"`
}

// ConditionSnapshot for JSON serialization
type ConditionSnapshot struct {
	CurrentValue bool    `json:"current_value"`
	TrueCount    int64   `json:"true_count"`
	FalseCount   int64   `json:"false_count"`
	TrueRate     float64 `json:"true_rate"`
}

// ThresholdSnapshot for JSON serialization
type ThresholdSnapshot struct {
	Value       float64 `json:"value"`
	Threshold   float64 `json:"threshold"`
	IsExceeded  bool    `json:"is_exceeded"`
	ExceedCount int64   `json:"exceed_count"`
	TotalChecks int64   `json:"total_checks"`
	ExceedRate  float64 `json:"exceed_rate"`
}
