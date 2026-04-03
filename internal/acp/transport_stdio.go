package acp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	osexec "os/exec"
	"strings"
	"sync"
	"time"

	goacp "github.com/ironpark/go-acp"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

type localStdioTransport struct{}

func NewLocalStdioTransport() Transport {
	return &localStdioTransport{}
}

func (t *localStdioTransport) ID() string { return TransportLocalStdio }

type stdioRuntime struct {
	conn   *goacp.ClientSideConnection
	impl   *clientAdapter
	cmd    *osexec.Cmd
	ctx    context.Context
	cancel context.CancelFunc
	mu     sync.Mutex
	closed bool
}

type rawSessionResponse struct {
	SessionID     string                `json:"sessionId"`
	Modes         *rawSessionModeState  `json:"modes,omitempty"`
	ConfigOptions []rawSessionConfigDef `json:"configOptions,omitempty"`
}

type rawSetConfigOptionResponse struct {
	ConfigOptions []rawSessionConfigDef `json:"configOptions,omitempty"`
}

type rawSessionModeState struct {
	CurrentModeID string `json:"currentModeId"`
}

type rawSessionConfigDef struct {
	ID           string          `json:"id"`
	Name         string          `json:"name"`
	Description  string          `json:"description,omitempty"`
	CurrentValue string          `json:"currentValue,omitempty"`
	Options      json.RawMessage `json:"options,omitempty"`
}

type rawSessionConfigOption struct {
	Name        string                   `json:"name"`
	Value       string                   `json:"value"`
	Description string                   `json:"description,omitempty"`
	Options     []rawSessionConfigOption `json:"options,omitempty"`
}

type clientAdapter struct {
	mu            sync.RWMutex
	onEvent       func(ACPEvent)
	onPermission  func(PermissionRequest) (PermissionDecision, error)
	onInteractive func(context.Context, ACPDriverExtensionPayload) (json.RawMessage, error)
	stateMu       sync.Mutex
	tools         map[string]toolState
	seqMu         sync.Mutex
	callbackSeq   int64
}

type toolState struct {
	title       string
	status      string
	kind        string
	input       json.RawMessage
	content     json.RawMessage
	contentText string
	meta        json.RawMessage
	rawOutput   json.RawMessage
	locations   []ToolLocation
}

const acpTracePayloadLimit = 512

type rpcTraceEnvelope struct {
	Method string          `json:"method"`
	ID     json.RawMessage `json:"id"`
}

type stdioLineLogger struct {
	direction string
	mu        sync.Mutex
	buf       strings.Builder
	seq       int64
}

func (c *clientAdapter) setCallbacks(onEvent func(ACPEvent), onPermission func(PermissionRequest) (PermissionDecision, error), onInteractive func(context.Context, ACPDriverExtensionPayload) (json.RawMessage, error)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onEvent = onEvent
	c.onPermission = onPermission
	c.onInteractive = onInteractive
	if c.tools == nil {
		c.tools = map[string]toolState{}
	}
}

func (c *clientAdapter) emit(ev ACPEvent) {
	c.mu.RLock()
	fn := c.onEvent
	c.mu.RUnlock()
	if fn != nil {
		fn(ev)
	}
}

func (c *clientAdapter) nextCallbackSeq() int64 {
	c.seqMu.Lock()
	defer c.seqMu.Unlock()
	c.callbackSeq++
	return c.callbackSeq
}

func (c *clientAdapter) RequestPermission(ctx context.Context, params *goacp.RequestPermissionRequest) (*goacp.RequestPermissionResponse, error) {
	c.mu.RLock()
	fn := c.onPermission
	c.mu.RUnlock()
	if fn == nil {
		return nil, errors.New("no ACP permission handler configured")
	}

	options := make([]PermissionOption, 0, len(params.Options))
	for _, opt := range params.Options {
		options = append(options, PermissionOption{ID: string(opt.OptionID), Label: opt.Name})
	}
	req := PermissionRequest{
		SessionID: string(params.SessionID),
		ToolTitle: params.ToolCall.Title,
		Options:   options,
		Raw:       params,
	}
	decision, err := fn(req)
	if err != nil {
		return nil, err
	}
	decisionID := string(decision)
	for _, opt := range params.Options {
		if string(opt.OptionID) == decisionID {
			return &goacp.RequestPermissionResponse{
				Outcome: goacp.NewRequestPermissionOutcomeSelected(opt.OptionID),
			}, nil
		}
	}
	if len(params.Options) == 0 {
		return nil, fmt.Errorf("permission decision %q not applicable: no options", decisionID)
	}
	return &goacp.RequestPermissionResponse{
		Outcome: goacp.NewRequestPermissionOutcomeSelected(params.Options[0].OptionID),
	}, nil
}

func (c *clientAdapter) toolStartPayload(v goacp.SessionUpdateToolCall) ToolStartPayload {
	state := toolState{
		title:       v.Title,
		status:      toolStatusString(v.Status),
		kind:        toolKindString(v.Kind),
		input:       cloneRawMessage(v.RawInput),
		content:     marshalACPValue(v.Content),
		contentText: toolContentText(v.Content),
		meta:        marshalACPValue(v.Meta),
		rawOutput:   cloneRawMessage(v.RawOutput),
		locations:   toolLocationsFromACP(v.Locations),
	}
	c.storeToolState(string(v.ToolCallID), state)
	return ToolStartPayload{
		ToolCallID: string(v.ToolCallID),
		Title:      state.title,
		Status:     state.status,
		Kind:       state.kind,
		Input:      state.input,
		Content:    state.content,
		Meta:       state.meta,
		RawOutput:  state.rawOutput,
		Locations:  cloneToolLocations(state.locations),
	}
}

func (c *clientAdapter) toolUpdatePayload(v goacp.SessionUpdateToolCallUpdate) ToolUpdatePayload {
	toolCallID := string(v.ToolCallID)
	updated := toolState{
		title:       strings.TrimSpace(v.Title),
		status:      toolStatusString(v.Status),
		kind:        toolKindString(v.Kind),
		input:       cloneRawMessage(v.RawInput),
		content:     marshalACPValue(v.Content),
		contentText: toolContentText(v.Content),
		meta:        marshalACPValue(v.Meta),
		rawOutput:   cloneRawMessage(v.RawOutput),
		locations:   toolLocationsFromACP(v.Locations),
	}
	state := c.mergeToolState(toolCallID, updated)
	return ToolUpdatePayload{
		ToolCallID:   toolCallID,
		Title:        state.title,
		Status:       state.status,
		Kind:         state.kind,
		Content:      state.content,
		ContentText:  state.contentText,
		Meta:         state.meta,
		Input:        state.input,
		RawOutput:    state.rawOutput,
		Locations:    cloneToolLocations(state.locations),
		IsTerminal:   state.status == "completed" || state.status == "failed",
		IsSuccessful: state.status == "completed",
	}
}

func (c *clientAdapter) storeToolState(toolCallID string, state toolState) {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	if c.tools == nil {
		c.tools = map[string]toolState{}
	}
	c.tools[toolCallID] = state
}

func (c *clientAdapter) mergeToolState(toolCallID string, update toolState) toolState {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	if c.tools == nil {
		c.tools = map[string]toolState{}
	}
	state := c.tools[toolCallID]
	if update.title != "" {
		state.title = update.title
	}
	if update.status != "" {
		state.status = update.status
	}
	if update.kind != "" {
		state.kind = update.kind
	}
	if len(update.input) > 0 {
		state.input = update.input
	}
	if len(update.content) > 0 {
		state.content = update.content
	}
	if update.contentText != "" {
		state.contentText = update.contentText
	}
	if len(update.meta) > 0 {
		state.meta = update.meta
	}
	if len(update.rawOutput) > 0 {
		state.rawOutput = update.rawOutput
	}
	if len(update.locations) > 0 {
		state.locations = update.locations
	}
	c.tools[toolCallID] = state
	return state
}

func toolStatusString(status *goacp.ToolCallStatus) string {
	if status == nil {
		return ""
	}
	return string(*status)
}

func summarizeTraceText(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if len(s) <= acpTracePayloadLimit {
		return s
	}
	return s[:acpTracePayloadLimit] + "...(truncated)"
}

func summarizeTraceRaw(raw json.RawMessage) string {
	raw = json.RawMessage(strings.TrimSpace(string(raw)))
	if len(raw) == 0 {
		return ""
	}
	if len(raw) <= acpTracePayloadLimit {
		return string(raw)
	}
	return string(raw[:acpTracePayloadLimit]) + "...(truncated)"
}

func debugRaw(raw json.RawMessage) string {
	raw = json.RawMessage(strings.TrimSpace(string(raw)))
	if len(raw) == 0 {
		return ""
	}
	return string(raw)
}

func decodeRawResponse[T any](raw json.RawMessage) (*T, error) {
	var out T
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func flattenSessionConfigOptions(rawOptions json.RawMessage) []rawSessionConfigOption {
	var options []rawSessionConfigOption
	if len(rawOptions) == 0 {
		return nil
	}
	if err := json.Unmarshal(rawOptions, &options); err != nil {
		return nil
	}
	var flat []rawSessionConfigOption
	var walk func(items []rawSessionConfigOption)
	walk = func(items []rawSessionConfigOption) {
		for _, item := range items {
			if strings.TrimSpace(item.Value) != "" {
				flat = append(flat, rawSessionConfigOption{
					Name:        strings.TrimSpace(item.Name),
					Value:       strings.TrimSpace(item.Value),
					Description: strings.TrimSpace(item.Description),
				})
			}
			if len(item.Options) > 0 {
				walk(item.Options)
			}
		}
	}
	walk(options)
	return flat
}

func extractModelState(configOptions []rawSessionConfigDef) *ACPModelState {
	for _, cfg := range configOptions {
		if strings.TrimSpace(cfg.ID) != "model" {
			continue
		}
		flat := flattenSessionConfigOptions(cfg.Options)
		state := &ACPModelState{
			CurrentValue: strings.TrimSpace(cfg.CurrentValue),
			Options:      make([]ACPModelChoice, 0, len(flat)),
		}
		for _, option := range flat {
			state.Options = append(state.Options, ACPModelChoice{
				Value:       option.Value,
				Name:        option.Name,
				Description: option.Description,
			})
		}
		return state
	}
	return nil
}

func summarizeTraceLine(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	const max = 2048
	if len(s) <= max {
		return s
	}
	return s[:max] + "...(truncated)"
}

func sessionUpdateKind(update *goacp.SessionUpdate) string {
	if update == nil {
		return ""
	}
	return goacp.MatchSessionUpdate(update, goacp.SessionUpdateMatcher[string]{
		AgentMessageChunk:       func(v goacp.SessionUpdateAgentMessageChunk) string { return "agent_message_chunk" },
		AgentThoughtChunk:       func(v goacp.SessionUpdateAgentThoughtChunk) string { return "agent_thought_chunk" },
		ToolCall:                func(v goacp.SessionUpdateToolCall) string { return "tool_call" },
		ToolCallUpdate:          func(v goacp.SessionUpdateToolCallUpdate) string { return "tool_call_update" },
		Plan:                    func(v goacp.SessionUpdatePlan) string { return "plan" },
		CurrentModeUpdate:       func(v goacp.SessionUpdateCurrentModeUpdate) string { return "current_mode_update" },
		SessionInfoUpdate:       func(v goacp.SessionUpdateSessionInfoUpdate) string { return "session_info_update" },
		ConfigOptionUpdate:      func(v goacp.SessionUpdateConfigOptionUpdate) string { return "config_option_update" },
		AvailableCommandsUpdate: func(v goacp.SessionUpdateAvailableCommandsUpdate) string { return "available_commands_update" },
		UserMessageChunk:        func(v goacp.SessionUpdateUserMessageChunk) string { return "user_message_chunk" },
		Default:                 func() string { return "unknown" },
	})
}

func newStdioLineLogger(direction string) *stdioLineLogger {
	return &stdioLineLogger{direction: direction}
}

func (l *stdioLineLogger) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, b := range p {
		if b == '\n' {
			l.flushLocked()
			continue
		}
		_ = l.buf.WriteByte(b)
	}
	return len(p), nil
}

func (l *stdioLineLogger) flushLocked() {
	line := l.buf.String()
	l.buf.Reset()
	if strings.TrimSpace(line) == "" {
		return
	}
	l.seq++
	method := ""
	id := ""
	var env rpcTraceEnvelope
	if err := json.Unmarshal([]byte(line), &env); err == nil {
		method = env.Method
		id = strings.TrimSpace(string(env.ID))
	}
	L_trace("acp: raw stdio",
		"direction", l.direction,
		"seq", l.seq,
		"method", method,
		"id", id,
		"lineLen", len(line),
		"line", summarizeTraceLine(line),
	)
}

func toolKindString(kind *goacp.ToolKind) string {
	if kind == nil {
		return ""
	}
	return string(*kind)
}

func toolContentText(items []goacp.ToolCallContent) string {
	if len(items) == 0 {
		return ""
	}
	var parts []string
	for _, item := range items {
		if cc, ok := item.AsContent(); ok {
			if txt, ok := cc.Content.Content.AsText(); ok {
				parts = append(parts, txt.Text)
			}
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func toolLocationsFromACP(items []goacp.ToolCallLocation) []ToolLocation {
	if len(items) == 0 {
		return nil
	}
	out := make([]ToolLocation, 0, len(items))
	for _, item := range items {
		var line *int64
		if item.Line != nil {
			v := *item.Line
			line = &v
		}
		out = append(out, ToolLocation{Path: item.Path, Line: line})
	}
	return out
}

func cloneToolLocations(items []ToolLocation) []ToolLocation {
	if len(items) == 0 {
		return nil
	}
	out := make([]ToolLocation, 0, len(items))
	for _, item := range items {
		var line *int64
		if item.Line != nil {
			v := *item.Line
			line = &v
		}
		out = append(out, ToolLocation{Path: item.Path, Line: line})
	}
	return out
}

func marshalACPValue(v any) json.RawMessage {
	if v == nil {
		return nil
	}
	data, err := json.Marshal(v)
	if err != nil || len(data) == 0 || string(data) == "null" || string(data) == "{}" || string(data) == "[]" {
		return nil
	}
	return data
}

func cloneRawMessage(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	out := make(json.RawMessage, len(raw))
	copy(out, raw)
	return out
}

func extensionDriver(method string) string {
	method = strings.TrimSpace(method)
	if idx := strings.Index(method, "/"); idx > 0 {
		return method[:idx]
	}
	return ""
}

func extensionSemanticKind(method string) string {
	switch strings.TrimSpace(method) {
	case "cursor/ask_question":
		return "interactive_question"
	case "cursor/create_plan":
		return "interactive_approval"
	case "cursor/update_todos":
		return "progress_checklist"
	case "cursor/task":
		return "delegated_task"
	case "cursor/generate_image":
		return "generated_asset"
	default:
		return "unknown"
	}
}

func extensionInteractive(method string) bool {
	switch strings.TrimSpace(method) {
	case "cursor/ask_question", "cursor/create_plan":
		return true
	default:
		return false
	}
}

func extensionToolCallID(method string, params json.RawMessage) string {
	switch strings.TrimSpace(method) {
	case "cursor/update_todos":
		var payload TodoUpdatePayload
		if err := json.Unmarshal(params, &payload); err == nil {
			return strings.TrimSpace(payload.ToolCallID)
		}
	case "cursor/create_plan":
		var payload PlanRequestPayload
		if err := json.Unmarshal(params, &payload); err == nil {
			return strings.TrimSpace(payload.ToolCallID)
		}
	case "cursor/ask_question":
		var payload QuestionPayload
		if err := json.Unmarshal(params, &payload); err == nil {
			return strings.TrimSpace(payload.ToolCallID)
		}
	case "cursor/task":
		var payload TaskPayload
		if err := json.Unmarshal(params, &payload); err == nil {
			return strings.TrimSpace(payload.ToolCallID)
		}
	case "cursor/generate_image":
		var payload struct {
			ToolCallID string `json:"toolCallId"`
		}
		if err := json.Unmarshal(params, &payload); err == nil {
			return strings.TrimSpace(payload.ToolCallID)
		}
	}
	return ""
}

func extensionSummary(method string, params json.RawMessage) string {
	switch strings.TrimSpace(method) {
	case "cursor/update_todos":
		var payload TodoUpdatePayload
		if err := json.Unmarshal(params, &payload); err == nil {
			return fmt.Sprintf("Cursor updated %d todo(s)", len(payload.Todos))
		}
	case "cursor/create_plan":
		var payload PlanRequestPayload
		if err := json.Unmarshal(params, &payload); err == nil {
			if strings.TrimSpace(payload.Name) != "" {
				return fmt.Sprintf("Cursor requested plan approval: %s", payload.Name)
			}
			return "Cursor requested plan approval"
		}
	case "cursor/ask_question":
		var payload QuestionPayload
		if err := json.Unmarshal(params, &payload); err == nil {
			if len(payload.Questions) > 0 && strings.TrimSpace(payload.Questions[0].Prompt) != "" {
				return "Cursor asked a question: " + strings.TrimSpace(payload.Questions[0].Prompt)
			}
			if strings.TrimSpace(payload.Title) != "" {
				return "Cursor asked a question: " + strings.TrimSpace(payload.Title)
			}
			return "Cursor asked a question"
		}
	case "cursor/task":
		var payload TaskPayload
		if err := json.Unmarshal(params, &payload); err == nil {
			if strings.TrimSpace(payload.Description) != "" {
				return "Cursor task completed: " + strings.TrimSpace(payload.Description)
			}
			return "Cursor task completed"
		}
	case "cursor/generate_image":
		return "Cursor reported generated image output"
	}
	return "ACP driver extension: " + strings.TrimSpace(method)
}

func buildDriverExtensionPayload(method string, params json.RawMessage) ACPDriverExtensionPayload {
	return ACPDriverExtensionPayload{
		Driver:       extensionDriver(method),
		Method:       strings.TrimSpace(method),
		Interactive:  extensionInteractive(method),
		SemanticKind: extensionSemanticKind(method),
		ToolCallID:   extensionToolCallID(method, params),
		Summary:      extensionSummary(method, params),
		Payload:      cloneRawMessage(params),
	}
}

func (c *clientAdapter) SessionUpdate(ctx context.Context, params *goacp.SessionNotification) error {
	ts := time.Now()
	callbackSeq := c.nextCallbackSeq()
	updateJSON := marshalACPValue(params.Update)
	L_trace("acp: session update callback",
		"callbackSeq", callbackSeq,
		"sessionID", string(params.SessionID),
		"kind", sessionUpdateKind(&params.Update),
		"updateBytes", len(updateJSON),
		"update", summarizeTraceRaw(updateJSON),
	)
	goacp.MatchSessionUpdate(&params.Update, goacp.SessionUpdateMatcher[struct{}]{
		AgentMessageChunk: func(v goacp.SessionUpdateAgentMessageChunk) struct{} {
			if text, ok := v.Content.AsText(); ok {
				L_trace("acp: text delta", "callbackSeq", callbackSeq, "deltaLen", len(text.Text), "delta", summarizeTraceText(text.Text))
				c.emit(ACPEvent{Type: EventTextDelta, Payload: TextDeltaPayload{Text: text.Text}, Timestamp: ts})
			}
			return struct{}{}
		},
		AgentThoughtChunk: func(v goacp.SessionUpdateAgentThoughtChunk) struct{} {
			if text, ok := v.Content.AsText(); ok {
				L_trace("acp: thought delta", "callbackSeq", callbackSeq, "deltaLen", len(text.Text), "delta", summarizeTraceText(text.Text))
				c.emit(ACPEvent{Type: EventThought, Payload: ThoughtDeltaPayload{Text: text.Text}, Timestamp: ts})
			}
			return struct{}{}
		},
		ToolCall: func(v goacp.SessionUpdateToolCall) struct{} {
			payload := c.toolStartPayload(v)
			L_debug("acp: tool call started", "callbackSeq", callbackSeq, "toolCallID", payload.ToolCallID, "title", payload.Title, "status", payload.Status)
			L_trace("acp: tool call payload",
				"callbackSeq", callbackSeq,
				"toolCallID", payload.ToolCallID,
				"inputBytes", len(payload.Input),
				"input", summarizeTraceRaw(payload.Input),
				"contentBytes", len(payload.Content),
				"content", summarizeTraceRaw(payload.Content),
				"metaBytes", len(payload.Meta),
				"meta", summarizeTraceRaw(payload.Meta),
				"rawOutputBytes", len(payload.RawOutput),
				"rawOutput", summarizeTraceRaw(payload.RawOutput),
				"locations", len(payload.Locations),
				"kind", payload.Kind,
			)
			c.emit(ACPEvent{Type: EventToolStart, Payload: payload, Timestamp: ts})
			return struct{}{}
		},
		ToolCallUpdate: func(v goacp.SessionUpdateToolCallUpdate) struct{} {
			payload := c.toolUpdatePayload(v)
			L_debug("acp: tool call updated", "callbackSeq", callbackSeq, "toolCallID", payload.ToolCallID, "title", payload.Title, "status", payload.Status, "terminal", payload.IsTerminal)
			L_trace("acp: tool update payload",
				"callbackSeq", callbackSeq,
				"toolCallID", payload.ToolCallID,
				"inputBytes", len(payload.Input),
				"input", summarizeTraceRaw(payload.Input),
				"contentBytes", len(payload.Content),
				"content", summarizeTraceRaw(payload.Content),
				"contentTextLen", len(payload.ContentText),
				"contentText", summarizeTraceText(payload.ContentText),
				"metaBytes", len(payload.Meta),
				"meta", summarizeTraceRaw(payload.Meta),
				"rawOutputBytes", len(payload.RawOutput),
				"rawOutput", summarizeTraceRaw(payload.RawOutput),
				"locations", len(payload.Locations),
				"kind", payload.Kind,
			)
			c.emit(ACPEvent{Type: EventToolUpdate, Payload: payload, Timestamp: ts})
			return struct{}{}
		},
		Plan: func(v goacp.SessionUpdatePlan) struct{} {
			entries := make([]TodoItem, 0, len(v.Entries))
			for i, e := range v.Entries {
				entries = append(entries, TodoItem{
					ID:      fmt.Sprintf("plan-%d", i+1),
					Content: e.Content,
					Status:  string(e.Status),
				})
			}
			c.emit(ACPEvent{Type: EventPlanRequest, Payload: PlanRequestPayload{Todos: entries}, Timestamp: ts})
			return struct{}{}
		},
		CurrentModeUpdate: func(v goacp.SessionUpdateCurrentModeUpdate) struct{} {
			c.emit(ACPEvent{Type: EventStatus, Payload: StatusPayload{Message: fmt.Sprintf("mode:%s", v.CurrentModeID)}, Timestamp: ts})
			return struct{}{}
		},
		SessionInfoUpdate: func(v goacp.SessionUpdateSessionInfoUpdate) struct{} {
			c.emit(ACPEvent{Type: EventStatus, Payload: StatusPayload{Message: fmt.Sprintf("%s %s", v.Title, v.UpdatedAt)}, Timestamp: ts})
			return struct{}{}
		},
		AvailableCommandsUpdate: func(v goacp.SessionUpdateAvailableCommandsUpdate) struct{} {
			c.emit(ACPEvent{Type: EventStatus, Payload: StatusPayload{Message: fmt.Sprintf("commands:%d", len(v.AvailableCommands))}, Timestamp: ts})
			return struct{}{}
		},
		Default: func() struct{} { return struct{}{} },
	})
	return nil
}

func (c *clientAdapter) WriteTextFile(ctx context.Context, params *goacp.WriteTextFileRequest) (*goacp.WriteTextFileResponse, error) {
	return nil, errors.New("fs/write_text_file unsupported in GoClaw ACP MVP")
}

func (c *clientAdapter) ReadTextFile(ctx context.Context, params *goacp.ReadTextFileRequest) (*goacp.ReadTextFileResponse, error) {
	return nil, errors.New("fs/read_text_file unsupported in GoClaw ACP MVP")
}

func (c *clientAdapter) CreateTerminal(ctx context.Context, params *goacp.CreateTerminalRequest) (*goacp.CreateTerminalResponse, error) {
	return nil, errors.New("terminal/create unsupported in GoClaw ACP MVP")
}

func (c *clientAdapter) TerminalOutput(ctx context.Context, params *goacp.TerminalOutputRequest) (*goacp.TerminalOutputResponse, error) {
	return nil, errors.New("terminal/output unsupported in GoClaw ACP MVP")
}

func (c *clientAdapter) ReleaseTerminal(ctx context.Context, params *goacp.ReleaseTerminalRequest) (*goacp.ReleaseTerminalResponse, error) {
	return nil, errors.New("terminal/release unsupported in GoClaw ACP MVP")
}

func (c *clientAdapter) WaitForTerminalExit(ctx context.Context, params *goacp.WaitForTerminalExitRequest) (*goacp.WaitForTerminalExitResponse, error) {
	return nil, errors.New("terminal/wait_for_exit unsupported in GoClaw ACP MVP")
}

func (c *clientAdapter) KillTerminalCommand(ctx context.Context, params *goacp.KillTerminalRequest) (*goacp.KillTerminalResponse, error) {
	return nil, errors.New("terminal/kill unsupported in GoClaw ACP MVP")
}

func (c *clientAdapter) ExtMethod(ctx context.Context, method string, params json.RawMessage) (any, error) {
	now := time.Now()
	payload := buildDriverExtensionPayload(method, params)
	L_debug("acp: driver extension",
		"driver", payload.Driver,
		"method", payload.Method,
		"toolCallID", payload.ToolCallID,
		"interactive", payload.Interactive,
		"semanticKind", payload.SemanticKind,
		"summary", payload.Summary,
		"payload", debugRaw(payload.Payload),
	)
	L_trace("acp: driver extension payload",
		"driver", payload.Driver,
		"method", payload.Method,
		"toolCallID", payload.ToolCallID,
		"interactive", payload.Interactive,
		"semanticKind", payload.SemanticKind,
		"payloadBytes", len(payload.Payload),
		"payload", summarizeTraceRaw(payload.Payload),
	)
	c.emit(ACPEvent{Type: EventDriverExt, Payload: payload, Timestamp: now})
	if !payload.Interactive {
		return map[string]any{}, nil
	}
	c.mu.RLock()
	waitFn := c.onInteractive
	c.mu.RUnlock()
	if waitFn == nil {
		return nil, fmt.Errorf("no ACP interactive handler configured for %s", payload.Method)
	}
	L_debug("acp: waiting for interactive extension response",
		"driver", payload.Driver,
		"method", payload.Method,
		"toolCallID", payload.ToolCallID,
	)
	result, err := waitFn(ctx, payload)
	if err != nil {
		L_debug("acp: interactive extension wait failed",
			"driver", payload.Driver,
			"method", payload.Method,
			"toolCallID", payload.ToolCallID,
			"error", err,
		)
		return nil, err
	}
	L_debug("acp: interactive extension response resolved",
		"driver", payload.Driver,
		"method", payload.Method,
		"toolCallID", payload.ToolCallID,
		"responseBytes", len(result),
		"response", debugRaw(result),
	)
	L_trace("acp: interactive extension response payload",
		"driver", payload.Driver,
		"method", payload.Method,
		"toolCallID", payload.ToolCallID,
		"response", summarizeTraceRaw(result),
	)
	if len(result) == 0 {
		return map[string]any{}, nil
	}
	var decoded any
	if err := json.Unmarshal(result, &decoded); err != nil {
		return nil, fmt.Errorf("invalid interactive response payload for %s: %w", payload.Method, err)
	}
	return decoded, nil
}

func (t *localStdioTransport) newSessionRaw(ctx context.Context, runtime *stdioRuntime, cwd string) (*rawSessionResponse, error) {
	raw, err := runtime.conn.ExtMethod(ctx, goacp.AgentMethods.SessionNew, &goacp.NewSessionRequest{
		Cwd:        cwd,
		MCPServers: []goacp.MCPServer{},
	})
	if err != nil {
		return nil, err
	}
	return decodeRawResponse[rawSessionResponse](raw)
}

func (t *localStdioTransport) loadSessionRaw(ctx context.Context, runtime *stdioRuntime, cwd string, sessionID string) (*rawSessionResponse, error) {
	raw, err := runtime.conn.ExtMethod(ctx, goacp.AgentMethods.SessionLoad, &goacp.LoadSessionRequest{
		Cwd:        cwd,
		MCPServers: []goacp.MCPServer{},
		SessionID:  goacp.SessionID(sessionID),
	})
	if err != nil {
		return nil, err
	}
	return decodeRawResponse[rawSessionResponse](raw)
}

func (t *localStdioTransport) setModelRaw(ctx context.Context, runtime *stdioRuntime, sessionID string, modelValue string) (*ACPModelState, error) {
	raw, err := runtime.conn.ExtMethod(ctx, goacp.AgentMethods.SessionSetConfigOption, &goacp.SetSessionConfigOptionRequest{
		SessionID: goacp.SessionID(sessionID),
		ConfigID:  goacp.SessionConfigID("model"),
		Value:     goacp.SessionConfigValueID(modelValue),
	})
	if err != nil {
		return nil, err
	}
	resp, err := decodeRawResponse[rawSetConfigOptionResponse](raw)
	if err != nil {
		return nil, err
	}
	return extractModelState(resp.ConfigOptions), nil
}

func (t *localStdioTransport) NewSession(ctx context.Context, req NewSessionRequest) (*SessionHandle, error) {
	cwd := strings.TrimSpace(req.CWD)
	if cwd == "" {
		return nil, errors.New("cwd is required for new ACP session")
	}
	runtime, err := t.spawnRuntime(ctx, req.Driver, cwd, req.Mode)
	if err != nil {
		return nil, err
	}
	resp, err := t.newSessionRaw(ctx, runtime, cwd)
	if err != nil {
		_ = t.closeRuntime(ctx, runtime)
		return nil, fmt.Errorf("acp session/new failed: %w", err)
	}
	handle := &SessionHandle{
		SessionID: strings.TrimSpace(resp.SessionID),
		CWD:       cwd,
		Mode:      "",
		Transport: t.ID(),
		Driver:    req.Driver.ID(),
		Models:    extractModelState(resp.ConfigOptions),
		runtime:   runtime,
	}
	if resp.Modes != nil {
		handle.Mode = strings.TrimSpace(resp.Modes.CurrentModeID)
	}
	if req.Mode != "" {
		if err := t.SetMode(ctx, handle, req.Mode); err != nil {
			_ = t.closeRuntime(ctx, runtime)
			return nil, err
		}
	}
	return handle, nil
}

func (t *localStdioTransport) LoadSession(ctx context.Context, req LoadSessionRequest) (*SessionHandle, error) {
	cwd := strings.TrimSpace(req.CWD)
	if cwd == "" {
		return nil, errors.New("cwd is required for load ACP session")
	}
	if strings.TrimSpace(req.SessionID) == "" {
		return nil, errors.New("session ID is required for load ACP session")
	}
	runtime, err := t.spawnRuntime(ctx, req.Driver, cwd, req.Mode)
	if err != nil {
		return nil, err
	}
	resp, err := t.loadSessionRaw(ctx, runtime, cwd, req.SessionID)
	if err != nil {
		_ = t.closeRuntime(ctx, runtime)
		return nil, fmt.Errorf("acp session/load failed: %w", err)
	}
	handle := &SessionHandle{
		SessionID: req.SessionID,
		CWD:       cwd,
		Mode:      "",
		Transport: t.ID(),
		Driver:    req.Driver.ID(),
		Models:    extractModelState(resp.ConfigOptions),
		runtime:   runtime,
	}
	if resp.Modes != nil {
		handle.Mode = strings.TrimSpace(resp.Modes.CurrentModeID)
	}
	if req.Mode != "" {
		if err := t.SetMode(ctx, handle, req.Mode); err != nil {
			_ = t.closeRuntime(ctx, runtime)
			return nil, err
		}
	}
	return handle, nil
}

func (t *localStdioTransport) SetMode(ctx context.Context, handle *SessionHandle, mode string) error {
	runtime, err := requireRuntime(handle)
	if err != nil {
		return err
	}
	mode = strings.TrimSpace(mode)
	if mode == "" {
		return nil
	}
	if _, err := runtime.conn.SetSessionMode(ctx, &goacp.SetSessionModeRequest{
		SessionID: goacp.SessionID(handle.SessionID),
		ModeID:    goacp.SessionModeID(mode),
	}); err != nil {
		return fmt.Errorf("acp session/set_mode failed: %w", err)
	}
	handle.Mode = mode
	return nil
}

func (t *localStdioTransport) SetModel(ctx context.Context, handle *SessionHandle, modelValue string) (*ACPModelState, error) {
	runtime, err := requireRuntime(handle)
	if err != nil {
		return nil, err
	}
	modelValue = strings.TrimSpace(modelValue)
	if modelValue == "" {
		return nil, errors.New("model value is required")
	}
	state, err := t.setModelRaw(ctx, runtime, handle.SessionID, modelValue)
	if err != nil {
		return nil, fmt.Errorf("acp session/set_config_option failed: %w", err)
	}
	if state == nil {
		state = cloneModelState(handle.Models)
		if state == nil {
			state = &ACPModelState{}
		}
		state.CurrentValue = modelValue
	}
	handle.Models = cloneModelState(state)
	return cloneModelState(state), nil
}

func (t *localStdioTransport) ListModels(ctx context.Context, handle *SessionHandle) (*ACPModelState, error) {
	_ = ctx
	if handle == nil {
		return nil, errors.New("acp session handle is required")
	}
	if handle.Models == nil || len(handle.Models.Options) == 0 {
		return nil, errors.New("ACP session does not advertise any model options")
	}
	return cloneModelState(handle.Models), nil
}

func (t *localStdioTransport) Prompt(ctx context.Context, handle *SessionHandle, req PromptRequest) (*PromptResult, error) {
	runtime, err := requireRuntime(handle)
	if err != nil {
		return nil, err
	}
	var finalText strings.Builder
	wrappedEvents := func(ev ACPEvent) {
		switch ev.Type {
		case EventTextDelta:
			if payload, ok := ev.Payload.(TextDeltaPayload); ok {
				finalText.WriteString(payload.Text)
			}
		}
		if req.OnEvent != nil {
			req.OnEvent(ev)
		}
	}
	runtime.impl.setCallbacks(wrappedEvents, req.OnPermission, req.OnInteractive)
	defer runtime.impl.setCallbacks(nil, nil, nil)

	resp, err := runtime.conn.Prompt(ctx, &goacp.PromptRequest{
		SessionID: goacp.SessionID(handle.SessionID),
		Prompt: []goacp.ContentBlock{
			goacp.NewContentBlockText(req.Text),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("acp session/prompt failed: %w", err)
	}
	L_trace("acp: prompt completed",
		"sessionID", handle.SessionID,
		"stopReason", string(resp.StopReason),
		"finalTextLen", len(strings.TrimSpace(finalText.String())),
		"finalText", summarizeTraceText(strings.TrimSpace(finalText.String())),
	)
	return &PromptResult{
		StopReason: string(resp.StopReason),
		FinalText:  strings.TrimSpace(finalText.String()),
	}, nil
}

func (t *localStdioTransport) Cancel(ctx context.Context, handle *SessionHandle) error {
	runtime, err := requireRuntime(handle)
	if err != nil {
		return err
	}
	return runtime.conn.Cancel(ctx, &goacp.CancelNotification{SessionID: goacp.SessionID(handle.SessionID)})
}

func (t *localStdioTransport) Close(ctx context.Context, handle *SessionHandle) error {
	runtime, err := requireRuntime(handle)
	if err != nil {
		return nil
	}
	return t.closeRuntime(ctx, runtime)
}

func requireRuntime(handle *SessionHandle) (*stdioRuntime, error) {
	if handle == nil || handle.runtime == nil {
		return nil, errors.New("acp session handle has no active runtime")
	}
	runtime, ok := handle.runtime.(*stdioRuntime)
	if !ok || runtime == nil {
		return nil, errors.New("invalid ACP runtime handle")
	}
	return runtime, nil
}

func (t *localStdioTransport) spawnRuntime(ctx context.Context, driver Driver, cwd, mode string) (*stdioRuntime, error) {
	if driver == nil {
		return nil, errors.New("driver is required")
	}
	if !driver.SupportsTransport(t.ID()) {
		return nil, fmt.Errorf("driver %q does not support transport %q", driver.ID(), t.ID())
	}
	spec, err := driver.LaunchSpec(ctx, LaunchSpecRequest{CWD: cwd, Mode: mode, Env: map[string]string{}})
	if err != nil {
		return nil, err
	}

	runtimeCtx, runtimeCancel := context.WithCancel(context.Background())
	// #nosec G204 -- command and args come from the trusted ACP driver launch spec.
	cmd := osexec.CommandContext(runtimeCtx, spec.Command, spec.Args...)
	cmd.Dir = cwd
	if len(spec.Env) > 0 {
		cmd.Env = append([]string{}, strings.TrimSpace(strings.Join([]string{}, "")))
		cmd.Env = append(cmd.Env, defaultEnv(spec.Env)...)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		runtimeCancel()
		return nil, fmt.Errorf("failed to create ACP stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		runtimeCancel()
		return nil, fmt.Errorf("failed to create ACP stdout pipe: %w", err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		runtimeCancel()
		return nil, fmt.Errorf("failed to start ACP driver: %w", err)
	}

	impl := &clientAdapter{}
	reader := io.TeeReader(stdout, newStdioLineLogger("recv"))
	writer := io.MultiWriter(stdin, newStdioLineLogger("send"))
	conn := goacp.NewClientSideConnection(impl, writer, reader)
	go func() {
		_ = cmd.Wait()
		_ = conn.Close()
	}()
	go func() {
		_ = conn.Start(runtimeCtx)
	}()
	time.Sleep(50 * time.Millisecond)

	initResp, err := conn.Initialize(ctx, &goacp.InitializeRequest{
		ProtocolVersion: goacp.ProtocolVersion(goacp.CurrentProtocolVersion),
		ClientCapabilities: &goacp.ClientCapabilities{
			FS: &goacp.FileSystemCapabilities{
				ReadTextFile:  false,
				WriteTextFile: false,
			},
			Terminal: false,
		},
	})
	if err != nil {
		_ = conn.Close()
		runtimeCancel()
		return nil, fmt.Errorf("acp initialize failed: %w", err)
	}
	_ = initResp
	if authDriver, ok := driver.(AuthMethodProvider); ok {
		if methodID := strings.TrimSpace(authDriver.AuthMethodID()); methodID != "" {
			if _, err := conn.Authenticate(ctx, &goacp.AuthenticateRequest{MethodID: methodID}); err != nil {
				_ = conn.Close()
				runtimeCancel()
				return nil, fmt.Errorf("acp authenticate failed: %w", err)
			}
		}
	}
	return &stdioRuntime{conn: conn, impl: impl, cmd: cmd, ctx: runtimeCtx, cancel: runtimeCancel}, nil
}

func (t *localStdioTransport) closeRuntime(ctx context.Context, runtime *stdioRuntime) error {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.closed {
		return nil
	}
	runtime.closed = true
	if runtime.cancel != nil {
		runtime.cancel()
	}
	return runtime.conn.Close()
}

func defaultEnv(overrides map[string]string) []string {
	base := map[string]string{}
	for _, kv := range os.Environ() {
		if idx := strings.Index(kv, "="); idx > 0 {
			base[kv[:idx]] = kv[idx+1:]
		}
	}
	for k, v := range overrides {
		base[k] = v
	}
	result := make([]string, 0, len(base))
	for k, v := range base {
		result = append(result, k+"="+v)
	}
	return result
}
