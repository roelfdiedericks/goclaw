package memorygraph

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// MarkdownIngester scans workspace markdown files for ingestion
type MarkdownIngester struct {
	workspaceDir    string
	includePatterns []string
	excludePatterns []string
}

// NewMarkdownIngester creates a new markdown ingester
// workspaceDir is the workspace root (e.g., ~/.openclaw/workspace)
// config provides include/exclude patterns
func NewMarkdownIngester(workspaceDir string, config IngestionConfig) *MarkdownIngester {
	include := config.IncludePatterns
	exclude := config.ExcludePatterns

	// Apply defaults if empty
	if len(include) == 0 {
		include = []string{"*.md", "memory/*.md", "albums/*.md"}
	}
	if len(exclude) == 0 {
		exclude = []string{"skills/**", "ref/**", "goclaw/**", ".*/**"}
	}

	return &MarkdownIngester{
		workspaceDir:    workspaceDir,
		includePatterns: include,
		excludePatterns: exclude,
	}
}

// Type returns the source type identifier
func (m *MarkdownIngester) Type() string {
	return "markdown"
}

// Scan walks the workspace directory and returns markdown files matching patterns
func (m *MarkdownIngester) Scan(ctx context.Context) (<-chan IngestItem, error) {
	ch := make(chan IngestItem)

	go func() {
		defer close(ch)

		L_info("memorygraph: scanning workspace for markdown",
			"dir", m.workspaceDir,
			"include", m.includePatterns,
			"exclude", m.excludePatterns)

		// Collect all matching files
		files := m.findMatchingFiles()

		for _, relPath := range files {
			select {
			case <-ctx.Done():
				return
			default:
			}

			fullPath := filepath.Join(m.workspaceDir, relPath)

			// Read file content
			content, err := os.ReadFile(fullPath)
			if err != nil {
				L_warn("memorygraph: failed to read file", "path", relPath, "error", err)
				continue
			}

			// Skip empty files
			if len(content) == 0 {
				continue
			}

			item := IngestItem{
				SourcePath:  relPath,
				Content:     string(content),
				ContentHash: HashContent(string(content)),
				Metadata: map[string]string{
					"filename": filepath.Base(relPath),
					"dir":      filepath.Dir(relPath),
				},
			}

			select {
			case ch <- item:
				L_debug("memorygraph: found markdown", "path", relPath, "size", len(content))
			case <-ctx.Done():
				return
			}
		}

		L_info("memorygraph: scan complete", "files", len(files))
	}()

	return ch, nil
}

// findMatchingFiles returns all files matching include patterns but not exclude patterns
func (m *MarkdownIngester) findMatchingFiles() []string {
	var matched []string

	// Walk through workspace and collect .md files
	filepath.Walk(m.workspaceDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}

		// Skip directories
		if info.IsDir() {
			return nil
		}

		// Only .md files
		if !strings.HasSuffix(strings.ToLower(path), ".md") {
			return nil
		}

		relPath, err := filepath.Rel(m.workspaceDir, path)
		if err != nil {
			return nil
		}

		// Normalize path separators for matching
		relPath = filepath.ToSlash(relPath)

		// Check exclude patterns first (they take priority)
		for _, pattern := range m.excludePatterns {
			if matchGlob(relPath, pattern) {
				L_debug("memorygraph: excluded by pattern", "path", relPath, "pattern", pattern)
				return nil
			}
		}

		// Check include patterns
		for _, pattern := range m.includePatterns {
			if matchGlob(relPath, pattern) {
				matched = append(matched, relPath)
				return nil
			}
		}

		return nil
	})

	return matched
}

// matchGlob matches a path against a glob pattern
// Supports: * (any chars except /), ** (any path including /), ? (single char)
func matchGlob(path, pattern string) bool {
	// Normalize both to forward slashes
	path = filepath.ToSlash(path)
	pattern = filepath.ToSlash(pattern)

	// Handle ** patterns (match any path segment)
	if strings.Contains(pattern, "**") {
		// Split pattern at **
		parts := strings.Split(pattern, "**")
		if len(parts) == 2 {
			prefix := parts[0]
			suffix := parts[1]

			// Remove leading/trailing slashes from suffix
			suffix = strings.TrimPrefix(suffix, "/")

			// Prefix must match start of path
			if prefix != "" && !strings.HasPrefix(path, prefix) {
				return false
			}

			// If no suffix, prefix match is enough
			if suffix == "" {
				return strings.HasPrefix(path, strings.TrimSuffix(prefix, "/"))
			}

			// Path after prefix must end with suffix pattern
			remaining := path
			if prefix != "" {
				remaining = strings.TrimPrefix(path, prefix)
			}

			// Match suffix (which may contain wildcards)
			return matchSimpleGlob(remaining, suffix) || matchSimpleGlob(filepath.Base(path), suffix)
		}
	}

	return matchSimpleGlob(path, pattern)
}

// matchSimpleGlob matches a path against a pattern with * and ? wildcards (no **)
func matchSimpleGlob(path, pattern string) bool {
	// Use filepath.Match for simple patterns
	matched, err := filepath.Match(pattern, path)
	if err != nil {
		return false
	}
	if matched {
		return true
	}

	// Also try matching just the filename for patterns without /
	if !strings.Contains(pattern, "/") {
		matched, err = filepath.Match(pattern, filepath.Base(path))
		if err == nil && matched {
			return true
		}
	}

	return false
}
