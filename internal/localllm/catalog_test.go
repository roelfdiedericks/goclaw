package localllm

import "testing"

func TestManagedModelCatalogIncludesGemma4Families(t *testing.T) {
	catalog := ManagedModelCatalog()
	if len(catalog) != 5 {
		t.Fatalf("expected 5 managed models, got %d", len(catalog))
	}

	for _, id := range []string{"gemma4-e2b", "gemma4-e4b", "gemma4-26b-a4b", "gemma4-31b"} {
		spec, err := ManagedModelByID(id)
		if err != nil {
			t.Fatalf("ManagedModelByID(%q) returned error: %v", id, err)
		}
		if spec.Family != ModelFamilyGemma4 {
			t.Fatalf("expected gemma4 family for %q, got %q", id, spec.Family)
		}
		if spec.HFRepo == "" || spec.PreferredFilename == "" || spec.MMProjFilename == "" {
			t.Fatalf("expected download metadata for %q, got %#v", id, spec)
		}
	}
}

func TestManagedModelCatalogQwen3CoderNextTextOnly(t *testing.T) {
	spec, err := ManagedModelByID("qwen3-coder-next")
	if err != nil {
		t.Fatalf("ManagedModelByID: %v", err)
	}
	if spec.Family != ModelFamilyQwenCoder {
		t.Fatalf("family: got %q", spec.Family)
	}
	if spec.HFRepo != "ggml-org/Qwen3-Coder-Next-GGUF" || spec.PreferredFilename != "Qwen3-Coder-Next-Q8_0.gguf" {
		t.Fatalf("unexpected repo/filename: %#v", spec)
	}
	if spec.MMProjFilename != "" {
		t.Fatalf("expected no mmproj for text-only catalog entry")
	}
}

func TestManagedModelByIDUnknown(t *testing.T) {
	if _, err := ManagedModelByID("nope"); err == nil {
		t.Fatalf("expected unknown model error")
	}
}
