package setup

import (
	"testing"

	"github.com/roelfdiedericks/goclaw/internal/config"
	"github.com/roelfdiedericks/goclaw/internal/llm"
	"github.com/roelfdiedericks/goclaw/internal/localllm"
)

func TestConfigureManagedLocalProviderForEditorCreatesProvider(t *testing.T) {
	cfg := &config.Config{}

	alias, agentRef, changed := configureManagedLocalProviderForEditor(cfg, localllm.ManagedSpec{
		ModelID: "gemma4-e2b",
		Host:    "127.0.0.1",
		Port:    8080,
	}, false)

	if !changed {
		t.Fatalf("expected provider configuration to mark config changed")
	}
	if agentRef != "" {
		t.Fatalf("expected no agent ref for provider-only configuration, got %q", agentRef)
	}
	if alias != "local-llm" {
		t.Fatalf("expected default alias local-llm, got %q", alias)
	}

	provider, ok := cfg.LLM.Providers[alias]
	if !ok {
		t.Fatalf("expected provider %q to be created", alias)
	}
	if provider.Driver != "llamacpp" || provider.Subtype != "llamacpp-managed" || provider.LlamaCpp == nil {
		t.Fatalf("unexpected provider %#v", provider)
	}
	if provider.LlamaCpp.Mode != llm.LlamaCppModeManaged || provider.LlamaCpp.ManagedModelID != "gemma4-e2b" {
		t.Fatalf("unexpected managed provider config %#v", provider.LlamaCpp)
	}
	if provider.LlamaCpp.Host != "127.0.0.1" || provider.LlamaCpp.Port != 8080 {
		t.Fatalf("unexpected managed provider host/port %#v", provider.LlamaCpp)
	}
}

func TestConfigureManagedLocalProviderForEditorUpdatesAgentChain(t *testing.T) {
	cfg := &config.Config{
		LLM: llm.LLMConfig{
			Providers: map[string]llm.LLMProviderConfig{
				"local": {
					Driver:  "llamacpp",
					Subtype: "llamacpp-managed",
					LlamaCpp: &llm.LlamaCppProviderConfig{
						Mode:           llm.LlamaCppModeManaged,
						ManagedModelID: "gemma4-e2b",
						Host:           "127.0.0.1",
						Port:           8080,
					},
				},
			},
			Agent: llm.LLMPurposeConfig{
				Models: []string{
					"anthropic/claude-sonnet-4-20250514",
					"local/custom-model",
				},
			},
		},
	}

	alias, agentRef, changed := configureManagedLocalProviderForEditor(cfg, localllm.ManagedSpec{
		ModelID: "gemma4-e4b",
		Host:    "127.0.0.1",
		Port:    8080,
	}, true)

	if !changed {
		t.Fatalf("expected provider+chain update to mark config changed")
	}
	if alias != "local" {
		t.Fatalf("expected existing provider alias local, got %q", alias)
	}
	if agentRef != "local/managed" {
		t.Fatalf("expected normalized agent ref local/managed, got %q", agentRef)
	}
	if got := cfg.LLM.Providers["local"].LlamaCpp.ManagedModelID; got != "gemma4-e4b" {
		t.Fatalf("expected managed model update, got %q", got)
	}
	if len(cfg.LLM.Agent.Models) != 2 {
		t.Fatalf("unexpected agent chain %#v", cfg.LLM.Agent.Models)
	}
	if cfg.LLM.Agent.Models[0] != "anthropic/claude-sonnet-4-20250514" {
		t.Fatalf("expected primary chain entry preserved, got %#v", cfg.LLM.Agent.Models)
	}
	if cfg.LLM.Agent.Models[1] != "local/managed" {
		t.Fatalf("expected existing local alias entry normalized, got %#v", cfg.LLM.Agent.Models)
	}
}
