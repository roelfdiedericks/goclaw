package websearch

import (
	"testing"

	toolsconfig "github.com/roelfdiedericks/goclaw/internal/tools/config"
)

func TestNormalizeProviderID(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{in: "grok", want: providerGrok},
		{in: "xai", want: providerGrok},
		{in: "brave", want: providerBrave},
		{in: "perplexity", want: providerPerplexity},
		{in: "pplx", want: providerPerplexity},
		{in: "gemini", want: providerGemini},
		{in: "google", want: providerGemini},
		{in: "unknown", want: ""},
	}

	for _, tt := range tests {
		if got := normalizeProviderID(tt.in); got != tt.want {
			t.Fatalf("normalizeProviderID(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestResolveProviderChain_AutoOrderAndFallbackOverride(t *testing.T) {
	tool := &Tool{
		cfg: toolConfig{
			Web: toolsconfig.WebToolsConfig{
				Search: toolsconfig.WebSearchConfig{
					Enabled:  true,
					Provider: "auto",
					Providers: toolsconfig.WebSearchProvidersConfig{
						Grok:       toolsconfig.WebSearchProviderConfig{APIKey: "grok-key"},
						Brave:      toolsconfig.WebSearchProviderConfig{APIKey: "brave-key"},
						Perplexity: toolsconfig.WebSearchProviderConfig{APIKey: "pplx-key"},
						Gemini:     toolsconfig.WebSearchProviderConfig{APIKey: "gem-key"},
					},
				},
			},
		},
	}

	chain := tool.resolveProviderChain()
	if len(chain) != 4 {
		t.Fatalf("expected 4 providers in chain, got %d", len(chain))
	}
	if chain[0].ID != providerGrok || chain[1].ID != providerBrave || chain[2].ID != providerPerplexity || chain[3].ID != providerGemini {
		t.Fatalf("unexpected auto chain order: %+v", providerIDs(chain))
	}

	tool.cfg.Web.Search.FallbackProviders = []string{"brave", "grok", "brave"}
	chain = tool.resolveProviderChain()
	if len(chain) != 2 {
		t.Fatalf("expected deduped fallback chain length 2, got %d", len(chain))
	}
	if chain[0].ID != providerBrave || chain[1].ID != providerGrok {
		t.Fatalf("unexpected fallback chain order: %+v", providerIDs(chain))
	}
}

func TestProviderConfigFor_KeyPrecedence(t *testing.T) {
	tool := &Tool{
		cfg: toolConfig{
			Web: toolsconfig.WebToolsConfig{
				BraveAPIKey: "legacy-brave",
				Search: toolsconfig.WebSearchConfig{
					Providers: toolsconfig.WebSearchProvidersConfig{
						Brave: toolsconfig.WebSearchProviderConfig{APIKey: ""},
						Grok:  toolsconfig.WebSearchProviderConfig{APIKey: ""},
					},
				},
			},
			LLMProviders: map[string]llmProviderCredential{
				"xai": {Driver: "xai", APIKey: "llm-xai-key"},
			},
		},
	}

	braveCfg := tool.providerConfigFor(providerBrave)
	if braveCfg.APIKey != "legacy-brave" {
		t.Fatalf("expected brave legacy key fallback, got %q", braveCfg.APIKey)
	}

	grokCfg := tool.providerConfigFor(providerGrok)
	if grokCfg.APIKey != "llm-xai-key" {
		t.Fatalf("expected grok llm key fallback, got %q", grokCfg.APIKey)
	}

	tool.cfg.Web.Search.Providers.Grok.APIKey = "web-grok-key"
	grokCfg = tool.providerConfigFor(providerGrok)
	if grokCfg.APIKey != "web-grok-key" {
		t.Fatalf("expected explicit web grok key override, got %q", grokCfg.APIKey)
	}
}
