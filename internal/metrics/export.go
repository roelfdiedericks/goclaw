package metrics

import (
	"time"
)

// Global functions for dot-import usage

// MetricStart begins timing an operation
func MetricStart(topic, function string) string {
	return GetInstance().StartTiming(topic, function)
}

// MetricEnd completes timing an operation
func MetricEnd(key string) {
	GetInstance().EndTiming(key)
}

// MetricDuration records a duration directly
func MetricDuration(topic, function string, duration time.Duration) {
	GetInstance().RecordDuration(topic, function, duration)
}

// MetricStartAuto begins timing with automatic function detection
func MetricStartAuto(topic string) func() {
	function := GetCaller()
	key := GetInstance().StartTiming(topic, function)
	return func() {
		GetInstance().EndTiming(key)
	}
}

// MetricFunc wraps an entire function for timing
func MetricFunc(topic string) func() {
	return MetricStartAuto(topic)
}

// MetricHit records a cache hit
func MetricHit(topic, function string) {
	GetInstance().RecordHit(topic, function)
}

// MetricMiss records a cache miss
func MetricMiss(topic, function string) {
	GetInstance().RecordMiss(topic, function)
}

// MetricInc increments a counter by 1
func MetricInc(topic, function string) {
	GetInstance().IncrementCounter(topic, function)
}

// MetricAdd adds a value to a counter
func MetricAdd(topic, function string, delta int64) {
	GetInstance().AddCounter(topic, function, delta)
}

// MetricSet sets a gauge value
func MetricSet(topic, function string, value int64) {
	GetInstance().SetGauge(topic, function, value)
}

// MetricTimingWithFunc times a function execution
func MetricTimingWithFunc(topic, function string, fn func()) {
	start := time.Now()
	fn()
	GetInstance().RecordDuration(topic, function, time.Since(start))
}

// MetricTimerStart begins timing an operation with clearer naming
// Returns a timer key that must be passed to MetricTimerStop
func MetricTimerStart(topic, function string) string {
	return GetInstance().StartTiming(topic, function)
}

// MetricTimerStop completes timing an operation with clearer naming
func MetricTimerStop(key string) {
	GetInstance().EndTiming(key)
}

// MetricSuccess records a successful operation
func MetricSuccess(topic, operation string) {
	GetInstance().RecordSuccess(topic, operation)
}

// MetricFail records a failed operation without reason
func MetricFail(topic, operation string) {
	GetInstance().RecordFailure(topic, operation, "")
}

// MetricFailWithReason records a failed operation with a specific reason
func MetricFailWithReason(topic, operation, reason string) {
	GetInstance().RecordFailure(topic, operation, reason)
}

// MetricOutcome records a specific outcome
func MetricOutcome(topic, operation, outcome string) {
	GetInstance().RecordOutcome(topic, operation, outcome)
}

// MetricError records an error with type classification
func MetricError(topic, operation, errorType string) {
	GetInstance().RecordError(topic, operation, errorType, "")
}

// MetricErrorWithMsg records an error with type and message
func MetricErrorWithMsg(topic, operation, errorType, errorMsg string) {
	GetInstance().RecordError(topic, operation, errorType, errorMsg)
}

// MetricCondition records a boolean condition
func MetricCondition(topic, condition string, value bool) {
	GetInstance().RecordCondition(topic, condition, value)
}

// MetricThreshold checks a value against a threshold
func MetricThreshold(topic, metric string, value, threshold float64) {
	GetInstance().RecordThreshold(topic, metric, value, threshold)
}
