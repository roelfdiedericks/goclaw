package stt

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/pion/opus/pkg/oggreader"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// GoogleProvider implements STT using Google Cloud Speech-to-Text API.
type GoogleProvider struct {
	config GoogleConfig
	client *http.Client
}

// NewGoogleProvider creates a new Google Cloud STT provider.
func NewGoogleProvider(cfg GoogleConfig) (*GoogleProvider, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("google API key not configured")
	}

	lang := cfg.LanguageCode
	if lang == "" {
		lang = "en-US"
	}

	L_info("stt: google provider initialized", "language", lang)

	return &GoogleProvider{
		config: GoogleConfig{
			APIKey:       cfg.APIKey,
			LanguageCode: lang,
		},
		client: &http.Client{},
	}, nil
}

// getOggSampleRate reads the sample rate from an OGG file header.
// Returns 0 if it cannot be determined.
func getOggSampleRate(filePath string) int {
	file, err := os.Open(filePath)
	if err != nil {
		return 0
	}
	defer file.Close()

	_, header, err := oggreader.NewWith(file)
	if err != nil {
		return 0
	}

	return int(header.SampleRate)
}

// Transcribe converts an audio file to text using Google Cloud Speech-to-Text.
// Google accepts OGG_OPUS directly - no conversion needed!
func (g *GoogleProvider) Transcribe(filePath string) (string, error) {
	L_debug("stt: google transcribing", "file", filePath)

	// Read and base64 encode the audio file
	audioData, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("read audio file: %w", err)
	}

	audioBase64 := base64.StdEncoding.EncodeToString(audioData)

	// Determine encoding from file extension
	ext := strings.ToLower(filepath.Ext(filePath))
	encoding := "OGG_OPUS" // Default for Telegram/WhatsApp voice notes
	sampleRate := 0        // Will be detected from file or defaulted

	if ext == ".ogg" || ext == ".opus" || ext == ".oga" {
		// Read sample rate from OGG header
		sampleRate = getOggSampleRate(filePath)
		if sampleRate == 0 {
			sampleRate = 48000 // Fallback to common rate
		}
	} else if ext == ".wav" {
		encoding = "LINEAR16"
		sampleRate = 16000
	} else if ext == ".mp3" {
		encoding = "MP3"
		// Let Google detect for MP3
	} else if ext == ".flac" {
		encoding = "FLAC"
		// Let Google detect for FLAC
	}

	// Build request body
	config := map[string]interface{}{
		"encoding":                   encoding,
		"languageCode":               g.config.LanguageCode,
		"model":                      "default",
		"enableAutomaticPunctuation": true,
	}
	if sampleRate > 0 {
		config["sampleRateHertz"] = sampleRate
	}

	reqBody := map[string]interface{}{
		"config": config,
		"audio": map[string]interface{}{
			"content": audioBase64,
		},
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	// Create request
	url := fmt.Sprintf("https://speech.googleapis.com/v1/speech:recognize?key=%s", g.config.APIKey)
	req, err := http.NewRequestWithContext(context.Background(), "POST", url, bytes.NewReader(jsonBody))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	L_debug("stt: sending to google", "encoding", encoding, "language", g.config.LanguageCode)

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
		L_error("stt: google request failed", "status", resp.StatusCode, "body", string(body))

		// Try to parse error response
		var errResp struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(body, &errResp) == nil && errResp.Error.Message != "" {
			return "", fmt.Errorf("google API error: %s", errResp.Error.Message)
		}
		return "", fmt.Errorf("google API error: status %d", resp.StatusCode)
	}

	// Parse response
	var result struct {
		Results []struct {
			Alternatives []struct {
				Transcript string  `json:"transcript"`
				Confidence float64 `json:"confidence"`
			} `json:"alternatives"`
		} `json:"results"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}

	// Concatenate all transcripts
	var transcripts []string
	for _, r := range result.Results {
		if len(r.Alternatives) > 0 {
			transcripts = append(transcripts, r.Alternatives[0].Transcript)
		}
	}

	transcript := strings.Join(transcripts, " ")
	L_debug("stt: google transcription complete", "length", len(transcript))

	return transcript, nil
}

// Name returns the provider name.
func (g *GoogleProvider) Name() string {
	return "google"
}

// Close releases any resources (none for HTTP client).
func (g *GoogleProvider) Close() error {
	return nil
}
