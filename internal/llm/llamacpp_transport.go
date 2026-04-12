package llm

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// LlamaCppChatAugmentTransport mutates OpenAI-compatible chat completion JSON for
// llama-server (cache_prompt, id_slot). It should sit outside CapturingTransport in
// the client chain so capture/reasoning stay llama-agnostic.
type LlamaCppChatAugmentTransport struct {
	Next http.RoundTripper

	mu sync.Mutex

	armed       bool
	cachePrompt bool
	idSlot      int // < 0 means omit id_slot
}

// RoundTrip reads the request body, applies llama fields when armed for
// chat/completions, then delegates to Next.
func (t *LlamaCppChatAugmentTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	next := t.Next
	if next == nil {
		next = http.DefaultTransport
	}

	var reqBody []byte
	if req.Body != nil {
		reqBody, _ = io.ReadAll(req.Body)
		_ = req.Body.Close()
		req.Body = io.NopCloser(bytes.NewReader(reqBody))
	}

	t.mu.Lock()
	armed := t.armed
	cache := t.cachePrompt
	slot := t.idSlot
	t.armed = false
	t.cachePrompt = false
	t.idSlot = -1
	t.mu.Unlock()

	if armed && strings.Contains(req.URL.Path, "chat/completions") && len(reqBody) > 0 {
		reqBody = injectLlamaCppChatFields(reqBody, cache, slot)
		req.Body = io.NopCloser(bytes.NewReader(reqBody))
		req.ContentLength = int64(len(reqBody))
	}

	return next.RoundTrip(req)
}

// SetLlamaCppChatAugment arms the next chat/completions request with llama-server fields.
// idSlot < 0 omits id_slot (unpinned). Single-shot: cleared after RoundTrip.
func (t *LlamaCppChatAugmentTransport) SetLlamaCppChatAugment(cachePrompt bool, idSlot int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.armed = true
	t.cachePrompt = cachePrompt
	t.idSlot = idSlot
}

func injectLlamaCppChatFields(body []byte, cachePrompt bool, idSlot int) []byte {
	var data map[string]interface{}
	if err := json.Unmarshal(body, &data); err != nil {
		L_debug("llamacpp transport: failed to parse body for injection", "error", err)
		return body
	}
	if cachePrompt {
		data["cache_prompt"] = true
	}
	if idSlot >= 0 {
		data["id_slot"] = idSlot
	}
	modified, err := json.Marshal(data)
	if err != nil {
		L_debug("llamacpp transport: failed to marshal augmented body", "error", err)
		return body
	}
	L_trace("llamacpp transport: injected chat fields",
		"cachePrompt", cachePrompt,
		"idSlot", idSlot,
		"originalLen", len(body),
		"modifiedLen", len(modified),
	)
	return modified
}
