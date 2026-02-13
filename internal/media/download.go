package media

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	tele "gopkg.in/telebot.v4"
)

// DownloadTimeout is the maximum time to wait for a file download
const DownloadTimeout = 30 * time.Second

// DownloadFromTelegram downloads a file from Telegram using the bot API.
// The file parameter should be a telebot.File with a valid FileID.
// Context is used for cancellation; timeout is handled by DownloadTimeout.
func DownloadFromTelegram(ctx context.Context, bot *tele.Bot, file *tele.File) ([]byte, error) {
	if file == nil || file.FileID == "" {
		return nil, fmt.Errorf("invalid file: missing FileID")
	}

	// Get file info (including download URL)
	fileInfo, err := bot.FileByID(file.FileID)
	if err != nil {
		return nil, fmt.Errorf("failed to get file info: %w", err)
	}

	// Build download URL
	downloadURL := fmt.Sprintf("https://api.telegram.org/file/bot%s/%s",
		bot.Token, fileInfo.FilePath)

	// Create request with context
	req, err := http.NewRequestWithContext(ctx, "GET", downloadURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Download the file
	client := &http.Client{Timeout: DownloadTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to download file: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download failed with status: %d", resp.StatusCode)
	}

	// Read the file content
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read file content: %w", err)
	}

	return data, nil
}

// DownloadPhoto downloads the largest available photo size from a Telegram photo array.
// Telegram sends photos in multiple sizes; we want the largest one.
func DownloadPhoto(ctx context.Context, bot *tele.Bot, photo *tele.Photo) ([]byte, error) {
	if photo == nil || photo.FileID == "" {
		return nil, fmt.Errorf("invalid photo: missing FileID")
	}

	// Photo is already a File, use it directly
	return DownloadFromTelegram(ctx, bot, &photo.File)
}

// DownloadAndOptimize downloads an image from Telegram and optimizes it for the LLM.
func DownloadAndOptimize(ctx context.Context, bot *tele.Bot, photo *tele.Photo) (*ImageData, error) {
	// Download the photo
	data, err := DownloadPhoto(ctx, bot, photo)
	if err != nil {
		return nil, fmt.Errorf("failed to download photo: %w", err)
	}

	// Optimize for Anthropic limits
	optimized, err := Optimize(data)
	if err != nil {
		return nil, fmt.Errorf("failed to optimize photo: %w", err)
	}

	return optimized, nil
}
