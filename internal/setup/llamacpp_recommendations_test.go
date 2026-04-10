package setup

import "testing"

func TestConfigureWizardForManagedLlamaCppUsesManagedSentinel(t *testing.T) {
	data := NewWizardData()

	ConfigureWizardForManagedLlamaCpp(data, "gemma4-e2b")

	if data.LLMOnboardingChoice != LLMChoiceLocalGemma {
		t.Fatalf("expected local gemma onboarding choice, got %q", data.LLMOnboardingChoice)
	}
	if data.LLMProviderID != LLMProviderManagedLlamaCpp {
		t.Fatalf("expected managed provider preset, got %q", data.LLMProviderID)
	}
	if data.LLMDriver != "llamacpp" {
		t.Fatalf("expected llamacpp driver, got %q", data.LLMDriver)
	}
	if data.LLMModel != "managed" {
		t.Fatalf("expected managed model sentinel, got %q", data.LLMModel)
	}
	if data.LLMManagedModelID != "gemma4-e2b" {
		t.Fatalf("expected managed model selection to be preserved, got %q", data.LLMManagedModelID)
	}
}

func TestWizardLLMModelDisplayUsesManagedModelLabel(t *testing.T) {
	data := NewWizardData()
	data.LLMOnboardingChoice = LLMChoiceLocalGemma
	data.LLMManagedModelID = "gemma4-e2b"

	got := WizardLLMModelDisplay(data)
	if got != "Gemma 4 E2B" {
		t.Fatalf("expected managed model label, got %q", got)
	}
}
