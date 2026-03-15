package sandbox

// ApplyUserSandboxOverride applies the per-user sandbox override to an already
// evaluated global/category sandbox decision.
func ApplyUserSandboxOverride(categoryEnabled bool, userSandbox bool) bool {
	if !categoryEnabled {
		return false
	}
	return userSandbox
}
