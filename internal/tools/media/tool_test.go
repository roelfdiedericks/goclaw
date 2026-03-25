package media

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	mediastore "github.com/roelfdiedericks/goclaw/internal/media"
)

func TestMediaToolInfoReturnsStoreSnapshot(t *testing.T) {
	baseDir := t.TempDir()
	store, err := mediastore.NewMediaStore(mediastore.MediaConfig{Dir: baseDir})
	if err != nil {
		t.Fatalf("NewMediaStore: %v", err)
	}
	t.Cleanup(store.Close)

	path := writeToolMediaFile(t, baseDir, "voice", "sample.txt", "voice-data")
	if path == "" {
		t.Fatal("expected media file path")
	}

	tool := NewTool(store)
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"info","category":"voice"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	payload := decodeToolOutput(t, result.GetText())
	if payload["ok"] != true {
		t.Fatalf("expected ok=true, got %#v", payload["ok"])
	}
	if payload["action"] != "info" {
		t.Fatalf("expected action=info, got %#v", payload["action"])
	}
	if payload["category"] != "voice" {
		t.Fatalf("expected category=voice, got %#v", payload["category"])
	}
	if !strings.Contains(payload["summary"].(string), "Voice uses") {
		t.Fatalf("expected voice summary, got %#v", payload["summary"])
	}
	categories := payload["categories"].(map[string]any)
	if len(categories) != 1 {
		t.Fatalf("expected one category, got %d", len(categories))
	}
	voice := categories["voice"].(map[string]any)
	if voice["status"] != "ok" {
		t.Fatalf("expected voice status ok, got %#v", voice["status"])
	}
}

func TestMediaToolInfoReturnsStructuredErrorWhenStoreMissing(t *testing.T) {
	tool := NewTool(nil)

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"info"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	payload := decodeToolOutput(t, result.GetText())
	if payload["ok"] != false {
		t.Fatalf("expected ok=false, got %#v", payload["ok"])
	}
	errPayload := payload["error"].(map[string]any)
	if errPayload["code"] != "not_initialized" {
		t.Fatalf("expected not_initialized code, got %#v", errPayload["code"])
	}
}

func TestMediaToolInfoReturnsStructuredErrorForBadCategory(t *testing.T) {
	baseDir := t.TempDir()
	store, err := mediastore.NewMediaStore(mediastore.MediaConfig{Dir: baseDir})
	if err != nil {
		t.Fatalf("NewMediaStore: %v", err)
	}
	t.Cleanup(store.Close)

	tool := NewTool(store)
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"info","category":"bad"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	payload := decodeToolOutput(t, result.GetText())
	if payload["ok"] != false {
		t.Fatalf("expected ok=false, got %#v", payload["ok"])
	}
	errPayload := payload["error"].(map[string]any)
	if errPayload["code"] != "invalid_category" {
		t.Fatalf("expected invalid_category code, got %#v", errPayload["code"])
	}
}

func decodeToolOutput(t *testing.T, text string) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		t.Fatalf("unmarshal tool output: %v\n%s", err, text)
	}
	return payload
}

func writeToolMediaFile(t *testing.T, baseDir, subdir, name, content string) string {
	t.Helper()
	path := mediastorePath(t, baseDir, subdir, name)
	modTime := time.Now()
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	if err := os.Chtimes(path, modTime, modTime); err != nil {
		t.Fatalf("chtimes %s: %v", path, err)
	}
	return path
}

func mediastorePath(t *testing.T, baseDir, subdir, name string) string {
	t.Helper()
	dir := filepath.Join(baseDir, filepath.FromSlash(subdir))
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	return filepath.Join(dir, name)
}
