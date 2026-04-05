package a2a

import (
	"context"
	"fmt"
	"strings"
	"time"

	a2aproto "github.com/a2aproject/a2a-go/v2/a2a"
	libp2ptransport "github.com/roelfdiedericks/goclaw/internal/a2a/transports/libp2p"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

func (m *Manager) StartInboundTask(ctx context.Context, peerID, taskID, input string) (<-chan TaskSnapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pruneExpiredTasksLocked(time.Now())

	if m.adapter == nil {
		L_warn("a2a: inbound task rejected", "peerID", peerID, "taskID", taskID, "error", "A2A executor not configured")
		return nil, fmt.Errorf("A2A executor not configured")
	}
	trusted, localUser, err := m.resolveTrustedPeer(peerID)
	if err != nil {
		L_warn("a2a: inbound task rejected", "peerID", peerID, "taskID", taskID, "error", err)
		return nil, err
	}

	key := taskKey(peerID, taskID)
	if existing := m.tasks[key]; existing != nil {
		L_warn("a2a: inbound task rejected", "peerID", peerID, "taskID", taskID, "error", "task already exists")
		return nil, fmt.Errorf("task %s already exists for peer %s", taskID, peerID)
	}

	taskCtx, cancel := context.WithCancel(ctx)
	sessionKey := SessionKeyForTask(TransportIDLibp2p, peerID, taskID)
	contextID := a2aproto.NewContextID()
	watcher := make(chan TaskSnapshot, 16)
	rt := &taskRuntime{
		Key:        key,
		TaskID:     taskID,
		RemotePeer: peerID,
		LocalUser:  trusted.LocalUser,
		SessionKey: sessionKey,
		ContextID:  contextID,
		ArtifactID: a2aproto.NewArtifactID(),
		Direction:  TaskDirectionInbound,
		State:      TaskStateSubmitted,
		Cancel:     cancel,
		Watchers:   []chan TaskSnapshot{watcher},
		Snapshot:   newSubmittedSnapshot(peerID, sessionKey, taskID, contextID, input),
	}
	m.tasks[key] = rt
	m.broadcastLocked(rt, rt.Snapshot)
	L_info("a2a: inbound task accepted", "peerID", peerID, "taskID", taskID, "localUser", trusted.LocalUser, "contextID", contextID, "inputLength", len(input))

	go m.runInboundTask(taskCtx, rt, localUser.ID, strings.TrimSpace(input))
	return watcher, nil
}

func (m *Manager) runInboundTask(ctx context.Context, rt *taskRuntime, localUserID, input string) {
	L_info("a2a: inbound task started", "peerID", rt.RemotePeer, "taskID", rt.TaskID, "localUser", localUserID, "contextID", rt.ContextID)
	emit := func(event a2aproto.Event) {
		m.mu.Lock()
		defer m.mu.Unlock()
		current := m.tasks[rt.Key]
		if current == nil {
			return
		}
		next := applyEventToSnapshot(current.Snapshot, event)
		current.State = next.State
		L_trace("a2a: inbound task event applied", "peerID", current.RemotePeer, "taskID", current.TaskID, "state", next.State, "contextID", next.ContextID)
		m.broadcastLocked(current, next)
	}

	err := m.adapter.Execute(ctx, ExecutionRequest{
		TaskID:      a2aproto.TaskID(rt.TaskID),
		ContextID:   rt.ContextID,
		ArtifactID:  rt.ArtifactID,
		TransportID: TransportIDLibp2p,
		RemotePeer:  rt.RemotePeer,
		LocalUser:   localUserID,
		SessionKey:  rt.SessionKey,
		Message:     newInboundUserMessage(rt.TaskID, rt.ContextID, input),
	}, emit)

	m.mu.Lock()
	defer m.mu.Unlock()
	current := m.tasks[rt.Key]
	if current == nil {
		return
	}
	current.Snapshot.UpdatedAt = time.Now()
	switch {
	case current.Snapshot.State == TaskStateCompleted || current.Snapshot.State == TaskStateFailed || current.Snapshot.State == TaskStateCancelled:
		current.State = current.Snapshot.State
	case err == nil:
		current.State = TaskStateCompleted
		current.Snapshot.State = TaskStateCompleted
	case ctx.Err() != nil:
		current.State = TaskStateCancelled
		current.Snapshot.State = TaskStateCancelled
		current.Snapshot.Error = "task cancelled"
	default:
		current.State = TaskStateFailed
		current.Snapshot.State = TaskStateFailed
		current.Snapshot.Error = err.Error()
	}
	switch current.State {
	case TaskStateFailed:
		L_warn("a2a: inbound task finished", "peerID", current.RemotePeer, "taskID", current.TaskID, "state", current.State, "error", current.Snapshot.Error)
	default:
		L_info("a2a: inbound task finished", "peerID", current.RemotePeer, "taskID", current.TaskID, "state", current.State, "contextID", current.Snapshot.ContextID)
	}
	m.broadcastLocked(current, current.Snapshot)
}

func (m *Manager) ResumeTask(peerID, taskID string) (<-chan TaskSnapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pruneExpiredTasksLocked(time.Now())

	key := taskKey(peerID, taskID)
	rt := m.tasks[key]
	if rt == nil {
		if m.isExpiredTaskLocked(key, time.Now()) {
			L_warn("a2a: task resume rejected", "peerID", peerID, "taskID", taskID, "error", "retained state expired")
			return nil, fmt.Errorf("task %s can no longer be resumed: retained state expired", taskID)
		}
		L_warn("a2a: task resume rejected", "peerID", peerID, "taskID", taskID, "error", "task not resumable")
		return nil, fmt.Errorf("task %s can no longer be resumed", taskID)
	}
	L_info("a2a: task resume subscribed", "peerID", peerID, "taskID", taskID, "state", rt.State)
	return m.subscribeTaskLocked(rt, true), nil
}

func (m *Manager) CancelTask(peerID, taskID string) error {
	m.mu.RLock()
	rt := m.tasks[taskKey(peerID, taskID)]
	m.mu.RUnlock()
	if rt == nil {
		L_warn("a2a: task cancel rejected", "peerID", peerID, "taskID", taskID, "error", "task not found")
		return fmt.Errorf("task %s not found", taskID)
	}
	L_info("a2a: task cancel requested", "peerID", peerID, "taskID", taskID, "direction", rt.Direction)
	rt.Cancel()
	return nil
}

func (m *Manager) broadcastLocked(rt *taskRuntime, snapshot TaskSnapshot) {
	rt.Snapshot = snapshot
	L_trace("a2a: broadcasting task snapshot", "peerID", rt.RemotePeer, "taskID", rt.TaskID, "state", snapshot.State, "watchers", len(rt.Watchers), "direction", rt.Direction)
	active := rt.Watchers[:0]
	for _, watcher := range rt.Watchers {
		if watcher == nil {
			continue
		}
		select {
		case watcher <- snapshot:
			if isTerminalTaskState(snapshot.State) {
				close(watcher)
				continue
			}
			active = append(active, watcher)
		default:
			active = append(active, watcher)
		}
	}
	rt.Watchers = active
}

func (m *Manager) subscribeTaskLocked(rt *taskRuntime, includeInitial bool) chan TaskSnapshot {
	ch := make(chan TaskSnapshot, 16)
	if includeInitial {
		ch <- rt.Snapshot
	}
	if isTerminalTaskState(rt.State) {
		close(ch)
		return ch
	}
	rt.Watchers = append(rt.Watchers, ch)
	L_trace("a2a: watcher subscribed", "peerID", rt.RemotePeer, "taskID", rt.TaskID, "watchers", len(rt.Watchers))
	return ch
}

func (m *Manager) closeWatchersLocked(rt *taskRuntime) {
	L_trace("a2a: closing task watchers", "peerID", rt.RemotePeer, "taskID", rt.TaskID, "watchers", len(rt.Watchers))
	for _, watcher := range rt.Watchers {
		if watcher == nil {
			continue
		}
		close(watcher)
	}
	rt.Watchers = nil
}

func (m *Manager) removeWatcherLocked(rt *taskRuntime, target chan TaskSnapshot) {
	if rt == nil || target == nil {
		return
	}
	next := rt.Watchers[:0]
	for _, watcher := range rt.Watchers {
		if watcher == nil || watcher == target {
			continue
		}
		next = append(next, watcher)
	}
	rt.Watchers = next
	L_trace("a2a: watcher removed", "peerID", rt.RemotePeer, "taskID", rt.TaskID, "watchers", len(rt.Watchers))
}

func (m *Manager) retentionDuration() time.Duration {
	if m.cfg.Libp2p.Protocol.StateRetentionSecs <= 0 {
		return 0
	}
	return time.Duration(m.cfg.Libp2p.Protocol.StateRetentionSecs) * time.Second
}

func (m *Manager) pruneExpiredTasksLocked(now time.Time) {
	retention := m.retentionDuration()
	if retention <= 0 {
		return
	}
	for key, expiredAt := range m.expiredTasks {
		if now.Sub(expiredAt) > retention {
			delete(m.expiredTasks, key)
		}
	}
	cutoff := now.Add(-retention)
	for key, rt := range m.tasks {
		if !taskRetentionCandidate(rt.State) {
			continue
		}
		if rt.Snapshot.UpdatedAt.IsZero() || rt.Snapshot.UpdatedAt.After(cutoff) {
			continue
		}
		m.closeWatchersLocked(rt)
		delete(m.tasks, key)
		m.expiredTasks[key] = now
	}
}

func (m *Manager) isExpiredTaskLocked(key string, now time.Time) bool {
	expiredAt, ok := m.expiredTasks[key]
	if !ok {
		return false
	}
	retention := m.retentionDuration()
	if retention > 0 && now.Sub(expiredAt) > retention {
		delete(m.expiredTasks, key)
		return false
	}
	return true
}

func taskRetentionCandidate(state TaskState) bool {
	return isTerminalTaskState(state) || state == TaskStateInterrupted
}

func (m *Manager) startInboundTaskFromTransport(ctx context.Context, peerID, taskID, input string) (<-chan libp2ptransport.TaskUpdate, error) {
	updates, err := m.StartInboundTask(ctx, peerID, taskID, input)
	if err != nil {
		return nil, err
	}
	out := make(chan libp2ptransport.TaskUpdate, 16)
	go func() {
		defer close(out)
		for snapshot := range updates {
			out <- transportUpdateFromSnapshot(snapshot)
		}
	}()
	return out, nil
}

func (m *Manager) resumeTaskFromTransport(peerID, taskID string) (<-chan libp2ptransport.TaskUpdate, error) {
	updates, err := m.ResumeTask(peerID, taskID)
	if err != nil {
		return nil, err
	}
	out := make(chan libp2ptransport.TaskUpdate, 16)
	go func() {
		defer close(out)
		for snapshot := range updates {
			out <- transportUpdateFromSnapshot(snapshot)
		}
	}()
	return out, nil
}

func transportUpdateFromSnapshot(snapshot TaskSnapshot) libp2ptransport.TaskUpdate {
	return libp2ptransport.TaskUpdate{
		TaskID:    snapshot.TaskID,
		ContextID: snapshot.ContextID,
		State:     string(snapshot.State),
		Content:   snapshot.Content,
		Error:     snapshot.Error,
		UpdatedAt: snapshot.UpdatedAt,
	}
}

func snapshotFromTransportUpdate(update libp2ptransport.TaskUpdate, peerID, sessionKey string) TaskSnapshot {
	return TaskSnapshot{
		TaskID:     update.TaskID,
		PeerID:     peerID,
		SessionKey: sessionKey,
		ContextID:  update.ContextID,
		State:      TaskState(update.State),
		Content:    update.Content,
		Error:      update.Error,
		UpdatedAt:  update.UpdatedAt,
	}
}

func (m *Manager) SubmitRemoteTask(ctx context.Context, target, input string) (string, <-chan TaskSnapshot, error) {
	m.mu.RLock()
	rt := m.runtime
	m.mu.RUnlock()
	if rt == nil {
		L_warn("a2a: outbound task submit rejected", "peerID", target, "error", "runtime not started")
		return "", nil, fmt.Errorf("A2A runtime not started")
	}
	taskID := "remote-" + strings.ReplaceAll(time.Now().UTC().Format("20060102T150405.000000000"), ".", "")
	sessionKey := SessionKeyForTask(TransportIDLibp2p, target, taskID)

	m.mu.Lock()
	m.pruneExpiredTasksLocked(time.Now())
	key := taskKey(target, taskID)
	if existing := m.tasks[key]; existing != nil {
		m.mu.Unlock()
		L_warn("a2a: outbound task submit rejected", "peerID", target, "taskID", taskID, "error", "task already exists")
		return "", nil, fmt.Errorf("task %s already exists for peer %s", taskID, target)
	}
	taskRuntime := &taskRuntime{
		Key:        key,
		TaskID:     taskID,
		RemotePeer: target,
		SessionKey: sessionKey,
		Direction:  TaskDirectionOutbound,
		State:      TaskStateSubmitted,
		Snapshot: TaskSnapshot{
			TaskID:     taskID,
			PeerID:     target,
			SessionKey: sessionKey,
			State:      TaskStateSubmitted,
			UpdatedAt:  time.Now(),
		},
	}
	m.tasks[key] = taskRuntime
	watcher := m.subscribeTaskLocked(taskRuntime, true)
	m.mu.Unlock()
	L_info("a2a: outbound task submitted", "peerID", target, "taskID", taskID, "sessionKey", sessionKey, "inputLength", len(input))

	updates, err := rt.SubmitRemoteTask(ctx, target, taskID, input, m.transportPeerCandidates())
	if err != nil {
		m.mu.Lock()
		close(watcher)
		delete(m.tasks, key)
		m.mu.Unlock()
		L_warn("a2a: outbound task submit failed", "peerID", target, "taskID", taskID, "error", err)
		return "", nil, err
	}
	go m.trackRemoteTaskUpdates(key, target, sessionKey, updates)
	return taskID, watcher, nil
}

func (m *Manager) ResumeRemoteTask(ctx context.Context, target, taskID string) (<-chan TaskSnapshot, error) {
	m.mu.RLock()
	rt := m.runtime
	m.mu.RUnlock()
	if rt == nil {
		L_warn("a2a: outbound task resume rejected", "peerID", target, "taskID", taskID, "error", "runtime not started")
		return nil, fmt.Errorf("A2A runtime not started")
	}
	sessionKey := SessionKeyForTask(TransportIDLibp2p, target, taskID)
	key := taskKey(target, taskID)

	m.mu.Lock()
	m.pruneExpiredTasksLocked(time.Now())
	current := m.tasks[key]
	if current == nil {
		current = &taskRuntime{
			Key:        key,
			TaskID:     taskID,
			RemotePeer: target,
			SessionKey: sessionKey,
			Direction:  TaskDirectionOutbound,
			State:      TaskStateRunning,
			Snapshot: TaskSnapshot{
				TaskID:     taskID,
				PeerID:     target,
				SessionKey: sessionKey,
				State:      TaskStateRunning,
				UpdatedAt:  time.Now(),
			},
		}
		m.tasks[key] = current
	}
	watcher := m.subscribeTaskLocked(current, current.Snapshot.UpdatedAt.After(time.Time{}))
	m.mu.Unlock()

	if isTerminalTaskState(current.State) {
		L_info("a2a: outbound task resume reused terminal snapshot", "peerID", target, "taskID", taskID, "state", current.State)
		return watcher, nil
	}
	L_info("a2a: outbound task resume requested", "peerID", target, "taskID", taskID, "sessionKey", sessionKey)
	updates, err := rt.ResumeRemoteTask(ctx, target, taskID, m.transportPeerCandidates())
	if err != nil {
		m.mu.Lock()
		current := m.tasks[key]
		if current != nil {
			m.removeWatcherLocked(current, watcher)
			current.State = TaskStateFailed
			current.Snapshot.State = TaskStateFailed
			current.Snapshot.Error = err.Error()
			current.Snapshot.UpdatedAt = time.Now()
			m.broadcastLocked(current, current.Snapshot)
		}
		m.mu.Unlock()
		L_warn("a2a: outbound task resume failed", "peerID", target, "taskID", taskID, "error", err)
		return nil, err
	}
	go m.trackRemoteTaskUpdates(key, target, sessionKey, updates)
	return watcher, nil
}

func (m *Manager) CancelRemoteTask(ctx context.Context, target, taskID string) (TaskSnapshot, error) {
	m.mu.RLock()
	rt := m.runtime
	m.mu.RUnlock()
	if rt == nil {
		L_warn("a2a: outbound task cancel rejected", "peerID", target, "taskID", taskID, "error", "runtime not started")
		return TaskSnapshot{}, fmt.Errorf("A2A runtime not started")
	}
	L_info("a2a: outbound task cancel requested", "peerID", target, "taskID", taskID)
	update, err := rt.CancelRemoteTask(ctx, target, taskID, m.transportPeerCandidates())
	if err != nil {
		L_warn("a2a: outbound task cancel failed", "peerID", target, "taskID", taskID, "error", err)
		return TaskSnapshot{}, err
	}
	snapshot := snapshotFromTransportUpdate(update, target, SessionKeyForTask(TransportIDLibp2p, target, taskID))
	key := taskKey(target, taskID)
	m.mu.Lock()
	m.pruneExpiredTasksLocked(time.Now())
	current := m.tasks[key]
	if current == nil {
		current = &taskRuntime{
			Key:        key,
			TaskID:     taskID,
			RemotePeer: target,
			SessionKey: snapshot.SessionKey,
			Direction:  TaskDirectionOutbound,
		}
		m.tasks[key] = current
	}
	current.State = snapshot.State
	current.Snapshot = snapshot
	m.broadcastLocked(current, snapshot)
	m.mu.Unlock()
	L_info("a2a: outbound task cancelled", "peerID", target, "taskID", taskID, "state", snapshot.State)
	return snapshot, nil
}

func (m *Manager) trackRemoteTaskUpdates(key, peerID, sessionKey string, updates <-chan libp2ptransport.TaskUpdate) {
	for update := range updates {
		snapshot := snapshotFromTransportUpdate(update, peerID, sessionKey)
		L_trace("a2a: remote task update received", "peerID", peerID, "taskID", update.TaskID, "state", update.State, "contextID", update.ContextID)
		m.mu.Lock()
		current := m.tasks[key]
		if current == nil {
			m.mu.Unlock()
			continue
		}
		if snapshot.ContextID == "" {
			snapshot.ContextID = current.ContextID
		}
		current.ContextID = snapshot.ContextID
		current.State = snapshot.State
		m.broadcastLocked(current, snapshot)
		m.mu.Unlock()
		switch snapshot.State {
		case TaskStateFailed:
			L_warn("a2a: remote task state updated", "peerID", peerID, "taskID", update.TaskID, "state", snapshot.State, "error", snapshot.Error)
		case TaskStateCompleted, TaskStateCancelled, TaskStateInterrupted:
			L_info("a2a: remote task state updated", "peerID", peerID, "taskID", update.TaskID, "state", snapshot.State, "contextID", snapshot.ContextID)
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	current := m.tasks[key]
	if current == nil {
		return
	}
	if current.State == TaskStateInterrupted {
		L_info("a2a: remote task stream interrupted", "peerID", peerID, "taskID", current.TaskID, "contextID", current.ContextID)
		m.closeWatchersLocked(current)
	}
	m.pruneExpiredTasksLocked(time.Now())
}
