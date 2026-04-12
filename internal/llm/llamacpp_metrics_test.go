package llm

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/roelfdiedericks/goclaw/internal/types"
)

func TestParseLastLlamaTimingsFromSSE(t *testing.T) {
	sse := "" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"x\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"timings\":{\"cache_n\":12,\"prompt_n\":3}}\n\n" +
		"data: [DONE]\n\n"
	cacheN, promptN, ok := parseLastLlamaTimingsFromSSE([]byte(sse))
	if !ok {
		t.Fatal("expected timings found")
	}
	if cacheN != 12 || promptN != 3 {
		t.Fatalf("got cacheN=%d promptN=%d", cacheN, promptN)
	}
}

func TestParseLastLlamaTimingsFromSSE_lastWins(t *testing.T) {
	sse := "" +
		"data: {\"timings\":{\"cache_n\":1,\"prompt_n\":1}}\n\n" +
		"data: {\"timings\":{\"cache_n\":9,\"prompt_n\":2}}\n\n"
	cacheN, promptN, ok := parseLastLlamaTimingsFromSSE([]byte(sse))
	if !ok {
		t.Fatal("expected timings found")
	}
	if cacheN != 9 || promptN != 2 {
		t.Fatalf("expected last object, got cacheN=%d promptN=%d", cacheN, promptN)
	}
}

func TestMetricBaseFromPrometheusToken(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"llamacpp:kv_cache_tokens", "kv_cache_tokens"},
		{"llamacpp_kv_cache_tokens", "kv_cache_tokens"},
		{`llamacpp:kv_cache_tokens{slot="0"}`, "kv_cache_tokens"},
	}
	for _, tc := range tests {
		got := metricBaseFromPrometheusToken(tc.in)
		if got != tc.want {
			t.Errorf("%q: got %q want %q", tc.in, got, tc.want)
		}
	}
}

func TestParsePrometheusSampleLine(t *testing.T) {
	id, v, ok := parsePrometheusSampleLine("llamacpp:kv_cache_tokens 42")
	if !ok || id != "llamacpp:kv_cache_tokens" || v != 42 {
		t.Fatalf("got %q %v %v", id, v, ok)
	}
	id, v, ok = parsePrometheusSampleLine(`llamacpp:kv_cache_usage_ratio 0.25`)
	if !ok || v != 0.25 {
		t.Fatalf("ratio: got %q %v %v", id, v, ok)
	}
	id, v, ok = parsePrometheusSampleLine("# HELP x")
	if ok {
		t.Fatal("expected comment to fail")
	}
}

func TestApplyLlamaServerPrometheusText_duplicateEmitsDebugPath(t *testing.T) {
	body := "llamacpp:kv_cache_tokens 1\nllamacpp:kv_cache_tokens 99\n"
	// Should not panic; first value wins.
	applyLlamaServerPrometheusText("llm/llamacpp/test/model", body, "test")
}

func TestLlamaCppStreamMessageScrapesMetricsAndParsesTimings(t *testing.T) {
	var scrapeCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/props":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"total_slots":2,"default_generation_settings":{"n_ctx":8192}}`))
		case "/health":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		case "/v1/chat/completions":
			w.Header().Set("Content-Type", "text/event-stream")
			flusher, ok := w.(http.Flusher)
			if !ok {
				t.Fatalf("response writer does not support flushing")
			}
			_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"},\"finish_reason\":\"\"}]}\n\n")
			flusher.Flush()
			_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":1},\"timings\":{\"cache_n\":4,\"prompt_n\":1}}\n\n")
			flusher.Flush()
			_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
			flusher.Flush()
		case "/metrics":
			scrapeCount.Add(1)
			w.Header().Set("Content-Type", "text/plain; version=0.0.4")
			_, _ = w.Write([]byte("llamacpp:kv_cache_tokens 7\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	p, err := NewLlamaCppProvider("local", LLMProviderConfig{BaseURL: server.URL})
	if err != nil {
		t.Fatalf("NewLlamaCppProvider: %v", err)
	}
	prov := p.WithModel("m1").(*LlamaCppProvider)

	ctx := ContextWithSlotOwner(context.Background(), "session:test:main")
	_, err = prov.StreamMessage(ctx, []types.Message{{Role: "user", Content: "hi"}}, nil, "", nil, nil)
	if err != nil {
		t.Fatalf("StreamMessage: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for scrapeCount.Load() < 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if scrapeCount.Load() < 1 {
		t.Fatalf("expected /metrics scrape, got count %d", scrapeCount.Load())
	}
}
