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

// OpenAIProvider implements STT using OpenAI's Whisper API.
type OpenAIProvider struct {
	config OpenAIConfig
	client *http.Client
}

// NewOpenAIProvider creates a new OpenAI Whisper STT provider.
func NewOpenAIProvider(cfg OpenAIConfig) (*OpenAIProvider, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("openai API key not configured")
	}

	model := cfg.Model
	if model == "" {
		model = "whisper-1"
	}

	L_info("stt: openai provider initialized", "model", model)

	return &OpenAIProvider{
		config: OpenAIConfig{
			APIKey: cfg.APIKey,
			Model:  model,
		},
		client: &http.Client{},
	}, nil
}

// Transcribe converts an audio file to text using OpenAI's Whisper API.
// OpenAI accepts OGG/Opus directly - no conversion needed!
func (o *OpenAIProvider) Transcribe(filePath string) (string, error) {
	L_debug("stt: openai transcribing", "file", filePath)

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
	if err := writer.WriteField("model", o.config.Model); err != nil {
		return "", fmt.Errorf("write model field: %w", err)
	}

	// Add response format
	if err := writer.WriteField("response_format", "text"); err != nil {
		return "", fmt.Errorf("write response_format field: %w", err)
	}

	if err := writer.Close(); err != nil {
		return "", fmt.Errorf("close multipart writer: %w", err)
	}

	// Create request
	req, err := http.NewRequestWithContext(context.Background(), "POST", "https://api.openai.com/v1/audio/transcriptions", &buf)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+o.config.APIKey)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	L_debug("stt: sending to openai", "url", req.URL.String(), "contentType", writer.FormDataContentType())

	// Send request
	resp, err := o.client.Do(req)
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
		L_error("stt: openai request failed", "status", resp.StatusCode, "body", string(body))

		// Try to parse error response
		var errResp struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(body, &errResp) == nil && errResp.Error.Message != "" {
			return "", fmt.Errorf("openai API error: %s", errResp.Error.Message)
		}
		return "", fmt.Errorf("openai API error: status %d", resp.StatusCode)
	}

	// Response is plain text when response_format=text
	result := string(body)
	L_debug("stt: openai transcription complete", "length", len(result))

	return result, nil
}

// Name returns the provider name.
func (o *OpenAIProvider) Name() string {
	return "openai"
}

// Close releases any resources (none for HTTP client).
func (o *OpenAIProvider) Close() error {
	return nil
}
