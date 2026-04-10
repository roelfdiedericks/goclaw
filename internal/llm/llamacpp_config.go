package llm

import (
	"fmt"
	"strings"

	"github.com/roelfdiedericks/goclaw/internal/localllm"
)

const (
	LlamaCppModeManaged  = "managed"
	LlamaCppModeEndpoint = "endpoint"
)

func resolveLlamaCppProviderConfig(cfg LLMProviderConfig) LLMProviderConfig {
	if cfg.Driver != "llamacpp" || cfg.LlamaCpp == nil {
		return cfg
	}

	resolved := cfg
	mode := normalizedLlamaCppMode(cfg)
	if mode != LlamaCppModeManaged {
		return resolved
	}

	host := strings.TrimSpace(cfg.LlamaCpp.Host)
	if host == "" {
		host = "127.0.0.1"
	}
	port := cfg.LlamaCpp.Port
	if port == 0 {
		port = 8080
	}
	resolved.BaseURL = fmt.Sprintf("http://%s:%d", host, port)
	return resolved
}

func resolveLlamaCppModelName(cfg LLMProviderConfig, requested string) string {
	if cfg.Driver != "llamacpp" || cfg.LlamaCpp == nil || normalizedLlamaCppMode(cfg) != LlamaCppModeManaged {
		return requested
	}

	model := strings.TrimSpace(requested)
	if model != "" && model != "managed" && model != "@managed" {
		return model
	}

	if alias := strings.TrimSpace(cfg.LlamaCpp.ModelAlias); alias != "" {
		return alias
	}

	spec, err := localllm.ManagedModelByID(strings.TrimSpace(cfg.LlamaCpp.ManagedModelID))
	if err != nil {
		return model
	}
	return spec.APIModelName()
}

func normalizedLlamaCppMode(cfg LLMProviderConfig) string {
	if cfg.LlamaCpp == nil {
		return ""
	}
	mode := strings.ToLower(strings.TrimSpace(cfg.LlamaCpp.Mode))
	if mode != "" {
		return mode
	}
	if strings.TrimSpace(cfg.BaseURL) != "" {
		return LlamaCppModeEndpoint
	}
	return LlamaCppModeManaged
}
