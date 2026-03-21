package websearch

import (
	"strings"

	toolsconfig "github.com/roelfdiedericks/goclaw/internal/tools/config"
)

type llmProviderCredential struct {
	Driver  string
	Subtype string
	APIKey  string
}

type toolConfig struct {
	Web          toolsconfig.WebToolsConfig
	LLMProviders map[string]llmProviderCredential
}

type providerAttempt struct {
	ID     string
	Config ProviderConfig
}

func normalizeProviderID(raw string) string {
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case "xai", "grok":
		return providerGrok
	case providerBrave:
		return providerBrave
	case "pplx", providerPerplexity:
		return providerPerplexity
	case "google", providerGemini:
		return providerGemini
	default:
		return ""
	}
}

func normalizeProviderList(raw []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(raw))
	for _, p := range raw {
		id := normalizeProviderID(p)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

func (t *Tool) resolveProviderOrder() []string {
	cfg := t.cfg.Web.Search
	explicit := normalizeProviderID(cfg.Provider)

	if explicit == "" || explicit == "auto" {
		return append([]string(nil), defaultAutoProviderOrder...)
	}

	order := []string{explicit}
	for _, p := range defaultAutoProviderOrder {
		if p != explicit {
			order = append(order, p)
		}
	}
	return order
}

func (t *Tool) providerConfigFor(id string) ProviderConfig {
	p := t.cfg.Web.Search.Providers
	cfg := ProviderConfig{}
	switch id {
	case providerBrave:
		cfg.APIKey = strings.TrimSpace(p.Brave.APIKey)
		cfg.BaseURL = strings.TrimSpace(p.Brave.BaseURL)
		cfg.Model = strings.TrimSpace(p.Brave.Model)
		// Legacy fallback for existing users.
		if cfg.APIKey == "" {
			cfg.APIKey = strings.TrimSpace(t.cfg.Web.BraveAPIKey)
		}
	case providerGrok:
		cfg.APIKey = strings.TrimSpace(p.Grok.APIKey)
		cfg.BaseURL = strings.TrimSpace(p.Grok.BaseURL)
		cfg.Model = strings.TrimSpace(p.Grok.Model)
	case providerPerplexity:
		cfg.APIKey = strings.TrimSpace(p.Perplexity.APIKey)
		cfg.BaseURL = strings.TrimSpace(p.Perplexity.BaseURL)
		cfg.Model = strings.TrimSpace(p.Perplexity.Model)
	case providerGemini:
		cfg.APIKey = strings.TrimSpace(p.Gemini.APIKey)
		cfg.BaseURL = strings.TrimSpace(p.Gemini.BaseURL)
		cfg.Model = strings.TrimSpace(p.Gemini.Model)
	}

	if cfg.APIKey == "" {
		cfg.APIKey = strings.TrimSpace(t.resolveLLMProviderAPIKey(id))
	}
	return cfg
}

func (t *Tool) resolveLLMProviderAPIKey(webProviderID string) string {
	for _, p := range t.cfg.LLMProviders {
		if strings.TrimSpace(p.APIKey) == "" {
			continue
		}
		driver := strings.ToLower(strings.TrimSpace(p.Driver))
		subtype := strings.ToLower(strings.TrimSpace(p.Subtype))
		switch webProviderID {
		case providerGrok:
			if driver == "xai" || subtype == "xai" {
				return p.APIKey
			}
		case providerGemini:
			if driver == "google" || subtype == "google" || subtype == "gemini" {
				return p.APIKey
			}
		case providerPerplexity:
			if driver == "perplexity" || subtype == "perplexity" {
				return p.APIKey
			}
		}
	}
	return ""
}

func (t *Tool) resolveProviderChain() []providerAttempt {
	order := t.resolveProviderOrder()
	explicitFallback := normalizeProviderList(t.cfg.Web.Search.FallbackProviders)
	if len(explicitFallback) > 0 {
		order = explicitFallback
	}

	seen := map[string]bool{}
	chain := make([]providerAttempt, 0, len(order))
	for _, id := range order {
		id = normalizeProviderID(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		chain = append(chain, providerAttempt{
			ID:     id,
			Config: t.providerConfigFor(id),
		})
	}
	return chain
}
