// Package web provides browser-based setup wizard and configuration editor
package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/roelfdiedericks/goclaw/internal/bus"
	telegrampairing "github.com/roelfdiedericks/goclaw/internal/channels/telegram"
	whatsapppairing "github.com/roelfdiedericks/goclaw/internal/channels/whatsapp"
	"github.com/roelfdiedericks/goclaw/internal/config"
	"github.com/roelfdiedericks/goclaw/internal/config/forms"
	"github.com/roelfdiedericks/goclaw/internal/configapply"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/metadata"
	"github.com/roelfdiedericks/goclaw/internal/sandbox"
	"github.com/roelfdiedericks/goclaw/internal/setup"
	setuppairing "github.com/roelfdiedericks/goclaw/internal/setup/pairing"
)

// WizardStep defines a single step in the setup wizard
type WizardStep struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

// WizardSteps defines all steps in the setup wizard
var WizardSteps = []WizardStep{
	{ID: "welcome", Title: "Welcome to GoClaw", Description: "Let's get you set up with your personal AI assistant."},
	{ID: "agent", Title: "Agent Identity", Description: "Set your assistant name, emoji, and typing style."},
	{ID: "workspace", Title: "Workspace", Description: "Choose where GoClaw will store files and configurations."},
	{ID: "user", Title: "Owner Account", Description: "Create your owner account for authentication."},
	{ID: "channels", Title: "Communication Channels", Description: "Configure how you'll interact with GoClaw."},
	{ID: "pairing", Title: "Channel Pairing", Description: "Pair each enabled owner channel before you continue."},
	{ID: "llm", Title: "LLM Provider", Description: "Set up your language model provider (Anthropic, OpenAI, etc.)."},
	{ID: "voice", Title: "Voice Settings", Description: "Configure speech-to-text and voice LLM (optional)."},
	{ID: "security", Title: "Security & Skills", Description: "Configure sandboxing and skill installation sources."},
	{ID: "review", Title: "Review & Finish", Description: "Review your settings and complete the setup."},
}

// WizardState holds the current wizard session state
type WizardState struct {
	Step            int               `json:"step"`
	Data            *setup.WizardData `json:"data"`
	PairingSessions map[string]string `json:"-"`
	mu              sync.Mutex
}

// WizardAPI provides API endpoints for the wizard
type WizardAPI struct {
	state      *WizardState
	configPath string
}

// NewWizardAPI creates a new wizard API handler
func NewWizardAPI(configPath string, _ configapply.Caller) *WizardAPI {
	data := buildWizardData(configPath)
	telegrampairing.RegisterPairingCommands()
	whatsapppairing.RegisterPairingCommands()

	return &WizardAPI{
		state: &WizardState{
			Step:            1,
			Data:            data,
			PairingSessions: newWizardPairingSessions(),
		},
		configPath: configPath,
	}
}

// Reset reloads wizard data and returns the wizard to step 1.
func (w *WizardAPI) Reset() {
	w.state.mu.Lock()
	defer w.state.mu.Unlock()
	w.state.Step = 1
	w.state.Data = buildWizardData(w.configPath)
	w.state.PairingSessions = newWizardPairingSessions()
}

func buildWizardData(configPath string) *setup.WizardData {
	data := setup.NewWizardData()

	// Try to load existing GoClaw config
	loadResult, err := loadWizardConfig(configPath)
	if err == nil && loadResult.Config != nil {
		data.LoadFromExisting(loadResult.Config, loadResult.SourcePath)
		L_info("wizard: loaded existing config", "path", loadResult.SourcePath)
	} else if config.IsMissingOrIncompleteConfigError(err) {
		defaultResult, defaultsErr := config.LoadDefaults()
		if defaultsErr != nil {
			L_warn("wizard: failed to seed defaults", "error", defaultsErr)
		} else if defaultResult.Config != nil {
			data.LoadFromDefaults(defaultResult.Config)
			L_info("wizard: seeded from config defaults")
		}
	} else if err != nil {
		L_warn("wizard: failed to load config", "error", err)
	}

	// Check for OpenClaw installation
	data.LoadFromOpenClaw()
	if data.OpenClawExists {
		L_info("wizard: detected OpenClaw installation")
	}

	return data
}

func newWizardPairingSessions() map[string]string {
	seed := time.Now().UnixNano()
	return map[string]string{
		"telegram": fmt.Sprintf("wizard-telegram-%d", seed),
		"whatsapp": fmt.Sprintf("wizard-whatsapp-%d", seed),
	}
}

func resetWizardPairingState(data *setup.WizardData, sessions map[string]string) map[string]string {
	if data == nil {
		return newWizardPairingSessions()
	}

	for channel, sessionID := range sessions {
		if strings.TrimSpace(sessionID) == "" {
			continue
		}
		component := channel + ".pairing"
		_ = bus.SendCommandWithSource(component, "cancel", setuppairing.CancelRequest{
			BaseRequest: setuppairing.BaseRequest{
				SessionID: sessionID,
				Surface:   "web-wizard",
			},
		}, "http", "")
	}

	data.ResetPairingStage()
	return newWizardPairingSessions()
}

func wizardStepsFor(data *setup.WizardData) []WizardStep {
	steps := make([]WizardStep, 0, len(WizardSteps))
	for _, step := range WizardSteps {
		if step.ID == "pairing" && (data == nil || (!data.TelegramEnabled && !data.WhatsAppEnabled)) {
			continue
		}
		steps = append(steps, step)
	}
	return steps
}

func currentWizardStep(state *WizardState) []WizardStep {
	return wizardStepsFor(state.Data)
}

// HandleGetState returns the current wizard state
func (w *WizardAPI) HandleGetState(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(rw, http.StatusMethodNotAllowed, APIResponse{
			Success: false,
			Message: "Method not allowed",
		})
		return
	}

	w.state.mu.Lock()
	defer w.state.mu.Unlock()
	steps := currentWizardStep(w.state)
	if w.state.Step > len(steps) {
		w.state.Step = len(steps)
	}

	writeJSON(rw, http.StatusOK, APIResponse{
		Success: true,
		Data: map[string]interface{}{
			"step":       w.state.Step,
			"totalSteps": len(steps),
			"steps":      steps,
			"data":       w.state.Data,
		},
	})
}

// HandleGetStep returns the FormDef and current data for a specific step
func (w *WizardAPI) HandleGetStep(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(rw, http.StatusMethodNotAllowed, APIResponse{
			Success: false,
			Message: "Method not allowed",
		})
		return
	}

	w.state.mu.Lock()
	step := w.state.Step
	data := w.state.Data
	steps := currentWizardStep(w.state)
	w.state.mu.Unlock()

	if step < 1 || step > len(steps) {
		writeJSON(rw, http.StatusBadRequest, APIResponse{
			Success: false,
			Message: "Invalid step",
		})
		return
	}

	stepDef := steps[step-1]
	formDef := getStepFormDef(stepDef.ID, data)

	var formHTML string
	if stepDef.ID == "pairing" {
		formHTML = renderPairingStepHTML(data)
	} else if formDef != nil {
		applyWizardFormDefaults(w.state.Data, formDef)
		html, err := RenderFormHTML(*formDef, "wizardData")
		if err != nil {
			L_warn("wizard: failed to render form", "step", stepDef.ID, "error", err)
		} else {
			formHTML = string(html)
		}
	}

	writeJSON(rw, http.StatusOK, APIResponse{
		Success: true,
		Data: map[string]interface{}{
			"step":     step,
			"stepDef":  stepDef,
			"formHTML": formHTML,
			"data":     data,
		},
	})
}

// HandleSubmitStep validates and saves data for the current step
func (w *WizardAPI) HandleSubmitStep(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(rw, http.StatusMethodNotAllowed, APIResponse{
			Success: false,
			Message: "Method not allowed",
		})
		return
	}

	var payload map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeJSON(rw, http.StatusBadRequest, APIResponse{
			Success: false,
			Message: "Invalid JSON payload",
		})
		return
	}

	w.state.mu.Lock()
	defer w.state.mu.Unlock()
	steps := currentWizardStep(w.state)

	// Update wizard data from payload
	if err := updateWizardData(w.state.Data, payload); err != nil {
		writeJSON(rw, http.StatusBadRequest, APIResponse{
			Success: false,
			Message: err.Error(),
		})
		return
	}

	// Validate current step
	if errors := validateStep(steps[w.state.Step-1].ID, w.state.Data); len(errors) > 0 {
		writeJSON(rw, http.StatusBadRequest, APIResponse{
			Success: false,
			Errors:  errors,
		})
		return
	}

	// Advance to next step
	steps = currentWizardStep(w.state)
	if w.state.Step < len(steps) {
		w.state.Step++
	}

	writeJSON(rw, http.StatusOK, APIResponse{
		Success: true,
		Data: map[string]interface{}{
			"step": w.state.Step,
		},
	})
}

// HandlePrevStep goes back to the previous step
func (w *WizardAPI) HandlePrevStep(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(rw, http.StatusMethodNotAllowed, APIResponse{
			Success: false,
			Message: "Method not allowed",
		})
		return
	}

	w.state.mu.Lock()
	defer w.state.mu.Unlock()
	steps := currentWizardStep(w.state)

	if w.state.Step > 1 {
		if w.state.Step <= len(steps) && steps[w.state.Step-1].ID == "pairing" {
			w.state.PairingSessions = resetWizardPairingState(w.state.Data, w.state.PairingSessions)
		}
		w.state.Step--
	}

	writeJSON(rw, http.StatusOK, APIResponse{
		Success: true,
		Data: map[string]interface{}{
			"step": w.state.Step,
		},
	})
}

// HandleFinish saves the final configuration
func (w *WizardAPI) HandleFinish(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(rw, http.StatusMethodNotAllowed, APIResponse{
			Success: false,
			Message: "Method not allowed",
		})
		return
	}

	w.state.mu.Lock()
	data := w.state.Data
	w.state.mu.Unlock()

	// Save configuration using the existing setup logic
	if err := setup.SaveWizardConfigToPath(data, w.configPath); err != nil {
		L_error("wizard: failed to save config", "error", err)
		writeJSON(rw, http.StatusInternalServerError, APIResponse{
			Success: false,
			Message: fmt.Sprintf("Failed to save configuration: %v", err),
		})
		return
	}

	L_info("wizard: configuration saved successfully")

	writeJSON(rw, http.StatusOK, APIResponse{
		Success: true,
		Message: "Configuration saved successfully",
	})
}

// HandlePairingAction handles wizard pairing start/status/cancel requests.
func (w *WizardAPI) HandlePairingAction(rw http.ResponseWriter, r *http.Request) {
	channel, action, ok := parseWizardPairingPath(r.URL.Path)
	if !ok {
		writeJSON(rw, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid pairing path"})
		return
	}

	w.state.mu.Lock()
	data := w.state.Data
	sessionID := w.state.PairingSessions[channel]
	w.state.mu.Unlock()

	switch action {
	case "start":
		var res bus.CommandResult
		switch channel {
		case "telegram":
			data.UserTelegramID = ""
			res = bus.SendCommandWithSource("telegram.pairing", "start", setuppairing.TelegramStartRequest{
				StartRequest: setuppairing.StartRequest{
					BaseRequest: setuppairing.BaseRequest{SessionID: sessionID, Surface: "web-wizard"},
				},
				BotToken: data.TelegramToken,
			}, "http", "")
		case "whatsapp":
			data.UserWhatsAppID = ""
			res = bus.SendCommandWithSource("whatsapp.pairing", "start", setuppairing.WhatsAppStartRequest{
				StartRequest: setuppairing.StartRequest{
					BaseRequest: setuppairing.BaseRequest{SessionID: sessionID, Surface: "web-wizard"},
				},
			}, "http", "")
		default:
			writeJSON(rw, http.StatusBadRequest, APIResponse{Success: false, Message: "Unsupported pairing channel"})
			return
		}
		writePairingResult(rw, res)
	case "status":
		component := channel + ".pairing"
		res := bus.SendCommandWithSource(component, "status", setuppairing.StatusRequest{
			BaseRequest: setuppairing.BaseRequest{SessionID: sessionID, Surface: "web-wizard"},
		}, "http", "")
		if status, ok := res.Data.(setuppairing.Status); ok {
			w.stagePairingIdentity(channel, status)
		} else if statusPtr, ok := res.Data.(*setuppairing.Status); ok && statusPtr != nil {
			w.stagePairingIdentity(channel, *statusPtr)
		}
		writePairingResult(rw, res)
	case "cancel":
		component := channel + ".pairing"
		res := bus.SendCommandWithSource(component, "cancel", setuppairing.CancelRequest{
			BaseRequest: setuppairing.BaseRequest{SessionID: sessionID, Surface: "web-wizard"},
		}, "http", "")
		writePairingResult(rw, res)
	default:
		writeJSON(rw, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Unsupported pairing action"})
	}
}

func (w *WizardAPI) stagePairingIdentity(channel string, status setuppairing.Status) {
	if status.State != setuppairing.StatePaired || status.Identity == nil {
		return
	}
	w.state.mu.Lock()
	defer w.state.mu.Unlock()
	switch channel {
	case "telegram":
		w.state.Data.UserTelegramID = status.Identity.ID
	case "whatsapp":
		w.state.Data.UserWhatsAppID = status.Identity.ID
	}
}

func writePairingResult(rw http.ResponseWriter, res bus.CommandResult) {
	if res.Error != nil {
		writeJSON(rw, http.StatusBadRequest, APIResponse{Success: false, Message: res.Message})
		return
	}
	writeJSON(rw, http.StatusOK, APIResponse{Success: true, Message: res.Message, Data: res.Data})
}

func parseWizardPairingPath(path string) (channel string, action string, ok bool) {
	trimmed := strings.TrimPrefix(path, "/setup/api/wizard/pairing/")
	parts := strings.Split(strings.Trim(trimmed, "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func renderPairingStepHTML(data *setup.WizardData) string {
	var sections []string
	if data.TelegramEnabled {
		sections = append(sections, `
<div class="card mb-3" data-pairing-channel="telegram">
  <div class="card-body">
    <div class="d-flex justify-content-between align-items-start gap-3 flex-wrap">
      <div>
        <h6 class="mb-1">Telegram</h6>
        <p class="text-muted mb-2">Send the one-time code to your bot from the owner account.</p>
      </div>
      <span class="badge text-bg-secondary js-pairing-badge">Not started</span>
    </div>
    <div class="small text-muted mb-2 js-pairing-message">Telegram owner pairing has not started yet.</div>
    <div class="alert alert-success d-none js-pairing-success">
      <div class="d-flex align-items-start gap-2">
        <i class="bi bi-check-circle-fill fs-5"></i>
        <div>
          <div class="fw-semibold">Telegram pairing complete</div>
          <div class="small js-pairing-success-text">You can continue to the next step.</div>
        </div>
      </div>
    </div>
    <div class="alert alert-light border d-none js-pairing-artifact"></div>
    <div class="small text-muted mb-2 js-pairing-identity"></div>
    <div class="d-flex gap-2 flex-wrap">
      <button type="button" class="btn btn-sm btn-primary js-pairing-start">Start Pairing</button>
      <button type="button" class="btn btn-sm btn-outline-secondary js-pairing-cancel d-none">Cancel</button>
      <button type="button" class="btn btn-sm btn-outline-secondary js-pairing-refresh">Refresh</button>
    </div>
  </div>
</div>`)
	}
	if data.WhatsAppEnabled {
		sections = append(sections, `
<div class="card mb-3" data-pairing-channel="whatsapp">
  <div class="card-body">
    <div class="d-flex justify-content-between align-items-start gap-3 flex-wrap">
      <div>
        <h6 class="mb-1">WhatsApp</h6>
        <p class="text-muted mb-2">Scan the QR code from WhatsApp on your phone.</p>
      </div>
      <span class="badge text-bg-secondary js-pairing-badge">Not started</span>
    </div>
    <div class="small text-muted mb-2 js-pairing-message">WhatsApp owner pairing has not started yet.</div>
    <div class="alert alert-success d-none js-pairing-success">
      <div class="d-flex align-items-start gap-2">
        <i class="bi bi-check-circle-fill fs-5"></i>
        <div>
          <div class="fw-semibold">WhatsApp pairing complete</div>
          <div class="small js-pairing-success-text">You can continue to the next step.</div>
        </div>
      </div>
    </div>
    <div class="alert alert-light border d-none js-pairing-artifact"></div>
    <div class="small text-muted mb-2 js-pairing-identity"></div>
    <div class="d-flex gap-2 flex-wrap">
      <button type="button" class="btn btn-sm btn-primary js-pairing-start">Start Pairing</button>
      <button type="button" class="btn btn-sm btn-outline-secondary js-pairing-cancel d-none">Cancel</button>
      <button type="button" class="btn btn-sm btn-outline-secondary js-pairing-refresh">Refresh</button>
    </div>
  </div>
</div>`)
	}
	if len(sections) == 0 {
		return `<div class="alert alert-info mb-0">No channel pairing is required for the current setup.</div>`
	}
	return `<div class="js-wizard-pairing-step"><div class="alert alert-info">Each enabled channel must be paired before you can continue. Successful pairing only stages the owner identity until you finish setup.</div>` + strings.Join(sections, "") + `</div>`
}

func loadWizardConfig(configPath string) (*config.LoadResult, error) {
	if configPath == "" {
		return config.Load()
	}
	return config.LoadFromPath(configPath)
}

// HandleGetModels returns available models for a provider
func (w *WizardAPI) HandleGetModels(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(rw, http.StatusMethodNotAllowed, APIResponse{
			Success: false,
			Message: "Method not allowed",
		})
		return
	}

	// Extract provider from path: /setup/api/wizard/models/{provider}
	providerID := r.URL.Path[len("/setup/api/wizard/models/"):]
	if providerID == "" {
		writeJSON(rw, http.StatusBadRequest, APIResponse{
			Success: false,
			Message: "Provider ID required",
		})
		return
	}

	meta := metadata.Get()
	modelIDs := meta.GetKnownChatModels(providerID)
	defaultLarge, _ := meta.GetDefaultModels(providerID)

	var models []map[string]string
	for _, mid := range modelIDs {
		label := mid
		if m, ok := meta.GetModel(providerID, mid); ok {
			label = fmt.Sprintf("%s (%s)", m.Name, mid)
		}
		if mid == defaultLarge {
			label += " - default"
		}
		models = append(models, map[string]string{
			"value": mid,
			"label": label,
		})
	}

	writeJSON(rw, http.StatusOK, APIResponse{
		Success: true,
		Data: map[string]interface{}{
			"models":       models,
			"defaultModel": defaultLarge,
		},
	})
}

// getStepFormDef returns the FormDef for a wizard step
func getStepFormDef(stepID string, data *setup.WizardData) *forms.FormDef {
	switch stepID {
	case "welcome":
		return &forms.FormDef{
			Title:       "Welcome",
			Description: "GoClaw is ready to be configured. Click Next to begin.",
			Sections: []forms.Section{
				{
					Title:    "OpenClaw Migration",
					ShowWhen: "OpenClawExists=true",
					Desc:     "An existing OpenClaw installation was detected at ~/.openclaw/",
					Fields: []forms.Field{
						{
							Name:    "OpenClawImport",
							Title:   "Import settings from OpenClaw",
							Type:    forms.Toggle,
							Desc:    "Import workspace path, Telegram token, and other settings from your OpenClaw configuration",
							Default: true,
						},
					},
				},
				{
					Title:    "Existing Configuration",
					ShowWhen: "ConfigExists=true",
					Desc:     "An existing GoClaw configuration was found. Your current settings have been loaded.",
				},
			},
		}

	case "workspace":
		return &forms.FormDef{
			Title: "Workspace Configuration",
			Sections: []forms.Section{
				{
					Title: "Working Directory",
					Fields: []forms.Field{
						{
							Name:    "WorkspacePath",
							Title:   "Workspace Path",
							Type:    forms.Text,
							Desc:    "Directory where GoClaw will store files and operate",
							Default: "~/.goclaw/workspace",
						},
					},
				},
			},
		}

	case "agent":
		return &forms.FormDef{
			Title: "Agent Identity",
			Sections: []forms.Section{
				{
					Title: "Display Settings",
					Fields: []forms.Field{
						{Name: "AgentName", Title: "Agent Name", Type: forms.Text, Required: true, Default: "GoClaw"},
						{Name: "AgentEmoji", Title: "Emoji (optional)", Type: forms.Text, Default: "🐾", Desc: "Pick an emoji to prefix the agent name"},
						{Name: "AgentTyping", Title: "Typing Text (optional)", Type: forms.Text, Desc: fmt.Sprintf("Custom typing indicator text (max %d chars)", setup.WizardAgentTypingMaxLen)},
					},
				},
			},
		}

	case "user":
		return &forms.FormDef{
			Title: "Owner Account",
			Sections: []forms.Section{
				{
					Title: "Account Details",
					Fields: []forms.Field{
						{
							Name:     "UserName",
							Title:    "Username",
							Type:     forms.Text,
							Desc:     "Lowercase alphanumeric, used for login",
							Required: true,
						},
						{
							Name:     "UserDisplayName",
							Title:    "Display Name",
							Type:     forms.Text,
							Desc:     "How GoClaw will address you",
							Required: true,
						},
						{
							Name:  "UserPassword",
							Title: "HTTP Password",
							Type:  forms.Secret,
							Desc:  "Password for web interface login",
						},
						{
							Name:  "UserPasswordConf",
							Title: "Confirm Password",
							Type:  forms.Secret,
						},
						{
							Name:  "UserTelegramID",
							Title: "Telegram ID (optional)",
							Type:  forms.Text,
							Desc:  "Owner Telegram identity. This can also be filled by the pairing step.",
						},
						{
							Name:  "UserWhatsAppID",
							Title: "WhatsApp ID (optional)",
							Type:  forms.Text,
							Desc:  "Owner WhatsApp identity. This can also be filled by the pairing step.",
						},
					},
				},
			},
		}

	case "channels":
		return &forms.FormDef{
			Title: "Communication Channels",
			Sections: []forms.Section{
				{
					Title: "HTTP Server",
					Fields: []forms.Field{
						{Name: "HTTPEnabled", Title: "Enable HTTP Server", Type: forms.Toggle, Default: true},
						{Name: "HTTPListen", Title: "Listen Address", Type: forms.Text, Default: "127.0.0.1:1337"},
					},
				},
				{
					Title: "Telegram Bot",
					Fields: []forms.Field{
						{Name: "TelegramEnabled", Title: "Enable Telegram Bot", Type: forms.Toggle},
						{Name: "TelegramToken", Title: "Bot Token", Type: forms.Secret, Desc: "Get from @BotFather on Telegram"},
					},
				},
				{
					Title: "WhatsApp",
					Fields: []forms.Field{
						{Name: "WhatsAppEnabled", Title: "Enable WhatsApp", Type: forms.Toggle, Desc: "Requires device linking after setup"},
					},
				},
			},
		}

	case "llm":
		// Build model options from metadata based on selected provider
		var modelOptions []forms.Option
		meta := metadata.Get()
		if data != nil && data.LLMProviderID != "" && data.LLMProviderID != "custom" {
			modelIDs := meta.GetKnownChatModels(data.LLMProviderID)
			defaultLarge, _ := meta.GetDefaultModels(data.LLMProviderID)
			for _, mid := range modelIDs {
				label := mid
				if m, ok := meta.GetModel(data.LLMProviderID, mid); ok {
					label = fmt.Sprintf("%s (%s)", m.Name, mid)
				}
				if mid == defaultLarge {
					label += " - default"
				}
				modelOptions = append(modelOptions, forms.Option{Value: mid, Label: label})
			}
		}

		// If no provider selected or custom, show a text field for model
		modelField := forms.Field{
			Name:  "LLMModel",
			Title: "Model",
			Type:  forms.Text,
			Desc:  "e.g., claude-sonnet-4-20250514",
		}
		if len(modelOptions) > 0 {
			modelField = forms.Field{
				Name:    "LLMModel",
				Title:   "Model",
				Type:    forms.Select,
				Options: modelOptions,
			}
		}

		return &forms.FormDef{
			Title: "LLM Provider",
			Sections: []forms.Section{
				{
					Title: "Provider Selection",
					Desc:  "Choose your primary language model provider.",
					Fields: []forms.Field{
						{
							Name:  "LLMProviderID",
							Title: "Provider",
							Type:  forms.Select,
							Options: []forms.Option{
								{Value: "anthropic", Label: "Anthropic (Claude)"},
								{Value: "openai", Label: "OpenAI (GPT)"},
								{Value: "google", Label: "Google (Gemini)"},
								{Value: "xai", Label: "xAI (Grok)"},
								{Value: "openrouter", Label: "OpenRouter"},
								{Value: "custom", Label: "Custom/Local"},
							},
						},
						{Name: "LLMAPIKey", Title: "API Key", Type: forms.Secret},
						{Name: "LLMBaseURL", Title: "Base URL (optional)", Type: forms.Text, Desc: "For custom endpoints"},
						modelField,
					},
				},
			},
		}

	case "voice":
		return &forms.FormDef{
			Title: "Voice Settings (Optional)",
			Sections: []forms.Section{
				{
					Title: "Speech-to-Text",
					Fields: []forms.Field{
						{Name: "STTEnabled", Title: "Enable Speech-to-Text", Type: forms.Toggle},
						{
							Name:  "STTModel",
							Title: "Whisper Model",
							Type:  forms.Select,
							Options: []forms.Option{
								{Value: "ggml-tiny.en.bin", Label: "Tiny (fast, less accurate)"},
								{Value: "ggml-base.en.bin", Label: "Base (balanced)"},
								{Value: "ggml-small.en.bin", Label: "Small (more accurate)"},
							},
							Default: "ggml-base.en.bin",
						},
					},
				},
				{
					Title: "Voice LLM (xAI)",
					Desc:  "Real-time voice conversations using xAI's voice API.",
					Fields: []forms.Field{
						{Name: "VoiceLLMEnabled", Title: "Enable Voice LLM", Type: forms.Toggle},
						{Name: "VoiceLLMAPIKey", Title: "xAI API Key", Type: forms.Secret},
						{
							Name:  "VoiceLLMVoice",
							Title: "Voice",
							Type:  forms.Select,
							Options: []forms.Option{
								{Value: "Eve", Label: "Eve (Female, energetic)"},
								{Value: "Ara", Label: "Ara (Female, warm)"},
								{Value: "Rex", Label: "Rex (Male, confident)"},
								{Value: "Sal", Label: "Sal (Neutral, balanced)"},
								{Value: "Leo", Label: "Leo (Male, authoritative)"},
							},
							Default: "Eve",
						},
					},
				},
			},
		}

	case "security":
		modeOptions := sandbox.SupportedModeOptions()
		return &forms.FormDef{
			Title:       "",
			Description: "",
			Sections: []forms.Section{
				{
					Title: "Sandboxing Presets",
					Desc:  fmt.Sprintf("Pick a preset first. Managed backend: %s.", sandbox.CurrentBackendDisplayName()),
					Fields: []forms.Field{
						{
							Name:  "SandboxPreset",
							Title: "Security Preset",
							Type:  forms.Select,
							Options: []forms.Option{
								{Label: "Assistant (recommended)", Value: setup.SandboxPresetAssistant},
								{Label: "Permissive", Value: setup.SandboxPresetPermissive},
								{Label: "Hardened", Value: setup.SandboxPresetHardened},
								{Label: "Custom (advanced)", Value: setup.SandboxPresetCustom},
							},
							Default: setup.SandboxPresetAssistant,
						},
					},
				},
				{
					Title:    "Custom Preset Notice",
					ShowWhen: "SandboxPreset=custom",
					Desc:     setup.SandboxPresetWarningText(setup.SandboxPresetCustom).Body,
				},
				{
					Title:    "Preset Consent",
					ShowWhen: "SandboxPreset=permissive",
					Desc:     setup.SandboxPresetWarningText(setup.SandboxPresetPermissive).Body,
					Fields: []forms.Field{
						{Name: "SandboxConsentPermissive", Title: setup.SandboxPresetWarningText(setup.SandboxPresetPermissive).Consent, Type: forms.Toggle},
					},
				},
				{
					Title:    "Preset Consent",
					ShowWhen: "SandboxPreset=assistant",
					Desc:     setup.SandboxPresetWarningText(setup.SandboxPresetAssistant).Body,
					Fields: []forms.Field{
						{Name: "SandboxConsentAssistant", Title: setup.SandboxPresetWarningText(setup.SandboxPresetAssistant).Consent, Type: forms.Toggle},
					},
				},
				{
					Title:    "Preset Consent",
					ShowWhen: "SandboxPreset=hardened",
					Desc:     setup.SandboxPresetWarningText(setup.SandboxPresetHardened).Body,
					Fields: []forms.Field{
						{Name: "SandboxConsentHardened", Title: setup.SandboxPresetWarningText(setup.SandboxPresetHardened).Consent, Type: forms.Toggle},
					},
				},
				{
					Title: "Advanced Sandbox Settings",
					Desc:  "Optional: customize sandbox mode and category toggles manually.",
					Fields: []forms.Field{
						{Name: "SandboxAdvanced", Title: "Show advanced sandbox settings", Type: forms.Toggle},
					},
					Nested: &forms.FormDef{
						Sections: []forms.Section{
							{
								ShowWhen: "SandboxAdvanced=true",
								Fields: []forms.Field{
									{Name: "SandboxEnabled", Title: "Enable Sandboxing", Type: forms.Toggle, Default: true},
								},
								Nested: &forms.FormDef{
									Sections: []forms.Section{
										{
											ShowWhen: "SandboxEnabled=true",
											Fields: []forms.Field{
												{Name: "ExecSandboxEnabled", Title: "Enable Exec Sandboxing", Type: forms.Toggle, Default: true},
												{Name: "BrowserSandboxEnabled", Title: "Enable Browser Sandboxing", Type: forms.Toggle, Default: true},
												{Name: "FileToolsSandboxEnabled", Title: "Enable File Tool Sandboxing", Type: forms.Toggle, Default: true},
												{Name: "SandboxMode", Title: "Sandbox mode", Type: forms.Select, Options: modeOptions},
											},
										},
										{
											ShowWhen: "SandboxEnabled=false",
											Desc:     "Sandbox categories and mode are not applicable while sandboxing is disabled.",
										},
									},
								},
							},
						},
					},
				},
				{
					Title: "Skill Installation Sources",
					Desc:  "Control where the agent can install skills from.",
					Fields: []forms.Field{
						{Name: "SkillsAllowEmbedded", Title: "Allow Embedded Skills", Type: forms.Toggle, Default: true},
						{Name: "SkillsAllowClawHub", Title: "Allow ClawHub (Public Repository)", Type: forms.Toggle},
						{Name: "SkillsAllowLocal", Title: "Allow Local Paths (Security Risk!)", Type: forms.Toggle},
					},
				},
			},
		}

	case "review":
		return &forms.FormDef{
			Title:       "Review & Finish",
			Description: "Review your settings below. Click Finish to save the configuration.",
		}

	default:
		return nil
	}
}

func applyWizardFormDefaults(data *setup.WizardData, def *forms.FormDef) {
	if data == nil || def == nil {
		return
	}
	rv := reflect.ValueOf(data)
	if rv.Kind() != reflect.Ptr || rv.IsNil() {
		return
	}
	seedWizardSections(rv.Elem(), data, def.Sections)
}

func seedWizardSections(rv reflect.Value, data *setup.WizardData, sections []forms.Section) {
	for _, sec := range sections {
		for _, field := range sec.Fields {
			if field.Default == nil || data.IsDirty(field.Name) || strings.Contains(field.Name, ".") {
				continue
			}
			fv := rv.FieldByName(field.Name)
			if !fv.IsValid() || !fv.CanSet() || !fv.IsZero() {
				continue
			}
			seedWizardFieldDefault(fv, field.Default)
		}
		if sec.Nested != nil {
			seedWizardSections(rv, data, sec.Nested.Sections)
		}
	}
}

func seedWizardFieldDefault(fv reflect.Value, value any) {
	if value == nil {
		return
	}
	v := reflect.ValueOf(value)
	if !v.IsValid() {
		return
	}
	if v.Type().AssignableTo(fv.Type()) {
		fv.Set(v)
		return
	}
	if v.Type().ConvertibleTo(fv.Type()) {
		fv.Set(v.Convert(fv.Type()))
	}
}

// updateWizardData updates the WizardData from a JSON payload
func updateWizardData(data *setup.WizardData, payload map[string]interface{}) error {
	// Convert payload to JSON and back to WizardData
	// This is a simple approach; a more robust solution would handle each field
	jsonData, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	// Create a temporary struct to unmarshal into
	// (direct unmarshal would reset non-payload fields)
	type wizardFields struct {
		OpenClawImport           bool   `json:"OpenClawImport"`
		AgentName                string `json:"AgentName"`
		AgentEmoji               string `json:"AgentEmoji"`
		AgentTyping              string `json:"AgentTyping"`
		WorkspacePath            string `json:"WorkspacePath"`
		UserName                 string `json:"UserName"`
		UserDisplayName          string `json:"UserDisplayName"`
		UserTelegramID           string `json:"UserTelegramID"`
		UserWhatsAppID           string `json:"UserWhatsAppID"`
		UserPassword             string `json:"UserPassword"`
		UserPasswordConf         string `json:"UserPasswordConf"`
		HTTPEnabled              bool   `json:"HTTPEnabled"`
		HTTPListen               string `json:"HTTPListen"`
		TelegramEnabled          bool   `json:"TelegramEnabled"`
		TelegramToken            string `json:"TelegramToken"`
		WhatsAppEnabled          bool   `json:"WhatsAppEnabled"`
		LLMProviderID            string `json:"LLMProviderID"`
		LLMAPIKey                string `json:"LLMAPIKey"`
		LLMBaseURL               string `json:"LLMBaseURL"`
		LLMModel                 string `json:"LLMModel"`
		STTEnabled               bool   `json:"STTEnabled"`
		STTModel                 string `json:"STTModel"`
		VoiceLLMEnabled          bool   `json:"VoiceLLMEnabled"`
		VoiceLLMAPIKey           string `json:"VoiceLLMAPIKey"`
		VoiceLLMVoice            string `json:"VoiceLLMVoice"`
		SandboxPreset            string `json:"SandboxPreset"`
		SandboxAdvanced          bool   `json:"SandboxAdvanced"`
		SandboxMode              string `json:"SandboxMode"`
		SandboxEnabled           bool   `json:"SandboxEnabled"`
		ExecSandboxEnabled       bool   `json:"ExecSandboxEnabled"`
		BrowserSandboxEnabled    bool   `json:"BrowserSandboxEnabled"`
		FileToolsSandboxEnabled  bool   `json:"FileToolsSandboxEnabled"`
		SandboxConsentPermissive bool   `json:"SandboxConsentPermissive"`
		SandboxConsentAssistant  bool   `json:"SandboxConsentAssistant"`
		SandboxConsentHardened   bool   `json:"SandboxConsentHardened"`
		SkillsAllowEmbedded      bool   `json:"SkillsAllowEmbedded"`
		SkillsAllowClawHub       bool   `json:"SkillsAllowClawHub"`
		SkillsAllowLocal         bool   `json:"SkillsAllowLocal"`
	}

	var fields wizardFields
	if err := json.Unmarshal(jsonData, &fields); err != nil {
		return err
	}

	// Handle OpenClaw import toggle
	if _, ok := payload["OpenClawImport"]; ok {
		data.ApplyOpenClawImport(fields.OpenClawImport)
	}

	if _, ok := payload["AgentName"]; ok {
		data.AgentName = fields.AgentName
		data.MarkDirty("AgentName")
	}
	if _, ok := payload["AgentEmoji"]; ok {
		data.AgentEmoji = fields.AgentEmoji
		data.MarkDirty("AgentEmoji")
	}
	if _, ok := payload["AgentTyping"]; ok {
		data.AgentTyping = fields.AgentTyping
		data.MarkDirty("AgentTyping")
	}

	// Update fields and mark dirty when values change
	if fields.WorkspacePath != "" {
		data.WorkspacePath = fields.WorkspacePath
		data.MarkDirty("WorkspacePath")
	}
	if fields.UserName != "" {
		data.UserName = fields.UserName
	}
	if fields.UserDisplayName != "" {
		data.UserDisplayName = fields.UserDisplayName
	}
	if _, ok := payload["UserTelegramID"]; ok {
		data.UserTelegramID = strings.TrimSpace(fields.UserTelegramID)
	}
	if _, ok := payload["UserWhatsAppID"]; ok {
		data.UserWhatsAppID = strings.TrimSpace(fields.UserWhatsAppID)
	}
	if fields.UserPassword != "" {
		data.UserPassword = fields.UserPassword
	}
	if fields.UserPasswordConf != "" {
		data.UserPasswordConf = fields.UserPasswordConf
	}
	data.HTTPEnabled = fields.HTTPEnabled
	data.MarkDirty("HTTPEnabled")
	if fields.HTTPListen != "" {
		data.HTTPListen = fields.HTTPListen
		data.MarkDirty("HTTPListen")
	}
	data.TelegramEnabled = fields.TelegramEnabled
	data.MarkDirty("TelegramEnabled")
	if fields.TelegramToken != "" {
		data.TelegramToken = fields.TelegramToken
		data.MarkDirty("TelegramToken")
	}
	data.WhatsAppEnabled = fields.WhatsAppEnabled
	data.MarkDirty("WhatsAppEnabled")
	if fields.LLMProviderID != "" {
		data.LLMProviderID = fields.LLMProviderID
		data.MarkDirty("LLMProviderID")
	}
	if fields.LLMAPIKey != "" {
		data.LLMAPIKey = fields.LLMAPIKey
		data.MarkDirty("LLMAPIKey")
	}
	if fields.LLMBaseURL != "" {
		data.LLMBaseURL = fields.LLMBaseURL
		data.MarkDirty("LLMBaseURL")
	}
	if fields.LLMModel != "" {
		data.LLMModel = fields.LLMModel
		data.MarkDirty("LLMModel")
	}
	data.STTEnabled = fields.STTEnabled
	data.MarkDirty("STTEnabled")
	if fields.STTModel != "" {
		data.STTModel = fields.STTModel
		data.MarkDirty("STTModel")
	}
	data.VoiceLLMEnabled = fields.VoiceLLMEnabled
	data.MarkDirty("VoiceLLMEnabled")
	if fields.VoiceLLMAPIKey != "" {
		data.VoiceLLMAPIKey = fields.VoiceLLMAPIKey
		data.MarkDirty("VoiceLLMAPIKey")
	}
	if fields.VoiceLLMVoice != "" {
		data.VoiceLLMVoice = fields.VoiceLLMVoice
		data.MarkDirty("VoiceLLMVoice")
	}

	// Security/sandboxing fields
	if fields.SandboxPreset != "" {
		data.SandboxPreset = setup.NormalizeSandboxPreset(fields.SandboxPreset)
		data.MarkDirty("SandboxPreset")
	}
	data.SandboxAdvanced = fields.SandboxAdvanced
	data.MarkDirty("SandboxAdvanced")
	if fields.SandboxMode != "" {
		data.SandboxMode = fields.SandboxMode
		data.MarkDirty("SandboxMode")
	}
	data.SandboxEnabled = fields.SandboxEnabled
	data.MarkDirty("SandboxEnabled")
	data.ExecSandboxEnabled = fields.ExecSandboxEnabled
	data.MarkDirty("ExecSandboxEnabled")
	data.BrowserSandboxEnabled = fields.BrowserSandboxEnabled
	data.MarkDirty("BrowserSandboxEnabled")
	data.FileToolsSandboxEnabled = fields.FileToolsSandboxEnabled
	data.MarkDirty("FileToolsSandboxEnabled")
	data.SandboxConsentPermissive = fields.SandboxConsentPermissive
	data.MarkDirty("SandboxConsentPermissive")
	data.SandboxConsentAssistant = fields.SandboxConsentAssistant
	data.MarkDirty("SandboxConsentAssistant")
	data.SandboxConsentHardened = fields.SandboxConsentHardened
	data.MarkDirty("SandboxConsentHardened")
	if !data.SandboxAdvanced {
		setup.ApplySandboxPreset(data, data.SandboxPreset)
	}
	data.SkillsAllowEmbedded = fields.SkillsAllowEmbedded
	data.MarkDirty("SkillsAllowEmbedded")
	data.SkillsAllowClawHub = fields.SkillsAllowClawHub
	data.MarkDirty("SkillsAllowClawHub")
	data.SkillsAllowLocal = fields.SkillsAllowLocal
	data.MarkDirty("SkillsAllowLocal")

	return nil
}

// validateStep validates the data for a specific step
func validateStep(stepID string, data *setup.WizardData) map[string]string {
	errors := make(map[string]string)

	switch stepID {
	case "agent":
		if strings.TrimSpace(data.AgentName) == "" {
			errors["AgentName"] = "Agent name is required"
		}
		if utf8.RuneCountInString(data.AgentTyping) > setup.WizardAgentTypingMaxLen {
			errors["AgentTyping"] = fmt.Sprintf("Typing text must be %d characters or fewer", setup.WizardAgentTypingMaxLen)
		}

	case "user":
		if data.UserName == "" {
			errors["UserName"] = "Username is required"
		}
		if data.UserDisplayName == "" {
			errors["UserDisplayName"] = "Display name is required"
		}
		if data.UserPassword != "" && data.UserPassword != data.UserPasswordConf {
			errors["UserPasswordConf"] = "Passwords do not match"
		}

	case "channels":
		if !data.HTTPEnabled && !data.TelegramEnabled && !data.WhatsAppEnabled {
			errors["_general"] = "At least one channel must be enabled"
		}
		if data.TelegramEnabled && data.TelegramToken == "" {
			errors["TelegramToken"] = "Bot token is required when Telegram is enabled"
		}

	case "pairing":
		if data.TelegramEnabled && strings.TrimSpace(data.UserTelegramID) == "" {
			errors["_general"] = "Telegram is enabled but not paired yet."
		}
		if data.WhatsAppEnabled && strings.TrimSpace(data.UserWhatsAppID) == "" {
			errors["_general"] = "WhatsApp is enabled but not paired yet."
		}

	case "llm":
		if data.LLMProviderID == "" {
			errors["LLMProviderID"] = "Please select an LLM provider"
		}
		if data.LLMAPIKey == "" && data.LLMProviderID != "custom" {
			errors["LLMAPIKey"] = "API key is required"
		}

	case "voice":
		if data.VoiceLLMEnabled && data.VoiceLLMAPIKey == "" {
			errors["VoiceLLMAPIKey"] = "xAI API key is required when Voice LLM is enabled"
		}

	case "security":
		switch setup.NormalizeSandboxPreset(data.SandboxPreset) {
		case setup.SandboxPresetPermissive:
			if !data.SandboxConsentPermissive {
				errors["SandboxConsentPermissive"] = "You must explicitly acknowledge the risk for Permissive mode"
			}
		case setup.SandboxPresetHardened:
			if !data.SandboxConsentHardened {
				errors["SandboxConsentHardened"] = "You must acknowledge reduced capability for Hardened mode"
			}
		case setup.SandboxPresetCustom:
			// Custom uses advanced settings by design; no preset consent checkbox required.
		default:
			if !data.SandboxConsentAssistant {
				errors["SandboxConsentAssistant"] = "You must acknowledge Assistant mode access scope"
			}
		}
	}

	return errors
}
