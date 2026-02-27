package skills

import (
	"fmt"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// SourceType identifies the source of a skill for installation
type SourceType string

const (
	SourceTypeEmbedded SourceType = "embedded"
	SourceTypeClawHub  SourceType = "clawhub"
	SourceTypeLocal    SourceType = "local"
)

// Fetcher provides access to skills from a specific source
type Fetcher interface {
	// Type returns the source type
	Type() SourceType

	// List returns all available skill names
	List() ([]string, error)

	// Exists checks if a skill exists in this source
	Exists(name string) bool

	// FetchTo extracts/copies the skill to the destination directory
	FetchTo(name, destDir string) error
}

// EmbeddedFetcher fetches skills from the embedded catalog
type EmbeddedFetcher struct{}

func NewEmbeddedFetcher() *EmbeddedFetcher {
	return &EmbeddedFetcher{}
}

func (f *EmbeddedFetcher) Type() SourceType {
	return SourceTypeEmbedded
}

func (f *EmbeddedFetcher) List() ([]string, error) {
	return ListEmbedded()
}

func (f *EmbeddedFetcher) Exists(name string) bool {
	return SkillExistsInCatalog(name)
}

func (f *EmbeddedFetcher) FetchTo(name, destDir string) error {
	return ExtractSkill(name, destDir)
}

// ClawHubFetcher fetches skills from ClawHub (stub - Phase 2)
type ClawHubFetcher struct{}

func NewClawHubFetcher() *ClawHubFetcher {
	return &ClawHubFetcher{}
}

func (f *ClawHubFetcher) Type() SourceType {
	return SourceTypeClawHub
}

func (f *ClawHubFetcher) List() ([]string, error) {
	L_debug("clawhub: list not implemented yet")
	return nil, fmt.Errorf("ClawHub integration not implemented yet")
}

func (f *ClawHubFetcher) Exists(name string) bool {
	return false
}

func (f *ClawHubFetcher) FetchTo(name, destDir string) error {
	return fmt.Errorf("ClawHub integration not implemented yet")
}

// LocalFetcher fetches skills from a local directory path
type LocalFetcher struct {
	basePath string
}

func NewLocalFetcher(basePath string) *LocalFetcher {
	return &LocalFetcher{basePath: basePath}
}

func (f *LocalFetcher) Type() SourceType {
	return SourceTypeLocal
}

func (f *LocalFetcher) List() ([]string, error) {
	L_debug("local: list not implemented yet", "path", f.basePath)
	return nil, fmt.Errorf("local path fetcher not implemented yet")
}

func (f *LocalFetcher) Exists(name string) bool {
	return false
}

func (f *LocalFetcher) FetchTo(name, destDir string) error {
	return fmt.Errorf("local path fetcher not implemented yet")
}

// GetFetcher returns a fetcher for the given source type
func GetFetcher(sourceType SourceType, localPath string) (Fetcher, error) {
	switch sourceType {
	case SourceTypeEmbedded:
		return NewEmbeddedFetcher(), nil
	case SourceTypeClawHub:
		return NewClawHubFetcher(), nil
	case SourceTypeLocal:
		if localPath == "" {
			return nil, fmt.Errorf("local path required for local source")
		}
		return NewLocalFetcher(localPath), nil
	default:
		return nil, fmt.Errorf("unknown source type: %s", sourceType)
	}
}
