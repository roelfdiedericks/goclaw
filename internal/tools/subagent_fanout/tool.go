package subagent_fanout

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	cronpkg "github.com/roelfdiedericks/goclaw/internal/cron"
	"github.com/roelfdiedericks/goclaw/internal/delegatedrun"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/session"
	"github.com/roelfdiedericks/goclaw/internal/types"
	"github.com/roelfdiedericks/goclaw/internal/user"
)

const synthesisMaxInputChars = 12000
const defaultInlineReserveTokens = 4000

// Tool runs a bounded parallel fanout across delegated runs and returns detailed child results.
type Tool struct {
	inject  ReturnToRequesterInject
	deliver ReturnToRequesterDeliver
}

type ReturnToRequesterInject func(ctx context.Context, u *user.User, source, sessionKey, runID, message, toolError string) error
type ReturnToRequesterDeliver func(ctx context.Context, u *user.User, source, message string) error

func NewTool() *Tool { return &Tool{} }

func NewToolWithReturnToRequester(inject ReturnToRequesterInject, deliver ReturnToRequesterDeliver) *Tool {
	return &Tool{
		inject:  inject,
		deliver: deliver,
	}
}

func (t *Tool) Name() string { return "subagent_fanout" }

func (t *Tool) Description() string {
	return "Run several workers in parallel and get their results in the current turn. It tries to return full child outputs inline; if some do not fit, it returns explicit run IDs so you can inspect the rest. If any worker fails, times out, is canceled, or fails to start, the fanout result returns ok=false with failure details. You can optionally request one extra summary. Later completion callback is off by default. Owner sessions only."
}

func (t *Tool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"prompts": map[string]any{
				"type":        "array",
				"description": "Worker tasks to run in parallel. The tool returns their results in the same turn when possible.",
				"items": map[string]any{
					"type": "string",
				},
			},
			"purpose": map[string]any{
				"type":        "string",
				"description": "Optional short label so you can recognize this fanout later.",
			},
			"parallelism": map[string]any{
				"type":        "integer",
				"description": "Maximum workers to run at the same time. Default 3.",
			},
			"timeoutSeconds": map[string]any{
				"type":        "integer",
				"description": "Optional time limit for each worker in seconds.",
			},
			"freshContext": map[string]any{
				"type":        "boolean",
				"description": "If true, each worker starts with a fresh context instead of carrying over current conversation state. Default true.",
			},
			"ephemeral": map[string]any{
				"type":        "boolean",
				"description": "If true, treat the worker sessions as temporary and do not keep them in normal history. Default true.",
			},
			"enableThinking": map[string]any{
				"type":        "boolean",
				"description": "If true, allow extra model thinking for the child workers if the chosen model supports it.",
			},
			"parentRunId": map[string]any{
				"type":        "string",
				"description": "Optional parent runId if you want to attach this fanout to a specific parent. Otherwise the current run is used when possible.",
			},
			"includeSummary": map[string]any{
				"type":        "boolean",
				"description": "If true, also include one extra machine-generated summary across the worker results. Default false. The main result is still the worker outputs themselves. If worker outcomes are unhealthy or the summary would only cover some worker outputs, GoClaw skips it instead of returning a partial summary.",
			},
			"summaryPrompt": map[string]any{
				"type":        "string",
				"description": "Optional instruction for the extra summary. Use this only if you want the summary phrased in a specific way.",
			},
			"summaryTimeoutSeconds": map[string]any{
				"type":        "integer",
				"description": "Optional time limit in seconds for the extra summary only.",
			},
			"notifyOnComplete": map[string]any{
				"type":        "boolean",
				"description": "If true, also tell you later when the fanout is fully complete. Default false. This is separate from the worker results returned now.",
			},
		},
		"required": []string{"prompts"},
	}
}

type fanoutInput struct {
	Prompts               []string `json:"prompts"`
	Purpose               string   `json:"purpose"`
	LLMPurpose            string   `json:"llmPurpose"`
	Parallelism           int      `json:"parallelism"`
	TimeoutSeconds        int      `json:"timeoutSeconds"`
	FreshContext          *bool    `json:"freshContext"`
	Ephemeral             *bool    `json:"ephemeral"`
	EnableThinking        bool     `json:"enableThinking"`
	ParentRunID           string   `json:"parentRunId"`
	IncludeSummary        bool     `json:"includeSummary"`
	SummaryPrompt         string   `json:"summaryPrompt"`
	SummaryTimeoutSeconds int      `json:"summaryTimeoutSeconds"`
	NotifyOnComplete      *bool    `json:"notifyOnComplete"`
}

type childOutcome struct {
	Index  int                   `json:"index"`
	Prompt string                `json:"prompt"`
	RunID  string                `json:"runId,omitempty"`
	State  delegatedrun.RunState `json:"state,omitempty"`
	Output string                `json:"output,omitempty"`
	Error  string                `json:"error,omitempty"`
}

type returnedChildOutcome struct {
	Index               int                   `json:"index"`
	Prompt              string                `json:"prompt"`
	RunID               string                `json:"runId,omitempty"`
	State               delegatedrun.RunState `json:"state,omitempty"`
	Output              string                `json:"output,omitempty"`
	Error               string                `json:"error,omitempty"`
	OutputOmittedReason string                `json:"outputOmittedReason,omitempty"`
}

type fanoutSynthesisOutcome struct {
	OK         bool                  `json:"ok"`
	RunID      string                `json:"runId,omitempty"`
	State      delegatedrun.RunState `json:"state,omitempty"`
	Text       string                `json:"text,omitempty"`
	Error      string                `json:"error,omitempty"`
	ModelError string                `json:"modelError,omitempty"`
}

type fanoutSummaryStatus struct {
	Requested     bool   `json:"requested"`
	Included      bool   `json:"included"`
	Reason        string `json:"reason,omitempty"`
	Message       string `json:"message,omitempty"`
	IncludedItems int    `json:"includedItems,omitempty"`
	TotalItems    int    `json:"totalItems,omitempty"`
}

type fanoutOverflowHandle struct {
	Index  int                   `json:"index"`
	Prompt string                `json:"prompt"`
	RunID  string                `json:"runId,omitempty"`
	State  delegatedrun.RunState `json:"state,omitempty"`
}

type fanoutInspectPath struct {
	Tool   string `json:"tool"`
	Action string `json:"action"`
}

type fanoutOverflow struct {
	Triggered         bool                   `json:"triggered"`
	ReturnedInline    int                    `json:"returnedInline"`
	OmittedFromInline int                    `json:"omittedFromInline"`
	Inspect           *fanoutInspectPath     `json:"inspect,omitempty"`
	Omitted           []fanoutOverflowHandle `json:"omitted,omitempty"`
	Message           string                 `json:"message,omitempty"`
}

type fanoutStats struct {
	Total       int `json:"total"`
	Completed   int `json:"completed"`
	Failed      int `json:"failed"`
	TimedOut    int `json:"timedOut"`
	Canceled    int `json:"canceled"`
	SpawnFailed int `json:"spawnFailed"`
}

type fanoutPayload struct {
	OK                 bool                   `json:"ok"`
	Status             string                 `json:"status"`
	Message            string                 `json:"message"`
	Stats              fanoutStats            `json:"stats"`
	ParentRunID        string                 `json:"parentRunId,omitempty"`
	Parallelism        int                    `json:"parallelism"`
	NotifyOnComplete   bool                   `json:"notifyOnComplete"`
	Items              []returnedChildOutcome `json:"items"`
	Overflow           fanoutOverflow         `json:"overflow"`
	ExtraSummaryStatus fanoutSummaryStatus    `json:"extraSummaryStatus"`
	ExtraSummary       *fanoutSynthesisOutcome `json:"extraSummary,omitempty"`
	CompletionCallback map[string]any         `json:"completionCallback"`
}

func (t *Tool) Execute(ctx context.Context, input json.RawMessage) (*types.ToolResult, error) {
	var in fanoutInput
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, fmt.Errorf("invalid input: %w", err)
	}
	if len(in.Prompts) == 0 {
		return nil, fmt.Errorf("prompts is required")
	}
	sessionCtx := types.GetSessionContext(ctx)
	if sessionCtx == nil || sessionCtx.User == nil || !sessionCtx.User.IsOwner() {
		return nil, fmt.Errorf("subagent tools are owner-only")
	}
	service := cronpkg.GetService()
	if service == nil {
		return nil, fmt.Errorf("cron service is not running")
	}

	prompts := make([]string, 0, len(in.Prompts))
	for _, p := range in.Prompts {
		p = strings.TrimSpace(p)
		if p == "" {
			return nil, fmt.Errorf("prompts cannot contain empty items")
		}
		prompts = append(prompts, p)
	}

	freshContext := true
	if in.FreshContext != nil {
		freshContext = *in.FreshContext
	}
	ephemeral := true
	if in.Ephemeral != nil {
		ephemeral = *in.Ephemeral
	}
	purpose := strings.TrimSpace(in.Purpose)
	if purpose == "" {
		purpose = "subagent_fanout"
	}
	llmPurpose := strings.TrimSpace(in.LLMPurpose)
	if llmPurpose == "" {
		llmPurpose = "subagent"
	}
	parentRunID := strings.TrimSpace(in.ParentRunID)
	if parentRunID == "" {
		sessionRunID := strings.TrimSpace(sessionCtx.RunID)
		if sessionRunID != "" {
			if _, ok := service.GetDelegatedRun(sessionRunID); ok {
				parentRunID = sessionRunID
				L_debug("subagent_fanout: auto parent linked from delegated session run", "parentRunID", parentRunID)
			} else {
				L_debug("subagent_fanout: session run not in delegated registry, spawning as top-level", "sessionRunID", sessionRunID)
			}
		}
	}

	parallelism := in.Parallelism
	if parallelism <= 0 {
		parallelism = 3
	}
	if parallelism > len(prompts) {
		parallelism = len(prompts)
	}
	L_info("subagent_fanout: start", "totalPrompts", len(prompts), "parallelism", parallelism, "parentRunID", parentRunID, "includeSummary", in.IncludeSummary, "timeoutSeconds", in.TimeoutSeconds, "purpose", purpose, "llmPurpose", llmPurpose)

	outcomes := make([]childOutcome, len(prompts))
	sem := make(chan struct{}, parallelism)
	var wg sync.WaitGroup
	for i := range prompts {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			outcomes[i] = t.runChild(ctx, service, sessionCtx, in, prompts[i], i, purpose, llmPurpose, parentRunID, freshContext, ephemeral)
		}()
	}
	wg.Wait()

	completed := 0
	failed := 0
	timedOut := 0
	canceled := 0
	spawnFailed := 0
	for _, item := range outcomes {
		switch item.State {
		case delegatedrun.RunStateCompleted:
			completed++
		case delegatedrun.RunStateFailed:
			failed++
		case delegatedrun.RunStateTimeout:
			timedOut++
		case delegatedrun.RunStateCanceled:
			canceled++
		default:
			if item.RunID == "" {
				spawnFailed++
			}
		}
	}

	stats := fanoutStats{
		Total:       len(outcomes),
		Completed:   completed,
		Failed:      failed,
		TimedOut:    timedOut,
		Canceled:    canceled,
		SpawnFailed: spawnFailed,
	}
	ok, status, statusMessage, completionState := fanoutOutcome(stats)

	notifyOnComplete := false
	if in.NotifyOnComplete != nil {
		notifyOnComplete = *in.NotifyOnComplete
	}
	if t.inject == nil && t.deliver == nil {
		notifyOnComplete = false
	}

	summaryStatus := fanoutSummaryStatus{}
	var summary *fanoutSynthesisOutcome
	if in.IncludeSummary {
		if ok {
			summary, summaryStatus = t.runSynthesis(ctx, service, sessionCtx, in, outcomes, parentRunID, llmPurpose, freshContext, ephemeral)
		} else {
			summaryStatus = fanoutSummaryStatus{
				Requested: true,
				Included:  false,
				Reason:    "child_outcomes_unhealthy",
				Message:   "Extra summary skipped because one or more worker runs failed, timed out, were canceled, or failed to start.",
			}
		}
	}

	payload := packFanoutPayload(sessionCtx, outcomes, ok, status, statusMessage, stats, parentRunID, parallelism, notifyOnComplete, summaryStatus, summary)
	if notifyOnComplete {
		if handoffRunID, handoffErr := t.startCompletionHandoffDispatch(ctx, service, sessionCtx, outcomes, summary, stats, statusMessage, completionState, parentRunID, llmPurpose, freshContext, ephemeral); handoffErr != nil {
			L_warn("subagent_fanout: later completion callback setup failed", "error", handoffErr)
			payload.CompletionCallback = map[string]any{
				"enabled": false,
				"error":   handoffErr.Error(),
			}
		} else {
			payload.CompletionCallback = map[string]any{
				"enabled": true,
				"runId":   handoffRunID,
				"mode":    "later_callback",
			}
		}
	} else {
		payload.CompletionCallback = map[string]any{
			"enabled": false,
		}
	}

	out, _ := json.MarshalIndent(payload, "", "  ")
	L_info("subagent_fanout: completed", "total", len(outcomes), "completed", completed, "failed", failed, "timedOut", timedOut, "canceled", canceled, "spawnFailed", spawnFailed, "overflow", payload.Overflow.Triggered, "returnedInline", payload.Overflow.ReturnedInline)
	return types.TextResult(string(out)), nil
}

func (t *Tool) startCompletionHandoffDispatch(
	ctx context.Context,
	service *cronpkg.Service,
	sessionCtx *types.SessionContext,
	outcomes []childOutcome,
	summary *fanoutSynthesisOutcome,
	stats fanoutStats,
	statusMessage string,
	completionState delegatedrun.RunState,
	parentRunID, llmPurpose string,
	freshContext, ephemeral bool,
) (string, error) {
	if t.inject == nil {
		return "", fmt.Errorf("session injector unavailable")
	}
	if sessionCtx == nil || sessionCtx.User == nil {
		return "", fmt.Errorf("session context is required")
	}
	if strings.TrimSpace(sessionCtx.Channel) == "" || strings.TrimSpace(sessionCtx.SessionKey) == "" {
		return "", fmt.Errorf("requester channel/session is required")
	}
	requesterID := delegatedrun.BuildRequesterID(sessionCtx.Channel, sessionCtx.ChatID, sessionCtx.User.ID)
	finalText := buildFanoutCompletionSummary(outcomes, summary, stats, statusMessage)
	spec := delegatedrun.RunSpec{
		ParentRunID:              parentRunID,
		RequesterType:            "subagent_fanout",
		RequesterID:              requesterID,
		RequesterSessionKey:      strings.TrimSpace(sessionCtx.SessionKey),
		SessionKey:               fmt.Sprintf("subagent-fanout-completion:%d", time.Now().UnixNano()),
		Prompt:                   "synthetic fanout completion handoff",
		Purpose:                  "subagent_fanout_completion",
		LLMPurpose:               llmPurpose,
		ResultMode:               "return_to_requester",
		ExpectsCompletionMessage: true,
		FreshContext:             freshContext,
		Ephemeral:                ephemeral,
		TimeoutSeconds:           120,
		UserID:                   sessionCtx.User.ID,
		EnableThinking:           false,
		SkipMirror:               true,
		JobName:                  "subagent_fanout",
	}
	result := delegatedrun.RunResult{FinalText: finalText}
	runID, err := service.CreateSyntheticDelegatedCompletion(spec, result, completionState)
	if err != nil {
		return "", err
	}
	go t.dispatchRecordedCompletion(runID, result, completionState, nil, service, sessionCtx.User, strings.TrimSpace(sessionCtx.Channel), strings.TrimSpace(sessionCtx.SessionKey))
	return runID, nil
}

func (t *Tool) dispatchRecordedCompletion(runID string, result delegatedrun.RunResult, state delegatedrun.RunState, waitErr error, service *cronpkg.Service, requesterUser *user.User, requesterSource, requesterSessionKey string) {
	if strings.TrimSpace(runID) == "" {
		return
	}
	ctx := context.Background()
	adapter := t.completionAdapter(service)
	envelope, notifyErr := delegatedrun.NotifyRecordedCompletion(
		ctx,
		runID,
		state,
		result,
		waitErr,
		service.GetDelegatedRun,
		service.ListDelegatedRuns,
		adapter,
		t.dispatchCompletionPath(service, requesterUser, requesterSource, requesterSessionKey),
	)
	if notifyErr != nil {
		if errors.Is(notifyErr, delegatedrun.ErrCompletionDeferredDescendants) {
			rec, _ := service.GetDelegatedRun(runID)
			wakeAt := rec.ContinuationWakeAt
			delegatedrun.SharedCompletionWakeScheduler().Schedule(runID, wakeAt, func() {
				t.dispatchRecordedCompletion(runID, result, state, waitErr, service, requesterUser, requesterSource, requesterSessionKey)
			})
			L_info("subagent_fanout: recorded completion deferred for active descendants", "runID", runID, "reason", notifyErr.Error(), "wakeAt", wakeAt)
			return
		}
		L_warn("subagent_fanout: recorded completion dispatch failed", "runID", runID, "error", notifyErr, "waitError", envelope.WaitError)
		return
	}
	L_info("subagent_fanout: recorded completion dispatched", "runID", runID, "state", envelope.State)
}

func (t *Tool) runSynthesis(
	ctx context.Context,
	service *cronpkg.Service,
	sessionCtx *types.SessionContext,
	in fanoutInput,
	outcomes []childOutcome,
	parentRunID string,
	llmPurpose string,
	freshContext, ephemeral bool,
) (*fanoutSynthesisOutcome, fanoutSummaryStatus) {
	requesterID := delegatedrun.BuildRequesterID(sessionCtx.Channel, sessionCtx.ChatID, sessionCtx.User.ID)
	summaryPrompt := strings.TrimSpace(in.SummaryPrompt)
	if summaryPrompt == "" {
		summaryPrompt = "Create one concise summary across the worker results. Include key agreements, disagreements, and recommended next action."
	}
	outcomesJSON, included, inputTruncated := buildBoundedSynthesisOutcomesJSON(outcomes, synthesisMaxInputChars)
	L_debug("subagent_fanout: summary input prepared", "totalItems", len(outcomes), "includedItems", included, "inputTruncated", inputTruncated)
	status := fanoutSummaryStatus{
		Requested:     true,
		IncludedItems: included,
		TotalItems:    len(outcomes),
	}
	if inputTruncated {
		status.Included = false
		status.Reason = "partial_summary_skipped"
		status.Message = "Extra summary skipped because it would only cover some worker outputs. Use the main worker results instead."
		L_info("subagent_fanout: extra summary skipped", "parentRunID", parentRunID, "includedItems", included, "totalItems", len(outcomes), "reason", status.Reason)
		return nil, status
	}
	modelPrompt := fmt.Sprintf(
		"Fanout child outcomes JSON:\n%s\n\nMeta: included=%d total=%d\n\nInstruction:\n%s",
		outcomesJSON,
		included,
		len(outcomes),
		summaryPrompt,
	)

	summaryTimeout := in.TimeoutSeconds
	if in.SummaryTimeoutSeconds > 0 {
		summaryTimeout = in.SummaryTimeoutSeconds
	}
	L_info("subagent_fanout: extra summary start", "parentRunID", parentRunID, "includedItems", included, "totalItems", len(outcomes), "timeoutSeconds", summaryTimeout, "llmPurpose", llmPurpose)

	spec := delegatedrun.RunSpec{
		ParentRunID:         parentRunID,
		RequesterType:       "subagent_fanout",
		RequesterID:         requesterID,
		RequesterSessionKey: strings.TrimSpace(sessionCtx.SessionKey),
		SessionKey:          fmt.Sprintf("subagent-fanout-summary:%d", time.Now().UnixNano()),
		Prompt:              modelPrompt,
		Purpose:             "subagent_fanout_summary",
		LLMPurpose:          llmPurpose,
		ResultMode:          "store_only",
		FreshContext:        freshContext,
		Ephemeral:           ephemeral,
		TimeoutSeconds:      summaryTimeout,
		UserID:              sessionCtx.User.ID,
		EnableThinking:      in.EnableThinking,
		SkipMirror:          true,
		JobName:             "subagent_fanout",
	}
	runID, err := service.StartDelegatedRun(ctx, spec)
	if err != nil {
		L_warn("subagent_fanout: extra summary spawn failed", "parentRunID", parentRunID, "error", err)
		return &fanoutSynthesisOutcome{
			OK:    false,
			Error: err.Error(),
		}, fanoutSummaryStatus{
			Requested:     true,
			Included:      false,
			Reason:        "summary_failed_to_start",
			Message:       "Extra summary could not be started.",
			IncludedItems: included,
			TotalItems:    len(outcomes),
		}
	}
	L_debug("subagent_fanout: extra summary spawned", "runID", runID, "parentRunID", parentRunID)
	result, state, waitErr := service.WaitDelegatedRun(ctx, runID)
	resp := &fanoutSynthesisOutcome{
		OK:    waitErr == nil,
		RunID: runID,
		State: state,
		Text:  strings.TrimSpace(result.FinalText),
	}
	if waitErr != nil {
		L_warn("subagent_fanout: extra summary wait failed", "runID", runID, "state", state, "error", waitErr)
		resp.Error = waitErr.Error()
	}
	if result.Error != "" {
		L_warn("subagent_fanout: extra summary model error", "runID", runID, "state", state, "error", result.Error)
		resp.ModelError = result.Error
	}
	if waitErr == nil && result.Error == "" {
		L_info("subagent_fanout: extra summary completed", "runID", runID, "state", state, "textLen", len(strings.TrimSpace(result.FinalText)))
	}
	t.recordSynthesisCompletionParity(service, runID, result, state, waitErr)
	status.Included = true
	return resp, status
}

func (t *Tool) recordSynthesisCompletionParity(service *cronpkg.Service, runID string, result delegatedrun.RunResult, state delegatedrun.RunState, waitErr error) {
	if strings.TrimSpace(runID) == "" || service == nil {
		return
	}
	ctx := context.Background()
	adapter := delegatedrun.CompletionDispatchAdapter{
		GetRun:          service.GetDelegatedRun,
		RecordPhase:     service.RecordDelegatedDispatchPhase,
		MarkDispatched:  service.MarkDelegatedCompletionDispatched,
		AdvanceSeq:      service.AdvanceDelegatedCompletionDispatchSeq,
		UpdateLifecycle: service.UpdateDelegatedCompletionLifecycle,
	}
	_, err := delegatedrun.NotifyRecordedCompletion(
		ctx,
		runID,
		state,
		result,
		waitErr,
		service.GetDelegatedRun,
		service.ListDelegatedRuns,
		adapter,
		func(ctx context.Context, path delegatedrun.DispatchPath, runID, msg, toolError string) error {
			// Summary parity path should use shared dispatch lifecycle without producing
			// an extra requester-facing notification.
			return nil
		},
	)
	if err != nil {
		L_warn("subagent_fanout: extra summary parity completion flow failed", "runID", runID, "error", err)
		return
	}
	L_debug("subagent_fanout: extra summary parity completion flow recorded", "runID", runID)
}

func (t *Tool) completionAdapter(service *cronpkg.Service) delegatedrun.CompletionDispatchAdapter {
	return delegatedrun.CompletionDispatchAdapter{
		GetRun:          service.GetDelegatedRun,
		RecordPhase:     service.RecordDelegatedDispatchPhase,
		MarkDispatched:  service.MarkDelegatedCompletionDispatched,
		ClaimDispatch:   service.ClaimDelegatedCompletionDispatch,
		ReleaseClaim:    service.ReleaseDelegatedCompletionDispatch,
		AdvanceSeq:      service.AdvanceDelegatedCompletionDispatchSeq,
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
}

func (t *Tool) dispatchCompletionPath(service *cronpkg.Service, requesterUser *user.User, requesterSource, requesterSessionKey string) func(context.Context, delegatedrun.DispatchPath, string, string, string) error {
	return func(ctx context.Context, path delegatedrun.DispatchPath, runID, msg, toolError string) error {
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
	}
}

func buildFanoutCompletionSummary(outcomes []childOutcome, summary *fanoutSynthesisOutcome, stats fanoutStats, statusMessage string) string {
	var b strings.Builder
	b.WriteString("Fanout run completed.\n")
	if strings.TrimSpace(statusMessage) != "" {
		b.WriteString(statusMessage)
		b.WriteString("\n")
	}
	b.WriteString(fmt.Sprintf(
		"total: %d\ncompleted: %d\nfailed: %d\ntimedOut: %d\ncanceled: %d\nspawnFailed: %d",
		stats.Total,
		stats.Completed,
		stats.Failed,
		stats.TimedOut,
		stats.Canceled,
		stats.SpawnFailed,
	))
	if summary != nil && strings.TrimSpace(summary.Text) != "" {
		b.WriteString("\n\nextra summary:\n")
		b.WriteString(strings.TrimSpace(summary.Text))
	}
	maxItems := len(outcomes)
	if maxItems > 8 {
		maxItems = 8
	}
	if maxItems > 0 {
		b.WriteString("\n\nchild outcomes:\n")
		for i := 0; i < maxItems; i++ {
			item := outcomes[i]
			line := fmt.Sprintf("%d. [%s] %s", item.Index+1, item.State, item.Prompt)
			if strings.TrimSpace(item.Output) != "" {
				line += " -> " + trimForDisplay(item.Output, 240)
			} else if strings.TrimSpace(item.Error) != "" {
				line += " -> error: " + strings.TrimSpace(item.Error)
			}
			b.WriteString(line)
			if i < maxItems-1 {
				b.WriteString("\n")
			}
		}
		if len(outcomes) > maxItems {
			b.WriteString(fmt.Sprintf("\n... and %d more child outcomes.", len(outcomes)-maxItems))
		}
	}
	return strings.TrimSpace(b.String())
}

func buildBoundedSynthesisOutcomesJSON(outcomes []childOutcome, maxChars int) (string, int, bool) {
	if len(outcomes) == 0 {
		return "[]", 0, false
	}
	n := len(outcomes)
	truncated := false
	if maxChars <= 0 {
		maxChars = synthesisMaxInputChars
	}
	for n > 1 {
		b, _ := json.Marshal(outcomes[:n])
		if len(b) <= maxChars {
			return string(b), n, truncated
		}
		n--
		truncated = true
	}
	b, _ := json.Marshal(outcomes[:1])
	return string(b), 1, len(outcomes) > 1
}

func (t *Tool) runChild(
	ctx context.Context,
	service *cronpkg.Service,
	sessionCtx *types.SessionContext,
	in fanoutInput,
	prompt string,
	index int,
	purpose, llmPurpose, parentRunID string,
	freshContext, ephemeral bool,
) childOutcome {
	out := childOutcome{
		Index:  index,
		Prompt: prompt,
	}
	L_trace("subagent_fanout: child begin", "index", index, "promptLen", len(prompt), "parentRunID", parentRunID)
	requesterID := delegatedrun.BuildRequesterID(sessionCtx.Channel, sessionCtx.ChatID, sessionCtx.User.ID)
	spec := delegatedrun.RunSpec{
		ParentRunID:         parentRunID,
		RequesterType:       "subagent_fanout",
		RequesterID:         requesterID,
		RequesterSessionKey: strings.TrimSpace(sessionCtx.SessionKey),
		SessionKey:          fmt.Sprintf("subagent-fanout:%d:%d", time.Now().UnixNano(), index),
		Prompt:              prompt,
		Purpose:             purpose,
		LLMPurpose:          llmPurpose,
		ResultMode:          "store_only",
		FreshContext:        freshContext,
		Ephemeral:           ephemeral,
		TimeoutSeconds:      in.TimeoutSeconds,
		UserID:              sessionCtx.User.ID,
		EnableThinking:      in.EnableThinking,
		SkipMirror:          true,
		JobName:             "subagent_fanout",
	}

	var runID string
	var err error
	for attempt := 0; attempt < 200; attempt++ {
		if ctx.Err() != nil {
			L_debug("subagent_fanout: child canceled before spawn", "index", index, "attempt", attempt+1, "error", ctx.Err())
			out.Error = ctx.Err().Error()
			out.State = delegatedrun.RunStateCanceled
			return out
		}
		L_trace("subagent_fanout: child spawn attempt", "index", index, "attempt", attempt+1, "parentRunID", parentRunID)
		runID, err = service.StartDelegatedRun(ctx, spec)
		if err == nil {
			L_debug("subagent_fanout: child spawned", "index", index, "runID", runID, "attempt", attempt+1)
			break
		}
		if !isRetryableSpawnError(err) {
			L_warn("subagent_fanout: child spawn failed", "index", index, "attempt", attempt+1, "retryable", false, "error", err)
			out.Error = err.Error()
			return out
		}
		L_debug("subagent_fanout: child spawn retry", "index", index, "attempt", attempt+1, "error", err)
		time.Sleep(25 * time.Millisecond)
	}
	if err != nil {
		L_warn("subagent_fanout: child spawn exhausted retries", "index", index, "error", err)
		out.Error = err.Error()
		return out
	}
	out.RunID = runID

	L_trace("subagent_fanout: child waiting", "index", index, "runID", runID)
	res, state, waitErr := service.WaitDelegatedRun(ctx, runID)
	out.State = state
	if waitErr != nil {
		L_warn("subagent_fanout: child wait failed", "index", index, "runID", runID, "state", state, "error", waitErr)
		out.Error = waitErr.Error()
		return out
	}
	if res.Error != "" {
		L_warn("subagent_fanout: child completed with model error", "index", index, "runID", runID, "state", state, "error", res.Error)
		out.Error = res.Error
	}
	out.Output = strings.TrimSpace(res.FinalText)
	L_debug("subagent_fanout: child completed", "index", index, "runID", runID, "state", state, "resultLen", len(out.Output), "errorPresent", out.Error != "")
	return out
}

func packFanoutPayload(sessionCtx *types.SessionContext, outcomes []childOutcome, ok bool, status, message string, stats fanoutStats, parentRunID string, parallelism int, notifyOnComplete bool, summaryStatus fanoutSummaryStatus, summary *fanoutSynthesisOutcome) fanoutPayload {
	payload := fanoutPayload{
		OK:                 ok,
		Status:             status,
		Message:            message,
		Stats:              stats,
		ParentRunID:        parentRunID,
		Parallelism:        parallelism,
		NotifyOnComplete:   notifyOnComplete,
		ExtraSummaryStatus: summaryStatus,
	}

	availableTokens, bounded := inlineBudgetTokens(sessionCtx)
	if !bounded {
		payload.Items = buildReturnedChildOutcomes(outcomes, len(outcomes))
		payload.Overflow = fanoutOverflow{
			Triggered:         false,
			ReturnedInline:    len(outcomes),
			OmittedFromInline: 0,
		}
		payload.ExtraSummary = summary
		return payload
	}

	includedCount := 0
	baseCandidate := fanoutPayload{
			OK:                 ok,
			Status:             status,
			Message:            message,
		Stats:              stats,
		ParentRunID:        parentRunID,
		Parallelism:        parallelism,
		NotifyOnComplete:   notifyOnComplete,
		Items:              buildReturnedChildOutcomes(outcomes, 0),
		Overflow:           buildOverflowState(outcomes, 0),
		ExtraSummaryStatus: summaryStatus,
		ExtraSummary:       summary,
	}
	if estimatePayloadTokens(baseCandidate) <= availableTokens {
		for next := 1; next <= len(outcomes); next++ {
			candidate := fanoutPayload{
				OK:                 ok,
				Status:             status,
				Message:            message,
				Stats:              stats,
				ParentRunID:        parentRunID,
				Parallelism:        parallelism,
				NotifyOnComplete:   notifyOnComplete,
				Items:              buildReturnedChildOutcomes(outcomes, next),
				Overflow:           buildOverflowState(outcomes, next),
				ExtraSummaryStatus: summaryStatus,
				ExtraSummary:       summary,
			}
			if estimatePayloadTokens(candidate) > availableTokens {
				break
			}
			includedCount = next
		}
	}

	payload.Items = buildReturnedChildOutcomes(outcomes, includedCount)
	payload.Overflow = buildOverflowState(outcomes, includedCount)
	payload.ExtraSummary = summary
	return payload
}

func buildReturnedChildOutcomes(outcomes []childOutcome, includedCount int) []returnedChildOutcome {
	items := make([]returnedChildOutcome, 0, len(outcomes))
	for i, outcome := range outcomes {
		item := returnedChildOutcome{
			Index:  outcome.Index,
			Prompt: outcome.Prompt,
			RunID:  outcome.RunID,
			State:  outcome.State,
			Error:  outcome.Error,
		}
		if i < includedCount {
			item.Output = outcome.Output
		} else if strings.TrimSpace(outcome.Output) != "" {
			item.OutputOmittedReason = "inline_budget"
		}
		items = append(items, item)
	}
	return items
}

func buildOverflowState(outcomes []childOutcome, includedCount int) fanoutOverflow {
	omitted := make([]fanoutOverflowHandle, 0)
	for i := includedCount; i < len(outcomes); i++ {
		outcome := outcomes[i]
		if strings.TrimSpace(outcome.Output) == "" {
			continue
		}
		omitted = append(omitted, fanoutOverflowHandle{
			Index:  outcome.Index,
			Prompt: outcome.Prompt,
			RunID:  outcome.RunID,
			State:  outcome.State,
		})
	}
	if len(omitted) == 0 {
		return fanoutOverflow{
			Triggered:         false,
			ReturnedInline:    includedCount,
			OmittedFromInline: 0,
		}
	}
	return fanoutOverflow{
		Triggered:         true,
		ReturnedInline:    includedCount,
		OmittedFromInline: len(omitted),
		Inspect: &fanoutInspectPath{
			Tool:   "subagent_status",
			Action: "info",
		},
		Omitted: omitted,
		Message: "Some worker outputs were omitted from the inline result because the current session budget could not fit them. Use subagent_status with action=info and the omitted runIds to inspect the missing details.",
	}
}

func inlineBudgetTokens(sessionCtx *types.SessionContext) (int, bool) {
	if sessionCtx == nil || sessionCtx.MaxTokens <= 0 {
		return 0, false
	}
	reserve := sessionCtx.ReserveTokens
	if reserve <= 0 {
		reserve = defaultInlineReserveTokens
	}
	available := sessionCtx.MaxTokens - reserve - sessionCtx.TotalTokens
	if available < 0 {
		available = 0
	}
	return available, true
}

func estimatePayloadTokens(payload fanoutPayload) int {
	b, err := json.Marshal(payload)
	if err != nil {
		return len(fmt.Sprintf("%v", payload)) / 4
	}
	return session.GetTokenEstimator().EstimateTokens(string(b))
}

func trimForDisplay(text string, max int) string {
	text = strings.TrimSpace(text)
	if max <= 0 || len(text) <= max {
		return text
	}
	return text[:max] + "...(truncated)"
}

func isRetryableSpawnError(err error) bool {
	msg := err.Error()
	if strings.Contains(msg, "active child limit reached") {
		return true
	}
	if strings.Contains(msg, "global active delegated run limit reached") {
		return true
	}
	return false
}

func fanoutOutcome(stats fanoutStats) (bool, string, string, delegatedrun.RunState) {
	if stats.Failed == 0 && stats.TimedOut == 0 && stats.Canceled == 0 && stats.SpawnFailed == 0 {
		return true, "completed", "Fanout finished successfully.", delegatedrun.RunStateCompleted
	}
	if stats.Completed == 0 {
		if stats.TimedOut > 0 && stats.Failed == 0 && stats.Canceled == 0 && stats.SpawnFailed == 0 {
			return false, "failed", fmt.Sprintf("Fanout failed: %d worker run(s) timed out.", stats.TimedOut), delegatedrun.RunStateTimeout
		}
		return false, "failed", fmt.Sprintf("Fanout failed: completed=%d failed=%d timedOut=%d canceled=%d spawnFailed=%d.", stats.Completed, stats.Failed, stats.TimedOut, stats.Canceled, stats.SpawnFailed), delegatedrun.RunStateFailed
	}
	return false, "partial_failure", fmt.Sprintf("Fanout finished with failures: completed=%d failed=%d timedOut=%d canceled=%d spawnFailed=%d.", stats.Completed, stats.Failed, stats.TimedOut, stats.Canceled, stats.SpawnFailed), delegatedrun.RunStateFailed
}

