package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/roelfdiedericks/goclaw/internal/release"
)

func main() {
	if len(os.Args) < 2 {
		fatalf("usage: releasetool <command>")
	}

	switch os.Args[1] {
	case "current":
		runCurrent(os.Args[2:])
	case "validate":
		runValidate(os.Args[2:])
	case "next-version":
		runNextVersion()
	case "next-tag":
		runNextTag()
	case "changelog":
		runChangelog(os.Args[2:])
	case "release-notes":
		runReleaseNotes(os.Args[2:])
	default:
		fatalf("unknown command: %s", os.Args[1])
	}
}

func runCurrent(args []string) {
	fs := flag.NewFlagSet("current", flag.ExitOnError)
	field := fs.String("field", "", "field to print: version, channel, date, next-version, tag")
	jsonOut := fs.Bool("json", false, "print JSON")
	fs.Parse(args)

	state := mustState()
	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(state); err != nil {
			fatalf("encode json: %v", err)
		}
		return
	}

	switch *field {
	case "":
		fmt.Printf("version=%s channel=%s date=%s next=%s tag=%s\n",
			state.CurrentVersion, state.CurrentChannel, state.CurrentDate, state.NextVersion, state.ComputedTag)
	case "version":
		fmt.Println(state.CurrentVersion)
	case "channel":
		fmt.Println(state.CurrentChannel)
	case "date":
		fmt.Println(state.CurrentDate)
	case "next-version":
		fmt.Println(state.NextVersion)
	case "tag":
		fmt.Println(state.ComputedTag)
	default:
		fatalf("unknown field: %s", *field)
	}
}

func runValidate(args []string) {
	fs := flag.NewFlagSet("validate", flag.ExitOnError)
	releaseMode := fs.Bool("release", false, "validate for release")
	fs.Parse(args)

	ch := mustChangelog()
	state := mustState()
	for _, warning := range state.Warnings {
		fmt.Fprintf(os.Stderr, "warning: %s\n", warning)
	}

	if state.CurrentVersion == "" {
		fatalf("no version in changelog")
	}
	if state.CurrentChannel == "" {
		fatalf("no channel in changelog")
	}
	if *releaseMode {
		if state.CurrentDate == "" {
			fatalf("latest release entry has no date")
		}
	}
	if ch.LatestRelease() == nil {
		fatalf("no release entries found")
	}
	fmt.Println("OK")
}

func runNextVersion() {
	state := mustState()
	fmt.Println(state.NextVersion)
}

func runNextTag() {
	state := mustState()
	fmt.Println(state.ComputedTag)
}

func runChangelog(args []string) {
	if len(args) == 0 || args[0] != "new-entry" {
		fatalf("usage: releasetool changelog new-entry [--channel CHANNEL] [--date YYYY-MM-DD]")
	}

	fs := flag.NewFlagSet("changelog new-entry", flag.ExitOnError)
	channel := fs.String("channel", "", "override release channel")
	date := fs.String("date", "", "override release date")
	fs.Parse(args[1:])

	ch := mustChangelog()
	state := mustState()
	nextChannel := state.CurrentChannel
	if *channel != "" {
		nextChannel = *channel
	}
	if nextChannel == "" {
		nextChannel = "beta"
	}

	rendered, warnings, err := ch.RenderWithNewEntry(state.NextVersion, nextChannel, *date)
	if err != nil {
		fatalf("render changelog: %v", err)
	}

	path := changelogPath()
	if err := os.WriteFile(path, rendered, 0600); err != nil {
		fatalf("write changelog: %v", err)
	}
	for _, warning := range warnings {
		fmt.Fprintf(os.Stderr, "warning: %s\n", warning)
	}
	fmt.Printf("wrote %s\n", path)
}

func runReleaseNotes(args []string) {
	fs := flag.NewFlagSet("release-notes", flag.ExitOnError)
	tag := fs.String("tag", "", "git tag (e.g. v0.1.3 or v0.1.3-beta.1)")
	fs.Parse(args)
	if *tag == "" {
		fatalf("--tag is required")
	}

	ch := mustChangelog()
	notes, warnings, err := ch.ReleaseNotesForTag(*tag)
	if err != nil {
		fatalf("release notes: %v", err)
	}
	for _, warning := range warnings {
		fmt.Fprintf(os.Stderr, "warning: %s\n", warning)
	}
	if strings.TrimSpace(notes) == "" {
		fmt.Printf("Release %s\n", *tag)
		return
	}
	fmt.Println(notes)
}

func mustChangelog() *release.Changelog {
	data, err := os.ReadFile(changelogPath())
	if err != nil {
		fatalf("read changelog: %v", err)
	}
	ch, err := release.Parse(data)
	if err != nil {
		fatalf("parse changelog: %v", err)
	}
	return ch
}

func mustState() release.ReleaseState {
	ch := mustChangelog()
	state, err := ch.CurrentState(gitTags())
	if err != nil {
		fatalf("current state: %v", err)
	}
	return state
}

func changelogPath() string {
	return filepath.Join(".", "CHANGELOG.md")
}

func gitTags() []string {
	cmd := exec.Command("git", "tag", "--list") //nolint:gosec // trusted repo tool invocation
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	var tags []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			tags = append(tags, line)
		}
	}
	return tags
}

func fatalf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "releasetool: "+format+"\n", args...)
	os.Exit(1)
}
