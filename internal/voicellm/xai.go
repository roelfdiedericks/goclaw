package voicellm

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/metrics"
)

const (
	xaiVoiceEndpoint = "wss://api.x.ai/v1/realtime"
	xaiWriteWait     = 30 * time.Second
	xaiPongWait      = 60 * time.Second
)

// XAIProvider implements the VoiceLLM Provider interface for xAI Voice API
type XAIProvider struct {
	name         string
	config       ProviderConfig
	callbacks    Callbacks
	metricPrefix string // voicellm/xai/{name}

	mu        sync.Mutex
	conn      *websocket.Conn
	connected bool
	ctx       context.Context
	cancel    context.CancelFunc

	// Read loop state
	readDone chan struct{}

	// Stats for debugging
	audioChunksSent int

	// Tool call tracking - prevents duplicate response.create messages
	// See: https://docs.x.ai/developers/model-capabilities/audio/voice-agent
	// Section: "Avoid Audio Overlap During Tool Calls"
	// When multiple tools are called in one turn, we must only send ONE response.create
	// after ALL tool results are submitted, not after each individual tool.
	pendingToolResults int

	// Session timing
	sessionStart time.Time

	// Audio effects processor
	effects *AudioEffects
}

// NewXAIProvider creates a new xAI VoiceLLM provider instance
func NewXAIProvider(name string, cfg ProviderConfig) (Provider, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("xai voicellm: API key required")
	}

	// Apply defaults
	if cfg.Voice == "" {
		cfg.Voice = "Eve"
	}
	if cfg.SampleRate == 0 {
		cfg.SampleRate = 24000
	}
	if cfg.AudioFormat == "" {
		cfg.AudioFormat = "pcm"
	}

	return &XAIProvider{
		name:         name,
		config:       cfg,
		metricPrefix: fmt.Sprintf("voicellm/xai/%s", name),
	}, nil
}

// Name returns the provider instance name
func (p *XAIProvider) Name() string {
	return p.name
}

// Type returns the provider type
func (p *XAIProvider) Type() string {
	return "xai"
}

// Connect establishes the WebSocket connection to xAI Voice API
func (p *XAIProvider) Connect(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.connected && p.conn != nil {
		return nil
	}

	connectStart := time.Now()

	// Create connection context
	p.ctx, p.cancel = context.WithCancel(ctx)

	// Set up headers with API key
	header := http.Header{}
	header.Set("Authorization", "Bearer "+p.config.APIKey)

	dialer := websocket.Dialer{
		HandshakeTimeout: 30 * time.Second,
	}

	L_debug("xai voicellm: connecting", "endpoint", xaiVoiceEndpoint)

	conn, resp, err := dialer.DialContext(p.ctx, xaiVoiceEndpoint, header)
	if err != nil {
		metrics.MetricDuration(p.metricPrefix, "connect", time.Since(connectStart))
		metrics.MetricFailWithReason(p.metricPrefix, "connect_status", "dial_error")
		if resp != nil {
			L_error("xai voicellm: dial failed",
				"status", resp.StatusCode,
				"error", err,
			)
			resp.Body.Close()
			if resp.StatusCode == 401 || resp.StatusCode == 403 {
				return fmt.Errorf("xai voicellm: authentication failed (HTTP %d): %w", resp.StatusCode, err)
			}
			return fmt.Errorf("xai voicellm: dial failed (HTTP %d): %w", resp.StatusCode, err)
		}
		L_error("xai voicellm: dial failed", "error", err)
		return fmt.Errorf("xai voicellm: dial failed: %w", err)
	}

	p.conn = conn
	p.connected = true
	p.readDone = make(chan struct{})
	p.sessionStart = time.Now()
	p.audioChunksSent = 0

	// Get effects config from global registry (effects are provider-agnostic)
	var effectsCfg EffectsConfig
	if reg := GetRegistry(); reg != nil {
		effectsCfg = reg.GetEffectsConfig()
	}
	p.effects = NewAudioEffects(effectsCfg, p.config.SampleRate)

	// Record metrics
	metrics.MetricDuration(p.metricPrefix, "connect", time.Since(connectStart))
	metrics.MetricSuccess(p.metricPrefix, "connect_status")
	metrics.MetricInc(p.metricPrefix, "sessions_total")

	// Start read loop
	go p.readLoop()

	if p.effects.IsEnabled() {
		L_info("xai voicellm: connected", "endpoint", xaiVoiceEndpoint, "effects", effectsCfg.Mode)
	} else {
		L_info("xai voicellm: connected", "endpoint", xaiVoiceEndpoint)
	}

	return nil
}

// Configure sends session configuration to the voice API
func (p *XAIProvider) Configure(cfg SessionConfig) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.connected || p.conn == nil {
		return fmt.Errorf("xai voicellm: not connected")
	}

	// Use session config values if provided, otherwise fall back to provider config
	sampleRate := p.config.SampleRate
	if cfg.SampleRate > 0 {
		sampleRate = cfg.SampleRate
	}
	audioFormat := p.config.AudioFormat
	if cfg.AudioFormat != "" {
		audioFormat = cfg.AudioFormat
	}
	voice := p.config.Voice
	if cfg.Voice != "" {
		voice = cfg.Voice
	}

	// Build session.update message
	sessionConfig := map[string]any{
		"instructions": cfg.Instructions,
		"voice":        voice,
		"audio": map[string]any{
			"input": map[string]any{
				"format": map[string]any{
					"type": "audio/" + audioFormat,
					"rate": sampleRate,
				},
			},
			"output": map[string]any{
				"format": map[string]any{
					"type": "audio/" + audioFormat,
					"rate": sampleRate,
				},
			},
		},
	}

	// Add VAD configuration
	if cfg.ServerVAD {
		sessionConfig["turn_detection"] = map[string]any{
			"type": "server_vad",
		}
	} else {
		sessionConfig["turn_detection"] = nil
	}

	// Add tools if provided
	if len(cfg.Tools) > 0 {
		var tools []map[string]any
		for _, tool := range cfg.Tools {
			tools = append(tools, map[string]any{
				"type":        "function",
				"name":        tool.Name,
				"description": tool.Description,
				"parameters":  tool.InputSchema,
			})
		}
		sessionConfig["tools"] = tools
	}

	msg := map[string]any{
		"type":    "session.update",
		"session": sessionConfig,
	}

	// Log the full session config at trace level
	if jsonBytes, err := json.MarshalIndent(msg, "", "  "); err == nil {
		L_trace("xai voicellm: session.update payload", "json", string(jsonBytes))
	}

	if err := p.writeJSON(msg); err != nil {
		return fmt.Errorf("xai voicellm: failed to send session.update: %w", err)
	}

	L_debug("xai voicellm: session configured",
		"voice", voice,
		"sampleRate", sampleRate,
		"serverVAD", cfg.ServerVAD,
		"tools", len(cfg.Tools))

	return nil
}

// Close disconnects from the voice API
func (p *XAIProvider) Close() error {
	p.mu.Lock()

	// Record session duration if we had a session
	if !p.sessionStart.IsZero() {
		sessionDuration := time.Since(p.sessionStart)
		metrics.MetricDuration(p.metricPrefix, "session", sessionDuration)
		// Cumulative counter for cost calculations (total seconds connected)
		metrics.MetricAdd(p.metricPrefix, "session_seconds_total", int64(sessionDuration.Seconds()))
		p.sessionStart = time.Time{}
	}

	if p.cancel != nil {
		p.cancel()
	}

	if p.conn != nil {
		p.conn.Close()
		p.conn = nil
	}
	p.connected = false
	p.mu.Unlock()

	// Wait for read loop to finish
	if p.readDone != nil {
		select {
		case <-p.readDone:
		case <-time.After(2 * time.Second):
			L_warn("xai voicellm: read loop didn't finish in time")
		}
	}

	L_debug("xai voicellm: closed")
	return nil
}

// IsConnected returns whether the WebSocket is connected
func (p *XAIProvider) IsConnected() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.connected && p.conn != nil
}

// SendAudio sends PCM audio data to the voice API
func (p *XAIProvider) SendAudio(pcm []byte) error {
	return p.SendAudioBase64(base64.StdEncoding.EncodeToString(pcm))
}

// SendAudioBase64 sends base64-encoded PCM audio to the voice API
func (p *XAIProvider) SendAudioBase64(b64 string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.connected || p.conn == nil {
		return fmt.Errorf("xai voicellm: not connected")
	}

	msg := map[string]any{
		"type":  "input_audio_buffer.append",
		"audio": b64,
	}

	if err := p.writeJSON(msg); err != nil {
		return fmt.Errorf("xai voicellm: failed to send audio: %w", err)
	}

	// Decode to get raw byte count for metrics
	rawBytes := base64.StdEncoding.DecodedLen(len(b64))
	metrics.MetricAdd(p.metricPrefix, "audio_bytes_in", int64(rawBytes))

	p.audioChunksSent++
	if p.audioChunksSent%25 == 1 { // Log every 25 chunks (~5 seconds)
		// Analyze audio content
		if raw, err := base64.StdEncoding.DecodeString(b64); err == nil {
			// Calculate RMS and peak from int16 samples
			samples := len(raw) / 2
			var sumSq float64
			var peak int16
			for i := 0; i < len(raw)-1; i += 2 {
				sample := int16(raw[i]) | (int16(raw[i+1]) << 8) // little-endian
				if sample < 0 {
					sample = -sample
				}
				if sample > peak {
					peak = sample
				}
				sumSq += float64(sample) * float64(sample)
			}
			rms := int(math.Sqrt(sumSq / float64(samples)))
			L_trace("xai voicellm: audio analysis",
				"chunks", p.audioChunksSent,
				"samples", samples,
				"rms", rms,
				"peak", peak,
				"b64len", len(b64))
		} else {
			L_trace("xai voicellm: audio chunks sent", "total", p.audioChunksSent, "b64len", len(b64))
		}
	}
	return nil
}

// SendText sends text input to the voice API (for text-based input alongside voice)
func (p *XAIProvider) SendText(text string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.connected || p.conn == nil {
		return fmt.Errorf("xai voicellm: not connected")
	}

	// Create a user message item
	msg := map[string]any{
		"type": "conversation.item.create",
		"item": map[string]any{
			"type": "message",
			"role": "user",
			"content": []map[string]any{
				{
					"type": "input_text",
					"text": text,
				},
			},
		},
	}

	if err := p.writeJSON(msg); err != nil {
		return fmt.Errorf("xai voicellm: failed to send text: %w", err)
	}

	L_debug("xai voicellm: sent text", "length", len(text))
	return nil
}

// CommitAudioBuffer commits the audio buffer for processing (manual VAD mode)
func (p *XAIProvider) CommitAudioBuffer() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.connected || p.conn == nil {
		return fmt.Errorf("xai voicellm: not connected")
	}

	msg := map[string]any{
		"type": "input_audio_buffer.commit",
	}

	if err := p.writeJSON(msg); err != nil {
		return fmt.Errorf("xai voicellm: failed to commit audio: %w", err)
	}

	L_trace("xai voicellm: audio buffer committed")
	return nil
}

// RequestResponse requests the model to generate a response (manual mode)
func (p *XAIProvider) RequestResponse() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.connected || p.conn == nil {
		return fmt.Errorf("xai voicellm: not connected")
	}

	msg := map[string]any{
		"type": "response.create",
	}

	if err := p.writeJSON(msg); err != nil {
		return fmt.Errorf("xai voicellm: failed to request response: %w", err)
	}

	L_debug("xai voicellm: response requested")
	return nil
}

// SetCallbacks sets the callback handlers for async events
func (p *XAIProvider) SetCallbacks(cb Callbacks) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.callbacks = cb
}

// writeJSON sends a JSON message over the WebSocket (must hold mutex)
func (p *XAIProvider) writeJSON(msg any) error {
	if err := p.conn.SetWriteDeadline(time.Now().Add(xaiWriteWait)); err != nil {
		return err
	}
	// Log non-audio messages at trace level
	if msgMap, ok := msg.(map[string]any); ok {
		if msgType, _ := msgMap["type"].(string); msgType != "input_audio_buffer.append" {
			L_trace("xai voicellm: sending", "type", msgType)
		}
	}
	return p.conn.WriteJSON(msg)
}

// readLoop continuously reads events from the WebSocket
func (p *XAIProvider) readLoop() {
	defer close(p.readDone)

	for {
		select {
		case <-p.ctx.Done():
			return
		default:
		}

		_, data, err := p.conn.ReadMessage()
		if err != nil {
			p.mu.Lock()
			p.connected = false
			p.mu.Unlock()

			if p.callbacks.OnError != nil && p.ctx.Err() == nil {
				p.callbacks.OnError(fmt.Errorf("xai voicellm: read error: %w", err))
			}
			return
		}

		// Parse the event
		var event xaiEvent
		if err := json.Unmarshal(data, &event); err != nil {
			L_warn("xai voicellm: failed to parse event", "error", err)
			continue
		}

		// Log raw event at trace level (truncate audio data)
		if event.Type != "ping" && event.Type != "response.output_audio.delta" {
			rawLen := len(data)
			if rawLen > 500 {
				L_trace("xai voicellm: raw event", "type", event.Type, "len", rawLen, "preview", string(data[:500]))
			} else {
				L_trace("xai voicellm: raw event", "type", event.Type, "json", string(data))
			}
		}

		p.handleEvent(&event)
	}
}

// handleEvent processes an incoming event from xAI
func (p *XAIProvider) handleEvent(event *xaiEvent) {
	// Log audio-related and ping events at trace level (very frequent)
	switch event.Type {
	case "response.output_audio.delta", "response.output_audio_transcript.delta", "ping":
		L_trace("xai voicellm: event received", "type", event.Type)
	default:
		L_debug("xai voicellm: event received", "type", event.Type)
	}

	switch event.Type {
	case "conversation.created":
		L_debug("xai voicellm: conversation created")

	case "session.created", "session.updated":
		L_debug("xai voicellm: session updated")

	case "input_audio_buffer.speech_started":
		L_info("xai voicellm: speech started (VAD detected)")
		if p.callbacks.OnSpeechStarted != nil {
			p.callbacks.OnSpeechStarted()
		}

	case "input_audio_buffer.speech_stopped":
		L_info("xai voicellm: speech stopped (VAD detected)")
		if p.callbacks.OnSpeechStopped != nil {
			p.callbacks.OnSpeechStopped()
		}

	case "conversation.item.input_audio_transcription.completed":
		// User's speech has been transcribed
		if event.Transcript != "" {
			metrics.MetricAdd(p.metricPrefix, "user_transcript_chars", int64(len(event.Transcript)))
		}
		if p.callbacks.OnInputTranscript != nil && event.Transcript != "" {
			p.callbacks.OnInputTranscript(event.Transcript)
		}

	case "response.output_audio.delta":
		// Audio chunk from assistant
		if p.callbacks.OnAudioDelta != nil && event.Delta != "" {
			audio, err := base64.StdEncoding.DecodeString(event.Delta)
			if err != nil {
				L_warn("xai voicellm: failed to decode audio delta", "error", err)
			} else {
				metrics.MetricAdd(p.metricPrefix, "audio_bytes_out", int64(len(audio)))
				if p.effects != nil && p.effects.IsEnabled() {
					audio = p.effects.Process(audio)
				}
				p.callbacks.OnAudioDelta(audio)
			}
		}

	case "response.output_audio_transcript.delta":
		// Transcript delta from assistant speech
		if event.Delta != "" {
			metrics.MetricAdd(p.metricPrefix, "agent_transcript_chars", int64(len(event.Delta)))
		}
		if p.callbacks.OnTranscriptDelta != nil && event.Delta != "" {
			p.callbacks.OnTranscriptDelta(event.Delta)
		}

	case "response.function_call_arguments.done":
		// Tool call complete, execute it
		p.handleToolCall(event)

	case "response.done":
		// Response turn complete
		// If we had tool calls this turn, NOW send the single response.create
		// This prevents duplicate responses when multiple tools are called
		p.mu.Lock()
		hadToolCalls := p.pendingToolResults > 0
		p.pendingToolResults = 0
		p.mu.Unlock()

		if hadToolCalls {
			p.requestContinuation()
		}

		if p.callbacks.OnTurnComplete != nil {
			p.callbacks.OnTurnComplete()
		}

	case "error":
		if p.callbacks.OnError != nil {
			errMsg := "unknown error"
			if event.Error != nil {
				errMsg = event.Error.Message
			}
			p.callbacks.OnError(fmt.Errorf("xai voicellm: %s", errMsg))
		}

	default:
		L_trace("xai voicellm: unhandled event", "type", event.Type)
	}
}

// handleToolCall executes a tool and sends the result back
func (p *XAIProvider) handleToolCall(event *xaiEvent) {
	if p.callbacks.OnToolCall == nil {
		L_warn("xai voicellm: tool call received but no callback set",
			"name", event.Name, "callID", event.CallID)
		return
	}

	// Track tool call
	metrics.MetricInc(p.metricPrefix, "tool_calls")
	toolStart := time.Now()

	// Execute the tool via callback
	result := p.callbacks.OnToolCall(event.CallID, event.Name, event.Arguments)

	// Record tool execution time
	metrics.MetricDuration(p.metricPrefix, "tool_latency", time.Since(toolStart))

	// Send result back to xAI
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.connected || p.conn == nil {
		L_warn("xai voicellm: cannot send tool result, disconnected")
		return
	}

	// Send function call output
	outputMsg := map[string]any{
		"type": "conversation.item.create",
		"item": map[string]any{
			"type":    "function_call_output",
			"call_id": event.CallID,
			"output":  result,
		},
	}

	if err := p.writeJSON(outputMsg); err != nil {
		L_error("xai voicellm: failed to send tool result", "error", err)
		return
	}

	// Track that we had a tool call - response.create will be sent in response.done handler
	// This fixes duplicate responses when multiple tools are called in one turn.
	// See xAI docs: "Avoid Audio Overlap During Tool Calls"
	// Future enhancement: consider waiting for audio playback to complete before
	// sending response.create, as recommended by xAI docs for smoother UX.
	p.pendingToolResults++

	L_debug("xai voicellm: tool result sent", "name", event.Name, "callID", event.CallID, "pendingResults", p.pendingToolResults)
}

// requestContinuation sends response.create to request the model to continue
func (p *XAIProvider) requestContinuation() {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.connected || p.conn == nil {
		L_warn("xai voicellm: cannot request continuation, disconnected")
		return
	}

	responseMsg := map[string]any{
		"type": "response.create",
	}

	if err := p.writeJSON(responseMsg); err != nil {
		L_error("xai voicellm: failed to request continuation", "error", err)
		return
	}

	L_debug("xai voicellm: continuation requested after tool results")
}

// xaiEvent represents an event from the xAI Voice API
type xaiEvent struct {
	Type string `json:"type"`

	// For transcript events
	Transcript string `json:"transcript,omitempty"`

	// For delta events (audio, text)
	Delta string `json:"delta,omitempty"`

	// For tool calls
	CallID    string `json:"call_id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`

	// For output items
	Item *xaiItem `json:"item,omitempty"`

	// For error events
	Error *xaiError `json:"error,omitempty"`
}

type xaiItem struct {
	Type      string `json:"type"`
	CallID    string `json:"call_id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

type xaiError struct {
	Type    string `json:"type"`
	Code    string `json:"code"`
	Message string `json:"message"`
}
