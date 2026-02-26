package metrics

import (
	"database/sql"
	"encoding/json"
	"time"

	_ "github.com/mattn/go-sqlite3"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/paths"
)

const (
	saveInterval  = 5 * time.Minute
	pruneMaxAge   = 7 * 24 * time.Hour
	dbFileName    = "metrics.db"
	dbOpenOptions = "?_busy_timeout=5000"
)

const schemaSQL = `CREATE TABLE IF NOT EXISTS metrics (
	path       TEXT PRIMARY KEY,
	type       TEXT NOT NULL,
	data       BLOB NOT NULL,
	updated_at INTEGER NOT NULL
)`

// initPersistence opens the metrics DB, loads persisted data, prunes stale entries,
// and starts a background save ticker. Degrades to in-memory if anything fails.
func (m *MetricsManager) initPersistence() {
	dbPath, err := paths.DataPath(dbFileName)
	if err != nil {
		L_warn("metrics: persistence disabled, cannot resolve data path", "error", err)
		return
	}

	if err := paths.EnsureParentDir(dbPath); err != nil {
		L_warn("metrics: persistence disabled, cannot create directory", "error", err)
		return
	}

	db, err := sql.Open("sqlite3", dbPath+dbOpenOptions)
	if err != nil {
		L_warn("metrics: persistence disabled, cannot open database", "error", err)
		return
	}

	if _, err := db.Exec(schemaSQL); err != nil {
		L_warn("metrics: persistence disabled, schema creation failed", "error", err)
		db.Close()
		return
	}

	m.db = db
	m.stopSave = make(chan struct{})

	loaded, err := m.load()
	if err != nil {
		L_warn("metrics: failed to load persisted data", "error", err)
	} else if loaded > 0 {
		L_info("metrics: loaded persisted data", "count", loaded)
	}

	pruned, err := m.prune()
	if err != nil {
		L_warn("metrics: failed to prune stale data", "error", err)
	} else if pruned > 0 {
		L_info("metrics: pruned stale metrics", "count", pruned)
	}

	go m.saveLoop()
}

// saveLoop runs periodic saves until stopSave is closed.
func (m *MetricsManager) saveLoop() {
	ticker := time.NewTicker(saveInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if err := m.save(); err != nil {
				L_warn("metrics: periodic save failed", "error", err)
			}
		case <-m.stopSave:
			return
		}
	}
}

// Close stops the background save ticker, performs a final save, and closes the DB.
// Safe to call even if persistence was never initialized.
func (m *MetricsManager) Close() error {
	if m.db == nil {
		return nil
	}

	if m.stopSave != nil {
		close(m.stopSave)
	}

	if err := m.save(); err != nil {
		L_warn("metrics: final save failed", "error", err)
	}

	err := m.db.Close()
	m.db = nil
	return err
}

// save writes all metrics to the database in a single transaction.
func (m *MetricsManager) save() error {
	if m.db == nil {
		return nil
	}

	tx, err := m.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	stmt, err := tx.Prepare(`INSERT INTO metrics (path, type, data, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET data = excluded.data, updated_at = excluded.updated_at`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	now := time.Now().Unix()

	m.mu.RLock()
	defer m.mu.RUnlock()

	if err := saveMapEntries(stmt, now, m.timings, TypeTiming, marshalTiming); err != nil {
		return err
	}
	if err := saveMapEntries(stmt, now, m.hitMiss, TypeHitMiss, marshalHitMiss); err != nil {
		return err
	}
	if err := saveMapEntries(stmt, now, m.counters, TypeCounter, marshalCounter); err != nil {
		return err
	}
	if err := saveMapEntries(stmt, now, m.gauges, TypeGauge, marshalGauge); err != nil {
		return err
	}
	if err := saveMapEntries(stmt, now, m.successFail, TypeSuccessFail, marshalSuccessFail); err != nil {
		return err
	}
	if err := saveMapEntries(stmt, now, m.outcomes, TypeOutcome, marshalOutcome); err != nil {
		return err
	}
	if err := saveMapEntries(stmt, now, m.errors, TypeError, marshalError); err != nil {
		return err
	}
	if err := saveMapEntries(stmt, now, m.conditions, TypeCondition, marshalCondition); err != nil {
		return err
	}
	if err := saveMapEntries(stmt, now, m.thresholds, TypeThreshold, marshalThreshold); err != nil {
		return err
	}
	if err := saveMapEntries(stmt, now, m.costs, TypeCost, marshalCost); err != nil {
		return err
	}

	return tx.Commit()
}

// saveMapEntries serializes all entries in a metric map and upserts them.
func saveMapEntries[T any](stmt *sql.Stmt, now int64, metrics map[string]*T, metricType MetricType, marshal func(*T) ([]byte, error)) error {
	for path, metric := range metrics {
		data, err := marshal(metric)
		if err != nil {
			L_warn("metrics: failed to marshal metric", "path", path, "type", metricType, "error", err)
			continue
		}
		if _, err := stmt.Exec(path, string(metricType), data, now); err != nil {
			return err
		}
	}
	return nil
}

// load reads all persisted metrics from the database and restores them in memory.
func (m *MetricsManager) load() (int, error) {
	rows, err := m.db.Query("SELECT path, type, data FROM metrics")
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	m.mu.Lock()
	defer m.mu.Unlock()

	count := 0
	for rows.Next() {
		var path, metricType string
		var data []byte
		if err := rows.Scan(&path, &metricType, &data); err != nil {
			L_warn("metrics: failed to scan row", "error", err)
			continue
		}

		if err := m.restoreMetric(path, MetricType(metricType), data); err != nil {
			L_warn("metrics: failed to restore metric", "path", path, "type", metricType, "error", err)
			continue
		}
		count++
	}

	return count, rows.Err()
}

// prune deletes metrics not updated within the retention period.
func (m *MetricsManager) prune() (int, error) {
	cutoff := time.Now().Add(-pruneMaxAge).Unix()
	result, err := m.db.Exec("DELETE FROM metrics WHERE updated_at < ?", cutoff)
	if err != nil {
		return 0, err
	}
	n, _ := result.RowsAffected()
	return int(n), nil
}

// restoreMetric deserializes a metric and registers it in the appropriate map and tree.
// Must be called with m.mu held.
func (m *MetricsManager) restoreMetric(path string, metricType MetricType, data []byte) error {
	node := m.getOrCreateNode(path)
	node.Type = metricType

	switch metricType {
	case TypeTiming:
		metric, err := unmarshalTiming(data)
		if err != nil {
			return err
		}
		m.timings[path] = metric
		node.Metric = metric

	case TypeHitMiss:
		metric, err := unmarshalHitMiss(data)
		if err != nil {
			return err
		}
		m.hitMiss[path] = metric
		node.Metric = metric

	case TypeCounter:
		metric, err := unmarshalCounter(data)
		if err != nil {
			return err
		}
		m.counters[path] = metric
		node.Metric = metric

	case TypeGauge:
		metric, err := unmarshalGauge(data)
		if err != nil {
			return err
		}
		m.gauges[path] = metric
		node.Metric = metric

	case TypeSuccessFail:
		metric, err := unmarshalSuccessFail(data)
		if err != nil {
			return err
		}
		m.successFail[path] = metric
		node.Metric = metric

	case TypeOutcome:
		metric, err := unmarshalOutcome(data)
		if err != nil {
			return err
		}
		m.outcomes[path] = metric
		node.Metric = metric

	case TypeError:
		metric, err := unmarshalError(data)
		if err != nil {
			return err
		}
		m.errors[path] = metric
		node.Metric = metric

	case TypeCondition:
		metric, err := unmarshalCondition(data)
		if err != nil {
			return err
		}
		m.conditions[path] = metric
		node.Metric = metric

	case TypeThreshold:
		metric, err := unmarshalThreshold(data)
		if err != nil {
			return err
		}
		m.thresholds[path] = metric
		node.Metric = metric

	case TypeCost:
		metric, err := unmarshalCost(data)
		if err != nil {
			return err
		}
		m.costs[path] = metric
		node.Metric = metric
	}

	return nil
}

// ---- Intermediary structs for serialization ----
// These mirror metric fields but are JSON-safe (no mutex, no unexported ring buffers).

type persistTiming struct {
	Count   int64           `json:"count"`
	Total   time.Duration   `json:"total"`
	Min     time.Duration   `json:"min"`
	Max     time.Duration   `json:"max"`
	Last    time.Duration   `json:"last"`
	Samples []time.Duration `json:"samples,omitempty"`
}

type persistHitMiss struct {
	Hits    int64     `json:"hits"`
	Misses  int64     `json:"misses"`
	LastHit time.Time `json:"last_hit"`
}

type persistCounter struct {
	Value int64     `json:"value"`
	Last  time.Time `json:"last"`
}

type persistGauge struct {
	Value int64     `json:"value"`
	Min   int64     `json:"min"`
	Max   int64     `json:"max"`
	Last  time.Time `json:"last"`
}

type persistSuccessFail struct {
	Success        int64            `json:"success"`
	Failures       int64            `json:"failures"`
	LastSuccess    time.Time        `json:"last_success"`
	LastFailure    time.Time        `json:"last_failure"`
	FailureReasons map[string]int64 `json:"failure_reasons,omitempty"`
}

type persistOutcome struct {
	Outcomes    map[string]int64 `json:"outcomes"`
	LastOutcome string           `json:"last_outcome"`
	LastTime    time.Time        `json:"last_time"`
	Total       int64            `json:"total"`
}

type persistError struct {
	ErrorsByType  map[string]int64 `json:"errors_by_type"`
	TotalErrors   int64            `json:"total_errors"`
	LastError     string           `json:"last_error"`
	LastErrorType string           `json:"last_error_type"`
	LastErrorTime time.Time        `json:"last_error_time"`
}

type persistCondition struct {
	CurrentValue bool      `json:"current_value"`
	TrueCount    int64     `json:"true_count"`
	FalseCount   int64     `json:"false_count"`
	LastChange   time.Time `json:"last_change"`
	LastChecked  time.Time `json:"last_checked"`
}

type persistThreshold struct {
	Value        float64   `json:"value"`
	Threshold    float64   `json:"threshold"`
	ExceedCount  int64     `json:"exceed_count"`
	TotalChecks  int64     `json:"total_checks"`
	LastExceeded time.Time `json:"last_exceeded"`
	LastChecked  time.Time `json:"last_checked"`
	IsExceeded   bool      `json:"is_exceeded"`
}

type persistCost struct {
	Total int64 `json:"total"`
	Last  int64 `json:"last"`
	Min   int64 `json:"min"`
	Max   int64 `json:"max"`
	Count int64 `json:"count"`
}

// ---- Marshal helpers (per-type, no type assertions needed) ----

func marshalTiming(m *TimingMetric) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return json.Marshal(persistTiming{
		Count: m.Count, Total: m.Total, Min: m.Min, Max: m.Max, Last: m.Last, Samples: m.samples,
	})
}

func marshalHitMiss(m *HitMissMetric) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return json.Marshal(persistHitMiss{Hits: m.Hits, Misses: m.Misses, LastHit: m.LastHit})
}

func marshalCounter(m *CounterMetric) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return json.Marshal(persistCounter{Value: m.Value, Last: m.Last})
}

func marshalGauge(m *GaugeMetric) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return json.Marshal(persistGauge{Value: m.Value, Min: m.Min, Max: m.Max, Last: m.Last})
}

func marshalSuccessFail(m *SuccessFailMetric) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return json.Marshal(persistSuccessFail{
		Success: m.Success, Failures: m.Failures,
		LastSuccess: m.LastSuccess, LastFailure: m.LastFailure, FailureReasons: m.FailureReasons,
	})
}

func marshalOutcome(m *OutcomeMetric) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return json.Marshal(persistOutcome{
		Outcomes: m.Outcomes, LastOutcome: m.LastOutcome, LastTime: m.LastTime, Total: m.Total,
	})
}

func marshalError(m *ErrorMetric) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return json.Marshal(persistError{
		ErrorsByType: m.ErrorsByType, TotalErrors: m.TotalErrors,
		LastError: m.LastError, LastErrorType: m.LastErrorType, LastErrorTime: m.LastErrorTime,
	})
}

func marshalCondition(m *ConditionMetric) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return json.Marshal(persistCondition{
		CurrentValue: m.CurrentValue, TrueCount: m.TrueCount, FalseCount: m.FalseCount,
		LastChange: m.LastChange, LastChecked: m.LastChecked,
	})
}

func marshalThreshold(m *ThresholdMetric) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return json.Marshal(persistThreshold{
		Value: m.Value, Threshold: m.Threshold, ExceedCount: m.ExceedCount,
		TotalChecks: m.TotalChecks, LastExceeded: m.LastExceeded, LastChecked: m.LastChecked,
		IsExceeded: m.IsExceeded,
	})
}

func marshalCost(m *CostMetric) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return json.Marshal(persistCost{Total: m.Total, Last: m.Last, Min: m.Min, Max: m.Max, Count: m.Count})
}

// ---- Unmarshal helpers ----

func unmarshalTiming(data []byte) (*TimingMetric, error) {
	var p persistTiming
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	m := &TimingMetric{
		Count:   p.Count,
		Total:   p.Total,
		Min:     p.Min,
		Max:     p.Max,
		Last:    p.Last,
		samples: p.Samples,
	}
	if m.samples == nil {
		m.samples = make([]time.Duration, 0, maxSamples)
	}
	if len(m.samples) >= maxSamples {
		m.sampleIdx = 0
	} else {
		m.sampleIdx = len(m.samples)
	}
	return m, nil
}

func unmarshalHitMiss(data []byte) (*HitMissMetric, error) {
	var p persistHitMiss
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	return &HitMissMetric{
		Hits:    p.Hits,
		Misses:  p.Misses,
		LastHit: p.LastHit,
	}, nil
}

func unmarshalCounter(data []byte) (*CounterMetric, error) {
	var p persistCounter
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	return &CounterMetric{
		Value: p.Value,
		Last:  p.Last,
	}, nil
}

func unmarshalGauge(data []byte) (*GaugeMetric, error) {
	var p persistGauge
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	return &GaugeMetric{
		Value: p.Value,
		Min:   p.Min,
		Max:   p.Max,
		Last:  p.Last,
	}, nil
}

func unmarshalSuccessFail(data []byte) (*SuccessFailMetric, error) {
	var p persistSuccessFail
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	if p.FailureReasons == nil {
		p.FailureReasons = make(map[string]int64)
	}
	return &SuccessFailMetric{
		Success:        p.Success,
		Failures:       p.Failures,
		LastSuccess:    p.LastSuccess,
		LastFailure:    p.LastFailure,
		FailureReasons: p.FailureReasons,
	}, nil
}

func unmarshalOutcome(data []byte) (*OutcomeMetric, error) {
	var p persistOutcome
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	if p.Outcomes == nil {
		p.Outcomes = make(map[string]int64)
	}
	return &OutcomeMetric{
		Outcomes:    p.Outcomes,
		LastOutcome: p.LastOutcome,
		LastTime:    p.LastTime,
		Total:       p.Total,
	}, nil
}

func unmarshalError(data []byte) (*ErrorMetric, error) {
	var p persistError
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	if p.ErrorsByType == nil {
		p.ErrorsByType = make(map[string]int64)
	}
	return &ErrorMetric{
		ErrorsByType:  p.ErrorsByType,
		TotalErrors:   p.TotalErrors,
		LastError:     p.LastError,
		LastErrorType: p.LastErrorType,
		LastErrorTime: p.LastErrorTime,
	}, nil
}

func unmarshalCondition(data []byte) (*ConditionMetric, error) {
	var p persistCondition
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	return &ConditionMetric{
		CurrentValue: p.CurrentValue,
		TrueCount:    p.TrueCount,
		FalseCount:   p.FalseCount,
		LastChange:   p.LastChange,
		LastChecked:  p.LastChecked,
	}, nil
}

func unmarshalThreshold(data []byte) (*ThresholdMetric, error) {
	var p persistThreshold
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	return &ThresholdMetric{
		Value:        p.Value,
		Threshold:    p.Threshold,
		ExceedCount:  p.ExceedCount,
		TotalChecks:  p.TotalChecks,
		LastExceeded: p.LastExceeded,
		LastChecked:  p.LastChecked,
		IsExceeded:   p.IsExceeded,
	}, nil
}

func unmarshalCost(data []byte) (*CostMetric, error) {
	var p persistCost
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	return &CostMetric{
		Total: p.Total,
		Last:  p.Last,
		Min:   p.Min,
		Max:   p.Max,
		Count: p.Count,
	}, nil
}

