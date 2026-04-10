package localllm

import "testing"

func TestManagedModelCatalogIncludesGemma4Families(t *testing.T) {
	catalog := ManagedModelCatalog()
	if len(catalog) != 4 {
		t.Fatalf("expected 4 managed models, got %d", len(catalog))
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

func TestManagedModelByIDUnknown(t *testing.T) {
	if _, err := ManagedModelByID("nope"); err == nil {
		t.Fatalf("expected unknown model error")
	}
}
