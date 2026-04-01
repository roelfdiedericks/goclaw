package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	osexec "os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	acp "github.com/ironpark/go-acp"
)

type spikeClient struct {
	permissionMode string
	extOutDir      string
	askResponse    string
	askJSON        string
	planResponse   string
	planJSON       string

	extMu           sync.Mutex
	extObservations []extObservation
}

type extObservation struct {
	Index        int             `json:"index"`
	Method       string          `json:"method"`
	ObservedAt   string          `json:"observedAt"`
	TopLevelKeys []string        `json:"topLevelKeys,omitempty"`
	SchemaPaths  []string        `json:"schemaPaths,omitempty"`
	Params       json.RawMessage `json:"params"`
}

type extMethodSummary struct {
	Method       string   `json:"method"`
	Count        int      `json:"count"`
	TopLevelKeys []string `json:"topLevelKeys,omitempty"`
	SchemaPaths  []string `json:"schemaPaths,omitempty"`
}

type questionSelection struct {
	AllowMultiple bool
	OptionIDs     []string
}

func parseQuestionSelection(params json.RawMessage) questionSelection {
	var payload struct {
		Questions []struct {
			AllowMultiple bool `json:"allowMultiple"`
			Options []struct {
				ID string `json:"id"`
			} `json:"options"`
		} `json:"questions"`
	}
	if err := json.Unmarshal(params, &payload); err != nil {
		return questionSelection{}
	}
	if len(payload.Questions) == 0 {
		return questionSelection{}
	}
	result := questionSelection{AllowMultiple: payload.Questions[0].AllowMultiple}
	for _, opt := range payload.Questions[0].Options {
		if id := strings.TrimSpace(opt.ID); id != "" {
			result.OptionIDs = append(result.OptionIDs, id)
		}
	}
	return result
}

func decodeRawJSONResult(raw string) (any, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return map[string]any{}, nil
	}
	var decoded any
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		return nil, err
	}
	return decoded, nil
}

func (c *spikeClient) askQuestionResult(params json.RawMessage) (any, error) {
	if strings.TrimSpace(c.askJSON) != "" {
		return decodeRawJSONResult(c.askJSON)
	}
	mode := strings.TrimSpace(c.askResponse)
	if mode == "" {
		mode = "selected-id-text"
	}
	selection := parseQuestionSelection(params)
	firstID := ""
	if len(selection.OptionIDs) > 0 {
		firstID = selection.OptionIDs[0]
	}
	firstTwoIDs := selection.OptionIDs
	if len(firstTwoIDs) > 2 {
		firstTwoIDs = firstTwoIDs[:2]
	}
	switch mode {
	case "empty":
		return map[string]any{}, nil
	case "selected-id":
		if firstID == "" {
			firstID = "option-1"
		}
		return map[string]any{"selectedId": firstID}, nil
	case "selected-id-text":
		if firstID == "" {
			firstID = "option-1"
		}
		return map[string]any{
			"selectedId": firstID,
			"text":       "Selected by goacp probe",
		}, nil
	case "selected-ids":
		if len(firstTwoIDs) == 0 {
			firstTwoIDs = []string{"option-1", "option-2"}
		}
		return map[string]any{"selectedIds": firstTwoIDs}, nil
	case "selected-option-ids":
		if len(firstTwoIDs) == 0 {
			firstTwoIDs = []string{"option-1", "option-2"}
		}
		return map[string]any{"selectedOptionIds": firstTwoIDs}, nil
	case "selected-ids-text":
		if len(firstTwoIDs) == 0 {
			firstTwoIDs = []string{"option-1", "option-2"}
		}
		return map[string]any{
			"selectedIds": firstTwoIDs,
			"text":        "Selected by goacp probe",
		}, nil
	case "answered":
		if firstID == "" {
			firstID = "option-1"
		}
		return map[string]any{
			"outcome": "answered",
			"answer":  firstID,
		}, nil
	case "cancelled":
		return map[string]any{"outcome": "cancelled"}, nil
	default:
		return nil, fmt.Errorf("unknown ask question response mode %q", mode)
	}
}

func (c *spikeClient) createPlanResult() (any, error) {
	if strings.TrimSpace(c.planJSON) != "" {
		return decodeRawJSONResult(c.planJSON)
	}
	mode := strings.TrimSpace(c.planResponse)
	if mode == "" {
		mode = "approved-feedback"
	}
	switch mode {
	case "empty":
		return map[string]any{}, nil
	case "approved":
		return map[string]any{"approved": true}, nil
	case "approved-feedback":
		return map[string]any{
			"approved":     true,
			"userFeedback": "Looks good, proceed.",
		}, nil
	case "rejected":
		return map[string]any{"approved": false}, nil
	case "rejected-feedback":
		return map[string]any{
			"approved":     false,
			"userFeedback": "Rejected by goacp probe.",
		}, nil
	default:
		return nil, fmt.Errorf("unknown create plan response mode %q", mode)
	}
}

func (c *spikeClient) RequestPermission(ctx context.Context, params *acp.RequestPermissionRequest) (*acp.RequestPermissionResponse, error) {
	fmt.Fprintf(os.Stderr, "permission request: %+v\n", params)
	switch c.permissionMode {
	case "allow-once", "allow-always", "reject-once":
		if len(params.Options) > 0 {
			for _, option := range params.Options {
				if string(option.OptionID) == c.permissionMode {
					fmt.Fprintf(os.Stderr, "permission response: auto-select %s\n", c.permissionMode)
					return &acp.RequestPermissionResponse{
						Outcome: acp.NewRequestPermissionOutcomeSelected(option.OptionID),
					}, nil
				}
			}
			fmt.Fprintf(os.Stderr, "permission mode %q not offered; selecting first option %q\n", c.permissionMode, params.Options[0].OptionID)
			return &acp.RequestPermissionResponse{
				Outcome: acp.NewRequestPermissionOutcomeSelected(params.Options[0].OptionID),
			}, nil
		}
	case "fail":
		break
	}
	return nil, errors.New("permission requests are not handled by this spike")
}

func (c *spikeClient) SessionUpdate(ctx context.Context, params *acp.SessionNotification) error {
	acp.MatchSessionUpdate(&params.Update, acp.SessionUpdateMatcher[struct{}]{
		AgentMessageChunk: func(v acp.SessionUpdateAgentMessageChunk) struct{} {
			if text, ok := v.Content.AsText(); ok {
				fmt.Print(text.Text)
			} else {
				fmt.Printf("[agent_message_chunk non-text]\n")
			}
			return struct{}{}
		},
		AgentThoughtChunk: func(v acp.SessionUpdateAgentThoughtChunk) struct{} {
			if text, ok := v.Content.AsText(); ok {
				fmt.Printf("\n[thought] %s\n", text.Text)
			} else {
				fmt.Printf("\n[thought non-text]\n")
			}
			return struct{}{}
		},
		ToolCall: func(v acp.SessionUpdateToolCall) struct{} {
			status := ""
			if v.Status != nil {
				status = string(*v.Status)
			}
			if status != "" {
				fmt.Printf("\n[tool start] %s (%s)\n", v.Title, status)
			} else {
				fmt.Printf("\n[tool start] %s\n", v.Title)
			}
			return struct{}{}
		},
		ToolCallUpdate: func(v acp.SessionUpdateToolCallUpdate) struct{} {
			status := ""
			if v.Status != nil {
				status = string(*v.Status)
			}
			if status != "" {
				fmt.Printf("\n[tool update] %s (%s)\n", v.ToolCallID, status)
			} else {
				fmt.Printf("\n[tool update] %s\n", v.ToolCallID)
			}
			return struct{}{}
		},
		Plan: func(v acp.SessionUpdatePlan) struct{} {
			fmt.Printf("\n[plan] %d entries\n", len(v.Entries))
			return struct{}{}
		},
		SessionInfoUpdate: func(v acp.SessionUpdateSessionInfoUpdate) struct{} {
			fmt.Printf("\n[session-info] title=%q updatedAt=%q\n", v.Title, v.UpdatedAt)
			return struct{}{}
		},
		CurrentModeUpdate: func(v acp.SessionUpdateCurrentModeUpdate) struct{} {
			fmt.Printf("\n[mode] %s\n", v.CurrentModeID)
			return struct{}{}
		},
		ConfigOptionUpdate: func(v acp.SessionUpdateConfigOptionUpdate) struct{} {
			fmt.Printf("\n[config] %d options updated\n", len(v.ConfigOptions))
			return struct{}{}
		},
		AvailableCommandsUpdate: func(v acp.SessionUpdateAvailableCommandsUpdate) struct{} {
			fmt.Printf("\n[commands] %d commands available\n", len(v.AvailableCommands))
			return struct{}{}
		},
		UserMessageChunk: func(v acp.SessionUpdateUserMessageChunk) struct{} {
			if text, ok := v.Content.AsText(); ok {
				fmt.Printf("\n[user-echo] %s\n", text.Text)
			}
			return struct{}{}
		},
		Default: func() struct{} {
			fmt.Printf("\n[session-update]\n")
			return struct{}{}
		},
	})

	return nil
}

func sortedJSONKeys(raw json.RawMessage) []string {
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil || len(obj) == 0 {
		return nil
	}

	keys := make([]string, 0, len(obj))
	for key := range obj {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func collectJSONSchemaPaths(raw json.RawMessage) []string {
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil
	}

	paths := make(map[string]struct{})
	var walk func(prefix string, v any)
	walk = func(prefix string, v any) {
		switch vv := v.(type) {
		case map[string]any:
			keys := make([]string, 0, len(vv))
			for key := range vv {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				child := key
				if prefix != "" {
					child = prefix + "." + key
				}
				paths[child] = struct{}{}
				walk(child, vv[key])
			}
		case []any:
			arrayPath := prefix + "[]"
			if prefix == "" {
				arrayPath = "[]"
			}
			paths[arrayPath] = struct{}{}
			for _, item := range vv {
				walk(arrayPath, item)
			}
		}
	}

	walk("", decoded)

	out := make([]string, 0, len(paths))
	for path := range paths {
		out = append(out, path)
	}
	sort.Strings(out)
	return out
}

func (c *spikeClient) recordExtObservation(method string, params json.RawMessage) extObservation {
	obs := extObservation{
		Method:       method,
		ObservedAt:   time.Now().UTC().Format(time.RFC3339Nano),
		TopLevelKeys: sortedJSONKeys(params),
		SchemaPaths:  collectJSONSchemaPaths(params),
		Params:       append(json.RawMessage(nil), params...),
	}

	c.extMu.Lock()
	defer c.extMu.Unlock()
	obs.Index = len(c.extObservations) + 1
	c.extObservations = append(c.extObservations, obs)
	return obs
}

func sanitizeMethodName(method string) string {
	replacer := strings.NewReplacer("/", "-", "\\", "-", ":", "-", " ", "-", ".", "-")
	method = replacer.Replace(strings.TrimSpace(method))
	method = strings.Trim(method, "-")
	if method == "" {
		return "unknown"
	}
	return method
}

func normalizeStopReason(reason acp.StopReason) string {
	return strings.TrimSpace(strings.ToLower(string(reason)))
}

func (c *spikeClient) extensionSummaries() []extMethodSummary {
	c.extMu.Lock()
	defer c.extMu.Unlock()

	type summaryState struct {
		count int
		keys  map[string]struct{}
		paths map[string]struct{}
	}

	byMethod := make(map[string]*summaryState)
	for _, obs := range c.extObservations {
		state := byMethod[obs.Method]
		if state == nil {
			state = &summaryState{
				keys:  make(map[string]struct{}),
				paths: make(map[string]struct{}),
			}
			byMethod[obs.Method] = state
		}
		state.count++
		for _, key := range obs.TopLevelKeys {
			state.keys[key] = struct{}{}
		}
		for _, path := range obs.SchemaPaths {
			state.paths[path] = struct{}{}
		}
	}

	methods := make([]string, 0, len(byMethod))
	for method := range byMethod {
		methods = append(methods, method)
	}
	sort.Strings(methods)

	out := make([]extMethodSummary, 0, len(methods))
	for _, method := range methods {
		state := byMethod[method]
		keys := make([]string, 0, len(state.keys))
		for key := range state.keys {
			keys = append(keys, key)
		}
		sort.Strings(keys)

		paths := make([]string, 0, len(state.paths))
		for path := range state.paths {
			paths = append(paths, path)
		}
		sort.Strings(paths)

		out = append(out, extMethodSummary{
			Method:       method,
			Count:        state.count,
			TopLevelKeys: keys,
			SchemaPaths:  paths,
		})
	}

	return out
}

func (c *spikeClient) printExtensionSummary(w io.Writer) {
	summaries := c.extensionSummaries()
	if len(summaries) == 0 {
		fmt.Fprintln(w, "\n[ext summary] no extension payloads observed")
		return
	}

	fmt.Fprintln(w, "\n[ext summary]")
	for _, summary := range summaries {
		fmt.Fprintf(w, "- %s: count=%d\n", summary.Method, summary.Count)
		if len(summary.TopLevelKeys) > 0 {
			fmt.Fprintf(w, "  keys: %s\n", strings.Join(summary.TopLevelKeys, ", "))
		}
		if len(summary.SchemaPaths) > 0 {
			fmt.Fprintf(w, "  schema: %s\n", strings.Join(summary.SchemaPaths, ", "))
		}
	}
}

func (c *spikeClient) writeExtensionArtifacts(dir, scenario, mode, cwd string) error {
	c.extMu.Lock()
	observations := append([]extObservation(nil), c.extObservations...)
	c.extMu.Unlock()

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create extension artifact dir: %w", err)
	}

	type observationRecord struct {
		Index        int      `json:"index"`
		Method       string   `json:"method"`
		ObservedAt   string   `json:"observedAt"`
		TopLevelKeys []string `json:"topLevelKeys,omitempty"`
		SchemaPaths  []string `json:"schemaPaths,omitempty"`
		File         string   `json:"file"`
	}

	type manifest struct {
		Scenario     string              `json:"scenario"`
		Mode         string              `json:"mode,omitempty"`
		CWD          string              `json:"cwd"`
		GeneratedAt  string              `json:"generatedAt"`
		Count        int                 `json:"count"`
		Methods      []extMethodSummary  `json:"methods"`
		Observations []observationRecord `json:"observations"`
	}

	manifestData := manifest{
		Scenario:    scenario,
		Mode:        mode,
		CWD:         cwd,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Count:       len(observations),
		Methods:     c.extensionSummaries(),
	}

	for _, obs := range observations {
		filename := fmt.Sprintf("%03d-%s.json", obs.Index, sanitizeMethodName(obs.Method))
		path := filepath.Join(dir, filename)
		payload, err := json.MarshalIndent(obs, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal extension observation %d: %w", obs.Index, err)
		}
		if err := os.WriteFile(path, payload, 0o644); err != nil {
			return fmt.Errorf("write extension observation %d: %w", obs.Index, err)
		}
		manifestData.Observations = append(manifestData.Observations, observationRecord{
			Index:        obs.Index,
			Method:       obs.Method,
			ObservedAt:   obs.ObservedAt,
			TopLevelKeys: obs.TopLevelKeys,
			SchemaPaths:  obs.SchemaPaths,
			File:         filename,
		})
	}

	summaryPath := filepath.Join(dir, "summary.json")
	summaryBytes, err := json.MarshalIndent(manifestData, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal extension summary: %w", err)
	}
	if err := os.WriteFile(summaryPath, summaryBytes, 0o644); err != nil {
		return fmt.Errorf("write extension summary: %w", err)
	}

	fmt.Fprintf(os.Stderr, "extension artifacts written: %s\n", dir)
	return nil
}

func (c *spikeClient) WriteTextFile(ctx context.Context, params *acp.WriteTextFileRequest) (*acp.WriteTextFileResponse, error) {
	return nil, errors.New("write_text_file not supported by this spike")
}

func (c *spikeClient) ReadTextFile(ctx context.Context, params *acp.ReadTextFileRequest) (*acp.ReadTextFileResponse, error) {
	return nil, errors.New("read_text_file not supported by this spike")
}

func (c *spikeClient) CreateTerminal(ctx context.Context, params *acp.CreateTerminalRequest) (*acp.CreateTerminalResponse, error) {
	return nil, errors.New("terminal/create not supported by this spike")
}

func (c *spikeClient) TerminalOutput(ctx context.Context, params *acp.TerminalOutputRequest) (*acp.TerminalOutputResponse, error) {
	return nil, errors.New("terminal/output not supported by this spike")
}

func (c *spikeClient) ReleaseTerminal(ctx context.Context, params *acp.ReleaseTerminalRequest) (*acp.ReleaseTerminalResponse, error) {
	return nil, errors.New("terminal/release not supported by this spike")
}

func (c *spikeClient) WaitForTerminalExit(ctx context.Context, params *acp.WaitForTerminalExitRequest) (*acp.WaitForTerminalExitResponse, error) {
	return nil, errors.New("terminal/wait_for_exit not supported by this spike")
}

func (c *spikeClient) KillTerminalCommand(ctx context.Context, params *acp.KillTerminalRequest) (*acp.KillTerminalResponse, error) {
	return nil, errors.New("terminal/kill not supported by this spike")
}

func (c *spikeClient) ExtMethod(ctx context.Context, method string, params json.RawMessage) (any, error) {
	fmt.Fprintf(os.Stderr, "\n[ext method] %s\n", method)
	if len(params) > 0 {
		obs := c.recordExtObservation(method, params)
		if len(obs.TopLevelKeys) > 0 {
			fmt.Fprintf(os.Stderr, "[ext keys] %s\n", strings.Join(obs.TopLevelKeys, ", "))
		}
		if len(obs.SchemaPaths) > 0 {
			fmt.Fprintf(os.Stderr, "[ext schema] %s\n", strings.Join(obs.SchemaPaths, ", "))
		}
		fmt.Fprintf(os.Stderr, "[ext params] %s\n", string(params))
	}
	switch method {
	case "cursor/ask_question":
		result, err := c.askQuestionResult(params)
		if err != nil {
			return nil, err
		}
		if data, err := json.Marshal(result); err == nil {
			fmt.Fprintf(os.Stderr, "[ext result] %s\n", string(data))
		}
		return result, nil
	case "cursor/create_plan":
		result, err := c.createPlanResult()
		if err != nil {
			return nil, err
		}
		if data, err := json.Marshal(result); err == nil {
			fmt.Fprintf(os.Stderr, "[ext result] %s\n", string(data))
		}
		return result, nil
	default:
		return map[string]any{}, nil
	}
}

type lineLogger struct {
	prefix string
	sink   io.Writer
	mu     sync.Mutex
	buf    bytes.Buffer
}

func (l *lineLogger) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, b := range p {
		if b == '\n' {
			fmt.Fprintf(l.sink, "%s %s\n", l.prefix, l.buf.String())
			l.buf.Reset()
			continue
		}
		_ = l.buf.WriteByte(b)
	}
	return len(p), nil
}

func spawnClientConnection(ctx context.Context, client *spikeClient, raw bool) (*acp.ClientSideConnection, error) {
	cmd := osexec.CommandContext(ctx, "agent", "acp")

	agentStdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stdin pipe: %w", err)
	}
	agentStdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start ACP agent: %w", err)
	}

	var reader io.Reader = agentStdout
	var writer io.Writer = agentStdin
	if raw {
		reader = io.TeeReader(agentStdout, &lineLogger{prefix: "[recv]", sink: os.Stderr})
		writer = io.MultiWriter(agentStdin, &lineLogger{prefix: "[send]", sink: os.Stderr})
	}

	conn := acp.NewClientSideConnection(client, writer, reader)
	go func() {
		_ = cmd.Wait()
		_ = conn.Close()
	}()

	return conn, nil
}

func initConnection(ctx context.Context, client *spikeClient, raw bool) (*acp.ClientSideConnection, *acp.InitializeResponse, error) {
	conn, err := spawnClientConnection(ctx, client, raw)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to spawn ACP agent: %w", err)
	}

	go func() {
		if err := conn.Start(ctx); err != nil && !errors.Is(err, context.Canceled) {
			fmt.Fprintf(os.Stderr, "connection error: %v\n", err)
		}
	}()

	// go-acp initializes request state inside Start(), so give the
	// connection loop a moment to come up before sending requests.
	time.Sleep(50 * time.Millisecond)

	initResp, err := conn.Initialize(ctx, &acp.InitializeRequest{
		ProtocolVersion: acp.ProtocolVersion(acp.CurrentProtocolVersion),
		ClientCapabilities: &acp.ClientCapabilities{
			FS: &acp.FileSystemCapabilities{
				ReadTextFile:  false,
				WriteTextFile: false,
			},
			Terminal: false,
		},
	})
	if err != nil {
		_ = conn.Close()
		return nil, nil, fmt.Errorf("initialize failed: %w", err)
	}

	if _, err := conn.Authenticate(ctx, &acp.AuthenticateRequest{MethodID: "cursor_login"}); err != nil {
		_ = conn.Close()
		return nil, nil, fmt.Errorf("authenticate failed: %w", err)
	}

	return conn, initResp, nil
}

func newSession(ctx context.Context, conn *acp.ClientSideConnection, cwd string) (*acp.NewSessionResponse, error) {
	return conn.NewSession(ctx, &acp.NewSessionRequest{
		Cwd:        cwd,
		MCPServers: []acp.MCPServer{},
	})
}

func setSessionMode(ctx context.Context, conn *acp.ClientSideConnection, sessionID acp.SessionID, mode string) error {
	mode = strings.TrimSpace(mode)
	if mode == "" {
		return nil
	}

	_, err := conn.SetSessionMode(ctx, &acp.SetSessionModeRequest{
		SessionID: sessionID,
		ModeID:    acp.SessionModeID(mode),
	})
	if err != nil {
		return fmt.Errorf("set session mode failed: %w", err)
	}

	fmt.Fprintf(os.Stderr, "session mode set: %s\n", mode)
	return nil
}

func runBasicPrompt(ctx context.Context, conn *acp.ClientSideConnection, sessionID acp.SessionID, prompt string) error {
	fmt.Fprintf(os.Stderr, "prompt: %s\n\n", prompt)
	promptResp, err := conn.Prompt(ctx, &acp.PromptRequest{
		SessionID: sessionID,
		Prompt: []acp.ContentBlock{
			acp.NewContentBlockText(prompt),
		},
	})
	if err != nil {
		return fmt.Errorf("prompt failed: %w", err)
	}
	fmt.Fprintf(os.Stderr, "\n\nstop reason: %s\n", promptResp.StopReason)
	return nil
}

func runListLoadScenario(ctx context.Context, conn *acp.ClientSideConnection, cwd string, sessionID acp.SessionID) error {
	listResp, err := conn.ListSessions(ctx, &acp.ListSessionsRequest{Cwd: cwd})
	if err != nil {
		fmt.Fprintf(os.Stderr, "list sessions unavailable or failed: %v\n", err)
	} else {
		fmt.Fprintf(os.Stderr, "listed sessions: %d\n", len(listResp.Sessions))
		found := false
		for _, sess := range listResp.Sessions {
			if sess.SessionID == sessionID {
				found = true
				fmt.Fprintf(os.Stderr, "found session in list: %s\n", sess.SessionID)
				break
			}
		}
		if !found {
			fmt.Fprintf(os.Stderr, "warning: new session %s not present in list response\n", sessionID)
		}
	}

	loadResp, err := conn.LoadSession(ctx, &acp.LoadSessionRequest{
		Cwd:        cwd,
		MCPServers: []acp.MCPServer{},
		SessionID:  sessionID,
	})
	if err != nil {
		return fmt.Errorf("load session failed: %w", err)
	}
	modes := 0
	if loadResp.Modes != nil {
		modes = len(loadResp.Modes.AvailableModes)
	}
	fmt.Fprintf(os.Stderr, "load session ok: configOptions=%d modes=%d\n", len(loadResp.ConfigOptions), modes)

	return runBasicPrompt(ctx, conn, sessionID, "After loading this ACP session, say ACP_LOAD_OK and one sentence about whether the session stayed usable.")
}

func runCancelScenario(ctx context.Context, conn *acp.ClientSideConnection, sessionID acp.SessionID) error {
	prompt := "Take your time. Do not answer immediately. Think step by step for a while, explore the repository thoroughly, and only answer after extended investigation."
	fmt.Fprintf(os.Stderr, "cancel scenario prompt: %s\n\n", prompt)

	resultCh := make(chan error, 1)
	go func() {
		promptResp, err := conn.Prompt(ctx, &acp.PromptRequest{
			SessionID: sessionID,
			Prompt: []acp.ContentBlock{
				acp.NewContentBlockText(prompt),
			},
		})
		if err != nil {
			resultCh <- fmt.Errorf("prompt failed: %w", err)
			return
		}
		if promptResp.StopReason == acp.StopReasonCancelled {
			resultCh <- nil
			return
		}
		resultCh <- fmt.Errorf("prompt completed with stop reason: %s", promptResp.StopReason)
	}()

	time.Sleep(2 * time.Second)
	fmt.Fprintf(os.Stderr, "\nsending cancel...\n")
	if err := conn.Cancel(ctx, &acp.CancelNotification{SessionID: sessionID}); err != nil {
		return fmt.Errorf("cancel failed: %w", err)
	}

	select {
	case result := <-resultCh:
		if result == nil {
			fmt.Fprintf(os.Stderr, "cancel scenario succeeded: stop reason was cancelled\n")
			return nil
		}
		return result
	case <-time.After(15 * time.Second):
		return errors.New("cancel sent, but prompt did not finish within 15s")
	}
}

func runExtensionProbeScenario(ctx context.Context, conn *acp.ClientSideConnection, sessionID acp.SessionID) error {
	prompt := "Create a short explicit plan, keep and update a todo list while you work, and if you need clarification ask a multiple-choice question before proceeding. Then summarize what you attempted."
	return runBasicPrompt(ctx, conn, sessionID, prompt)
}

func runSchemaScenario(ctx context.Context, conn *acp.ClientSideConnection, sessionID acp.SessionID, scenario string) error {
	var prompt string
	switch scenario {
	case "schema-todos":
		prompt = "You are helping me probe Cursor ACP schemas. Create and maintain a structured todo list with at least three items while you do a tiny documentation investigation, updating items as you progress. Prefer the richest structured todo UX you support."
	case "schema-create-plan":
		prompt = "You are helping me probe Cursor ACP schemas. Immediately create a short structured plan for improving docs sidebar navigation and search using the richest structured plan approval UX you support. Do not browse, research, or use any other tools first. Do not implement any changes. After the approval response arrives, stop immediately."
	case "schema-ask-question":
		prompt = "You are helping me probe Cursor ACP schemas. Before creating any plan or doing any work, ask exactly one blocking multiple-choice clarification question using the richest structured question UX you support. Do not proceed until that question is resolved. Once the answer is received, stop immediately."
	case "schema-task":
		prompt = "You are helping me probe Cursor ACP schemas. If your environment supports subagents or task delegation, delegate a small repository inspection task, wait for the result, and summarize what happened."
	case "schema-generate-image":
		prompt = "You are helping me probe Cursor ACP schemas. If your environment supports structured image generation, generate a simple image mockup for a docs sidebar and search concept, then summarize the generated result."
	default:
		return fmt.Errorf("unknown schema scenario %q", scenario)
	}
	return runBasicPrompt(ctx, conn, sessionID, prompt)
}

func defaultModeForScenario(scenario string) string {
	switch scenario {
	case "schema-todos", "schema-create-plan", "schema-ask-question":
		return "plan"
	case "schema-task", "schema-generate-image":
		return "agent"
	default:
		return ""
	}
}

func runResumeACPScenario(ctx context.Context, client *spikeClient, cwd, mode string, raw bool) error {
	conn, initResp, err := initConnection(ctx, client, raw)
	if err != nil {
		return err
	}
	defer conn.Close()

	fmt.Fprintf(os.Stderr, "initialized protocol v%d\n", initResp.ProtocolVersion)
	sessionResp, err := newSession(ctx, conn, cwd)
	if err != nil {
		return fmt.Errorf("new session failed: %w", err)
	}
	fmt.Fprintf(os.Stderr, "seed session: %s\n", sessionResp.SessionID)
	if err := setSessionMode(ctx, conn, sessionResp.SessionID, mode); err != nil {
		return err
	}
	if err := runBasicPrompt(ctx, conn, sessionResp.SessionID, "Reply with exactly ACP_RESUME_SEED and remember that token for this session."); err != nil {
		return err
	}
	_ = conn.Close()
	time.Sleep(250 * time.Millisecond)

	loadConn, loadInit, err := initConnection(ctx, client, raw)
	if err != nil {
		return err
	}
	defer loadConn.Close()
	fmt.Fprintf(os.Stderr, "reinitialized protocol v%d\n", loadInit.ProtocolVersion)
	loadResp, err := loadConn.LoadSession(ctx, &acp.LoadSessionRequest{
		Cwd:        cwd,
		MCPServers: []acp.MCPServer{},
		SessionID:  sessionResp.SessionID,
	})
	if err != nil {
		return fmt.Errorf("load session failed: %w", err)
	}
	modeCount := 0
	if loadResp.Modes != nil {
		modeCount = len(loadResp.Modes.AvailableModes)
	}
	fmt.Fprintf(os.Stderr, "load session ok: configOptions=%d modes=%d\n", len(loadResp.ConfigOptions), modeCount)
	return runBasicPrompt(ctx, loadConn, sessionResp.SessionID, "If you remember the previous turn, reply with exactly ACP_RESUME_OK ACP_RESUME_SEED. If not, say what you remember.")
}

func runResumeModeScenario(ctx context.Context, client *spikeClient, cwd, mode string, raw bool) error {
	if strings.TrimSpace(mode) == "" {
		return errors.New("resume-mode scenario requires --mode")
	}

	conn, initResp, err := initConnection(ctx, client, raw)
	if err != nil {
		return err
	}
	defer conn.Close()

	fmt.Fprintf(os.Stderr, "initialized protocol v%d\n", initResp.ProtocolVersion)
	sessionResp, err := newSession(ctx, conn, cwd)
	if err != nil {
		return fmt.Errorf("new session failed: %w", err)
	}
	fmt.Fprintf(os.Stderr, "seed session: %s\n", sessionResp.SessionID)
	if err := setSessionMode(ctx, conn, sessionResp.SessionID, mode); err != nil {
		return err
	}

	var seedPrompt string
	var loadedPrompt string
	switch mode {
	case "plan":
		seedPrompt = "Create a short structured plan for adding a file named plan_mode_probe.txt, but do not implement it."
		loadedPrompt = "Create a tiny hello world bash script named hello.sh in the current directory. If you are still in plan mode, request explicit structured plan approval before any change."
	case "ask":
		seedPrompt = "Briefly explain what ask mode means in this session."
		loadedPrompt = "Create a file named ask_mode_probe.txt in the current directory containing ASK_MODE_TEST. If you are still in ask mode, do not make changes; instead explain that you are in read-only Q&A mode."
	default:
		return fmt.Errorf("resume-mode scenario only supports mode=plan or mode=ask")
	}

	if err := runBasicPrompt(ctx, conn, sessionResp.SessionID, seedPrompt); err != nil {
		return err
	}
	_ = conn.Close()
	time.Sleep(250 * time.Millisecond)

	loadConn, loadInit, err := initConnection(ctx, client, raw)
	if err != nil {
		return err
	}
	defer loadConn.Close()

	fmt.Fprintf(os.Stderr, "reinitialized protocol v%d\n", loadInit.ProtocolVersion)
	loadResp, err := loadConn.LoadSession(ctx, &acp.LoadSessionRequest{
		Cwd:        cwd,
		MCPServers: []acp.MCPServer{},
		SessionID:  sessionResp.SessionID,
	})
	if err != nil {
		return fmt.Errorf("load session failed: %w", err)
	}

	reportedMode := ""
	modeCount := 0
	if loadResp.Modes != nil {
		modeCount = len(loadResp.Modes.AvailableModes)
		reportedMode = string(loadResp.Modes.CurrentModeID)
	}
	fmt.Fprintf(os.Stderr, "load session ok: configOptions=%d modes=%d currentMode=%q\n", len(loadResp.ConfigOptions), modeCount, reportedMode)

	return runBasicPrompt(ctx, loadConn, sessionResp.SessionID, loadedPrompt)
}

func createCLIChat(ctx context.Context, cwd string) (string, error) {
	cmd := osexec.CommandContext(ctx, "agent", "--workspace", cwd, "create-chat")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("agent create-chat failed: %w", err)
	}
	id := strings.TrimSpace(string(out))
	if id == "" {
		return "", errors.New("agent create-chat returned empty session id")
	}
	return id, nil
}

func runCLICreateLoadScenario(ctx context.Context, client *spikeClient, cwd, mode string, raw bool) error {
	sessionID, err := createCLIChat(ctx, cwd)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "cli session: %s\n", sessionID)

	conn, initResp, err := initConnection(ctx, client, raw)
	if err != nil {
		return err
	}
	defer conn.Close()
	fmt.Fprintf(os.Stderr, "initialized protocol v%d\n", initResp.ProtocolVersion)
	loadResp, err := conn.LoadSession(ctx, &acp.LoadSessionRequest{
		Cwd:        cwd,
		MCPServers: []acp.MCPServer{},
		SessionID:  acp.SessionID(sessionID),
	})
	if err != nil {
		return fmt.Errorf("load cli-created session failed: %w", err)
	}
	modeCount := 0
	if loadResp.Modes != nil {
		modeCount = len(loadResp.Modes.AvailableModes)
	}
	fmt.Fprintf(os.Stderr, "load cli-created session ok: configOptions=%d modes=%d\n", len(loadResp.ConfigOptions), modeCount)
	if err := setSessionMode(ctx, conn, acp.SessionID(sessionID), mode); err != nil {
		return err
	}
	return runBasicPrompt(ctx, conn, acp.SessionID(sessionID), "Reply with exactly ACP_CLI_LOAD_OK if this loaded session is usable.")
}

func main() {
	os.Exit(run())
}

func run() int {
	var (
		cwd            string
		prompt         string
		timeout        time.Duration
		scenario       string
		mode           string
		permissionMode string
		raw            bool
		extOutDir      string
		askResponse    string
		askJSON        string
		planResponse   string
		planJSON       string
	)

	flag.StringVar(&cwd, "cwd", ".", "Working directory for the ACP session")
	flag.StringVar(&prompt, "prompt", "Say ACP_SPIKE_OK and describe your current capabilities in one short paragraph.", "Prompt to send")
	flag.DurationVar(&timeout, "timeout", 60*time.Second, "Overall timeout for the spike run")
	flag.StringVar(&scenario, "scenario", "basic", "Scenario to run: basic, list-load, cancel, ext-probe, schema-todos, schema-create-plan, schema-ask-question, schema-task, schema-generate-image, resume-acp, resume-mode, cli-create-load")
	flag.StringVar(&mode, "mode", "", "Optional ACP session mode to set after session creation (for example: agent, ask, plan)")
	flag.StringVar(&permissionMode, "permission", "allow-once", "Permission handling mode: allow-once, allow-always, reject-once, fail")
	flag.BoolVar(&raw, "raw", false, "Log raw JSON-RPC lines sent to and received from Cursor ACP")
	flag.StringVar(&extOutDir, "ext-out-dir", "", "Optional directory where observed extension payloads and a schema summary will be written")
	flag.StringVar(&askResponse, "ask-response", "selected-id-text", "cursor/ask_question response preset: selected-id-text, selected-id, selected-ids, selected-option-ids, selected-ids-text, answered, cancelled, empty")
	flag.StringVar(&askJSON, "ask-json", "", "Optional raw JSON result payload override for cursor/ask_question")
	flag.StringVar(&planResponse, "plan-response", "approved-feedback", "cursor/create_plan response preset: approved-feedback, approved, rejected-feedback, rejected, empty")
	flag.StringVar(&planJSON, "plan-json", "", "Optional raw JSON result payload override for cursor/create_plan")
	flag.Parse()

	if flag.NArg() > 0 {
		prompt = strings.Join(flag.Args(), " ")
	}

	absCWD, err := filepath.Abs(cwd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to resolve cwd: %v\n", err)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	if mode == "" {
		if defaultMode := defaultModeForScenario(scenario); defaultMode != "" {
			mode = defaultMode
			fmt.Fprintf(os.Stderr, "scenario defaulted mode to %s\n", mode)
		}
	}

	client := &spikeClient{
		permissionMode: permissionMode,
		extOutDir:      extOutDir,
		askResponse:    askResponse,
		askJSON:        askJSON,
		planResponse:   planResponse,
		planJSON:       planJSON,
	}
	finalize := func(exitCode int) int {
		client.printExtensionSummary(os.Stderr)
		if strings.TrimSpace(client.extOutDir) != "" {
			if err := client.writeExtensionArtifacts(client.extOutDir, scenario, mode, absCWD); err != nil {
				fmt.Fprintf(os.Stderr, "failed to write extension artifacts: %v\n", err)
				return 1
			}
		}
		return exitCode
	}
	if scenario == "resume-acp" {
		if err := runResumeACPScenario(ctx, client, absCWD, mode, raw); err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			return finalize(1)
		}
		return finalize(0)
	}
	if scenario == "resume-mode" {
		if err := runResumeModeScenario(ctx, client, absCWD, mode, raw); err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			return finalize(1)
		}
		return finalize(0)
	}
	if scenario == "cli-create-load" {
		if err := runCLICreateLoadScenario(ctx, client, absCWD, mode, raw); err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			return finalize(1)
		}
		return finalize(0)
	}

	conn, initResp, err := initConnection(ctx, client, raw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return finalize(1)
	}
	defer conn.Close()

	fmt.Fprintf(os.Stderr, "initialized protocol v%d\n", initResp.ProtocolVersion)

	sessionResp, err := newSession(ctx, conn, absCWD)
	if err != nil {
		fmt.Fprintf(os.Stderr, "new session failed: %v\n", err)
		return finalize(1)
	}

	fmt.Fprintf(os.Stderr, "session: %s\n", sessionResp.SessionID)
	if err := setSessionMode(ctx, conn, sessionResp.SessionID, mode); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return finalize(1)
	}

	switch scenario {
	case "basic":
		err = runBasicPrompt(ctx, conn, sessionResp.SessionID, prompt)
	case "list-load":
		err = runListLoadScenario(ctx, conn, absCWD, sessionResp.SessionID)
	case "cancel":
		err = runCancelScenario(ctx, conn, sessionResp.SessionID)
	case "ext-probe":
		err = runExtensionProbeScenario(ctx, conn, sessionResp.SessionID)
	case "schema-todos", "schema-create-plan", "schema-ask-question", "schema-task", "schema-generate-image":
		err = runSchemaScenario(ctx, conn, sessionResp.SessionID, scenario)
	default:
		err = fmt.Errorf("unknown scenario %q", scenario)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "\n%s\n", err)
		return finalize(1)
	}
	return finalize(0)
}
