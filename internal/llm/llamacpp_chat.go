package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/metadata"
	. "github.com/roelfdiedericks/goclaw/internal/metrics"
	"github.com/roelfdiedericks/goclaw/internal/tokens"
	"github.com/roelfdiedericks/goclaw/internal/types"
	openai "github.com/sashabaranov/go-openai"
)

func (p *LlamaCppProvider) applyLlamaCppChatTransport(ctx context.Context) {
	if p.chatAugment == nil || p.embeddingOnly || p.serverRoot == "" {
		return
	}

	_, totalSlots := p.fetchCachedProps(context.Background())
	if totalSlots < 1 {
		totalSlots = 1
	}
	globalLlamaSlotManager.SyncCapacity(p.serverRoot, totalSlots)

	sessionKey := SlotOwnerFromContext(ctx)
	if sessionKey == "" {
		p.slotMu.Lock()
		p.slotPersisted = -1
		p.slotMu.Unlock()
		p.chatAugment.SetLlamaCppChatAugment(true, -1)
		L_debug("llamacpp: chat augment (unpinned, no session key)", "provider", p.name)
		return
	}

	ownerKey := sessionKey + "|" + p.name + "|" + p.model

	p.slotMu.Lock()
	if p.slotPersisted >= totalSlots {
		globalLlamaSlotManager.Release(p.serverRoot, ownerKey)
		p.slotPersisted = -1
	}
	preferred := -1
	if p.slotPersisted >= 0 && p.slotPersisted < totalSlots {
		preferred = p.slotPersisted
	}
	p.slotMu.Unlock()

	id, pinned := globalLlamaSlotManager.Acquire(p.serverRoot, ownerKey, preferred)
	if !pinned {
		p.slotMu.Lock()
		p.slotPersisted = -1
		p.slotMu.Unlock()
		p.chatAugment.SetLlamaCppChatAugment(true, -1)
		L_debug("llamacpp: slot lease unavailable, unpinned chat request",
			"provider", p.name,
			"serverRoot", p.serverRoot,
			"ownerKey", ownerKey,
			"capacity", totalSlots,
		)
		return
	}

	p.slotMu.Lock()
	p.slotPersisted = id
	p.slotMu.Unlock()
	p.chatAugment.SetLlamaCppChatAugment(true, id)
	L_debug("llamacpp: chat augment pinned", "provider", p.name, "slot", id, "ownerKey", ownerKey)
}

func (p *LlamaCppProvider) StreamMessage(
	ctx context.Context,
	messages []types.Message,
	toolDefs []types.ToolDefinition,
	systemPrompt string,
	onDelta func(delta string),
	opts *StreamOptions,
) (*Response, error) {
	startTime := time.Now()
	contextWindow := p.ContextTokens()

	p.applyLlamaCppChatTransport(ctx)

	enableThinking := false
	var thinkingLevel ThinkingLevel
	var onThinkingDelta func(string)
	if opts != nil {
		thinkingLevel = ThinkingLevel(opts.ThinkingLevel)
		if thinkingLevel == "" && opts.EnableThinking {
			thinkingLevel = DefaultThinkingLevel
		}
		enableThinking = thinkingLevel.IsEnabled()
		onThinkingDelta = opts.OnThinkingDelta
	}

	if enableThinking && p.transport != nil {
		effort := thinkingLevel.OpenRouterEffort()
		if effort != "" {
			p.transport.SetReasoningEffort(effort)
			L_debug("llamacpp: set reasoning effort", "provider", p.name, "level", thinkingLevel, "effort", effort)
		}
	}

	var reasoningParser *SSEReasoningParser
	if enableThinking && p.transport != nil {
		reasoningParser = NewSSEReasoningParser(onThinkingDelta)
		p.transport.SetOnChunk(reasoningParser.ProcessChunk)
	}

	L_info("llm: request started", "provider", p.name, "model", p.model, "messages", len(messages), "tools", len(toolDefs), "thinking", enableThinking, "thinkingLevel", thinkingLevel)
	L_debug("preparing llama.cpp request", "messages", len(messages), "tools", len(toolDefs))

	openaiMessages, repairStats := convertToOpenAIMessages(messages)
	if repairStats.modified {
		L_debug("repaired tool pairing for llama.cpp",
			"droppedOrphans", repairStats.droppedOrphans,
			"mergedToolCalls", repairStats.mergedToolCalls)
	}

	if systemPrompt != "" {
		openaiMessages = append([]openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleSystem, Content: systemPrompt},
		}, openaiMessages...)
		p.trace("system prompt set", "length", len(systemPrompt))
	}

	openaiTools := convertToOpenAITools(toolDefs)
	configuredMax := p.MaxTokens()
	isReasoningModel := p.metadataProvider != "" && metadata.Get().SupportsReasoning(p.metadataProvider, p.model)

	req := openai.ChatCompletionRequest{
		Model:    p.model,
		Messages: openaiMessages,
		Stream:   true,
		StreamOptions: &openai.StreamOptions{
			IncludeUsage: true,
		},
	}

	if isReasoningModel {
		req.MaxCompletionTokens = configuredMax
	} else {
		req.MaxTokens = configuredMax
	}

	if len(openaiTools) > 0 {
		req.Tools = openaiTools
		var toolNames []string
		for _, t := range openaiTools {
			if t.Function != nil {
				toolNames = append(toolNames, t.Function.Name)
			}
		}
		p.trace("tools attached", "count", len(openaiTools), "names", toolNames)
	}

	reqBytes, _ := json.Marshal(req)
	reqSizeKB := len(reqBytes) / 1024
	estimatedInput := tokens.Get().Count(string(reqBytes))

	maxTokens := tokens.CapMaxTokens(configuredMax, contextWindow, estimatedInput, 100)
	if maxTokens != configuredMax {
		L_debug("llamacpp: capped max_tokens to fit context",
			"provider", p.name,
			"original", configuredMax,
			"capped", maxTokens,
			"contextWindow", contextWindow,
			"estimatedInput", estimatedInput)
		if isReasoningModel {
			req.MaxCompletionTokens = maxTokens
		} else {
			req.MaxTokens = maxTokens
		}
	}

	if cachedLimit, ok := modelMaxOutputTokens.Load(p.model); ok {
		limit := cachedLimit.(int) //nolint:errcheck // only int values stored
		if maxTokens > limit {
			L_debug("llamacpp: capping max_tokens to cached model limit",
				"model", p.model,
				"requested", maxTokens,
				"limit", limit)
			maxTokens = limit
			if isReasoningModel {
				req.MaxCompletionTokens = maxTokens
			} else {
				req.MaxTokens = maxTokens
			}
		}
	}

	L_info("llm: request size",
		"provider", p.name,
		"model", p.model,
		"messages", len(openaiMessages),
		"tools", len(openaiTools),
		"sizeKB", reqSizeKB,
		"estimatedTokens", estimatedInput,
	)

	p.trace("llamacpp: sending request",
		"provider", p.name,
		"model", p.model,
		"baseURL", p.baseURL,
		"maxTokens", maxTokens,
		"messageCount", len(openaiMessages),
		"toolCount", len(openaiTools),
		"requestSizeKB", reqSizeKB,
	)

	for i, msg := range openaiMessages {
		if i >= 5 {
			p.trace("llamacpp: request messages truncated", "shown", 5, "total", len(openaiMessages))
			break
		}
		contentLen := len(msg.Content)
		if len(msg.MultiContent) > 0 {
			contentLen = len(msg.MultiContent)
		}
		p.trace("llamacpp: request message",
			"idx", i,
			"role", msg.Role,
			"contentLen", contentLen,
			"toolCallsCount", len(msg.ToolCalls),
			"toolCallID", msg.ToolCallID,
		)
	}

	dumpCtx := StartDump(p.name, p.model, p.baseURL, openaiMessages, openaiTools, systemPrompt, 1)
	dumpCtx.SetTokenInfo(TokenInfo{
		ContextWindow:  contextWindow,
		EstimatedInput: estimatedInput,
		ConfiguredMax:  configuredMax,
		CappedMax:      maxTokens,
		SafetyMargin:   tokens.SafetyMargin,
		Buffer:         100,
	})

	reqCapture := NewRequestCapture()
	dumpCtx.SetRequestCapture(reqCapture)
	captureCtx := WithRequestCapture(ctx, reqCapture)

	stream, err := p.client.CreateChatCompletionStream(captureCtx, req)
	if err != nil {
		errStr := err.Error()
		if isMaxTokens, parsedLimit := ParseMaxTokensLimit(errStr); isMaxTokens && parsedLimit > 0 {
			L_warn("llamacpp: max_tokens exceeds model limit, caching and retrying",
				"model", p.model,
				"requestedTokens", maxTokens,
				"modelLimit", parsedLimit)
			modelMaxOutputTokens.Store(p.model, parsedLimit)
			return p.StreamMessage(ctx, messages, toolDefs, systemPrompt, onDelta, opts)
		}

		L_error("stream creation failed - request details",
			"provider", p.name,
			"baseURL", p.baseURL,
			"model", p.model,
			"maxTokens", configuredMax,
			"messageCount", len(openaiMessages),
			"toolCount", len(openaiTools),
			"requestSizeKB", reqSizeKB,
			"stream", req.Stream,
		)

		roleCounts := make(map[string]int)
		for _, msg := range openaiMessages {
			roleCounts[string(msg.Role)]++
		}
		L_error("stream creation failed - message roles", "roles", roleCounts)

		var apiErr *openai.APIError
		var reqErr *openai.RequestError
		if errors.As(err, &apiErr) {
			if isMaxTokens, parsedLimit := ParseMaxTokensLimit(apiErr.Message); isMaxTokens && parsedLimit > 0 {
				L_warn("llamacpp: max_tokens exceeds model limit (APIError), caching and retrying",
					"model", p.model,
					"requestedTokens", maxTokens,
					"modelLimit", parsedLimit)
				modelMaxOutputTokens.Store(p.model, parsedLimit)
				return p.StreamMessage(ctx, messages, toolDefs, systemPrompt, onDelta, opts)
			}
			L_error("stream creation failed (APIError)",
				"provider", p.name,
				"model", p.model,
				"statusCode", apiErr.HTTPStatusCode,
				"status", apiErr.HTTPStatus,
				"code", apiErr.Code,
				"message", apiErr.Message,
				"type", apiErr.Type,
				"param", apiErr.Param,
			)
		} else if errors.As(err, &reqErr) {
			L_error("stream creation failed (RequestError)",
				"provider", p.name,
				"model", p.model,
				"statusCode", reqErr.HTTPStatusCode,
				"status", reqErr.HTTPStatus,
				"error", reqErr.Error(),
			)
		} else {
			L_error("stream creation failed", "provider", p.name, "model", p.model, "error", err)
		}

		FinishDumpError(dumpCtx, err, p.transport)
		if reqCapture != nil {
			_, respBody, _, _ := reqCapture.GetData()
			err = CheckResponseBody(err, respBody)
		}
		if p.metricPrefix != "" {
			MetricDuration(p.metricPrefix, "request", time.Since(startTime))
			MetricFailWithReason(p.metricPrefix, "request_status", "stream_creation_error")
		}
		if reasoningParser != nil && p.transport != nil {
			p.transport.SetOnChunk(nil)
		}
		return nil, fmt.Errorf("stream error: %w", err)
	}
	defer stream.Close()

	response := &Response{}
	var toolCalls []openai.ToolCall
	var reasoningContent string
	chunkNum := 0

	const noContentWarnInterval = 60 * time.Second
	lastContentTime := time.Now()
	lastWarnTime := time.Time{}
	emptyChunkCount := 0
	firstContentLogged := false
	toolCallsStarted := make(map[int]bool)

	for {
		chunk, err := stream.Recv()
		if err != nil {
			if err.Error() == "EOF" {
				p.trace("llamacpp: stream complete",
					"provider", p.name,
					"totalChunks", chunkNum,
					"duration", time.Since(startTime).Round(time.Millisecond),
					"textLen", len(response.Text),
					"toolCallsCount", len(toolCalls),
				)
				break
			}

			errStr := err.Error()
			if isMaxTokens, parsedLimit := ParseMaxTokensLimit(errStr); isMaxTokens && parsedLimit > 0 {
				L_warn("llamacpp: max_tokens exceeds model limit (stream), caching and retrying",
					"model", p.model,
					"requestedTokens", maxTokens,
					"modelLimit", parsedLimit)
				modelMaxOutputTokens.Store(p.model, parsedLimit)
				stream.Close()
				return p.StreamMessage(ctx, messages, toolDefs, systemPrompt, onDelta, opts)
			}

			var apiErr *openai.APIError
			var reqErr *openai.RequestError
			if errors.As(err, &apiErr) {
				if isMaxTokens, parsedLimit := ParseMaxTokensLimit(apiErr.Message); isMaxTokens && parsedLimit > 0 {
					L_warn("llamacpp: max_tokens exceeds model limit (stream APIError), caching and retrying",
						"model", p.model,
						"requestedTokens", maxTokens,
						"modelLimit", parsedLimit)
					modelMaxOutputTokens.Store(p.model, parsedLimit)
					stream.Close()
					return p.StreamMessage(ctx, messages, toolDefs, systemPrompt, onDelta, opts)
				}
				L_error("stream recv failed (APIError)",
					"provider", p.name,
					"model", p.model,
					"statusCode", apiErr.HTTPStatusCode,
					"code", apiErr.Code,
					"message", apiErr.Message,
					"type", apiErr.Type,
				)
			} else if errors.As(err, &reqErr) {
				L_error("stream recv failed (RequestError)",
					"provider", p.name,
					"model", p.model,
					"statusCode", reqErr.HTTPStatusCode,
					"error", reqErr.Error(),
				)
			} else {
				L_error("stream recv failed",
					"provider", p.name,
					"model", p.model,
					"error", err,
					"errorType", fmt.Sprintf("%T", err),
				)
			}

			FinishDumpError(dumpCtx, err, p.transport)
			if reqCapture != nil {
				_, respBody, _, _ := reqCapture.GetData()
				err = CheckResponseBody(err, respBody)
			}
			if p.metricPrefix != "" {
				MetricDuration(p.metricPrefix, "request", time.Since(startTime))
				MetricFailWithReason(p.metricPrefix, "request_status", "stream_error")
			}
			if reasoningParser != nil && p.transport != nil {
				p.transport.SetOnChunk(nil)
			}
			return nil, fmt.Errorf("stream error: %w", err)
		}

		chunkNum++
		if len(chunk.Choices) == 0 {
			emptyChunkCount++
			timeSinceContent := time.Since(lastContentTime)
			if timeSinceContent >= noContentWarnInterval && time.Since(lastWarnTime) >= noContentWarnInterval {
				L_warn("llamacpp: waiting for content",
					"provider", p.name,
					"noContentFor", timeSinceContent.Round(time.Second),
					"elapsed", time.Since(startTime).Round(time.Second),
					"emptyChunks", emptyChunkCount,
				)
				lastWarnTime = time.Now()
			}
			continue
		}

		choice := chunk.Choices[0]
		hasContent := choice.Delta.Content != ""
		hasReasoning := choice.Delta.ReasoningContent != ""
		hasToolCalls := len(choice.Delta.ToolCalls) > 0
		hasFinishReason := choice.FinishReason != ""
		isEmptyChunk := !hasContent && !hasReasoning && !hasToolCalls && !hasFinishReason
		if isEmptyChunk {
			emptyChunkCount++
			timeSinceContent := time.Since(lastContentTime)
			if timeSinceContent >= noContentWarnInterval && time.Since(lastWarnTime) >= noContentWarnInterval {
				L_warn("llamacpp: waiting for content",
					"provider", p.name,
					"noContentFor", timeSinceContent.Round(time.Second),
					"elapsed", time.Since(startTime).Round(time.Second),
					"emptyChunks", emptyChunkCount,
				)
				lastWarnTime = time.Now()
			}
			continue
		}

		if emptyChunkCount > 0 {
			p.trace("llamacpp: content after waiting",
				"provider", p.name,
				"waitedFor", time.Since(lastContentTime).Round(time.Millisecond),
				"emptyChunks", emptyChunkCount,
			)
			emptyChunkCount = 0
		}
		lastContentTime = time.Now()

		if hasReasoning {
			reasoningContent += choice.Delta.ReasoningContent
		}
		if hasContent {
			if !firstContentLogged {
				preview := choice.Delta.Content
				if len(preview) > 50 {
					preview = preview[:50] + "..."
				}
				p.trace("llamacpp: first content received",
					"provider", p.name,
					"chunk", chunkNum,
					"preview", preview,
				)
				firstContentLogged = true
			}
			response.Text += choice.Delta.Content
			if onDelta != nil {
				onDelta(choice.Delta.Content)
			}
		}

		for _, tc := range choice.Delta.ToolCalls {
			idx := 0
			if tc.Index != nil {
				idx = *tc.Index
			}
			for len(toolCalls) <= idx {
				toolCalls = append(toolCalls, openai.ToolCall{})
			}
			if !toolCallsStarted[idx] && (tc.ID != "" || tc.Function.Name != "") {
				toolCallsStarted[idx] = true
				p.trace("llamacpp: tool call started",
					"provider", p.name,
					"chunk", chunkNum,
					"idx", idx,
					"id", tc.ID,
					"name", tc.Function.Name,
				)
			}
			if tc.ID != "" {
				toolCalls[idx].ID = tc.ID
			}
			if tc.Type != "" {
				toolCalls[idx].Type = tc.Type
			}
			if tc.Function.Name != "" {
				toolCalls[idx].Function.Name += tc.Function.Name
			}
			if tc.Function.Arguments != "" {
				toolCalls[idx].Function.Arguments += tc.Function.Arguments
			}
		}

		if hasFinishReason {
			response.StopReason = string(choice.FinishReason)
			p.trace("llamacpp: finish_reason received",
				"provider", p.name,
				"chunk", chunkNum,
				"finishReason", choice.FinishReason,
				"toolCallsCount", len(toolCalls),
				"textLen", len(response.Text),
			)
		}

		if chunk.Usage != nil {
			response.InputTokens = chunk.Usage.PromptTokens
			response.OutputTokens = chunk.Usage.CompletionTokens
			if chunk.Usage.PromptTokensDetails != nil {
				response.CacheReadTokens = chunk.Usage.PromptTokensDetails.CachedTokens
			}
			if chunk.Usage.CompletionTokensDetails != nil {
				response.ReasoningTokens = chunk.Usage.CompletionTokensDetails.ReasoningTokens
			}
		}
	}

	for i, tc := range toolCalls {
		if tc.ID != "" {
			p.trace("llamacpp: tool call complete",
				"provider", p.name,
				"idx", i,
				"id", tc.ID,
				"name", tc.Function.Name,
				"argsLen", len(tc.Function.Arguments),
			)
		}
	}

	if reasoningContent != "" {
		response.Thinking = reasoningContent
		L_info("llm: reasoning content captured (native)", "length", len(reasoningContent))
	}
	if reasoningParser != nil {
		parsedReasoning := reasoningParser.GetReasoning()
		if parsedReasoning != "" {
			if response.Thinking != "" {
				response.Thinking += "\n\n--- reasoning_details ---\n" + parsedReasoning
			} else {
				response.Thinking = parsedReasoning
			}
			L_info("llm: reasoning_details captured (SSE)", "length", len(parsedReasoning))
		}
		if p.transport != nil {
			p.transport.SetOnChunk(nil)
		}
	}

	if len(toolCalls) > 0 && toolCalls[0].ID != "" {
		response.StopReason = "tool_use"
		for _, tc := range toolCalls {
			if tc.ID != "" {
				response.ToolCalls = append(response.ToolCalls, ToolCallInfo{
					ID:    tc.ID,
					Name:  tc.Function.Name,
					Input: json.RawMessage(tc.Function.Arguments),
				})
			}
		}
		L_info("llm: tool use detected", "provider", p.name, "totalCalls", len(response.ToolCalls))
	} else if len(toolCalls) > 0 {
		L_warn("llamacpp: tool_calls present but first ID empty",
			"provider", p.name,
			"count", len(toolCalls),
			"firstID", toolCalls[0].ID,
			"firstName", toolCalls[0].Function.Name,
			"firstArgs", toolCalls[0].Function.Arguments,
		)
	}

	if response.InputTokens == 0 {
		response.InputTokens = estimateOpenAITokens(openaiMessages, systemPrompt)
		L_debug("llm: estimated input tokens (API didn't provide)", "estimated", response.InputTokens)
	}
	if response.OutputTokens == 0 && response.Text != "" {
		response.OutputTokens = len(response.Text) / 4
	}

	elapsed := time.Since(startTime)
	usagePercent := 0.0
	if contextWindow > 0 {
		usagePercent = float64(response.InputTokens) / float64(contextWindow) * 100.0
	}
	L_info("llm: request completed", "provider", p.name, "duration", elapsed.Round(time.Millisecond),
		"inputTokens", response.InputTokens, "outputTokens", response.OutputTokens)

	p.trace("llamacpp: response summary",
		"provider", p.name,
		"textLen", len(response.Text),
		"stopReason", response.StopReason,
		"toolCalls", len(response.ToolCalls),
		"thinkingLen", len(response.Thinking),
		"hasToolUse", response.HasToolUse(),
	)

	if p.metricPrefix != "" {
		MetricDuration(p.metricPrefix, "request", elapsed)
		MetricAdd(p.metricPrefix, "input_tokens", int64(response.InputTokens))
		MetricAdd(p.metricPrefix, "output_tokens", int64(response.OutputTokens))
		if response.CacheReadTokens > 0 {
			MetricAdd(p.metricPrefix, "cache_read_tokens", int64(response.CacheReadTokens))
		}
		if response.ReasoningTokens > 0 {
			MetricAdd(p.metricPrefix, "reasoning_tokens", int64(response.ReasoningTokens))
		}
		MetricOutcome(p.metricPrefix, "stop_reason", response.StopReason)
		MetricSuccess(p.metricPrefix, "request_status")
		if contextWindow > 0 {
			MetricSet(p.metricPrefix, "context_window", int64(contextWindow))
			MetricSet(p.metricPrefix, "context_used", int64(response.InputTokens))
			MetricThreshold(p.metricPrefix, "context_usage_percent", usagePercent, 100.0)
		}
		EmitCostMetrics(p.metricPrefix, PurposeFromContext(ctx), p.config, p.metadataProvider, p.model, response)
	}

	FinishDumpSuccess(dumpCtx, p.dumpOnSuccess)
	return response, nil
}
