package skills

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// ParseSkillFile parses a SKILL.md file and returns a Skill struct.
// The file format is YAML frontmatter (between --- delimiters) followed by markdown content.
func ParseSkillFile(path string, source Source) (*Skill, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read skill file: %w", err)
	}

	// Calculate SHA for change detection
	hash := sha256.Sum256(content)
	contentSHA := hex.EncodeToString(hash[:])

	// Try to extract frontmatter
	frontmatter, _, err := extractFrontmatter(content)
	if err != nil {
		// No frontmatter - try to parse without it
		skill := parseSkillWithoutFrontmatter(path, source, content, contentSHA)
		if skill != nil {
			return skill, nil
		}
		return nil, fmt.Errorf("failed to extract frontmatter: %w", err)
	}

	// Try standard YAML parsing first
	var fm Frontmatter
	yamlErr := yaml.Unmarshal(frontmatter, &fm)

	if yamlErr != nil {
		// If YAML fails (common with unquoted colons), use manual parsing
		fm, err = parseSimpleFrontmatter(frontmatter)
		if err != nil {
			return nil, fmt.Errorf("failed to parse frontmatter: %w (yaml error: %v)", err, yamlErr)
		}
	}

	// Parse metadata if present
	var metadata *OpenClawMetadata
	if fm.Metadata != nil {
		metadata, err = parseMetadata(fm.Metadata)
		if err != nil {
			// Log warning but don't fail - skill can work without metadata
			metadata = nil
		}
	}

	// Derive name from filename if not in frontmatter
	name := fm.Name
	if name == "" {
		name = filepath.Base(filepath.Dir(path))
	}

	skill := &Skill{
		Name:        name,
		Description: fm.Description,
		Location:    path,
		Source:      source,
		Content:     string(content),
		ContentSHA:  contentSHA,
		Metadata:    metadata,
		Enabled:     true, // Enabled by default until audit
		LoadedAt:    time.Now(),
	}

	return skill, nil
}

// extractFrontmatter extracts YAML frontmatter from content.
// Returns frontmatter bytes, remaining content, and error.
func extractFrontmatter(content []byte) ([]byte, []byte, error) {
	// Must start with ---
	if !bytes.HasPrefix(content, []byte("---")) {
		return nil, nil, fmt.Errorf("file does not start with frontmatter delimiter (---)")
	}

	// Find closing ---
	rest := content[3:]
	idx := bytes.Index(rest, []byte("\n---"))
	if idx < 0 {
		return nil, nil, fmt.Errorf("no closing frontmatter delimiter found")
	}

	frontmatter := rest[:idx]
	remaining := rest[idx+4:] // Skip \n---

	return frontmatter, remaining, nil
}

// parseMetadata parses the metadata field which can be either:
// - A YAML map with openclaw nested inside
// - A JSON string containing the metadata
func parseMetadata(metadata interface{}) (*OpenClawMetadata, error) {
	switch v := metadata.(type) {
	case string:
		// JSON string
		return parseMetadataFromJSON(v)
	case map[string]interface{}:
		// YAML map - look for openclaw key
		return parseMetadataFromMap(v)
	default:
		return nil, fmt.Errorf("unexpected metadata type: %T", metadata)
	}
}

// parseMetadataFromJSON parses metadata from a JSON string
func parseMetadataFromJSON(metadataStr string) (*OpenClawMetadata, error) {
	// First try to parse as-is (might be full metadata)
	var meta OpenClawMetadata
	if err := json.Unmarshal([]byte(metadataStr), &meta); err == nil {
		return &meta, nil
	}

	// Try parsing as wrapper with openclaw key
	var wrapper struct {
		OpenClaw *OpenClawMetadata `json:"openclaw"`
	}
	if err := json.Unmarshal([]byte(metadataStr), &wrapper); err == nil && wrapper.OpenClaw != nil {
		return wrapper.OpenClaw, nil
	}

	return nil, fmt.Errorf("failed to parse metadata JSON")
}

// parseMetadataFromMap parses metadata from a YAML map
func parseMetadataFromMap(metadataMap map[string]interface{}) (*OpenClawMetadata, error) {
	// Look for openclaw key
	openclawData, ok := metadataMap["openclaw"]
	if !ok {
		// Maybe the map IS the openclaw metadata directly
		openclawData = metadataMap
	}

	// Convert to JSON then parse (easier than manual type assertions)
	jsonBytes, err := json.Marshal(openclawData)
	if err != nil {
		return nil, err
	}

	var meta OpenClawMetadata
	if err := json.Unmarshal(jsonBytes, &meta); err != nil {
		return nil, err
	}

	return &meta, nil
}

// parseSkillWithoutFrontmatter creates a basic skill from a file without frontmatter
func parseSkillWithoutFrontmatter(path string, source Source, content []byte, contentSHA string) *Skill {
	// Try to extract name from first markdown heading
	name := extractNameFromHeading(content)
	if name == "" {
		// Use directory name
		name = filepath.Base(filepath.Dir(path))
	}

	return &Skill{
		Name:       name,
		Location:   path,
		Source:     source,
		Content:    string(content),
		ContentSHA: contentSHA,
		Enabled:    true,
		LoadedAt:   time.Now(),
	}
}

// extractNameFromHeading extracts a name from the first markdown heading
func extractNameFromHeading(content []byte) string {
	// Match # Heading or ## Heading
	re := regexp.MustCompile(`(?m)^#{1,2}\s+(.+)$`)
	matches := re.FindSubmatch(content)
	if len(matches) >= 2 {
		return strings.TrimSpace(string(matches[1]))
	}
	return ""
}

// parseSimpleFrontmatter manually parses simple key: value frontmatter
// This handles cases where YAML fails due to unquoted colons in values
func parseSimpleFrontmatter(content []byte) (Frontmatter, error) {
	var fm Frontmatter

	lines := bytes.Split(content, []byte("\n"))
	var currentKey string
	var metadataLines []string
	inMetadata := false

	for _, line := range lines {
		lineStr := string(line)
		trimmed := strings.TrimSpace(lineStr)

		// Skip empty lines
		if trimmed == "" {
			continue
		}

		// Check for top-level key
		if !strings.HasPrefix(lineStr, " ") && !strings.HasPrefix(lineStr, "\t") {
			// This is a top-level key
			if idx := strings.Index(lineStr, ":"); idx > 0 {
				key := strings.TrimSpace(lineStr[:idx])
				value := strings.TrimSpace(lineStr[idx+1:])

				if key == "metadata" {
					inMetadata = true
					currentKey = key
					if value != "" && value != "|" && value != ">" {
						// Inline metadata
						fm.Metadata = value
						inMetadata = false
					}
				} else {
					inMetadata = false
					switch key {
					case "name":
						fm.Name = value
					case "description":
						fm.Description = value
					case "homepage":
						fm.Homepage = value
					}
				}
				currentKey = key
			}
		} else if inMetadata && currentKey == "metadata" {
			// Accumulate metadata lines
			metadataLines = append(metadataLines, lineStr)
		}
	}

	// If we collected metadata lines, try to parse them
	if len(metadataLines) > 0 {
		metadataStr := strings.Join(metadataLines, "\n")
		// Try YAML first
		var metaMap map[string]interface{}
		if err := yaml.Unmarshal([]byte(metadataStr), &metaMap); err == nil {
			fm.Metadata = metaMap
		} else {
			// Store as string
			fm.Metadata = metadataStr
		}
	}

	return fm, nil
}
