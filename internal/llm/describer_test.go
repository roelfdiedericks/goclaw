package llm

import (
	"context"
	"errors"
	"testing"
)

func TestAgentVisionDescriberNilRegistry(t *testing.T) {
	d := &AgentVisionDescriber{Registry: nil, Purpose: "agent"}
	_, err := d.DescribeImage(context.Background(), []byte("x"), "image/png", "describe")
	if !errors.Is(err, ErrNoRegistry) {
		t.Fatalf("expected ErrNoRegistry, got %v", err)
	}
}

func TestAgentVisionDescriberNilReceiver(t *testing.T) {
	var d *AgentVisionDescriber
	_, err := d.DescribeImage(context.Background(), []byte("x"), "image/png", "describe")
	if !errors.Is(err, ErrNoRegistry) {
		t.Fatalf("expected ErrNoRegistry, got %v", err)
	}
}

func TestNewAgentVisionDescriberDefaultsToAgentPurpose(t *testing.T) {
	d := NewAgentVisionDescriber(nil)
	if d.Purpose != "agent" {
		t.Fatalf("expected default purpose \"agent\", got %q", d.Purpose)
	}
}

func TestDescribeImageWithFailoverNoVisionModels(t *testing.T) {
	// Registry with a non-vision mock provider in the agent chain.
	r := &Registry{
		providers: map[string]providerInstance{
			"p1": {provider: &mockProvider{name: "p1", typ: "mock", model: "base"}},
		},
		purposes: map[string]LLMPurposeConfig{
			"agent": {Models: []string{"p1/base"}},
		},
		cooldowns: make(map[string]*providerCooldown),
	}

	_, err := r.DescribeImageWithFailover(
		context.Background(),
		"agent",
		[]byte{0x89, 0x50, 0x4E, 0x47},
		"image/png",
		"describe this",
	)
	if !errors.Is(err, ErrNoVisionModels) {
		t.Fatalf("expected ErrNoVisionModels, got %v", err)
	}
}

func TestDescribeImageWithFailoverEmptyImage(t *testing.T) {
	r := &Registry{
		providers: map[string]providerInstance{
			"p1": {provider: &mockProvider{name: "p1", typ: "mock", model: "base"}},
		},
		purposes: map[string]LLMPurposeConfig{
			"agent": {Models: []string{"p1/base"}},
		},
		cooldowns: make(map[string]*providerCooldown),
	}

	_, err := r.DescribeImageWithFailover(
		context.Background(),
		"agent",
		[]byte{},
		"image/png",
		"describe",
	)
	if err == nil || !containsSubstr(err.Error(), "empty image payload") {
		t.Fatalf("expected empty-image-payload error, got %v", err)
	}
}

func containsSubstr(s, sub string) bool {
	return len(s) >= len(sub) && indexOf(s, sub) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
