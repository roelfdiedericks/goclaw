package setup

import (
	"encoding/json"
	"fmt"
	"os"

	telegramconfig "github.com/roelfdiedericks/goclaw/internal/channels/telegram/config"
	"github.com/roelfdiedericks/goclaw/internal/config"
	"github.com/roelfdiedericks/goclaw/internal/config/forms"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// RunTelegramSetupTview runs the tview-based telegram configuration UI
func RunTelegramSetupTview() error {
	// Register command handlers
	telegramconfig.RegisterCommands()

	// Load config
	cfg, configPath, err := loadTelegramConfigTV()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Get form definition
	formDef := telegramconfig.ConfigFormDef()

	// Render the form (component path matches bus command namespace)
	result, err := forms.RenderTview(formDef, &cfg, "channels.telegram")
	if err != nil {
		return fmt.Errorf("form error: %w", err)
	}

	// Handle result
	switch result {
	case forms.ResultSaved:
		if err := saveTelegramConfigTV(cfg, configPath); err != nil {
			return fmt.Errorf("failed to save: %w", err)
		}
		fmt.Println("Telegram configuration saved.")
	case forms.ResultCancelled:
		fmt.Println("Cancelled.")
	}

	return nil
}

// loadTelegramConfigTV loads the telegram section from goclaw.json
func loadTelegramConfigTV() (telegramconfig.Config, string, error) {
	result, err := config.Load()
	if err != nil {
		// Return defaults if no config
		return telegramconfig.Config{
			Enabled: false,
		}, "", nil
	}

	return result.Config.Channels.Telegram, result.SourcePath, nil
}

// saveTelegramConfigTV saves the telegram config back to goclaw.json
func saveTelegramConfigTV(cfg telegramconfig.Config, configPath string) error {
	if configPath == "" {
		return fmt.Errorf("no config file path")
	}

	// Load full config
	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("failed to read config: %w", err)
	}

	var fullConfig map[string]interface{}
	if err := json.Unmarshal(data, &fullConfig); err != nil {
		return fmt.Errorf("failed to parse config: %w", err)
	}

	// Convert telegram config to map
	telegramData, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("failed to marshal telegram config: %w", err)
	}

	var telegramMap map[string]interface{}
	if err := json.Unmarshal(telegramData, &telegramMap); err != nil {
		return fmt.Errorf("failed to convert telegram config: %w", err)
	}

	// Ensure channels section exists
	channels, ok := fullConfig["channels"].(map[string]interface{})
	if !ok {
		channels = make(map[string]interface{})
		fullConfig["channels"] = channels
	}

	// Update telegram section under channels
	channels["telegram"] = telegramMap

	// Write back
	output, err := json.MarshalIndent(fullConfig, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	if err := os.WriteFile(configPath, output, 0600); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}

	L_info("telegram: config saved", "path", configPath)
	return nil
}
