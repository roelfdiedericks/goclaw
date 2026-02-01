package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Config represents the merged goclaw configuration
type Config struct {
	Gateway  GatewayConfig  `json:"gateway"`
	Telegram TelegramConfig `json:"telegram"`
	LLM      LLMConfig      `json:"llm"`
}

type GatewayConfig struct {
	Port int `json:"port"`
}

type TelegramConfig struct {
	BotToken     string  `json:"botToken"`
	AllowedUsers []int64 `json:"allowedUsers"`
}

type LLMConfig struct {
	Provider string `json:"provider"` // "anthropic"
	Model    string `json:"model"`
	APIKey   string `json:"apiKey"`
}

// Load reads configuration from goclaw.json, falling back to openclaw.json
// goclaw.json values override openclaw.json values
func Load() (*Config, error) {
	cfg := &Config{
		Gateway: GatewayConfig{
			Port: 3378, // Default: different from openclaw's 3377
		},
		LLM: LLMConfig{
			Provider: "anthropic",
			Model:    "claude-opus-4-5",
		},
	}

	// Try to find config files
	home, _ := os.UserHomeDir()
	openclawPath := filepath.Join(home, ".openclaw", "openclaw.json")
	goclawPath := filepath.Join(home, ".openclaw", "goclaw.json")

	// Load base config from openclaw.json if it exists
	if data, err := os.ReadFile(openclawPath); err == nil {
		// Parse relevant fields from openclaw.json
		var base map[string]interface{}
		if err := json.Unmarshal(data, &base); err == nil {
			cfg.mergeOpenclawConfig(base)
		}
	}

	// Override with goclaw.json if it exists
	if data, err := os.ReadFile(goclawPath); err == nil {
		if err := json.Unmarshal(data, cfg); err != nil {
			return nil, err
		}
	}

	return cfg, nil
}

// mergeOpenclawConfig extracts relevant settings from openclaw.json
func (c *Config) mergeOpenclawConfig(base map[string]interface{}) {
	// Extract LLM settings
	if llm, ok := base["llm"].(map[string]interface{}); ok {
		if key, ok := llm["apiKey"].(string); ok {
			c.LLM.APIKey = key
		}
		if model, ok := llm["model"].(string); ok {
			c.LLM.Model = model
		}
	}

	// Extract Telegram settings from channels
	if channels, ok := base["channels"].([]interface{}); ok {
		for _, ch := range channels {
			if channel, ok := ch.(map[string]interface{}); ok {
				if kind, ok := channel["kind"].(string); ok && kind == "telegram" {
					if token, ok := channel["botToken"].(string); ok {
						c.Telegram.BotToken = token
					}
					if users, ok := channel["allowedUsers"].([]interface{}); ok {
						for _, u := range users {
							if uid, ok := u.(float64); ok {
								c.Telegram.AllowedUsers = append(c.Telegram.AllowedUsers, int64(uid))
							}
						}
					}
				}
			}
		}
	}
}
