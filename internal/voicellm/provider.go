// Package voicellm provides interfaces and implementations for real-time voice LLM providers.
// VoiceLLMs (xAI Voice, OpenAI Realtime) are separate from text LLM providers as they
// operate over persistent WebSocket connections with streaming audio I/O.
package voicellm

import (
	"context"

	"github.com/roelfdiedericks/goclaw/internal/types"
)

// Provider abstracts real-time voice LLM APIs (xAI Voice, OpenAI Realtime, etc.)
// Unlike text LLM providers, VoiceLLM providers maintain persistent WebSocket connections
// and stream audio bidirectionally.
type Provider interface {
	// Identity
	Name() string // Provider instance name (e.g., "xai", "openai")
	Type() string // Provider type (e.g., "xai", "openai")

	// Session lifecycle
	Connect(ctx context.Context) error
	Configure(cfg SessionConfig) error
	Close() error
	IsConnected() bool

	// Audio I/O
	SendAudio(pcm []byte) error
	SendAudioBase64(b64 string) error
	SendText(text string) error

	// Manual turn control (when ServerVAD=false)
	CommitAudioBuffer() error
	RequestResponse() error

	// Callbacks (set before Connect)
	SetCallbacks(cb Callbacks)
}

// SessionConfig is provider-agnostic session configuration sent after Connect.
type SessionConfig struct {
	Instructions string                 // System prompt / instructions
	Tools        []types.ToolDefinition // Available tools
	Voice        string                 // Provider-specific voice (Eve, marin, etc.)
	SampleRate   int                    // Audio sample rate (default 24000)
	AudioFormat  string                 // "pcm", "pcmu", "pcma"
	ServerVAD    bool                   // Enable server-side voice activity detection
}

// Callbacks for async events from the voice provider.
// Set these before calling Connect().
type Callbacks struct {
	// OnAudioDelta receives response audio chunks (PCM bytes)
	OnAudioDelta func(audio []byte)

	// OnTranscriptDelta receives response transcript deltas (what assistant is saying)
	OnTranscriptDelta func(text string)

	// OnInputTranscript receives the transcribed user speech (what user said)
	OnInputTranscript func(text string)

	// OnToolCall is invoked when the model wants to call a tool.
	// callID is used to correlate with tool results.
	// Returns the tool result string.
	OnToolCall func(callID, name, args string) string

	// OnSpeechStarted is called when VAD detects user started speaking
	OnSpeechStarted func()

	// OnSpeechStopped is called when VAD detects user stopped speaking
	OnSpeechStopped func()

	// OnTurnComplete is called when a response turn is finished.
	// Use this to persist the conversation turn.
	OnTurnComplete func()

	// OnError is called when an error occurs
	OnError func(err error)
}

// ToolDefinition is an alias to types.ToolDefinition for convenience
type ToolDefinition = types.ToolDefinition
