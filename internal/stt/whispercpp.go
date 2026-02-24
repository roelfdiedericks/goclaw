package stt

import (
	"fmt"
	"io"
	"strings"

	"github.com/ggerganov/whisper.cpp/bindings/go/pkg/whisper"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// WhisperCppProvider implements STT using whisper.cpp.
type WhisperCppProvider struct {
	model  whisper.Model
	config WhisperCppConfig
}

// WhisperCppConfig holds configuration for Whisper.cpp.
type WhisperCppConfig struct {
	ModelsDir string `json:"modelsDir"` // Directory containing whisper models
	Model     string `json:"model"`     // Model name (e.g., "ggml-base.en.bin")
	Language  string `json:"language"`  // Language code (e.g., "en", "auto" for detection)
	Threads   uint   `json:"threads"`   // Number of threads (0 = auto)
}

// NewWhisperCppProvider creates a new Whisper.cpp STT provider.
func NewWhisperCppProvider(cfg WhisperCppConfig) (*WhisperCppProvider, error) {
	if cfg.ModelsDir == "" {
		return nil, fmt.Errorf("whisper.cpp modelsDir not configured")
	}
	if cfg.Model == "" {
		return nil, fmt.Errorf("whisper.cpp model not configured")
	}

	modelPath := cfg.ModelsDir + "/" + cfg.Model
	L_info("stt: loading whisper.cpp model", "path", modelPath)

	model, err := whisper.New(modelPath)
	if err != nil {
		return nil, fmt.Errorf("load whisper model: %w", err)
	}

	L_info("stt: whisper.cpp model loaded", "multilingual", model.IsMultilingual())

	return &WhisperCppProvider{
		model:  model,
		config: cfg,
	}, nil
}

// Transcribe converts an audio file to text using Whisper.cpp.
func (w *WhisperCppProvider) Transcribe(filePath string) (string, error) {
	L_debug("stt: whisper.cpp transcribing", "file", filePath)

	// Convert audio to 16kHz mono float32 (required by whisper.cpp)
	samples, err := ConvertToFloat32(filePath)
	if err != nil {
		return "", fmt.Errorf("convert audio: %w", err)
	}

	L_debug("stt: audio converted", "samples", len(samples), "duration_sec", float64(len(samples))/16000.0)

	// Create context for this transcription
	ctx, err := w.model.NewContext()
	if err != nil {
		return "", fmt.Errorf("create whisper context: %w", err)
	}

	// Configure context
	if w.config.Language != "" && w.config.Language != "auto" {
		if err := ctx.SetLanguage(w.config.Language); err != nil {
			L_warn("stt: failed to set language", "language", w.config.Language, "error", err)
		}
	} else if w.config.Language == "auto" {
		if err := ctx.SetLanguage("auto"); err != nil {
			L_debug("stt: auto language detection not supported for this model")
		}
	}

	if w.config.Threads > 0 {
		ctx.SetThreads(w.config.Threads)
	}

	// Process audio
	if err := ctx.Process(samples, nil, nil, nil); err != nil {
		return "", fmt.Errorf("whisper process: %w", err)
	}

	// Collect all segments
	var text strings.Builder
	for {
		segment, err := ctx.NextSegment()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("get segment: %w", err)
		}
		text.WriteString(segment.Text)
	}

	result := strings.TrimSpace(text.String())
	L_debug("stt: whisper.cpp transcription complete", "length", len(result))

	return result, nil
}

// Name returns the provider name.
func (w *WhisperCppProvider) Name() string {
	return "whispercpp"
}

// Close releases the whisper model.
func (w *WhisperCppProvider) Close() error {
	L_debug("stt: closing whisper.cpp model")
	return w.model.Close()
}
