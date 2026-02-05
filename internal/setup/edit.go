package setup

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/huh"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

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
			huh.NewOption("Browser Profiles", "browser"),
			huh.NewOption("---", "---"),
			huh.NewOption("View Current Config", "view"),
			huh.NewOption("Save and Exit", "save"),
			huh.NewOption("Exit without Saving", "exit"),
		}

		var choice string
		form := huh.NewForm(
			huh.NewGroup(
				huh.NewSelect[string]().
					Title("Select option to edit").
					Options(options...).
					Value(&choice),
			),
		)

		if err := form.Run(); err != nil {
			return err
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
		case "browser":
			fmt.Println("\nRun 'goclaw browser setup' to manage browser profiles.")
		case "view":
			e.viewConfig()
		case "save":
			return e.save()
		case "exit":
			if e.modified {
				var confirm bool
				form := huh.NewForm(
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

func (e *Editor) editWorkspace() error {
	current := e.getWorkspace()

	var newPath string
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("Workspace path").
				Value(&newPath).
				Placeholder(current),
		),
	)

	if err := form.Run(); err != nil {
		return err
	}

	if newPath != "" && newPath != current {
		if e.config["gateway"] == nil {
			e.config["gateway"] = make(map[string]interface{})
		}
		e.config["gateway"].(map[string]interface{})["workingDir"] = ExpandPath(newPath)
		e.modified = true
	}

	return nil
}

func (e *Editor) editUsers() error {
	fmt.Println("\nUser management is not yet implemented in edit mode.")
	fmt.Println("Edit users.json directly or run 'goclaw setup' to reconfigure.")
	return nil
}

func (e *Editor) editProviders() error {
	fmt.Println("\nProvider management is not yet implemented in edit mode.")
	fmt.Println("Edit goclaw.json directly or run 'goclaw setup wizard' to reconfigure.")
	return nil
}

func (e *Editor) editAgentModel() error {
	current := e.getAgentModel()

	var newModel string
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("Agent model").
				Description("Format: provider/model (e.g., anthropic/claude-opus-4-5)").
				Value(&newModel).
				Placeholder(current),
		),
	)

	if err := form.Run(); err != nil {
		return err
	}

	if newModel != "" && newModel != current {
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

	var newModel string
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("Embedding model").
				Description("Format: provider/model (e.g., ollama/nomic-embed-text). Leave empty to disable.").
				Value(&newModel).
				Placeholder(current),
		),
	)

	if err := form.Run(); err != nil {
		return err
	}

	if newModel != current {
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

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title("Enable Telegram bot?").
				Value(&enabled),
		),
	)

	// Set initial value
	enabled = currentEnabled

	if err := form.Run(); err != nil {
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

		var newToken string
		form := huh.NewForm(
			huh.NewGroup(
				huh.NewInput().
					Title("Telegram Bot Token").
					Description("Leave empty to keep current token").
					EchoMode(huh.EchoModePassword).
					Value(&newToken),
			),
		)

		if err := form.Run(); err != nil {
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

	var newListen string
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("HTTP listen address").
				Description("Format: :port or host:port").
				Value(&newListen).
				Placeholder(current),
		),
	)

	if err := form.Run(); err != nil {
		return err
	}

	if newListen != "" && newListen != current {
		if e.config["http"] == nil {
			e.config["http"] = make(map[string]interface{})
		}
		e.config["http"].(map[string]interface{})["listen"] = newListen
		e.modified = true
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
