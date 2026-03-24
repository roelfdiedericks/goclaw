package delegatedrun

// SpawnLimits controls delegated subagent spawning policy.
// Zero values mean "unlimited".
type SpawnLimits struct {
	MaxSpawnDepth              int
	MaxActiveChildrenPerParent int
	MaxConcurrentRuns          int
	DefaultTimeoutSeconds      int
	MaxTimeoutSeconds          int
}

// IsActiveState reports whether a run is still in flight.
func IsActiveState(state RunState) bool {
	return state == RunStateQueued || state == RunStateRunning
}
