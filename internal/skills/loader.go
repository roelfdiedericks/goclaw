package skills

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync/atomic"
	"time"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// skillsData holds the loaded skills (for atomic swap)
type skillsData struct {
	skills map[string]*Skill
	cache  map[string]skillCacheEntry
}

// Loader discovers and loads skills from configured directories.
// Note: bundledDir and managedDir have been removed. Skills are now installed
// from the embedded catalog to the workspace skills directory.
type Loader struct {
	workspaceDir string   // Current workspace skills
	extraDirs    []string // Additional directories from config (power user feature)

	data atomic.Pointer[skillsData]
}

type skillCacheEntry struct {
	skill   *Skill
	modTime time.Time
}

// NewLoader creates a new skill loader.
// workspaceDir is the primary skills directory (typically ~/.openclaw/workspace/skills).
// extraDirs are additional directories specified in config (advanced use).
func NewLoader(workspaceDir string, extraDirs []string) *Loader {
	l := &Loader{
		workspaceDir: workspaceDir,
		extraDirs:    extraDirs,
	}
	// Initialize with empty data
	l.data.Store(&skillsData{
		skills: make(map[string]*Skill),
		cache:  make(map[string]skillCacheEntry),
	})
	return l
}

// LoadAll loads skills from all configured directories.
// Precedence: extraDirs (lowest) < workspace (highest)
func (l *Loader) LoadAll() ([]*Skill, error) {
	L_debug("skills: loading from directories",
		"workspace", l.workspaceDir,
		"extraDirs", len(l.extraDirs))

	// Get current cache for reuse
	oldData := l.data.Load()
	oldCache := oldData.cache

	// Build new skills map
	newSkills := make(map[string]*Skill)
	newCache := make(map[string]skillCacheEntry)

	// Copy existing cache entries
	for k, v := range oldCache {
		newCache[k] = v
	}

	// Load in precedence order (lowest first, so higher ones override)
	sources := []struct {
		path   string
		source Source
	}{}

	// Add extra dirs first (lowest precedence)
	for _, dir := range l.extraDirs {
		sources = append(sources, struct {
			path   string
			source Source
		}{dir, SourceExtra})
	}

	// Workspace has highest precedence
	if l.workspaceDir != "" {
		sources = append(sources, struct {
			path   string
			source Source
		}{l.workspaceDir, SourceWorkspace})
	}

	totalLoaded := 0
	totalFailed := 0
	bySource := make(map[Source]int)

	for _, src := range sources {
		if src.path == "" {
			continue
		}
		loaded, failed, err := l.loadFromDirectory(src.path, src.source, newSkills, newCache)
		if err != nil {
			L_warn("skills: failed to load directory", "path", src.path, "error", err)
			// Continue loading from other directories
		}
		totalLoaded += loaded
		totalFailed += failed
		if loaded > 0 {
			bySource[src.source] = loaded
		}
	}

	// Atomic swap
	l.data.Store(&skillsData{
		skills: newSkills,
		cache:  newCache,
	})

	// Log summary at INFO level
	L_info("skills: loaded",
		"total", len(newSkills),
		"failed", totalFailed,
		"workspace", bySource[SourceWorkspace],
		"extra", bySource[SourceExtra])

	// Build result slice
	skills := make([]*Skill, 0, len(newSkills))
	for _, skill := range newSkills {
		skills = append(skills, skill)
	}
	sort.Slice(skills, func(i, j int) bool {
		return skills[i].Name < skills[j].Name
	})

	return skills, nil
}

// loadFromDirectory scans a directory for SKILL.md files and loads them.
func (l *Loader) loadFromDirectory(dir string, source Source, skills map[string]*Skill, cache map[string]skillCacheEntry) (loaded int, failed int, err error) {
	// Check if directory exists
	info, err := os.Stat(dir)
	if os.IsNotExist(err) {
		L_debug("skills: directory does not exist", "path", dir, "source", source)
		return 0, 0, nil
	}
	if err != nil {
		L_warn("skills: failed to stat directory", "path", dir, "error", err)
		return 0, 0, err
	}
	if !info.IsDir() {
		L_debug("skills: path is not a directory", "path", dir)
		return 0, 0, nil
	}

	L_debug("skills: scanning directory", "path", dir, "source", source)

	// Walk directory looking for SKILL.md files
	entries, err := os.ReadDir(dir)
	if err != nil {
		L_warn("skills: failed to read directory", "path", dir, "error", err)
		return 0, 0, err
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		skillPath := filepath.Join(dir, entry.Name(), "SKILL.md")
		if _, err := os.Stat(skillPath); os.IsNotExist(err) {
			continue
		}

		skill, err := l.loadSkill(skillPath, source, cache)
		if err != nil {
			L_warn("skills: failed to load skill",
				"skill", entry.Name(),
				"path", skillPath,
				"error", err)
			failed++
			continue
		}

		loaded++

		// Store with precedence (later sources override earlier ones)
		existing, exists := skills[skill.Name]
		if exists {
			L_debug("skills: overridden by higher precedence",
				"name", skill.Name,
				"old_source", existing.Source,
				"new_source", skill.Source)
		}
		skills[skill.Name] = skill
		L_trace("skills: loaded",
			"name", skill.Name,
			"source", skill.Source)
	}

	L_debug("skills: directory scan complete", "path", dir, "loaded", loaded, "failed", failed)
	return loaded, failed, nil
}

// loadSkill loads a single skill file, using cache if available.
func (l *Loader) loadSkill(path string, source Source, cache map[string]skillCacheEntry) (*Skill, error) {
	// Check file mod time
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}

	// Check cache
	if cached, ok := cache[path]; ok {
		if cached.modTime.Equal(info.ModTime()) {
			// Return cached skill but update source
			cached.skill.Source = source
			return cached.skill, nil
		}
	}

	// Parse fresh
	skill, err := ParseSkillFile(path, source)
	if err != nil {
		return nil, err
	}
	skill.LoadedAt = time.Now()

	// Update cache
	cache[path] = skillCacheEntry{
		skill:   skill,
		modTime: info.ModTime(),
	}

	return skill, nil
}

// GetAllSkills returns all loaded skills as a slice.
func (l *Loader) GetAllSkills() []*Skill {
	data := l.data.Load()

	skills := make([]*Skill, 0, len(data.skills))
	for _, skill := range data.skills {
		skills = append(skills, skill)
	}

	// Sort by name for consistent ordering
	sort.Slice(skills, func(i, j int) bool {
		return skills[i].Name < skills[j].Name
	})

	return skills
}

// GetSkill returns a specific skill by name.
func (l *Loader) GetSkill(name string) *Skill {
	data := l.data.Load()
	return data.skills[name]
}

// RemoveSkill removes a skill from the loaded skills map.
// Used to discard ineligible or flagged skills at startup.
func (l *Loader) RemoveSkill(name string) {
	data := l.data.Load()
	if _, exists := data.skills[name]; !exists {
		return
	}

	newSkills := make(map[string]*Skill)
	for k, v := range data.skills {
		if k != name {
			newSkills[k] = v
		}
	}

	l.data.Store(&skillsData{
		skills: newSkills,
		cache:  data.cache,
	})
}

// GetEligibleSkills returns skills that are eligible for the current environment.
func (l *Loader) GetEligibleSkills(ctx EligibilityContext) []*Skill {
	data := l.data.Load()

	var eligible []*Skill
	for _, skill := range data.skills {
		if skill.IsEligible(ctx) {
			eligible = append(eligible, skill)
		}
	}

	// Sort by name
	sort.Slice(eligible, func(i, j int) bool {
		return eligible[i].Name < eligible[j].Name
	})

	return eligible
}

// GetStats returns statistics about loaded skills.
func (l *Loader) GetStats() ManagerStats {
	data := l.data.Load()

	stats := ManagerStats{
		TotalSkills: len(data.skills),
		BySource:    make(map[Source]int),
	}

	ctx := EligibilityContext{OS: runtime.GOOS}

	for _, skill := range data.skills {
		stats.BySource[skill.Source]++
		if skill.IsEligible(ctx) {
			stats.EligibleSkills++
		}
		if len(skill.AuditFlags) > 0 {
			stats.FlaggedSkills++
		}
	}

	return stats
}

// Refresh reloads all skills from disk.
func (l *Loader) Refresh() error {
	_, err := l.LoadAll()
	return err
}

// InvalidateCache clears the cache for a specific path or all paths.
func (l *Loader) InvalidateCache(path string) {
	data := l.data.Load()

	if path == "" {
		// Clear all cache
		l.data.Store(&skillsData{
			skills: data.skills,
			cache:  make(map[string]skillCacheEntry),
		})
	} else {
		// Clear specific path
		newCache := make(map[string]skillCacheEntry)
		for k, v := range data.cache {
			if k != path {
				newCache[k] = v
			}
		}
		l.data.Store(&skillsData{
			skills: data.skills,
			cache:  newCache,
		})
	}
}

// WatchedDirs returns all directories being watched for changes.
func (l *Loader) WatchedDirs() []string {
	dirs := make([]string, 0, len(l.extraDirs)+1)
	dirs = append(dirs, l.extraDirs...)
	if l.workspaceDir != "" {
		dirs = append(dirs, l.workspaceDir)
	}
	return dirs
}
