// metamerge fetches model metadata from Catwalk and models.dev,
// merges them, and outputs internal/metadata/models.json.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
)

func main() {
	outPath := flag.String("out", "internal/metadata/models.json", "output file path")
	cacheDir := flag.String("cache-dir", "internal/metadata/.cache", "cache directory for downloaded sources")
	refresh := flag.Bool("refresh", false, "force re-fetch from remotes, ignore cache")
	offline := flag.Bool("offline", false, "use cache only, fail if missing")
	format := flag.Bool("format", true, "pretty-print JSON output")
	flag.Parse()

	fmt.Fprintln(os.Stderr, "metamerge: fetching catwalk providers...")
	cwProviders, err := fetchAllCatwalk(*cacheDir, *refresh, *offline)
	if err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: %v\n", err)
		os.Exit(1)
	}

	injectSyntheticProviders(&cwProviders)

	fmt.Fprintln(os.Stderr, "metamerge: fetching models.dev supplements...")
	mdModels := fetchModelsDevForProviders(cwProviders, *cacheDir, *refresh, *offline)

	fmt.Fprintln(os.Stderr, "metamerge: merging...")
	result := mergeAll(cwProviders, mdModels)

	var data []byte
	if *format {
		data, err = json.MarshalIndent(result, "", "  ")
	} else {
		data, err = json.Marshal(result)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: marshal: %v\n", err)
		os.Exit(1)
	}
	data = append(data, '\n')

	if err := os.WriteFile(*outPath, data, 0600); err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: write %s: %v\n", *outPath, err)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "metamerge: wrote %s (%d bytes)\n", *outPath, len(data))
}

// injectSyntheticProviders adds providers that don't come from Catwalk:
// Kimi (OpenAI variant), Ollama, and LM Studio.
func injectSyntheticProviders(cwProviders *[]*CatwalkProvider) {
	// Find kimi-coding to clone its models for the OpenAI variant
	var kimiModels []CatwalkModel
	for _, p := range *cwProviders {
		if p.ID == "kimi-coding" {
			p.Name = "Kimi Coding (Anthropic)"
			kimiModels = make([]CatwalkModel, len(p.Models))
			copy(kimiModels, p.Models)
			break
		}
	}

	syntheticProviders := []*CatwalkProvider{
		{
			ID:                  "kimi",
			Name:                "Kimi (OpenAI)",
			Type:                "openai-compat",
			APIKey:              "KIMI_API_KEY",
			APIEndpoint:         "https://api.moonshot.cn/v1",
			DefaultLargeModelID: "kimi-for-coding",
			DefaultSmallModelID: "kimi-for-coding",
			Models:              kimiModels,
		},
		{
			ID:                  "ollama",
			Name:                "Ollama (Local)",
			Type:                "ollama",
			APIEndpoint:         "http://localhost:11434",
			DefaultLargeModelID: "",
			DefaultSmallModelID: "",
			Models:              nil,
		},
		{
			ID:                  "lmstudio",
			Name:                "LM Studio (Local)",
			Type:                "openai-compat",
			APIEndpoint:         "http://localhost:1234/v1",
			DefaultLargeModelID: "",
			DefaultSmallModelID: "",
			Models:              nil,
		},
	}

	for _, sp := range syntheticProviders {
		*cwProviders = append(*cwProviders, sp)
		fmt.Fprintf(os.Stderr, "  synthetic: %s — %d models\n", sp.ID, len(sp.Models))
	}
}

// fetchModelsDevForProviders collects all models.dev lookup keys from the
// Catwalk providers and fetches them in a concurrent batch.
func fetchModelsDevForProviders(cwProviders []*CatwalkProvider, cacheDir string, refresh, offline bool) map[string]*ModelsDevModel {
	var lookups []struct{ Provider, ModelID string }

	for _, cw := range cwProviders {
		for _, m := range cw.Models {
			mdProv, mdModelID := modelsDevLookupKey(cw.ID, m.ID)
			if mdProv != "" && mdModelID != "" {
				lookups = append(lookups, struct{ Provider, ModelID string }{mdProv, mdModelID})
			}
		}
	}

	fmt.Fprintf(os.Stderr, "  models.dev: %d lookups to perform\n", len(lookups))
	return fetchAllModelsDevBatch(lookups, cacheDir, refresh, offline)
}
