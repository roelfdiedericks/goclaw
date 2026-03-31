package release

import (
	"strings"
	"testing"
)

const sampleChangelog = `# Changelog

All notable changes to GoClaw will be documented in this file.

## [Unreleased]

## [0.1.3] beta - 2026-03-14

- prerelease note

## [0.1.2] stable - 2026-03-14
- stable note
`

func TestParseCurrentState(t *testing.T) {
	ch, err := Parse([]byte(sampleChangelog))
	if err != nil {
		t.Fatalf("parse changelog: %v", err)
	}

	state, err := ch.CurrentState([]string{"v0.1.4-beta.1"})
	if err != nil {
		t.Fatalf("current state: %v", err)
	}

	if state.CurrentVersion != "0.1.3" {
		t.Fatalf("current version = %q", state.CurrentVersion)
	}
	if state.CurrentChannel != "beta" {
		t.Fatalf("current channel = %q", state.CurrentChannel)
	}
	if state.NextVersion != "0.1.4" {
		t.Fatalf("next version = %q", state.NextVersion)
	}
	if state.ComputedTag != "v0.1.4-beta.2" {
		t.Fatalf("computed tag = %q", state.ComputedTag)
	}
}

func TestMalformedLatestReleaseWarnsAndRecovers(t *testing.T) {
	ch, err := Parse([]byte(`# Changelog

## [Unreleased]

## [0.1.3]
- missing metadata

## [0.1.2] stable - 2026-03-14
- stable note
`))
	if err != nil {
		t.Fatalf("parse changelog: %v", err)
	}

	state, err := ch.CurrentState(nil)
	if err != nil {
		t.Fatalf("current state: %v", err)
	}

	if state.CurrentChannel != "stable" {
		t.Fatalf("recovered channel = %q", state.CurrentChannel)
	}
	if len(state.Warnings) == 0 {
		t.Fatalf("expected recovery warnings")
	}
}

func TestRenderWithRecreatedUnreleased(t *testing.T) {
	ch, err := Parse([]byte(`# Changelog

## [0.1.2] stable - 2026-03-14
- stable note
`))
	if err != nil {
		t.Fatalf("parse changelog: %v", err)
	}

	out, warnings, err := ch.RenderWithNewEntry("0.1.3", "stable", "2026-03-15")
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	text := string(out)
	if !strings.Contains(text, "## [Unreleased]") {
		t.Fatalf("missing recreated Unreleased section")
	}
	if !strings.Contains(text, "## [0.1.3] stable - 2026-03-15") {
		t.Fatalf("missing new release entry")
	}
	if len(warnings) == 0 {
		t.Fatalf("expected warning about recreated Unreleased")
	}
}

func TestRenderWithNewEntryMovesUnreleasedBodyIntoRelease(t *testing.T) {
	ch, err := Parse([]byte(`# Changelog

## [Unreleased]

- first note
- second note

## [0.1.2] stable - 2026-03-14
- stable note
`))
	if err != nil {
		t.Fatalf("parse changelog: %v", err)
	}

	out, warnings, err := ch.RenderWithNewEntry("0.1.3", "stable", "2026-03-15")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}

	text := string(out)
	expected := `## [Unreleased]

## [0.1.3] stable - 2026-03-15

- first note
- second note
`
	if !strings.Contains(text, expected) {
		t.Fatalf("expected unreleased notes to move into new release, got:\n%s", text)
	}
}

func TestRenderWithNewEntrySeedsPlaceholderWhenUnreleasedEmpty(t *testing.T) {
	ch, err := Parse([]byte(sampleChangelog))
	if err != nil {
		t.Fatalf("parse changelog: %v", err)
	}

	out, _, err := ch.RenderWithNewEntry("0.1.4", "beta", "2026-03-15")
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	text := string(out)
	expected := `## [Unreleased]

## [0.1.4] beta - 2026-03-15

-
`
	if !strings.Contains(text, expected) {
		t.Fatalf("expected placeholder bullet for empty unreleased section, got:\n%s", text)
	}
}

func TestReleaseNotesForPrereleaseTag(t *testing.T) {
	ch, err := Parse([]byte(sampleChangelog))
	if err != nil {
		t.Fatalf("parse changelog: %v", err)
	}

	notes, _, err := ch.ReleaseNotesForTag("v0.1.3-beta.1")
	if err != nil {
		t.Fatalf("release notes: %v", err)
	}
	if notes != "- prerelease note" {
		t.Fatalf("notes = %q", notes)
	}
}
