package metrics

import (
	"fmt"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	maxSamples = 1000 // Keep last 1000 samples for percentile calculations
)

// MetricsManager is the global metrics manager
type MetricsManager struct {
	mu          sync.RWMutex
	root        *MetricNode
	timings     map[string]*TimingMetric
	hitMiss     map[string]*HitMissMetric
	counters    map[string]*CounterMetric
	gauges      map[string]*GaugeMetric
	successFail map[string]*SuccessFailMetric
	outcomes    map[string]*OutcomeMetric
	errors      map[string]*ErrorMetric
	conditions  map[string]*ConditionMetric
	thresholds  map[string]*ThresholdMetric
	active      map[string]time.Time // For tracking active timings
	keyCounter  uint64               // For generating unique timer keys
}

var (
	instance *MetricsManager
	once     sync.Once
)

// GetInstance returns the singleton metrics manager
func GetInstance() *MetricsManager {
	once.Do(func() {
		instance = &MetricsManager{
			root: &MetricNode{
				Name:     "root",
				Path:     "",
				Children: make(map[string]*MetricNode),
			},
			timings:     make(map[string]*TimingMetric),
			hitMiss:     make(map[string]*HitMissMetric),
			counters:    make(map[string]*CounterMetric),
			gauges:      make(map[string]*GaugeMetric),
			successFail: make(map[string]*SuccessFailMetric),
			outcomes:    make(map[string]*OutcomeMetric),
			errors:      make(map[string]*ErrorMetric),
			conditions:  make(map[string]*ConditionMetric),
			thresholds:  make(map[string]*ThresholdMetric),
			active:      make(map[string]time.Time),
		}
	})
	return instance
}

// buildPath creates a normalized path from topic and function
func buildPath(topic, function string) string {
	if function == "" {
		return topic
	}
	return fmt.Sprintf("%s/%s", topic, function)
}

// getOrCreateNode ensures a node exists in the tree
func (m *MetricsManager) getOrCreateNode(path string) *MetricNode {
	parts := strings.Split(path, "/")
	current := m.root

	fullPath := ""
	for _, part := range parts {
		if fullPath == "" {
			fullPath = part
		} else {
			fullPath = fullPath + "/" + part
		}

		if current.Children == nil {
			current.Children = make(map[string]*MetricNode)
		}

		if node, exists := current.Children[part]; exists {
			current = node
		} else {
			newNode := &MetricNode{
				Name:     part,
				Path:     fullPath,
				Children: make(map[string]*MetricNode),
			}
			current.Children[part] = newNode
			current = newNode
		}
	}

	return current
}

// StartTiming begins timing an operation
func (m *MetricsManager) StartTiming(topic, function string) string {
	path := buildPath(topic, function)

	// Generate unique key to prevent collisions
	counter := atomic.AddUint64(&m.keyCounter, 1)
	key := fmt.Sprintf("%s#%d", path, counter)

	m.mu.Lock()
	m.active[key] = time.Now()
	m.mu.Unlock()

	return key
}

// EndTiming completes timing an operation
func (m *MetricsManager) EndTiming(key string) {
	m.mu.Lock()
	startTime, exists := m.active[key]
	if !exists {
		m.mu.Unlock()
		return
	}
	delete(m.active, key)
	m.mu.Unlock()

	// Extract the path from the unique key (format: path#counter)
	path := key
	if idx := strings.LastIndex(key, "#"); idx >= 0 {
		path = key[:idx]
	}

	duration := time.Since(startTime)
	m.RecordDuration(path, "", duration)
}

// RecordDuration records a duration directly
func (m *MetricsManager) RecordDuration(topic, function string, duration time.Duration) {
	path := topic
	if function != "" {
		path = buildPath(topic, function)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	metric, exists := m.timings[path]
	if !exists {
		metric = &TimingMetric{
			samples: make([]time.Duration, 0, maxSamples),
			Min:     duration,
			Max:     duration,
		}
		m.timings[path] = metric

		// Update tree
		node := m.getOrCreateNode(path)
		node.Type = TypeTiming
		node.Metric = metric
	}

	metric.mu.Lock()
	defer metric.mu.Unlock()

	metric.Count++
	metric.Total += duration
	metric.Last = duration

	if duration < metric.Min {
		metric.Min = duration
	}
	if duration > metric.Max {
		metric.Max = duration
	}

	// Add to samples ring buffer
	if len(metric.samples) < maxSamples {
		metric.samples = append(metric.samples, duration)
	} else {
		metric.samples[metric.sampleIdx] = duration
		metric.sampleIdx = (metric.sampleIdx + 1) % maxSamples
	}
}

// RecordHit records a cache hit
func (m *MetricsManager) RecordHit(topic, function string) {
	path := buildPath(topic, function)

	m.mu.Lock()
	defer m.mu.Unlock()

	metric, exists := m.hitMiss[path]
	if !exists {
		metric = &HitMissMetric{}
		m.hitMiss[path] = metric

		// Update tree
		node := m.getOrCreateNode(path)
		node.Type = TypeHitMiss
		node.Metric = metric
	}

	metric.mu.Lock()
	defer metric.mu.Unlock()

	metric.Hits++
	metric.LastHit = time.Now()
}

// RecordMiss records a cache miss
func (m *MetricsManager) RecordMiss(topic, function string) {
	path := buildPath(topic, function)

	m.mu.Lock()
	defer m.mu.Unlock()

	metric, exists := m.hitMiss[path]
	if !exists {
		metric = &HitMissMetric{}
		m.hitMiss[path] = metric

		// Update tree
		node := m.getOrCreateNode(path)
		node.Type = TypeHitMiss
		node.Metric = metric
	}

	metric.mu.Lock()
	defer metric.mu.Unlock()

	metric.Misses++
}

// IncrementCounter increments a counter
func (m *MetricsManager) IncrementCounter(topic, function string) {
	m.AddCounter(topic, function, 1)
}

// AddCounter adds to a counter
func (m *MetricsManager) AddCounter(topic, function string, delta int64) {
	path := buildPath(topic, function)

	m.mu.Lock()
	defer m.mu.Unlock()

	metric, exists := m.counters[path]
	if !exists {
		metric = &CounterMetric{}
		m.counters[path] = metric

		// Update tree
		node := m.getOrCreateNode(path)
		node.Type = TypeCounter
		node.Metric = metric
	}

	metric.mu.Lock()
	defer metric.mu.Unlock()

	metric.Value += delta
	metric.Last = time.Now()
}

// SetGauge sets a gauge value
func (m *MetricsManager) SetGauge(topic, function string, value int64) {
	path := buildPath(topic, function)

	m.mu.Lock()
	defer m.mu.Unlock()

	metric, exists := m.gauges[path]
	if !exists {
		metric = &GaugeMetric{
			Min: value,
			Max: value,
		}
		m.gauges[path] = metric

		// Update tree
		node := m.getOrCreateNode(path)
		node.Type = TypeGauge
		node.Metric = metric
	}

	metric.mu.Lock()
	defer metric.mu.Unlock()

	metric.Value = value
	metric.Last = time.Now()

	if value < metric.Min {
		metric.Min = value
	}
	if value > metric.Max {
		metric.Max = value
	}
}

// RecordSuccess records a successful operation
func (m *MetricsManager) RecordSuccess(topic, function string) {
	path := buildPath(topic, function)

	m.mu.Lock()
	metric, exists := m.successFail[path]
	if !exists {
		metric = &SuccessFailMetric{
			FailureReasons: make(map[string]int64),
		}
		m.successFail[path] = metric

		// Update tree
		node := m.getOrCreateNode(path)
		node.Type = TypeSuccessFail
		node.Metric = metric
	}
	m.mu.Unlock()

	metric.mu.Lock()
	defer metric.mu.Unlock()

	metric.Success++
	metric.LastSuccess = time.Now()

	// Update sliding window
	metric.recentWindow[metric.windowIndex] = true
	metric.windowIndex = (metric.windowIndex + 1) % len(metric.recentWindow)
	if metric.windowSize < len(metric.recentWindow) {
		metric.windowSize++
	}
}

// RecordFailure records a failed operation
func (m *MetricsManager) RecordFailure(topic, function, reason string) {
	path := buildPath(topic, function)

	m.mu.Lock()
	metric, exists := m.successFail[path]
	if !exists {
		metric = &SuccessFailMetric{
			FailureReasons: make(map[string]int64),
		}
		m.successFail[path] = metric

		// Update tree
		node := m.getOrCreateNode(path)
		node.Type = TypeSuccessFail
		node.Metric = metric
	}
	m.mu.Unlock()

	metric.mu.Lock()
	defer metric.mu.Unlock()

	metric.Failures++
	metric.LastFailure = time.Now()

	if reason != "" {
		metric.FailureReasons[reason]++
	}

	// Update sliding window
	metric.recentWindow[metric.windowIndex] = false
	metric.windowIndex = (metric.windowIndex + 1) % len(metric.recentWindow)
	if metric.windowSize < len(metric.recentWindow) {
		metric.windowSize++
	}
}

// RecordOutcome records a specific outcome
func (m *MetricsManager) RecordOutcome(topic, function, outcome string) {
	path := buildPath(topic, function)

	m.mu.Lock()
	metric, exists := m.outcomes[path]
	if !exists {
		metric = &OutcomeMetric{
			Outcomes: make(map[string]int64),
		}
		m.outcomes[path] = metric

		// Update tree
		node := m.getOrCreateNode(path)
		node.Type = TypeOutcome
		node.Metric = metric
	}
	m.mu.Unlock()

	metric.mu.Lock()
	defer metric.mu.Unlock()

	metric.Outcomes[outcome]++
	metric.Total++
	metric.LastOutcome = outcome
	metric.LastTime = time.Now()
}

// RecordError records an error by type
func (m *MetricsManager) RecordError(topic, function, errorType, errorMsg string) {
	path := buildPath(topic, function)

	m.mu.Lock()
	metric, exists := m.errors[path]
	if !exists {
		metric = &ErrorMetric{
			ErrorsByType: make(map[string]int64),
		}
		m.errors[path] = metric

		// Update tree
		node := m.getOrCreateNode(path)
		node.Type = TypeError
		node.Metric = metric
	}
	m.mu.Unlock()

	metric.mu.Lock()
	defer metric.mu.Unlock()

	metric.ErrorsByType[errorType]++
	metric.TotalErrors++
	metric.LastError = errorMsg
	metric.LastErrorType = errorType
	metric.LastErrorTime = time.Now()
}

// RecordCondition records a boolean condition
func (m *MetricsManager) RecordCondition(topic, condition string, value bool) {
	path := buildPath(topic, condition)

	m.mu.Lock()
	metric, exists := m.conditions[path]
	if !exists {
		metric = &ConditionMetric{}
		m.conditions[path] = metric

		// Update tree
		node := m.getOrCreateNode(path)
		node.Type = TypeCondition
		node.Metric = metric
	}
	m.mu.Unlock()

	metric.mu.Lock()
	defer metric.mu.Unlock()

	previousValue := metric.CurrentValue
	metric.CurrentValue = value
	metric.LastChecked = time.Now()

	if value {
		metric.TrueCount++
	} else {
		metric.FalseCount++
	}

	if previousValue != value {
		metric.LastChange = time.Now()
	}
}

// RecordThreshold checks a value against a threshold
func (m *MetricsManager) RecordThreshold(topic, metric string, value, threshold float64) {
	path := buildPath(topic, metric)

	m.mu.Lock()
	metricObj, exists := m.thresholds[path]
	if !exists {
		metricObj = &ThresholdMetric{
			Threshold: threshold,
		}
		m.thresholds[path] = metricObj

		// Update tree
		node := m.getOrCreateNode(path)
		node.Type = TypeThreshold
		node.Metric = metricObj
	}
	m.mu.Unlock()

	metricObj.mu.Lock()
	defer metricObj.mu.Unlock()

	metricObj.Value = value
	metricObj.Threshold = threshold
	metricObj.TotalChecks++
	metricObj.LastChecked = time.Now()

	exceeded := value > threshold
	metricObj.IsExceeded = exceeded

	if exceeded {
		metricObj.ExceedCount++
		metricObj.LastExceeded = time.Now()
	}
}

// GetSnapshot returns a snapshot of all metrics
func (m *MetricsManager) GetSnapshot() map[string]*MetricSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()

	snapshots := make(map[string]*MetricSnapshot)

	// Process timings
	for path, metric := range m.timings {
		metric.mu.RLock()
		avg := float64(0)
		if metric.Count > 0 {
			avg = float64(metric.Total) / float64(metric.Count) / float64(time.Millisecond)
		}

		snapshot := &MetricSnapshot{
			Path: path,
			Type: TypeTiming,
			Data: TimingSnapshot{
				Count:  metric.Count,
				AvgMs:  avg,
				MinMs:  float64(metric.Min) / float64(time.Millisecond),
				MaxMs:  float64(metric.Max) / float64(time.Millisecond),
				LastMs: float64(metric.Last) / float64(time.Millisecond),
				P95Ms:  calculatePercentile(metric.samples, 95),
				P99Ms:  calculatePercentile(metric.samples, 99),
			},
		}

		// Determine health
		snapshot.Health = getTimingHealth(avg)

		metric.mu.RUnlock()
		snapshots[path] = snapshot
	}

	// Process hit/miss
	for path, metric := range m.hitMiss {
		metric.mu.RLock()
		total := metric.Hits + metric.Misses
		hitRate := float64(0)
		if total > 0 {
			hitRate = float64(metric.Hits) / float64(total) * 100
		}

		snapshot := &MetricSnapshot{
			Path: path,
			Type: TypeHitMiss,
			Data: HitMissSnapshot{
				Hits:    metric.Hits,
				Misses:  metric.Misses,
				HitRate: hitRate,
			},
		}

		// Determine health
		snapshot.Health = getHitRateHealth(hitRate)

		metric.mu.RUnlock()
		snapshots[path] = snapshot
	}

	// Process counters
	for path, metric := range m.counters {
		metric.mu.RLock()
		snapshot := &MetricSnapshot{
			Path:   path,
			Type:   TypeCounter,
			Health: HealthGood,
			Data: CounterSnapshot{
				Value: metric.Value,
			},
		}
		metric.mu.RUnlock()
		snapshots[path] = snapshot
	}

	// Process gauges
	for path, metric := range m.gauges {
		metric.mu.RLock()
		snapshot := &MetricSnapshot{
			Path:   path,
			Type:   TypeGauge,
			Health: HealthGood,
			Data: GaugeSnapshot{
				Value: metric.Value,
				Min:   metric.Min,
				Max:   metric.Max,
			},
		}
		metric.mu.RUnlock()
		snapshots[path] = snapshot
	}

	// Process success/fail metrics
	for path, metric := range m.successFail {
		metric.mu.RLock()
		total := metric.Success + metric.Failures
		successRate := float64(0)
		if total > 0 {
			successRate = float64(metric.Success) / float64(total) * 100
		}

		// Calculate recent rate from sliding window
		recentCount := 0
		for i := 0; i < metric.windowSize; i++ {
			if metric.recentWindow[i] {
				recentCount++
			}
		}
		recentRate := float64(0)
		if metric.windowSize > 0 {
			recentRate = float64(recentCount) / float64(metric.windowSize) * 100
		}

		snapshot := &MetricSnapshot{
			Path: path,
			Type: TypeSuccessFail,
			Data: SuccessFailSnapshot{
				Success:        metric.Success,
				Failures:       metric.Failures,
				SuccessRate:    successRate,
				RecentRate:     recentRate,
				FailureReasons: metric.FailureReasons,
			},
		}

		// Determine health based on success rate
		if successRate >= 99 {
			snapshot.Health = HealthGood
		} else if successRate >= 95 {
			snapshot.Health = HealthWarning
		} else {
			snapshot.Health = HealthCritical
		}

		metric.mu.RUnlock()
		snapshots[path] = snapshot
	}

	// Process outcome metrics
	for path, metric := range m.outcomes {
		metric.mu.RLock()

		// Calculate top outcomes
		topOutcomes := make([]OutcomeStatistics, 0)
		for name, count := range metric.Outcomes {
			percentage := float64(0)
			if metric.Total > 0 {
				percentage = float64(count) / float64(metric.Total) * 100
			}
			topOutcomes = append(topOutcomes, OutcomeStatistics{
				Name:       name,
				Count:      count,
				Percentage: percentage,
			})
		}

		// Sort by count descending
		sort.Slice(topOutcomes, func(i, j int) bool {
			return topOutcomes[i].Count > topOutcomes[j].Count
		})

		// Keep top 5
		if len(topOutcomes) > 5 {
			topOutcomes = topOutcomes[:5]
		}

		snapshot := &MetricSnapshot{
			Path:   path,
			Type:   TypeOutcome,
			Health: HealthGood, // Outcomes are neutral
			Data: OutcomeSnapshot{
				Outcomes:    metric.Outcomes,
				Total:       metric.Total,
				TopOutcomes: topOutcomes,
			},
		}

		metric.mu.RUnlock()
		snapshots[path] = snapshot
	}

	// Process error metrics
	for path, metric := range m.errors {
		metric.mu.RLock()

		// Calculate error rate (simplified - could be time-based)
		errorRate := float64(0) // This could be errors per minute, etc.

		snapshot := &MetricSnapshot{
			Path: path,
			Type: TypeError,
			Data: ErrorSnapshot{
				ErrorsByType:  metric.ErrorsByType,
				TotalErrors:   metric.TotalErrors,
				LastError:     metric.LastError,
				LastErrorType: metric.LastErrorType,
				ErrorRate:     errorRate,
			},
		}

		// Health based on error count
		if metric.TotalErrors == 0 {
			snapshot.Health = HealthGood
		} else if metric.TotalErrors < 10 {
			snapshot.Health = HealthWarning
		} else {
			snapshot.Health = HealthCritical
		}

		metric.mu.RUnlock()
		snapshots[path] = snapshot
	}

	// Process condition metrics
	for path, metric := range m.conditions {
		metric.mu.RLock()

		total := metric.TrueCount + metric.FalseCount
		trueRate := float64(0)
		if total > 0 {
			trueRate = float64(metric.TrueCount) / float64(total) * 100
		}

		snapshot := &MetricSnapshot{
			Path:   path,
			Type:   TypeCondition,
			Health: HealthGood, // Conditions are neutral unless context-specific
			Data: ConditionSnapshot{
				CurrentValue: metric.CurrentValue,
				TrueCount:    metric.TrueCount,
				FalseCount:   metric.FalseCount,
				TrueRate:     trueRate,
			},
		}

		metric.mu.RUnlock()
		snapshots[path] = snapshot
	}

	// Process threshold metrics
	for path, metric := range m.thresholds {
		metric.mu.RLock()

		exceedRate := float64(0)
		if metric.TotalChecks > 0 {
			exceedRate = float64(metric.ExceedCount) / float64(metric.TotalChecks) * 100
		}

		snapshot := &MetricSnapshot{
			Path: path,
			Type: TypeThreshold,
			Data: ThresholdSnapshot{
				Value:       metric.Value,
				Threshold:   metric.Threshold,
				IsExceeded:  metric.IsExceeded,
				ExceedCount: metric.ExceedCount,
				TotalChecks: metric.TotalChecks,
				ExceedRate:  exceedRate,
			},
		}

		// Health based on whether threshold is exceeded
		if metric.IsExceeded {
			snapshot.Health = HealthCritical
		} else if exceedRate > 10 {
			snapshot.Health = HealthWarning
		} else {
			snapshot.Health = HealthGood
		}

		metric.mu.RUnlock()
		snapshots[path] = snapshot
	}

	return snapshots
}

// GetTree returns the metric tree structure
func (m *MetricsManager) GetTree() *MetricNode {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.root
}

// GetCaller returns the name of the calling function
// It automatically skips metrics package functions to find the real caller
func GetCaller() string {
	// Start at 2 to skip GetCaller itself and its immediate caller
	for skip := 2; skip < 10; skip++ {
		pc, _, _, ok := runtime.Caller(skip)
		if !ok {
			break
		}

		fn := runtime.FuncForPC(pc)
		if fn == nil {
			continue
		}

		fullName := fn.Name()

		// Skip if this is a metrics package function
		if strings.Contains(fullName, "/metrics.") ||
			strings.Contains(fullName, "MetricFunc") ||
			strings.Contains(fullName, "MetricStartAuto") ||
			strings.Contains(fullName, "MetricTimerStart") ||
			strings.Contains(fullName, "GetCaller") {
			continue
		}

		// Found a non-metrics function, extract the name
		name := fullName
		// Strip package path
		if idx := strings.LastIndex(name, "/"); idx >= 0 {
			name = name[idx+1:]
		}
		// Strip package name
		if idx := strings.Index(name, "."); idx >= 0 {
			name = name[idx+1:]
		}

		// Skip function literal closures (func1, func2, etc)
		if strings.HasPrefix(name, "func") && len(name) > 4 {
			if _, err := fmt.Sscanf(name[4:], "%d", new(int)); err == nil {
				continue // This is a closure, keep looking
			}
		}

		return name
	}

	return "unknown"
}

// calculatePercentile calculates the Nth percentile from samples
func calculatePercentile(samples []time.Duration, percentile int) float64 {
	if len(samples) == 0 {
		return 0
	}

	// Make a copy and sort
	sorted := make([]time.Duration, len(samples))
	copy(sorted, samples)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i] < sorted[j]
	})

	idx := (len(sorted) * percentile) / 100
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}

	return float64(sorted[idx]) / float64(time.Millisecond)
}

// getTimingHealth determines health based on timing
func getTimingHealth(avgMs float64) HealthStatus {
	// These thresholds can be adjusted
	if avgMs > 200 {
		return HealthCritical
	}
	if avgMs > 50 {
		return HealthWarning
	}
	return HealthGood
}

// getHitRateHealth determines health based on hit rate
func getHitRateHealth(hitRate float64) HealthStatus {
	if hitRate < 75 {
		return HealthCritical
	}
	if hitRate < 90 {
		return HealthWarning
	}
	return HealthGood
}
