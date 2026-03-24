package subagent_spawn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	cronpkg "github.com/roelfdiedericks/goclaw/internal/cron"
	"github.com/roelfdiedericks/goclaw/internal/delegatedrun"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/types"
	"github.com/roelfdiedericks/goclaw/internal/user"
)

// Tool starts delegated subagent runs asynchronously.
type Tool struct {
	inject  ReturnToRequesterInject
	deliver ReturnToRequesterDeliver
}

type ReturnToRequesterInject func(ctx context.Context, u *user.User, source, sessionKey, runID, message, toolError string) error
type ReturnToRequesterDeliver func(ctx context.Context, u *user.User, source, message string) error

func NewTool(inject ReturnToRequesterInject, deliver ReturnToRequesterDeliver) *Tool {
	return &Tool{
		inject:  inject,
		deliver: deliver,
	}
}

func (t *Tool) Name() string { return "subagent_spawn" }

func (t *Tool) Description() string {
	return "Start one background worker. Returns runId immediately and sends a completion callback later by default. Owner sessions only."
}

func (t *Tool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"prompt": map[string]any{
				"type":        "string",
				"description": "The task for the worker to do.",
			},
			"purpose": map[string]any{
				"type":        "string",
				"description": "Optional short label so you can recognize this worker later.",
			},
			"timeoutSeconds": map[string]any{
				"type":        "integer",
				"description": "Optional time limit for this worker in seconds.",
			},
			"freshContext": map[string]any{
				"type":        "boolean",
				"description": "If true, start the worker with a fresh context instead of carrying over current conversation state. Default true.",
			},
			"ephemeral": map[string]any{
				"type":        "boolean",
				"description": "If true, treat this worker as temporary and do not keep its session in normal history. Default true.",
			},
			"enableThinking": map[string]any{
				"type":        "boolean",
				"description": "If true, allow extra model thinking if the chosen model supports it.",
			},
			"parentRunId": map[string]any{
				"type":        "string",
				"description": "Optional parent runId if you want to attach this worker to a specific parent.",
			},
			"notifyOnComplete": map[string]any{
				"type":        "boolean",
				"description": "If true, tell you later when the worker finishes. Default true.",
			},
		},
		"required": []string{"prompt"},
	}
}

type spawnInput struct {
	Prompt         string `json:"prompt"`
	Purpose        string `json:"purpose"`
	LLMPurpose     string `json:"llmPurpose"`
	TimeoutSeconds int    `json:"timeoutSeconds"`
	FreshContext   *bool  `json:"freshContext"`
	Ephemeral      *bool  `json:"ephemeral"`
	EnableThinking bool   `json:"enableThinking"`
	ParentRunID    string `json:"parentRunId"`
	NotifyOnComplete *bool `json:"notifyOnComplete"`
}

func (t *Tool) Execute(ctx context.Context, input json.RawMessage) (*types.ToolResult, error) {
	var in spawnInput
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, fmt.Errorf("invalid input: %w", err)
	}
	if strings.TrimSpace(in.Prompt) == "" {
		return nil, fmt.Errorf("prompt is required")
	}

	sessionCtx := types.GetSessionContext(ctx)
	if sessionCtx == nil || sessionCtx.User == nil || !sessionCtx.User.IsOwner() {
		return nil, fmt.Errorf("subagent tools are owner-only")
	}

	service := cronpkg.GetService()
	if service == nil {
		return nil, fmt.Errorf("cron service is not running")
	}

	freshContext := true
	if in.FreshContext != nil {
		freshContext = *in.FreshContext
	}
	ephemeral := true
	if in.Ephemeral != nil {
		ephemeral = *in.Ephemeral
	}

	requesterType := "subagent"
	requesterID := delegatedrun.BuildRequesterID(sessionCtx.Channel, sessionCtx.ChatID, sessionCtx.User.ID)
	sessionKey := ""
	if sessionKey == "" {
		sessionKey = fmt.Sprintf("subagent:%d", time.Now().UnixNano())
	}

	spec := delegatedrun.RunSpec{
		ParentRunID:    strings.TrimSpace(in.ParentRunID),
		RequesterType:  requesterType,
		RequesterID:    requesterID,
		RequesterSessionKey: strings.TrimSpace(sessionCtx.SessionKey),
		SessionKey:     sessionKey,
		Prompt:         strings.TrimSpace(in.Prompt),
		Purpose:        strings.TrimSpace(in.Purpose),
		LLMPurpose:     strings.TrimSpace(in.LLMPurpose),
		FreshContext:   freshContext,
		Ephemeral:      ephemeral,
		TimeoutSeconds: in.TimeoutSeconds,
		UserID:         sessionCtx.User.ID,
		EnableThinking: in.EnableThinking,
		SkipMirror:     true,
		JobName:        "subagent",
	}
	if spec.LLMPurpose == "" {
		spec.LLMPurpose = "subagent"
	}
	if spec.ParentRunID == "" {
		sessionRunID := strings.TrimSpace(sessionCtx.RunID)
		if sessionRunID != "" {
			if _, ok := service.GetDelegatedRun(sessionRunID); ok {
				spec.ParentRunID = sessionRunID
				L_debug("subagent_spawn: auto parent linked from delegated session run", "parentRunID", spec.ParentRunID)
			} else {
				L_debug("subagent_spawn: session run not in delegated registry, spawning as top-level", "sessionRunID", sessionRunID)
			}
		}
	}

	notifyOnComplete := true
	if in.NotifyOnComplete != nil {
		notifyOnComplete = *in.NotifyOnComplete
	}
	if notifyOnComplete {
		if t.inject == nil {
			return nil, fmt.Errorf("notifyOnComplete requires a requester callback path, but session injection is not configured")
		}
		if strings.TrimSpace(sessionCtx.Channel) == "" {
			return nil, fmt.Errorf("notifyOnComplete requires a requester channel")
		}
		if strings.TrimSpace(sessionCtx.SessionKey) == "" {
			return nil, fmt.Errorf("notifyOnComplete requires a requester session key")
		}
		spec.ResultMode = "return_to_requester"
		spec.ExpectsCompletionMessage = true
		spec.InjectMode = "tool_result"
		spec.CompletionDispatchSeq = 1
		spec.CleanupState = "pending"
	} else {
		spec.ResultMode = "store_only"
		spec.ExpectsCompletionMessage = false
		spec.InjectMode = "tool_result"
		spec.CompletionDispatchSeq = 1
		spec.CleanupState = "none"
	}

	runID, err := service.StartDelegatedRun(ctx, spec)
	if err != nil {
		L_error("subagent_spawn: failed", "error", err)
		return nil, err
	}
	if notifyOnComplete {
		requesterUser := sessionCtx.User
		requesterSource := strings.TrimSpace(sessionCtx.Channel)
		requesterSessionKey := strings.TrimSpace(sessionCtx.SessionKey)
		go t.waitAndNotify(runID, service, requesterUser, requesterSource, requesterSessionKey)
	}

	L_info("subagent_spawn: started", "runID", runID, "purpose", spec.Purpose, "requesterType", spec.RequesterType)
	out, _ := json.MarshalIndent(map[string]any{
		"ok":               true,
		"runId":            runID,
		"state":            delegatedrun.RunStateRunning,
		"purpose":          spec.Purpose,
		"notifyOnComplete": notifyOnComplete,
	}, "", "  ")
	return types.TextResult(string(out)), nil
}

func (t *Tool) waitAndNotify(runID string, service *cronpkg.Service, requesterUser *user.User, requesterSource, requesterSessionKey string) {
	ctx := context.Background()
	adapter := delegatedrun.CompletionDispatchAdapter{
		GetRun:         service.GetDelegatedRun,
		RecordPhase:    service.RecordDelegatedDispatchPhase,
		MarkDispatched: service.MarkDelegatedCompletionDispatched,
		ClaimDispatch:  service.ClaimDelegatedCompletionDispatch,
		ReleaseClaim:   service.ReleaseDelegatedCompletionDispatch,
		AdvanceSeq:     service.AdvanceDelegatedCompletionDispatchSeq,
		UpdateLifecycle: service.UpdateDelegatedCompletionLifecycle,
		CanDispatchPath: func(rec delegatedrun.RunRecord, path delegatedrun.DispatchPath) (bool, string) {
			if path != delegatedrun.DispatchPathDirect {
				return true, ""
			}
			binding := delegatedrun.ResolveRequesterBinding(rec)
			if ok, reason := delegatedrun.CanDirectDispatchForBinding(binding, time.Now()); !ok {
				return false, "requester_binding:" + reason
			}
			return true, ""
		},
	}
	envelope, notifyErr := delegatedrun.NotifyRunCompletion(
		ctx,
		runID,
		service.WaitDelegatedRun,
		service.GetDelegatedRun,
		service.ListDelegatedRuns,
		adapter,
		func(ctx context.Context, path delegatedrun.DispatchPath, runID, msg, toolError string) error {
			rec, _ := service.GetDelegatedRun(runID)
			binding := delegatedrun.ResolveRequesterBinding(rec)
			resolvedSource := binding.Channel
			if resolvedSource == "" {
				resolvedSource = requesterSource
			}
			resolvedSessionKey := binding.SessionKey
			if resolvedSessionKey == "" {
				resolvedSessionKey = requesterSessionKey
			}
			switch path {
			case delegatedrun.DispatchPathQueue:
				if t.inject == nil {
					return delegatedrun.NewNonRetryableDispatchError(
						delegatedrun.DispatchErrPathUnavailable,
						path,
						"session injector unavailable",
						nil,
					)
				}
				return delegatedrun.WrapDispatchPathError(path, t.inject(ctx, requesterUser, resolvedSource, resolvedSessionKey, runID, msg, toolError))
			case delegatedrun.DispatchPathDirect:
				if t.deliver == nil {
					return delegatedrun.NewNonRetryableDispatchError(
						delegatedrun.DispatchErrPathUnavailable,
						path,
						"channel delivery unavailable",
						nil,
					)
				}
				if err := t.deliver(ctx, requesterUser, resolvedSource, msg); err != nil {
					return delegatedrun.WrapDispatchPathError(path, err)
				}
				now := time.Now()
				_ = service.UpdateDelegatedRequesterBinding(runID, delegatedrun.RequesterBindingUpdate{LastActiveAt: &now, Reason: "direct_dispatch"})
				return nil
			default:
				return delegatedrun.NewNonRetryableDispatchError(
					delegatedrun.DispatchErrUnknownPath,
					path,
					"unknown dispatch path",
					nil,
				)
			}
		},
	)
	if notifyErr != nil {
		if envelope.WaitError != "" {
			L_warn("subagent_spawn: wait failed", "runID", runID, "error", envelope.WaitError)
		}
		if errors.Is(notifyErr, delegatedrun.ErrCompletionDeferredDescendants) {
			rec, _ := service.GetDelegatedRun(runID)
			wakeAt := rec.ContinuationWakeAt
			delegatedrun.SharedCompletionWakeScheduler().Schedule(runID, wakeAt, func() {
				t.waitAndNotify(runID, service, requesterUser, requesterSource, requesterSessionKey)
			})
			L_info("subagent_spawn: completion deferred for active descendants", "runID", runID, "reason", notifyErr.Error(), "wakeAt", wakeAt)
			return
		}
		rec, _ := service.GetDelegatedRun(runID)
		L_warn("subagent_spawn: return dispatch failed", "runID", runID, "dispatchOrder", rec.DispatchOrder, "fallbackMode", rec.FallbackMode, "error", notifyErr)
		return
	}
	if envelope.WaitError != "" {
		L_warn("subagent_spawn: wait failed", "runID", runID, "error", envelope.WaitError)
	}
	L_info("subagent_spawn: return_to_requester delivered", "runID", runID, "state", envelope.State)
}


