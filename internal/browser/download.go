package browser

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/go-rod/rod/lib/launcher"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// Downloader handles Chromium binary management
type Downloader struct {
	binDir   string
	revision string
	mu       sync.Mutex
	binPath  string // Cached path to binary once downloaded
}

// NewDownloader creates a new Chromium downloader
func NewDownloader(binDir, revision string) *Downloader {
	return &Downloader{
		binDir:   binDir,
		revision: revision,
	}
}

// EnsureBrowser ensures Chromium is downloaded and returns the path to the binary.
// This is safe to call concurrently.
func (d *Downloader) EnsureBrowser() (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	// Already have a cached path?
	if d.binPath != "" {
		if _, err := os.Stat(d.binPath); err == nil {
			return d.binPath, nil
		}
		// Binary was removed, need to re-download
		d.binPath = ""
	}

	// Ensure bin directory exists
	if err := os.MkdirAll(d.binDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create browser bin directory: %w", err)
	}

	L_debug("browser: ensuring browser is available", "binDir", d.binDir, "revision", d.revision)

	// Create browser downloader
	b := launcher.NewBrowser()
	b.RootDir = d.binDir

	// Set revision if specified (note: Revision is an int in go-rod)
	// For now, use default revision
	L_debug("browser: using default revision")

	// Download if needed (this is a no-op if already downloaded)
	binPath, err := b.Get()
	if err != nil {
		return "", fmt.Errorf("failed to download browser: %w", err)
	}

	d.binPath = binPath
	L_info("browser: ready", "path", binPath)

	return binPath, nil
}

// GetBinPath returns the cached binary path, or empty if not downloaded yet
func (d *Downloader) GetBinPath() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.binPath
}

// IsDownloaded returns true if the browser has been downloaded
func (d *Downloader) IsDownloaded() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	
	if d.binPath == "" {
		return false
	}
	
	_, err := os.Stat(d.binPath)
	return err == nil
}

// ForceDownload downloads the browser even if it already exists
func (d *Downloader) ForceDownload() (string, error) {
	d.mu.Lock()
	d.binPath = "" // Clear cache to force re-download
	d.mu.Unlock()
	
	return d.EnsureBrowser()
}

// GetRevision returns the current revision string
func (d *Downloader) GetRevision() string {
	return d.revision
}

// GetBinDir returns the binary directory
func (d *Downloader) GetBinDir() string {
	return d.binDir
}

// FindExistingBrowser looks for an existing browser binary in the bin directory
func (d *Downloader) FindExistingBrowser() (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	// Check if we have a cached path
	if d.binPath != "" {
		if _, err := os.Stat(d.binPath); err == nil {
			return d.binPath, nil
		}
	}

	// Look for chromium directories in binDir
	entries, err := os.ReadDir(d.binDir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("browser not downloaded: bin directory does not exist")
		}
		return "", fmt.Errorf("failed to read bin directory: %w", err)
	}

	// Look for chromium-* directories
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		
		// Try common binary locations
		candidates := []string{
			filepath.Join(d.binDir, entry.Name(), "chrome"),
			filepath.Join(d.binDir, entry.Name(), "chrome.exe"),
			filepath.Join(d.binDir, entry.Name(), "Chromium.app", "Contents", "MacOS", "Chromium"),
		}
		
		for _, candidate := range candidates {
			if _, err := os.Stat(candidate); err == nil {
				d.binPath = candidate
				return candidate, nil
			}
		}
	}

	return "", fmt.Errorf("browser not downloaded: no chromium binary found in %s", d.binDir)
}
