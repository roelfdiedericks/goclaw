// Package update provides self-update functionality for GoClaw.
package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
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
	ArchiveName    string // e.g., "goclaw_0.1.0_linux_amd64.tar.gz"
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
		L_debug("update: failed to get executable path", "error", err)
		return false
	}
	exe, _ = filepath.EvalSymlinks(exe)

	// Check for common system paths
	systemPaths := []string{"/usr/bin/", "/usr/local/bin/", "/opt/"}
	for _, prefix := range systemPaths {
		if strings.HasPrefix(exe, prefix) {
			L_debug("update: system-managed installation detected", "path", exe, "prefix", prefix)
			return true
		}
	}
	L_debug("update: user installation detected", "path", exe)
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
	L_info("update: checking for updates",
		"channel", channel,
		"current", u.currentVersion,
		"os", runtime.GOOS,
		"arch", runtime.GOARCH,
	)

	release, err := u.getLatestRelease(channel)
	if err != nil {
		return nil, fmt.Errorf("failed to check for updates: %w", err)
	}

	newVersion := release.Version()
	isNewer := isNewerVersion(u.currentVersion, newVersion)

	L_info("update: version check complete",
		"current", u.currentVersion,
		"latest", newVersion,
		"channel", release.Channel(),
		"isNewer", isNewer,
		"tag", release.TagName,
	)

	// Build download URLs
	archiveName := fmt.Sprintf("goclaw_%s_%s_%s.tar.gz", newVersion, runtime.GOOS, runtime.GOARCH)
	downloadURL := fmt.Sprintf("%s/%s/releases/download/%s/%s",
		GitHubReleasesBase, GitHubRepo, release.TagName, archiveName)
	checksumURL := fmt.Sprintf("%s/%s/releases/download/%s/checksums.txt",
		GitHubReleasesBase, GitHubRepo, release.TagName)

	L_debug("update: download URLs prepared",
		"archive", archiveName,
		"downloadURL", downloadURL,
		"checksumURL", checksumURL,
	)

	return &UpdateInfo{
		CurrentVersion: u.currentVersion,
		NewVersion:     newVersion,
		Channel:        release.Channel(),
		Changelog:      release.Body,
		ArchiveName:    archiveName,
		DownloadURL:    downloadURL,
		ChecksumURL:    checksumURL,
		IsNewer:        isNewer,
	}, nil
}

// getLatestRelease fetches the latest release for a channel
func (u *Updater) getLatestRelease(channel string) (*Release, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if channel == "stable" {
		// Use /releases/latest for stable
		url := fmt.Sprintf("%s/repos/%s/releases/latest", GitHubAPIBase, GitHubRepo)
		L_debug("update: fetching latest stable release", "url", url)

		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/vnd.github.v3+json")
		req.Header.Set("User-Agent", "goclaw-updater")

		resp, err := u.httpClient.Do(req)
		if err != nil {
			L_debug("update: GitHub API request failed", "error", err)
			return nil, err
		}
		defer resp.Body.Close()

		L_debug("update: GitHub API response", "status", resp.StatusCode)

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

		L_debug("update: found stable release",
			"tag", release.TagName,
			"name", release.Name,
			"assets", len(release.Assets),
		)
		return &release, nil
	}

	// For other channels (beta, rc), fetch all releases and filter
	url := fmt.Sprintf("%s/repos/%s/releases", GitHubAPIBase, GitHubRepo)
	L_debug("update: fetching releases list", "url", url, "channel", channel)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", "goclaw-updater")

	resp, err := u.httpClient.Do(req)
	if err != nil {
		L_debug("update: GitHub API request failed", "error", err)
		return nil, err
	}
	defer resp.Body.Close()

	L_debug("update: GitHub API response", "status", resp.StatusCode)

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned %d", resp.StatusCode)
	}

	var releases []Release
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return nil, err
	}

	L_debug("update: fetched releases", "total", len(releases))

	// Find latest matching release
	for _, r := range releases {
		if r.Draft {
			L_trace("update: skipping draft release", "tag", r.TagName)
			continue
		}
		if channel == "beta" && strings.Contains(r.TagName, "-beta") {
			L_debug("update: found beta release", "tag", r.TagName)
			return &r, nil
		}
		if channel == "rc" && strings.Contains(r.TagName, "-rc") {
			L_debug("update: found rc release", "tag", r.TagName)
			return &r, nil
		}
	}

	return nil, fmt.Errorf("no %s releases found", channel)
}

// Download downloads the release archive and verifies its checksum
func (u *Updater) Download(info *UpdateInfo, onProgress func(downloaded, total int64)) (string, error) {
	L_info("update: starting download",
		"version", info.NewVersion,
		"archive", info.ArchiveName,
	)
	L_debug("update: download URL", "url", info.DownloadURL)

	// Create temp directory
	tmpDir, err := os.MkdirTemp("", "goclaw-update-*")
	if err != nil {
		return "", fmt.Errorf("failed to create temp directory: %w", err)
	}
	L_debug("update: temp directory created", "path", tmpDir)

	// Download archive
	archivePath := filepath.Join(tmpDir, "goclaw.tar.gz")
	L_debug("update: downloading archive", "dest", archivePath)

	if err := u.downloadFile(info.DownloadURL, archivePath, onProgress); err != nil {
		os.RemoveAll(tmpDir)
		return "", fmt.Errorf("failed to download: %w", err)
	}

	// Get downloaded file size
	if fi, err := os.Stat(archivePath); err == nil {
		L_info("update: download complete", "size", formatBytes(fi.Size()))
	}

	// Download and verify checksum
	L_debug("update: verifying checksum", "checksumURL", info.ChecksumURL)
	if err := u.verifyChecksum(archivePath, info.ArchiveName, info.ChecksumURL); err != nil {
		os.RemoveAll(tmpDir)
		return "", fmt.Errorf("checksum verification failed: %w", err)
	}

	L_info("update: checksum verified successfully")

	// Extract archive
	L_debug("update: extracting archive", "dest", tmpDir)
	binaryPath := filepath.Join(tmpDir, "goclaw")
	if err := extractTarGz(archivePath, tmpDir); err != nil {
		os.RemoveAll(tmpDir)
		return "", fmt.Errorf("failed to extract: %w", err)
	}

	// Verify binary exists
	fi, err := os.Stat(binaryPath)
	if err != nil {
		os.RemoveAll(tmpDir)
		return "", fmt.Errorf("binary not found in archive")
	}

	L_info("update: extraction complete",
		"binary", binaryPath,
		"size", formatBytes(fi.Size()),
	)

	return binaryPath, nil
}

// downloadFile downloads a file from URL to dest
func (u *Updater) downloadFile(url, dest string, onProgress func(downloaded, total int64)) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute) // Long timeout for downloads
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}

	resp, err := u.httpClient.Do(req)
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
func (u *Updater) verifyChecksum(filePath, archiveName, checksumURL string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Download checksums.txt
	L_debug("update: fetching checksums", "url", checksumURL)
	req, err := http.NewRequestWithContext(ctx, "GET", checksumURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := u.httpClient.Do(req)
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

	L_debug("update: checksums file downloaded", "size", len(checksumData))

	// Parse checksums (format: "hash  filename")
	// Look for exact match on the archive name we downloaded
	var expectedHash string
	checksumLines := strings.Split(string(checksumData), "\n")
	for _, line := range checksumLines {
		parts := strings.Fields(line)
		if len(parts) >= 2 && parts[1] == archiveName {
			expectedHash = parts[0]
			L_debug("update: found expected checksum",
				"archive", archiveName,
				"sha256", expectedHash[:16]+"...",
			)
			break
		}
	}

	if expectedHash == "" {
		L_debug("update: checksum not found in file",
			"archive", archiveName,
			"entriesChecked", len(checksumLines),
		)
		return fmt.Errorf("checksum not found for %s", archiveName)
	}

	// Calculate actual hash
	L_debug("update: calculating SHA256", "file", filePath)
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

	L_debug("update: checksum comparison",
		"expected", expectedHash[:16]+"...",
		"actual", actualHash[:16]+"...",
	)

	if actualHash != expectedHash {
		return fmt.Errorf("checksum mismatch for %s: expected %s, got %s",
			archiveName, expectedHash[:16]+"...", actualHash[:16]+"...")
	}

	return nil
}

// formatBytes formats bytes as human-readable string
func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

// Apply replaces the current binary with the new one
func (u *Updater) Apply(newBinaryPath string, noRestart bool) error {
	currentExe, err := GetExecutablePath()
	if err != nil {
		return err
	}

	L_info("update: applying update",
		"currentPath", currentExe,
		"newPath", newBinaryPath,
	)

	// Make new binary executable
	L_debug("update: setting executable permissions", "path", newBinaryPath)
	// G302: 0755 is intentional - this is an executable binary
	if err := os.Chmod(newBinaryPath, 0755); err != nil { //nolint:gosec
		return fmt.Errorf("failed to make binary executable: %w", err)
	}

	// Backup current binary
	backupPath := currentExe + ".old"
	L_debug("update: backing up current binary", "from", currentExe, "to", backupPath)
	if err := os.Rename(currentExe, backupPath); err != nil {
		return fmt.Errorf("failed to backup current binary: %w", err)
	}

	// Move new binary into place
	L_debug("update: installing new binary", "from", newBinaryPath, "to", currentExe)
	if err := copyFile(newBinaryPath, currentExe); err != nil {
		// Try to restore backup - best effort, ignore error since we're already returning one
		L_warn("update: install failed, restoring backup", "error", err)
		_ = os.Rename(backupPath, currentExe)
		return fmt.Errorf("failed to install new binary: %w", err)
	}

	// Make the installed binary executable
	// G302: 0755 is intentional - this is an executable binary
	if err := os.Chmod(currentExe, 0755); err != nil { //nolint:gosec
		// Try to restore backup
		L_warn("update: chmod failed, restoring backup", "error", err)
		_ = os.Rename(backupPath, currentExe)
		return fmt.Errorf("failed to make installed binary executable: %w", err)
	}

	// Clean up backup - best effort
	L_debug("update: removing backup", "path", backupPath)
	_ = os.Remove(backupPath)

	L_info("update: binary replaced successfully", "path", currentExe)

	if noRestart {
		L_info("update: skipping restart (noRestart=true)")
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
	// G204: exePath is the path to ourselves after update, not arbitrary user input
	return syscall.Exec(exePath, args, os.Environ()) //nolint:gosec
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
			c, _ = strconv.Atoi(part) // Ignore error, defaults to 0
		}
		if i < len(newParts) {
			part := strings.Split(newParts[i], "-")[0]
			n, _ = strconv.Atoi(part) // Ignore error, defaults to 0
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
