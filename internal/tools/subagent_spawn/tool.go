package subagent_spawn

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
	return "Spawn an asynchronous delegated run. Returns runId immediately. Owner sessions only."
}

func (t *Tool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"prompt": map[string]any{
				"type":        "string",
				"description": "Prompt/instructions for the delegated run",
			},
			"purpose": map[string]any{
				"type":        "string",
				"description": "Optional purpose tag (e.g., subagent, research, tool-task)",
			},
			"timeoutSeconds": map[string]any{
				"type":        "integer",
				"description": "Optional per-run timeout in seconds",
			},
			"freshContext": map[string]any{
				"type":        "boolean",
				"description": "If true, run with fresh context (default true)",
			},
			"ephemeral": map[string]any{
				"type":        "boolean",
				"description": "If true, skip persistence in session history (default true)",
			},
			"enableThinking": map[string]any{
				"type":        "boolean",
				"description": "Enable extended thinking if supported by model",
			},
			"parentRunId": map[string]any{
				"type":        "string",
				"description": "Optional parent run ID for lineage",
			},
			"requesterType": map[string]any{
				"type":        "string",
				"description": "Optional requester type override",
			},
			"requesterId": map[string]any{
				"type":        "string",
				"description": "Optional requester ID override",
			},
			"sessionKey": map[string]any{
				"type":        "string",
				"description": "Optional delegated session key override",
			},
			"resultMode": map[string]any{
				"type":        "string",
				"enum":        []string{"store_only", "return_to_requester"},
				"description": "Result handling mode. return_to_requester sends completion summary back to requester context asynchronously.",
			},
			"dispatchOrder": map[string]any{
				"type":        "string",
				"enum":        []string{"queue_first", "direct_first"},
				"description": "Optional return_to_requester dispatch ordering. queue_first injects to requester session first; direct_first sends to requester channel first.",
			},
			"fallbackMode": map[string]any{
				"type":        "string",
				"enum":        []string{"none", "queue_fallback", "direct_fallback"},
				"description": "Optional fallback dispatch behavior if primary return path fails.",
			},
		},
		"required": []string{"prompt"},
	}
}

type spawnInput struct {
	Prompt         string `json:"prompt"`
	Purpose        string `json:"purpose"`
	TimeoutSeconds int    `json:"timeoutSeconds"`
	FreshContext   *bool  `json:"freshContext"`
	Ephemeral      *bool  `json:"ephemeral"`
	EnableThinking bool   `json:"enableThinking"`
	ParentRunID    string `json:"parentRunId"`
	RequesterType  string `json:"requesterType"`
	RequesterID    string `json:"requesterId"`
	SessionKey     string `json:"sessionKey"`
	ResultMode     string `json:"resultMode"`
	DispatchOrder  string `json:"dispatchOrder"`
	FallbackMode   string `json:"fallbackMode"`
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

	requesterType := in.RequesterType
	if requesterType == "" {
		requesterType = "subagent"
	}
	requesterID := in.RequesterID
	if requesterID == "" {
		requesterID = strings.TrimSpace(sessionCtx.Channel + ":" + sessionCtx.ChatID)
		if requesterID == ":" || requesterID == "" {
			requesterID = sessionCtx.User.ID
		}
	}
	sessionKey := strings.TrimSpace(in.SessionKey)
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
		FreshContext:   freshContext,
		Ephemeral:      ephemeral,
		TimeoutSeconds: in.TimeoutSeconds,
		UserID:         sessionCtx.User.ID,
		EnableThinking: in.EnableThinking,
		SkipMirror:     true,
		JobName:        "subagent",
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

	resultMode := strings.TrimSpace(in.ResultMode)
	if resultMode == "" {
		resultMode = "store_only"
	}
	if resultMode != "store_only" && resultMode != "return_to_requester" {
		return nil, fmt.Errorf("resultMode must be one of: store_only, return_to_requester")
	}
	if resultMode == "return_to_requester" {
		if t.inject == nil {
			return nil, fmt.Errorf("return_to_requester requested but session injector is not configured")
		}
		if strings.TrimSpace(sessionCtx.Channel) == "" {
			return nil, fmt.Errorf("return_to_requester requires a requester channel")
		}
		if strings.TrimSpace(sessionCtx.SessionKey) == "" {
			return nil, fmt.Errorf("return_to_requester requires a requester session key")
		}
		spec.ResultMode = "return_to_requester"
		spec.DispatchOrder = normalizeDispatchOrder(in.DispatchOrder)
		spec.FallbackMode = normalizeFallbackMode(in.FallbackMode)
		spec.InjectMode = "tool_result"
		spec.CompletionDispatchSeq = 1
	} else {
		spec.ResultMode = "store_only"
		spec.DispatchOrder = "queue_first"
		spec.FallbackMode = "none"
		spec.InjectMode = "tool_result"
		spec.CompletionDispatchSeq = 1
	}

	runID, err := service.StartDelegatedRun(ctx, spec)
	if err != nil {
		L_error("subagent_spawn: failed", "error", err)
		return nil, err
	}
	if resultMode == "return_to_requester" {
		requesterUser := sessionCtx.User
		requesterSource := strings.TrimSpace(sessionCtx.Channel)
		requesterSessionKey := strings.TrimSpace(sessionCtx.SessionKey)
		go t.waitAndNotify(runID, service, requesterUser, requesterSource, requesterSessionKey)
	}

	L_info("subagent_spawn: started", "runID", runID, "purpose", spec.Purpose, "requesterType", spec.RequesterType)
	out, _ := json.MarshalIndent(map[string]any{
		"ok":         true,
		"runId":      runID,
		"state":      delegatedrun.RunStateRunning,
		"purpose":    spec.Purpose,
		"resultMode": resultMode,
	}, "", "  ")
	return types.TextResult(string(out)), nil
}

func (t *Tool) waitAndNotify(runID string, service *cronpkg.Service, requesterUser *user.User, requesterSource, requesterSessionKey string) {
	ctx := context.Background()
	result, state, err := service.WaitDelegatedRun(ctx, runID)
	if err != nil {
		L_warn("subagent_spawn: wait failed", "runID", runID, "error", err)
	}

	msg := buildReturnMessage(runID, state, result, err)
	toolError := extractReturnToolError(state, result, err)
	rec, hasRec := service.GetDelegatedRun(runID)
	completionSeq := 1
	if hasRec && rec.CompletionDispatchSeq > 0 {
		completionSeq = rec.CompletionDispatchSeq
	}
	completionKey := fmt.Sprintf("%s:%d", runID, completionSeq)
	if hasRec && strings.TrimSpace(rec.CompletionDispatchKey) == completionKey {
		L_info("subagent_spawn: duplicate completion suppressed", "runID", runID, "completionKey", completionKey)
		_ = service.RecordDelegatedDispatchPhase(runID, "dedupe_check", "skipped_duplicate", completionKey)
		return
	}
	dispatchOrder := "queue_first"
	fallbackMode := "none"
	if hasRec {
		if strings.TrimSpace(rec.DispatchOrder) != "" {
			dispatchOrder = strings.TrimSpace(rec.DispatchOrder)
		}
		if strings.TrimSpace(rec.FallbackMode) != "" {
			fallbackMode = strings.TrimSpace(rec.FallbackMode)
		}
	}
	if notifyErr := t.dispatchReturn(ctx, service, requesterUser, requesterSource, requesterSessionKey, runID, msg, toolError, dispatchOrder, fallbackMode); notifyErr != nil {
		L_warn("subagent_spawn: return dispatch failed", "runID", runID, "dispatchOrder", dispatchOrder, "fallbackMode", fallbackMode, "error", notifyErr)
		if nextSeq, seqErr := service.AdvanceDelegatedCompletionDispatchSeq(runID); seqErr == nil {
			_ = service.RecordDelegatedDispatchPhase(runID, "dispatch_seq", "advanced_for_retry", fmt.Sprintf("next=%d", nextSeq))
		}
		return
	}
	if markErr := service.MarkDelegatedCompletionDispatched(runID, completionKey); markErr != nil {
		L_warn("subagent_spawn: failed to mark completion dispatch", "runID", runID, "error", markErr)
	}
	L_info("subagent_spawn: return_to_requester delivered", "runID", runID, "state", state)
}

func normalizeDispatchOrder(v string) string {
	switch strings.TrimSpace(v) {
	case "direct_first":
		return "direct_first"
	default:
		return "queue_first"
	}
}

func normalizeFallbackMode(v string) string {
	switch strings.TrimSpace(v) {
	case "queue_fallback":
		return "queue_fallback"
	case "direct_fallback":
		return "direct_fallback"
	default:
		return "none"
	}
}

func (t *Tool) dispatchReturn(
	ctx context.Context,
	service *cronpkg.Service,
	requesterUser *user.User,
	requesterSource, requesterSessionKey, runID, msg, toolError, dispatchOrder, fallbackMode string,
) error {
	primary := "queue"
	secondary := "direct"
	if dispatchOrder == "direct_first" {
		primary = "direct"
		secondary = "queue"
	}

	_ = service.RecordDelegatedDispatchPhase(runID, primary+"_primary", "attempt", "")
	primaryErr := t.dispatchVia(ctx, primary, requesterUser, requesterSource, requesterSessionKey, runID, msg, toolError)
	if primaryErr == nil {
		_ = service.RecordDelegatedDispatchPhase(runID, primary+"_primary", "success", "")
		L_info("subagent_spawn: return dispatch primary success", "runID", runID, "path", primary)
		return nil
	}
	_ = service.RecordDelegatedDispatchPhase(runID, primary+"_primary", "failed", primaryErr.Error())
	L_warn("subagent_spawn: return dispatch primary failed", "runID", runID, "path", primary, "error", primaryErr)

	switch fallbackMode {
	case "none":
		return primaryErr
	case "queue_fallback":
		if secondary != "queue" {
			secondary = "queue"
		}
	case "direct_fallback":
		if secondary != "direct" {
			secondary = "direct"
		}
	default:
		return primaryErr
	}

	_ = service.RecordDelegatedDispatchPhase(runID, secondary+"_fallback", "attempt", "")
	fallbackErr := t.dispatchVia(ctx, secondary, requesterUser, requesterSource, requesterSessionKey, runID, msg, toolError)
	if fallbackErr != nil {
		_ = service.RecordDelegatedDispatchPhase(runID, secondary+"_fallback", "failed", fallbackErr.Error())
		return fmt.Errorf("primary=%s: %w; fallback=%s: %v", primary, primaryErr, secondary, fallbackErr)
	}
	_ = service.RecordDelegatedDispatchPhase(runID, secondary+"_fallback", "success", "")
	L_info("subagent_spawn: return dispatch fallback success", "runID", runID, "path", secondary)
	return nil
}

func (t *Tool) dispatchVia(
	ctx context.Context,
	mode string,
	requesterUser *user.User,
	requesterSource, requesterSessionKey, runID, msg, toolError string,
) error {
	switch mode {
	case "queue":
		if t.inject == nil {
			return fmt.Errorf("session injector unavailable")
		}
		return t.inject(ctx, requesterUser, requesterSource, requesterSessionKey, runID, msg, toolError)
	case "direct":
		if t.deliver == nil {
			return fmt.Errorf("channel delivery unavailable")
		}
		return t.deliver(ctx, requesterUser, requesterSource, msg)
	default:
		return fmt.Errorf("unknown dispatch mode: %s", mode)
	}
}

func buildReturnMessage(runID string, state delegatedrun.RunState, result delegatedrun.RunResult, waitErr error) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Subagent run completed.\nrunId: %s\nstate: %s", runID, state))
	if waitErr != nil {
		b.WriteString(fmt.Sprintf("\nwaitError: %s", waitErr.Error()))
	}
	if result.Error != "" {
		b.WriteString(fmt.Sprintf("\nerror: %s", result.Error))
	}
	text := strings.TrimSpace(result.FinalText)
	if text != "" {
		if len(text) > 3000 {
			text = text[:3000] + "...(truncated)"
		}
		b.WriteString("\n\nresult:\n")
		b.WriteString(text)
	}
	return b.String()
}

func extractReturnToolError(state delegatedrun.RunState, result delegatedrun.RunResult, waitErr error) string {
	if waitErr != nil {
		return waitErr.Error()
	}
	if result.Error != "" {
		return result.Error
	}
	switch state {
	case delegatedrun.RunStateFailed, delegatedrun.RunStateTimeout, delegatedrun.RunStateCanceled:
		return string(state)
	default:
		return ""
	}
}

