// Package setup - tview-based onboarding wizard
package setup

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"
	"github.com/mdp/qrterminal/v3"
	"github.com/rivo/tview"
	"github.com/roelfdiedericks/goclaw/internal/bus"
	telegrampairing "github.com/roelfdiedericks/goclaw/internal/channels/telegram"
	whatsapppairing "github.com/roelfdiedericks/goclaw/internal/channels/whatsapp"
	"github.com/roelfdiedericks/goclaw/internal/config"
	"github.com/roelfdiedericks/goclaw/internal/config/forms"
	"github.com/roelfdiedericks/goclaw/internal/llm"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/metadata"
	"github.com/roelfdiedericks/goclaw/internal/paths"
	"github.com/roelfdiedericks/goclaw/internal/sandbox"
	setuppairing "github.com/roelfdiedericks/goclaw/internal/setup/pairing"
	"github.com/roelfdiedericks/goclaw/internal/stt"
	"github.com/roelfdiedericks/goclaw/internal/user"
)

const WizardAgentTypingMaxLen = 80

// enableFormMouseScroll adds mouse scroll support to a tview.Form
// Converts scroll events to Tab/BackTab for field navigation
func enableFormMouseScroll(form *tview.Form, w *forms.Wizard) {
	app := w.App().App()
	form.SetMouseCapture(func(action tview.MouseAction, event *tcell.EventMouse) (tview.MouseAction, *tcell.EventMouse) {
		switch action {
		case tview.MouseScrollUp:
			go func() {
				app.QueueEvent(tcell.NewEventKey(tcell.KeyBacktab, 0, tcell.ModNone))
			}()
			return 0, nil
		case tview.MouseScrollDown:
			go func() {
				app.QueueEvent(tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone))
			}()
			return 0, nil
		}
		return action, event
	})
}

// WizardData holds all configuration values being edited across wizard steps
type WizardData struct {
	// Config detection
	ConfigExists   bool
	ConfigPath     string
	ExistingConfig *config.Config

	// OpenClaw migration
	OpenClawExists bool
	OpenClawImport bool
	OpenClawConfig map[string]interface{}

	// Workspace
	WorkspacePath string

	// Agent identity
	AgentName   string
	AgentEmoji  string
	AgentTyping string

	// User setup
	UserName              string
	UserDisplayName       string
	UserRole              string
	UserTelegramID        string
	UserWhatsAppID        string
	InitialUserTelegramID string
	InitialUserWhatsAppID string
	UserPassword          string
	UserPasswordConf      string
	UserExistingPwdHash   string // preserved from existing users.json

	// Telegram
	TelegramEnabled bool
	TelegramToken   string

	// WhatsApp
	WhatsAppEnabled bool

	// HTTP
	HTTPEnabled bool
	HTTPListen  string

	// Browser
	BrowserSetup bool

	// Sandboxing
	SandboxEnabled           bool
	SandboxMode              string
	ExecSandboxEnabled       bool
	BrowserSandboxEnabled    bool
	FileToolsSandboxEnabled  bool
	SandboxPreset            string
	SandboxAdvanced          bool
	SandboxConsentPermissive bool
	SandboxConsentAssistant  bool
	SandboxConsentHardened   bool

	// Skills Installation
	SkillsAllowEmbedded bool
	SkillsAllowClawHub  bool
	SkillsAllowLocal    bool

	// LLM
	LLMProviderID   string
	LLMProviderName string
	LLMDriver       string
	LLMAPIKey       string
	LLMBaseURL      string
	LLMModel        string
	LLMSkipped      bool

	// STT (Speech-to-Text)
	STTEnabled        bool
	STTModel          string
	STTModelAvailable bool // true if model exists (bundled or downloaded)

	// VoiceLLM (Real-time Voice)
	VoiceLLMEnabled bool
	VoiceLLMAPIKey  string
	VoiceLLMVoice   string // Eve, Ara, Rex, Sal, Leo

	// Dirty tracking - only save fields that were actually modified
	dirty map[string]bool

	baseImportState     wizardImportState
	openClawImportState wizardImportState
}

type wizardImportState struct {
	WorkspacePath   string
	TelegramEnabled bool
	TelegramToken   string
	UserTelegramID  string
}

// MarkDirty marks the specified fields as modified
func (d *WizardData) MarkDirty(fields ...string) {
	if d.dirty == nil {
		d.dirty = make(map[string]bool)
	}
	for _, f := range fields {
		d.dirty[f] = true
	}
}

// IsDirty returns true if the field was modified
func (d *WizardData) IsDirty(field string) bool {
	if d.dirty == nil {
		return false
	}
	return d.dirty[field]
}

// HasAnyDirty returns true if any of the specified fields are dirty
func (d *WizardData) HasAnyDirty(fields ...string) bool {
	for _, f := range fields {
		if d.IsDirty(f) {
			return true
		}
	}
	return false
}

// NewWizardData creates a new WizardData with defaults
func NewWizardData() *WizardData {
	d := &WizardData{
		AgentName:           "GoClaw",
		AgentEmoji:          "🐾",
		UserRole:            "owner",
		HTTPEnabled:         true,
		HTTPListen:          "127.0.0.1:1337",
		SkillsAllowEmbedded: true,
		SkillsAllowClawHub:  false,
		SkillsAllowLocal:    false,
		dirty:               make(map[string]bool),
	}
	ApplySandboxPreset(d, SandboxPresetAssistant)
	d.SandboxAdvanced = false
	d.captureBaseImportState()
	return d
}

// LoadFromExisting populates WizardData from existing config
func (d *WizardData) LoadFromExisting(cfg *config.Config, path string) {
	d.ConfigExists = true
	d.ConfigPath = path
	d.ExistingConfig = cfg
	d.loadFromConfig(cfg)
	d.captureBaseImportState()
}

// LoadFromDefaults seeds WizardData from a fully-defaulted config without
// marking it as an existing user configuration.
func (d *WizardData) LoadFromDefaults(cfg *config.Config) {
	d.ExistingConfig = cfg
	d.loadFromConfig(cfg)
	d.captureBaseImportState()
	ApplySandboxPreset(d, SandboxPresetAssistant)
	d.SandboxAdvanced = false
	d.MarkDirty(
		"SandboxPreset",
		"SandboxEnabled",
		"SandboxMode",
		"ExecSandboxEnabled",
		"BrowserSandboxEnabled",
		"FileToolsSandboxEnabled",
	)
}

func (d *WizardData) loadFromConfig(cfg *config.Config) {
	// Agent identity
	d.AgentName = cfg.Agent.Name
	d.AgentEmoji = cfg.Agent.Emoji
	d.AgentTyping = cfg.Agent.Typing
	if d.AgentName == "" {
		d.AgentName = "GoClaw"
	}

	// Extract values from existing config
	d.WorkspacePath = cfg.Gateway.WorkingDir
	d.TelegramEnabled = cfg.Channels.Telegram.Enabled
	d.TelegramToken = cfg.Channels.Telegram.BotToken
	d.WhatsAppEnabled = cfg.Channels.WhatsApp.Enabled

	// HTTP.Enabled is a pointer (nil = default true)
	if cfg.Channels.HTTP.Enabled != nil {
		d.HTTPEnabled = *cfg.Channels.HTTP.Enabled
	} else {
		d.HTTPEnabled = true // default
	}
	if cfg.Channels.HTTP.Listen != "" {
		d.HTTPListen = cfg.Channels.HTTP.Listen
	}

	// Sandboxing
	d.SandboxEnabled = cfg.Sandbox.IsEnabled()
	d.SandboxMode = cfg.Sandbox.GetMode()
	d.ExecSandboxEnabled = cfg.Sandbox.General.ExecEnabled
	d.BrowserSandboxEnabled = cfg.Sandbox.General.BrowserEnabled
	d.FileToolsSandboxEnabled = cfg.Sandbox.General.FileToolsEnabled
	d.SandboxPreset, d.SandboxAdvanced = DetectSandboxPreset(
		d.SandboxEnabled,
		d.SandboxMode,
		d.ExecSandboxEnabled,
		d.BrowserSandboxEnabled,
		d.FileToolsSandboxEnabled,
	)

	// LLM: load from first agent chain entry
	if len(cfg.LLM.Agent.Models) > 0 {
		parts := strings.SplitN(cfg.LLM.Agent.Models[0], "/", 2)
		if len(parts) == 2 {
			alias := parts[0]
			if provCfg, ok := cfg.LLM.Providers[alias]; ok {
				d.LLMProviderID = provCfg.Subtype
				if d.LLMProviderID == "" {
					d.LLMProviderID = provCfg.Driver
				}
				d.LLMDriver = provCfg.Driver
				d.LLMAPIKey = provCfg.APIKey
				d.LLMBaseURL = provCfg.BaseURL
				d.LLMModel = parts[1]

				if prov, ok := metadata.Get().GetModelProvider(d.LLMProviderID); ok {
					d.LLMProviderName = prov.Name
				}
			}
		}
	}

	// VoiceLLM
	d.VoiceLLMEnabled = cfg.VoiceLLM.Enabled
	if xai, ok := cfg.VoiceLLM.Providers["xai"]; ok {
		d.VoiceLLMAPIKey = xai.APIKey
		d.VoiceLLMVoice = xai.Voice
	}

	// STT (Speech-to-Text)
	if cfg.STT.Provider != "" {
		d.STTEnabled = true
		if cfg.STT.Provider == "whispercpp" {
			d.STTModel = cfg.STT.WhisperCpp.Model
		}
	}

	// Skills installation sources
	d.SkillsAllowEmbedded = cfg.Skills.Install.AllowEmbedded
	d.SkillsAllowClawHub = cfg.Skills.Install.AllowClawHub
	d.SkillsAllowLocal = cfg.Skills.Install.AllowLocal

	// Load user data from users.json
	d.loadUserFromUsersJSON()
}

// loadUserFromUsersJSON loads user profile data from users.json
func (d *WizardData) loadUserFromUsersJSON() {
	users, err := user.LoadUsers()
	if err != nil {
		L_warn("wizard: failed to load users.json", "error", err)
		return
	}

	if len(users) == 0 {
		return
	}

	// Find first owner user
	ownerUsername := users.GetOwner()
	if ownerUsername == "" {
		// No owner found, use first user
		for username := range users {
			ownerUsername = username
			break
		}
	}

	if ownerUsername == "" {
		return
	}

	user := users[ownerUsername]
	d.UserName = ownerUsername
	d.UserDisplayName = user.Name
	d.UserRole = user.Role
	if user.TelegramID != "" {
		d.UserTelegramID = user.TelegramID
		d.InitialUserTelegramID = user.TelegramID
	}
	if user.WhatsAppID != "" {
		d.UserWhatsAppID = user.WhatsAppID
		d.InitialUserWhatsAppID = user.WhatsAppID
	}
	if user.HTTPPasswordHash != "" {
		d.UserExistingPwdHash = user.HTTPPasswordHash
	}

	L_info("wizard: loaded user from users.json", "username", ownerUsername)
}

func (d *WizardData) ResetPairingStage() {
	d.UserTelegramID = d.InitialUserTelegramID
	d.UserWhatsAppID = d.InitialUserWhatsAppID
}

func (d *WizardData) captureBaseImportState() {
	d.baseImportState = wizardImportState{
		WorkspacePath:   d.WorkspacePath,
		TelegramEnabled: d.TelegramEnabled,
		TelegramToken:   d.TelegramToken,
		UserTelegramID:  d.UserTelegramID,
	}
}

func (d *WizardData) ApplyOpenClawImport(enable bool) {
	d.OpenClawImport = enable
	if enable {
		if d.openClawImportState.WorkspacePath != "" {
			d.WorkspacePath = d.openClawImportState.WorkspacePath
		}
		if d.openClawImportState.TelegramToken != "" {
			d.TelegramToken = d.openClawImportState.TelegramToken
			d.TelegramEnabled = d.openClawImportState.TelegramEnabled
		}
		if d.openClawImportState.UserTelegramID != "" {
			d.UserTelegramID = d.openClawImportState.UserTelegramID
		}
		return
	}

	d.WorkspacePath = d.baseImportState.WorkspacePath
	d.TelegramEnabled = d.baseImportState.TelegramEnabled
	d.TelegramToken = d.baseImportState.TelegramToken
	d.UserTelegramID = d.baseImportState.UserTelegramID
}

// LoadFromOpenClaw extracts settings from OpenClaw config
func (d *WizardData) LoadFromOpenClaw() {
	if !OpenClawExists() {
		return
	}
	d.OpenClawExists = true

	// Load OpenClaw config
	data, err := os.ReadFile(OpenClawConfigPath())
	if err != nil {
		L_warn("wizard: failed to read OpenClaw config", "error", err)
		return
	}

	if err := json.Unmarshal(data, &d.OpenClawConfig); err != nil {
		L_warn("wizard: failed to parse OpenClaw config", "error", err)
		return
	}

	// Extract workspace
	if agents, ok := d.OpenClawConfig["agents"].(map[string]interface{}); ok {
		if defaults, ok := agents["defaults"].(map[string]interface{}); ok {
			if ws, ok := defaults["workspace"].(string); ok {
				d.openClawImportState.WorkspacePath = ws
			}
		}
	}

	// Extract Telegram
	if channels, ok := d.OpenClawConfig["channels"].(map[string]interface{}); ok {
		if telegram, ok := channels["telegram"].(map[string]interface{}); ok {
			if token, ok := telegram["botToken"].(string); ok {
				d.openClawImportState.TelegramToken = token
				d.openClawImportState.TelegramEnabled = true
			}
			if allowFrom, ok := telegram["allowFrom"].([]interface{}); ok && len(allowFrom) > 0 {
				if id, ok := allowFrom[0].(string); ok {
					d.openClawImportState.UserTelegramID = id
				}
			}
		}
	}
}

// RunOnboardWizardTview runs the new tview-based onboarding wizard
func RunOnboardWizardTview() error {
	L_debug("setup: starting tview onboard wizard")

	data := NewWizardData()

	// Check for existing config
	loadResult, err := config.Load()
	if err == nil && loadResult.Config != nil {
		data.LoadFromExisting(loadResult.Config, loadResult.SourcePath)
	}

	// Check for OpenClaw
	data.LoadFromOpenClaw()

	telegrampairing.RegisterPairingCommands()
	whatsapppairing.RegisterPairingCommands()

	// Build wizard steps
	steps := buildWizardSteps(data)

	wizard := forms.NewWizard("GoClaw Setup", steps)

	// Store data in wizard for access from steps
	wizard.Data["wizardData"] = data

	// Show Editor button if config exists
	if data.ConfigExists {
		wizard.ShowEditorButton = true
	}

	result, err := wizard.Run()
	if err != nil {
		return fmt.Errorf("wizard error: %w", err)
	}

	// Check if user clicked "Open Editor" button (either in content or button bar)
	if wizard.GetData("goToEditor") == true || result == forms.WizardEditor {
		L_info("wizard: switching to editor")
		return RunEditorTview()
	}

	switch result {
	case forms.WizardCompleted:
		// Print config for testing
		printWizardConfig(data)
	case forms.WizardCancelled:
		fmt.Println("Setup cancelled.")
	}

	return nil
}

// buildWizardSteps creates all the wizard steps
func buildWizardSteps(data *WizardData) []forms.WizardStep {
	steps := []forms.WizardStep{
		stepWelcome(data),
	}

	// Add OpenClaw detection if found
	if data.OpenClawExists {
		steps = append(steps, stepOpenClawDetect(data))
	}

	steps = append(steps,
		stepAgentIdentity(data),
		stepWorkspace(data),
		stepUserSetup(data),
		stepTelegram(data),
		stepWhatsApp(data),
		stepChannelPairing(data),
		stepHTTP(data),
		stepSTT(data),
		stepLLMProvider(data),
		stepVoiceLLM(data),
		stepSandbox(data),
		stepReview(data),
	)

	return steps
}

// Step: Agent Identity
func stepAgentIdentity(data *WizardData) forms.WizardStep {
	return forms.WizardStep{
		Title: "Agent Identity",
		Content: func(w *forms.Wizard) tview.Primitive {
			form := tview.NewForm()
			form.SetBorder(false)
			enableFormMouseScroll(form, w)

			form.AddInputField("Agent Name", data.AgentName, 40, nil, func(text string) {
				data.AgentName = text
				data.MarkDirty("AgentName")
			})

			form.AddInputField("Agent Emoji (optional)", data.AgentEmoji, 8, nil, func(text string) {
				data.AgentEmoji = text
				data.MarkDirty("AgentEmoji")
			})

			form.AddInputField("Typing Text (optional)", data.AgentTyping, 60, nil, func(text string) {
				data.AgentTyping = text
				data.MarkDirty("AgentTyping")
			})

			return formWithHeader(`Set how your assistant appears in messages.

Name is required. Emoji and typing text are optional.
Typing text max length: 80 characters.`, 3, form)
		},
		OnExit: func(_ *forms.Wizard) error {
			if strings.TrimSpace(data.AgentName) == "" {
				return fmt.Errorf("agent name is required")
			}
			if utf8.RuneCountInString(data.AgentTyping) > WizardAgentTypingMaxLen {
				return fmt.Errorf("typing text must be %d characters or fewer", WizardAgentTypingMaxLen)
			}
			data.AgentName = strings.TrimSpace(data.AgentName)
			L_info("wizard: agent identity set", "name", data.AgentName, "emoji", data.AgentEmoji)
			return nil
		},
	}
}

// Step: Welcome
func stepWelcome(data *WizardData) forms.WizardStep {
	return forms.WizardStep{
		Title: "Welcome",
		Content: func(w *forms.Wizard) tview.Primitive {
			if data.ConfigExists {
				// Config exists - show Editor button option
				text := fmt.Sprintf(`[white]Welcome back to [cyan]GoClaw[white]!

[yellow]Existing configuration detected:[white]
  %s

You can open the [yellow]Editor[white] for quick access to specific settings,
or click [yellow]Next[white] to walk through all settings step by step.`, data.ConfigPath)

				header := tview.NewTextView().
					SetDynamicColors(true).
					SetText(text)
				header.SetBorder(false)

				// Add Editor button in content
				form := tview.NewForm()
				form.SetBorder(false)
				enableFormMouseScroll(form, w)
				form.AddButton("Open Editor", func() {
					w.SetData("goToEditor", true)
					w.App().Stop()
				})

				layout := tview.NewFlex().
					SetDirection(tview.FlexRow).
					AddItem(header, 8, 0, false).
					AddItem(form, 3, 0, true)

				return layout
			}

			// No config - show welcome message
			text := `[white]Welcome to [cyan]GoClaw[white]!

This wizard will help you set up your personal AI assistant.

We'll configure:
  • Agent identity
  • Workspace location
  • User profile
  • Telegram bot (optional)
  • HTTP server
  • Browser profiles
  • Sandboxing

[gray]Note: LLM providers are configured separately via 'goclaw setup edit'[white]

Press [yellow]Next[white] to begin.`

			tv := tview.NewTextView().
				SetDynamicColors(true).
				SetText(text)
			tv.SetBorder(false)
			return tv
		},
	}
}

// Step: OpenClaw Detection
func stepOpenClawDetect(data *WizardData) forms.WizardStep {
	return forms.WizardStep{
		Title: "OpenClaw Migration",
		Content: func(w *forms.Wizard) tview.Primitive {
			form := tview.NewForm()
			form.SetBorder(false)
			enableFormMouseScroll(form, w)

			form.AddTextView("", fmt.Sprintf(`Found existing OpenClaw installation at ~/.openclaw/

Detected settings:
  Workspace: %s
  Telegram: %s

Would you like to import these settings?
`, valueOrDefault(data.openClawImportState.WorkspacePath, "(not set)"),
				boolToConfigured(data.openClawImportState.TelegramToken != "")), 0, 8, false, false)

			form.AddButton("Yes, Import Settings", func() {
				data.ApplyOpenClawImport(true)
				L_info("wizard: will import OpenClaw settings")
				w.NextStep()
			})

			form.AddButton("No, Start Fresh", func() {
				data.ApplyOpenClawImport(false)
				L_info("wizard: skipping OpenClaw import")
				w.NextStep()
			})

			return form
		},
	}
}

// Step: Workspace
func stepWorkspace(data *WizardData) forms.WizardStep {
	return forms.WizardStep{
		Title: "Workspace",
		Content: func(w *forms.Wizard) tview.Primitive {
			form := tview.NewForm()
			form.SetBorder(false)
			enableFormMouseScroll(form, w)

			// Default workspace path
			if data.WorkspacePath == "" {
				data.WorkspacePath = DefaultWorkspacePath()
			}

			form.AddInputField("Workspace Path", data.WorkspacePath, 50, nil, func(text string) {
				data.WorkspacePath = text
				data.MarkDirty("WorkspacePath")
			})

			return formWithHeader(`The [cyan]workspace[white] is where GoClaw stores your agent's files,
including memory, transcripts, and project data.`, 3, form)
		},
		OnExit: func(w *forms.Wizard) error {
			if data.WorkspacePath == "" {
				return fmt.Errorf("workspace path is required")
			}
			data.WorkspacePath = ExpandPath(data.WorkspacePath)
			L_info("wizard: workspace set", "path", data.WorkspacePath)
			return nil
		},
	}
}

// Step: User Setup
func stepUserSetup(data *WizardData) forms.WizardStep {
	return forms.WizardStep{
		Title: "User Profile",
		Content: func(w *forms.Wizard) tview.Primitive {
			form := tview.NewForm()
			form.SetBorder(false)
			enableFormMouseScroll(form, w)

			form.AddInputField("Your Name", data.UserDisplayName, 40, nil, func(text string) {
				data.UserDisplayName = text
			})

			form.AddInputField("Username (lowercase)", data.UserName, 20, nil, func(text string) {
				data.UserName = text
			})

			form.AddInputField("Telegram User ID (optional)", data.UserTelegramID, 20, nil, func(text string) {
				data.UserTelegramID = text
			})

			form.AddInputField("WhatsApp ID (optional)", data.UserWhatsAppID, 24, nil, func(text string) {
				data.UserWhatsAppID = text
			})

			pwdLabel := "HTTP Password"
			if data.UserExistingPwdHash != "" {
				pwdLabel = "HTTP Password (set — leave blank to keep)"
			}
			form.AddPasswordField(pwdLabel, data.UserPassword, 40, '*', func(text string) {
				data.UserPassword = text
			})

			form.AddPasswordField("Confirm Password", data.UserPasswordConf, 40, '*', func(text string) {
				data.UserPasswordConf = text
			})

			return formWithHeader(`Set up your user profile.
Password is used for HTTP web interface authentication.`, 3, form)
		},
		OnExit: func(w *forms.Wizard) error {
			if data.UserDisplayName == "" {
				return fmt.Errorf("name is required")
			}
			if data.UserName == "" {
				data.UserName = sanitizeUsername(data.UserDisplayName)
			}
			if data.UserPassword != "" && data.UserPassword != data.UserPasswordConf {
				return fmt.Errorf("passwords do not match")
			}
			L_info("wizard: user set", "name", data.UserDisplayName, "username", data.UserName)
			return nil
		},
	}
}

// Step: Telegram
func stepTelegram(data *WizardData) forms.WizardStep {
	return forms.WizardStep{
		Title: "Telegram",
		Content: func(w *forms.Wizard) tview.Primitive {
			form := tview.NewForm()
			form.SetBorder(false)
			enableFormMouseScroll(form, w)

			form.AddCheckbox("Enable Telegram Bot", data.TelegramEnabled, func(checked bool) {
				data.TelegramEnabled = checked
				data.MarkDirty("TelegramEnabled")
			})

			form.AddInputField("Bot Token", data.TelegramToken, 50, nil, func(text string) {
				data.TelegramToken = text
				data.MarkDirty("TelegramToken")
			})

			return formWithHeader(`[cyan]Telegram[white] allows you to chat with GoClaw from your phone.
Get a bot token from [yellow]@BotFather[white] on Telegram.`, 3, form)
		},
		OnExit: func(w *forms.Wizard) error {
			if data.TelegramEnabled && data.TelegramToken == "" {
				return fmt.Errorf("bot token is required when Telegram is enabled")
			}
			L_info("wizard: telegram", "enabled", data.TelegramEnabled)
			return nil
		},
	}
}

// Step: WhatsApp
func stepWhatsApp(data *WizardData) forms.WizardStep {
	return forms.WizardStep{
		Title: "WhatsApp",
		Content: func(w *forms.Wizard) tview.Primitive {
			form := tview.NewForm()
			form.SetBorder(false)
			enableFormMouseScroll(form, w)

			form.AddCheckbox("Enable WhatsApp Channel", data.WhatsAppEnabled, func(checked bool) {
				data.WhatsAppEnabled = checked
				data.MarkDirty("WhatsAppEnabled")
			})

			return formWithHeader(`[cyan]WhatsApp[white] allows you to chat with GoClaw from WhatsApp.

After setup, pair your phone by running:
  [yellow]goclaw whatsapp link[white]

This will display a QR code to scan with your WhatsApp app.
You also need to set your WhatsApp ID:
  [yellow]goclaw user set-whatsapp <username> <phone>[white]`, 9, form)
		},
		OnExit: func(w *forms.Wizard) error {
			L_info("wizard: whatsapp", "enabled", data.WhatsAppEnabled)
			return nil
		},
	}
}

func stepChannelPairing(data *WizardData) forms.WizardStep {
	return forms.WizardStep{
		Title: "Channel Pairing",
		Content: func(w *forms.Wizard) tview.Primitive {
			header := tview.NewTextView().
				SetDynamicColors(true).
				SetWrap(true).
				SetText(`[cyan]Pair enabled owner channels[white]

Use this step to bind the owner account to Telegram and/or WhatsApp.
Successful pairing only stages the owner identity until you finish setup.`)
			header.SetBorder(false)

			status := tview.NewTextView().
				SetDynamicColors(true).
				SetWrap(true)
			status.SetBorder(true).SetTitle(" Pairing Status ").SetTitleAlign(tview.AlignLeft)

			form := tview.NewForm()
			form.SetBorder(false)
			enableFormMouseScroll(form, w)

			refreshStatus := func() {
				var lines []string
				if data.TelegramEnabled {
					lines = append(lines, formatTelegramPairingStatus())
				} else {
					lines = append(lines, "[gray]Telegram:[white] disabled")
				}
				if data.WhatsAppEnabled {
					lines = append(lines, formatWhatsAppPairingStatus())
				} else {
					lines = append(lines, "[gray]WhatsApp:[white] disabled")
				}
				status.SetText(strings.Join(lines, "\n\n"))
			}

			refreshStatus()

			form.AddButton("Start Telegram Pairing", func() {
				if !data.TelegramEnabled {
					w.App().ShowModal("Enable Telegram first in the Telegram step.", []string{"OK"}, nil)
					return
				}
				if strings.TrimSpace(data.TelegramToken) == "" {
					w.App().ShowModal("Telegram bot token is required before pairing.", []string{"OK"}, nil)
					return
				}
				res := bus.SendCommandWithSource("telegram.pairing", "start", setuppairing.TelegramStartRequest{
					StartRequest: setuppairing.StartRequest{
						BaseRequest: setuppairing.BaseRequest{
							SessionID: "tui-wizard-telegram",
							Surface:   "tui-wizard",
						},
					},
					BotToken: data.TelegramToken,
				}, "tui", "")
				if res.Error != nil {
					w.App().ShowModal(res.Message, []string{"OK"}, nil)
					return
				}
				data.UserTelegramID = ""
				refreshStatus()
			})
			form.AddButton("Refresh Telegram", func() {
				refreshTelegramPairing(data)
				refreshStatus()
			})
			form.AddButton("Start WhatsApp Pairing", func() {
				if !data.WhatsAppEnabled {
					w.App().ShowModal("Enable WhatsApp first in the WhatsApp step.", []string{"OK"}, nil)
					return
				}
				res := bus.SendCommandWithSource("whatsapp.pairing", "start", setuppairing.WhatsAppStartRequest{
					StartRequest: setuppairing.StartRequest{
						BaseRequest: setuppairing.BaseRequest{
							SessionID: "tui-wizard-whatsapp",
							Surface:   "tui-wizard",
						},
					},
				}, "tui", "")
				if res.Error != nil {
					w.App().ShowModal(res.Message, []string{"OK"}, nil)
					return
				}
				data.UserWhatsAppID = ""
				refreshStatus()
			})
			form.AddButton("Refresh WhatsApp", func() {
				refreshWhatsAppPairing(data)
				refreshStatus()
			})

			layout := tview.NewFlex().
				SetDirection(tview.FlexRow).
				AddItem(header, 4, 0, false).
				AddItem(form, 6, 0, true).
				AddItem(status, 0, 1, false)
			return layout
		},
		OnExit: func(_ *forms.Wizard) error {
			refreshTelegramPairing(data)
			refreshWhatsAppPairing(data)
			if data.TelegramEnabled && strings.TrimSpace(data.UserTelegramID) == "" {
				return fmt.Errorf("telegram is enabled but not paired yet")
			}
			if data.WhatsAppEnabled && strings.TrimSpace(data.UserWhatsAppID) == "" {
				return fmt.Errorf("whatsapp is enabled but not paired yet")
			}
			return nil
		},
	}
}

func refreshTelegramPairing(data *WizardData) {
	res := bus.SendCommandWithSource("telegram.pairing", "status", setuppairing.StatusRequest{
		BaseRequest: setuppairing.BaseRequest{
			SessionID: "tui-wizard-telegram",
			Surface:   "tui-wizard",
		},
	}, "tui", "")
	if status, ok := res.Data.(setuppairing.Status); ok && status.Identity != nil && status.Identity.ID != "" {
		data.UserTelegramID = status.Identity.ID
	} else if statusPtr, ok := res.Data.(*setuppairing.Status); ok && statusPtr != nil && statusPtr.Identity != nil && statusPtr.Identity.ID != "" {
		data.UserTelegramID = statusPtr.Identity.ID
	}
}

func refreshWhatsAppPairing(data *WizardData) {
	res := bus.SendCommandWithSource("whatsapp.pairing", "status", setuppairing.StatusRequest{
		BaseRequest: setuppairing.BaseRequest{
			SessionID: "tui-wizard-whatsapp",
			Surface:   "tui-wizard",
		},
	}, "tui", "")
	if status, ok := res.Data.(setuppairing.Status); ok && status.Identity != nil && status.Identity.ID != "" {
		data.UserWhatsAppID = status.Identity.ID
	} else if statusPtr, ok := res.Data.(*setuppairing.Status); ok && statusPtr != nil && statusPtr.Identity != nil && statusPtr.Identity.ID != "" {
		data.UserWhatsAppID = statusPtr.Identity.ID
	}
}

func formatTelegramPairingStatus() string {
	res := bus.SendCommandWithSource("telegram.pairing", "status", setuppairing.StatusRequest{
		BaseRequest: setuppairing.BaseRequest{
			SessionID: "tui-wizard-telegram",
			Surface:   "tui-wizard",
		},
	}, "tui", "")
	status := extractPairingStatus(res.Data, "telegram", "tui-wizard-telegram")
	lines := []string{fmt.Sprintf("[yellow]Telegram[white] [%s]", status.State), status.Message}
	if status.Artifacts != nil && status.Artifacts.Code != "" {
		lines = append(lines, fmt.Sprintf("Code: [green]%s[white]", status.Artifacts.Code))
	}
	if status.Identity != nil {
		lines = append(lines, fmt.Sprintf("Owner: %s %s %s", status.Identity.DisplayName, status.Identity.Username, status.Identity.ID))
	}
	return strings.Join(lines, "\n")
}

func formatWhatsAppPairingStatus() string {
	res := bus.SendCommandWithSource("whatsapp.pairing", "status", setuppairing.StatusRequest{
		BaseRequest: setuppairing.BaseRequest{
			SessionID: "tui-wizard-whatsapp",
			Surface:   "tui-wizard",
		},
	}, "tui", "")
	status := extractPairingStatus(res.Data, "whatsapp", "tui-wizard-whatsapp")
	lines := []string{fmt.Sprintf("[yellow]WhatsApp[white] [%s]", status.State), status.Message}
	if status.Artifacts != nil && status.Artifacts.QRCode != "" {
		lines = append(lines, renderPairingQRCode(status.Artifacts.QRCode))
	}
	if status.Identity != nil {
		lines = append(lines, fmt.Sprintf("Owner: %s %s", status.Identity.Phone, status.Identity.JID))
	}
	return strings.Join(lines, "\n")
}

func renderPairingQRCode(code string) string {
	var buf bytes.Buffer
	qrterminal.GenerateHalfBlock(code, qrterminal.L, &buf)
	return buf.String()
}

func extractPairingStatus(data any, channel, sessionID string) setuppairing.Status {
	if status, ok := data.(setuppairing.Status); ok {
		return status
	}
	if status, ok := data.(*setuppairing.Status); ok && status != nil {
		return *status
	}
	return setuppairing.Status{
		Channel:   channel,
		SessionID: sessionID,
		State:     setuppairing.StateNotStarted,
		Message:   "Pairing has not started yet.",
	}
}

// Step: HTTP
func stepHTTP(data *WizardData) forms.WizardStep {
	return forms.WizardStep{
		Title: "HTTP Server",
		Content: func(w *forms.Wizard) tview.Primitive {
			// Header text
			header := tview.NewTextView().
				SetDynamicColors(true).
				SetText(`The [cyan]HTTP server[white] provides a web interface for GoClaw.`)
			header.SetBorder(false)

			// Button bar form
			buttons := tview.NewForm()
			buttons.SetBorder(false)
			buttons.SetButtonsAlign(tview.AlignLeft)
			enableFormMouseScroll(buttons, w)

			// Input form (separate so buttons don't mix with input)
			inputForm := tview.NewForm()
			inputForm.SetBorder(false)
			enableFormMouseScroll(inputForm, w)

			// Add input field first so we have the reference
			inputForm.AddInputField("Listen Address", data.HTTPListen, 30, nil, func(text string) {
				data.HTTPListen = text
				data.MarkDirty("HTTPListen")
				if text != "" {
					data.HTTPEnabled = true
					data.MarkDirty("HTTPEnabled")
				}
			})
			listenInput, _ := inputForm.GetFormItemByLabel("Listen Address").(*tview.InputField)

			// Add buttons
			buttons.AddButton("Local Only", func() {
				data.HTTPEnabled = true
				data.HTTPListen = "127.0.0.1:1337"
				data.MarkDirty("HTTPEnabled", "HTTPListen")
				listenInput.SetText(data.HTTPListen)
			})

			buttons.AddButton("Network (IPv4)", func() {
				data.HTTPEnabled = true
				data.HTTPListen = "0.0.0.0:1337"
				data.MarkDirty("HTTPEnabled", "HTTPListen")
				listenInput.SetText(data.HTTPListen)
			})

			buttons.AddButton("Network (All)", func() {
				data.HTTPEnabled = true
				data.HTTPListen = ":1337"
				data.MarkDirty("HTTPEnabled", "HTTPListen")
				listenInput.SetText(data.HTTPListen)
			})

			buttons.AddButton("Disable", func() {
				data.HTTPEnabled = false
				data.HTTPListen = ""
				data.MarkDirty("HTTPEnabled", "HTTPListen")
				listenInput.SetText("")
			})

			// Explanation text (below buttons)
			explanation := tview.NewTextView().
				SetDynamicColors(true).
				SetText(`
  [yellow]Local Only[white] - Only this machine (127.0.0.1:1337)
  [yellow]Network (IPv4)[white] - IPv4 network access (0.0.0.0:1337)
  [yellow]Network (All)[white] - Full network access (:1337)
  [yellow]Disable[white] - No HTTP server`)
			explanation.SetBorder(false)

			// Layout: header, buttons, input, explanation
			layout := tview.NewFlex().
				SetDirection(tview.FlexRow).
				AddItem(header, 2, 0, false).
				AddItem(buttons, 3, 0, false).
				AddItem(inputForm, 3, 0, true).
				AddItem(explanation, 0, 1, false)

			return layout
		},
		OnExit: func(w *forms.Wizard) error {
			if data.HTTPEnabled && data.HTTPListen == "" {
				return fmt.Errorf("listen address is required when HTTP is enabled")
			}
			L_info("wizard: http", "enabled", data.HTTPEnabled, "listen", data.HTTPListen)
			return nil
		},
	}
}

// Step: Speech-to-Text
func stepSTT(data *WizardData) forms.WizardStep {
	return forms.WizardStep{
		Title: "Speech-to-Text",
		Content: func(w *forms.Wizard) tview.Primitive {
			form := tview.NewForm()
			form.SetBorder(false)
			enableFormMouseScroll(form, w)

			// Check if model is available (user dir or system dir)
			defaultModel := "ggml-tiny.en.bin"
			modelsDir := "~/.goclaw/stt/whisper"
			expandedDir, _ := paths.ExpandTilde(modelsDir)

			// Check user directory
			modelAvailable := stt.IsModelDownloaded(expandedDir, defaultModel)
			// Check system directory (fallback for .deb installs)
			if !modelAvailable {
				modelAvailable = stt.IsModelDownloaded("/usr/share/goclaw/stt", defaultModel)
			}
			data.STTModelAvailable = modelAvailable
			data.STTModel = defaultModel

			// Enable by default if model is available
			if modelAvailable && !data.STTEnabled {
				data.STTEnabled = true
			}

			form.AddCheckbox("Enable Speech-to-Text", data.STTEnabled, func(checked bool) {
				data.STTEnabled = checked
				data.MarkDirty("STTEnabled", "STTModel")
			})

			// Model status with colors (5th param enables dynamic colors)
			statusText := "[red]Not available[-] - download required"
			if modelAvailable {
				statusText = "[green]Ready[-] - ggml-tiny.en.bin available"
			}

			form.AddTextView("Model Status", statusText, 50, 1, true, false)

			// Download button (only if model not available)
			if !modelAvailable {
				form.AddButton("Download Model (~39 MB)", func() {
					// Show downloading message
					w.App().ShowModal(
						"Downloading whisper model (ggml-tiny.en.bin)...\n\nThis may take a minute.",
						[]string{},
						nil,
					)

					// Download in background
					go func() {
						model := stt.GetModel(defaultModel)
						if model == nil {
							w.App().App().QueueUpdateDraw(func() {
								w.App().ShowModal("Error: Model not found in catalog", []string{"OK"}, nil)
							})
							return
						}

						err := stt.DownloadModel(model, modelsDir)
						w.App().App().QueueUpdateDraw(func() {
							if err != nil {
								w.App().ShowModal(fmt.Sprintf("Download failed: %v", err), []string{"OK"}, nil)
							} else {
								data.STTModelAvailable = true
								w.App().ShowModal("Model downloaded successfully!", []string{"OK"}, func(idx int, label string) {
									w.RefreshCurrentStep()
								})
							}
						})
					}()
				})
			}

			return formWithHeader(`[cyan]Speech-to-Text[white] enables voice message transcription.

GoClaw uses [yellow]Whisper.cpp[white] for local, offline transcription.
This requires a model file (~39 MB for the tiny English model).

[gray]Advanced options (cloud providers, larger models) available in[white]
[cyan]goclaw setup edit[white] [gray]after initial setup.[white]`, 7, form)
		},
		OnExit: func(w *forms.Wizard) error {
			if data.STTEnabled && !data.STTModelAvailable {
				return fmt.Errorf("please download a model or disable STT")
			}
			L_info("wizard: stt", "enabled", data.STTEnabled, "model", data.STTModel)
			return nil
		},
	}
}

// Step: VoiceLLM (Real-time Voice)
func stepVoiceLLM(data *WizardData) forms.WizardStep {
	return forms.WizardStep{
		Title: "Real-time Voice",
		Content: func(w *forms.Wizard) tview.Primitive {
			form := tview.NewForm()
			form.SetBorder(false)
			enableFormMouseScroll(form, w)

			// Reuse xAI API key from LLM step if available
			if data.VoiceLLMAPIKey == "" && data.LLMProviderID == "xai" && data.LLMAPIKey != "" {
				data.VoiceLLMAPIKey = data.LLMAPIKey
			}

			// Auto-enable if API key is present and HTTP is enabled
			if data.VoiceLLMAPIKey != "" && data.HTTPEnabled && !data.VoiceLLMEnabled {
				data.VoiceLLMEnabled = true
			}

			// If HTTP is disabled, show warning and disable the feature
			if !data.HTTPEnabled {
				return formWithHeader(`[yellow]⚠ HTTP channel is disabled[white]

Real-time voice requires the HTTP channel to be enabled.
Enable it in the [cyan]HTTP Server[white] step to use voice.

You can skip this step and configure voice later via
[cyan]goclaw setup edit[white].`, 7, form)
			}

			// Default voice to Eve if not set
			if data.VoiceLLMVoice == "" {
				data.VoiceLLMVoice = "Eve"
			}

			form.AddCheckbox("Enable real-time voice", data.VoiceLLMEnabled, func(checked bool) {
				data.VoiceLLMEnabled = checked
				data.MarkDirty("VoiceLLMEnabled")
			})

			form.AddPasswordField("xAI API Key", data.VoiceLLMAPIKey, 50, '*', func(text string) {
				data.VoiceLLMAPIKey = text
				data.MarkDirty("VoiceLLMAPIKey")
				// Auto-enable when API key is entered
				if text != "" && !data.VoiceLLMEnabled {
					data.VoiceLLMEnabled = true
					data.MarkDirty("VoiceLLMEnabled")
					w.RefreshCurrentStep()
				}
			})

			// Voice dropdown
			voiceOptions := []string{"Eve", "Ara", "Rex", "Sal", "Leo"}
			voiceDescriptions := map[string]string{
				"Eve": "Female, energetic (default)",
				"Ara": "Female, warm/friendly",
				"Rex": "Male, confident/clear",
				"Sal": "Neutral, smooth/balanced",
				"Leo": "Male, authoritative",
			}

			// Find current voice index
			voiceIndex := 0
			for i, v := range voiceOptions {
				if v == data.VoiceLLMVoice {
					voiceIndex = i
					break
				}
			}

			form.AddDropDown("Voice", voiceOptions, voiceIndex, func(option string, index int) {
				data.VoiceLLMVoice = option
				data.MarkDirty("VoiceLLMVoice")
			})

			// Add voice description
			if desc, ok := voiceDescriptions[data.VoiceLLMVoice]; ok {
				form.AddTextView("", "[gray]"+desc+"[white]", 40, 1, true, false)
			}

			// Test button
			form.AddButton("Test API Key", func() {
				if data.VoiceLLMAPIKey == "" {
					w.App().ShowModal("Please enter an API key first.", []string{"OK"}, nil)
					return
				}

				// Validate voice
				validVoices := map[string]bool{"Eve": true, "Ara": true, "Rex": true, "Sal": true, "Leo": true}
				voice := data.VoiceLLMVoice
				if voice == "" {
					voice = "Eve"
				}
				if !validVoices[voice] {
					w.App().ShowModal(fmt.Sprintf("Unknown voice '%s'. Valid: Eve, Ara, Rex, Sal, Leo", voice), []string{"OK"}, nil)
					return
				}

				// Basic API key format validation for xAI
				if !strings.HasPrefix(data.VoiceLLMAPIKey, "xai-") {
					w.App().ShowModal("xAI API keys typically start with 'xai-'. Please verify your key.", []string{"OK"}, nil)
					return
				}

				w.App().ShowModal(fmt.Sprintf("Configuration valid!\n\nDriver: xAI\nVoice: %s", voice), []string{"OK"}, nil)
			})

			return formWithHeader(`[cyan]Real-time Voice[white] enables natural spoken conversations
via the HTTP [yellow]/voice[white] endpoint.

Uses [yellow]xAI's Realtime Voice API[white] for low-latency voice interaction.

[gray]Advanced options (OpenAI, sample rates) available in[white]
[cyan]goclaw setup edit[white] [gray]after initial setup.[white]`, 7, form)
		},
		OnExit: func(w *forms.Wizard) error {
			if data.VoiceLLMEnabled && data.VoiceLLMAPIKey == "" {
				return fmt.Errorf("xAI API key is required when voice is enabled")
			}
			L_info("wizard: voicellm", "enabled", data.VoiceLLMEnabled, "voice", data.VoiceLLMVoice)
			return nil
		},
	}
}

// Step: Browser Setup (placeholder — will be wired into wizard flow)
func stepBrowser(data *WizardData) forms.WizardStep { //nolint:unused
	return forms.WizardStep{
		Title: "Browser",
		Content: func(w *forms.Wizard) tview.Primitive {
			form := tview.NewForm()
			form.SetBorder(false)
			enableFormMouseScroll(form, w)

			form.AddCheckbox("Set up browser after wizard completes", data.BrowserSetup, func(checked bool) {
				data.BrowserSetup = checked
			})

			return formWithHeader(`[cyan]Browser profiles[white] allow GoClaw to access authenticated websites.
You can set this up later with [yellow]goclaw browser setup[white].`, 3, form)
		},
	}
}

// Step: Sandboxing
func stepSandbox(data *WizardData) forms.WizardStep {
	return forms.WizardStep{
		Title: "Sandboxing",
		Content: func(w *forms.Wizard) tview.Primitive {
			if sandbox.CurrentSandboxBackend() == sandbox.BackendNone {
				tv := tview.NewTextView().
					SetDynamicColors(true).
					SetText(`[yellow]Note:[white] Managed sandboxing is currently implemented for Linux and macOS.
On other platforms, the exec and browser tools run without OS sandbox enforcement.`)
				tv.SetBorder(false)
				return tv
			}

			form := tview.NewForm()
			form.SetBorder(false)
			enableFormMouseScroll(form, w)

			presetLabels := []string{"Assistant (recommended)", "Permissive", "Hardened", "Custom (advanced)"}
			presetValues := []string{SandboxPresetAssistant, SandboxPresetPermissive, SandboxPresetHardened, SandboxPresetCustom}
			presetIndex := 0
			for i, value := range presetValues {
				if value == NormalizeSandboxPreset(data.SandboxPreset) {
					presetIndex = i
					break
				}
			}

			form.AddDropDown("Security preset", presetLabels, presetIndex, func(option string, optionIndex int) {
				if optionIndex < 0 || optionIndex >= len(presetValues) {
					return
				}
				selectedPreset := presetValues[optionIndex]
				warning := SandboxPresetWarningText(selectedPreset)
				previousPreset := NormalizeSandboxPreset(data.SandboxPreset)
				if selectedPreset == SandboxPresetCustom {
					data.SandboxPreset = selectedPreset
					data.SandboxAdvanced = true
					data.MarkDirty("SandboxPreset", "SandboxAdvanced")
					w.App().ShowModal(
						warning.Title+"\n\n"+warning.Body,
						[]string{"OK"},
						nil,
					)
					return
				}
				sandboxPresetConsentModal(w, warning, func() {
					data.SandboxPreset = selectedPreset
					data.MarkDirty("SandboxPreset")
					data.MarkDirty("SandboxConsentPermissive")
					data.MarkDirty("SandboxConsentAssistant")
					data.MarkDirty("SandboxConsentHardened")
					data.SandboxConsentPermissive = selectedPreset == SandboxPresetPermissive
					data.SandboxConsentAssistant = selectedPreset == SandboxPresetAssistant
					data.SandboxConsentHardened = selectedPreset == SandboxPresetHardened
					refreshSandboxConsentCheckbox(form, data)
					if !data.SandboxAdvanced {
						ApplySandboxPreset(data, selectedPreset)
						refreshSandboxAdvancedControls(form, data)
					}
				}, func() {
					if dd, ok := form.GetFormItemByLabel("Security preset").(*tview.DropDown); ok {
						dd.SetCurrentOption(presetDropDownIndex(previousPreset))
					}
				})
			})

			form.AddCheckbox("I acknowledge the selected preset warning", false, func(checked bool) {
				data.SandboxConsentPermissive = false
				data.SandboxConsentAssistant = false
				data.SandboxConsentHardened = false
				if checked {
					switch NormalizeSandboxPreset(data.SandboxPreset) {
					case SandboxPresetPermissive:
						data.SandboxConsentPermissive = true
					case SandboxPresetHardened:
						data.SandboxConsentHardened = true
					default:
						data.SandboxConsentAssistant = true
					}
				}
				data.MarkDirty("SandboxConsentPermissive")
				data.MarkDirty("SandboxConsentAssistant")
				data.MarkDirty("SandboxConsentHardened")
			})
			refreshSandboxConsentCheckbox(form, data)

			form.AddCheckbox("Show advanced sandbox settings", data.SandboxAdvanced, func(checked bool) {
				data.SandboxAdvanced = checked
				data.MarkDirty("SandboxAdvanced")
				if !checked {
					ApplySandboxPreset(data, data.SandboxPreset)
					refreshSandboxAdvancedControls(form, data)
				}
			})

			modeOptions := sandbox.SupportedModeOptions()
			modeLabels := make([]string, 0, len(modeOptions))
			for _, mode := range modeOptions {
				modeLabels = append(modeLabels, mode.Label)
			}
			form.AddDropDown("Sandbox mode", modeLabels, modeDropDownIndex(data.SandboxMode, modeOptions), func(_ string, optionIndex int) {
				if optionIndex < 0 || optionIndex >= len(modeOptions) {
					return
				}
				data.SandboxMode = modeOptions[optionIndex].Value
				data.MarkDirty("SandboxMode")
			})

			form.AddCheckbox("Enable sandboxing", data.SandboxEnabled, func(checked bool) {
				data.SandboxEnabled = checked
				data.MarkDirty("SandboxEnabled")
			})

			form.AddCheckbox("Enable exec sandboxing", data.ExecSandboxEnabled, func(checked bool) {
				data.ExecSandboxEnabled = checked
				data.MarkDirty("ExecSandboxEnabled")
			})

			form.AddCheckbox("Enable browser sandboxing", data.BrowserSandboxEnabled, func(checked bool) {
				data.BrowserSandboxEnabled = checked
				data.MarkDirty("BrowserSandboxEnabled")
			})

			form.AddCheckbox("Enable file tool sandboxing", data.FileToolsSandboxEnabled, func(checked bool) {
				data.FileToolsSandboxEnabled = checked
				data.MarkDirty("FileToolsSandboxEnabled")
			})

			// Skills installation sources
			form.AddTextView("", "\n─── Skill Installation Sources ───", 50, 2, false, false)

			form.AddCheckbox("Allow embedded skills", data.SkillsAllowEmbedded, func(checked bool) {
				data.SkillsAllowEmbedded = checked
				data.MarkDirty("SkillsAllowEmbedded")
			})

			form.AddCheckbox("Allow ClawHub (public repository)", data.SkillsAllowClawHub, func(checked bool) {
				data.SkillsAllowClawHub = checked
				data.MarkDirty("SkillsAllowClawHub")
			})

			form.AddCheckbox("Allow local paths (⚠ security risk)", data.SkillsAllowLocal, func(checked bool) {
				if checked {
					data.SkillsAllowLocal = false
					localSkillsConfirmModal(w, form, func() {
						data.SkillsAllowLocal = true
						data.MarkDirty("SkillsAllowLocal")
					})
					return
				}
				data.SkillsAllowLocal = checked
				data.MarkDirty("SkillsAllowLocal")
			})

			return formWithHeader(`[cyan]Sandboxing presets[white] provide a safer starting point.
Pick a preset, review the warning, and confirm.

Use [yellow]advanced sandbox settings[white] only when you need custom mode/toggle control.

[yellow]Skill Installation Sources[white] control where the agent can install skills from.
Embedded skills are bundled with GoClaw. ClawHub is a public skill repository.`, 7, form)
		},
		OnExit: func(_ *forms.Wizard) error {
			switch NormalizeSandboxPreset(data.SandboxPreset) {
			case SandboxPresetPermissive:
				if !data.SandboxConsentPermissive {
					return fmt.Errorf("please acknowledge the Permissive warning before continuing")
				}
			case SandboxPresetHardened:
				if !data.SandboxConsentHardened {
					return fmt.Errorf("please acknowledge the Hardened warning before continuing")
				}
			case SandboxPresetCustom:
				// Custom mode is an explicit advanced path; no preset consent checkbox required.
			default:
				if !data.SandboxConsentAssistant {
					return fmt.Errorf("please acknowledge the Assistant warning before continuing")
				}
			}
			return nil
		},
	}
}

func sandboxPresetConsentModal(w *forms.Wizard, warning SandboxPresetWarning, onConfirm func(), onCancel func()) {
	w.App().ShowModal(
		warning.Title+"\n\n"+warning.Body+"\n\n"+warning.Consent+"\n\nContinue?",
		[]string{"No", "Yes"},
		func(buttonIndex int, buttonLabel string) {
			if buttonLabel == "Yes" {
				onConfirm()
				return
			}
			if onCancel != nil {
				onCancel()
			}
		},
	)
}

func presetDropDownIndex(preset string) int {
	switch NormalizeSandboxPreset(preset) {
	case SandboxPresetPermissive:
		return 1
	case SandboxPresetHardened:
		return 2
	case SandboxPresetCustom:
		return 3
	default:
		return 0
	}
}

func modeDropDownIndex(mode string, options []forms.Option) int {
	for i, option := range options {
		if option.Value == mode {
			return i
		}
	}
	for i, option := range options {
		if option.Value == sandbox.ModeHome {
			return i
		}
	}
	return 0
}

func refreshSandboxAdvancedControls(form *tview.Form, data *WizardData) {
	if modeDropdown, ok := form.GetFormItemByLabel("Sandbox mode").(*tview.DropDown); ok {
		modeDropdown.SetCurrentOption(modeDropDownIndex(data.SandboxMode, sandbox.SupportedModeOptions()))
	}
	if sandboxEnabled, ok := form.GetFormItemByLabel("Enable sandboxing").(*tview.Checkbox); ok {
		sandboxEnabled.SetChecked(data.SandboxEnabled)
	}
	if execEnabled, ok := form.GetFormItemByLabel("Enable exec sandboxing").(*tview.Checkbox); ok {
		execEnabled.SetChecked(data.ExecSandboxEnabled)
	}
	if browserEnabled, ok := form.GetFormItemByLabel("Enable browser sandboxing").(*tview.Checkbox); ok {
		browserEnabled.SetChecked(data.BrowserSandboxEnabled)
	}
	if fileToolsEnabled, ok := form.GetFormItemByLabel("Enable file tool sandboxing").(*tview.Checkbox); ok {
		fileToolsEnabled.SetChecked(data.FileToolsSandboxEnabled)
	}
}

func refreshSandboxConsentCheckbox(form *tview.Form, data *WizardData) {
	if consent, ok := form.GetFormItemByLabel("I acknowledge the selected preset warning").(*tview.Checkbox); ok {
		checked := false
		switch NormalizeSandboxPreset(data.SandboxPreset) {
		case SandboxPresetPermissive:
			checked = data.SandboxConsentPermissive
		case SandboxPresetHardened:
			checked = data.SandboxConsentHardened
		default:
			checked = data.SandboxConsentAssistant
		}
		consent.SetChecked(checked)
	}
}

// localSkillsConfirmModal warns about the security risks of enabling local path skills.
func localSkillsConfirmModal(w *forms.Wizard, form *tview.Form, onConfirm func()) {
	w.App().ShowModal(
		"⚠ SECURITY WARNING ⚠\n\n"+
			"Enabling local path skills allows installing from any filesystem path.\n\n"+
			"A malicious script could create a fake skill directory and trick\n"+
			"the agent into installing it.\n\n"+
			"Only enable if you understand the risks.\n\nContinue?",
		[]string{"No", "Yes"},
		func(buttonIndex int, buttonLabel string) {
			if buttonLabel == "Yes" {
				onConfirm()
				cb := form.GetFormItemByLabel("Allow local paths (⚠ security risk)")
				if checkbox, ok := cb.(*tview.Checkbox); ok {
					checkbox.SetChecked(true)
				}
			}
		},
	)
}

// Step: LLM Provider selection + config (combined into one wizard step)
func stepLLMProvider(data *WizardData) forms.WizardStep {
	return forms.WizardStep{
		Title: "LLM Provider",
		Content: func(w *forms.Wizard) tview.Primitive {
			// If we already have a provider selected (re-run), show the config form
			if data.LLMProviderID != "" && !data.LLMSkipped {
				return buildLLMConfigForm(data, w)
			}
			return buildLLMProviderList(data, w)
		},
	}
}

// buildLLMProviderList shows the flat provider list for selection.
func buildLLMProviderList(data *WizardData, w *forms.Wizard) tview.Primitive {
	meta := metadata.Get()
	providerIDs := meta.ModelProviderIDs()

	list := tview.NewList()
	list.SetBorder(false)
	list.ShowSecondaryText(false)

	for _, pid := range providerIDs {
		providerID := pid
		prov, ok := meta.GetModelProvider(providerID)
		if !ok {
			continue
		}
		list.AddItem(prov.Name, "", 0, func() {
			data.LLMProviderID = providerID
			data.LLMProviderName = prov.Name
			data.LLMDriver = prov.Driver
			data.LLMBaseURL = prov.APIEndpoint
			data.LLMAPIKey = ""
			data.LLMModel = ""
			data.LLMSkipped = false
			data.MarkDirty("LLMProviderID", "LLMDriver", "LLMBaseURL", "LLMModel")

			large, _ := meta.GetDefaultModels(providerID)
			data.LLMModel = large

			w.RefreshCurrentStep()
		})
	}

	list.AddItem("", "", 0, nil) // separator
	list.AddItem("Skip (configure later)", "", 0, func() {
		data.LLMSkipped = true
		data.LLMProviderID = ""
		w.NextStep()
	})

	header := tview.NewTextView().
		SetDynamicColors(true).
		SetText(`GoClaw needs an LLM provider to function. This is the AI model
that powers your agent — it handles conversations, tool use, and reasoning.

[yellow]Select a provider and enter your API key.[white]
[gray]You can add more providers later with[white] [cyan]goclaw setup edit[white]`)
	header.SetBorder(false)

	layout := tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(header, 5, 0, false).
		AddItem(list, 0, 1, true)

	return layout
}

// buildLLMConfigForm shows the API key / URL form for the selected provider.
func buildLLMConfigForm(data *WizardData, w *forms.Wizard) tview.Primitive {
	meta := metadata.Get()
	isLocal := llm.DriverOrEndpointIsLocal(data.LLMDriver, data.LLMBaseURL)

	// Provider info header (static)
	headerInfo := fmt.Sprintf("[cyan]%s[white]\n", data.LLMProviderName)
	headerInfo += fmt.Sprintf("Driver:   %s\n", data.LLMDriver)
	if !isLocal {
		headerInfo += fmt.Sprintf("Endpoint: %s", data.LLMBaseURL)
	}
	header := tview.NewTextView().SetDynamicColors(true).SetText(headerInfo)
	header.SetBorder(false)

	// Model info panel (updates when dropdown changes)
	modelInfo := tview.NewTextView().SetDynamicColors(true)
	modelInfo.SetBorder(false)

	updateModelInfo := func(modelID string) {
		if model, ok := meta.GetModel(data.LLMProviderID, modelID); ok {
			var lines []string
			lines = append(lines, fmt.Sprintf("[green]%s[white] (%s)", model.Name, modelID))
			lines = append(lines, fmt.Sprintf("Context: %dk  Output: %dk", model.ContextWindow/1000, model.MaxOutputTokens/1000))

			var caps []string
			if model.Capabilities.Vision {
				caps = append(caps, "Vision")
			}
			if model.Capabilities.ToolUse {
				caps = append(caps, "Tool Use")
			}
			if model.Capabilities.Reasoning {
				caps = append(caps, "Reasoning")
			}
			if len(caps) > 0 {
				lines = append(lines, strings.Join(caps, " | "))
			}
			lines = append(lines, fmt.Sprintf("Cost: $%.2f / $%.2f per 1M tokens", model.Cost.Input, model.Cost.Output))
			modelInfo.SetText(strings.Join(lines, "\n"))
		} else {
			modelInfo.SetText(fmt.Sprintf("%s (no metadata available)", modelID))
		}
	}

	// Form
	form := tview.NewForm()
	form.SetBorder(false)
	enableFormMouseScroll(form, w)

	if isLocal {
		form.AddInputField("URL", data.LLMBaseURL, 50, nil, func(text string) {
			data.LLMBaseURL = text
			data.MarkDirty("LLMBaseURL")
		})
		form.AddInputField("Model", data.LLMModel, 50, nil, func(text string) {
			data.LLMModel = text
			data.MarkDirty("LLMModel")
		})
	} else {
		// Model dropdown
		modelIDs := meta.GetKnownChatModels(data.LLMProviderID)
		large, _ := meta.GetDefaultModels(data.LLMProviderID)

		if len(modelIDs) > 0 {
			options := make([]string, 0, len(modelIDs))
			selectedIdx := 0
			for i, mid := range modelIDs {
				label := mid
				if mid == large {
					label += " (default)"
				}
				if m, ok := meta.GetModel(data.LLMProviderID, mid); ok {
					label = fmt.Sprintf("%s - %s", m.Name, mid)
					if mid == large {
						label += " (default)"
					}
				}
				options = append(options, label)
				if mid == data.LLMModel || (data.LLMModel == "" && mid == large) {
					selectedIdx = i
				}
			}

			form.AddDropDown("Agent Model", options, selectedIdx, func(option string, index int) {
				if index >= 0 && index < len(modelIDs) {
					data.LLMModel = modelIDs[index]
					data.MarkDirty("LLMModel")
					updateModelInfo(data.LLMModel)
				}
			})

			// Set initial model
			if data.LLMModel == "" {
				data.LLMModel = large
			}
		}

		form.AddPasswordField("API Key", data.LLMAPIKey, 60, '*', func(text string) {
			data.LLMAPIKey = text
			data.MarkDirty("LLMAPIKey")
		})
	}

	form.AddButton("Change Provider", func() {
		data.LLMProviderID = ""
		data.LLMProviderName = ""
		w.RefreshCurrentStep()
	})

	// Initialize model info
	if data.LLMModel != "" {
		updateModelInfo(data.LLMModel)
	}

	layout := tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(header, 4, 0, false).
		AddItem(form, 7, 0, true).
		AddItem(modelInfo, 5, 0, false).
		AddItem(nil, 0, 1, false)

	return layout
}

// Step: Review
func stepReview(data *WizardData) forms.WizardStep {
	return forms.WizardStep{
		Title: "Review",
		Content: func(w *forms.Wizard) tview.Primitive {
			summary := fmt.Sprintf(`[cyan]Configuration Summary[white]

Workspace:    %s
Agent:        %s%s
User:         %s (%s)
Telegram:     %s
WhatsApp:     %s
HTTP:         %s
Security:     %s
LLM:          %s
Voice LLM:    %s
STT:          %s

Press [yellow]Finish[white] to complete setup.`,
				data.WorkspacePath,
				data.AgentName,
				func() string {
					if data.AgentEmoji == "" {
						return ""
					}
					return " (" + data.AgentEmoji + ")"
				}(),
				data.UserDisplayName,
				data.UserName,
				boolToEnabled(data.TelegramEnabled),
				boolToEnabled(data.WhatsAppEnabled),
				formatHTTP(data),
				wizardSecuritySummary(data),
				wizardLLMSummary(data),
				wizardVoiceSummary(data),
				wizardSTTSummary(data),
			)

			tv := tview.NewTextView().
				SetDynamicColors(true).
				SetText(summary)
			tv.SetBorder(false)
			return tv
		},
	}
}

// Helper functions

func valueOrDefault(val, def string) string {
	if val == "" {
		return def
	}
	return val
}

func boolToConfigured(b bool) string {
	if b {
		return "configured"
	}
	return "not configured"
}

func boolToEnabled(b bool) string {
	if b {
		return "enabled"
	}
	return "disabled"
}

func wizardSecuritySummary(data *WizardData) string {
	switch NormalizeSandboxPreset(data.SandboxPreset) {
	case SandboxPresetPermissive:
		return "Permissive - least restricted, best flexibility"
	case SandboxPresetHardened:
		return "Hardened - stronger protection with reduced capability"
	case SandboxPresetCustom:
		return "Custom - advanced manually selected security settings"
	default:
		return "Assistant - balanced protection for normal use"
	}
}

func wizardLLMSummary(data *WizardData) string {
	if data == nil || data.LLMSkipped || data.LLMProviderID == "" {
		return "Not configured"
	}
	provider := data.LLMProviderName
	if strings.TrimSpace(provider) == "" {
		provider = data.LLMProviderID
	}
	model := strings.TrimSpace(data.LLMModel)
	if model == "" {
		return provider
	}
	return fmt.Sprintf("%s - %s", provider, model)
}

func wizardVoiceSummary(data *WizardData) string {
	if data == nil || !data.VoiceLLMEnabled {
		return "Disabled"
	}
	voice := strings.TrimSpace(data.VoiceLLMVoice)
	if voice == "" {
		return "Enabled"
	}
	return fmt.Sprintf("Enabled (%s)", voice)
}

func wizardSTTSummary(data *WizardData) string {
	if data == nil || !data.STTEnabled {
		return "Disabled"
	}
	model := strings.TrimSpace(data.STTModel)
	if model == "" {
		return "Enabled"
	}
	return fmt.Sprintf("Enabled (%s)", model)
}

func boolToSetup(b bool) string { //nolint:unused
	if b {
		return "will setup after wizard"
	}
	return "skip (can setup later)"
}

func formatHTTP(data *WizardData) string {
	if !data.HTTPEnabled {
		return "disabled"
	}
	return data.HTTPListen
}

func sanitizeUsername(name string) string {
	// Simple sanitization: lowercase, replace spaces with underscores
	result := ""
	for _, r := range name {
		if r >= 'a' && r <= 'z' {
			result += string(r)
		} else if r >= 'A' && r <= 'Z' {
			result += string(r + 32) // lowercase
		} else if r == ' ' || r == '-' {
			result += "_"
		}
	}
	if result == "" {
		result = "user"
	}
	return result
}

// formWithHeader creates a layout with colored header text above a form
// The header supports tview color markup like [yellow], [green], [white], etc.
func formWithHeader(headerText string, headerLines int, form *tview.Form) tview.Primitive {
	header := tview.NewTextView().
		SetDynamicColors(true).
		SetText(headerText)
	header.SetBorder(false)

	layout := tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(header, headerLines, 0, false).
		AddItem(form, 0, 1, true)

	return layout
}

// printWizardConfig saves the wizard config and users to their respective files
func printWizardConfig(data *WizardData) {
	// Get save paths
	configPath, err := paths.DefaultConfigPath()
	if err != nil {
		fmt.Printf("Error getting config path: %v\n", err)
		return
	}
	usersPath, err := paths.UsersPath(configPath)
	if err != nil {
		fmt.Printf("Error getting users path: %v\n", err)
		return
	}

	// Ensure parent directory exists
	if err := paths.EnsureParentDir(configPath); err != nil {
		fmt.Printf("Error creating config directory: %v\n", err)
		return
	}

	// Build and save config
	cfg := buildConfigFromWizardData(data)
	if err := config.BackupAndWriteJSON(configPath, cfg, config.DefaultBackupCount); err != nil {
		fmt.Printf("Error saving config: %v\n", err)
		return
	}
	fmt.Printf("Configuration saved to: %s\n", configPath)

	// Build and save users
	userEntry := map[string]interface{}{
		"name": data.UserDisplayName,
		"role": data.UserRole,
	}
	if data.UserTelegramID != "" {
		userEntry["telegram_id"] = data.UserTelegramID
	}
	if data.UserWhatsAppID != "" {
		userEntry["whatsapp_id"] = data.UserWhatsAppID
	}
	if data.UserPassword != "" {
		hash, err := user.HashPassword(data.UserPassword)
		if err != nil {
			fmt.Printf("Error hashing password: %v\n", err)
		} else {
			userEntry["http_password_hash"] = hash
		}
	} else if data.UserExistingPwdHash != "" {
		userEntry["http_password_hash"] = data.UserExistingPwdHash
	}
	users := map[string]interface{}{
		data.UserName: userEntry,
	}

	if err := config.BackupAndWriteJSON(usersPath, users, config.DefaultBackupCount); err != nil {
		fmt.Printf("Error saving users: %v\n", err)
		return
	}
	fmt.Printf("Users saved to: %s\n", usersPath)

	// Print next steps
	fmt.Println("\nSetup complete! Start GoClaw with:")
	if data.LLMSkipped || data.LLMProviderID == "" {
		fmt.Println("  1. Run 'goclaw setup edit' to configure LLM providers")
		fmt.Println("  2. Run 'goclaw start' (recommended - runs with supervisor)")
		fmt.Println("     or 'goclaw gateway' to run in foreground")
	} else {
		fmt.Println("  goclaw start       (recommended - runs with supervisor)")
		fmt.Println("  goclaw gateway     (runs in foreground)")
	}
	fmt.Println("\nOptional:")
	fmt.Println("  - Run 'goclaw setup edit' to add more providers or fine-tune settings")
	fmt.Println("  - Run 'goclaw browser setup' to configure browser profiles for authenticated web access")
}

// buildConfigFromWizardData creates a config structure from wizard data.
// When an existing config exists, starts from it to preserve all settings,
// then overlays ONLY the fields that were actually modified (dirty).
func buildConfigFromWizardData(data *WizardData) map[string]interface{} {
	var cfg map[string]interface{}

	if data.ExistingConfig != nil {
		raw, err := json.Marshal(data.ExistingConfig)
		if err == nil {
			_ = json.Unmarshal(raw, &cfg)
		}
	}
	if cfg == nil {
		cfg = make(map[string]interface{})
	}

	// Workspace - only if dirty
	if data.HasAnyDirty("AgentName", "AgentEmoji", "AgentTyping") {
		deepSet(cfg, "agent.name", data.AgentName)
		deepSet(cfg, "agent.emoji", data.AgentEmoji)
		deepSet(cfg, "agent.typing", data.AgentTyping)
	}

	// Workspace - only if dirty
	if data.IsDirty("WorkspacePath") {
		deepSet(cfg, "gateway.workingDir", data.WorkspacePath)
	}

	// Channels - only if any channel field is dirty
	if data.HasAnyDirty("TelegramEnabled", "TelegramToken") {
		deepSet(cfg, "channels.telegram.enabled", data.TelegramEnabled)
		deepSet(cfg, "channels.telegram.botToken", data.TelegramToken)
	}
	if data.IsDirty("WhatsAppEnabled") {
		deepSet(cfg, "channels.whatsapp.enabled", data.WhatsAppEnabled)
	}
	if data.HasAnyDirty("HTTPEnabled", "HTTPListen") {
		deepSet(cfg, "channels.http.enabled", data.HTTPEnabled)
		deepSet(cfg, "channels.http.listen", data.HTTPListen)
	}

	// Session store path - always set if config is new
	if data.ExistingConfig == nil {
		storePath, _ := paths.DataPath("sessions.db")
		deepSet(cfg, "session.storePath", storePath)
	}

	// Security/Sandboxing - only if dirty
	if data.HasAnyDirty(
		"SandboxPreset",
		"SandboxAdvanced",
		"SandboxMode",
		"SandboxEnabled",
		"ExecSandboxEnabled",
		"BrowserSandboxEnabled",
		"FileToolsSandboxEnabled",
		"SandboxConsentPermissive",
		"SandboxConsentAssistant",
		"SandboxConsentHardened",
	) {
		enabled := data.SandboxEnabled
		mode := data.SandboxMode
		execEnabled := data.ExecSandboxEnabled
		browserEnabled := data.BrowserSandboxEnabled
		fileToolsEnabled := data.FileToolsSandboxEnabled

		if !data.SandboxAdvanced {
			presetData := *data
			ApplySandboxPreset(&presetData, data.SandboxPreset)
			enabled = presetData.SandboxEnabled
			mode = presetData.SandboxMode
			execEnabled = presetData.ExecSandboxEnabled
			browserEnabled = presetData.BrowserSandboxEnabled
			fileToolsEnabled = presetData.FileToolsSandboxEnabled
		}

		deepSet(cfg, "sandbox.general.enabled", enabled)
		deepSet(cfg, "sandbox.general.mode", mode)
		deepSet(cfg, "sandbox.general.execEnabled", execEnabled)
		deepSet(cfg, "sandbox.general.browserEnabled", browserEnabled)
		deepSet(cfg, "sandbox.general.fileToolsEnabled", fileToolsEnabled)
	}

	// Skills installation sources - only if dirty
	if data.HasAnyDirty("SkillsAllowEmbedded", "SkillsAllowClawHub", "SkillsAllowLocal") {
		deepSet(cfg, "skills.install.allowEmbedded", data.SkillsAllowEmbedded)
		deepSet(cfg, "skills.install.allowClawHub", data.SkillsAllowClawHub)
		deepSet(cfg, "skills.install.allowLocal", data.SkillsAllowLocal)
	}

	// LLM provider - only if dirty
	if data.HasAnyDirty("LLMProviderID", "LLMDriver", "LLMAPIKey", "LLMBaseURL", "LLMModel") {
		if !data.LLMSkipped && data.LLMProviderID != "" {
			alias := data.LLMProviderID

			deepSet(cfg, "llm.providers."+alias+".driver", data.LLMDriver)
			deepSet(cfg, "llm.providers."+alias+".subtype", data.LLMProviderID)

			if data.LLMAPIKey != "" {
				deepSet(cfg, "llm.providers."+alias+".apiKey", data.LLMAPIKey)
			}
			if data.LLMBaseURL != "" {
				deepSet(cfg, "llm.providers."+alias+".baseURL", data.LLMBaseURL)
			}

			// Anthropic: auto-enable prompt caching
			if data.LLMDriver == "anthropic" {
				deepSet(cfg, "llm.providers."+alias+".promptCaching", true)
			}

			// Agent chain: replace first model, preserve fallbacks
			ref := alias + "/" + data.LLMModel
			agentModels := getStringSlice(cfg, "llm.agent.models")
			if len(agentModels) > 0 {
				agentModels[0] = ref
			} else {
				agentModels = []string{ref}
			}
			deepSet(cfg, "llm.agent.models", agentModels)
		}
	}

	// STT (Speech-to-Text) - only if dirty
	if data.HasAnyDirty("STTEnabled", "STTModel") {
		if data.STTEnabled {
			deepSet(cfg, "stt.enabled", true)
			deepSet(cfg, "stt.provider", "whispercpp")
			deepSet(cfg, "stt.whispercpp.model", data.STTModel)
			deepSet(cfg, "stt.whispercpp.modelsDir", "~/.goclaw/stt/whisper")
		} else {
			deepSet(cfg, "stt.enabled", false)
		}
	}

	// VoiceLLM (Real-time Voice) - only if dirty
	if data.HasAnyDirty("VoiceLLMEnabled", "VoiceLLMAPIKey", "VoiceLLMVoice") {
		if data.VoiceLLMEnabled && data.VoiceLLMAPIKey != "" {
			deepSet(cfg, "voicellm.enabled", true)
			deepSet(cfg, "voicellm.default", "xai")
			deepSet(cfg, "voicellm.providers.xai.driver", "xai")
			deepSet(cfg, "voicellm.providers.xai.apiKey", data.VoiceLLMAPIKey)
			if data.VoiceLLMVoice != "" {
				deepSet(cfg, "voicellm.providers.xai.voice", data.VoiceLLMVoice)
			}
		} else {
			deepSet(cfg, "voicellm.enabled", false)
		}
	}

	ensureBuiltInHugotConfigMap(cfg)
	return cfg
}

// deepSet sets a value at a dotted path in a nested map, creating
// intermediate maps as needed. Does not destroy sibling keys.
func deepSet(m map[string]interface{}, path string, value interface{}) {
	parts := strings.Split(path, ".")
	current := m
	for i, part := range parts {
		if i == len(parts)-1 {
			current[part] = value
			return
		}
		next, ok := current[part]
		if !ok {
			next = make(map[string]interface{})
			current[part] = next
		}
		if nextMap, ok := next.(map[string]interface{}); ok {
			current = nextMap
		} else {
			newMap := make(map[string]interface{})
			current[part] = newMap
			current = newMap
		}
	}
}

// getStringSlice extracts a []string from a nested map at a dotted path.
func getStringSlice(m map[string]interface{}, path string) []string {
	parts := strings.Split(path, ".")
	current := m
	for i, part := range parts {
		if i == len(parts)-1 {
			if val, ok := current[part]; ok {
				if slice, ok := val.([]interface{}); ok {
					result := make([]string, 0, len(slice))
					for _, v := range slice {
						if s, ok := v.(string); ok {
							result = append(result, s)
						}
					}
					return result
				}
				if slice, ok := val.([]string); ok {
					return slice
				}
			}
			return nil
		}
		next, ok := current[part]
		if !ok {
			return nil
		}
		if nextMap, ok := next.(map[string]interface{}); ok {
			current = nextMap
		} else {
			return nil
		}
	}
	return nil
}

func ensureBuiltInHugotConfigMap(cfg map[string]interface{}) {
	deepSet(cfg, "llm.providers."+llm.BuiltInHugotProviderAlias+".driver", "hugot")
	deepSet(cfg, "llm.providers."+llm.BuiltInHugotProviderAlias+".embeddingOnly", true)
	deepSet(cfg, "llm.providers."+llm.BuiltInHugotProviderAlias+".subtype", "hugot")

	embeddingModels := getStringSlice(cfg, "llm.embeddings.models")
	if len(embeddingModels) == 0 {
		deepSet(cfg, "llm.embeddings.models", []string{
			llm.BuiltInHugotProviderAlias + "/" + llm.DefaultHugotEmbeddingModel,
		})
	}
}
