package setup

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/roelfdiedericks/goclaw/internal/channels/telegram"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// Model represents a model from an API response
type Model struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

// TestProvider tests a provider connection and returns available models
func TestProvider(preset ProviderPreset, apiKey string) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	switch preset.Type {
	case "anthropic":
		return ListAnthropicModels(ctx, apiKey)
	case "openai":
		return ListOpenAIModels(ctx, preset.BaseURL, apiKey)
	case "ollama":
		return ListOllamaModels(ctx, preset.BaseURL)
	default:
		return nil, fmt.Errorf("unknown provider type: %s", preset.Type)
	}
}

// ListAnthropicModels fetches available models from Anthropic API
func ListAnthropicModels(ctx context.Context, apiKey string) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", "https://api.anthropic.com/v1/models", nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("connection failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 401 {
		return nil, fmt.Errorf("invalid API key")
	}
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API error (%d): %s", resp.StatusCode, string(body))
	}

	var result struct {
		Data []Model `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	var models []string
	for _, m := range result.Data {
		models = append(models, m.ID)
	}

	L_debug("setup: listed Anthropic models", "count", len(models))
	return models, nil
}

// ListOpenAIModels fetches available models from an OpenAI-compatible API
func ListOpenAIModels(ctx context.Context, baseURL, apiKey string) ([]string, error) {
	url := strings.TrimSuffix(baseURL, "/") + "/models"

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("connection failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 401 {
		return nil, fmt.Errorf("invalid API key")
	}
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API error (%d): %s", resp.StatusCode, string(body))
	}

	var result struct {
		Data []Model `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	var models []string
	for _, m := range result.Data {
		models = append(models, m.ID)
	}

	L_debug("setup: listed OpenAI-compatible models", "url", baseURL, "count", len(models))
	return models, nil
}

// ListOllamaModels fetches available models from Ollama
func ListOllamaModels(ctx context.Context, baseURL string) ([]string, error) {
	url := strings.TrimSuffix(baseURL, "/") + "/api/tags"

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("connection failed (is Ollama running?): %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API error (%d): %s", resp.StatusCode, string(body))
	}

	var result struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	var models []string
	for _, m := range result.Models {
		models = append(models, m.Name)
	}

	L_debug("setup: listed Ollama models", "url", baseURL, "count", len(models))
	return models, nil
}

// TestTelegramToken validates a Telegram bot token by calling getMe
func TestTelegramToken(token string) (string, error) {
	return telegram.TestToken(token)
}

// TestConnection tests basic connectivity to a URL
func TestConnection(url string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("connection failed: %w", err)
	}
	resp.Body.Close()

	return nil
}
