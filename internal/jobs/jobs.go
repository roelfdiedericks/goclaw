package jobs

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/roelfdiedericks/goclaw/internal/bus"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

type State string

const (
	StateRunning   State = "running"
	StateCompleted State = "completed"
	StateFailed    State = "failed"
	StateCanceled  State = "canceled"
)

const DefaultPollAfterMs = 1000

var ErrJobNotFound = errors.New("job not found")

type Status struct {
	JobID           string         `json:"jobID"`
	OwnerComponent  string         `json:"ownerComponent"`
	OwnerAction     string         `json:"ownerAction"`
	State           State          `json:"state"`
	Phase           string         `json:"phase,omitempty"`
	Message         string         `json:"message,omitempty"`
	ProgressPercent int            `json:"progressPercent,omitempty"`
	StartedAt       time.Time      `json:"startedAt"`
	UpdatedAt       time.Time      `json:"updatedAt"`
	FinishedAt      time.Time      `json:"finishedAt,omitempty"`
	PollAfterMs     int            `json:"pollAfterMs,omitempty"`
	Error           string         `json:"error,omitempty"`
	Result          interface{}    `json:"result,omitempty"`
	Cancelable      bool           `json:"cancelable"`
	Metadata        map[string]any `json:"metadata,omitempty"`
}

type StartSpec struct {
	OwnerComponent string
	OwnerAction    string
	InitialPhase   string
	InitialMessage string
	PollAfterMs    int
	Cancelable     bool
	Metadata       map[string]any
}

type Worker func(context.Context, *Reporter) (interface{}, error)

type Manager struct {
	mu   sync.RWMutex
	jobs map[string]*jobEntry
}

type jobEntry struct {
	cancel context.CancelFunc
	status Status
}

type Reporter struct {
	jobID   string
	manager *Manager
}

var (
	globalManager     = &Manager{jobs: make(map[string]*jobEntry)}
	globalManagerOnce sync.Once
)

func GetManager() *Manager {
	globalManagerOnce.Do(func() {
		globalManager = &Manager{jobs: make(map[string]*jobEntry)}
	})
	return globalManager
}

func (m *Manager) Start(spec StartSpec, worker Worker) Status {
	if spec.PollAfterMs <= 0 {
		spec.PollAfterMs = DefaultPollAfterMs
	}
	if spec.InitialPhase == "" {
		spec.InitialPhase = "starting"
	}
	now := time.Now()
	jobID := uuid.NewString()
	ctx, cancel := context.WithCancel(context.Background())
	status := Status{
		JobID:           jobID,
		OwnerComponent:  spec.OwnerComponent,
		OwnerAction:     spec.OwnerAction,
		State:           StateRunning,
		Phase:           spec.InitialPhase,
		Message:         spec.InitialMessage,
		StartedAt:       now,
		UpdatedAt:       now,
		PollAfterMs:     spec.PollAfterMs,
		Cancelable:      spec.Cancelable,
		Metadata:        cloneMetadata(spec.Metadata),
		ProgressPercent: 0,
	}

	m.mu.Lock()
	m.jobs[jobID] = &jobEntry{
		cancel: cancel,
		status: status,
	}
	m.mu.Unlock()

	publish("jobs.started", status)

	go func() {
		reporter := &Reporter{jobID: jobID, manager: m}
		result, err := worker(ctx, reporter)
		switch {
		case errors.Is(err, context.Canceled):
			m.finish(jobID, StateCanceled, "canceled", "Canceled", "", result)
		case err != nil:
			m.finish(jobID, StateFailed, "failed", err.Error(), err.Error(), result)
		default:
			m.finish(jobID, StateCompleted, "completed", "Completed", "", result)
		}
	}()

	L_info("jobs: started", "jobID", jobID, "component", spec.OwnerComponent, "action", spec.OwnerAction)
	return status
}

func (m *Manager) Status(jobID string) (Status, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	entry, ok := m.jobs[jobID]
	if !ok {
		return Status{}, false
	}
	return cloneStatus(entry.status), true
}

func (m *Manager) List(ownerComponent string) []Status {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Status, 0, len(m.jobs))
	for _, entry := range m.jobs {
		if ownerComponent != "" && entry.status.OwnerComponent != ownerComponent {
			continue
		}
		out = append(out, cloneStatus(entry.status))
	}
	sortStatuses(out)
	return out
}

func (m *Manager) Cancel(jobID string) (Status, error) {
	m.mu.RLock()
	entry, ok := m.jobs[jobID]
	m.mu.RUnlock()
	if !ok {
		return Status{}, ErrJobNotFound
	}
	if entry.cancel == nil || !entry.status.Cancelable || entry.status.State != StateRunning {
		return cloneStatus(entry.status), nil
	}
	entry.cancel()
	status, _ := m.Status(jobID)
	return status, nil
}

func (m *Manager) finish(jobID string, state State, phase, message, errorText string, result interface{}) {
	m.mu.Lock()
	entry, ok := m.jobs[jobID]
	if !ok {
		m.mu.Unlock()
		return
	}
	entry.status.State = state
	entry.status.Phase = phase
	entry.status.Message = message
	entry.status.Error = errorText
	entry.status.Result = result
	entry.status.Cancelable = false
	entry.status.PollAfterMs = 0
	entry.status.FinishedAt = time.Now()
	entry.status.UpdatedAt = entry.status.FinishedAt
	if state == StateCompleted && entry.status.ProgressPercent < 100 {
		entry.status.ProgressPercent = 100
	}
	status := cloneStatus(entry.status)
	m.mu.Unlock()

	switch state {
	case StateCompleted:
		publish("jobs.completed", status)
	case StateFailed:
		publish("jobs.failed", status)
	case StateCanceled:
		publish("jobs.canceled", status)
	}
	L_info("jobs: finished", "jobID", jobID, "state", state, "component", status.OwnerComponent, "action", status.OwnerAction)
}

func (m *Manager) update(jobID, phase, message string, progressPercent int, metadata map[string]any) {
	m.mu.Lock()
	entry, ok := m.jobs[jobID]
	if !ok {
		m.mu.Unlock()
		return
	}
	if phase != "" {
		entry.status.Phase = phase
	}
	if message != "" {
		entry.status.Message = message
	}
	if progressPercent >= 0 {
		entry.status.ProgressPercent = progressPercent
	}
	if metadata != nil {
		if entry.status.Metadata == nil {
			entry.status.Metadata = map[string]any{}
		}
		for k, v := range metadata {
			entry.status.Metadata[k] = v
		}
	}
	entry.status.UpdatedAt = time.Now()
	status := cloneStatus(entry.status)
	m.mu.Unlock()

	publish("jobs.progress", status)
}

func (r *Reporter) Update(phase, message string, progressPercent int) {
	if r == nil || r.manager == nil {
		return
	}
	r.manager.update(r.jobID, phase, message, progressPercent, nil)
}

func (r *Reporter) Metadata(values map[string]any) {
	if r == nil || r.manager == nil || values == nil {
		return
	}
	r.manager.update(r.jobID, "", "", -1, values)
}

func cloneStatus(in Status) Status {
	out := in
	out.Metadata = cloneMetadata(in.Metadata)
	return out
}

func cloneMetadata(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func sortStatuses(items []Status) {
	for i := 0; i < len(items); i++ {
		for j := i + 1; j < len(items); j++ {
			if items[j].StartedAt.After(items[i].StartedAt) {
				items[i], items[j] = items[j], items[i]
			}
		}
	}
}

func publish(topic string, status Status) {
	bus.PublishEvent(topic, status)
}
