package acp_control

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/roelfdiedericks/goclaw/internal/acp"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/types"
)

type Tool struct{}

func NewTool() *Tool { return &Tool{} }

func (t *Tool) Name() string { return "acp_control" }

func (t *Tool) Description() string {
	return "Control ACP sessions through the ACP manager. Use to attach, detach, close, cancel, change mode, list or set the model, or steer an ACP session. Steering detaches by default unless stayAttached is true."
}

func (t *Tool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"enum":        []string{"attach", "detach", "close", "cancel", "set_mode", "list_models", "set_model", "steer"},
				"description": "ACP control action to perform.",
			},
			"driver": map[string]any{
				"type":        "string",
				"description": "Driver ID for attach. Defaults to cursor.",
			},
			"cwd": map[string]any{
				"type":        "string",
				"description": "Working directory for new attach/create flows.",
			},
			"mode": map[string]any{
				"type":        "string",
				"description": "Desired ACP session mode, such as agent, plan, or ask.",
			},
			"sessionId": map[string]any{
				"type":        "string",
				"description": "Existing ACP session ID to load/attach instead of creating a new one.",
			},
			"message": map[string]any{
				"type":        "string",
				"description": "Steering message to send into the existing ACP session.",
			},
			"model": map[string]any{
				"type":        "string",
				"description": "Friendly model alias to set on the attached ACP session, such as claude-4.6-opus-high-thinking.",
			},
			"stayAttached": map[string]any{
				"type":        "boolean",
				"description": "Keep the ACP session attached after steering. Defaults to false.",
			},
		},
		"required": []string{"action"},
	}
}

type controlInput struct {
	Action       string `json:"action"`
	Driver       string `json:"driver"`
	CWD          string `json:"cwd"`
	Mode         string `json:"mode"`
	SessionID    string `json:"sessionId"`
	Message      string `json:"message"`
	Model        string `json:"model"`
	StayAttached bool   `json:"stayAttached"`
}

func (t *Tool) Execute(ctx context.Context, input json.RawMessage) (*types.ToolResult, error) {
	var in controlInput
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, fmt.Errorf("invalid input: %w", err)
	}
	sc := types.GetSessionContext(ctx)
	if sc == nil || sc.User == nil {
		return nil, fmt.Errorf("missing session context")
	}
	mgr := acp.GetManager()
	if mgr == nil {
		return nil, fmt.Errorf("ACP manager not initialized")
	}
	if !sc.User.CanUseACP() {
		return nil, fmt.Errorf("ACP is not allowed for this user")
	}

	sessionKey := sc.SessionKey
	if strings.TrimSpace(sessionKey) == "" {
		return nil, fmt.Errorf("missing session key")
	}

	switch strings.ToLower(strings.TrimSpace(in.Action)) {
	case "attach":
		info, err := mgr.Attach(ctx, acp.AttachRequest{
			SessionKey: sessionKey,
			User:       sc.User,
			DriverID:   in.Driver,
			Transport:  acp.TransportLocalStdio,
			CWD:        in.CWD,
			Mode:       in.Mode,
			SessionID:  in.SessionID,
		})
		if err != nil {
			return nil, err
		}
		return infoResult("ACP attached.", info), nil
	case "detach":
		info, err := mgr.Detach(sessionKey)
		if err != nil {
			return nil, err
		}
		return infoResult("ACP detached.", info), nil
	case "close":
		if err := mgr.Close(ctx, sessionKey); err != nil {
			return nil, err
		}
		return types.TextResult("ACP session closed."), nil
	case "cancel":
		if err := mgr.Cancel(ctx, sessionKey); err != nil {
			return nil, err
		}
		return types.TextResult("ACP session cancelled."), nil
	case "set_mode":
		info, err := mgr.SetMode(ctx, sessionKey, in.Mode)
		if err != nil {
			return nil, err
		}
		return infoResult("ACP mode updated.", info), nil
	case "list_models":
		models, err := mgr.ListModels(ctx, sessionKey)
		if err != nil {
			return nil, err
		}
		return modelListResult(models), nil
	case "set_model":
		info, err := mgr.SetModel(ctx, sessionKey, in.Model)
		if err != nil {
			return nil, err
		}
		return infoResult("ACP model updated.", info), nil
	case "steer":
		if strings.TrimSpace(in.Message) == "" {
			return nil, fmt.Errorf("message is required for steer")
		}
		L_info("acp_control: steering session", "sessionKey", sessionKey, "mode", in.Mode, "stayAttached", in.StayAttached)
		if !in.StayAttached && mgr.IsAttached(sessionKey) {
			defer func() {
				if _, err := mgr.Detach(sessionKey); err != nil {
					L_warn("acp_control: failed to detach after steer", "sessionKey", sessionKey, "error", err)
				}
			}()
		}
		result, err := mgr.Steer(ctx, sessionKey, in.Message, acp.PromptOptions{})
		if err != nil {
			return nil, err
		}
		return types.TextResult(result.FinalText), nil
	default:
		return nil, fmt.Errorf("unknown action: %s", in.Action)
	}
}

func infoResult(prefix string, info *acp.AttachmentInfo) *types.ToolResult {
	if info == nil {
		return types.TextResult(prefix)
	}
	text := fmt.Sprintf(
		"%s\nsessionKey=%s\nattached=%t\nsessionId=%s\ndriver=%s\ntransport=%s\nmode=%s\ncwd=%s\nmodel=%s\nstate=%s\nbufferedEvents=%d",
		prefix,
		info.SessionKey,
		info.Attached,
		info.SessionID,
		info.Driver,
		info.Transport,
		info.Mode,
		info.CWD,
		info.CurrentModel,
		info.CurrentState,
		info.BufferedEvents,
	)
	return types.TextResult(text)
}

func modelListResult(models []acp.ACPModelOption) *types.ToolResult {
	if len(models) == 0 {
		return types.TextResult("No ACP models are available for this session.")
	}
	var text strings.Builder
	text.WriteString("ACP models:\n")
	for _, model := range models {
		prefix := "- "
		if model.Current {
			prefix = "* "
		}
		text.WriteString(prefix)
		text.WriteString(model.FriendlyID)
		if model.Name != "" {
			text.WriteString(" - ")
			text.WriteString(model.Name)
		}
		text.WriteString("\n")
	}
	return types.TextResult(strings.TrimSpace(text.String()))
}
