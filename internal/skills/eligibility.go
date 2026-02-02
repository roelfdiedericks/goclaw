package skills

import (
	"os"
	"os/exec"
	"runtime"
)

// IsEligible checks if a skill meets all runtime requirements.
// It checks: OS, required binaries, env vars, and config keys.
func (s *Skill) IsEligible(ctx EligibilityContext) bool {
	// Check if explicitly disabled
	if !s.Enabled {
		s.Eligible = false
		return false
	}

	// Check per-skill config override
	if ctx.SkillConfig != nil && !ctx.SkillConfig.Enabled {
		s.Eligible = false
		return false
	}

	// No metadata means always eligible (basic skill)
	if s.Metadata == nil {
		s.Eligible = true
		return true
	}

	// Check OS
	if !checkOS(s.Metadata.OS, ctx.OS) {
		s.Eligible = false
		return false
	}

	// Check requirements
	if s.Metadata.Requires != nil {
		if !checkRequirements(s.Metadata.Requires, ctx) {
			s.Eligible = false
			return false
		}
	}

	s.Eligible = true
	return true
}

// checkOS verifies the skill is compatible with the current OS.
// Empty OS list means compatible with all.
func checkOS(allowedOS []string, currentOS string) bool {
	if len(allowedOS) == 0 {
		return true
	}

	// Use provided OS or default to runtime
	if currentOS == "" {
		currentOS = runtime.GOOS
	}

	for _, os := range allowedOS {
		if os == currentOS {
			return true
		}
	}

	return false
}

// checkRequirements verifies all requirements are met.
func checkRequirements(req *SkillRequirements, ctx EligibilityContext) bool {
	// Check all required binaries exist
	for _, bin := range req.Bins {
		if !binaryExists(bin) {
			return false
		}
	}

	// Check at least one of anyBins exists
	if len(req.AnyBins) > 0 {
		found := false
		for _, bin := range req.AnyBins {
			if binaryExists(bin) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	// Check required environment variables
	for _, envVar := range req.Env {
		if os.Getenv(envVar) == "" {
			return false
		}
	}

	// Check required config keys
	if len(req.Config) > 0 && ctx.ConfigKeys != nil {
		for _, key := range req.Config {
			if !ctx.ConfigKeys[key] {
				return false
			}
		}
	}

	return true
}

// binaryExists checks if a binary is available in PATH.
func binaryExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// GetMissingRequirements returns a list of unmet requirements.
// Useful for explaining why a skill is not eligible.
func (s *Skill) GetMissingRequirements(ctx EligibilityContext) []string {
	var missing []string

	if s.Metadata == nil {
		return missing
	}

	// Check OS
	if len(s.Metadata.OS) > 0 {
		currentOS := ctx.OS
		if currentOS == "" {
			currentOS = runtime.GOOS
		}
		found := false
		for _, os := range s.Metadata.OS {
			if os == currentOS {
				found = true
				break
			}
		}
		if !found {
			missing = append(missing, "OS: requires "+joinStrings(s.Metadata.OS))
		}
	}

	if s.Metadata.Requires == nil {
		return missing
	}

	req := s.Metadata.Requires

	// Check binaries
	for _, bin := range req.Bins {
		if !binaryExists(bin) {
			missing = append(missing, "binary: "+bin)
		}
	}

	// Check anyBins
	if len(req.AnyBins) > 0 {
		found := false
		for _, bin := range req.AnyBins {
			if binaryExists(bin) {
				found = true
				break
			}
		}
		if !found {
			missing = append(missing, "one of: "+joinStrings(req.AnyBins))
		}
	}

	// Check env vars
	for _, envVar := range req.Env {
		if os.Getenv(envVar) == "" {
			missing = append(missing, "env: "+envVar)
		}
	}

	// Check config keys
	if ctx.ConfigKeys != nil {
		for _, key := range req.Config {
			if !ctx.ConfigKeys[key] {
				missing = append(missing, "config: "+key)
			}
		}
	}

	return missing
}

// joinStrings joins strings with ", "
func joinStrings(strs []string) string {
	if len(strs) == 0 {
		return ""
	}
	if len(strs) == 1 {
		return strs[0]
	}

	result := strs[0]
	for i := 1; i < len(strs); i++ {
		result += ", " + strs[i]
	}
	return result
}
