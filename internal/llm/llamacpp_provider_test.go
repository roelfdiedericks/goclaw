package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/roelfdiedericks/goclaw/internal/types"
)

func TestLlamaCppContextTokensUsesPropsCache(t *testing.T) {
	var propsHits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/props":
			propsHits.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"total_slots":1,"default_generation_settings":{"n_ctx":12345}}`))
		case "/health":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	p, err := NewLlamaCppProvider("local", LLMProviderConfig{BaseURL: server.URL})
	if err != nil {
		t.Fatalf("NewLlamaCppProvider returned error: %v", err)
	}
	provider := p.WithModel("test-model").(*LlamaCppProvider)

	if got := provider.ContextTokens(); got != 12345 {
		t.Fatalf("expected props context 12345, got %d", got)
	}
	if got := provider.ContextTokens(); got != 12345 {
		t.Fatalf("expected cached props context 12345, got %d", got)
	}
	if hits := propsHits.Load(); hits != 1 {
		t.Fatalf("expected /props to be fetched once, got %d", hits)
	}
}

func TestLlamaCppIsAvailableUsesUncachedHealth(t *testing.T) {
	var healthy atomic.Bool
	healthy.Store(true)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			if healthy.Load() {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"status":"ok"}`))
				return
			}
			http.Error(w, `{"status":"unavailable"}`, http.StatusServiceUnavailable)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	p, err := NewLlamaCppProvider("local", LLMProviderConfig{BaseURL: server.URL})
	if err != nil {
		t.Fatalf("NewLlamaCppProvider returned error: %v", err)
	}
	provider := p.WithModel("test-model").(*LlamaCppProvider)

	if !provider.IsAvailable() {
		t.Fatal("expected provider to be available when /health returns 200")
	}

	healthy.Store(false)
	if provider.IsAvailable() {
		t.Fatal("expected provider to become unavailable immediately after /health returns 503")
	}
}

func TestLlamaCppStreamMessageAndEmbeddings(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/props":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"total_slots":1,"default_generation_settings":{"n_ctx":8192}}`))
		case "/health":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		case "/v1/chat/completions":
			w.Header().Set("Content-Type", "text/event-stream")
			flusher, ok := w.(http.Flusher)
			if !ok {
				t.Fatalf("response writer does not support flushing")
			}
			_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hello\"},\"finish_reason\":\"\"}]}\n\n")
			flusher.Flush()
			_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":2}}\n\n")
			flusher.Flush()
			_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
			flusher.Flush()
		case "/v1/embeddings":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"index":0,"embedding":[0.5,1.5,2.5]}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	p, err := NewLlamaCppProvider("local", LLMProviderConfig{BaseURL: server.URL})
	if err != nil {
		t.Fatalf("NewLlamaCppProvider returned error: %v", err)
	}
	chatProvider := p.WithModel("test-model").(*LlamaCppProvider)

	ctx := ContextWithSlotOwner(context.Background(), "session:test:main")
	resp, err := chatProvider.StreamMessage(
		ctx,
		[]types.Message{{Role: "user", Content: "hi"}},
		nil,
		"",
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("StreamMessage returned error: %v", err)
	}
	if resp.Text != "hello" {
		t.Fatalf("expected streamed text hello, got %q", resp.Text)
	}
	if resp.InputTokens != 5 || resp.OutputTokens != 2 {
		t.Fatalf("unexpected token usage: input=%d output=%d", resp.InputTokens, resp.OutputTokens)
	}

	embeddingProvider := p.WithEmbeddingModel("embedding-model").(*LlamaCppProvider)
	if !embeddingProvider.IsAvailable() {
		t.Fatal("expected embedding provider to be available after probe")
	}
	vec, err := embeddingProvider.Embed(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Embed returned error: %v", err)
	}
	if len(vec) != 3 {
		t.Fatalf("expected embedding length 3, got %d", len(vec))
	}
	if embeddingProvider.EmbeddingDimensions() != 3 {
		t.Fatalf("expected cached embedding dimensions 3, got %d", embeddingProvider.EmbeddingDimensions())
	}
}

func TestLlamaCppChatRequestInjectsCachePromptAndSlot(t *testing.T) {
	var mu sync.Mutex
	var chatBody []byte
	var embedBody []byte

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/props":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"total_slots":2,"default_generation_settings":{"n_ctx":8192}}`))
		case "/health":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		case "/v1/chat/completions":
			b, _ := io.ReadAll(r.Body)
			mu.Lock()
			chatBody = b
			mu.Unlock()
			w.Header().Set("Content-Type", "text/event-stream")
			flusher, ok := w.(http.Flusher)
			if !ok {
				t.Fatalf("response writer does not support flushing")
			}
			_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"x\"},\"finish_reason\":\"\"}]}\n\n")
			flusher.Flush()
			_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1}}\n\n")
			flusher.Flush()
			_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
			flusher.Flush()
		case "/v1/embeddings":
			b, _ := io.ReadAll(r.Body)
			mu.Lock()
			embedBody = b
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"index":0,"embedding":[1.0]}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	p, err := NewLlamaCppProvider("llama-a", LLMProviderConfig{BaseURL: server.URL})
	if err != nil {
		t.Fatalf("NewLlamaCppProvider: %v", err)
	}
	prov := p.WithModel("m1").(*LlamaCppProvider)
	ctx := ContextWithSlotOwner(context.Background(), "sess-aug-1")
	if _, err := prov.StreamMessage(ctx, []types.Message{{Role: "user", Content: "hi"}}, nil, "", nil, nil); err != nil {
		t.Fatalf("StreamMessage: %v", err)
	}

	mu.Lock()
	raw := append([]byte(nil), chatBody...)
	mu.Unlock()

	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("chat body json: %v", err)
	}
	if payload["cache_prompt"] != true {
		t.Fatalf("expected cache_prompt true, got %v", payload["cache_prompt"])
	}
	if id, ok := payload["id_slot"]; !ok {
		t.Fatal("expected id_slot in chat request")
	} else {
		switch v := id.(type) {
		case float64:
			if int(v) != 0 {
				t.Fatalf("expected id_slot 0, got %v", id)
			}
		default:
			t.Fatalf("unexpected id_slot type %T", id)
		}
	}

	emb := p.WithEmbeddingModel("emb").(*LlamaCppProvider)
	if _, err := emb.Embed(context.Background(), "z"); err != nil {
		t.Fatalf("Embed: %v", err)
	}
	mu.Lock()
	embRaw := append([]byte(nil), embedBody...)
	mu.Unlock()
	var embPayload map[string]any
	if err := json.Unmarshal(embRaw, &embPayload); err != nil {
		t.Fatalf("embed body json: %v", err)
	}
	if _, has := embPayload["cache_prompt"]; has {
		t.Fatalf("embeddings request must not include cache_prompt")
	}
	if _, has := embPayload["id_slot"]; has {
		t.Fatalf("embeddings request must not include id_slot")
	}
}

func TestLlamaCppUnpinnedWhenSlotsExhausted(t *testing.T) {
	var mu sync.Mutex
	var bodies [][]byte

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/props":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"total_slots":1,"default_generation_settings":{"n_ctx":4096}}`))
		case "/health":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		case "/v1/chat/completions":
			b, _ := io.ReadAll(r.Body)
			mu.Lock()
			bodies = append(bodies, b)
			mu.Unlock()
			w.Header().Set("Content-Type", "text/event-stream")
			flusher, ok := w.(http.Flusher)
			if !ok {
				t.Fatalf("response writer does not support flushing")
			}
			_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"y\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1}}\n\n")
			flusher.Flush()
			_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
			flusher.Flush()
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	p, err := NewLlamaCppProvider("llama-b", LLMProviderConfig{BaseURL: server.URL})
	if err != nil {
		t.Fatalf("NewLlamaCppProvider: %v", err)
	}
	prov := p.WithModel("m1").(*LlamaCppProvider)

	ctx1 := ContextWithSlotOwner(context.Background(), "sess-full-1")
	if _, err := prov.StreamMessage(ctx1, []types.Message{{Role: "user", Content: "a"}}, nil, "", nil, nil); err != nil {
		t.Fatalf("first stream: %v", err)
	}
	ctx2 := ContextWithSlotOwner(context.Background(), "sess-full-2")
	if _, err := prov.StreamMessage(ctx2, []types.Message{{Role: "user", Content: "b"}}, nil, "", nil, nil); err != nil {
		t.Fatalf("second stream: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 2 {
		t.Fatalf("expected 2 chat bodies, got %d", len(bodies))
	}
	var second map[string]any
	if err := json.Unmarshal(bodies[1], &second); err != nil {
		t.Fatalf("json: %v", err)
	}
	if second["cache_prompt"] != true {
		t.Fatalf("second request should still set cache_prompt")
	}
	if _, has := second["id_slot"]; has {
		t.Fatalf("exhausted slots: expected id_slot omitted, got %v", second["id_slot"])
	}
}
