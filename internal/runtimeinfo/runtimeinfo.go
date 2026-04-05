package runtimeinfo

import (
	"sync"
	"sync/atomic"
)

var (
	launchMu  sync.RWMutex
	launchCwd string

	shuttingDown int32
)

// SetLaunchCwd records the process launch directory for later runtime lookups.
func SetLaunchCwd(path string) {
	launchMu.Lock()
	launchCwd = path
	launchMu.Unlock()
}

// LaunchCwd returns the process launch directory recorded at startup.
func LaunchCwd() string {
	launchMu.RLock()
	defer launchMu.RUnlock()
	return launchCwd
}

// SetShuttingDown marks the application as shutting down.
func SetShuttingDown() {
	atomic.StoreInt32(&shuttingDown, 1)
}

// IsShuttingDown returns true if the application is shutting down.
func IsShuttingDown() bool {
	return atomic.LoadInt32(&shuttingDown) == 1
}
