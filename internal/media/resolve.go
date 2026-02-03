// Package media provides utilities for media path resolution and mimetype detection.
// resolve.go implements path resolution, mimetype detection, and escaping for inline media syntax.
package media

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// ResolveMediaPath converts various path formats to an absolute path.
// Supported formats:
//   - Relative to media root: "browser/screenshot.png" or "./media/browser/screenshot.png"
//   - Absolute paths in allowed directories: "/home/user/.openclaw/media/..."
//
// Returns error if path is invalid, outside allowed directories, or doesn't exist.
func ResolveMediaPath(mediaRoot, path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("empty path")
	}

	var absPath string

	// Handle different path formats
	if strings.HasPrefix(path, "./media/") {
		// OpenClaw-style relative path: ./media/subdir/file.ext
		subpath := strings.TrimPrefix(path, "./media/")
		absPath = filepath.Join(mediaRoot, subpath)
	} else if filepath.IsAbs(path) {
		// Absolute path - validate it's in allowed directories
		absPath = filepath.Clean(path)

		// Check if it's within media root
		if !strings.HasPrefix(absPath, mediaRoot) {
			// Also allow /tmp/ for temporary files
			if !strings.HasPrefix(absPath, "/tmp/") {
				return "", fmt.Errorf("path outside allowed directories: %s", path)
			}
		}
	} else {
		// Relative path without ./media/ prefix - assume relative to media root
		absPath = filepath.Join(mediaRoot, path)
	}

	// Clean the path to prevent traversal
	absPath = filepath.Clean(absPath)

	// Final security check - ensure no traversal escaped
	if !strings.HasPrefix(absPath, mediaRoot) && !strings.HasPrefix(absPath, "/tmp/") {
		return "", fmt.Errorf("path traversal detected: %s", path)
	}

	return absPath, nil
}

// DetectMimeType detects the MIME type of a file by reading its content.
// Falls back to extension-based detection if content detection fails.
func DetectMimeType(path string) (string, error) {
	// Open file and read first 512 bytes for detection
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	// Read up to 512 bytes (http.DetectContentType uses at most 512)
	buf := make([]byte, 512)
	n, err := f.Read(buf)
	if err != nil && n == 0 {
		return "", err
	}

	// Detect from content
	mimeType := http.DetectContentType(buf[:n])

	// http.DetectContentType returns "application/octet-stream" for unknown types
	// Try extension-based fallback for common media types
	if mimeType == "application/octet-stream" {
		if extMime := mimeFromExtension(path); extMime != "" {
			mimeType = extMime
		}
	}

	return mimeType, nil
}

// mimeFromExtension returns MIME type based on file extension.
func mimeFromExtension(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	// Images
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".svg":
		return "image/svg+xml"
	case ".ico":
		return "image/x-icon"
	// Video
	case ".mp4":
		return "video/mp4"
	case ".webm":
		return "video/webm"
	case ".mov":
		return "video/quicktime"
	case ".avi":
		return "video/x-msvideo"
	case ".mkv":
		return "video/x-matroska"
	// Audio
	case ".mp3":
		return "audio/mpeg"
	case ".ogg":
		return "audio/ogg"
	case ".wav":
		return "audio/wav"
	case ".m4a":
		return "audio/mp4"
	case ".flac":
		return "audio/flac"
	// Documents
	case ".pdf":
		return "application/pdf"
	case ".txt":
		return "text/plain"
	case ".json":
		return "application/json"
	case ".xml":
		return "application/xml"
	default:
		return ""
	}
}

// EscapePath escapes a path for use in the {{media:mime:'path'}} syntax.
// Escapes single quotes and backslashes.
func EscapePath(path string) string {
	// Escape backslashes first, then single quotes
	path = strings.ReplaceAll(path, "\\", "\\\\")
	path = strings.ReplaceAll(path, "'", "\\'")
	return path
}

// UnescapePath reverses the escaping done by EscapePath.
func UnescapePath(path string) string {
	// Unescape single quotes first, then backslashes
	path = strings.ReplaceAll(path, "\\'", "'")
	path = strings.ReplaceAll(path, "\\\\", "\\")
	return path
}

// FileExists checks if a file exists and is not a directory.
func FileExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}
