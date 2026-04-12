package localllm

import "testing"

func TestManagedModelCatalogIncludesGemma4Families(t *testing.T) {
	catalog := ManagedModelCatalog()
	if len(catalog) != 9 {
		t.Fatalf("expected 9 managed models, got %d", len(catalog))
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

func TestManagedModelCatalogQwen3Coder30BA3BVariants(t *testing.T) {
	cases := []struct {
		id       string
		quant    string
		filename string
	}{
		{
			id:       "qwen3-coder-30b-a3b-q2-k",
			quant:    "Q2_K",
			filename: "Qwen3-Coder-30B-A3B-Instruct.Q2_K.gguf",
		},
		{
			id:       "qwen3-coder-30b-a3b-q3-k-s",
			quant:    "Q3_K_S",
			filename: "Qwen3-Coder-30B-A3B-Instruct.Q3_K_S.gguf",
		},
		{
			id:       "qwen3-coder-30b-a3b-q4-k-s",
			quant:    "Q4_K_S",
			filename: "Qwen3-Coder-30B-A3B-Instruct.Q4_K_S.gguf",
		},
		{
			id:       "qwen3-coder-30b-a3b-q4-k-m",
			quant:    "Q4_K_M",
			filename: "Qwen3-Coder-30B-A3B-Instruct.Q4_K_M.gguf",
		},
	}

	for _, tc := range cases {
		spec, err := ManagedModelByID(tc.id)
		if err != nil {
			t.Fatalf("ManagedModelByID(%q): %v", tc.id, err)
		}
		if spec.Family != ModelFamilyQwenCoder {
			t.Fatalf("%q family: got %q", tc.id, spec.Family)
		}
		if spec.HFRepo != "mradermacher/Qwen3-Coder-30B-A3B-Instruct-GGUF" {
			t.Fatalf("%q repo: got %q", tc.id, spec.HFRepo)
		}
		if spec.PreferredQuant != tc.quant || spec.PreferredFilename != tc.filename {
			t.Fatalf("%q quant/filename mismatch: %#v", tc.id, spec)
		}
		if spec.MMProjFilename != "" {
			t.Fatalf("%q should not require mmproj", tc.id)
		}
	}
}

func TestManagedModelByIDUnknown(t *testing.T) {
	if _, err := ManagedModelByID("nope"); err == nil {
		t.Fatalf("expected unknown model error")
	}
}
