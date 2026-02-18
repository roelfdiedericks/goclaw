package setup

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/roelfdiedericks/goclaw/internal/config"
	"github.com/roelfdiedericks/goclaw/internal/config/forms"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/transcript"
)

// RunTranscriptSetupTview runs the transcript configuration using tview
func RunTranscriptSetupTview() error {
	// Load config
	cfg, configPath, err := loadTranscriptConfigTV()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Get form definition
	formDef := transcript.ConfigFormDef(cfg)

	// Render using tview
	result, err := forms.RenderTview(formDef, &cfg, "transcript")
	if err != nil {
		return fmt.Errorf("form error: %w", err)
	}

	// Handle result
	switch result {
	case forms.ResultSaved:
		if err := saveTranscriptConfigTV(cfg, configPath); err != nil {
			return fmt.Errorf("failed to save: %w", err)
		}
		fmt.Println("Configuration saved.")
	case forms.ResultCancelled:
		fmt.Println("Cancelled.")
	}

	return nil
}

// loadTranscriptConfigTV loads the transcript section from goclaw.json
func loadTranscriptConfigTV() (config.TranscriptConfig, string, error) {
	result, err := config.Load()
	if err != nil {
		// Return defaults if no config
		return config.TranscriptConfig{
			Enabled:                true,
			IndexIntervalSeconds:   30,
			BatchSize:              100,
			BackfillBatchSize:      10,
			MaxGroupGapSeconds:     300,
			MaxMessagesPerChunk:    8,
			MaxEmbeddingContentLen: 16000,
			Query: config.TranscriptQueryConfig{
				MaxResults:    10,
				MinScore:      0.3,
				VectorWeight:  0.7,
				KeywordWeight: 0.3,
			},
		}, "", nil
	}

	return result.Config.Transcript, result.SourcePath, nil
}

// saveTranscriptConfigTV saves the transcript config back to goclaw.json
func saveTranscriptConfigTV(cfg config.TranscriptConfig, configPath string) error {
	if configPath == "" {
		return fmt.Errorf("no config file path")
	}

	// Load full config
	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("failed to read config: %w", err)
	}

	var fullConfig map[string]interface{}
	if err := json.Unmarshal(data, &fullConfig); err != nil {
		return fmt.Errorf("failed to parse config: %w", err)
	}

	// Convert transcript config to map
	transcriptData, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("failed to marshal transcript config: %w", err)
	}

	var transcriptMap map[string]interface{}
	if err := json.Unmarshal(transcriptData, &transcriptMap); err != nil {
		return fmt.Errorf("failed to convert transcript config: %w", err)
	}

	// Update transcript section
	fullConfig["transcript"] = transcriptMap

	// Write back
	output, err := json.MarshalIndent(fullConfig, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	if err := os.WriteFile(configPath, output, 0600); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}

	L_info("transcript: config saved", "path", configPath)
	return nil
}
