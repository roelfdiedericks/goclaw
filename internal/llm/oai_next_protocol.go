package llm

import "encoding/json"

// =============================================================================
// Request Types — sent to OpenAI via WebSocket as response.create events
// =============================================================================

// oaiRequest is the top-level event sent over WebSocket.
type oaiRequest struct {
	Type               string         `json:"type"`                   // "response.create"
	Model              string         `json:"model,omitempty"`        // e.g. "gpt-5.2"
	Instructions       string         `json:"instructions,omitempty"` // system prompt
	Input              []oaiInputItem `json:"input,omitempty"`        // conversation items
	Tools              []oaiToolDef   `json:"tools,omitempty"`        // tool definitions
	Store              *bool          `json:"store,omitempty"`        // server-side persistence
	PreviousResponseID string         `json:"previous_response_id,omitempty"`
	MaxOutputTokens    int            `json:"max_output_tokens,omitempty"`
	Generate           *bool          `json:"generate,omitempty"` // false = warmup only
}

// oaiInputItem represents an item in the input array (message, function_call, function_call_output).
// Uses a flat struct with Type as discriminator; omitempty keeps unused fields out of JSON.
type oaiInputItem struct {
	Type      string           `json:"type"`                // "message", "function_call", "function_call_output"
	Role      string           `json:"role,omitempty"`      // "user", "assistant", "developer"
	Content   []oaiContentPart `json:"content,omitempty"`   // for messages
	ID        string           `json:"id,omitempty"`        // for function_call (the call's own ID)
	CallID    string           `json:"call_id,omitempty"`   // for function_call and function_call_output
	Name      string           `json:"name,omitempty"`      // for function_call (tool name)
	Arguments string           `json:"arguments,omitempty"` // for function_call (JSON string)
	Output    string           `json:"output,omitempty"`    // for function_call_output (result text)
	Status    string           `json:"status,omitempty"`    // for function_call ("completed")
}

// oaiContentPart represents a content part within a message.
type oaiContentPart struct {
	Type     string `json:"type"`                // "input_text", "input_image", "output_text"
	Text     string `json:"text,omitempty"`      // for text parts
	ImageURL string `json:"image_url,omitempty"` // for input_image (data URI)
}

// oaiToolDef represents a tool definition in the tools array.
type oaiToolDef struct {
	Type        string          `json:"type"`                  // "function", "web_search", "code_interpreter"
	Name        string          `json:"name,omitempty"`        // for function tools
	Description string          `json:"description,omitempty"` // for function tools
	Parameters  json.RawMessage `json:"parameters,omitempty"`  // JSON schema for function tools
	Container   *oaiContainer   `json:"container,omitempty"`   // for code_interpreter
}

// oaiContainer specifies the execution environment for code_interpreter.
type oaiContainer struct {
	Type string `json:"type"` // "auto" or a container ID
}

// =============================================================================
// Streaming Event Types — received from OpenAI via WebSocket
// =============================================================================

// oaiEvent is a generic streaming event envelope. The Type field determines
// which nested fields are populated.
type oaiEvent struct {
	Type     string          `json:"type"`
	Response *oaiResponseObj `json:"response,omitempty"` // for response.created, response.done
	Item     *oaiOutputItem  `json:"item,omitempty"`     // for output_item.added, output_item.done
	Delta    string          `json:"delta,omitempty"`    // for content_part.delta (text)
	Part     *oaiContentPart `json:"part,omitempty"`     // for content_part.added

	// Structured delta for non-text deltas (reasoning, etc.)
	ContentDelta *oaiContentDelta `json:"content_delta,omitempty"`

	// For error events
	Status int       `json:"status,omitempty"`
	Error  *oaiError `json:"error,omitempty"`

	// Output index tracking
	OutputIndex int `json:"output_index,omitempty"`
}

// oaiContentDelta carries the actual delta payload for content_part.delta events.
type oaiContentDelta struct {
	Type string `json:"type,omitempty"` // "output_text_delta", etc.
	Text string `json:"text,omitempty"` // text content delta
}

// oaiResponseObj represents the response object in response.created / response.done events.
type oaiResponseObj struct {
	ID     string          `json:"id"`
	Status string          `json:"status,omitempty"` // "completed", "failed", "cancelled"
	Output []oaiOutputItem `json:"output,omitempty"`
	Usage  *oaiUsage       `json:"usage,omitempty"`
}

// oaiOutputItem represents an output item (message, function_call, web_search_call, etc.).
type oaiOutputItem struct {
	Type      string           `json:"type"`                // "message", "function_call", "web_search_call", "code_interpreter_call"
	ID        string           `json:"id,omitempty"`        // unique item ID
	Role      string           `json:"role,omitempty"`      // for messages ("assistant")
	Content   []oaiContentPart `json:"content,omitempty"`   // for messages
	CallID    string           `json:"call_id,omitempty"`   // for function_call
	Name      string           `json:"name,omitempty"`      // for function_call (tool name)
	Arguments string           `json:"arguments,omitempty"` // for function_call (JSON string)
	Status    string           `json:"status,omitempty"`    // "completed", "in_progress", "failed"
	Action    json.RawMessage  `json:"action,omitempty"`    // for web_search_call (search query etc.)
}

// oaiUsage contains token usage from response.done events.
type oaiUsage struct {
	InputTokens         int              `json:"input_tokens"`
	OutputTokens        int              `json:"output_tokens"`
	InputTokensDetails  *oaiTokenDetails `json:"input_tokens_details,omitempty"`
	OutputTokensDetails *oaiTokenDetails `json:"output_tokens_details,omitempty"`
}

// oaiTokenDetails provides breakdown of token usage (cached, reasoning, etc.).
type oaiTokenDetails struct {
	CachedTokens    int `json:"cached_tokens,omitempty"`
	ReasoningTokens int `json:"reasoning_tokens,omitempty"`
}

// oaiError represents an error from the server.
type oaiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// =============================================================================
// Constants
// =============================================================================

const (
	oaiEventResponseCreated          = "response.created"
	oaiEventOutputItemAdded          = "response.output_item.added"
	oaiEventContentPartAdded         = "response.content_part.added"
	oaiEventContentPartDone          = "response.content_part.done"
	oaiEventOutputTextDelta          = "response.output_text.delta"
	oaiEventReasoningTextDelta       = "response.reasoning_text.delta"
	oaiEventReasoningSummaryDelta    = "response.reasoning_summary_text.delta"
	oaiEventOutputItemDone           = "response.output_item.done"
	oaiEventResponseDone             = "response.done"
	oaiEventResponseCompleted        = "response.completed"
	oaiEventError                    = "error"

	oaiItemTypeMessage            = "message"
	oaiItemTypeFunctionCall       = "function_call"
	oaiItemTypeFunctionCallOutput = "function_call_output"
	oaiItemTypeWebSearchCall      = "web_search_call"
	oaiItemTypeCodeInterpreter    = "code_interpreter_call"
)
