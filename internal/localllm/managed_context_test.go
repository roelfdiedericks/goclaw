package localllm

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func TestDeriveManagedEffectiveContext_overrideWins(t *testing.T) {
	got := deriveManagedEffectiveContext(2048, "/no/such/file", 4096)
	if got != 2048 {
		t.Fatalf("expected override 2048, got %d", got)
	}
}

func TestDeriveManagedEffectiveContext_ggufWinsOverCatalog(t *testing.T) {
	data := buildMinimalGGUF([]func(*bytes.Buffer){
		func(b *bytes.Buffer) {
			writeGGUFKV(b, "llama.context_length", ggufTypeUint64, func(w *bytes.Buffer) {
				_ = binary.Write(w, binary.LittleEndian, uint64(7777))
			})
		},
	})
	path := filepath.Join(t.TempDir(), "m.gguf")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	got := deriveManagedEffectiveContext(0, path, 4096)
	if got != 7777 {
		t.Fatalf("expected GGUF 7777, got %d", got)
	}
}

func TestDeriveManagedEffectiveContext_catalogFallback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.gguf")
	got := deriveManagedEffectiveContext(0, path, 8192)
	if got != 8192 {
		t.Fatalf("expected catalog fallback 8192, got %d", got)
	}
}
