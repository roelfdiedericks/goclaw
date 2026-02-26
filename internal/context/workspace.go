// Package context handles workspace context file loading and system prompt building.
package context

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/roelfdiedericks/goclaw/internal/logging"
)

// Workspace file names - these define the agent's personality and behavior
const (
	FileAgents    = "AGENTS.md"    // Operating manual - shared with subagents
	FileSoul      = "SOUL.md"      // Personality - main agent only
	FileTools     = "TOOLS.md"     // Environment notes - shared with subagents
	FileIdentity  = "IDENTITY.md"  // Agent identity - main agent only
	FileUser      = "USER.md"      // User info - main agent only
	FileHeartbeat = "HEARTBEAT.md" // Periodic tasks - main agent only
	FileBootstrap = "BOOTSTRAP.md" // First-run setup - deleted after use
	FileMemory    = "MEMORY.md"    // Long-term memories - main agent only
)

// WorkspaceFile represents a loaded workspace context file
type WorkspaceFile struct {
	Name    string // e.g., "SOUL.md"
	Path    string // full path
	Content string // file contents (empty if missing)
	Missing bool   // true if file doesn't exist
}

// workspaceFileOrder defines the order files are loaded and injected
var workspaceFileOrder = []string{
	FileAgents,
	FileSoul,
	FileTools,
	FileIdentity,
	FileUser,
	FileBootstrap,
	FileMemory,
}

// subagentAllowlist defines which files subagents can see
var subagentAllowlist = map[string]bool{
	FileAgents: true,
	FileTools:  true,
}

// LoadWorkspaceFiles loads all workspace context files from the given directory.
// If includeMemory is false, MEMORY.md is excluded (for restricted roles).
func LoadWorkspaceFiles(workspaceDir string, includeMemory bool) []WorkspaceFile {
	logging.L_debug("context: loading workspace files", "dir", workspaceDir, "includeMemory", includeMemory)

	var files []WorkspaceFile

	for _, name := range workspaceFileOrder {
		// Skip MEMORY.md if not included
		if name == FileMemory && !includeMemory {
			logging.L_debug("context: skipping memory file (restricted)", "name", name)
			continue
		}

		filePath := filepath.Join(workspaceDir, name)
		content, err := os.ReadFile(filePath)

		if err != nil {
			files = append(files, WorkspaceFile{
				Name:    name,
				Path:    filePath,
				Missing: true,
			})
			logging.L_trace("context: workspace file missing", "name", name)
		} else {
			// Strip YAML frontmatter if present
			contentStr := stripFrontmatter(string(content))
			files = append(files, WorkspaceFile{
				Name:    name,
				Path:    filePath,
				Content: contentStr,
				Missing: false,
			})
			logging.L_debug("context: loaded workspace file", "name", name, "chars", len(contentStr))
		}
	}

	// Also check for memory.md (lowercase variant) - only if memory is included
	if includeMemory {
		memoryAltPath := filepath.Join(workspaceDir, "memory.md")
		if content, err := os.ReadFile(memoryAltPath); err == nil {
			// Only add if MEMORY.md wasn't found
			for i, f := range files {
				if f.Name == FileMemory && f.Missing {
					files[i] = WorkspaceFile{
						Name:    FileMemory,
						Path:    memoryAltPath,
						Content: stripFrontmatter(string(content)),
						Missing: false,
					}
					logging.L_debug("context: loaded alternate memory file", "path", memoryAltPath)
					break
				}
			}
		}
	}

	// Load daily memory files (today and yesterday)
	if includeMemory {
		files = append(files, loadDailyMemoryFiles(workspaceDir)...)
	}

	return files
}

// loadDailyMemoryFiles loads memory/YYYY-MM-DD.md for today and yesterday.
func loadDailyMemoryFiles(workspaceDir string) []WorkspaceFile {
	now := time.Now()
	dates := []time.Time{now.AddDate(0, 0, -1), now}
	memoryDir := filepath.Join(workspaceDir, "memory")

	var files []WorkspaceFile
	for _, d := range dates {
		name := fmt.Sprintf("memory/%s.md", d.Format("2006-01-02"))
		path := filepath.Join(memoryDir, d.Format("2006-01-02")+".md")
		content, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		contentStr := stripFrontmatter(string(content))
		files = append(files, WorkspaceFile{
			Name:    name,
			Path:    path,
			Content: contentStr,
			Missing: false,
		})
		logging.L_debug("context: loaded daily memory file", "name", name, "chars", len(contentStr))
	}
	return files
}

// FilterForSubagent filters workspace files to only those allowed for subagents
func FilterForSubagent(files []WorkspaceFile) []WorkspaceFile {
	var filtered []WorkspaceFile
	for _, f := range files {
		if subagentAllowlist[f.Name] {
			filtered = append(filtered, f)
		}
	}
	return filtered
}

// FilterForSession filters workspace files based on whether this is a subagent session
func FilterForSession(files []WorkspaceFile, isSubagent bool) []WorkspaceFile {
	if isSubagent {
		return FilterForSubagent(files)
	}
	return files
}

// FilterMemory removes MEMORY.md from the workspace files (for restricted roles)
func FilterMemory(files []WorkspaceFile) []WorkspaceFile {
	var filtered []WorkspaceFile
	for _, f := range files {
		if f.Name != FileMemory {
			filtered = append(filtered, f)
		}
	}
	return filtered
}

// stripFrontmatter removes YAML frontmatter from content
func stripFrontmatter(content string) string {
	if !strings.HasPrefix(content, "---") {
		return content
	}

	// Find the closing ---
	endIndex := strings.Index(content[3:], "\n---")
	if endIndex == -1 {
		return content
	}

	// Skip past the closing --- and any leading whitespace
	start := 3 + endIndex + 4 // 3 for initial ---, endIndex, 4 for \n---
	trimmed := content[start:]
	trimmed = strings.TrimLeft(trimmed, "\n\r\t ")

	return trimmed
}

// HasSoulFile returns true if SOUL.md was found in the workspace files
func HasSoulFile(files []WorkspaceFile) bool {
	for _, f := range files {
		if f.Name == FileSoul && !f.Missing {
			return true
		}
	}
	return false
}

// GetFile returns the content of a specific file, or empty string if missing
func GetFile(files []WorkspaceFile, name string) string {
	for _, f := range files {
		if f.Name == name && !f.Missing {
			return f.Content
		}
	}
	return ""
}
