package stt

import (
	"os"
	"path/filepath"
)

// WhisperModel represents an available whisper.cpp model.
type WhisperModel struct {
	Name      string // Filename: "ggml-tiny.en.bin"
	Label     string // Display name: "Tiny English"
	Size      string // Human readable: "39 MB"
	SizeBytes int64  // For progress calculation
	URL       string // Download URL
}

// WhisperModels is the catalog of available whisper.cpp models.
// Models from: https://huggingface.co/ggerganov/whisper.cpp
var WhisperModels = []WhisperModel{
	{
		Name:      "ggml-tiny.en.bin",
		Label:     "Tiny English",
		Size:      "39 MB",
		SizeBytes: 39_000_000,
		URL:       "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-tiny.en.bin",
	},
	{
		Name:      "ggml-tiny.bin",
		Label:     "Tiny Multilingual",
		Size:      "39 MB",
		SizeBytes: 39_000_000,
		URL:       "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-tiny.bin",
	},
	{
		Name:      "ggml-base.en.bin",
		Label:     "Base English",
		Size:      "142 MB",
		SizeBytes: 142_000_000,
		URL:       "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-base.en.bin",
	},
	{
		Name:      "ggml-base.bin",
		Label:     "Base Multilingual",
		Size:      "142 MB",
		SizeBytes: 142_000_000,
		URL:       "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-base.bin",
	},
	{
		Name:      "ggml-small.en.bin",
		Label:     "Small English",
		Size:      "466 MB",
		SizeBytes: 466_000_000,
		URL:       "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-small.en.bin",
	},
	{
		Name:      "ggml-small.bin",
		Label:     "Small Multilingual",
		Size:      "466 MB",
		SizeBytes: 466_000_000,
		URL:       "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-small.bin",
	},
	{
		Name:      "ggml-medium.bin",
		Label:     "Medium Multilingual",
		Size:      "1.5 GB",
		SizeBytes: 1_500_000_000,
		URL:       "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-medium.bin",
	},
	{
		Name:      "ggml-large-v3.bin",
		Label:     "Large V3 Multilingual",
		Size:      "3.0 GB",
		SizeBytes: 3_000_000_000,
		URL:       "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-large-v3.bin",
	},
}

// GetModel returns the model with the given name, or nil if not found.
func GetModel(name string) *WhisperModel {
	for i := range WhisperModels {
		if WhisperModels[i].Name == name {
			return &WhisperModels[i]
		}
	}
	return nil
}

// IsModelDownloaded checks if a model file exists in the given directory.
func IsModelDownloaded(modelsDir, name string) bool {
	if modelsDir == "" || name == "" {
		return false
	}
	path := filepath.Join(modelsDir, name)
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir() && info.Size() > 0
}

// GetModelOptions returns form options for the model dropdown.
// Downloaded models are marked with a checkmark.
func GetModelOptions(modelsDir string) []ModelOption {
	options := make([]ModelOption, 0, len(WhisperModels)+1)

	// Add "none selected" option
	options = append(options, ModelOption{
		Label:      "(select a model)",
		Value:      "",
		Downloaded: false,
	})

	for _, m := range WhisperModels {
		downloaded := IsModelDownloaded(modelsDir, m.Name)
		label := m.Label + " (" + m.Size + ")"
		if downloaded {
			label = "✓ " + label
		}
		options = append(options, ModelOption{
			Label:      label,
			Value:      m.Name,
			Downloaded: downloaded,
		})
	}

	return options
}

// ModelOption represents a dropdown option for model selection.
type ModelOption struct {
	Label      string
	Value      string
	Downloaded bool
}
