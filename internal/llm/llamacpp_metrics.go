package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
	. "github.com/roelfdiedericks/goclaw/internal/metrics"
)

const (
	llamaTimingsMaxTokens        = 1 << 30
	llamaPrometheusScrapeTimeout = 2 * time.Second
)

var llamaTimingsMissingLogged sync.Map // key: provider|model → suppress repeated L_debug

// parseLastLlamaTimingsFromSSE scans OpenAI-style SSE text for the last JSON object
// that contains a non-null timings object (llama-server chat completions).
func parseLastLlamaTimingsFromSSE(sseBody []byte) (cacheN, promptN int64, ok bool) {
	sc := bufio.NewScanner(bytes.NewReader(sseBody))
	buf := make([]byte, 0, 64*1024)
	sc.Buffer(buf, 8*1024*1024)

	var lastCache, lastPrompt int64
	found := false

	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		var root map[string]json.RawMessage
		if err := json.Unmarshal([]byte(payload), &root); err != nil {
			continue
		}
		rawT, hasT := root["timings"]
		if !hasT || len(rawT) == 0 || string(rawT) == "null" {
			continue
		}
		var timings struct {
			CacheN  float64 `json:"cache_n"`
			PromptN float64 `json:"prompt_n"`
		}
		if err := json.Unmarshal(rawT, &timings); err != nil {
			continue
		}
		lastCache = clampFloatToInt64Timings(timings.CacheN)
		lastPrompt = clampFloatToInt64Timings(timings.PromptN)
		found = true
	}
	return lastCache, lastPrompt, found
}

func clampFloatToInt64Timings(f float64) int64 {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0
	}
	if f <= 0 {
		return 0
	}
	if f >= float64(llamaTimingsMaxTokens) {
		return llamaTimingsMaxTokens
	}
	return int64(math.Round(f))
}

func emitLlamaCppTimingsMetrics(metricPrefix string, cacheN, promptN int64) {
	if metricPrefix == "" {
		return
	}
	MetricAdd(metricPrefix, "kv_reuse_tokens", cacheN)
	MetricAdd(metricPrefix, "kv_prompt_eval_tokens", promptN)
	if cacheN > 0 {
		MetricHit(metricPrefix, "kv_prompt_reuse")
	} else {
		MetricMiss(metricPrefix, "kv_prompt_reuse")
	}
}

func maybeLogLlamaTimingsMissing(provider, model string) {
	key := provider + "|" + model
	if _, loaded := llamaTimingsMissingLogged.LoadOrStore(key, struct{}{}); loaded {
		return
	}
	L_debug("llamacpp: no timings in stream capture", "provider", provider, "model", model)
}

// metricBaseFromPrometheusToken strips optional llamacpp: / llamacpp_ prefix and label set.
func metricBaseFromPrometheusToken(token string) string {
	if i := strings.IndexByte(token, '{'); i >= 0 {
		token = token[:i]
	}
	token = strings.TrimPrefix(token, "llamacpp:")
	token = strings.TrimPrefix(token, "llamacpp_")
	return token
}

// parsePrometheusSampleLine parses a single exposition line into metric id (with labels) and value.
func parsePrometheusSampleLine(line string) (metricID string, value float64, ok bool) {
	line = strings.TrimSpace(line)
	if line == "" || line[0] == '#' {
		return "", 0, false
	}
	parts := strings.Fields(line)
	if len(parts) < 2 {
		return "", 0, false
	}
	metricID = parts[0]
	// Prometheus text: name value [timestamp]. Value is always the second field for lines we accept.
	valStr := parts[1]
	v, err := strconv.ParseFloat(valStr, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return "", 0, false
	}
	return metricID, v, true
}

// applyLlamaServerPrometheusText parses llama-server /metrics body and emits MetricSet for v1 series.
func applyLlamaServerPrometheusText(metricPrefix, body string, provider string) {
	if metricPrefix == "" {
		return
	}
	seen := make(map[string]bool)
	for _, line := range strings.Split(body, "\n") {
		metricID, v, ok := parsePrometheusSampleLine(line)
		if !ok {
			continue
		}
		base := metricBaseFromPrometheusToken(metricID)
		if seen[base] {
			L_debug("llamacpp: duplicate prometheus sample for metric", "metric", base, "provider", provider)
			continue
		}
		seen[base] = true

		switch base {
		case "kv_cache_tokens":
			MetricSet(metricPrefix, "server_kv_cache_tokens", clampFloatToInt64Gauge(v))
		case "kv_cache_usage_ratio":
			// Leaf server_kv_cache_usage_bps: fixed-point ratio*10^4 (not finance bps); 10000 ~= 100% KV full.
			MetricSet(metricPrefix, "server_kv_cache_usage_bps", int64(math.Round(v*10_000)))
		case "requests_processing":
			MetricSet(metricPrefix, "server_requests_processing", clampFloatToInt64Gauge(v))
		case "requests_deferred":
			MetricSet(metricPrefix, "server_requests_deferred", clampFloatToInt64Gauge(v))
		case "n_tokens_max":
			MetricSet(metricPrefix, "server_n_tokens_max", clampFloatToInt64Gauge(v))
		}
	}
}

func clampFloatToInt64Gauge(f float64) int64 {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0
	}
	if f <= 0 {
		return 0
	}
	if f >= float64(llamaTimingsMaxTokens) {
		return llamaTimingsMaxTokens
	}
	return int64(math.Round(f))
}

func scrapeHostForLog(serverRoot string) string {
	u, err := url.Parse(serverRoot)
	if err != nil || u.Host == "" {
		return serverRoot
	}
	return u.Host
}

// goLlamaCppScrapeServerMetrics fetches serverRoot/metrics in a goroutine (plan: non-blocking, dedicated client).
func goLlamaCppScrapeServerMetrics(metricPrefix, serverRoot, provider string) {
	if metricPrefix == "" || serverRoot == "" {
		return
	}
	go func(metricPrefix, serverRoot, provider string) {
		ctx, cancel := context.WithTimeout(context.Background(), llamaPrometheusScrapeTimeout)
		defer cancel()
		u := strings.TrimRight(serverRoot, "/") + "/metrics"
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			L_warn("llamacpp: metrics scrape request build failed", "provider", provider, "host", scrapeHostForLog(serverRoot), "error", err)
			return
		}
		client := &http.Client{Transport: http.DefaultTransport, Timeout: llamaPrometheusScrapeTimeout}
		resp, err := client.Do(req)
		if err != nil {
			L_warn("llamacpp: metrics scrape failed", "provider", provider, "host", scrapeHostForLog(serverRoot), "error", err)
			return
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		if err != nil {
			L_warn("llamacpp: metrics scrape read failed", "provider", provider, "host", scrapeHostForLog(serverRoot), "error", err)
			return
		}
		host := scrapeHostForLog(serverRoot)
		text := string(body)

		switch resp.StatusCode {
		case http.StatusOK:
			break
		case http.StatusNotImplemented: // 501 from llama-server when --metrics off
			L_debug("llamacpp: metrics endpoint disabled", "provider", provider, "host", host, "status", resp.StatusCode)
			return
		case http.StatusNotFound:
			L_debug("llamacpp: metrics not found", "provider", provider, "host", host, "status", resp.StatusCode)
			return
		default:
			L_warn("llamacpp: metrics scrape unexpected status", "provider", provider, "host", host, "status", resp.StatusCode)
			return
		}

		trim := strings.TrimSpace(text)
		if strings.HasPrefix(trim, "{") && strings.Contains(trim, `"error"`) {
			L_debug("llamacpp: metrics body is json error payload", "provider", provider, "host", host)
			return
		}

		applyLlamaServerPrometheusText(metricPrefix, text, provider)
	}(metricPrefix, serverRoot, provider)
}
