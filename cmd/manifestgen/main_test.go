package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestBuildManifestEntriesPreservesCurrentForwardHistory(t *testing.T) {
	templatesDir := t.TempDir()
	templatePath := filepath.Join(templatesDir, "AGENTS.md")
	content := "---\nignored: true\n---\n\ncurrent content\n"
	if err := os.WriteFile(templatePath, []byte(content), 0o644); err != nil {
		t.Fatalf("write template: %v", err)
	}
	if err := os.WriteFile(filepath.Join(templatesDir, bootstrapTemplate), []byte("bootstrap"), 0o644); err != nil {
		t.Fatalf("write bootstrap template: %v", err)
	}

	existing := map[string]manifestEntry{
		"AGENTS.md": {
			Current: checksumString("older current"),
			Known:   []string{checksumString("oldest known")},
		},
	}

	entries, err := buildManifestEntries(templatesDir, existing)
	if err != nil {
		t.Fatalf("buildManifestEntries: %v", err)
	}

	entry, ok := entries["AGENTS.md"]
	if !ok {
		t.Fatalf("expected AGENTS.md entry")
	}
	if entry.Current != checksumString("current content\n") {
		t.Fatalf("unexpected current checksum %q", entry.Current)
	}
	if !slices.Contains(entry.Known, checksumString("older current")) {
		t.Fatalf("expected previous current checksum to be preserved, got %#v", entry.Known)
	}
	if !slices.Contains(entry.Known, checksumString("oldest known")) {
		t.Fatalf("expected prior known checksum to be preserved, got %#v", entry.Known)
	}
	if _, ok := entries[bootstrapTemplate]; ok {
		t.Fatalf("did not expect bootstrap template in manifest")
	}
}

func TestRenderGeneratedFileRoundTripsExistingData(t *testing.T) {
	entries := map[string]manifestEntry{
		"SOUL.md": {
			Current: checksumString("current"),
			Known:   []string{checksumString("older")},
		},
	}

	content, err := renderGeneratedFile(entries)
	if err != nil {
		t.Fatalf("renderGeneratedFile: %v", err)
	}

	path := filepath.Join(t.TempDir(), "template_manifest_generated.go")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write generated file: %v", err)
	}

	got, err := readExistingManifestData(path)
	if err != nil {
		t.Fatalf("readExistingManifestData: %v", err)
	}
	if got["SOUL.md"].Current != entries["SOUL.md"].Current {
		t.Fatalf("unexpected current checksum: %#v", got["SOUL.md"])
	}
	if !slices.Equal(got["SOUL.md"].Known, entries["SOUL.md"].Known) {
		t.Fatalf("unexpected known history: got %#v want %#v", got["SOUL.md"].Known, entries["SOUL.md"].Known)
	}
}
