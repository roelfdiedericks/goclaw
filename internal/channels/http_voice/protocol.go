package http_voice

// Client -> Server messages
const (
	MsgTypeConnect      = "connect"      // Initial connection with auth
	MsgTypeDisconnect   = "disconnect"   // Graceful disconnect
	MsgTypeAudio        = "audio"        // Audio chunk (base64 PCM)
	MsgTypeCapabilities = "capabilities" // Browser audio capabilities
)

// Server -> Client messages
const (
	MsgTypeConnected     = "connected"     // Connection acknowledged
	MsgTypeAudioDelta    = "audio"         // Audio chunk from assistant
	MsgTypeTranscript    = "transcript"    // Transcript update (user or assistant)
	MsgTypeMirror        = "mirror"        // Message from another channel
	MsgTypeAgentMessage  = "agent_message" // Direct agent output (tool messages, etc.)
	MsgTypeError         = "error"         // Error message
	MsgTypeStatus        = "status"        // Status update (connecting, connected, etc.)
)

// ClientMessage is a message from the browser to the server
type ClientMessage struct {
	Type  string `json:"type"`
	Audio string `json:"audio,omitempty"` // Base64 encoded PCM audio
	Token string `json:"token,omitempty"` // Auth token for connect

	// Capabilities fields (sent once after audio pipeline starts)
	BrowserSampleRate int    `json:"browserSampleRate,omitempty"` // Browser's native sample rate
	OutputSampleRate  int    `json:"outputSampleRate,omitempty"`  // Rate after resampling (24kHz)
	UserAgent         string `json:"userAgent,omitempty"`         // Browser user agent
	AudioConstraints  string `json:"audioConstraints,omitempty"`  // Applied audio constraints
}

// ServerMessage is a message from the server to the browser
type ServerMessage struct {
	Type       string `json:"type"`
	Audio      string `json:"audio,omitempty"`      // Base64 encoded PCM audio
	Transcript string `json:"transcript,omitempty"` // Text transcript
	Role       string `json:"role,omitempty"`       // "user" or "assistant"
	IsFinal    bool   `json:"isFinal,omitempty"`    // True if transcript is final
	Error      string `json:"error,omitempty"`      // Error message
	Status     string `json:"status,omitempty"`     // Status message
	Source     string `json:"source,omitempty"`     // Source channel for mirror
	UserMsg    string `json:"userMsg,omitempty"`    // User message for mirror
	Message    string `json:"message,omitempty"`    // Agent message content (for agent_message type)
}
