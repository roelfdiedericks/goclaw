package stt

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/roelfdiedericks/goclaw/internal/bus"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/paths"
)

// DownloadModel downloads a whisper model to the specified directory.
// Progress is logged via L_info.
func DownloadModel(model *WhisperModel, destDir string) error {
	if model == nil {
		return fmt.Errorf("model is nil")
	}

	// Expand ~ in path
	expandedDir, err := paths.ExpandTilde(destDir)
	if err != nil {
		return fmt.Errorf("expand path: %w", err)
	}

	// Create directory if needed
	if err := os.MkdirAll(expandedDir, 0750); err != nil {
		return fmt.Errorf("create models directory: %w", err)
	}

	destPath := filepath.Join(expandedDir, model.Name)
	tempPath := destPath + ".download"

	L_info("stt: downloading model", "model", model.Name, "size", model.Size, "url", model.URL)

	// Create HTTP request
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", model.URL, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	// Send request
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed: HTTP %d", resp.StatusCode)
	}

	// Get total size from header or use estimate
	totalSize := resp.ContentLength
	if totalSize <= 0 {
		totalSize = model.SizeBytes
	}

	// Create temp file
	tempFile, err := os.Create(tempPath)
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}

	// Download with progress
	var downloaded int64
	lastLog := time.Now()
	buf := make([]byte, 1024*1024) // 1MB buffer

	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			if _, writeErr := tempFile.Write(buf[:n]); writeErr != nil {
				tempFile.Close()
				os.Remove(tempPath)
				return fmt.Errorf("write file: %w", writeErr)
			}
			downloaded += int64(n)

			// Log progress every 2 seconds
			if time.Since(lastLog) > 2*time.Second {
				percent := int(float64(downloaded) / float64(totalSize) * 100)
				downloadedMB := downloaded / (1024 * 1024)
				totalMB := totalSize / (1024 * 1024)
				L_info("stt: downloading", "progress", fmt.Sprintf("%d%%", percent), "downloaded", fmt.Sprintf("%d/%d MB", downloadedMB, totalMB))
				lastLog = time.Now()
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			tempFile.Close()
			os.Remove(tempPath)
			return fmt.Errorf("read response: %w", err)
		}
	}

	tempFile.Close()

	// Rename temp file to final name
	if err := os.Rename(tempPath, destPath); err != nil {
		os.Remove(tempPath)
		return fmt.Errorf("rename file: %w", err)
	}

	L_info("stt: download complete", "model", model.Name, "path", destPath)
	return nil
}

// HandleDownload handles the "download" bus command for STT.
func HandleDownload(cmd bus.Command) bus.CommandResult {
	// Get config from payload
	cfg, ok := cmd.Payload.(*Config)
	if !ok {
		// Try to get from global config
		return bus.CommandResult{
			Success: false,
			Message: "Invalid payload type",
			Error:   fmt.Errorf("expected *stt.Config, got %T", cmd.Payload),
		}
	}

	// Validate provider is whispercpp
	if cfg.Provider != "whispercpp" {
		return bus.CommandResult{
			Success: false,
			Message: "Download only available for Whisper.cpp provider",
			Error:   fmt.Errorf("provider is %s, not whispercpp", cfg.Provider),
		}
	}

	// Validate model is selected
	if cfg.WhisperCpp.Model == "" {
		return bus.CommandResult{
			Success: false,
			Message: "Select a model first",
			Error:   fmt.Errorf("no model selected"),
		}
	}

	// Get model from catalog
	model := GetModel(cfg.WhisperCpp.Model)
	if model == nil {
		return bus.CommandResult{
			Success: false,
			Message: fmt.Sprintf("Unknown model: %s", cfg.WhisperCpp.Model),
			Error:   fmt.Errorf("model not in catalog: %s", cfg.WhisperCpp.Model),
		}
	}

	// Check if already downloaded
	modelsDir := cfg.WhisperCpp.ModelsDir
	if modelsDir == "" {
		modelsDir = "~/.goclaw/stt/whisper"
	}

	expandedDir, err := paths.ExpandTilde(modelsDir)
	if err != nil {
		return bus.CommandResult{
			Success: false,
			Message: fmt.Sprintf("Invalid models directory: %s", modelsDir),
			Error:   err,
		}
	}

	if IsModelDownloaded(expandedDir, model.Name) {
		return bus.CommandResult{
			Success: true,
			Message: fmt.Sprintf("Model %s already downloaded", model.Name),
		}
	}

	// Download the model
	if err := DownloadModel(model, modelsDir); err != nil {
		return bus.CommandResult{
			Success: false,
			Message: fmt.Sprintf("Download failed: %v", err),
			Error:   err,
		}
	}

	return bus.CommandResult{
		Success: true,
		Message: fmt.Sprintf("Model %s downloaded successfully", model.Name),
	}
}

// RegisterCommands registers STT bus commands.
func RegisterCommands() {
	bus.RegisterCommand("stt", "download", HandleDownload)
}
