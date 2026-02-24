package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/BurntSushi/toml"
)

const modelsDevBaseURL = "https://raw.githubusercontent.com/anomalyco/models.dev/dev/providers/"

// modelsDevLookupKey resolves a Catwalk provider ID + model ID into the
// models.dev provider directory and model filename for fetching.
// Returns ("", "") if no models.dev lookup is possible.
func modelsDevLookupKey(catwalkProviderID, modelID string) (mdProvider, mdModelID string) {
	// OpenRouter: model IDs are "provider/model" — split and use origin
	if catwalkProviderID == "openrouter" {
		parts := strings.SplitN(modelID, "/", 2)
		if len(parts) == 2 {
			return parts[0], parts[1]
		}
		return "", ""
	}

	mdProvider, ok := modelsDevProviderMap[catwalkProviderID]
	if !ok || mdProvider == "" {
		return "", ""
	}

	return mdProvider, modelID
}

func modelsDevURL(mdProvider, mdModelID string) string {
	return modelsDevBaseURL + mdProvider + "/models/" + mdModelID + ".toml"
}

func modelsDevCachePath(cacheDir, mdProvider, mdModelID string) string {
	return filepath.Join(cacheDir, "models.dev", mdProvider, mdModelID+".toml")
}

// fetchModelsDevModel fetches and parses a single models.dev TOML file.
// Returns nil if the model doesn't exist in models.dev (404).
func fetchModelsDevModel(mdProvider, mdModelID, cacheDir string, refresh, offline bool) *ModelsDevModel {
	url := modelsDevURL(mdProvider, mdModelID)
	cachePath := modelsDevCachePath(cacheDir, mdProvider, mdModelID)

	data, err := fetchCached(url, cachePath, refresh, offline)
	if err != nil {
		fmt.Fprintf(os.Stderr, "WARN: models.dev fetch %s/%s: %v\n", mdProvider, mdModelID, err)
		return nil
	}
	if data == nil {
		return nil
	}

	var m ModelsDevModel
	if err := toml.Unmarshal(data, &m); err != nil {
		fmt.Fprintf(os.Stderr, "WARN: models.dev parse %s/%s: %v\n", mdProvider, mdModelID, err)
		return nil
	}

	return &m
}

// modelsDevResult holds a lookup result for concurrent fetching.
type modelsDevResult struct {
	Key   string // "provider/model"
	Model *ModelsDevModel
}

// fetchAllModelsDevBatch concurrently fetches models.dev data for a batch of lookups.
// Each lookup is a (mdProvider, mdModelID) pair. Results are deduped by key.
func fetchAllModelsDevBatch(lookups []struct{ Provider, ModelID string }, cacheDir string, refresh, offline bool) map[string]*ModelsDevModel {
	type lookupKey struct{ Provider, ModelID string }

	seen := make(map[string]bool)
	var unique []lookupKey

	for _, l := range lookups {
		key := l.Provider + "/" + l.ModelID
		if !seen[key] {
			seen[key] = true
			unique = append(unique, lookupKey{l.Provider, l.ModelID})
		}
	}

	results := make(map[string]*ModelsDevModel)
	var mu sync.Mutex
	var wg sync.WaitGroup

	sem := make(chan struct{}, 10)

	for _, l := range unique {
		wg.Add(1)
		go func(p, m string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			model := fetchModelsDevModel(p, m, cacheDir, refresh, offline)
			if model != nil {
				mu.Lock()
				results[p+"/"+m] = model
				mu.Unlock()
			}
		}(l.Provider, l.ModelID)
	}

	wg.Wait()
	return results
}
