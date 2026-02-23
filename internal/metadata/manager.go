// Package metadata provides provider metadata for the LLM configuration UI.
package metadata

import (
	_ "embed"
	"encoding/json"
	"os"
	"sync"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/paths"
)

//go:embed providers.json
var embeddedProviders []byte

// ProviderMeta contains metadata about an LLM provider type.
type ProviderMeta struct {
	Name         string        `json:"name"`
	Description  string        `json:"description,omitempty"`
	SignupURL    string        `json:"signupURL,omitempty"`
	Instructions string        `json:"instructions,omitempty"`
	DefaultURL   string        `json:"defaultURL,omitempty"` // For Ollama
	Subtypes     []SubtypeMeta `json:"subtypes"`
}

// SubtypeMeta contains metadata about a provider subtype (e.g., OpenRouter for openai type).
type SubtypeMeta struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Description    string `json:"description,omitempty"`
	DefaultBaseURL string `json:"defaultBaseURL,omitempty"`
	SignupURL      string `json:"signupURL,omitempty"`
	RequiresAPIKey bool   `json:"requiresAPIKey"`
}

// ProvidersData is the root structure of providers.json.
type ProvidersData struct {
	Providers map[string]ProviderMeta `json:"providers"`
}

// Manager provides access to provider metadata.
type Manager struct {
	data ProvidersData
	mu   sync.RWMutex
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

// load loads metadata from local file or embedded fallback.
func (m *Manager) load() {
	m.mu.Lock()
	defer m.mu.Unlock()

	localPath := m.localPath()

	// Try loading from local file first
	if data, err := os.ReadFile(localPath); err == nil {
		if err := json.Unmarshal(data, &m.data); err == nil {
			L_debug("metadata: loaded from local file", "path", localPath)
			return
		}
		L_warn("metadata: failed to parse local file, using embedded", "path", localPath, "error", err)
	}

	// Fall back to embedded
	if err := json.Unmarshal(embeddedProviders, &m.data); err != nil {
		L_error("metadata: failed to parse embedded providers.json", "error", err)
		m.data = ProvidersData{Providers: make(map[string]ProviderMeta)}
		return
	}
	L_debug("metadata: loaded from embedded")

	// Bootstrap: write to local if it doesn't exist
	m.bootstrap(localPath)
}

// bootstrap writes the embedded metadata to the local path if it doesn't exist.
func (m *Manager) bootstrap(localPath string) {
	if _, err := os.Stat(localPath); err == nil {
		return // Already exists
	}

	if err := paths.EnsureParentDir(localPath); err != nil {
		L_warn("metadata: failed to create directory", "path", localPath, "error", err)
		return
	}

	if err := os.WriteFile(localPath, embeddedProviders, 0644); err != nil {
		L_warn("metadata: failed to write local file", "path", localPath, "error", err)
		return
	}

	L_info("metadata: bootstrapped local file", "path", localPath)
}

// localPath returns the path to the local metadata file.
func (m *Manager) localPath() string {
	p, _ := paths.DataPath("metadata/providers.json")
	return p
}

// GetProvider returns metadata for a provider type.
func (m *Manager) GetProvider(providerType string) (ProviderMeta, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	p, ok := m.data.Providers[providerType]
	return p, ok
}

// GetAllProviders returns all provider metadata.
func (m *Manager) GetAllProviders() map[string]ProviderMeta {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[string]ProviderMeta, len(m.data.Providers))
	for k, v := range m.data.Providers {
		result[k] = v
	}
	return result
}

// GetSubtype returns metadata for a specific subtype of a provider.
func (m *Manager) GetSubtype(providerType, subtypeID string) (SubtypeMeta, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	provider, ok := m.data.Providers[providerType]
	if !ok {
		return SubtypeMeta{}, false
	}

	for _, st := range provider.Subtypes {
		if st.ID == subtypeID {
			return st, true
		}
	}
	return SubtypeMeta{}, false
}

// ProviderTypes returns the list of available provider type IDs.
func (m *Manager) ProviderTypes() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	types := make([]string, 0, len(m.data.Providers))
	for k := range m.data.Providers {
		types = append(types, k)
	}
	return types
}
