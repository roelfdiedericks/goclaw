// Package metadata provides model metadata for LLM configuration and runtime.
package metadata

import (
	_ "embed"
	"encoding/json"
	"sort"
	"strings"
	"sync"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

//go:embed models.json
var embeddedModels []byte

// Manager provides access to model metadata from models.json.
type Manager struct {
	models ModelsData
	mu     sync.RWMutex
}

var (
	instance *Manager
	once     sync.Once
)

// Get returns the singleton metadata manager.
func Get() *Manager {
	once.Do(func() {
		instance = &Manager{}
		instance.load()
	})
	return instance
}

// load loads the embedded models.json data.
func (m *Manager) load() {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := json.Unmarshal(embeddedModels, &m.models); err != nil {
		L_error("metadata: failed to parse embedded models.json", "error", err)
		m.models = ModelsData{Providers: make(map[string]*ModelProvider)}
		return
	}

	totalModels := 0
	for _, p := range m.models.Providers {
		totalModels += len(p.Models)
	}
	L_info("metadata: models loaded", "providers", len(m.models.Providers), "models", totalModels)
}

// --- Provider-level ---

// GetModelProvider returns provider info from models.json.
func (m *Manager) GetModelProvider(providerID string) (*ModelProvider, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	p, ok := m.models.Providers[providerID]
	return p, ok
}

// GetDriver returns the GoClaw driver name for a provider (anthropic/openai/xai/ollama).
// Returns empty string if the provider is not in models.json.
func (m *Manager) GetDriver(providerID string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if p, ok := m.models.Providers[providerID]; ok {
		return p.Driver
	}
	return ""
}

// ResolveProvider resolves a provider config to a models.json provider ID.
// Checks subtype first, then infers from URL, then falls back to driver name.
func (m *Manager) ResolveProvider(subtype, driver, baseURL string) string {
	if subtype != "" {
		return subtype
	}
	if baseURL != "" {
		if id := m.InferProviderByURL(baseURL); id != "" {
			return id
		}
	}
	return driver
}

// InferProviderByURL matches a base URL against known provider endpoints.
// Tries exact match first, then hostname-keyword fallback.
// Returns the provider ID if found, or empty string if no match.
func (m *Manager) InferProviderByURL(baseURL string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	baseURL = strings.TrimSuffix(baseURL, "/")

	// Exact match
	for id, p := range m.models.Providers {
		ep := strings.TrimSuffix(p.APIEndpoint, "/")
		if ep != "" && strings.EqualFold(ep, baseURL) {
			return id
		}
	}

	// Hostname-keyword fallback: extract hostname and match against provider IDs
	// and known domain keywords (e.g. "moonshot" in api.moonshot.ai → kimi)
	lower := strings.ToLower(baseURL)
	for id, p := range m.models.Providers {
		ep := strings.ToLower(p.APIEndpoint)
		if ep == "" {
			continue
		}

		// Extract hostnames for comparison
		idHost := extractHostname(ep)
		urlHost := extractHostname(lower)
		if idHost == "" || urlHost == "" {
			continue
		}

		// Match if hostnames share a significant keyword
		// e.g. api.moonshot.cn and api.moonshot.ai both contain "moonshot"
		idParts := strings.Split(idHost, ".")
		urlParts := strings.Split(urlHost, ".")
		for _, ip := range idParts {
			if len(ip) < 4 || ip == "api" || ip == "com" || ip == "openai" {
				continue
			}
			for _, up := range urlParts {
				if ip == up {
					return id
				}
			}
		}
		_ = p
	}

	return ""
}

func extractHostname(url string) string {
	url = strings.TrimPrefix(url, "https://")
	url = strings.TrimPrefix(url, "http://")
	if idx := strings.Index(url, "/"); idx != -1 {
		url = url[:idx]
	}
	if idx := strings.Index(url, ":"); idx != -1 {
		url = url[:idx]
	}
	return url
}

// ModelProviderIDs returns all provider IDs from models.json, sorted.
func (m *Manager) ModelProviderIDs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	ids := make([]string, 0, len(m.models.Providers))
	for k := range m.models.Providers {
		ids = append(ids, k)
	}
	sort.Strings(ids)
	return ids
}

// --- Model-level ---

// GetModel returns a single model's metadata.
// Performs exact match first, then bidirectional prefix matching:
//   - modelID is a prefix of a metadata ID (claude-opus-4-5 -> claude-opus-4-5-20251101)
//   - a metadata ID is a prefix of modelID (grok-4 -> grok-4-latest)
// On multiple prefix matches, the longest (most specific) metadata ID wins.
func (m *Manager) GetModel(providerID, modelID string) (*Model, bool) {
	_, model, ok := m.ResolveModel(providerID, modelID)
	return model, ok
}

// ResolveModel returns the matched metadata ID, model, and whether a match was found.
// Use this when you need to know which metadata ID was actually matched (e.g. for display).
func (m *Manager) ResolveModel(providerID, modelID string) (matchedID string, model *Model, ok bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	p, pok := m.models.Providers[providerID]
	if !pok {
		return "", nil, false
	}

	if model, ok := p.Models[modelID]; ok {
		return modelID, model, true
	}

	id, model, ok := m.fuzzyMatchModel(p.Models, modelID)
	return id, model, ok
}

// fuzzyMatchModel tries bidirectional prefix matching against the model map.
func (m *Manager) fuzzyMatchModel(models map[string]*Model, modelID string) (string, *Model, bool) {
	var bestID string
	var bestModel *Model

	for id, model := range models {
		matched := false
		if strings.HasPrefix(id, modelID) {
			matched = true
		} else if strings.HasPrefix(modelID, id) {
			matched = true
		}

		if matched {
			if bestModel == nil || len(id) > len(bestID) {
				bestID = id
				bestModel = model
			}
		}
	}

	if bestModel != nil {
		return bestID, bestModel, true
	}
	return "", nil, false
}

// GetModels returns all models for a provider. Returns nil if provider not found.
func (m *Manager) GetModels(providerID string) map[string]*Model {
	m.mu.RLock()
	defer m.mu.RUnlock()

	p, ok := m.models.Providers[providerID]
	if !ok {
		return nil
	}
	return p.Models
}

// --- Capability queries ---

// GetContextWindow returns the context window size for a model, or 0 if unknown.
func (m *Manager) GetContextWindow(providerID, modelID string) int64 {
	if model, ok := m.GetModel(providerID, modelID); ok {
		return model.ContextWindow
	}
	return 0
}

// SupportsVision returns whether a model supports image input.
func (m *Manager) SupportsVision(providerID, modelID string) bool {
	if model, ok := m.GetModel(providerID, modelID); ok {
		return model.Capabilities.Vision
	}
	return false
}

// SupportsReasoning returns whether a model supports extended thinking.
func (m *Manager) SupportsReasoning(providerID, modelID string) bool {
	if model, ok := m.GetModel(providerID, modelID); ok {
		return model.Capabilities.Reasoning
	}
	return false
}

// --- For setup UI ---

// GetKnownChatModels returns sorted model IDs for a provider.
func (m *Manager) GetKnownChatModels(providerID string) []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	p, ok := m.models.Providers[providerID]
	if !ok {
		return nil
	}

	ids := make([]string, 0, len(p.Models))
	for id := range p.Models {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// GetDefaultModels returns the default large and small model IDs for a provider.
func (m *Manager) GetDefaultModels(providerID string) (large, small string) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if p, ok := m.models.Providers[providerID]; ok {
		return p.DefaultLargeModel, p.DefaultSmallModel
	}
	return "", ""
}
