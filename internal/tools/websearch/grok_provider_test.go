package websearch

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGrokDriverUsesResponsesWebSearchToolType(t *testing.T) {
	var gotToolType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("unmarshal payload: %v", err)
		}
		toolsRaw, ok := payload["tools"].([]any)
		if !ok || len(toolsRaw) == 0 {
			t.Fatalf("tools missing in payload: %#v", payload["tools"])
		}
		firstTool, ok := toolsRaw[0].(map[string]any)
		if !ok {
			t.Fatalf("unexpected tool shape: %#v", toolsRaw[0])
		}
		gotToolType, _ = firstTool["type"].(string)

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"output_text":"ok","citations":["https://example.com","https://example.com"]}`))
	}))
	defer srv.Close()

	d := &grokDriver{}
	resp, err := d.Search(context.Background(), SearchRequest{Query: "test query"}, ProviderConfig{
		APIKey:  "xai-key",
		BaseURL: srv.URL,
		Model:   "grok-4-0709",
	})
	if err != nil {
		t.Fatalf("search returned error: %v", err)
	}
	if gotToolType != "web_search" {
		t.Fatalf("expected tool type web_search, got %q", gotToolType)
	}
	if resp.Provider != providerGrok {
		t.Fatalf("expected provider %q, got %q", providerGrok, resp.Provider)
	}
	if resp.Answer != "ok" {
		t.Fatalf("expected answer ok, got %q", resp.Answer)
	}
	if len(resp.Citations) != 1 || resp.Citations[0] != "https://example.com" {
		t.Fatalf("expected deduped citation, got %#v", resp.Citations)
	}
}
