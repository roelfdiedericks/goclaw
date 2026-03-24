package subagent_status

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	cronpkg "github.com/roelfdiedericks/goclaw/internal/cron"
	"github.com/roelfdiedericks/goclaw/internal/delegatedrun"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/types"
)

// Tool queries delegated subagent run status.
type Tool struct{}

func NewTool() *Tool { return &Tool{} }

func (t *Tool) Name() string { return "subagent_status" }

func (t *Tool) Description() string {
	return "Inspect a worker, fetch its full result by runId, review its logs, or send it a message. Owner sessions only."
}

func (t *Tool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"enum":        []string{"list", "info", "log", "focus", "unfocus", "steer", "send"},
				"description": "What to do: list workers, inspect one worker and fetch its stored result, read its event log, control later completion delivery, or message a running worker.",
			},
			"runId": map[string]any{
				"type":        "string",
				"description": "Worker run ID. Use this with action=info to inspect one worker and fetch its stored result.",
			},
			"limit": map[string]any{
				"type":        "integer",
				"description": "For action=list, maximum workers to return. Default 20.",
			},
			"sinceEventId": map[string]any{
				"type":        "integer",
				"description": "For action=log, only include events after this event ID.",
			},
			"logDepth": map[string]any{
				"type":        "integer",
				"description": "For action=log, maximum events to return. Default 50, max 500.",
			},
			"reason": map[string]any{
				"type":        "string",
				"description": "Optional note explaining why you changed focus or unfocus.",
			},
			"content": map[string]any{
				"type":        "string",
				"description": "For action=steer or action=send, the message to put into the worker session.",
			},
		},
	}
}

type statusInput struct {
	Action       string `json:"action"`
	RunID        string `json:"runId"`
	Limit        int    `json:"limit"`
	SinceEventID int64  `json:"sinceEventId"`
	LogDepth     int    `json:"logDepth"`
	Reason       string `json:"reason"`
	Content      string `json:"content"`
}

func (t *Tool) Execute(ctx context.Context, input json.RawMessage) (*types.ToolResult, error) {
	var in statusInput
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, fmt.Errorf("invalid input: %w", err)
	}

	sessionCtx := types.GetSessionContext(ctx)
	if sessionCtx == nil || sessionCtx.User == nil {
		return nil, delegatedrun.EnsureSubagentOwner(false)
	}
	if err := delegatedrun.EnsureSubagentOwner(sessionCtx.User.IsOwner()); err != nil {
		return nil, err
	}

	service := cronpkg.GetService()
	if service == nil {
		return nil, fmt.Errorf("cron service is not running")
	}

	action, err := delegatedrun.NormalizeStatusAction(in.Action, in.RunID)
	if err != nil {
		return nil, err
	}
	if action == "focus" || action == "unfocus" {
		runID := strings.TrimSpace(in.RunID)
		if runID == "" {
			return nil, fmt.Errorf("runId is required for %s action", action)
		}
		rec, ok := service.GetDelegatedRun(runID)
		if !ok {
			return nil, fmt.Errorf("run not found: %s", in.RunID)
		}
		now := time.Now()
		nextState := delegatedrun.RequesterBindingFocused
		if action == "unfocus" {
			nextState = delegatedrun.RequesterBindingUnfocused
		}
		update := delegatedrun.RequesterBindingUpdate{
			State:     nextState,
			Reason:    strings.TrimSpace(in.Reason),
			UpdatedAt: &now,
		}
		if err := service.UpdateDelegatedRequesterBinding(runID, update); err != nil {
			return nil, err
		}
		updated, _ := service.GetDelegatedRun(runID)
		out, _ := json.MarshalIndent(map[string]any{
			"action": action,
			"run": normalizeRecord(updated),
			"previousBindingState": rec.RequesterBindingState,
		}, "", "  ")
		return types.TextResult(string(out)), nil
	}
	if action == "steer" || action == "send" {
		runID := strings.TrimSpace(in.RunID)
		if runID == "" {
			return nil, fmt.Errorf("runId is required for %s action", action)
		}
		if strings.TrimSpace(in.Content) == "" {
			return nil, fmt.Errorf("content is required for %s action", action)
		}
		rec, ok := service.GetDelegatedRun(runID)
		if !ok {
			return nil, fmt.Errorf("run not found: %s", in.RunID)
		}
		if !delegatedrun.IsActiveState(rec.State) {
			return nil, delegatedrun.PolicyDenied(delegatedrun.PolicyReasonRunNotActive, fmt.Sprintf("run state is %s", rec.State))
		}
		if strings.TrimSpace(rec.SessionKey) == "" {
			return nil, fmt.Errorf("run session unavailable for %s action", action)
		}
		invokeLLM := action == "steer"
		if err := service.InjectDelegatedSessionMessage(ctx, rec.SessionKey, strings.TrimSpace(in.Content), invokeLLM, sessionCtx.User); err != nil {
			return nil, err
		}
		out, _ := json.MarshalIndent(map[string]any{
			"action":    action,
			"runId":     runID,
			"sessionKey": rec.SessionKey,
			"delivered": true,
			"invokeLLM": invokeLLM,
		}, "", "  ")
		return types.TextResult(string(out)), nil
	}

	if action == "log" {
		depth, err := delegatedrun.NormalizeStatusLogDepth(in.LogDepth)
		if err != nil {
			return nil, err
		}
		events := service.ListDelegatedRunEvents(in.SinceEventID, depth)
		items := make([]map[string]any, 0, len(events))
		for _, ev := range events {
			if runID := strings.TrimSpace(in.RunID); runID != "" && ev.RunID != runID {
				continue
			}
			items = append(items, map[string]any{
				"id":        ev.ID,
				"runId":     ev.RunID,
				"eventType": ev.EventType,
				"payload":   ev.Payload,
				"timestamp": ev.Timestamp,
			})
		}
		out, _ := json.MarshalIndent(map[string]any{
			"action":       "log",
			"sinceEventId": in.SinceEventID,
			"logDepth":     depth,
			"events":       items,
			"count":        len(items),
		}, "", "  ")
		return types.TextResult(string(out)), nil
	}

	if action == "info" || strings.TrimSpace(in.RunID) != "" {
		runID := strings.TrimSpace(in.RunID)
		if runID == "" {
			return nil, fmt.Errorf("runId is required for info action")
		}
		L_debug("subagent_status: single run query", "runID", runID)
		rec, ok := service.GetDelegatedRun(runID)
		if !ok {
			L_warn("subagent_status: run not found", "runID", runID)
			return nil, fmt.Errorf("run not found: %s", in.RunID)
		}
		out, _ := json.MarshalIndent(map[string]any{
			"action": "info",
			"run": normalizeRecord(rec),
		}, "", "  ")
		L_debug("subagent_status: single run returned", "runID", runID, "state", rec.State)
		return types.TextResult(string(out)), nil
	}

	limit := in.Limit
	if limit <= 0 {
		limit = 20
	}
	recs := service.ListDelegatedRuns()
	if len(recs) > limit {
		recs = recs[:limit]
	}
	L_debug("subagent_status: list query", "limit", limit, "returned", len(recs))
	items := make([]map[string]any, 0, len(recs))
	for _, rec := range recs {
		items = append(items, normalizeRecord(rec))
	}
	out, _ := json.MarshalIndent(map[string]any{
		"action": "list",
		"items": items,
		"count": len(items),
	}, "", "  ")
	return types.TextResult(string(out)), nil
}

func normalizeRecord(rec delegatedrun.RunRecord) map[string]any {
	now := time.Now()
	bindingAgeSeconds, bindingIdleSeconds, canDirectDispatch, directDispatchReason := delegatedrun.BindingTelemetry(rec, now)
	return map[string]any{
		"runId":         rec.RunID,
		"parentRunId":   rec.ParentRunID,
		"requesterType": rec.RequesterType,
		"requesterId":   rec.RequesterID,
		"requesterSessionKey": rec.RequesterSessionKey,
		"requesterBindingState": rec.RequesterBindingState,
		"requesterBindingReason": rec.RequesterBindingReason,
		"requesterBindingUpdatedAt": rec.RequesterBindingUpdatedAt,
		"requesterBindingLastActiveAt": rec.RequesterBindingLastActiveAt,
		"requesterBindingAgeSeconds": bindingAgeSeconds,
		"requesterBindingIdleSeconds": bindingIdleSeconds,
		"canDirectDispatch": canDirectDispatch,
		"directDispatchReason": directDispatchReason,
		"sessionKey":    rec.SessionKey,
		"purpose":       rec.Purpose,
		"resultMode":    rec.ResultMode,
		"dispatchOrder": rec.DispatchOrder,
		"fallbackMode":  rec.FallbackMode,
		"cleanupState":  rec.CleanupState,
		"deferredReason": rec.DeferredReason,
		"continuationState": rec.ContinuationState,
		"continuationReason": rec.ContinuationReason,
		"continuationWakeAt": rec.ContinuationWakeAt,
		"dispatchPhases": rec.DispatchPhases,
		"state":         rec.State,
		"startedAt":     rec.StartedAt,
		"finishedAt":    rec.FinishedAt,
		"result": map[string]any{
			"finalText": rec.Result.FinalText,
			"error":     rec.Result.Error,
			"usage":     rec.Result.Usage,
		},
	}
}

