package main

import (
	"fmt"
	"os"
	"slices"
)

// mergeAll takes parsed Catwalk providers and models.dev supplements,
// applies merge rules, driver mapping, and produces the final ModelsFile.
func mergeAll(cwProviders []*CatwalkProvider, mdModels map[string]*ModelsDevModel) *ModelsFile {
	out := &ModelsFile{
		Generated:       nowISO(),
		ModelsDevBranch: "dev",
		Providers:       make(map[string]*Provider),
	}

	totalModels := 0
	supplemented := 0

	for _, cw := range cwProviders {
		driver, ok := driverMap[cw.ID]
		if !ok {
			fmt.Fprintf(os.Stderr, "ERROR: no driver mapping for provider %q, skipping\n", cw.ID)
			continue
		}

		prov := &Provider{
			Name:              cw.Name,
			APIEndpoint:       cw.APIEndpoint,
			APIKeyEnv:         cw.APIKey,
			CatwalkType:       cw.Type,
			Driver:            driver,
			DefaultLargeModel: cw.DefaultLargeModelID,
			DefaultSmallModel: cw.DefaultSmallModelID,
			Models:            make(map[string]*Model),
		}

		for _, cwm := range cw.Models {
			mdProv, mdModelID := modelsDevLookupKey(cw.ID, cwm.ID)
			var md *ModelsDevModel
			if mdProv != "" && mdModelID != "" {
				md = mdModels[mdProv+"/"+mdModelID]
			}

			model := mergeModel(&cwm, md)
			prov.Models[cwm.ID] = model
			totalModels++
			if md != nil {
				supplemented++
			}
		}

		out.Providers[cw.ID] = prov
	}

	fmt.Fprintf(os.Stderr, "  merged: %d providers, %d models (%d supplemented from models.dev)\n",
		len(out.Providers), totalModels, supplemented)

	return out
}

// mergeModel merges a single Catwalk model with optional models.dev supplement.
func mergeModel(cw *CatwalkModel, md *ModelsDevModel) *Model {
	m := &Model{
		Name:            cw.Name,
		ContextWindow:   cw.ContextWindow,
		MaxOutputTokens: cw.DefaultMaxTokens,
		Cost: Cost{
			Input:      cw.CostPer1MIn,
			Output:     cw.CostPer1MOut,
			CacheRead:  cw.CostPer1MOutCached,
			CacheWrite: cw.CostPer1MInCached,
		},
		Capabilities: Capabilities{
			Vision:                 cw.SupportsAttachmentsBool(),
			ToolUse:                true, // All Catwalk models support tools
			Streaming:              true, // All Catwalk models support streaming
			Reasoning:              cw.CanReasonBool(),
			ReasoningLevels:        cw.ReasoningLevels,
			DefaultReasoningEffort: cw.EffectiveReasoningEffort(),
		},
		Modalities: inferModalities(cw),
	}

	// Supplement from models.dev if available
	if md != nil {
		supplementFromModelsDev(m, md)
	}

	// Ensure modalities are consistent with resolved vision capability
	if m.Capabilities.Vision && !slices.Contains(m.Modalities.Input, "image") {
		m.Modalities.Input = append(m.Modalities.Input, "image")
	}

	return m
}

// inferModalities builds modalities from Catwalk data alone.
func inferModalities(cw *CatwalkModel) Modalities {
	mod := Modalities{
		Output: []string{"text"},
	}
	if cw.SupportsAttachmentsBool() {
		mod.Input = []string{"text", "image"}
	} else {
		mod.Input = []string{"text"}
	}
	return mod
}

// supplementFromModelsDev enriches a model with models.dev data.
// Catwalk remains authoritative for fields it covers well.
// models.dev fills gaps and provides richer detail.
func supplementFromModelsDev(m *Model, md *ModelsDevModel) {
	// Modalities: models.dev is more explicit — prefer it
	if len(md.Modalities.Input) > 0 {
		m.Modalities.Input = md.Modalities.Input
	}
	if len(md.Modalities.Output) > 0 {
		m.Modalities.Output = md.Modalities.Output
	}

	// Vision: true wins on conflict (avoid false negatives)
	mdHasVision := slices.Contains(md.Modalities.Input, "image")
	if mdHasVision && !m.Capabilities.Vision {
		m.Capabilities.Vision = true
	}

	// Cost supplements (fields Catwalk doesn't have)
	if md.Cost.CacheWrite > 0 {
		m.Cost.CacheWrite = md.Cost.CacheWrite
	}
	if md.Cost.Reasoning > 0 {
		m.Cost.Reasoning = md.Cost.Reasoning
	}

	// Tool call: models.dev has an explicit boolean
	if md.ToolCall != nil {
		m.Capabilities.ToolUse = *md.ToolCall
	}

	// Structured output
	if md.StructuredOutput != nil {
		m.Capabilities.StructuredOutput = *md.StructuredOutput
	}

	// MaxOutputTokens: use models.dev if Catwalk value seems like a default
	if md.Limit.Output > 0 && md.Limit.Output > m.MaxOutputTokens {
		m.MaxOutputTokens = md.Limit.Output
	}

	// Metadata: all from models.dev
	m.Metadata.Family = md.Family
	m.Metadata.KnowledgeCutoff = md.Knowledge
	m.Metadata.ReleaseDate = md.ReleaseDate
	m.Metadata.OpenWeights = md.OpenWeights
}
