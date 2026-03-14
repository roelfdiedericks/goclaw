// Package web provides browser-based setup wizard and configuration editor
package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"github.com/roelfdiedericks/goclaw/internal/config"
	"github.com/roelfdiedericks/goclaw/internal/config/forms"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/metadata"
	"github.com/roelfdiedericks/goclaw/internal/setup"
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
	{ID: "workspace", Title: "Workspace", Description: "Choose where GoClaw will store files and configurations."},
	{ID: "user", Title: "Owner Account", Description: "Create your owner account for authentication."},
	{ID: "channels", Title: "Communication Channels", Description: "Configure how you'll interact with GoClaw."},
	{ID: "llm", Title: "LLM Provider", Description: "Set up your language model provider (Anthropic, OpenAI, etc.)."},
	{ID: "voice", Title: "Voice Settings", Description: "Configure speech-to-text and voice LLM (optional)."},
	{ID: "security", Title: "Security & Skills", Description: "Configure sandboxing and skill installation sources."},
	{ID: "review", Title: "Review & Finish", Description: "Review your settings and complete the setup."},
}

// WizardState holds the current wizard session state
type WizardState struct {
	Step int                    `json:"step"`
	Data *setup.WizardData      `json:"data"`
	mu   sync.Mutex
}

// WizardAPI provides API endpoints for the wizard
type WizardAPI struct {
	state      *WizardState
	configPath string
}

// NewWizardAPI creates a new wizard API handler
func NewWizardAPI(configPath string) *WizardAPI {
	data := setup.NewWizardData()

	// Try to load existing GoClaw config
	loadResult, err := loadWizardConfig(configPath)
	if err == nil && loadResult.Config != nil {
		data.LoadFromExisting(loadResult.Config, loadResult.SourcePath)
		L_info("wizard: loaded existing config", "path", loadResult.SourcePath)
	}

	// Check for OpenClaw installation
	data.LoadFromOpenClaw()
	if data.OpenClawExists {
		L_info("wizard: detected OpenClaw installation")
	}

	return &WizardAPI{
		state: &WizardState{
			Step: 1,
			Data: data,
		},
		configPath: configPath,
	}
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

	writeJSON(rw, http.StatusOK, APIResponse{
		Success: true,
		Data: map[string]interface{}{
			"step":       w.state.Step,
			"totalSteps": len(WizardSteps),
			"steps":      WizardSteps,
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
	w.state.mu.Unlock()

	if step < 1 || step > len(WizardSteps) {
		writeJSON(rw, http.StatusBadRequest, APIResponse{
			Success: false,
			Message: "Invalid step",
		})
		return
	}

	stepDef := WizardSteps[step-1]
	formDef := getStepFormDef(stepDef.ID, data)

	var formHTML string
	if formDef != nil {
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

	// Update wizard data from payload
	if err := updateWizardData(w.state.Data, payload); err != nil {
		writeJSON(rw, http.StatusBadRequest, APIResponse{
			Success: false,
			Message: err.Error(),
		})
		return
	}

	// Validate current step
	if errors := validateStep(WizardSteps[w.state.Step-1].ID, w.state.Data); len(errors) > 0 {
		writeJSON(rw, http.StatusBadRequest, APIResponse{
			Success: false,
			Errors:  errors,
		})
		return
	}

	// Advance to next step
	if w.state.Step < len(WizardSteps) {
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

	if w.state.Step > 1 {
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
							Name: "UserPasswordConf",
							Title: "Confirm Password",
							Type:  forms.Secret,
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
		return &forms.FormDef{
			Title:       "Security & Skills",
			Description: "Configure sandboxing and skill installation sources.",
			Sections: []forms.Section{
				{
					Title: "Sandboxing",
					Desc:  "Sandboxing restricts tools to only access files within your workspace, preventing accidental or malicious access to system files.",
					Fields: []forms.Field{
						{Name: "ExecBubblewrap", Title: "Enable Exec Sandboxing", Type: forms.Toggle, Default: true},
						{Name: "BrowserBubblewrap", Title: "Enable Browser Sandboxing", Type: forms.Toggle, Default: true},
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
		OpenClawImport      bool   `json:"OpenClawImport"`
		WorkspacePath       string `json:"WorkspacePath"`
		UserName            string `json:"UserName"`
		UserDisplayName     string `json:"UserDisplayName"`
		UserPassword        string `json:"UserPassword"`
		UserPasswordConf    string `json:"UserPasswordConf"`
		HTTPEnabled         bool   `json:"HTTPEnabled"`
		HTTPListen          string `json:"HTTPListen"`
		TelegramEnabled     bool   `json:"TelegramEnabled"`
		TelegramToken       string `json:"TelegramToken"`
		WhatsAppEnabled     bool   `json:"WhatsAppEnabled"`
		LLMProviderID       string `json:"LLMProviderID"`
		LLMAPIKey           string `json:"LLMAPIKey"`
		LLMBaseURL          string `json:"LLMBaseURL"`
		LLMModel            string `json:"LLMModel"`
		STTEnabled          bool   `json:"STTEnabled"`
		STTModel            string `json:"STTModel"`
		VoiceLLMEnabled     bool   `json:"VoiceLLMEnabled"`
		VoiceLLMAPIKey      string `json:"VoiceLLMAPIKey"`
		VoiceLLMVoice       string `json:"VoiceLLMVoice"`
		ExecBubblewrap      bool   `json:"ExecBubblewrap"`
		BrowserBubblewrap   bool   `json:"BrowserBubblewrap"`
		SkillsAllowEmbedded bool   `json:"SkillsAllowEmbedded"`
		SkillsAllowClawHub  bool   `json:"SkillsAllowClawHub"`
		SkillsAllowLocal    bool   `json:"SkillsAllowLocal"`
	}

	var fields wizardFields
	if err := json.Unmarshal(jsonData, &fields); err != nil {
		return err
	}

	// Handle OpenClaw import toggle
	data.OpenClawImport = fields.OpenClawImport

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
	data.ExecBubblewrap = fields.ExecBubblewrap
	data.MarkDirty("ExecBubblewrap")
	data.BrowserBubblewrap = fields.BrowserBubblewrap
	data.MarkDirty("BrowserBubblewrap")
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
	}

	return errors
}
