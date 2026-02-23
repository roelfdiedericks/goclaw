// Package setup - tview-based onboarding wizard
package setup

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/rivo/tview"
	"github.com/roelfdiedericks/goclaw/internal/config"
	"github.com/roelfdiedericks/goclaw/internal/config/forms"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
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
	UserName        string
	UserDisplayName string
	UserRole        string
	UserTelegramID  string

	// Telegram
	TelegramEnabled bool
	TelegramToken   string

	// HTTP
	HTTPEnabled bool
	HTTPListen  string

	// Browser
	BrowserSetup bool

	// Sandboxing
	ExecBubblewrap    bool
	BrowserBubblewrap bool
}

// NewWizardData creates a new WizardData with defaults
func NewWizardData() *WizardData {
	return &WizardData{
		UserRole:    "owner",
		HTTPEnabled: true,
		HTTPListen:  "127.0.0.1:1337",
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
		stepHTTP(data),
		stepBrowser(data),
		stepSandbox(data),
		stepLLMNote(data),
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

			return form
		},
		OnExit: func(w *forms.Wizard) error {
			if data.UserDisplayName == "" {
				return fmt.Errorf("name is required")
			}
			if data.UserName == "" {
				// Generate username from display name
				data.UserName = sanitizeUsername(data.UserDisplayName)
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

// Step: Sandboxing (placeholder)
func stepSandbox(data *WizardData) forms.WizardStep {
	return forms.WizardStep{
		Title: "Sandboxing",
		Content: func(w *forms.Wizard) tview.Primitive {
			// Check if on Linux
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
				data.ExecBubblewrap = checked
			})

			form.AddCheckbox("Enable browser sandboxing", data.BrowserBubblewrap, func(checked bool) {
				data.BrowserBubblewrap = checked
			})

			return formWithHeader(`[cyan]Sandboxing[white] restricts tools to only access files within your workspace,
preventing accidental or malicious access to system files.

[green]Highly recommended for security.[white]`, 5, form)
		},
	}
}

// Step: LLM Note
func stepLLMNote(data *WizardData) forms.WizardStep {
	return forms.WizardStep{
		Title: "LLM Providers",
		Content: func(w *forms.Wizard) tview.Primitive {
			text := `[yellow]Important:[white] LLM providers are not configured in this wizard.

After completing setup, run:

  [cyan]goclaw setup edit[white]

This will open the configuration editor where you can:
  • Add API keys for Anthropic, OpenAI, etc.
  • Configure local models (Ollama, LM Studio)
  • Select agent and embedding models

Press [yellow]Next[white] to continue to the summary.`

			tv := tview.NewTextView().
				SetDynamicColors(true).
				SetText(text)
			tv.SetBorder(false)
			return tv
		},
	}
}

// Step: Review
func stepReview(data *WizardData) forms.WizardStep {
	return forms.WizardStep{
		Title: "Review",
		Content: func(w *forms.Wizard) tview.Primitive {
			summary := fmt.Sprintf(`[cyan]Configuration Summary[white]

Workspace:    %s
User:         %s (%s)
Telegram:     %s
HTTP:         %s
Browser:      %s
Sandboxing:   exec=%v, browser=%v

Press [yellow]Finish[white] to complete setup.

[gray](Note: Configuration will be printed but not saved during testing)[white]`,
				data.WorkspacePath,
				data.UserDisplayName,
				data.UserName,
				boolToEnabled(data.TelegramEnabled),
				formatHTTP(data),
				boolToSetup(data.BrowserSetup),
				data.ExecBubblewrap,
				data.BrowserBubblewrap,
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
	fmt.Println("  1. Run 'goclaw setup edit' to configure LLM providers")
	fmt.Println("  2. Run 'goclaw tui' to start GoClaw")
	if data.BrowserSetup {
		fmt.Println("  3. Run 'goclaw browser setup' to configure browser profiles")
	}
}

// buildConfigFromWizardData creates a config structure from wizard data
func buildConfigFromWizardData(data *WizardData) map[string]interface{} {
	cfg := map[string]interface{}{
		"gateway": map[string]interface{}{
			"workingDir": data.WorkspacePath,
		},
		"telegram": map[string]interface{}{
			"enabled":  data.TelegramEnabled,
			"botToken": data.TelegramToken,
		},
		"http": map[string]interface{}{
			"enabled": data.HTTPEnabled,
			"listen":  data.HTTPListen,
		},
		"session": map[string]interface{}{
			"storePath": func() string { p, _ := paths.DataPath("sessions.db"); return p }(),
		},
		"tools": map[string]interface{}{
			"exec": map[string]interface{}{
				"bubblewrap": map[string]interface{}{
					"enabled": data.ExecBubblewrap,
				},
			},
			"browser": map[string]interface{}{
				"bubblewrap": map[string]interface{}{
					"enabled": data.BrowserBubblewrap,
				},
			},
		},
	}

	return cfg
}
