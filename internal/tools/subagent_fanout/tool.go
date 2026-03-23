package subagent_fanout

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	cronpkg "github.com/roelfdiedericks/goclaw/internal/cron"
	"github.com/roelfdiedericks/goclaw/internal/delegatedrun"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/types"
)

const synthesisMaxInputChars = 12000

// Tool runs a bounded parallel fanout across delegated runs and returns a deterministic aggregate.
type Tool struct{}

func NewTool() *Tool { return &Tool{} }

func (t *Tool) Name() string { return "subagent_fanout" }

func (t *Tool) Description() string {
	return "Spawn multiple delegated runs with bounded parallelism and deterministic aggregation. Owner sessions only."
}

func (t *Tool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"prompts": map[string]any{
				"type":        "array",
				"description": "Child prompts to fan out",
				"items": map[string]any{
					"type": "string",
				},
			},
			"purpose": map[string]any{
				"type":        "string",
				"description": "Optional purpose for child runs",
			},
			"parallelism": map[string]any{
				"type":        "integer",
				"description": "Max child runs active concurrently (default 3)",
			},
			"timeoutSeconds": map[string]any{
				"type":        "integer",
				"description": "Optional per-child timeout seconds",
			},
			"freshContext": map[string]any{
				"type":        "boolean",
				"description": "If true, children run with fresh context (default true)",
			},
			"ephemeral": map[string]any{
				"type":        "boolean",
				"description": "If true, child runs skip session persistence (default true)",
			},
			"enableThinking": map[string]any{
				"type":        "boolean",
				"description": "Enable extended thinking for child runs if supported",
			},
			"parentRunId": map[string]any{
				"type":        "string",
				"description": "Optional parent run ID override; defaults to current session run ID",
			},
			"synthesize": map[string]any{
				"type":        "boolean",
				"description": "If true, run an additional model-mediated synthesis pass over deterministic fanout outcomes",
			},
			"synthesisPrompt": map[string]any{
				"type":        "string",
				"description": "Optional synthesis instruction used when synthesize=true",
			},
			"synthesisTimeoutSeconds": map[string]any{
				"type":        "integer",
				"description": "Optional timeout override for synthesis pass only (defaults to timeoutSeconds)",
			},
		},
		"required": []string{"prompts"},
	}
}

type fanoutInput struct {
	Prompts        []string `json:"prompts"`
	Purpose        string   `json:"purpose"`
	Parallelism    int      `json:"parallelism"`
	TimeoutSeconds int      `json:"timeoutSeconds"`
	FreshContext   *bool    `json:"freshContext"`
	Ephemeral      *bool    `json:"ephemeral"`
	EnableThinking bool     `json:"enableThinking"`
	ParentRunID    string   `json:"parentRunId"`
	Synthesize     bool     `json:"synthesize"`
	SynthesisPrompt string  `json:"synthesisPrompt"`
	SynthesisTimeoutSeconds int `json:"synthesisTimeoutSeconds"`
}

type childOutcome struct {
	Index            int                   `json:"index"`
	Prompt           string                `json:"prompt"`
	RunID            string                `json:"runId,omitempty"`
	State            delegatedrun.RunState `json:"state,omitempty"`
	FinalTextPreview string                `json:"finalTextPreview,omitempty"`
	Error            string                `json:"error,omitempty"`
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
	L_info("subagent_fanout: start", "totalPrompts", len(prompts), "parallelism", parallelism, "parentRunID", parentRunID, "synthesize", in.Synthesize, "timeoutSeconds", in.TimeoutSeconds, "purpose", purpose)

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
			outcomes[i] = t.runChild(ctx, service, sessionCtx, in, prompts[i], i, purpose, parentRunID, freshContext, ephemeral)
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

	payload := map[string]any{
		"ok": true,
		"summary": map[string]any{
			"total":       len(outcomes),
			"completed":   completed,
			"failed":      failed,
			"timedOut":    timedOut,
			"canceled":    canceled,
			"spawnFailed": spawnFailed,
		},
		"parentRunId": parentRunID,
		"parallelism": parallelism,
		"items":       outcomes,
	}
	if in.Synthesize {
		payload["synthesis"] = t.runSynthesis(ctx, service, sessionCtx, in, outcomes, parentRunID, freshContext, ephemeral)
	}
	out, _ := json.MarshalIndent(payload, "", "  ")
	L_info("subagent_fanout: completed", "total", len(outcomes), "completed", completed, "failed", failed, "timedOut", timedOut, "canceled", canceled, "spawnFailed", spawnFailed)
	return types.TextResult(string(out)), nil
}

func (t *Tool) runSynthesis(
	ctx context.Context,
	service *cronpkg.Service,
	sessionCtx *types.SessionContext,
	in fanoutInput,
	outcomes []childOutcome,
	parentRunID string,
	freshContext, ephemeral bool,
) map[string]any {
	requesterID := strings.TrimSpace(sessionCtx.Channel + ":" + sessionCtx.ChatID)
	if requesterID == ":" || requesterID == "" {
		requesterID = sessionCtx.User.ID
	}
	synthesisPrompt := strings.TrimSpace(in.SynthesisPrompt)
	if synthesisPrompt == "" {
		synthesisPrompt = "Create a concise synthesis across child outcomes. Include key agreements, disagreements, and recommended next action."
	}
	outcomesJSON, included, truncated := buildBoundedSynthesisOutcomesJSON(outcomes, synthesisMaxInputChars)
	L_debug("subagent_fanout: synthesis input prepared", "totalItems", len(outcomes), "includedItems", included, "truncated", truncated)
	modelPrompt := fmt.Sprintf(
		"Fanout child outcomes JSON:\n%s\n\nMeta: truncated=%t included=%d total=%d\n\nInstruction:\n%s",
		outcomesJSON,
		truncated,
		included,
		len(outcomes),
		synthesisPrompt,
	)

	synthesisTimeout := in.TimeoutSeconds
	if in.SynthesisTimeoutSeconds > 0 {
		synthesisTimeout = in.SynthesisTimeoutSeconds
	}
	L_info("subagent_fanout: synthesis start", "parentRunID", parentRunID, "includedItems", included, "totalItems", len(outcomes), "truncated", truncated, "timeoutSeconds", synthesisTimeout)

	spec := delegatedrun.RunSpec{
		ParentRunID:         parentRunID,
		RequesterType:       "subagent_fanout",
		RequesterID:         requesterID,
		RequesterSessionKey: strings.TrimSpace(sessionCtx.SessionKey),
		SessionKey:          fmt.Sprintf("subagent-fanout-synthesis:%d", time.Now().UnixNano()),
		Prompt:              modelPrompt,
		Purpose:             "subagent_fanout_synthesis",
		ResultMode:          "store_only",
		FreshContext:        freshContext,
		Ephemeral:           ephemeral,
		TimeoutSeconds:      synthesisTimeout,
		UserID:              sessionCtx.User.ID,
		EnableThinking:      in.EnableThinking,
		SkipMirror:          true,
		JobName:             "subagent_fanout",
	}
	runID, err := service.StartDelegatedRun(ctx, spec)
	if err != nil {
		L_warn("subagent_fanout: synthesis spawn failed", "parentRunID", parentRunID, "error", err)
		return map[string]any{
			"ok":    false,
			"error": err.Error(),
		}
	}
	L_debug("subagent_fanout: synthesis spawned", "runID", runID, "parentRunID", parentRunID)
	result, state, waitErr := service.WaitDelegatedRun(ctx, runID)
	resp := map[string]any{
		"ok":    waitErr == nil,
		"runId": runID,
		"state": state,
		"text":  strings.TrimSpace(result.FinalText),
		"truncated": truncated,
		"includedItems": included,
		"totalItems": len(outcomes),
	}
	if waitErr != nil {
		L_warn("subagent_fanout: synthesis wait failed", "runID", runID, "state", state, "error", waitErr)
		resp["error"] = waitErr.Error()
	}
	if result.Error != "" {
		L_warn("subagent_fanout: synthesis model error", "runID", runID, "state", state, "error", result.Error)
		resp["modelError"] = result.Error
	}
	if waitErr == nil && result.Error == "" {
		L_info("subagent_fanout: synthesis completed", "runID", runID, "state", state, "textLen", len(strings.TrimSpace(result.FinalText)))
	}
	return resp
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
	purpose, parentRunID string,
	freshContext, ephemeral bool,
) childOutcome {
	out := childOutcome{
		Index:  index,
		Prompt: prompt,
	}
	L_trace("subagent_fanout: child begin", "index", index, "promptLen", len(prompt), "parentRunID", parentRunID)
	requesterID := strings.TrimSpace(sessionCtx.Channel + ":" + sessionCtx.ChatID)
	if requesterID == ":" || requesterID == "" {
		requesterID = sessionCtx.User.ID
	}
	spec := delegatedrun.RunSpec{
		ParentRunID:         parentRunID,
		RequesterType:       "subagent_fanout",
		RequesterID:         requesterID,
		RequesterSessionKey: strings.TrimSpace(sessionCtx.SessionKey),
		SessionKey:          fmt.Sprintf("subagent-fanout:%d:%d", time.Now().UnixNano(), index),
		Prompt:              prompt,
		Purpose:             purpose,
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
	final := strings.TrimSpace(res.FinalText)
	if len(final) > 400 {
		final = final[:400] + "...(truncated)"
	}
	out.FinalTextPreview = final
	L_debug("subagent_fanout: child completed", "index", index, "runID", runID, "state", state, "resultLen", len(final), "errorPresent", out.Error != "")
	return out
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

