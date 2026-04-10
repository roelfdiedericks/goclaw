package gateway

import (
	"testing"

	"github.com/roelfdiedericks/goclaw/internal/config"
	"github.com/roelfdiedericks/goclaw/internal/llm"
)

func TestManagedLocalLLMSpecFromConfig(t *testing.T) {
	cfg := &config.Config{}
	cfg.LLM.Agent.Models = []string{"llamacpp-managed/managed"}
	cfg.LLM.Providers = map[string]llm.LLMProviderConfig{
		"llamacpp-managed": {
			Driver: "llamacpp",
			LlamaCpp: &llm.LlamaCppProviderConfig{
				Mode:           llm.LlamaCppModeManaged,
				ManagedModelID: "gemma4-e2b",
				RuntimeVersion: "b7777",
				Host:           "127.0.0.1",
				Port:           8080,
				ModelAlias:     "gemma-local",
			},
		},
	}

	spec, ok := managedLocalLLMSpec(cfg)
	if !ok {
		t.Fatalf("expected managed local llamacpp config to resolve")
	}
	if spec.ModelID != "gemma4-e2b" {
		t.Fatalf("expected managed model ID, got %q", spec.ModelID)
	}
	if spec.RuntimeVersion != "b7777" {
		t.Fatalf("expected runtime version, got %q", spec.RuntimeVersion)
	}
	if spec.ModelAlias != "gemma-local" {
		t.Fatalf("expected model alias, got %q", spec.ModelAlias)
	}
}
