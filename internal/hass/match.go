package hass

import (
	"regexp"
	"strings"
)

// MatchGlob performs simple glob matching (* matches any characters).
func MatchGlob(pattern, s string) bool {
	// Handle empty pattern
	if pattern == "" {
		return s == ""
	}

	// Convert glob pattern to match logic
	// Supports: * (any chars)
	parts := strings.Split(pattern, "*")
	if len(parts) == 1 {
		// No wildcards
		return pattern == s
	}

	// Check prefix
	if !strings.HasPrefix(s, parts[0]) {
		return false
	}
	s = s[len(parts[0]):]

	// Check middle parts
	for i := 1; i < len(parts)-1; i++ {
		idx := strings.Index(s, parts[i])
		if idx < 0 {
			return false
		}
		s = s[idx+len(parts[i]):]
	}

	// Check suffix
	return strings.HasSuffix(s, parts[len(parts)-1])
}

// MatchRegex performs regex pattern matching.
// Returns true if the pattern matches, false otherwise.
// Returns false on invalid regex patterns.
func MatchRegex(pattern, s string) (bool, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return false, err
	}
	return re.MatchString(s), nil
}

// MatchSubscription checks if an entity ID matches a subscription's pattern or regex.
// Returns true if the entity matches the subscription's filter criteria.
func MatchSubscription(sub *Subscription, entityID string) bool {
	// Check glob pattern first
	if sub.Pattern != "" {
		return MatchGlob(sub.Pattern, entityID)
	}

	// Check regex pattern
	if sub.Regex != "" {
		matched, err := MatchRegex(sub.Regex, entityID)
		if err != nil {
			// Invalid regex - log and skip
			return false
		}
		return matched
	}

	// No pattern specified - match all
	return true
}
