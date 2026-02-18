package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/roelfdiedericks/goclaw/internal/actions"
	"github.com/roelfdiedericks/goclaw/internal/config"
	"github.com/roelfdiedericks/goclaw/internal/config/forms"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// TConfig is an alias for config.TelegramConfig for convenience
type TConfig = config.TelegramConfig

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

// RegisterActions registers telegram config action handlers
// Note: "apply" action is registered by gateway (main.go) which owns channel lifecycle
func RegisterActions() {
	actions.Register("telegram", "test", handleTest)
}

// handleTest validates the bot token via Telegram API
func handleTest(action actions.Action) actions.Result {
	cfg, ok := action.Payload.(*TConfig)
	if !ok {
		return actions.Result{
			Error:   fmt.Errorf("invalid payload type"),
			Message: "Internal error: invalid config type",
		}
	}

	if cfg.BotToken == "" {
		return actions.Result{
			Error:   fmt.Errorf("bot token is empty"),
			Message: "Bot token is required",
		}
	}

	username, err := TestToken(cfg.BotToken)
	if err != nil {
		L_warn("telegram: test connection failed", "error", err)
		return actions.Result{
			Error:   err,
			Message: fmt.Sprintf("Connection failed: %s", err),
		}
	}

	L_info("telegram: test connection successful", "bot", "@"+username)
	return actions.Result{
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
