package delegatedrun

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

var ErrRunNotFound = errors.New("delegated run not found")

// Registry is an in-memory delegated run state index.
// SQLite-backed persistence can be layered under this API later.
type Registry interface {
	Create(record RunRecord) error
	UpdateState(runID string, state RunState) error
	Complete(runID string, result RunResult, state RunState) error
	MarkCompletionDispatched(runID string, dispatchKey string) error
	ClaimCompletionDispatch(runID, claimToken string, seq int) (bool, error)
	ReleaseCompletionDispatch(runID, claimToken string) error
	AdvanceCompletionDispatchSeq(runID string) (int, error)
	RecordDispatchPhase(runID, phase, status, detail string) error
	UpdateCompletionLifecycle(runID string, update CompletionLifecycleUpdate) error
	UpdateRequesterBinding(runID string, update RequesterBindingUpdate) error
	Get(runID string) (RunRecord, bool)
	List() []RunRecord
}

type MemoryRegistry struct {
	mu   sync.RWMutex
	runs map[string]RunRecord
}

func NewMemoryRegistry() *MemoryRegistry {
	return &MemoryRegistry{
		runs: make(map[string]RunRecord),
	}
}

func (r *MemoryRegistry) Create(record RunRecord) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.runs[record.RunID] = record
	return nil
}

func (r *MemoryRegistry) UpdateState(runID string, state RunState) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok := r.runs[runID]
	if !ok {
		return ErrRunNotFound
	}
	rec.State = state
	r.runs[runID] = rec
	return nil
}

func (r *MemoryRegistry) Complete(runID string, result RunResult, state RunState) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok := r.runs[runID]
	if !ok {
		return ErrRunNotFound
	}
	now := time.Now()
	rec.State = state
	rec.Result = result
	rec.FinishedAt = &now
	r.runs[runID] = rec
	return nil
}

func (r *MemoryRegistry) MarkCompletionDispatched(runID string, dispatchKey string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok := r.runs[runID]
	if !ok {
		return ErrRunNotFound
	}
	rec.CompletionDispatchKey = dispatchKey
	if seq, err := dispatchSeqFromKey(runID, dispatchKey); err == nil {
		rec.CompletionDispatchSeq = seq
	}
	rec.CompletionClaimToken = ""
	rec.CompletionClaimSeq = 0
	rec.CleanupState = "dispatched"
	r.runs[runID] = rec
	return nil
}

func (r *MemoryRegistry) ClaimCompletionDispatch(runID, claimToken string, seq int) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok := r.runs[runID]
	if !ok {
		return false, ErrRunNotFound
	}
	if dispatchSeq, err := dispatchSeqFromKey(runID, rec.CompletionDispatchKey); err == nil && dispatchSeq == seq {
		return false, nil
	}
	if rec.CompletionClaimToken != "" && rec.CompletionClaimSeq == seq && rec.CompletionClaimToken != claimToken {
		return false, nil
	}
	rec.CompletionClaimToken = claimToken
	rec.CompletionClaimSeq = seq
	r.runs[runID] = rec
	return true, nil
}

func (r *MemoryRegistry) ReleaseCompletionDispatch(runID, claimToken string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok := r.runs[runID]
	if !ok {
		return ErrRunNotFound
	}
	if rec.CompletionClaimToken == claimToken {
		rec.CompletionClaimToken = ""
		rec.CompletionClaimSeq = 0
		r.runs[runID] = rec
	}
	return nil
}

func (r *MemoryRegistry) AdvanceCompletionDispatchSeq(runID string) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok := r.runs[runID]
	if !ok {
		return 0, ErrRunNotFound
	}
	if rec.CompletionDispatchSeq <= 0 {
		rec.CompletionDispatchSeq = 2
	} else {
		rec.CompletionDispatchSeq++
	}
	r.runs[runID] = rec
	return rec.CompletionDispatchSeq, nil
}

func (r *MemoryRegistry) RecordDispatchPhase(runID, phase, status, detail string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok := r.runs[runID]
	if !ok {
		return ErrRunNotFound
	}
	rec.DispatchPhases = append(rec.DispatchPhases, CompletionDispatchPhase{
		Phase:     phase,
		Path:      DispatchPathNone,
		Delivered: status == "success",
		Error:     detail,
	})
	r.runs[runID] = rec
	return nil
}

func (r *MemoryRegistry) UpdateCompletionLifecycle(runID string, update CompletionLifecycleUpdate) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok := r.runs[runID]
	if !ok {
		return ErrRunNotFound
	}
	rec.CleanupState = update.CleanupState
	rec.DeferredReason = update.DeferredReason
	rec.ContinuationState = update.ContinuationState
	rec.ContinuationReason = update.ContinuationReason
	rec.ContinuationWakeAt = update.ContinuationWakeAt
	r.runs[runID] = rec
	return nil
}

func (r *MemoryRegistry) UpdateRequesterBinding(runID string, update RequesterBindingUpdate) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok := r.runs[runID]
	if !ok {
		return ErrRunNotFound
	}
	if strings.TrimSpace(update.State) != "" {
		rec.RequesterBindingState = NormalizeRequesterBindingState(update.State)
	}
	rec.RequesterBindingReason = strings.TrimSpace(update.Reason)
	if update.UpdatedAt != nil {
		rec.RequesterBindingUpdatedAt = update.UpdatedAt
	}
	if update.LastActiveAt != nil {
		rec.RequesterBindingLastActiveAt = update.LastActiveAt
	}
	r.runs[runID] = rec
	return nil
}

func (r *MemoryRegistry) Get(runID string) (RunRecord, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	rec, ok := r.runs[runID]
	return rec, ok
}

func (r *MemoryRegistry) List() []RunRecord {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]RunRecord, 0, len(r.runs))
	for _, rec := range r.runs {
		out = append(out, rec)
	}
	return out
}

// CompositeRegistry writes updates to multiple registries while reading from
// the first (primary) registry.
type CompositeRegistry struct {
	primary Registry
	mirrors []Registry
}

func NewCompositeRegistry(primary Registry, mirrors ...Registry) *CompositeRegistry {
	return &CompositeRegistry{primary: primary, mirrors: mirrors}
}

func (r *CompositeRegistry) Create(record RunRecord) error {
	if r.primary != nil {
		_ = r.primary.Create(record)
	}
	for _, m := range r.mirrors {
		_ = m.Create(record)
	}
	return nil
}

func (r *CompositeRegistry) UpdateState(runID string, state RunState) error {
	if r.primary != nil {
		_ = r.primary.UpdateState(runID, state)
	}
	for _, m := range r.mirrors {
		_ = m.UpdateState(runID, state)
	}
	return nil
}

func (r *CompositeRegistry) Complete(runID string, result RunResult, state RunState) error {
	if r.primary != nil {
		_ = r.primary.Complete(runID, result, state)
	}
	for _, m := range r.mirrors {
		_ = m.Complete(runID, result, state)
	}
	return nil
}

func (r *CompositeRegistry) MarkCompletionDispatched(runID string, dispatchKey string) error {
	if r.primary != nil {
		_ = r.primary.MarkCompletionDispatched(runID, dispatchKey)
	}
	for _, m := range r.mirrors {
		_ = m.MarkCompletionDispatched(runID, dispatchKey)
	}
	return nil
}

func (r *CompositeRegistry) ClaimCompletionDispatch(runID, claimToken string, seq int) (bool, error) {
	claimed := true
	var claimErr error
	if r.primary != nil {
		claimed, claimErr = r.primary.ClaimCompletionDispatch(runID, claimToken, seq)
	}
	if !claimed || claimErr != nil {
		return claimed, claimErr
	}
	for _, m := range r.mirrors {
		_, _ = m.ClaimCompletionDispatch(runID, claimToken, seq)
	}
	return true, nil
}

func (r *CompositeRegistry) ReleaseCompletionDispatch(runID, claimToken string) error {
	if r.primary != nil {
		_ = r.primary.ReleaseCompletionDispatch(runID, claimToken)
	}
	for _, m := range r.mirrors {
		_ = m.ReleaseCompletionDispatch(runID, claimToken)
	}
	return nil
}

func (r *CompositeRegistry) AdvanceCompletionDispatchSeq(runID string) (int, error) {
	seq := 0
	var seqErr error
	if r.primary != nil {
		seq, seqErr = r.primary.AdvanceCompletionDispatchSeq(runID)
	}
	for _, m := range r.mirrors {
		_, _ = m.AdvanceCompletionDispatchSeq(runID)
	}
	return seq, seqErr
}

func (r *CompositeRegistry) RecordDispatchPhase(runID, phase, status, detail string) error {
	if r.primary != nil {
		_ = r.primary.RecordDispatchPhase(runID, phase, status, detail)
	}
	for _, m := range r.mirrors {
		_ = m.RecordDispatchPhase(runID, phase, status, detail)
	}
	return nil
}

func (r *CompositeRegistry) UpdateCompletionLifecycle(runID string, update CompletionLifecycleUpdate) error {
	if r.primary != nil {
		_ = r.primary.UpdateCompletionLifecycle(runID, update)
	}
	for _, m := range r.mirrors {
		_ = m.UpdateCompletionLifecycle(runID, update)
	}
	return nil
}

func (r *CompositeRegistry) UpdateRequesterBinding(runID string, update RequesterBindingUpdate) error {
	if r.primary != nil {
		_ = r.primary.UpdateRequesterBinding(runID, update)
	}
	for _, m := range r.mirrors {
		_ = m.UpdateRequesterBinding(runID, update)
	}
	return nil
}

func (r *CompositeRegistry) Get(runID string) (RunRecord, bool) {
	if r.primary == nil {
		return RunRecord{}, false
	}
	return r.primary.Get(runID)
}

func (r *CompositeRegistry) List() []RunRecord {
	if r.primary == nil {
		return nil
	}
	return r.primary.List()
}

func dispatchSeqFromKey(runID, key string) (int, error) {
	prefix := runID + ":"
	if !strings.HasPrefix(key, prefix) {
		return 0, fmt.Errorf("dispatch key %q does not match run %s", key, runID)
	}
	raw := strings.TrimPrefix(key, prefix)
	seq, err := strconv.Atoi(raw)
	if err != nil || seq <= 0 {
		return 0, fmt.Errorf("invalid dispatch sequence in key %q", key)
	}
	return seq, nil
}

