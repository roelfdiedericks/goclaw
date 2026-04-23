// Package config defines the Telegram channel configuration.
// This is a separate package to avoid import cycles with gateway.
package config

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/roelfdiedericks/goclaw/internal/bus"
	"github.com/roelfdiedericks/goclaw/internal/config/forms"
	"github.com/roelfdiedericks/goclaw/internal/logging"
)

// Config holds the Telegram bot configuration
type Config struct {
	Enabled   bool            `json:"enabled"`
	BotToken  string          `json:"botToken"`
	RateLimit RateLimitConfig `json:"rateLimit"`
}

// RateLimitConfig controls outbound flood-control behavior for the Telegram bot.
// All durations are in milliseconds to match the project's existing retry-config style.
type RateLimitConfig struct {
	// MaxRetries is the maximum number of flood-retry attempts after the initial send.
	// A value of 3 means up to 4 attempts total (initial + 3 retries).
	MaxRetries int `json:"maxRetries" default:"3"`
	// InitialBackoffMs is the minimum backoff for the first retry; subsequent retries
	// double this value. The actual wait is max(retry_after, initial*2^attempt).
	InitialBackoffMs int `json:"initialBackoffMs" default:"1000"`
	// MaxBackoffMs caps the wait per retry. If Telegram's retry_after exceeds this
	// value, the retry is abandoned and the error is returned immediately.
	MaxBackoffMs int `json:"maxBackoffMs" default:"120000"`
	// PerChatMinGapMs is the minimum spacing between consecutive sends to the same
	// chat. Acts as a proactive throttle to avoid hitting Telegram's per-chat limit.
	// Only sends to the same chatID serialize; different chats never block each other.
	PerChatMinGapMs int `json:"perChatMinGapMs" default:"35"`
}

// Normalize fills in default values for any zero-valued rate-limit fields so
// configs loaded from disk without a `rateLimit` block behave correctly.
func (c *Config) Normalize() {
	if c.RateLimit.MaxRetries <= 0 {
		c.RateLimit.MaxRetries = 3
	}
	if c.RateLimit.InitialBackoffMs <= 0 {
		c.RateLimit.InitialBackoffMs = 1000
	}
	if c.RateLimit.MaxBackoffMs <= 0 {
		c.RateLimit.MaxBackoffMs = 120000
	}
	if c.RateLimit.PerChatMinGapMs <= 0 {
		c.RateLimit.PerChatMinGapMs = 35
	}
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

	logging.L_info("telegram: config applied", "enabled", cfg.Enabled, "hasToken", cfg.BotToken != "")
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
		logging.L_warn("telegram: test connection failed", "error", err)
		return bus.CommandResult{
			Error:   err,
			Message: fmt.Sprintf("Connection failed: %s", err),
		}
	}

	logging.L_info("telegram: test connection successful", "bot", "@"+username)
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

	logging.L_debug("telegram: validated token", "username", result.Result.Username)
	return result.Result.Username, nil
}
