package setup

import (
	"testing"

	"github.com/roelfdiedericks/goclaw/internal/config"
	"github.com/roelfdiedericks/goclaw/internal/llm"
)

func TestBuildConfigFromWizardDataSeedsBuiltInHugotEmbeddings(t *testing.T) {
	cfg := buildConfigFromWizardData(&WizardData{})

	if got := getStringSlice(cfg, "llm.embeddings.models"); len(got) != 1 || got[0] != llm.BuiltInHugotProviderAlias+"/"+llm.DefaultHugotEmbeddingModel {
		t.Fatalf("expected default Hugot embeddings chain, got %#v", got)
	}

	providers, ok := cfg["llm"].(map[string]interface{})["providers"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected llm.providers map to be present")
	}
	hugotCfgRaw, ok := providers[llm.BuiltInHugotProviderAlias]
	if !ok {
		t.Fatalf("expected built-in provider %q to be present", llm.BuiltInHugotProviderAlias)
	}
	hugotCfg, ok := hugotCfgRaw.(map[string]interface{})
	if !ok {
		t.Fatalf("expected built-in provider config to be a map")
	}
	if got := hugotCfg["driver"]; got != "hugot" {
		t.Fatalf("expected built-in provider driver hugot, got %#v", got)
	}
}

func TestBuildConfigFromWizardDataPreservesExistingEmbeddingsChain(t *testing.T) {
	data := &WizardData{
		ExistingConfig: &config.Config{
			LLM: llm.LLMConfig{
				Embeddings: llm.LLMPurposeConfig{
					Models: []string{"existing/provider-model"},
				},
			},
		},
	}

	cfg := buildConfigFromWizardData(data)
	got := getStringSlice(cfg, "llm.embeddings.models")
	if len(got) != 1 || got[0] != "existing/provider-model" {
		t.Fatalf("expected existing embeddings chain to be preserved, got %#v", got)
	}
}
