package setup

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/roelfdiedericks/goclaw/internal/browser"
	"github.com/roelfdiedericks/goclaw/internal/bwrap"
	"github.com/roelfdiedericks/goclaw/internal/config"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// isUserAbort checks if the error is a user abort (Escape pressed)
func isUserAbort(err error) bool {
	return errors.Is(err, huh.ErrUserAborted)
}

// Editor manages the configuration editor
type Editor struct {
	configPath string
	config     map[string]interface{}
	modified   bool
}

// NewEditor creates a new editor for the given config path
func NewEditor(configPath string) *Editor {
	return &Editor{
		configPath: configPath,
	}
}

// Run executes the edit mode
func (e *Editor) Run() error {
	// Load existing config
	data, err := os.ReadFile(e.configPath)
	if err != nil {
		return fmt.Errorf("failed to read config: %w", err)
	}

	if err := json.Unmarshal(data, &e.config); err != nil {
		return fmt.Errorf("failed to parse config: %w", err)
	}

	// Suppress non-error logs during TUI
	prevLevel := suppressLogs()
	defer restoreLogs(prevLevel)

	return e.mainMenu()
}

func (e *Editor) mainMenu() error {
	for {
		fmt.Println()
		fmt.Println("GoClaw Configuration")
		fmt.Printf("Config: %s\n", e.configPath)
		fmt.Println()

		// Build menu with current values
		options := []huh.Option[string]{
			huh.NewOption(fmt.Sprintf("Workspace             [%s]", e.getWorkspace()), "workspace"),
			huh.NewOption(fmt.Sprintf("Users                 [%s]", e.getUsersSummary()), "users"),
			huh.NewOption(fmt.Sprintf("LLM Providers         [%s]", e.getProvidersSummary()), "providers"),
			huh.NewOption(fmt.Sprintf("Agent Model           [%s]", e.getAgentModel()), "agent"),
			huh.NewOption(fmt.Sprintf("Embedding Model       [%s]", e.getEmbeddingModel()), "embedding"),
			huh.NewOption(fmt.Sprintf("Telegram              [%s]", e.getTelegramStatus()), "telegram"),
			huh.NewOption(fmt.Sprintf("HTTP Server           [%s]", e.getHTTPStatus()), "http"),
			huh.NewOption(fmt.Sprintf("Sandboxing            [%s]", e.getSandboxStatus()), "sandbox"),
			huh.NewOption("Browser Profiles", "browser"),
			huh.NewOption("---", "---"),
			huh.NewOption("View Current Config", "view"),
			huh.NewOption("Save and Exit", "save"),
			huh.NewOption("Exit without Saving", "exit"),
		}

		var choice string
		form := newForm(
			huh.NewGroup(
				huh.NewSelect[string]().
					Title("Select option to edit").
					Options(options...).
					Value(&choice),
			),
		)

		if err := form.Run(); err != nil {
			if isUserAbort(err) {
				// Escape pressed - treat as exit
				choice = "exit"
			} else {
				return err
			}
		}

		switch choice {
		case "---":
			continue
		case "workspace":
			if err := e.editWorkspace(); err != nil {
				return err
			}
		case "users":
			if err := e.editUsers(); err != nil {
				return err
			}
		case "providers":
			if err := e.editProviders(); err != nil {
				return err
			}
		case "agent":
			if err := e.editAgentModel(); err != nil {
				return err
			}
		case "embedding":
			if err := e.editEmbeddingModel(); err != nil {
				return err
			}
		case "telegram":
			if err := e.editTelegram(); err != nil {
				return err
			}
		case "http":
			if err := e.editHTTP(); err != nil {
				return err
			}
		case "sandbox":
			if err := e.editSandbox(); err != nil {
				return err
			}
		case "browser":
			if err := e.launchBrowserSetup(); err != nil {
				fmt.Printf("Browser setup failed: %v\n", err)
			}
		case "view":
			e.viewConfig()
		case "save":
			return e.save()
		case "exit":
			if e.modified {
				var confirm bool
				form := newForm(
					huh.NewGroup(
						huh.NewConfirm().
							Title("You have unsaved changes. Exit anyway?").
							Value(&confirm),
					),
				)
				if err := form.Run(); err != nil {
					return err
				}
				if !confirm {
					continue
				}
			}
			return nil
		}
	}
}

func (e *Editor) getWorkspace() string {
	if gateway, ok := e.config["gateway"].(map[string]interface{}); ok {
		if ws, ok := gateway["workingDir"].(string); ok {
			return ws
		}
	}
	return "not set"
}

func (e *Editor) getUsersSummary() string {
	usersPath := GetUsersPath(e.configPath)
	data, err := os.ReadFile(usersPath)
	if err != nil {
		return "none"
	}

	var users map[string]interface{}
	if err := json.Unmarshal(data, &users); err != nil {
		return "error"
	}

	if len(users) == 0 {
		return "none"
	}

	// Find owner
	for name, entry := range users {
		if m, ok := entry.(map[string]interface{}); ok {
			if role, ok := m["role"].(string); ok && role == "owner" {
				return fmt.Sprintf("%d user: %s (owner)", len(users), name)
			}
		}
	}

	return fmt.Sprintf("%d users", len(users))
}

func (e *Editor) getProvidersSummary() string {
	if llm, ok := e.config["llm"].(map[string]interface{}); ok {
		if providers, ok := llm["providers"].(map[string]interface{}); ok {
			var names []string
			for name := range providers {
				names = append(names, name)
			}
			if len(names) > 0 {
				return strings.Join(names, ", ")
			}
		}
	}
	return "none"
}

func (e *Editor) getAgentModel() string {
	if llm, ok := e.config["llm"].(map[string]interface{}); ok {
		if agent, ok := llm["agent"].(map[string]interface{}); ok {
			if models, ok := agent["models"].([]interface{}); ok && len(models) > 0 {
				if m, ok := models[0].(string); ok {
					return m
				}
			}
		}
	}
	return "not set"
}

func (e *Editor) getEmbeddingModel() string {
	if llm, ok := e.config["llm"].(map[string]interface{}); ok {
		if embeddings, ok := llm["embeddings"].(map[string]interface{}); ok {
			if models, ok := embeddings["models"].([]interface{}); ok && len(models) > 0 {
				if m, ok := models[0].(string); ok {
					return m
				}
			}
		}
	}
	return "disabled"
}

func (e *Editor) getTelegramStatus() string {
	if telegram, ok := e.config["telegram"].(map[string]interface{}); ok {
		if enabled, ok := telegram["enabled"].(bool); ok && enabled {
			return "enabled"
		}
	}
	return "disabled"
}

func (e *Editor) getHTTPStatus() string {
	if http, ok := e.config["http"].(map[string]interface{}); ok {
		if listen, ok := http["listen"].(string); ok && listen != "" {
			return listen
		}
	}
	return ":1337"
}

func (e *Editor) getSandboxStatus() string {
	execEnabled := false
	browserEnabled := false

	if tools, ok := e.config["tools"].(map[string]interface{}); ok {
		if exec, ok := tools["exec"].(map[string]interface{}); ok {
			if bw, ok := exec["bubblewrap"].(map[string]interface{}); ok {
				if enabled, ok := bw["enabled"].(bool); ok {
					execEnabled = enabled
				}
			}
		}
		if browser, ok := tools["browser"].(map[string]interface{}); ok {
			if bw, ok := browser["bubblewrap"].(map[string]interface{}); ok {
				if enabled, ok := bw["enabled"].(bool); ok {
					browserEnabled = enabled
				}
			}
		}
	}

	if !bwrap.IsLinux() {
		return "N/A (Linux only)"
	}
	if !bwrap.IsAvailable("") {
		return "bwrap not installed"
	}

	if execEnabled && browserEnabled {
		return "exec + browser"
	} else if execEnabled {
		return "exec"
	} else if browserEnabled {
		return "browser"
	}
	return "disabled"
}

func (e *Editor) editWorkspace() error {
	current := e.getWorkspace()
	newPath := current // Pre-populate with current value

	form := newForm(
		huh.NewGroup(
			huh.NewInput().
				Title("Workspace path").
				Value(&newPath),
		),
	)

	if err := form.Run(); err != nil {
		if isUserAbort(err) {
			return nil // Escape pressed, go back
		}
		return err
	}

	if newPath != current {
		expandedPath := ExpandPath(newPath)

		// Check if workspace needs initialization
		soulPath := expandedPath + "/SOUL.md"
		if _, err := os.Stat(soulPath); os.IsNotExist(err) {
			// Workspace doesn't have template files
			var initWorkspace bool
			initForm := newForm(
				huh.NewGroup(
					huh.NewConfirm().
						Title("Initialize workspace?").
						Description("This path doesn't contain workspace files. Create them?").
						Value(&initWorkspace),
				),
			)

			if err := initForm.Run(); err != nil {
				if isUserAbort(err) {
					return nil // Escape = cancel the whole edit
				}
				return err
			}

			if initWorkspace {
				if err := CreateWorkspace(expandedPath); err != nil {
					fmt.Printf("⚠ Failed to initialize workspace: %s\n", err)
				} else {
					fmt.Printf("✓ Workspace initialized at %s\n", expandedPath)
				}
			}
		}

		if e.config["gateway"] == nil {
			e.config["gateway"] = make(map[string]interface{})
		}
		e.config["gateway"].(map[string]interface{})["workingDir"] = expandedPath
		e.modified = true
	}

	return nil
}

func (e *Editor) editUsers() error {
	return RunUserEditor()
}

func (e *Editor) editProviders() error {
	for {
		// Get current providers
		providers := e.getProvidersMap()

		// Build menu options
		options := []huh.Option[string]{}

		// List existing providers
		for name, cfg := range providers {
			provCfg, ok := cfg.(map[string]interface{})
			if !ok {
				continue
			}
			provType := "unknown"
			if t, ok := provCfg["type"].(string); ok {
				provType = t
			}
			options = append(options, huh.NewOption(
				fmt.Sprintf("  %s [%s]", name, provType),
				"edit:"+name,
			))
		}

		if len(options) > 0 {
			options = append(options, huh.NewOption("---", "---"))
		}

		options = append(options,
			huh.NewOption("+ Add provider", "add"),
			huh.NewOption("Back", "back"),
		)

		var choice string
		form := newForm(
			huh.NewGroup(
				huh.NewSelect[string]().
					Title("LLM Providers").
					Options(options...).
					Value(&choice),
			),
		)

		if err := form.Run(); err != nil {
			if isUserAbort(err) {
				return nil
			}
			return err
		}

		switch {
		case choice == "---":
			continue
		case choice == "back":
			return nil
		case choice == "add":
			if err := e.addProvider(); err != nil {
				return err
			}
		case strings.HasPrefix(choice, "edit:"):
			name := strings.TrimPrefix(choice, "edit:")
			if err := e.editSingleProvider(name); err != nil {
				return err
			}
		}
	}
}

func (e *Editor) getProvidersMap() map[string]interface{} {
	if llm, ok := e.config["llm"].(map[string]interface{}); ok {
		if providers, ok := llm["providers"].(map[string]interface{}); ok {
			return providers
		}
	}
	return make(map[string]interface{})
}

func (e *Editor) ensureProvidersMap() map[string]interface{} {
	if e.config["llm"] == nil {
		e.config["llm"] = make(map[string]interface{})
	}
	llm := e.config["llm"].(map[string]interface{})
	if llm["providers"] == nil {
		llm["providers"] = make(map[string]interface{})
	}
	return llm["providers"].(map[string]interface{})
}

func (e *Editor) addProvider() error {
	// Build options from presets
	options := []huh.Option[string]{}
	for _, preset := range Presets {
		options = append(options, huh.NewOption(
			fmt.Sprintf("%s - %s", preset.Name, preset.Description),
			preset.Key,
		))
	}
	options = append(options,
		huh.NewOption("Other (OpenAI-compatible)", "custom"),
		huh.NewOption("Back", "back"),
	)

	var choice string
	form := newForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Select provider type").
				Options(options...).
				Value(&choice),
		),
	)

	if err := form.Run(); err != nil {
		if isUserAbort(err) {
			return nil
		}
		return err
	}

	if choice == "back" {
		return nil
	}

	if choice == "custom" {
		return e.addCustomProvider()
	}

	// Find preset
	preset := GetPreset(choice)
	if preset == nil {
		return fmt.Errorf("unknown preset: %s", choice)
	}

	return e.addPresetProvider(preset)
}

func (e *Editor) addPresetProvider(preset *ProviderPreset) error {
	providers := e.ensureProvidersMap()

	// Check if already exists
	if _, exists := providers[preset.Key]; exists {
		fmt.Printf("\nProvider '%s' already exists. Edit it instead.\n", preset.Key)
		return nil
	}

	// Build provider config
	provCfg := map[string]interface{}{
		"type": preset.Type,
	}

	// Set base URL for non-standard providers
	if preset.Type == "ollama" {
		provCfg["url"] = preset.BaseURL
		provCfg["timeoutSeconds"] = 120
	} else if preset.BaseURL != "" {
		provCfg["baseUrl"] = preset.BaseURL
	}

	// Ask for API key if not local
	if !preset.IsLocal {
		var apiKey string
		form := newForm(
			huh.NewGroup(
				huh.NewInput().
					Title(fmt.Sprintf("%s API Key", preset.Name)).
					Description("Leave empty to skip for now").
					Value(&apiKey),
			),
		)

		if err := form.Run(); err != nil {
			if isUserAbort(err) {
				return nil
			}
			return err
		}

		if apiKey != "" {
			provCfg["apiKey"] = apiKey
		}
	} else {
		// For local providers, ask for URL
		url := preset.BaseURL
		form := newForm(
			huh.NewGroup(
				huh.NewInput().
					Title(fmt.Sprintf("%s URL", preset.Name)).
					Value(&url),
			),
		)

		if err := form.Run(); err != nil {
			if isUserAbort(err) {
				return nil
			}
			return err
		}

		if preset.Type == "ollama" {
			provCfg["url"] = url
		} else {
			provCfg["baseUrl"] = url
		}
	}

	// Test connection
	apiKey := ""
	if k, ok := provCfg["apiKey"].(string); ok {
		apiKey = k
	}
	url := preset.BaseURL
	if u, ok := provCfg["url"].(string); ok {
		url = u
	} else if u, ok := provCfg["baseUrl"].(string); ok {
		url = u
	}

	testPreset := *preset
	testPreset.BaseURL = url

	fmt.Printf("\nTesting connection to %s...\n", preset.Name)
	models, err := TestProvider(testPreset, apiKey)
	if err != nil {
		fmt.Printf("⚠ Connection failed: %s\n", err)
		fmt.Println("You can still proceed, but this provider may not work.")
	} else {
		fmt.Printf("✓ Connected! Found %d models\n", len(models))
	}

	providers[preset.Key] = provCfg
	e.modified = true
	fmt.Printf("Provider '%s' added.\n", preset.Key)
	return nil
}

func (e *Editor) addCustomProvider() error {
	var name, baseURL, apiKey string

	form := newForm(
		huh.NewGroup(
			huh.NewInput().
				Title("Provider name").
				Description("Internal key (e.g., 'myapi')").
				Value(&name),
			huh.NewInput().
				Title("Base URL").
				Description("OpenAI-compatible API endpoint").
				Value(&baseURL),
			huh.NewInput().
				Title("API Key").
				Description("Leave empty if not required").
				Value(&apiKey),
		),
	)

	if err := form.Run(); err != nil {
		if isUserAbort(err) {
			return nil
		}
		return err
	}

	if name == "" || baseURL == "" {
		fmt.Println("\nName and URL are required.")
		return nil
	}

	// Test connection
	testPreset := ProviderPreset{
		Name:    name,
		Type:    "openai",
		BaseURL: baseURL,
	}

	fmt.Printf("\nTesting connection to %s...\n", name)
	models, err := TestProvider(testPreset, apiKey)
	if err != nil {
		fmt.Printf("⚠ Connection failed: %s\n", err)
		fmt.Println("You can still proceed, but this provider may not work.")
	} else {
		fmt.Printf("✓ Connected! Found %d models\n", len(models))
	}

	providers := e.ensureProvidersMap()
	provCfg := map[string]interface{}{
		"type":    "openai",
		"baseUrl": baseURL,
	}
	if apiKey != "" {
		provCfg["apiKey"] = apiKey
	}

	providers[name] = provCfg
	e.modified = true
	fmt.Printf("Provider '%s' added.\n", name)
	return nil
}

func (e *Editor) editSingleProvider(name string) error {
	providers := e.getProvidersMap()
	provCfg, ok := providers[name].(map[string]interface{})
	if !ok {
		return fmt.Errorf("provider not found: %s", name)
	}

	for {
		// Build options based on provider config
		options := []huh.Option[string]{}

		provType := "unknown"
		if t, ok := provCfg["type"].(string); ok {
			provType = t
		}
		options = append(options, huh.NewOption(fmt.Sprintf("Type: %s", provType), "type"))

		// URL field (different key for ollama vs others)
		if provType == "ollama" {
			url := ""
			if u, ok := provCfg["url"].(string); ok {
				url = u
			}
			options = append(options, huh.NewOption(fmt.Sprintf("URL: %s", url), "url"))
		} else {
			baseURL := ""
			if u, ok := provCfg["baseUrl"].(string); ok {
				baseURL = u
			}
			options = append(options, huh.NewOption(fmt.Sprintf("Base URL: %s", baseURL), "baseUrl"))
		}

		// API key
		apiKey := ""
		if k, ok := provCfg["apiKey"].(string); ok {
			apiKey = k
		}
		if apiKey != "" {
			options = append(options, huh.NewOption(fmt.Sprintf("API Key: %s...%s", apiKey[:4], apiKey[len(apiKey)-4:]), "apiKey"))
		} else {
			options = append(options, huh.NewOption("API Key: (not set)", "apiKey"))
		}

		options = append(options,
			huh.NewOption("---", "---"),
			huh.NewOption("Test connection", "test"),
			huh.NewOption("Delete provider", "delete"),
			huh.NewOption("Back", "back"),
		)

		var choice string
		form := newForm(
			huh.NewGroup(
				huh.NewSelect[string]().
					Title(fmt.Sprintf("Edit: %s", name)).
					Options(options...).
					Value(&choice),
			),
		)

		if err := form.Run(); err != nil {
			if isUserAbort(err) {
				return nil
			}
			return err
		}

		switch choice {
		case "---":
			continue
		case "back":
			return nil
		case "type":
			// Edit type
			newType := provType
			typeForm := newForm(
				huh.NewGroup(
					huh.NewSelect[string]().
						Title("Provider type").
						Options(
							huh.NewOption("anthropic", "anthropic"),
							huh.NewOption("openai (compatible)", "openai"),
							huh.NewOption("ollama", "ollama"),
						).
						Value(&newType),
				),
			)
			if err := typeForm.Run(); err == nil {
				provCfg["type"] = newType
				e.modified = true
			}
		case "url", "baseUrl":
			// Edit URL
			urlKey := choice
			if choice == "url" && provType != "ollama" {
				urlKey = "baseUrl"
			}
			currentURL := ""
			if u, ok := provCfg[urlKey].(string); ok {
				currentURL = u
			}
			newURL := currentURL
			urlForm := newForm(
				huh.NewGroup(
					huh.NewInput().
						Title("URL").
						Value(&newURL),
				),
			)
			if err := urlForm.Run(); err == nil && newURL != currentURL {
				provCfg[urlKey] = newURL
				e.modified = true
			}
		case "apiKey":
			newKey := apiKey
			keyForm := newForm(
				huh.NewGroup(
					huh.NewInput().
						Title("API Key").
						Value(&newKey),
				),
			)
			if err := keyForm.Run(); err == nil && newKey != apiKey {
				if newKey == "" {
					delete(provCfg, "apiKey")
				} else {
					provCfg["apiKey"] = newKey
				}
				e.modified = true
			}
		case "test":
			// Build preset for testing
			url := ""
			if u, ok := provCfg["url"].(string); ok {
				url = u
			} else if u, ok := provCfg["baseUrl"].(string); ok {
				url = u
			}

			testPreset := ProviderPreset{
				Name:    name,
				Type:    provType,
				BaseURL: url,
			}

			fmt.Printf("\nTesting connection to %s...\n", name)
			models, err := TestProvider(testPreset, apiKey)
			if err != nil {
				fmt.Printf("⚠ Connection failed: %s\n", err)
			} else {
				fmt.Printf("✓ Connected! Found %d models\n", len(models))
				if len(models) > 0 && len(models) <= 10 {
					fmt.Println("Available models:")
					for _, m := range models {
						fmt.Printf("  - %s\n", m)
					}
				} else if len(models) > 10 {
					fmt.Println("First 10 models:")
					for _, m := range models[:10] {
						fmt.Printf("  - %s\n", m)
					}
					fmt.Printf("  ... and %d more\n", len(models)-10)
				}
			}
			fmt.Println()
		case "delete":
			var confirm bool
			confirmForm := newForm(
				huh.NewGroup(
					huh.NewConfirm().
						Title(fmt.Sprintf("Delete provider '%s'?", name)).
						Value(&confirm),
				),
			)
			if err := confirmForm.Run(); err == nil && confirm {
				delete(providers, name)
				e.modified = true
				fmt.Printf("\nProvider '%s' deleted.\n", name)
				return nil
			}
		}
	}
}

func (e *Editor) editAgentModel() error {
	current := e.getAgentModel()
	newModel := current // Pre-populate with current value

	form := newForm(
		huh.NewGroup(
			huh.NewInput().
				Title("Agent model").
				Description("Format: provider/model (e.g., anthropic/claude-opus-4-5)").
				Value(&newModel),
		),
	)

	if err := form.Run(); err != nil {
		if isUserAbort(err) {
			return nil // Escape pressed, go back
		}
		return err
	}

	if newModel != current {
		if e.config["llm"] == nil {
			e.config["llm"] = make(map[string]interface{})
		}
		llm := e.config["llm"].(map[string]interface{})
		if llm["agent"] == nil {
			llm["agent"] = make(map[string]interface{})
		}
		llm["agent"].(map[string]interface{})["models"] = []string{newModel}
		e.modified = true
	}

	return nil
}

func (e *Editor) editEmbeddingModel() error {
	current := e.getEmbeddingModel()
	// Pre-populate, but treat "disabled" as empty for editing
	newModel := current
	if newModel == "disabled" {
		newModel = ""
	}

	form := newForm(
		huh.NewGroup(
			huh.NewInput().
				Title("Embedding model").
				Description("Format: provider/model (e.g., ollama/nomic-embed-text). Clear to disable.").
				Value(&newModel),
		),
	)

	if err := form.Run(); err != nil {
		if isUserAbort(err) {
			return nil // Escape pressed, go back
		}
		return err
	}

	// Normalize for comparison
	effectiveCurrent := current
	if effectiveCurrent == "disabled" {
		effectiveCurrent = ""
	}

	if newModel != effectiveCurrent {
		if e.config["llm"] == nil {
			e.config["llm"] = make(map[string]interface{})
		}
		llm := e.config["llm"].(map[string]interface{})
		if llm["embeddings"] == nil {
			llm["embeddings"] = make(map[string]interface{})
		}

		if newModel == "" {
			llm["embeddings"].(map[string]interface{})["models"] = []string{}
		} else {
			llm["embeddings"].(map[string]interface{})["models"] = []string{newModel}
		}
		e.modified = true
	}

	return nil
}

func (e *Editor) editTelegram() error {
	var enabled bool
	currentEnabled := e.getTelegramStatus() == "enabled"

	form := newForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title("Enable Telegram bot?").
				Value(&enabled),
		),
	)

	// Set initial value
	enabled = currentEnabled

	if err := form.Run(); err != nil {
		if isUserAbort(err) {
			return nil // Escape pressed, go back
		}
		return err
	}

	if enabled != currentEnabled {
		if e.config["telegram"] == nil {
			e.config["telegram"] = make(map[string]interface{})
		}
		e.config["telegram"].(map[string]interface{})["enabled"] = enabled
		e.modified = true
	}

	if enabled {
		// Get current token
		var currentToken string
		if telegram, ok := e.config["telegram"].(map[string]interface{}); ok {
			if token, ok := telegram["botToken"].(string); ok {
				currentToken = token
			}
		}

		newToken := currentToken // Pre-populate with current value
		form := newForm(
			huh.NewGroup(
				huh.NewInput().
					Title("Telegram Bot Token").
					Value(&newToken),
			),
		)

		if err := form.Run(); err != nil {
			if isUserAbort(err) {
				return nil // Escape pressed, go back
			}
			return err
		}

		if newToken != "" && newToken != currentToken {
			e.config["telegram"].(map[string]interface{})["botToken"] = newToken
			e.modified = true

			// Test the new token
			username, err := TestTelegramToken(newToken)
			if err != nil {
				fmt.Printf("⚠ Token validation failed: %s\n", err)
			} else {
				fmt.Printf("✓ Bot username: @%s\n", username)
			}
		}
	}

	return nil
}

func (e *Editor) editHTTP() error {
	current := e.getHTTPStatus()
	newListen := current // Pre-populate with current value

	form := newForm(
		huh.NewGroup(
			huh.NewInput().
				Title("HTTP listen address").
				Description("Format: :port or host:port").
				Value(&newListen),
		),
	)

	if err := form.Run(); err != nil {
		if isUserAbort(err) {
			return nil // Escape pressed, go back
		}
		return err
	}

	if newListen != current {
		if e.config["http"] == nil {
			e.config["http"] = make(map[string]interface{})
		}
		e.config["http"].(map[string]interface{})["listen"] = newListen
		e.modified = true
	}

	return nil
}

func (e *Editor) editSandbox() error {
	fmt.Println()

	// Check if bubblewrap is available
	if !bwrap.IsLinux() {
		fmt.Println("Bubblewrap sandboxing is only available on Linux.")
		fmt.Println("Press Enter to continue...")
		fmt.Scanln()
		return nil
	}

	if !bwrap.IsAvailable("") {
		fmt.Println("Bubblewrap (bwrap) is not installed.")
		fmt.Println()
		fmt.Println("Install with:")
		fmt.Println("  Debian/Ubuntu:  sudo apt install bubblewrap")
		fmt.Println("  Fedora/RHEL:    sudo dnf install bubblewrap")
		fmt.Println("  Arch:           sudo pacman -S bubblewrap")
		fmt.Println()
		fmt.Println("Press Enter to continue...")
		fmt.Scanln()
		return nil
	}

	// Get current values
	execEnabled := false
	browserEnabled := false

	if tools, ok := e.config["tools"].(map[string]interface{}); ok {
		if exec, ok := tools["exec"].(map[string]interface{}); ok {
			if bw, ok := exec["bubblewrap"].(map[string]interface{}); ok {
				if enabled, ok := bw["enabled"].(bool); ok {
					execEnabled = enabled
				}
			}
		}
		if browser, ok := tools["browser"].(map[string]interface{}); ok {
			if bw, ok := browser["bubblewrap"].(map[string]interface{}); ok {
				if enabled, ok := bw["enabled"].(bool); ok {
					browserEnabled = enabled
				}
			}
		}
	}

	fmt.Println("═══════════════════════════════════════")
	fmt.Println("       Sandbox Configuration")
	fmt.Println("═══════════════════════════════════════")
	fmt.Println()
	fmt.Println("Bubblewrap provides kernel-level sandboxing that")
	fmt.Println("restricts file access to the workspace directory.")
	fmt.Println()

	form := newForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title("Enable exec tool sandboxing?").
				Description("Restricts shell commands to workspace directory only").
				Value(&execEnabled),
			huh.NewConfirm().
				Title("Enable browser sandboxing?").
				Description("Restricts browser to workspace and profile directories").
				Value(&browserEnabled),
		),
	)

	if err := form.Run(); err != nil {
		if isUserAbort(err) {
			return nil // Escape pressed, go back
		}
		return err
	}

	// Ensure tools section exists
	if e.config["tools"] == nil {
		e.config["tools"] = make(map[string]interface{})
	}
	tools := e.config["tools"].(map[string]interface{})

	// Ensure bubblewrap global section exists
	if tools["bubblewrap"] == nil {
		tools["bubblewrap"] = map[string]interface{}{"path": ""}
	}

	// Ensure exec section exists
	if tools["exec"] == nil {
		tools["exec"] = map[string]interface{}{
			"timeout": 1800,
			"bubblewrap": map[string]interface{}{
				"enabled":      false,
				"extraRoBind":  []string{},
				"extraBind":    []string{},
				"extraEnv":     map[string]string{},
				"allowNetwork": true,
				"clearEnv":     true,
			},
		}
	}
	exec := tools["exec"].(map[string]interface{})
	if exec["bubblewrap"] == nil {
		exec["bubblewrap"] = map[string]interface{}{
			"enabled":      false,
			"extraRoBind":  []string{},
			"extraBind":    []string{},
			"extraEnv":     map[string]string{},
			"allowNetwork": true,
			"clearEnv":     true,
		}
	}
	exec["bubblewrap"].(map[string]interface{})["enabled"] = execEnabled

	// Ensure browser section exists with bubblewrap
	if tools["browser"] == nil {
		tools["browser"] = map[string]interface{}{
			"enabled": true,
			"bubblewrap": map[string]interface{}{
				"enabled":     false,
				"extraRoBind": []string{},
				"extraBind":   []string{},
				"gpu":         true,
			},
		}
	}
	browser := tools["browser"].(map[string]interface{})
	if browser["bubblewrap"] == nil {
		browser["bubblewrap"] = map[string]interface{}{
			"enabled":     false,
			"extraRoBind": []string{},
			"extraBind":   []string{},
			"gpu":         true,
		}
	}
	browser["bubblewrap"].(map[string]interface{})["enabled"] = browserEnabled

	e.modified = true

	if execEnabled || browserEnabled {
		fmt.Println("Sandbox settings updated.")
	} else {
		fmt.Println("Sandboxing disabled.")
	}

	return nil
}

func (e *Editor) viewConfig() {
	pretty, err := json.MarshalIndent(e.config, "", "  ")
	if err != nil {
		fmt.Println("Error formatting config:", err)
		return
	}

	fmt.Println()
	fmt.Println("Current configuration:")
	fmt.Println()
	fmt.Println(string(pretty))
}

func (e *Editor) save() error {
	if !e.modified {
		fmt.Println("\nNo changes to save.")
		return nil
	}

	// Create backup before writing
	if err := BackupFile(e.configPath); err != nil {
		L_warn("setup: backup failed, continuing anyway", "error", err)
	}

	data, err := json.MarshalIndent(e.config, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	if err := os.WriteFile(e.configPath, data, 0600); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}

	L_info("setup: saved configuration", "path", e.configPath)
	fmt.Printf("\n✓ Configuration saved to %s\n", e.configPath)

	return nil
}

// launchBrowserSetup launches a headed browser for profile setup
func (e *Editor) launchBrowserSetup() error {
	fmt.Println()
	fmt.Println("═══════════════════════════════════════")
	fmt.Println("        Browser Profile Setup")
	fmt.Println("═══════════════════════════════════════")
	fmt.Println()

	// Ask which profile to set up
	var profile string
	form := newForm(
		huh.NewGroup(
			huh.NewInput().
				Title("Profile name").
				Description("Enter profile name (default: 'default')").
				Placeholder("default").
				Value(&profile),
		),
	)

	if err := form.Run(); err != nil {
		if isUserAbort(err) {
			return nil
		}
		return err
	}

	if profile == "" {
		profile = "default"
	}

	fmt.Println()
	fmt.Println("A browser window will open.")
	fmt.Println("Log in to any websites you want GoClaw to access,")
	fmt.Println("then close the browser when done.")
	fmt.Println()
	fmt.Println("Press Ctrl+C to cancel.")
	fmt.Println()

	// Load config
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
