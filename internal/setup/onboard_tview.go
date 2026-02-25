// Package setup - tview-based onboarding wizard
package setup

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/rivo/tview"
	"github.com/roelfdiedericks/goclaw/internal/config"
	"github.com/roelfdiedericks/goclaw/internal/config/forms"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/metadata"
	"github.com/roelfdiedericks/goclaw/internal/paths"
	"github.com/roelfdiedericks/goclaw/internal/user"
)

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

	// User setup
	UserName         string
	UserDisplayName  string
	UserRole         string
	UserTelegramID   string
	UserPassword         string
	UserPasswordConf     string
	UserExistingPwdHash  string // preserved from existing users.json

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
	ExecBubblewrap    bool
	BrowserBubblewrap bool

	// LLM
	LLMProviderID   string
	LLMProviderName string
	LLMDriver       string
	LLMAPIKey       string
	LLMBaseURL      string
	LLMModel        string
	LLMSkipped      bool
}

// NewWizardData creates a new WizardData with defaults
func NewWizardData() *WizardData {
	return &WizardData{
		UserRole:          "owner",
		HTTPEnabled:       true,
		HTTPListen:        "127.0.0.1:1337",
		ExecBubblewrap:    true,
		BrowserBubblewrap: true,
	}
}

// LoadFromExisting populates WizardData from existing config
func (d *WizardData) LoadFromExisting(cfg *config.Config, path string) {
	d.ConfigExists = true
	d.ConfigPath = path
	d.ExistingConfig = cfg

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
	d.HTTPListen = cfg.Channels.HTTP.Listen

	// Sandboxing
	d.ExecBubblewrap = cfg.Tools.Exec.Bubblewrap.Enabled
	d.BrowserBubblewrap = cfg.Tools.Browser.Bubblewrap.Enabled

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
				if provCfg.Driver == "ollama" {
					d.LLMBaseURL = provCfg.URL
				} else {
					d.LLMBaseURL = provCfg.BaseURL
				}
				d.LLMModel = parts[1]

				if prov, ok := metadata.Get().GetModelProvider(d.LLMProviderID); ok {
					d.LLMProviderName = prov.Name
				}
			}
		}
	}

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
	}
	if user.HTTPPasswordHash != "" {
		d.UserExistingPwdHash = user.HTTPPasswordHash
	}

	L_info("wizard: loaded user from users.json", "username", ownerUsername)
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
				d.WorkspacePath = ws
			}
		}
	}

	// Extract Telegram
	if channels, ok := d.OpenClawConfig["channels"].(map[string]interface{}); ok {
		if telegram, ok := channels["telegram"].(map[string]interface{}); ok {
			if token, ok := telegram["botToken"].(string); ok {
				d.TelegramToken = token
				d.TelegramEnabled = true
			}
			if allowFrom, ok := telegram["allowFrom"].([]interface{}); ok && len(allowFrom) > 0 {
				if id, ok := allowFrom[0].(string); ok {
					d.UserTelegramID = id
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
		stepWorkspace(data),
		stepUserSetup(data),
		stepTelegram(data),
		stepWhatsApp(data),
		stepHTTP(data),
		stepLLMProvider(data),
		stepSandbox(data),
		stepReview(data),
	)

	return steps
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

			form.AddTextView("", fmt.Sprintf(`Found existing OpenClaw installation at ~/.openclaw/

Detected settings:
  Workspace: %s
  Telegram: %s

Would you like to import these settings?
`, valueOrDefault(data.WorkspacePath, "(not set)"),
				boolToConfigured(data.TelegramToken != "")), 0, 8, false, false)

			form.AddButton("Yes, Import Settings", func() {
				data.OpenClawImport = true
				L_info("wizard: will import OpenClaw settings")
				w.NextStep()
			})

			form.AddButton("No, Start Fresh", func() {
				data.OpenClawImport = false
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

			// Default workspace path
			if data.WorkspacePath == "" {
				data.WorkspacePath = DefaultWorkspacePath()
			}

			form.AddInputField("Workspace Path", data.WorkspacePath, 50, nil, func(text string) {
				data.WorkspacePath = text
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

			form.AddInputField("Your Name", data.UserDisplayName, 40, nil, func(text string) {
				data.UserDisplayName = text
			})

			form.AddInputField("Username (lowercase)", data.UserName, 20, nil, func(text string) {
				data.UserName = text
			})

			form.AddInputField("Telegram User ID (optional)", data.UserTelegramID, 20, nil, func(text string) {
				data.UserTelegramID = text
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

			form.AddCheckbox("Enable Telegram Bot", data.TelegramEnabled, func(checked bool) {
				data.TelegramEnabled = checked
			})

			form.AddInputField("Bot Token", data.TelegramToken, 50, nil, func(text string) {
				data.TelegramToken = text
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

			form.AddCheckbox("Enable WhatsApp Channel", data.WhatsAppEnabled, func(checked bool) {
				data.WhatsAppEnabled = checked
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

			// Input form (separate so buttons don't mix with input)
			inputForm := tview.NewForm()
			inputForm.SetBorder(false)

			// Add input field first so we have the reference
			inputForm.AddInputField("Listen Address", data.HTTPListen, 30, nil, func(text string) {
				data.HTTPListen = text
				if text != "" {
					data.HTTPEnabled = true
				}
			})
			listenInput, _ := inputForm.GetFormItemByLabel("Listen Address").(*tview.InputField)

			// Add buttons
			buttons.AddButton("Local Only", func() {
				data.HTTPEnabled = true
				data.HTTPListen = "127.0.0.1:1337"
				listenInput.SetText(data.HTTPListen)
			})

			buttons.AddButton("Network (IPv4)", func() {
				data.HTTPEnabled = true
				data.HTTPListen = "0.0.0.0:1337"
				listenInput.SetText(data.HTTPListen)
			})

			buttons.AddButton("Network (All)", func() {
				data.HTTPEnabled = true
				data.HTTPListen = ":1337"
				listenInput.SetText(data.HTTPListen)
			})

			buttons.AddButton("Disable", func() {
				data.HTTPEnabled = false
				data.HTTPListen = ""
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

// Step: Browser Setup (placeholder)
func stepBrowser(data *WizardData) forms.WizardStep {
	return forms.WizardStep{
		Title: "Browser",
		Content: func(w *forms.Wizard) tview.Primitive {
			form := tview.NewForm()
			form.SetBorder(false)

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
			isLinux := isLinuxOS()

			if !isLinux {
				tv := tview.NewTextView().
					SetDynamicColors(true).
					SetText(`[yellow]Note:[white] Bubblewrap sandboxing is only available on Linux.
The exec and browser tools will run without kernel sandboxing.`)
				tv.SetBorder(false)
				return tv
			}

			form := tview.NewForm()
			form.SetBorder(false)

			form.AddCheckbox("Enable exec sandboxing", data.ExecBubblewrap, func(checked bool) {
				if !checked {
					data.ExecBubblewrap = true
					sandboxConfirmModal(w, form, 0, func() {
						data.ExecBubblewrap = false
					})
					return
				}
				data.ExecBubblewrap = checked
			})

			form.AddCheckbox("Enable browser sandboxing", data.BrowserBubblewrap, func(checked bool) {
				if !checked {
					data.BrowserBubblewrap = true
					sandboxConfirmModal(w, form, 1, func() {
						data.BrowserBubblewrap = false
					})
					return
				}
				data.BrowserBubblewrap = checked
			})

			return formWithHeader(`[cyan]Sandboxing[white] restricts tools to only access files within your workspace,
preventing accidental or malicious access to system files.

[green]Highly recommended.[white] Disabling gives the agent unrestricted filesystem access.`, 5, form)
		},
	}
}

// sandboxConfirmModal pops a modal asking the user to confirm disabling sandbox.
// If confirmed, onConfirm is called and the checkbox is unchecked visually.
// If cancelled, the checkbox stays checked (data was already reverted by caller).
func sandboxConfirmModal(w *forms.Wizard, form *tview.Form, checkboxIndex int, onConfirm func()) {
	w.App().ShowModal(
		"Disabling sandbox gives the agent unrestricted filesystem access.\n\n"+
			"Only recommended if you trust all installed skills and prompts.\n\nContinue?",
		[]string{"No", "Yes"},
		func(buttonIndex int, buttonLabel string) {
			if buttonLabel == "Yes" {
				onConfirm()
				cb := form.GetFormItemByLabel("Enable exec sandboxing")
				if checkboxIndex == 1 {
					cb = form.GetFormItemByLabel("Enable browser sandboxing")
				}
				if checkbox, ok := cb.(*tview.Checkbox); ok {
					checkbox.SetChecked(false)
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
	isLocal := data.LLMDriver == "ollama" || data.LLMProviderID == "lmstudio"

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

	if isLocal {
		form.AddInputField("URL", data.LLMBaseURL, 50, nil, func(text string) {
			data.LLMBaseURL = text
		})
		form.AddInputField("Model", data.LLMModel, 50, nil, func(text string) {
			data.LLMModel = text
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
			llmInfo := "(skipped)"
			if !data.LLMSkipped && data.LLMProviderID != "" {
				llmInfo = fmt.Sprintf("%s (%s)", data.LLMProviderName, data.LLMModel)
			}

			summary := fmt.Sprintf(`[cyan]Configuration Summary[white]

Workspace:    %s
User:         %s (%s)
Telegram:     %s
WhatsApp:     %s
HTTP:         %s
Sandboxing:   exec=%v, browser=%v
LLM:          %s

Press [yellow]Finish[white] to complete setup.`,
				data.WorkspacePath,
				data.UserDisplayName,
				data.UserName,
				boolToEnabled(data.TelegramEnabled),
				boolToEnabled(data.WhatsAppEnabled),
				formatHTTP(data),
				data.ExecBubblewrap,
				data.BrowserBubblewrap,
				llmInfo,
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

func boolToSetup(b bool) string {
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

func isLinuxOS() bool {
	// Check GOOS at runtime
	return os.Getenv("GOOS") == "linux" || fileExists("/proc/version")
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
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
	fmt.Println("\nSetup complete! Next steps:")
	if data.LLMSkipped || data.LLMProviderID == "" {
		fmt.Println("  1. Run 'goclaw setup edit' to configure LLM providers")
		fmt.Println("  2. Run 'goclaw tui' to start GoClaw")
	} else {
		fmt.Println("  1. Run 'goclaw tui' to start GoClaw")
	}
	fmt.Println("\nOptional:")
	fmt.Println("  - Run 'goclaw setup edit' to add more providers or fine-tune settings")
	fmt.Println("  - Run 'goclaw browser setup' to configure browser profiles for authenticated web access")
}

// buildConfigFromWizardData creates a config structure from wizard data.
// When an existing config exists, starts from it to preserve all settings,
// then overlays only the fields the wizard manages.
func buildConfigFromWizardData(data *WizardData) map[string]interface{} {
	var cfg map[string]interface{}

	if data.ExistingConfig != nil {
		raw, err := json.Marshal(data.ExistingConfig)
		if err == nil {
			json.Unmarshal(raw, &cfg)
		}
	}
	if cfg == nil {
		cfg = make(map[string]interface{})
	}

	// Overlay wizard-managed fields
	deepSet(cfg, "gateway.workingDir", data.WorkspacePath)

	deepSet(cfg, "channels.telegram.enabled", data.TelegramEnabled)
	deepSet(cfg, "channels.telegram.botToken", data.TelegramToken)
	deepSet(cfg, "channels.whatsapp.enabled", data.WhatsAppEnabled)
	deepSet(cfg, "channels.http.enabled", data.HTTPEnabled)
	deepSet(cfg, "channels.http.listen", data.HTTPListen)

	storePath, _ := paths.DataPath("sessions.db")
	deepSet(cfg, "session.storePath", storePath)

	deepSet(cfg, "tools.exec.bubblewrap.enabled", data.ExecBubblewrap)
	deepSet(cfg, "tools.browser.bubblewrap.enabled", data.BrowserBubblewrap)

	// LLM provider
	if !data.LLMSkipped && data.LLMProviderID != "" {
		alias := data.LLMProviderID

		deepSet(cfg, "llm.providers."+alias+".driver", data.LLMDriver)
		deepSet(cfg, "llm.providers."+alias+".subtype", data.LLMProviderID)

		if data.LLMAPIKey != "" {
			deepSet(cfg, "llm.providers."+alias+".apiKey", data.LLMAPIKey)
		}
		if data.LLMDriver == "ollama" {
			deepSet(cfg, "llm.providers."+alias+".url", data.LLMBaseURL)
		} else if data.LLMBaseURL != "" {
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
