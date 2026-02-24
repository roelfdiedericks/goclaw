package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const catwalkBaseURL = "https://raw.githubusercontent.com/charmbracelet/catwalk/main/internal/providers/configs/"

// fetchAllCatwalk fetches and parses all configured Catwalk provider JSON files.
// Returns a slice of parsed providers (skipping any that fail to fetch/parse).
func fetchAllCatwalk(cacheDir string, refresh, offline bool) ([]*CatwalkProvider, error) {
	cwCacheDir := filepath.Join(cacheDir, "catwalk")
	var providers []*CatwalkProvider
	var errors []string

	for _, p := range catwalkProviders {
		url := catwalkBaseURL + p.Filename + ".json"
		cachePath := filepath.Join(cwCacheDir, p.Filename+".json")

		data, err := fetchCached(url, cachePath, refresh, offline)
		if err != nil {
			errors = append(errors, fmt.Sprintf("%s: %v", p.Filename, err))
			continue
		}
		if data == nil {
			fmt.Fprintf(os.Stderr, "WARN: catwalk %s.json returned 404, skipping\n", p.Filename)
			continue
		}

		var cw CatwalkProvider
		if err := json.Unmarshal(data, &cw); err != nil {
			errors = append(errors, fmt.Sprintf("%s: parse error: %v", p.Filename, err))
			continue
		}

		// Use expected ID if the parsed ID differs from what we expect
		// (e.g. kimi.json has id "kimi-coding")
		if p.ExpectedID != "" && cw.ID != p.ExpectedID {
			fmt.Fprintf(os.Stderr, "INFO: catwalk %s.json has id %q, using expected %q\n",
				p.Filename, cw.ID, p.ExpectedID)
		}

		normalizeCatwalkProvider(&cw)

		providers = append(providers, &cw)
		fmt.Fprintf(os.Stderr, "  catwalk: %s — %d models\n", cw.ID, len(cw.Models))
	}

	if len(errors) > 0 {
		fmt.Fprintf(os.Stderr, "WARN: catwalk fetch errors:\n")
		for _, e := range errors {
			fmt.Fprintf(os.Stderr, "  - %s\n", e)
		}
	}

	if len(providers) == 0 {
		return nil, fmt.Errorf("no catwalk providers fetched successfully")
	}

	return providers, nil
}

// normalizeCatwalkProvider cleans up provider-level fields.
func normalizeCatwalkProvider(p *CatwalkProvider) {
	p.APIKey = strings.TrimPrefix(p.APIKey, "$")

	// Resolve env var references to empty, then apply known defaults
	if strings.HasPrefix(p.APIEndpoint, "$") {
		p.APIEndpoint = ""
	}
	if p.APIEndpoint == "" {
		if ep, ok := defaultEndpoints[p.ID]; ok {
			p.APIEndpoint = ep
		}
	}
}
