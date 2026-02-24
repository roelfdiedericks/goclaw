package stt

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// GroqProvider implements STT using Groq's Whisper API.
type GroqProvider struct {
	config GroqConfig
	client *http.Client
}

// NewGroqProvider creates a new Groq Whisper STT provider.
func NewGroqProvider(cfg GroqConfig) (*GroqProvider, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("groq API key not configured")
	}

	model := cfg.Model
	if model == "" {
		model = "whisper-large-v3"
	}

	L_info("stt: groq provider initialized", "model", model)

	return &GroqProvider{
		config: GroqConfig{
			APIKey: cfg.APIKey,
			Model:  model,
		},
		client: &http.Client{},
	}, nil
}

// Transcribe converts an audio file to text using Groq's Whisper API.
// Groq accepts OGG/Opus directly - no conversion needed!
func (g *GroqProvider) Transcribe(filePath string) (string, error) {
	L_debug("stt: groq transcribing", "file", filePath)

	// Open the audio file
	file, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("open audio file: %w", err)
	}
	defer file.Close()

	// Create multipart form
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	// Add the file
	filename := filepath.Base(filePath)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return "", fmt.Errorf("create form file: %w", err)
	}
	if _, err := io.Copy(part, file); err != nil {
		return "", fmt.Errorf("copy file to form: %w", err)
	}

	// Add model field
	if err := writer.WriteField("model", g.config.Model); err != nil {
		return "", fmt.Errorf("write model field: %w", err)
	}

	// Add response format
	if err := writer.WriteField("response_format", "text"); err != nil {
		return "", fmt.Errorf("write response_format field: %w", err)
	}

	if err := writer.Close(); err != nil {
		return "", fmt.Errorf("close multipart writer: %w", err)
	}

	// Create request - Groq uses a different endpoint
	req, err := http.NewRequestWithContext(context.Background(), "POST", "https://api.groq.com/openai/v1/audio/transcriptions", &buf)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+g.config.APIKey)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	L_debug("stt: sending to groq", "url", req.URL.String(), "model", g.config.Model)

	// Send request
	resp, err := g.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	// Read response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		L_error("stt: groq request failed", "status", resp.StatusCode, "body", string(body))

		// Try to parse error response
		var errResp struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(body, &errResp) == nil && errResp.Error.Message != "" {
			return "", fmt.Errorf("groq API error: %s", errResp.Error.Message)
		}
		return "", fmt.Errorf("groq API error: status %d", resp.StatusCode)
	}

	// Response is plain text when response_format=text
	result := string(body)
	L_debug("stt: groq transcription complete", "length", len(result))

	return result, nil
}

// Name returns the provider name.
func (g *GroqProvider) Name() string {
	return "groq"
}

// Close releases any resources (none for HTTP client).
func (g *GroqProvider) Close() error {
	return nil
}
