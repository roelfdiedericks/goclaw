package setup

import (
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/roelfdiedericks/goclaw/internal/browser"
	"github.com/roelfdiedericks/goclaw/internal/config"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// Wizard manages the setup wizard state
type Wizard struct {
	// Collected configuration
	workspacePath     string
	openclawImport    bool
	openclawConfig    map[string]interface{}
	selectedProviders []string
	providerConfigs   map[string]ProviderConfig
	importedAPIKeys   map[string]string // provider -> API key from OpenClaw
	agentModel        string
	embeddingModel    string
	skipEmbeddings    bool

	// User setup
	userName       string
	userDisplayName string
	userRole       string
	userTelegramID string

	// Telegram
	telegramEnabled bool
	telegramToken   string

	// HTTP
	httpPort    int
	httpAuthEnabled bool

	// Browser
	browserSetup bool
}

// ProviderConfig holds configuration for a single provider
type ProviderConfig struct {
	Type    string
	APIKey  string
	BaseURL string
}

// NewWizard creates a new wizard instance
func NewWizard() *Wizard {
	return &Wizard{
		providerConfigs: make(map[string]ProviderConfig),
		importedAPIKeys: make(map[string]string),
		httpPort:        1337,
		httpAuthEnabled: true,
		userRole:        "owner",
	}
}

// Run executes the full wizard
func (w *Wizard) Run() error {
	// Step 1: Welcome
	if err := w.showWelcome(); err != nil {
		return err
	}

	// Step 2: OpenClaw detection
	if err := w.handleOpenClawDetection(); err != nil {
		return err
	}

	// Step 2b: Workspace setup (if not importing)
	if !w.openclawImport {
		if err := w.setupWorkspace(); err != nil {
			return err
		}
	}

	// Step 3: LLM Providers
	if err := w.selectProviders(); err != nil {
		return err
	}

	// Step 4: Model Selection
	if err := w.selectModels(); err != nil {
		return err
	}

	// Step 5: User Setup
	if err := w.setupUser(); err != nil {
		return err
	}

	// Step 6: Telegram
	if err := w.setupTelegram(); err != nil {
		return err
	}

	// Step 7: HTTP Server
	if err := w.setupHTTP(); err != nil {
		return err
	}

	// Step 8: Browser Setup (optional)
	if err := w.offerBrowserSetup(); err != nil {
		return err
	}

	// Step 9: Review and Save
	return w.reviewAndSave()
}

func (w *Wizard) showWelcome() error {
	fmt.Println()
	fmt.Println("╔════════════════════════════════════════╗")
	fmt.Println("║        Welcome to GoClaw Setup         ║")
	fmt.Println("╚════════════════════════════════════════╝")
	fmt.Println()
	fmt.Println("This wizard will help you configure GoClaw.")
	fmt.Println("We'll set up:")
	fmt.Println("  • LLM providers (Anthropic, OpenAI, local models)")
	fmt.Println("  • Agent workspace")
	fmt.Println("  • User authentication")
	fmt.Println("  • Telegram bot (optional)")
	fmt.Println("  • HTTP server")
	fmt.Println()

	var proceed bool
	form := newForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title("Ready to begin?").
				Value(&proceed),
		),
	)

	if err := form.Run(); err != nil {
		return err
	}

	if !proceed {
		fmt.Println("Setup cancelled.")
		os.Exit(0)
	}

	return nil
}

func (w *Wizard) handleOpenClawDetection() error {
	if !OpenClawExists() {
		return nil
	}

	fmt.Println()
	fmt.Println("Detected existing OpenClaw installation at ~/.openclaw/")
	fmt.Println()

	var importConfig bool
	form := newForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title("Import settings from OpenClaw?").
				Description("We can import API keys, workspace path, and Telegram config").
				Value(&importConfig),
		),
	)

	if err := form.Run(); err != nil {
		return err
	}

	if !importConfig {
		return nil
	}

	// Load OpenClaw config
	data, err := os.ReadFile(OpenClawConfigPath())
	if err != nil {
		L_warn("setup: failed to read OpenClaw config", "error", err)
		fmt.Println("Warning: Could not read OpenClaw config. Proceeding with fresh setup.")
		return nil
	}

	if err := json.Unmarshal(data, &w.openclawConfig); err != nil {
		L_warn("setup: failed to parse OpenClaw config", "error", err)
		fmt.Println("Warning: Could not parse OpenClaw config. Proceeding with fresh setup.")
		return nil
	}

	w.openclawImport = true

	// Extract settings from OpenClaw config
	w.extractOpenClawSettings()

	fmt.Println()
	fmt.Println("✓ Imported settings from OpenClaw")
	if w.workspacePath != "" {
		fmt.Printf("  Workspace: %s\n", w.workspacePath)
	}
	if w.telegramToken != "" {
		fmt.Println("  Telegram: configured")
		w.telegramEnabled = true
	}
	if w.userTelegramID != "" {
		fmt.Printf("  Telegram user ID: %s\n", w.userTelegramID)
	}
	if len(w.importedAPIKeys) > 0 {
		for provider := range w.importedAPIKeys {
			fmt.Printf("  %s API key: found\n", provider)
		}
	}
	fmt.Println()

	return nil
}

func (w *Wizard) extractOpenClawSettings() {
	// Extract workspace path
	if agents, ok := w.openclawConfig["agents"].(map[string]interface{}); ok {
		if defaults, ok := agents["defaults"].(map[string]interface{}); ok {
			if ws, ok := defaults["workspace"].(string); ok {
				w.workspacePath = ws
			}
		}
	}

	// Extract Telegram settings
	if channels, ok := w.openclawConfig["channels"].(map[string]interface{}); ok {
		if telegram, ok := channels["telegram"].(map[string]interface{}); ok {
			if token, ok := telegram["botToken"].(string); ok {
				w.telegramToken = token
			}
			// Extract first telegram ID from allowFrom for user pre-fill
			if allowFrom, ok := telegram["allowFrom"].([]interface{}); ok && len(allowFrom) > 0 {
				if id, ok := allowFrom[0].(string); ok {
					w.userTelegramID = id
				}
			}
		}
	}

	// Extract API keys from auth-profiles.json
	home, _ := os.UserHomeDir()
	authProfilesPath := filepath.Join(home, ".openclaw", "agents", "main", "agent", "auth-profiles.json")
	if data, err := os.ReadFile(authProfilesPath); err == nil {
		var authProfiles map[string]interface{}
		if err := json.Unmarshal(data, &authProfiles); err == nil {
			if profiles, ok := authProfiles["profiles"].(map[string]interface{}); ok {
				// Anthropic key
				if anthropic, ok := profiles["anthropic:default"].(map[string]interface{}); ok {
					if key, ok := anthropic["key"].(string); ok {
						w.importedAPIKeys["anthropic"] = key
						L_debug("setup: imported Anthropic API key from auth-profiles.json")
					}
				}
				// OpenAI key
				if openai, ok := profiles["openai:default"].(map[string]interface{}); ok {
					if key, ok := openai["key"].(string); ok {
						w.importedAPIKeys["openai"] = key
						L_debug("setup: imported OpenAI API key from auth-profiles.json")
					}
				}
			}
		}
	}
}

func (w *Wizard) setupWorkspace() error {
	fmt.Println()

	// If workspace already set from OpenClaw import, just confirm
	if w.workspacePath != "" && w.openclawImport {
		fmt.Printf("Using workspace from OpenClaw: %s\n", w.workspacePath)

		// Create the workspace if needed
		if err := CreateWorkspace(w.workspacePath); err != nil {
			return fmt.Errorf("failed to create workspace: %w", err)
		}
		fmt.Printf("✓ Workspace ready at %s\n", w.workspacePath)
		return nil
	}

	// Check if OpenClaw exists (even if user didn't import)
	if OpenClawExists() {
		openclawWorkspace := GetOpenClawWorkspace()
		defaultPath := DefaultWorkspacePath()

		for {
			var choice string
			form := newForm(
				huh.NewGroup(
					huh.NewSelect[string]().
						Title("OpenClaw installation detected").
						Description("Would you like to share the workspace with OpenClaw?").
						Options(
							huh.NewOption(fmt.Sprintf("Share with OpenClaw (%s)", openclawWorkspace), "share"),
							huh.NewOption(fmt.Sprintf("Create new workspace (%s)", defaultPath), "new"),
							huh.NewOption("Custom path", "custom"),
						).
						Value(&choice),
				),
			)

			if err := form.Run(); err != nil {
				return err // Escape at top-level selection = abort
			}

			switch choice {
			case "share":
				w.workspacePath = openclawWorkspace
			case "new":
				w.workspacePath = defaultPath
			case "custom":
				w.workspacePath = defaultPath // Pre-fill with default
				customForm := newForm(
					huh.NewGroup(
						huh.NewInput().
							Title("Workspace path").
							Description("Enter your custom workspace path").
							Value(&w.workspacePath),
					),
				)
				if err := customForm.Run(); err != nil {
					if isUserAbort(err) {
						continue // Go back to workspace selection
					}
					return err
				}
			}
			break // Selection made, exit loop
		}
	} else {
		// No OpenClaw - just ask for path with default pre-filled
		w.workspacePath = DefaultWorkspacePath()

		form := newForm(
			huh.NewGroup(
				huh.NewInput().
					Title("Where should GoClaw store your workspace?").
					Description("This is where your agent's files will live").
					Value(&w.workspacePath),
			),
		)

		if err := form.Run(); err != nil {
			return err
		}
	}

	if w.workspacePath == "" {
		w.workspacePath = DefaultWorkspacePath()
	}

	w.workspacePath = ExpandPath(w.workspacePath)

	// Create the workspace
	if err := CreateWorkspace(w.workspacePath); err != nil {
		return fmt.Errorf("failed to create workspace: %w", err)
	}

	fmt.Printf("\n✓ Created workspace at %s\n", w.workspacePath)
	return nil
}

func (w *Wizard) selectProviders() error {
	fmt.Println()

	configured := make(map[string]bool)
	first := true

	for {
		// Build options from presets (excluding already configured)
		var options []huh.Option[string]
		for _, p := range Presets {
			if configured[p.Key] {
				continue
			}
			label := fmt.Sprintf("%s - %s", p.Name, p.Description)
			options = append(options, huh.NewOption(label, p.Key))
		}
		if !configured["custom"] {
			options = append(options, huh.NewOption("Other OpenAI-compatible - Custom endpoint", "custom"))
		}

		// Add "Done" option after first provider is configured
		if !first {
			options = append(options, huh.NewOption("Done - no more providers", "done"))
		}

		// Determine title
		title := "Which LLM provider would you like to configure?"
		if !first {
			title = "Setup another provider?"
		}

		var choice string
		form := newForm(
			huh.NewGroup(
				huh.NewSelect[string]().
					Title(title).
					Options(options...).
					Value(&choice),
			),
		)

		if err := form.Run(); err != nil {
			if isUserAbort(err) {
				if first {
					return fmt.Errorf("at least one provider must be configured")
				}
				return nil // Escape after first provider = done
			}
			return err
		}

		if choice == "done" {
			return nil
		}

		// Configure the selected provider
		if err := w.configureProvider(choice); err != nil {
			if isUserAbort(err) {
				continue // User cancelled this provider, let them pick again
			}
			return err
		}

		configured[choice] = true
		w.selectedProviders = append(w.selectedProviders, choice)
		first = false
	}
}

func (w *Wizard) configureProvider(key string) error {
	preset := GetPreset(key)

	var name, baseURL, apiKey string

	if key == "custom" {
		// Custom provider needs name and URL
		form := newForm(
			huh.NewGroup(
				huh.NewInput().
					Title("Provider name").
					Description("A short name for this provider (e.g., 'local-llm')").
					Value(&name),
				huh.NewInput().
					Title("Base URL").
					Description("The OpenAI-compatible API endpoint").
					Placeholder("http://localhost:8080/v1").
					Value(&baseURL),
				huh.NewInput().
					Title("API Key (optional)").
					Description("Leave empty if not required").
					Value(&apiKey),
			),
		)

		if err := form.Run(); err != nil {
			return err // Escape = go back to provider selection (handled by caller)
		}

		w.providerConfigs[name] = ProviderConfig{
			Type:    "openai",
			APIKey:  apiKey,
			BaseURL: baseURL,
		}
		return nil
	}

	// For preset providers
	if preset.IsLocal {
		// Local providers - just need URL confirmation
		baseURL = preset.BaseURL

		form := newForm(
			huh.NewGroup(
				huh.NewInput().
					Title(fmt.Sprintf("%s URL", preset.Name)).
					Description("Press enter to use default, or change if needed").
					Value(&baseURL).
					Placeholder(preset.BaseURL),
			),
		)

		if err := form.Run(); err != nil {
			return err // Escape = go back to provider selection (handled by caller)
		}

		if baseURL == "" {
			baseURL = preset.BaseURL
		}

		// Test connection
		fmt.Printf("Testing connection to %s...\n", preset.Name)
		models, err := TestProvider(*preset, "")
		if err != nil {
			fmt.Printf("⚠ Connection failed: %s\n", err)
			fmt.Println("You can still proceed, but this provider may not work.")
		} else {
			fmt.Printf("✓ Connected! Found %d models\n", len(models))
		}

		w.providerConfigs[key] = ProviderConfig{
			Type:    preset.Type,
			BaseURL: baseURL,
		}
	} else {
		// Cloud providers - need API key
		// Pre-populate from OpenClaw import if available
		if imported, ok := w.importedAPIKeys[key]; ok {
			apiKey = imported
		}

		description := "Enter your API key"
		if apiKey != "" {
			description = "Pre-filled from OpenClaw import"
		}

		form := newForm(
			huh.NewGroup(
				huh.NewInput().
					Title(fmt.Sprintf("%s API Key", preset.Name)).
					Description(description).
					Value(&apiKey),
			),
		)

		if err := form.Run(); err != nil {
			return err // Escape = go back to provider selection (handled by caller)
		}

		if apiKey == "" {
			fmt.Printf("⚠ No API key provided for %s. Skipping.\n", preset.Name)
			return nil
		}

		// Test API key
		fmt.Printf("Testing %s API key...\n", preset.Name)
		testPreset := *preset
		models, err := TestProvider(testPreset, apiKey)
		if err != nil {
			fmt.Printf("⚠ Validation failed: %s\n", err)
			fmt.Println("You can still proceed, but check your API key.")
		} else {
			fmt.Printf("✓ Valid! Found %d models\n", len(models))
		}

		w.providerConfigs[key] = ProviderConfig{
			Type:    preset.Type,
			APIKey:  apiKey,
			BaseURL: preset.BaseURL,
		}
	}

	return nil
}

func (w *Wizard) selectModels() error {
	fmt.Println()

	// Collect available models from configured providers
	var agentOptions []huh.Option[string]
	var embedOptions []huh.Option[string]

	for key := range w.providerConfigs {
		preset := GetPreset(key)
		if preset == nil {
			// Custom provider
			agentOptions = append(agentOptions, huh.NewOption(fmt.Sprintf("%s (enter model manually)", key), key+"/"))
			embedOptions = append(embedOptions, huh.NewOption(fmt.Sprintf("%s (enter model manually)", key), key+"/"))
			continue
		}

		// Add known chat models
		for _, model := range preset.KnownChatModels {
			label := fmt.Sprintf("%s/%s", key, model)
			agentOptions = append(agentOptions, huh.NewOption(label, label))
		}

		// Add known embedding models
		if preset.SupportsEmbeddings {
			for _, model := range preset.KnownEmbedModels {
				parts := strings.Split(model, "|")
				modelName := parts[0]
				dims := ""
				if len(parts) > 1 {
					dims = fmt.Sprintf(" (%s dims)", parts[1])
				}
				label := fmt.Sprintf("%s/%s%s", key, modelName, dims)
				value := fmt.Sprintf("%s/%s", key, modelName)
				embedOptions = append(embedOptions, huh.NewOption(label, value))
			}
		}
	}

	// Add manual entry option
	agentOptions = append(agentOptions, huh.NewOption("Enter manually...", "manual"))
	embedOptions = append(embedOptions, huh.NewOption("Enter manually...", "manual"))
	embedOptions = append(embedOptions, huh.NewOption("Skip embeddings (not recommended)", "skip"))

	// Select agent model (with loop for manual entry escape handling)
agentLoop:
	for {
		form := newForm(
			huh.NewGroup(
				huh.NewSelect[string]().
					Title("Select primary agent model").
					Description("This model will be used for the main agent").
					Options(agentOptions...).
					Value(&w.agentModel),
			),
		)

		if err := form.Run(); err != nil {
			return err // Escape at selection = abort
		}

		if w.agentModel == "manual" {
			manualForm := newForm(
				huh.NewGroup(
					huh.NewInput().
						Title("Enter agent model").
						Description("Format: provider/model (e.g., anthropic/claude-opus-4-5)").
						Value(&w.agentModel),
				),
			)
			if err := manualForm.Run(); err != nil {
				if isUserAbort(err) {
					w.agentModel = "" // Reset and go back to selection
					continue agentLoop
				}
				return err
			}
		}
		break agentLoop
	}

	// Select embedding model (with loop for manual entry escape handling)
embedLoop:
	for {
		form := newForm(
			huh.NewGroup(
				huh.NewSelect[string]().
					Title("Select embedding model").
					Description("Used for memory search and transcript search").
					Options(embedOptions...).
					Value(&w.embeddingModel),
			),
		)

		if err := form.Run(); err != nil {
			return err // Escape at selection = abort
		}

		if w.embeddingModel == "skip" {
			// Show warning
			fmt.Println()
			fmt.Println("⚠️  Without embeddings, the following features will be disabled:")
			fmt.Println("   - Memory search (semantic search over workspace files)")
			fmt.Println("   - Transcript search (search past conversations)")
			fmt.Println()

			var confirmSkip bool
			confirmForm := newForm(
				huh.NewGroup(
					huh.NewConfirm().
						Title("Continue without embeddings?").
						Value(&confirmSkip),
				),
			)

			if err := confirmForm.Run(); err != nil {
				if isUserAbort(err) {
					w.embeddingModel = "" // Reset and go back to selection
					continue embedLoop
				}
				return err
			}

			if !confirmSkip {
				w.embeddingModel = "" // Go back to embedding selection
				continue embedLoop
			}

			w.skipEmbeddings = true
			w.embeddingModel = ""
		} else if w.embeddingModel == "manual" {
			manualForm := newForm(
				huh.NewGroup(
					huh.NewInput().
						Title("Enter embedding model").
						Description("Format: provider/model (e.g., ollama/nomic-embed-text)").
						Value(&w.embeddingModel),
				),
			)
			if err := manualForm.Run(); err != nil {
				if isUserAbort(err) {
					w.embeddingModel = "" // Reset and go back to selection
					continue embedLoop
				}
				return err
			}
		}
		break embedLoop
	}

	return nil
}

func (w *Wizard) setupUser() error {
	fmt.Println()

	// Pre-fill telegram ID if imported
	telegramPlaceholder := "Optional - for Telegram authentication"
	if w.userTelegramID != "" {
		telegramPlaceholder = w.userTelegramID
	}

	form := newForm(
		huh.NewGroup(
			huh.NewInput().
				Title("What's your name?").
				Description("This will be used in the user profile").
				Value(&w.userDisplayName).
				Placeholder("Your name"),
			huh.NewInput().
				Title("Username").
				Description("Lowercase, used for login (e.g., 'rodent')").
				Value(&w.userName).
				Placeholder("username"),
			huh.NewInput().
				Title("Telegram user ID").
				Description(telegramPlaceholder).
				Value(&w.userTelegramID),
		),
	)

	if err := form.Run(); err != nil {
		return err
	}

	// Use telegram ID from placeholder if not entered
	if w.userTelegramID == "" && telegramPlaceholder != "Optional - for Telegram authentication" {
		w.userTelegramID = telegramPlaceholder
	}

	return nil
}

func (w *Wizard) setupTelegram() error {
	fmt.Println()

	// Skip if already configured from OpenClaw import
	if w.telegramEnabled && w.telegramToken != "" {
		fmt.Println("✓ Telegram already configured from OpenClaw import")

		var testToken bool
		form := newForm(
			huh.NewGroup(
				huh.NewConfirm().
					Title("Test Telegram token?").
					Value(&testToken),
			),
		)

		if err := form.Run(); err != nil {
			return err // Escape = skip test
		}

		if testToken {
			username, err := TestTelegramToken(w.telegramToken)
			if err != nil {
				fmt.Printf("⚠ Token validation failed: %s\n", err)
			} else {
				fmt.Printf("✓ Bot username: @%s\n", username)
			}
		}

		return nil
	}

	// Loop for escape handling in token input
	for {
		form := newForm(
			huh.NewGroup(
				huh.NewConfirm().
					Title("Enable Telegram bot?").
					Description("Connect GoClaw to Telegram for mobile access").
					Value(&w.telegramEnabled),
			),
		)

		if err := form.Run(); err != nil {
			return err // Escape at enable question = abort
		}

		if !w.telegramEnabled {
			return nil
		}

		tokenForm := newForm(
			huh.NewGroup(
				huh.NewInput().
					Title("Telegram Bot Token").
					Description("Get this from @BotFather on Telegram").
					Value(&w.telegramToken),
			),
		)

		if err := tokenForm.Run(); err != nil {
			if isUserAbort(err) {
				w.telegramEnabled = false // Reset and go back to enable question
				continue
			}
			return err
		}

		if w.telegramToken != "" {
			fmt.Println("Testing Telegram token...")
			username, err := TestTelegramToken(w.telegramToken)
			if err != nil {
				fmt.Printf("⚠ Token validation failed: %s\n", err)
			} else {
				fmt.Printf("✓ Bot username: @%s\n", username)
			}
		}

		return nil
	}
}

func (w *Wizard) setupHTTP() error {
	fmt.Println()

	form := newForm(
		huh.NewGroup(
			huh.NewInput().
				Title("HTTP server port").
				Description("Port for the web interface").
				Placeholder("1337").
				Validate(func(s string) error {
					if s == "" {
						return nil
					}
					var port int
					_, err := fmt.Sscanf(s, "%d", &port)
					if err != nil || port < 1 || port > 65535 {
						return fmt.Errorf("invalid port number")
					}
					return nil
				}).
				Value(new(string)),
			huh.NewConfirm().
				Title("Enable HTTP authentication?").
				Description("Recommended if the server is accessible from the network").
				Value(&w.httpAuthEnabled),
		),
	)

	if err := form.Run(); err != nil {
		return err
	}

	fmt.Printf("✓ HTTP server will be available at http://localhost:%d\n", w.httpPort)
	return nil
}

func (w *Wizard) offerBrowserSetup() error {
	fmt.Println()

	form := newForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title("Set up browser for authenticated web access?").
				Description("Opens a browser to save login sessions for web tools").
				Value(&w.browserSetup),
		),
	)

	if err := form.Run(); err != nil {
		return err
	}

	if w.browserSetup {
		fmt.Println()
		fmt.Println("Browser setup will launch after configuration is saved.")
		fmt.Println("You can also run it later with: goclaw browser setup")
	}

	return nil
}

func (w *Wizard) reviewAndSave() error {
	fmt.Println()
	fmt.Println("═══════════════════════════════════════")
	fmt.Println("           Configuration Summary")
	fmt.Println("═══════════════════════════════════════")
	fmt.Println()

	fmt.Printf("Workspace:     %s\n", w.workspacePath)
	fmt.Printf("User:          %s (%s)\n", w.userDisplayName, w.userName)
	fmt.Printf("Agent model:   %s\n", w.agentModel)
	if w.skipEmbeddings {
		fmt.Println("Embeddings:    disabled")
	} else {
		fmt.Printf("Embeddings:    %s\n", w.embeddingModel)
	}
	fmt.Printf("Telegram:      %v\n", w.telegramEnabled)
	fmt.Printf("HTTP port:     %d (auth: %v)\n", w.httpPort, w.httpAuthEnabled)
	fmt.Printf("Providers:     %s\n", strings.Join(w.selectedProviders, ", "))
	fmt.Println()

	var action string
	form := newForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("What would you like to do?").
				Options(
					huh.NewOption("Save and start using GoClaw", "save"),
					huh.NewOption("Make additional changes (advanced)", "edit"),
					huh.NewOption("Cancel without saving", "cancel"),
				).
				Value(&action),
		),
	)

	if err := form.Run(); err != nil {
		return err
	}

	switch action {
	case "cancel":
		fmt.Println("Setup cancelled. No changes were saved.")
		return nil
	case "edit":
		// Save first, then enter edit mode
		if err := w.saveConfig(); err != nil {
			return err
		}
		return RunEdit()
	case "save":
		return w.saveConfig()
	}

	return nil
}

func (w *Wizard) saveConfig() error {
	configPath := GetConfigPath(w.openclawImport)

	// Ensure directory exists
	if err := EnsureConfigDir(configPath); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	// Create backup before writing (if file exists)
	if err := BackupFile(configPath); err != nil {
		L_warn("setup: backup failed, continuing anyway", "error", err)
	}

	// Build configuration
	cfg := w.buildConfig()

	// Write config
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	if err := os.WriteFile(configPath, data, 0600); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}

	L_info("setup: saved configuration", "path", configPath)

	// Save users.json
	if err := w.saveUsers(configPath); err != nil {
		L_warn("setup: failed to save users", "error", err)
	}

	fmt.Println()
	fmt.Println("═══════════════════════════════════════")
	fmt.Println("        Setup complete!")
	fmt.Println("═══════════════════════════════════════")
	fmt.Println()
	fmt.Println("To start GoClaw:")
	fmt.Println()
	fmt.Println("  goclaw tui            Interactive mode with TUI (recommended)")
	fmt.Println("  goclaw gateway        Foreground mode (logs to terminal)")
	fmt.Println("  goclaw start          Daemon mode (runs in background)")
	fmt.Println("  goclaw stop           Stop the background daemon")
	fmt.Println()
	fmt.Println("Other useful commands:")
	fmt.Println()
	fmt.Println("  goclaw setup edit           Edit your configuration")
	fmt.Println("  goclaw browser setup        Set up browser profiles")
	fmt.Println("  goclaw config               View current configuration")
	fmt.Println()
	fmt.Printf("Configuration saved to: %s\n", configPath)
	fmt.Println()

	// Launch browser setup if requested
	if w.browserSetup {
		if err := w.launchBrowserSetup(configPath); err != nil {
			L_warn("setup: browser setup failed", "error", err)
			fmt.Printf("Browser setup failed: %v\n", err)
			fmt.Println("You can run it later with: goclaw browser setup")
		}
	}

	return nil
}

// launchBrowserSetup launches a headed browser for profile setup
func (w *Wizard) launchBrowserSetup(configPath string) error {
	fmt.Println()
	fmt.Println("═══════════════════════════════════════")
	fmt.Println("        Browser Profile Setup")
	fmt.Println("═══════════════════════════════════════")
	fmt.Println()
	fmt.Println("A browser window will open.")
	fmt.Println("Log in to any websites you want GoClaw to access,")
	fmt.Println("then close the browser when done.")
	fmt.Println()
	fmt.Println("Press Ctrl+C to skip browser setup.")
	fmt.Println()

	// Load the config we just saved
	loadResult, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	cfg := loadResult.Config

	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %w", err)
	}

	// Create browser config with headed mode
	browserCfg := browser.ToolsConfigAdapter{
		Dir:            cfg.Tools.Browser.Dir,
		AutoDownload:   cfg.Tools.Browser.AutoDownload,
		Revision:       cfg.Tools.Browser.Revision,
		Headless:       false, // Headed for setup
		NoSandbox:      cfg.Tools.Browser.NoSandbox,
		DefaultProfile: cfg.Tools.Browser.DefaultProfile,
		Timeout:        cfg.Tools.Browser.Timeout,
		Stealth:        cfg.Tools.Browser.Stealth,
		Device:         cfg.Tools.Browser.Device,
		ProfileDomains: cfg.Tools.Browser.ProfileDomains,
	}.ToConfig()

	// Initialize manager
	mgr, err := browser.InitManager(browserCfg)
	if err != nil {
		return fmt.Errorf("failed to initialize browser manager: %w", err)
	}

	// Ensure browser is downloaded
	if _, err := mgr.EnsureBrowser(); err != nil {
		return fmt.Errorf("failed to ensure browser: %w", err)
	}

	profile := "default"
	profileDir := browserCfg.ResolveProfileDir(home, profile)
	fmt.Printf("Profile: %s\n", profile)
	fmt.Printf("Location: %s\n", profileDir)
	fmt.Println()

	// Launch headed browser
	browserInstance, _, err := mgr.LaunchHeaded(profile, "")
	if err != nil {
		return fmt.Errorf("failed to launch browser: %w", err)
	}

	// Wait for browser window to close or Ctrl+C
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigChan)

	doneChan := make(chan struct{})
	go func() {
		// Poll for all pages closed
		time.Sleep(2 * time.Second)

		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				pages, err := browserInstance.Pages()
				if err != nil || len(pages) == 0 {
					close(doneChan)
					return
				}
			}
		}
	}()

	select {
	case <-sigChan:
		fmt.Println("\nBrowser setup cancelled.")
	case <-doneChan:
		fmt.Println("Browser closed.")
	}

	// Clean up
	if err := browserInstance.Close(); err != nil {
		L_debug("setup: failed to close browser", "error", err)
	}

	fmt.Println("Browser setup complete!")
	fmt.Println()
	return nil
}

func (w *Wizard) buildConfig() map[string]interface{} {
	// Build providers section
	providers := make(map[string]interface{})
	for key, cfg := range w.providerConfigs {
		p := map[string]interface{}{
			"type": cfg.Type,
		}
		if cfg.APIKey != "" {
			p["apiKey"] = cfg.APIKey
		}
		if cfg.BaseURL != "" {
			if cfg.Type == "ollama" {
				p["url"] = cfg.BaseURL
			} else {
				p["baseUrl"] = cfg.BaseURL
			}
		}
		providers[key] = p
	}

	// Build agent model array
	agentModels := []string{w.agentModel}

	// Build embedding model array
	var embeddingModels []string
	if !w.skipEmbeddings && w.embeddingModel != "" {
		embeddingModels = []string{w.embeddingModel}
	}

	config := map[string]interface{}{
		"gateway": map[string]interface{}{
			"workingDir": w.workspacePath,
		},
		"llm": map[string]interface{}{
			"providers": providers,
			"agent": map[string]interface{}{
				"models":    agentModels,
				"maxTokens": 8192,
			},
			"embeddings": map[string]interface{}{
				"models": embeddingModels,
			},
		},
		"telegram": map[string]interface{}{
			"enabled":  w.telegramEnabled,
			"botToken": w.telegramToken,
		},
		"http": map[string]interface{}{
			"listen": fmt.Sprintf(":%d", w.httpPort),
		},
	}

	return config
}

func (w *Wizard) saveUsers(configPath string) error {
	if w.userName == "" {
		return nil
	}

	usersPath := GetUsersPath(configPath)

	users := map[string]interface{}{
		w.userName: map[string]interface{}{
			"name": w.userDisplayName,
			"role": w.userRole,
		},
	}

	if w.userTelegramID != "" {
		users[w.userName].(map[string]interface{})["telegram_id"] = w.userTelegramID
	}

	data, err := json.MarshalIndent(users, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(usersPath, data, 0600)
}
