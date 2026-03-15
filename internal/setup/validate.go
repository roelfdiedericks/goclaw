package setup

import (
	"context"
	"fmt"
	"net/http"
	"time"

	telegramconfig "github.com/roelfdiedericks/goclaw/internal/channels/telegram/config"
	"github.com/roelfdiedericks/goclaw/internal/llm"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// TestProvider tests a provider connection and returns available models
func TestProvider(preset ProviderPreset, apiKey string) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	provider, err := llm.NewProvider("setup-validate", llm.LLMProviderConfig{
		Driver:  preset.Driver,
		APIKey:  apiKey,
		BaseURL: preset.BaseURL,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to initialize provider %s: %w", preset.Driver, err)
	}

	if tester, ok := provider.(llm.ConnectionTester); ok {
		if err := tester.TestConnection(ctx); err != nil {
			return nil, err
		}
	}

	lister, ok := provider.(llm.ModelLister)
	if !ok {
		return nil, fmt.Errorf("provider %s does not support model listing", preset.Driver)
	}
	modelInfos, err := lister.ListModels(ctx)
	if err != nil {
		return nil, err
	}
	models := make([]string, 0, len(modelInfos))
	for _, m := range modelInfos {
		if m.ID == "" {
			continue
		}
		models = append(models, m.ID)
	}
	L_debug("setup: listed provider models", "driver", preset.Driver, "count", len(models))
	return models, nil
}

// TestTelegramToken validates a Telegram bot token by calling getMe
func TestTelegramToken(token string) (string, error) {
	return telegramconfig.TestToken(token)
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
