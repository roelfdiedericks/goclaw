package media

import (
	"regexp"
	"strings"
)

// MediaRefPattern matches enriched media refs: {{media:mime:'path'}}
var MediaRefPattern = regexp.MustCompile(`\{\{media:([a-z]+/[a-z0-9.+-]+):'((?:[^'\\]|\\.)*)'\}\}`)

// ContainsMediaRefs checks if text contains any media references
func ContainsMediaRefs(text string) bool {
	return MediaRefPattern.MatchString(text)
}

// MediaSegment represents a segment of text or media in parsed agent output
type MediaSegment struct {
	IsMedia bool
	Text    string // for text segments
	Path    string // for media segments
	Mime    string // for media segments
}

// SplitMediaSegments splits text into text and media segments
func SplitMediaSegments(text string) []MediaSegment {
	var segments []MediaSegment
	lastIndex := 0

	matches := MediaRefPattern.FindAllStringSubmatchIndex(text, -1)
	for _, match := range matches {
		if match[0] > lastIndex {
			textBefore := strings.TrimSpace(text[lastIndex:match[0]])
			if textBefore != "" {
				segments = append(segments, MediaSegment{Text: textBefore})
			}
		}

		mime := text[match[2]:match[3]]
		escapedPath := text[match[4]:match[5]]
		path := UnescapePath(escapedPath)

		segments = append(segments, MediaSegment{
			IsMedia: true,
			Path:    path,
			Mime:    mime,
		})

		lastIndex = match[1]
	}

	if lastIndex < len(text) {
		textAfter := strings.TrimSpace(text[lastIndex:])
		if textAfter != "" {
			segments = append(segments, MediaSegment{Text: textAfter})
		}
	}

	return segments
}
