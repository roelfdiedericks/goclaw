package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// ResolvedPolicy is the single source of truth for visible-vs-backing path semantics.
type ResolvedPolicy struct {
	Mode             string
	VisibleHomeDir   string
	BackingHomeDir   string
	VisibleWorkspace string
	AutoDocsRoots    []string
	AutoDocsWrite    bool
}

type ResolvedPath struct {
	VisiblePath string
	ActualPath  string
	RootPath    string
	RootKind    string
	Relative    string
}

const (
	RootWorkspace = "workspace"
	RootSandboxHome = "sandbox-home"
	RootAutoDocs = "autodocs"
)

// ApplyUserSandboxOverride applies the per-user sandbox override to an already
// evaluated global/category sandbox decision.
func ApplyUserSandboxOverride(categoryEnabled bool, userSandbox bool) bool {
	if !categoryEnabled {
		return false
	}
	return userSandbox
}

// ResolvePolicy returns the current resolved sandbox path model.
func (m *Manager) ResolvePolicy() ResolvedPolicy {
	realHome, _ := os.UserHomeDir()
	m.mu.RLock()
	mode := m.mode
	workspace := filepath.Clean(m.workspaceRoot)
	backingHome := cleanPathIfSet(m.homeDir)
	autoDocsWrite := m.config.IsAutoDocsWriteMode()
	m.mu.RUnlock()

	return ResolvedPolicy{
		Mode:             mode,
		VisibleHomeDir:   cleanPathIfSet(realHome),
		BackingHomeDir:   backingHome,
		VisibleWorkspace: workspace,
		AutoDocsRoots:    m.GetAutoDocsRoots(),
		AutoDocsWrite:    autoDocsWrite,
	}
}

func cleanPathIfSet(path string) string {
	if path == "" {
		return ""
	}
	return filepath.Clean(path)
}

// ResolvePath maps a user-facing path into the backing filesystem path while
// preserving a host-like visible path model for the agent.
func (p ResolvedPolicy) ResolvePath(inputPath string, workingDir string) (ResolvedPath, error) {
	visible := p.expandVisiblePath(inputPath)
	if !filepath.IsAbs(visible) {
		visible = filepath.Clean(filepath.Join(workingDir, visible))
	}

	rootPath, relative, rootKind, ok := p.selectVisibleRoot(visible)
	if !ok {
		return ResolvedPath{}, fmt.Errorf("path escapes sandbox root: %s", inputPath)
	}

	actualPath := visible
	if rootKind == RootSandboxHome && p.shouldRemapSandboxHome() {
		actualPath = filepath.Clean(filepath.Join(p.BackingHomeDir, relative))
	}

	return ResolvedPath{
		VisiblePath: visible,
		ActualPath:  actualPath,
		RootPath:    rootPath,
		RootKind:    rootKind,
		Relative:    relative,
	}, nil
}

func (p ResolvedPolicy) expandVisiblePath(inputPath string) string {
	normalized := normalizeUnicodeSpaces(inputPath)
	if normalized == "~" {
		return p.VisibleHomeDir
	}
	if strings.HasPrefix(normalized, "~/") {
		return filepath.Clean(filepath.Join(p.VisibleHomeDir, normalized[2:]))
	}
	return normalized
}

func (p ResolvedPolicy) selectVisibleRoot(path string) (rootPath, relative, rootKind string, ok bool) {
	roots := []struct {
		path string
		kind string
	}{
		{p.VisibleWorkspace, RootWorkspace},
	}

	for _, root := range p.AutoDocsRoots {
		roots = append(roots, struct {
			path string
			kind string
		}{root, RootAutoDocs})
	}

	if p.allowHomeRootFallback() {
		roots = append(roots, struct {
			path string
			kind string
		}{p.VisibleHomeDir, RootSandboxHome})
	}

	for _, candidate := range roots {
		cleanRoot := filepath.Clean(candidate.path)
		relative, err := filepath.Rel(cleanRoot, path)
		if err != nil || strings.HasPrefix(relative, "..") || filepath.IsAbs(relative) {
			continue
		}
		return cleanRoot, relative, candidate.kind, true
	}

	return "", "", "", false
}

func (p ResolvedPolicy) shouldRemapSandboxHome() bool {
	if p.BackingHomeDir == "" {
		return false
	}
	// Darwin seatbelt has no mount namespace remap semantics, so file-tools
	// should resolve home paths against real home policy roots.
	return runtime.GOOS != "darwin"
}

func (p ResolvedPolicy) allowHomeRootFallback() bool {
	if p.VisibleHomeDir == "" {
		return false
	}
	// In Darwin autodocs modes, file-tools should only operate on workspace
	// and discovered visible non-hidden home directories.
	if runtime.GOOS == "darwin" && (p.Mode == ModeAutoDocsRead || p.Mode == ModeAutoDocsWrite) {
		return false
	}
	return true
}

func pathWithinAnyRoot(path string, roots []string) bool {
	cleanPath := filepath.Clean(path)
	for _, root := range roots {
		cleanRoot := filepath.Clean(root)
		if cleanPath == cleanRoot || strings.HasPrefix(cleanPath, cleanRoot+string(filepath.Separator)) {
			return true
		}
	}
	return false
}
