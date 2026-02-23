package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/roelfdiedericks/goclaw/internal/bus"
	"github.com/roelfdiedericks/goclaw/internal/config/forms"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// Config holds the Telegram bot configuration
type Config struct {
	Enabled  bool   `json:"enabled"`
	BotToken string `json:"botToken"`
}

// ConfigFormDef returns the form definition for editing TelegramConfig
func ConfigFormDef() forms.FormDef {
	return forms.FormDef{
		Title:       "Telegram Bot",
		Description: "Configure the Telegram bot connection",
		Sections: []forms.Section{
			{
				Title: "Connection",
				Fields: []forms.Field{
					{
						Name:  "enabled",
						Title: "Enabled",
						Desc:  "Enable the Telegram bot channel",
						Type:  forms.Toggle,
					},
					{
						Name:  "botToken",
						Title: "Bot Token",
						Desc:  "Telegram bot token from @BotFather",
						Type:  forms.Secret,
					},
				},
			},
		},
		Actions: []forms.ActionDef{
			{
				Name:  "test",
				Label: "Test Connection",
				Desc:  "Validate the bot token with Telegram API",
			},
			{
				Name:  "apply",
				Label: "Apply Now",
				Desc:  "Apply changes to running bot (requires gateway)",
			},
		},
	}
}

// configPath is the bus command namespace for telegram config
const configPath = "channels.telegram"

// RegisterCommands registers telegram config command handlers
func RegisterCommands() {
	bus.RegisterCommand(configPath, "test", handleTest)
	bus.RegisterCommand(configPath, "apply", handleApply)
}

// UnregisterCommands unregisters telegram config command handlers
func UnregisterCommands() {
	bus.UnregisterComponent(configPath)
}

// handleApply publishes the config.applied event for listeners to react
func handleApply(cmd bus.Command) bus.CommandResult {
	cfg, ok := cmd.Payload.(*Config)
	if !ok {
		return bus.CommandResult{
			Error:   fmt.Errorf("invalid payload type: expected *Config, got %T", cmd.Payload),
			Message: "Internal error: invalid config type",
		}
	}

	L_info("telegram: config applied", "enabled", cfg.Enabled, "hasToken", cfg.BotToken != "")
	bus.PublishEvent(configPath+".config.applied", cfg)

	return bus.CommandResult{
		Success: true,
		Message: "Config applied - bot will restart if needed",
	}
}

// handleTest validates the bot token via Telegram API
func handleTest(cmd bus.Command) bus.CommandResult {
	cfg, ok := cmd.Payload.(*Config)
	if !ok {
		return bus.CommandResult{
			Error:   fmt.Errorf("invalid payload type"),
			Message: "Internal error: invalid config type",
		}
	}

	if cfg.BotToken == "" {
		return bus.CommandResult{
			Error:   fmt.Errorf("bot token is empty"),
			Message: "Bot token is required",
		}
	}

	username, err := TestToken(cfg.BotToken)
	if err != nil {
		L_warn("telegram: test connection failed", "error", err)
		return bus.CommandResult{
			Error:   err,
			Message: fmt.Sprintf("Connection failed: %s", err),
		}
	}

	L_info("telegram: test connection successful", "bot", "@"+username)
	return bus.CommandResult{
		Success: true,
		Message: fmt.Sprintf("Connected to @%s", username),
	}
}

// TestToken validates a Telegram bot token by calling getMe
func TestToken(token string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	url := fmt.Sprintf("https://api.telegram.org/bot%s/getMe", token)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("connection failed: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		OK     bool `json:"ok"`
		Result struct {
			Username string `json:"username"`
		} `json:"result"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("failed to parse response: %w", err)
	}

	if !result.OK {
		return "", fmt.Errorf("invalid token: %s", result.Description)
	}

	L_debug("telegram: validated token", "username", result.Result.Username)
	return result.Result.Username, nil
}
