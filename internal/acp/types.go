package acp

import (
	"context"
	"encoding/json"
	"time"

	"github.com/roelfdiedericks/goclaw/internal/user"
)

type Driver interface {
	ID() string
	SupportsTransport(transportID string) bool
	LaunchSpec(ctx context.Context, req LaunchSpecRequest) (LaunchSpec, error)
}

type AuthMethodProvider interface {
	AuthMethodID() string
}

type LaunchSpecRequest struct {
	CWD  string
	Mode string
	Env  map[string]string
}

type LaunchSpec struct {
	Command string
	Args    []string
	Env     map[string]string
}

type Transport interface {
	ID() string
	NewSession(ctx context.Context, req NewSessionRequest) (*SessionHandle, error)
	LoadSession(ctx context.Context, req LoadSessionRequest) (*SessionHandle, error)
	SetMode(ctx context.Context, handle *SessionHandle, mode string) error
	SetModel(ctx context.Context, handle *SessionHandle, modelValue string) (*ACPModelState, error)
	ListModels(ctx context.Context, handle *SessionHandle) (*ACPModelState, error)
	Prompt(ctx context.Context, handle *SessionHandle, req PromptRequest) (*PromptResult, error)
	Cancel(ctx context.Context, handle *SessionHandle) error
	Close(ctx context.Context, handle *SessionHandle) error
}

type NewSessionRequest struct {
	Driver Driver
	CWD    string
	Mode   string
}

type LoadSessionRequest struct {
	Driver    Driver
	SessionID string
	CWD       string
	Mode      string
}

type SessionHandle struct {
	SessionID string
	CWD       string
	Mode      string
	Transport string
	Driver    string
	Models    *ACPModelState
	runtime   any
}

type ACPModelChoice struct {
	Value       string `json:"value"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type ACPModelState struct {
	CurrentValue string           `json:"currentValue,omitempty"`
	Options      []ACPModelChoice `json:"options,omitempty"`
}

type ACPModelOption struct {
	FriendlyID  string `json:"friendlyId"`
	Name        string `json:"name"`
	ACPValue    string `json:"acpValue"`
	Description string `json:"description,omitempty"`
	Current     bool   `json:"current,omitempty"`
}

type ACPEventType string

const (
	EventTextDelta   ACPEventType = "text_delta"
	EventThought     ACPEventType = "thought_delta"
	EventToolStart   ACPEventType = "tool_start"
	EventToolUpdate  ACPEventType = "tool_update"
	EventDriverExt   ACPEventType = "driver_extension"
	EventTodoUpdate  ACPEventType = "todo_update"
	EventPlanRequest ACPEventType = "plan_request"
	EventQuestion    ACPEventType = "question"
	EventStatus      ACPEventType = "status"
)

type ACPEvent struct {
	Type      ACPEventType
	Payload   any
	Timestamp time.Time
}

type TextDeltaPayload struct {
	Text string
}

type ThoughtDeltaPayload struct {
	Text string
}

type ToolLocation struct {
	Path string `json:"path"`
	Line *int64 `json:"line,omitempty"`
}

type ToolStartPayload struct {
	ToolCallID string
	Title      string
	Status     string
	Kind       string
	Input      json.RawMessage
	Content    json.RawMessage
	Meta       json.RawMessage
	RawOutput  json.RawMessage
	Locations  []ToolLocation
}

type ToolUpdatePayload struct {
	ToolCallID   string
	Title        string
	Status       string
	Kind         string
	Content      json.RawMessage
	ContentText  string
	Meta         json.RawMessage
	Input        json.RawMessage
	RawOutput    json.RawMessage
	Locations    []ToolLocation
	IsTerminal   bool
	IsSuccessful bool
}

type TodoItem struct {
	ID      string `json:"id"`
	Content string `json:"content"`
	Status  string `json:"status"`
}

type TodoUpdatePayload struct {
	ToolCallID string     `json:"toolCallId,omitempty"`
	Todos      []TodoItem `json:"todos,omitempty"`
	Merge      bool       `json:"merge,omitempty"`
}

type QuestionOption struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type QuestionItem struct {
	ID            string           `json:"id"`
	Prompt        string           `json:"prompt"`
	Options       []QuestionOption `json:"options"`
	AllowMultiple bool             `json:"allowMultiple"`
}

type QuestionPayload struct {
	ToolCallID string         `json:"toolCallId,omitempty"`
	Title      string         `json:"title,omitempty"`
	Questions  []QuestionItem `json:"questions,omitempty"`
}

type PlanPhase struct {
	Name  string     `json:"name,omitempty"`
	Todos []TodoItem `json:"todos,omitempty"`
}

type PlanRequestPayload struct {
	ToolCallID string      `json:"toolCallId,omitempty"`
	Name       string      `json:"name,omitempty"`
	Overview   string      `json:"overview,omitempty"`
	Plan       string      `json:"plan,omitempty"`
	Todos      []TodoItem  `json:"todos,omitempty"`
	IsProject  bool        `json:"isProject,omitempty"`
	Phases     []PlanPhase `json:"phases,omitempty"`
}

type TaskPayload struct {
	ToolCallID   string          `json:"toolCallId,omitempty"`
	Description  string          `json:"description,omitempty"`
	Prompt       string          `json:"prompt,omitempty"`
	SubagentType json.RawMessage `json:"subagentType,omitempty"`
	Model        string          `json:"model,omitempty"`
	AgentID      string          `json:"agentId,omitempty"`
	DurationMs   int64           `json:"durationMs,omitempty"`
}

type StatusPayload struct {
	Message string
}

type ACPDriverExtensionPayload struct {
	Driver       string          `json:"driver"`
	Method       string          `json:"method"`
	Interactive  bool            `json:"interactive"`
	SemanticKind string          `json:"semanticKind,omitempty"`
	ToolCallID   string          `json:"toolCallId,omitempty"`
	Summary      string          `json:"summary,omitempty"`
	Payload      json.RawMessage `json:"payload"`
}

type ACPDriverExtensionResponse struct {
	Driver          string          `json:"driver"`
	Method          string          `json:"method"`
	ToolCallID      string          `json:"toolCallId,omitempty"`
	ResponsePayload json.RawMessage `json:"responsePayload"`
}

type AttachmentExtensionInfo struct {
	Driver       string    `json:"driver"`
	Method       string    `json:"method"`
	Interactive  bool      `json:"interactive"`
	SemanticKind string    `json:"semanticKind,omitempty"`
	ToolCallID   string    `json:"toolCallId,omitempty"`
	Summary      string    `json:"summary,omitempty"`
	ObservedAt   time.Time `json:"observedAt"`
}

type AttachmentPendingRequestInfo struct {
	Driver       string    `json:"driver"`
	Method       string    `json:"method"`
	Interactive  bool      `json:"interactive"`
	SemanticKind string    `json:"semanticKind,omitempty"`
	ToolCallID   string    `json:"toolCallId,omitempty"`
	Summary      string    `json:"summary,omitempty"`
	CreatedAt    time.Time `json:"createdAt"`
}

type PermissionOption struct {
	ID    string
	Label string
}

type PermissionRequest struct {
	SessionID string
	ToolTitle string
	Options   []PermissionOption
	Raw       any
}

type PermissionDecision string

const (
	PermissionAllowOnce   PermissionDecision = "allow-once"
	PermissionAllowAlways PermissionDecision = "allow-always"
	PermissionRejectOnce  PermissionDecision = "reject-once"
)

type PromptRequest struct {
	Text          string
	OnEvent       func(ACPEvent)
	OnPermission  func(PermissionRequest) (PermissionDecision, error)
	OnInteractive func(context.Context, ACPDriverExtensionPayload) (json.RawMessage, error)
}

type PromptResult struct {
	StopReason string
	FinalText  string
}

type AttachRequest struct {
	SessionKey string
	User       *user.User
	DriverID   string
	Transport  string
	CWD        string
	Mode       string
	SessionID  string
}

type AttachmentInfo struct {
	SessionKey       string
	UserID           string
	Attached         bool
	SessionID        string
	CWD              string
	Mode             string
	Transport        string
	Driver           string
	LastActivity     time.Time
	BufferedEvents   int
	LastAssistant    string
	CurrentState     string
	PromptRunning    bool
	Todos            []TodoItem
	LastPlanName     string
	LastPlanOverview string
	LastQuestion     string
	CurrentModel     string
	AvailableModels  []ACPModelOption
	RecentExtensions []AttachmentExtensionInfo
	PendingRequests  []AttachmentPendingRequestInfo
}

type PromptOptions struct {
	OnEvent func(ACPEvent)
}
