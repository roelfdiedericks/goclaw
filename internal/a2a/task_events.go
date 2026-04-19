package a2a

import (
	"strings"
	"time"

	a2aproto "github.com/a2aproject/a2a-go/v2/a2a"
)

func newTaskInfo(taskID, contextID string) a2aproto.TaskInfo {
	return a2aproto.TaskInfo{
		TaskID:    a2aproto.TaskID(taskID),
		ContextID: contextID,
	}
}

func newInboundUserMessage(taskID, contextID, input string) *a2aproto.Message {
	msg := a2aproto.NewMessageForTask(
		a2aproto.MessageRoleUser,
		newTaskInfo(taskID, contextID),
		a2aproto.NewTextPart(input),
	)
	return msg
}

func newSubmittedSnapshot(peerID, sessionKey, taskID, contextID, input string) TaskSnapshot {
	task := a2aproto.NewSubmittedTask(
		newTaskInfo(taskID, contextID),
		newInboundUserMessage(taskID, contextID, input),
	)
	return snapshotFromA2ATask(task, peerID, sessionKey)
}

func snapshotFromA2ATask(task *a2aproto.Task, peerID, sessionKey string) TaskSnapshot {
	snapshot := TaskSnapshot{
		TaskID:     string(task.ID),
		PeerID:     peerID,
		SessionKey: sessionKey,
		ContextID:  task.ContextID,
		State:      taskStateFromA2A(task.Status.State),
		UpdatedAt:  time.Now(),
	}
	if task.Status.Timestamp != nil {
		snapshot.UpdatedAt = *task.Status.Timestamp
	}
	if task.Status.Message != nil {
		msg := textFromMessage(task.Status.Message)
		switch snapshot.State {
		case TaskStateFailed, TaskStateCancelled:
			snapshot.Error = msg
		default:
			snapshot.Content = msg
		}
	}
	return snapshot
}

func applyEventToSnapshot(snapshot TaskSnapshot, event a2aproto.Event) TaskSnapshot {
	snapshot.UpdatedAt = time.Now()

	switch ev := event.(type) {
	case *a2aproto.Task:
		next := snapshotFromA2ATask(ev, snapshot.PeerID, snapshot.SessionKey)
		next.Content = appendIfMissing(snapshot.Content, next.Content)
		if next.Error == "" {
			next.Error = snapshot.Error
		}
		return next
	case *a2aproto.Message:
		snapshot.ContextID = ev.ContextID
		snapshot.Content = appendIfMissing(snapshot.Content, textFromMessage(ev))
	case *a2aproto.TaskArtifactUpdateEvent:
		snapshot.ContextID = ev.ContextID
		snapshot.State = TaskStateRunning
		if ev.Artifact != nil {
			snapshot.Content = appendIfMissing(snapshot.Content, textFromParts(ev.Artifact.Parts))
		}
	case *a2aproto.TaskStatusUpdateEvent:
		snapshot.ContextID = ev.ContextID
		snapshot.State = taskStateFromA2A(ev.Status.State)
		if ev.Status.Timestamp != nil {
			snapshot.UpdatedAt = *ev.Status.Timestamp
		}
		if ev.Status.Message != nil {
			msg := textFromMessage(ev.Status.Message)
			switch snapshot.State {
			case TaskStateFailed, TaskStateCancelled:
				snapshot.Error = msg
			case TaskStateCompleted:
				if snapshot.Content == "" {
					snapshot.Content = msg
				}
			default:
				snapshot.Content = appendIfMissing(snapshot.Content, msg)
			}
		}
	}

	return snapshot
}

func taskStateFromA2A(state a2aproto.TaskState) TaskState {
	switch state {
	case a2aproto.TaskStateSubmitted:
		return TaskStateSubmitted
	case a2aproto.TaskStateWorking:
		return TaskStateRunning
	case a2aproto.TaskStateCompleted:
		return TaskStateCompleted
	case a2aproto.TaskStateCanceled:
		return TaskStateCancelled
	case a2aproto.TaskStateFailed, a2aproto.TaskStateRejected:
		return TaskStateFailed
	default:
		return TaskStateRunning
	}
}

func isTerminalTaskState(state TaskState) bool {
	return state == TaskStateCompleted || state == TaskStateFailed || state == TaskStateCancelled
}

func textFromMessage(msg *a2aproto.Message) string {
	if msg == nil {
		return ""
	}
	return textFromParts(msg.Parts)
}

func textFromParts(parts a2aproto.ContentParts) string {
	var text strings.Builder
	for _, part := range parts {
		if part == nil {
			continue
		}
		text.WriteString(part.Text())
	}
	return text.String()
}

func appendIfMissing(base, extra string) string {
	switch {
	case extra == "":
		return base
	case base == "":
		return extra
	case strings.HasSuffix(base, extra):
		return base
	default:
		return base + extra
	}
}
