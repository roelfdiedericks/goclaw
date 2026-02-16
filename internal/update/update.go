// Package update provides self-update functionality for GoClaw.
package update

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

const (
	// GitHubRepo is the repository path
	GitHubRepo = "roelfdiedericks/goclaw"

	// GitHubAPIBase is the GitHub API base URL
	GitHubAPIBase = "https://api.github.com"

	// GitHubReleasesBase is the GitHub releases download base URL
	GitHubReleasesBase = "https://github.com"
)

// Release represents a GitHub release
type Release struct {
	TagName    string    `json:"tag_name"`
	Name       string    `json:"name"`
	Body       string    `json:"body"`
	Prerelease bool      `json:"prerelease"`
	Draft      bool      `json:"draft"`
	CreatedAt  time.Time `json:"created_at"`
	Assets     []Asset   `json:"assets"`
}

// Asset represents a release asset
type Asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}

// Version extracts the clean version from the tag (removes 'v' prefix)
func (r *Release) Version() string {
	return strings.TrimPrefix(r.TagName, "v")
}

// Channel returns the release channel (stable or beta/rc)
func (r *Release) Channel() string {
	if r.Prerelease {
		// Extract channel from tag like v0.2.0-beta.1 -> beta
		if strings.Contains(r.TagName, "-beta") {
			return "beta"
		}
		if strings.Contains(r.TagName, "-rc") {
			return "rc"
		}
		return "prerelease"
	}
	return "stable"
}

// UpdateInfo contains information about an available update
type UpdateInfo struct {
	CurrentVersion string
	NewVersion     string
	Channel        string
	Changelog      string
	DownloadURL    string
	ChecksumURL    string
	IsNewer        bool
}

// Updater handles the update process
type Updater struct {
	currentVersion string
	httpClient     *http.Client
}

// NewUpdater creates a new Updater with the current version
func NewUpdater(currentVersion string) *Updater {
	return &Updater{
		currentVersion: currentVersion,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// IsSystemManaged checks if GoClaw is installed in a system-managed location
func IsSystemManaged() bool {
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	exe, _ = filepath.EvalSymlinks(exe)

	// Check for common system paths
	systemPaths := []string{"/usr/bin/", "/usr/local/bin/", "/opt/"}
	for _, prefix := range systemPaths {
		if strings.HasPrefix(exe, prefix) {
			return true
		}
	}
	return false
}

// GetExecutablePath returns the path to the current executable
func GetExecutablePath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("failed to get executable path: %w", err)
	}
	return filepath.EvalSymlinks(exe)
}

// CheckForUpdate checks if a newer version is available
func (u *Updater) CheckForUpdate(channel string) (*UpdateInfo, error) {
	L_debug("update: checking for updates", "channel", channel, "current", u.currentVersion)

	release, err := u.getLatestRelease(channel)
	if err != nil {
		return nil, fmt.Errorf("failed to check for updates: %w", err)
	}

	newVersion := release.Version()
	isNewer := isNewerVersion(u.currentVersion, newVersion)

	// Build download URLs
	archiveName := fmt.Sprintf("goclaw_%s_%s_%s.tar.gz", newVersion, runtime.GOOS, runtime.GOARCH)
	downloadURL := fmt.Sprintf("%s/%s/releases/download/%s/%s",
		GitHubReleasesBase, GitHubRepo, release.TagName, archiveName)
	checksumURL := fmt.Sprintf("%s/%s/releases/download/%s/checksums.txt",
		GitHubReleasesBase, GitHubRepo, release.TagName)

	return &UpdateInfo{
		CurrentVersion: u.currentVersion,
		NewVersion:     newVersion,
		Channel:        release.Channel(),
		Changelog:      release.Body,
		DownloadURL:    downloadURL,
		ChecksumURL:    checksumURL,
		IsNewer:        isNewer,
	}, nil
}

// getLatestRelease fetches the latest release for a channel
func (u *Updater) getLatestRelease(channel string) (*Release, error) {
	if channel == "stable" {
		// Use /releases/latest for stable
		url := fmt.Sprintf("%s/repos/%s/releases/latest", GitHubAPIBase, GitHubRepo)
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/vnd.github.v3+json")
		req.Header.Set("User-Agent", "goclaw-updater")

		resp, err := u.httpClient.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()

		if resp.StatusCode == http.StatusNotFound {
			return nil, fmt.Errorf("no stable releases found")
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("GitHub API returned %d", resp.StatusCode)
		}

		var release Release
		if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
			return nil, err
		}
		return &release, nil
	}

	// For other channels (beta, rc), fetch all releases and filter
	url := fmt.Sprintf("%s/repos/%s/releases", GitHubAPIBase, GitHubRepo)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", "goclaw-updater")

	resp, err := u.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned %d", resp.StatusCode)
	}

	var releases []Release
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return nil, err
	}

	// Find latest matching release
	for _, r := range releases {
		if r.Draft {
			continue
		}
		if channel == "beta" && strings.Contains(r.TagName, "-beta") {
			return &r, nil
		}
		if channel == "rc" && strings.Contains(r.TagName, "-rc") {
			return &r, nil
		}
	}

	return nil, fmt.Errorf("no %s releases found", channel)
}

// Download downloads the release archive and verifies its checksum
func (u *Updater) Download(info *UpdateInfo, onProgress func(downloaded, total int64)) (string, error) {
	L_debug("update: downloading", "url", info.DownloadURL)

	// Create temp directory
	tmpDir, err := os.MkdirTemp("", "goclaw-update-*")
	if err != nil {
		return "", fmt.Errorf("failed to create temp directory: %w", err)
	}

	// Download archive
	archivePath := filepath.Join(tmpDir, "goclaw.tar.gz")
	if err := u.downloadFile(info.DownloadURL, archivePath, onProgress); err != nil {
		os.RemoveAll(tmpDir)
		return "", fmt.Errorf("failed to download: %w", err)
	}

	// Download and verify checksum
	if err := u.verifyChecksum(archivePath, info.ChecksumURL); err != nil {
		os.RemoveAll(tmpDir)
		return "", fmt.Errorf("checksum verification failed: %w", err)
	}

	L_debug("update: checksum verified")

	// Extract archive
	binaryPath := filepath.Join(tmpDir, "goclaw")
	if err := extractTarGz(archivePath, tmpDir); err != nil {
		os.RemoveAll(tmpDir)
		return "", fmt.Errorf("failed to extract: %w", err)
	}

	// Verify binary exists
	if _, err := os.Stat(binaryPath); err != nil {
		os.RemoveAll(tmpDir)
		return "", fmt.Errorf("binary not found in archive")
	}

	return binaryPath, nil
}

// downloadFile downloads a file from URL to dest
func (u *Updater) downloadFile(url, dest string, onProgress func(downloaded, total int64)) error {
	resp, err := u.httpClient.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer out.Close()

	if onProgress != nil {
		// Wrap with progress tracking
		var downloaded int64
		buf := make([]byte, 32*1024)
		for {
			n, err := resp.Body.Read(buf)
			if n > 0 {
				if _, werr := out.Write(buf[:n]); werr != nil {
					return werr
				}
				downloaded += int64(n)
				onProgress(downloaded, resp.ContentLength)
			}
			if err == io.EOF {
				break
			}
			if err != nil {
				return err
			}
		}
	} else {
		if _, err := io.Copy(out, resp.Body); err != nil {
			return err
		}
	}

	return nil
}

// verifyChecksum verifies the file's SHA256 checksum
func (u *Updater) verifyChecksum(filePath, checksumURL string) error {
	// Download checksums.txt
	resp, err := u.httpClient.Get(checksumURL)
	if err != nil {
		return fmt.Errorf("failed to download checksums: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("checksums HTTP %d", resp.StatusCode)
	}

	checksumData, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	// Parse checksums (format: "hash  filename")
	fileName := filepath.Base(filePath)
	fileName = "goclaw.tar.gz" // We renamed it, need original name
	archiveName := fmt.Sprintf("goclaw_%s_%s_%s.tar.gz", "", runtime.GOOS, runtime.GOARCH)

	var expectedHash string
	for _, line := range strings.Split(string(checksumData), "\n") {
		parts := strings.Fields(line)
		if len(parts) >= 2 {
			// Match by suffix since we don't know exact version in filename
			if strings.HasSuffix(parts[1], fmt.Sprintf("_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH)) {
				expectedHash = parts[0]
				archiveName = parts[1]
				break
			}
		}
	}

	if expectedHash == "" {
		return fmt.Errorf("checksum not found for %s_%s", runtime.GOOS, runtime.GOARCH)
	}

	// Calculate actual hash
	f, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	actualHash := hex.EncodeToString(h.Sum(nil))

	if actualHash != expectedHash {
		return fmt.Errorf("checksum mismatch for %s: expected %s, got %s",
			archiveName, expectedHash[:16]+"...", actualHash[:16]+"...")
	}

	return nil
}

// Apply replaces the current binary with the new one
func (u *Updater) Apply(newBinaryPath string, noRestart bool) error {
	currentExe, err := GetExecutablePath()
	if err != nil {
		return err
	}

	L_info("update: applying", "current", currentExe, "new", newBinaryPath)

	// Make new binary executable
	if err := os.Chmod(newBinaryPath, 0755); err != nil {
		return fmt.Errorf("failed to make binary executable: %w", err)
	}

	// Backup current binary
	backupPath := currentExe + ".old"
	if err := os.Rename(currentExe, backupPath); err != nil {
		return fmt.Errorf("failed to backup current binary: %w", err)
	}

	// Move new binary into place
	if err := copyFile(newBinaryPath, currentExe); err != nil {
		// Try to restore backup
		os.Rename(backupPath, currentExe)
		return fmt.Errorf("failed to install new binary: %w", err)
	}

	// Clean up backup
	os.Remove(backupPath)

	L_info("update: binary replaced successfully")

	if noRestart {
		L_info("update: skipping restart (--no-restart)")
		return nil
	}

	// Restart via exec (replaces current process)
	return restart(currentExe)
}

// restart replaces the current process with a new instance
func restart(exePath string) error {
	L_info("update: restarting", "exe", exePath)

	// Get current args (skip the first one which is the executable path)
	args := os.Args

	// Use syscall.Exec to replace current process
	return syscall.Exec(exePath, args, os.Environ())
}

// copyFile copies src to dst
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}

	return out.Close()
}

// isNewerVersion compares two semver versions
func isNewerVersion(current, new string) bool {
	// Strip 'v' prefix if present
	current = strings.TrimPrefix(current, "v")
	new = strings.TrimPrefix(new, "v")

	// Simple comparison - split by dots and compare
	currentParts := strings.Split(current, ".")
	newParts := strings.Split(new, ".")

	for i := 0; i < 3; i++ {
		var c, n int
		if i < len(currentParts) {
			// Handle suffixes like "0-beta.1"
			part := strings.Split(currentParts[i], "-")[0]
			fmt.Sscanf(part, "%d", &c)
		}
		if i < len(newParts) {
			part := strings.Split(newParts[i], "-")[0]
			fmt.Sscanf(part, "%d", &n)
		}

		if n > c {
			return true
		}
		if n < c {
			return false
		}
	}

	// Same version, check prerelease (stable > beta > rc)
	currentPre := strings.Contains(current, "-")
	newPre := strings.Contains(new, "-")

	if currentPre && !newPre {
		return true // new is stable, current is prerelease
	}

	return false
}
