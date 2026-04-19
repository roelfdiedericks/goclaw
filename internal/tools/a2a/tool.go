package a2a

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	a2adomain "github.com/roelfdiedericks/goclaw/internal/a2a"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/types"
)

type Backend interface {
	GetA2AStatus() a2adomain.Status
	ListA2APeers(filter string) []a2adomain.PeerRecord
	ListA2ATasks(filter string, peer string) []a2adomain.TaskSummary
	GetA2APairingPayload() a2adomain.PairingPayload
	PingA2APeer(ctx context.Context, target string) (a2adomain.PingResult, error)
	SubmitA2ATask(ctx context.Context, target string, input string) (string, <-chan a2adomain.TaskSnapshot, error)
	ResumeA2ATask(ctx context.Context, target string, taskID string) (<-chan a2adomain.TaskSnapshot, error)
	CancelA2ATask(ctx context.Context, target string, taskID string) (a2adomain.TaskSnapshot, error)
}

type Tool struct {
	backend Backend
}

func NewTool(backend Backend) *Tool {
	return &Tool{backend: backend}
}

func (t *Tool) Name() string { return "a2a" }

func (t *Tool) Description() string {
	return "Inspect A2A state and interact with A2A peers. Actions: status, peers, tasks, pair, ping, submit, resume, cancel. Owner sessions only."
}

func (t *Tool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"enum":        []string{"status", "peers", "tasks", "pair", "ping", "submit", "resume", "cancel"},
				"description": "A2A action to perform.",
			},
			"filter": map[string]any{
				"type":        "string",
				"description": "Optional filter for peers/tasks. Defaults to 'all'.",
			},
			"peer": map[string]any{
				"type":        "string",
				"description": "Peer target for ping, submit, resume, cancel, or tasks scoped to one peer.",
			},
			"message": map[string]any{
				"type":        "string",
				"description": "Remote task input/message for submit.",
			},
			"taskId": map[string]any{
				"type":        "string",
				"description": "Task ID for resume or cancel.",
			},
			"timeoutSeconds": map[string]any{
				"type":        "integer",
				"description": "Optional operation timeout override in seconds for ping, submit, resume, or cancel.",
			},
		},
		"required": []string{"action"},
	}
}

type input struct {
	Action         string `json:"action"`
	Filter         string `json:"filter"`
	Peer           string `json:"peer"`
	Message        string `json:"message"`
	TaskID         string `json:"taskId"`
	TimeoutSeconds int    `json:"timeoutSeconds"`
}

type pingOutput struct {
	PeerID     string `json:"peerId"`
	Success    bool   `json:"success"`
	Latency    string `json:"latency"`
	Relayed    bool   `json:"relayed"`
	Message    string `json:"message"`
	RemoteMode string `json:"remoteMode,omitempty"`
}

type taskOperationOutput struct {
	Action   string                 `json:"action"`
	Peer     string                 `json:"peer"`
	TaskID   string                 `json:"taskId"`
	Snapshot a2adomain.TaskSnapshot `json:"snapshot"`
}

func (t *Tool) Execute(ctx context.Context, inputRaw json.RawMessage) (*types.ToolResult, error) {
	var in input
	if err := json.Unmarshal(inputRaw, &in); err != nil {
		return nil, fmt.Errorf("invalid input: %w", err)
	}
	if t.backend == nil {
		return nil, fmt.Errorf("A2A tool backend not configured")
	}
	sessionCtx := types.GetSessionContext(ctx)
	if sessionCtx == nil || sessionCtx.User == nil || !sessionCtx.User.IsOwner() {
		return nil, fmt.Errorf("A2A tool is owner-only")
	}

	in.Action = strings.TrimSpace(strings.ToLower(in.Action))
	in.Filter = strings.TrimSpace(strings.ToLower(in.Filter))
	in.Peer = strings.TrimSpace(in.Peer)
	in.Message = strings.TrimSpace(in.Message)
	in.TaskID = strings.TrimSpace(in.TaskID)

	L_info("a2a tool invoked",
		"action", in.Action,
		"peer", in.Peer,
		"taskID", in.TaskID,
		"filter", in.Filter,
		"messageLength", len(in.Message),
	)

	switch in.Action {
	case "status":
		return t.jsonResult(map[string]any{
			"action": "status",
			"status": t.backend.GetA2AStatus(),
		})
	case "peers":
		filter := defaultFilter(in.Filter)
		return t.jsonResult(map[string]any{
			"action": "peers",
			"filter": filter,
			"peers":  t.backend.ListA2APeers(filter),
		})
	case "tasks":
		filter := defaultFilter(in.Filter)
		return t.jsonResult(map[string]any{
			"action": "tasks",
			"filter": filter,
			"peer":   in.Peer,
			"tasks":  t.backend.ListA2ATasks(filter, in.Peer),
		})
	case "pair":
		return t.jsonResult(map[string]any{
			"action":  "pair",
			"pairing": t.backend.GetA2APairingPayload(),
		})
	case "ping":
		if in.Peer == "" {
			return nil, fmt.Errorf("peer is required for ping")
		}
		opCtx, cancel := toolTimeoutContext(ctx, in.TimeoutSeconds)
		defer cancel()
		result, err := t.backend.PingA2APeer(opCtx, in.Peer)
		if err != nil {
			return nil, err
		}
		return t.jsonResult(map[string]any{
			"action": "ping",
			"peer":   in.Peer,
			"result": pingOutput{
				PeerID:     result.PeerID,
				Success:    result.Success,
				Latency:    result.Latency.String(),
				Relayed:    result.Relayed,
				Message:    result.Message,
				RemoteMode: result.RemoteMode,
			},
		})
	case "submit":
		if in.Peer == "" {
			return nil, fmt.Errorf("peer is required for submit")
		}
		if in.Message == "" {
			return nil, fmt.Errorf("message is required for submit")
		}
		opCtx, cancel := toolTimeoutContext(ctx, in.TimeoutSeconds)
		defer cancel()
		taskID, updates, err := t.backend.SubmitA2ATask(opCtx, in.Peer, in.Message)
		if err != nil {
			return nil, err
		}
		return t.jsonResult(taskOperationOutput{
			Action:   "submit",
			Peer:     in.Peer,
			TaskID:   taskID,
			Snapshot: drainTaskSnapshots(opCtx, taskID, in.Peer, updates),
		})
	case "resume":
		if in.Peer == "" {
			return nil, fmt.Errorf("peer is required for resume")
		}
		if in.TaskID == "" {
			return nil, fmt.Errorf("taskId is required for resume")
		}
		opCtx, cancel := toolTimeoutContext(ctx, in.TimeoutSeconds)
		defer cancel()
		updates, err := t.backend.ResumeA2ATask(opCtx, in.Peer, in.TaskID)
		if err != nil {
			return nil, err
		}
		return t.jsonResult(taskOperationOutput{
			Action:   "resume",
			Peer:     in.Peer,
			TaskID:   in.TaskID,
			Snapshot: drainTaskSnapshots(opCtx, in.TaskID, in.Peer, updates),
		})
	case "cancel":
		if in.Peer == "" {
			return nil, fmt.Errorf("peer is required for cancel")
		}
		if in.TaskID == "" {
			return nil, fmt.Errorf("taskId is required for cancel")
		}
		opCtx, cancel := toolTimeoutContext(ctx, in.TimeoutSeconds)
		defer cancel()
		snapshot, err := t.backend.CancelA2ATask(opCtx, in.Peer, in.TaskID)
		if err != nil {
			return nil, err
		}
		return t.jsonResult(taskOperationOutput{
			Action:   "cancel",
			Peer:     in.Peer,
			TaskID:   in.TaskID,
			Snapshot: snapshot,
		})
	default:
		return nil, fmt.Errorf("unknown action: %s", in.Action)
	}
}

func (t *Tool) jsonResult(payload any) (*types.ToolResult, error) {
	out, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal result: %w", err)
	}
	return types.TextResult(string(out)), nil
}

func defaultFilter(filter string) string {
	filter = strings.TrimSpace(filter)
	if filter == "" {
		return "all"
	}
	return filter
}

func toolTimeoutContext(ctx context.Context, timeoutSeconds int) (context.Context, context.CancelFunc) {
	if timeoutSeconds <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, time.Duration(timeoutSeconds)*time.Second)
}

func drainTaskSnapshots(ctx context.Context, taskID, peer string, updates <-chan a2adomain.TaskSnapshot) a2adomain.TaskSnapshot {
	var latest a2adomain.TaskSnapshot
	for {
		select {
		case <-ctx.Done():
			if latest.TaskID == "" {
				return a2adomain.TaskSnapshot{
					TaskID:    taskID,
					PeerID:    peer,
					State:     a2adomain.TaskStateFailed,
					Error:     ctx.Err().Error(),
					UpdatedAt: time.Now(),
				}
			}
			latest.Error = ctx.Err().Error()
			latest.UpdatedAt = time.Now()
			return latest
		case snapshot, ok := <-updates:
			if !ok {
				if latest.TaskID == "" {
					latest.TaskID = taskID
				}
				if latest.PeerID == "" {
					latest.PeerID = peer
				}
				return latest
			}
			latest = snapshot
		}
	}
}
