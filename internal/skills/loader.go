package skills

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/log"
)

// skillsData holds the loaded skills (for atomic swap)
type skillsData struct {
	skills map[string]*Skill
	cache  map[string]skillCacheEntry
}

// Loader discovers and loads skills from multiple directories.
type Loader struct {
	bundledDir   string   // GoClaw bundled skills directory
	managedDir   string   // User-installed skills (~/.openclaw/skills)
	workspaceDir string   // Current workspace skills
	extraDirs    []string // Additional directories from config

	data atomic.Pointer[skillsData]
}

type skillCacheEntry struct {
	skill   *Skill
	modTime time.Time
}

// NewLoader creates a new skill loader.
func NewLoader(bundledDir, managedDir, workspaceDir string, extraDirs []string) *Loader {
	l := &Loader{
		bundledDir:   bundledDir,
		managedDir:   managedDir,
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
// Higher precedence sources override lower ones:
// extraDirs < bundled < managed < workspace
func (l *Loader) LoadAll() ([]*Skill, error) {
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

	// Then bundled
	if l.bundledDir != "" {
		sources = append(sources, struct {
			path   string
			source Source
		}{l.bundledDir, SourceBundled})
	}

	// Then managed
	if l.managedDir != "" {
		sources = append(sources, struct {
			path   string
			source Source
		}{l.managedDir, SourceManaged})
	}

	// Workspace has highest precedence
	if l.workspaceDir != "" {
		sources = append(sources, struct {
			path   string
			source Source
		}{l.workspaceDir, SourceWorkspace})
	}

	for _, src := range sources {
		if src.path == "" {
			continue
		}
		if err := l.loadFromDirectory(src.path, src.source, newSkills, newCache); err != nil {
			log.Warn("failed to load skills from directory", "path", src.path, "error", err)
			// Continue loading from other directories
		}
	}

	// Atomic swap
	l.data.Store(&skillsData{
		skills: newSkills,
		cache:  newCache,
	})

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
func (l *Loader) loadFromDirectory(dir string, source Source, skills map[string]*Skill, cache map[string]skillCacheEntry) error {
	// Check if directory exists
	info, err := os.Stat(dir)
	if os.IsNotExist(err) {
		log.Debug("skill directory does not exist", "path", dir)
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return nil
	}

	// Walk directory looking for SKILL.md files
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
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
			log.Warn("failed to load skill",
				"path", skillPath,
				"error", err)
			continue
		}

		// Store with precedence (later sources override earlier ones)
		existing, exists := skills[skill.Name]
		if exists {
			log.Debug("skill overridden by higher precedence",
				"name", skill.Name,
				"old_source", existing.Source,
				"new_source", skill.Source)
		}
		skills[skill.Name] = skill
		log.Debug("loaded skill",
			"name", skill.Name,
			"source", skill.Source,
			"path", skill.Location)
	}

	return nil
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
	dirs := make([]string, 0, len(l.extraDirs)+3)
	dirs = append(dirs, l.extraDirs...)
	if l.bundledDir != "" {
		dirs = append(dirs, l.bundledDir)
	}
	if l.managedDir != "" {
		dirs = append(dirs, l.managedDir)
	}
	if l.workspaceDir != "" {
		dirs = append(dirs, l.workspaceDir)
	}
	return dirs
}
