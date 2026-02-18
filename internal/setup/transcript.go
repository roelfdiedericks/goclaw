package setup

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/charmbracelet/huh"
	"github.com/roelfdiedericks/goclaw/internal/actions"
	"github.com/roelfdiedericks/goclaw/internal/config"
	"github.com/roelfdiedericks/goclaw/internal/config/forms"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// TranscriptEditor manages transcript configuration editing
type TranscriptEditor struct {
	cfg        config.TranscriptConfig
	configPath string
	modified   bool
}

// RunTranscriptSetup runs the transcript configuration editor
func RunTranscriptSetup() error {
	// Suppress non-error logs during TUI
	prevLevel := suppressLogs()
	defer restoreLogs(prevLevel)

	// Start persistent session
	StartSession(FrameTitleSetup)
	defer EndSession()

	// Load config
	cfg, configPath, err := loadTranscriptConfig()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	editor := &TranscriptEditor{
		cfg:        cfg,
		configPath: configPath,
	}

	return editor.mainMenu()
}

// mainMenu displays the main transcript config menu
func (e *TranscriptEditor) mainMenu() error {
	for {
		// Build menu options with current values
		options := []huh.Option[string]{
			huh.NewOption(fmt.Sprintf("Indexing              [%s]", e.enabledStatus()), "toggle"),
			huh.NewOption(fmt.Sprintf("Timing Settings       [Interval: %ds, Gap: %ds]", e.cfg.IndexIntervalSeconds, e.cfg.MaxGroupGapSeconds), "timing"),
			huh.NewOption(fmt.Sprintf("Batch Settings        [Size: %d, Msgs: %d]", e.cfg.BatchSize, e.cfg.MaxMessagesPerChunk), "batching"),
			huh.NewOption(fmt.Sprintf("Search Settings       [Max: %d, Score: %.2f]", e.cfg.Query.MaxResults, e.cfg.Query.MinScore), "search"),
			huh.NewOption("───────────────────────────────────", "---1"),
			huh.NewOption("Test Connection", "test"),
			huh.NewOption("Apply to Running Service", "apply"),
			huh.NewOption("Show Stats", "stats"),
			huh.NewOption("───────────────────────────────────", "---2"),
			huh.NewOption("Save and Exit", "save"),
			huh.NewOption("Exit without Saving", "exit"),
		}

		var choice string
		if err := RunMenu(FrameTitleSetup, "Transcript", options, &choice); err != nil {
			if isUserAbort(err) {
				return e.confirmExit()
			}
			return err
		}

		switch choice {
		case "toggle":
			// Direct toggle - no separate screen needed
			e.cfg.Enabled = !e.cfg.Enabled
			e.modified = true
		case "timing":
			if err := e.editTiming(); err != nil && !isUserAbort(err) {
				return err
			}
		case "batching":
			if err := e.editBatching(); err != nil && !isUserAbort(err) {
				return err
			}
		case "search":
			if err := e.editSearch(); err != nil && !isUserAbort(err) {
				return err
			}
		case "test":
			e.doTest()
		case "apply":
			e.doApply()
		case "stats":
			e.doStats()
		case "save":
			if err := e.save(); err != nil {
				e.showMessage("Error", err.Error())
			} else {
				e.showMessage("Saved", "Configuration saved successfully")
				return nil
			}
		case "exit":
			return e.confirmExit()
		case "---1", "---2":
			// Separator, do nothing
		}
	}
}

func (e *TranscriptEditor) enabledStatus() string {
	if e.cfg.Enabled {
		return "Enabled"
	}
	return "Disabled"
}

// editTiming edits timing-related indexing settings
func (e *TranscriptEditor) editTiming() error {
	indexInterval := fmt.Sprintf("%d", e.cfg.IndexIntervalSeconds)
	maxGap := fmt.Sprintf("%d", e.cfg.MaxGroupGapSeconds)

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewNote().
				Title("Timing Settings").
				Description("Control indexing frequency. Use ↑/↓ to navigate."),
			huh.NewInput().
				Title("Index Interval (seconds)").
				Description("How often to check for new messages (min: 5)").
				Value(&indexInterval),
			huh.NewInput().
				Title("Max Group Gap (seconds)").
				Description("Max time gap between messages in a conversation chunk.\n300 = 5 minutes of silence starts new chunk").
				Value(&maxGap),
		),
	).WithKeyMap(formKeyMap()).WithShowHelp(true).WithTheme(blueTheme())

	if err := RunForm(FrameTitleSetup, form); err != nil {
		return err
	}

	changed := false
	if v, err := parseInt(indexInterval); err == nil && v != e.cfg.IndexIntervalSeconds {
		e.cfg.IndexIntervalSeconds = v
		changed = true
	}
	if v, err := parseInt(maxGap); err == nil && v != e.cfg.MaxGroupGapSeconds {
		e.cfg.MaxGroupGapSeconds = v
		changed = true
	}
	if changed {
		e.modified = true
	}
	return nil
}

// editBatching edits batch-related indexing settings
func (e *TranscriptEditor) editBatching() error {
	batchSize := fmt.Sprintf("%d", e.cfg.BatchSize)
	backfillBatch := fmt.Sprintf("%d", e.cfg.BackfillBatchSize)
	maxMessages := fmt.Sprintf("%d", e.cfg.MaxMessagesPerChunk)
	maxEmbedLen := fmt.Sprintf("%d", e.cfg.MaxEmbeddingContentLen)

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewNote().
				Title("Batch Settings").
				Description("Control chunk sizes. Use ↑/↓ to navigate."),
			huh.NewInput().
				Title("Batch Size").
				Description("Max messages to process per indexing run").
				Value(&batchSize),
			huh.NewInput().
				Title("Backfill Batch Size").
				Description("Max chunks to backfill per interval").
				Value(&backfillBatch),
			huh.NewInput().
				Title("Max Messages Per Chunk").
				Description("Maximum messages grouped into one searchable chunk").
				Value(&maxMessages),
			huh.NewInput().
				Title("Max Embedding Length").
				Description("Max characters to embed per chunk (for vector search)").
				Value(&maxEmbedLen),
		),
	).WithKeyMap(formKeyMap()).WithShowHelp(true).WithTheme(blueTheme())

	if err := RunForm(FrameTitleSetup, form); err != nil {
		return err
	}

	changed := false
	if v, err := parseInt(batchSize); err == nil && v != e.cfg.BatchSize {
		e.cfg.BatchSize = v
		changed = true
	}
	if v, err := parseInt(backfillBatch); err == nil && v != e.cfg.BackfillBatchSize {
		e.cfg.BackfillBatchSize = v
		changed = true
	}
	if v, err := parseInt(maxMessages); err == nil && v != e.cfg.MaxMessagesPerChunk {
		e.cfg.MaxMessagesPerChunk = v
		changed = true
	}
	if v, err := parseInt(maxEmbedLen); err == nil && v != e.cfg.MaxEmbeddingContentLen {
		e.cfg.MaxEmbeddingContentLen = v
		changed = true
	}
	if changed {
		e.modified = true
	}
	return nil
}

// editSearch edits search settings
func (e *TranscriptEditor) editSearch() error {
	maxResults := fmt.Sprintf("%d", e.cfg.Query.MaxResults)
	minScore := fmt.Sprintf("%.2f", e.cfg.Query.MinScore)
	vectorWeight := fmt.Sprintf("%.2f", e.cfg.Query.VectorWeight)
	keywordWeight := fmt.Sprintf("%.2f", e.cfg.Query.KeywordWeight)

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewNote().
				Title("Search Settings").
				Description("Configure transcript search behavior. Use ↑/↓ to navigate."),
			huh.NewInput().
				Title("Max Results").
				Description("Maximum number of search results to return").
				Value(&maxResults),
			huh.NewInput().
				Title("Min Score").
				Description("Minimum similarity threshold (0.0 - 1.0)").
				Value(&minScore),
			huh.NewInput().
				Title("Vector Weight").
				Description("Weight for semantic/vector search (0.0 - 1.0)").
				Value(&vectorWeight),
			huh.NewInput().
				Title("Keyword Weight").
				Description("Weight for keyword/FTS search (0.0 - 1.0)").
				Value(&keywordWeight),
		),
	).WithKeyMap(formKeyMap()).WithShowHelp(true).WithTheme(blueTheme())

	if err := RunForm(FrameTitleSetup, form); err != nil {
		return err
	}

	// Parse and update
	changed := false
	if v, err := parseInt(maxResults); err == nil && v != e.cfg.Query.MaxResults {
		e.cfg.Query.MaxResults = v
		changed = true
	}
	if v, err := parseFloat(minScore); err == nil && v != e.cfg.Query.MinScore {
		e.cfg.Query.MinScore = v
		changed = true
	}
	if v, err := parseFloat(vectorWeight); err == nil && v != e.cfg.Query.VectorWeight {
		e.cfg.Query.VectorWeight = v
		changed = true
	}
	if v, err := parseFloat(keywordWeight); err == nil && v != e.cfg.Query.KeywordWeight {
		e.cfg.Query.KeywordWeight = v
		changed = true
	}

	if changed {
		e.modified = true
	}
	return nil
}

// doTest tests the connection
func (e *TranscriptEditor) doTest() {
	result := actions.Send("transcript", "test", nil)
	if result.Error != nil {
		e.showMessage("Test Failed", result.Message)
	} else {
		e.showMessage("Test Passed", result.Message)
	}
}

// doApply applies config to running service
func (e *TranscriptEditor) doApply() {
	result := actions.Send("transcript", "apply", e.cfg)
	if result.Error != nil {
		e.showMessage("Apply Failed", result.Message)
	} else {
		e.showMessage("Applied", result.Message)
	}
}

// doStats shows indexing stats
func (e *TranscriptEditor) doStats() {
	result := actions.Send("transcript", "stats", nil)
	if result.Error != nil {
		e.showMessage("Stats Failed", result.Message)
	} else {
		e.showMessage("Statistics", result.Message)
	}
}

// save saves the configuration
func (e *TranscriptEditor) save() error {
	return saveTranscriptConfig(e.cfg, e.configPath)
}

// confirmExit handles exit with potential unsaved changes
func (e *TranscriptEditor) confirmExit() error {
	if !e.modified {
		return nil
	}

	var save bool
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title("Unsaved Changes").
				Description("You have unsaved changes. Save before exiting?").
				Affirmative("Save").
				Negative("Discard").
				Value(&save),
		),
	).WithTheme(blueTheme())

	if err := RunForm(FrameTitleSetup, form); err != nil {
		return nil // User aborted, just exit
	}

	if save {
		return e.save()
	}
	return nil
}

// showMessage displays a message and waits for acknowledgment
func (e *TranscriptEditor) showMessage(title, msg string) {
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewNote().
				Title(title).
				Description(msg + "\n\nPress Enter to continue"),
		),
	).WithTheme(blueTheme())

	_ = RunForm(FrameTitleSetup, form)
}

// Helper functions

func parseInt(s string) (int, error) {
	var v int
	_, err := fmt.Sscanf(s, "%d", &v)
	return v, err
}

func parseFloat(s string) (float64, error) {
	var v float64
	_, err := fmt.Sscanf(s, "%f", &v)
	return v, err
}

// loadTranscriptConfig loads the transcript section from goclaw.json
func loadTranscriptConfig() (config.TranscriptConfig, string, error) {
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

// saveTranscriptConfig saves the transcript config back to goclaw.json
func saveTranscriptConfig(cfg config.TranscriptConfig, configPath string) error {
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

// Ensure forms package is imported (for future use)
var _ = forms.FormDef{}
