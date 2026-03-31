package release

import (
	"bufio"
	"bytes"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	unreleasedHeader = "## [Unreleased]"
	releaseHeaderRE  = regexp.MustCompile(`^## \[([0-9]+\.[0-9]+\.[0-9]+)\](?: ([a-z]+) - ([0-9]{4}-[0-9]{2}-[0-9]{2}))?$`)
	prereleaseTagRE  = regexp.MustCompile(`^v([0-9]+\.[0-9]+\.[0-9]+)-([a-z]+)\.([0-9]+)$`)
	stableTagRE      = regexp.MustCompile(`^v([0-9]+\.[0-9]+\.[0-9]+)$`)
)

type ReleaseEntry struct {
	Version   string
	Channel   string
	Date      string
	RawHeader string
	Body      []string
}

type Changelog struct {
	Preamble       []string
	HasUnreleased  bool
	UnreleasedBody []string
	Releases       []ReleaseEntry
	Warnings       []string
}

type ReleaseState struct {
	CurrentVersion string
	CurrentChannel string
	CurrentDate    string
	NextVersion    string
	ComputedTag    string
	IsPrerelease   bool
	Warnings       []string
}

func Parse(data []byte) (*Changelog, error) {
	ch := &Changelog{}
	scanner := bufio.NewScanner(bytes.NewReader(data))

	type sectionKind string
	const (
		sectionPreamble   sectionKind = "preamble"
		sectionUnreleased sectionKind = "unreleased"
		sectionRelease    sectionKind = "release"
	)

	kind := sectionPreamble
	var current *ReleaseEntry

	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case line == unreleasedHeader:
			kind = sectionUnreleased
			ch.HasUnreleased = true
			current = nil
			continue
		case releaseHeaderRE.MatchString(line):
			m := releaseHeaderRE.FindStringSubmatch(line)
			entry := ReleaseEntry{
				Version:   m[1],
				Channel:   m[2],
				Date:      m[3],
				RawHeader: line,
			}
			ch.Releases = append(ch.Releases, entry)
			current = &ch.Releases[len(ch.Releases)-1]
			kind = sectionRelease
			if current.Channel == "" || current.Date == "" {
				ch.Warnings = append(ch.Warnings, fmt.Sprintf("release entry %s is missing channel or date", current.Version))
			}
			continue
		}

		switch kind {
		case sectionPreamble:
			ch.Preamble = append(ch.Preamble, line)
		case sectionUnreleased:
			ch.UnreleasedBody = append(ch.UnreleasedBody, line)
		case sectionRelease:
			if current != nil {
				current.Body = append(current.Body, line)
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return ch, nil
}

func (c *Changelog) LatestRelease() *ReleaseEntry {
	if c == nil || len(c.Releases) == 0 {
		return nil
	}
	return &c.Releases[0]
}

func (c *Changelog) CurrentState(existingTags []string) (ReleaseState, error) {
	state := ReleaseState{
		Warnings: append([]string{}, c.Warnings...),
	}
	latest := c.LatestRelease()
	if latest == nil {
		state.CurrentVersion = "0.0.0"
		state.CurrentChannel = "beta"
		state.NextVersion = "0.0.1"
		state.ComputedTag = computeTag(state.NextVersion, state.CurrentChannel, existingTags)
		state.IsPrerelease = state.CurrentChannel != "stable"
		state.Warnings = append(state.Warnings, "no release entries found; defaulting to 0.0.1 beta")
		return state, nil
	}

	state.CurrentVersion = latest.Version
	state.CurrentDate = latest.Date
	state.CurrentChannel = latest.Channel
	if state.CurrentChannel == "" {
		for _, entry := range c.Releases[1:] {
			if entry.Channel != "" {
				state.CurrentChannel = entry.Channel
				state.Warnings = append(state.Warnings, fmt.Sprintf("latest release %s has no channel; recovered %q from older entry", latest.Version, state.CurrentChannel))
				break
			}
		}
	}
	if state.CurrentChannel == "" {
		state.CurrentChannel = "beta"
		state.Warnings = append(state.Warnings, fmt.Sprintf("latest release %s has no channel; defaulting to beta", latest.Version))
	}

	next, err := incrementPatch(latest.Version)
	if err != nil {
		return state, err
	}
	state.NextVersion = next
	state.ComputedTag = computeTag(state.NextVersion, state.CurrentChannel, existingTags)
	state.IsPrerelease = state.CurrentChannel != "stable"
	return state, nil
}

func (c *Changelog) RenderWithNewEntry(version, channel, date string) ([]byte, []string, error) {
	if version == "" {
		return nil, nil, fmt.Errorf("version is required")
	}
	if channel == "" {
		channel = "beta"
	}
	if date == "" {
		date = time.Now().Format("2006-01-02")
	}

	warnings := []string{}
	var out []string
	out = append(out, c.Preamble...)
	if len(out) > 0 && out[len(out)-1] != "" {
		out = append(out, "")
	}

	if !c.HasUnreleased {
		warnings = append(warnings, "recreated missing Unreleased section")
	}
	out = append(out, unreleasedHeader, "")
	out = append(out, fmt.Sprintf("## [%s] %s - %s", version, channel, date))
	newReleaseBody := c.UnreleasedBody
	if !hasMeaningfulBody(newReleaseBody) {
		newReleaseBody = []string{"", "-", ""}
	}
	out = append(out, newReleaseBody...)
	if len(newReleaseBody) == 0 || newReleaseBody[len(newReleaseBody)-1] != "" {
		out = append(out, "")
	}
	for _, entry := range c.Releases {
		out = append(out, entry.RawHeader)
		out = append(out, entry.Body...)
		if len(entry.Body) == 0 || entry.Body[len(entry.Body)-1] != "" {
			out = append(out, "")
		}
	}

	return []byte(strings.Join(out, "\n") + "\n"), warnings, nil
}

func hasMeaningfulBody(lines []string) bool {
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			return true
		}
	}
	return false
}

func (c *Changelog) ReleaseNotesForTag(tag string) (string, []string, error) {
	version, channel, err := parseTag(tag)
	if err != nil {
		return "", nil, err
	}

	for _, entry := range c.Releases {
		if entry.Version != version {
			continue
		}
		if channel == "stable" || entry.Channel == "" || entry.Channel == channel {
			return strings.TrimSpace(strings.Join(entry.Body, "\n")), c.Warnings, nil
		}
	}

	return "", c.Warnings, fmt.Errorf("no changelog entry found for tag %s", tag)
}

func incrementPatch(version string) (string, error) {
	parts := strings.Split(version, ".")
	if len(parts) != 3 {
		return "", fmt.Errorf("invalid semantic version %q", version)
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return "", err
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return "", err
	}
	patch, err := strconv.Atoi(parts[2])
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%d.%d.%d", major, minor, patch+1), nil
}

func computeTag(version, channel string, existingTags []string) string {
	if channel == "" || channel == "stable" {
		return "v" + version
	}

	next := 1
	prefix := fmt.Sprintf("v%s-%s.", version, channel)
	for _, tag := range existingTags {
		if !strings.HasPrefix(tag, prefix) {
			continue
		}
		suffix := strings.TrimPrefix(tag, prefix)
		n, err := strconv.Atoi(suffix)
		if err != nil {
			continue
		}
		if n >= next {
			next = n + 1
		}
	}
	return fmt.Sprintf("v%s-%s.%d", version, channel, next)
}

func parseTag(tag string) (version string, channel string, err error) {
	if m := prereleaseTagRE.FindStringSubmatch(tag); m != nil {
		return m[1], m[2], nil
	}
	if m := stableTagRE.FindStringSubmatch(tag); m != nil {
		return m[1], "stable", nil
	}
	return "", "", fmt.Errorf("invalid tag format: %s", tag)
}
