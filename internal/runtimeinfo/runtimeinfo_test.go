package runtimeinfo

import "testing"

func TestSetAndGetLaunchCwd(t *testing.T) {
	old := LaunchCwd()
	SetLaunchCwd("/tmp/goclaw-launch")
	t.Cleanup(func() {
		SetLaunchCwd(old)
	})

	if got := LaunchCwd(); got != "/tmp/goclaw-launch" {
		t.Fatalf("expected launch cwd to round-trip, got %q", got)
	}
}

func TestSetShuttingDown(t *testing.T) {
	shuttingDown = 0
	t.Cleanup(func() {
		shuttingDown = 0
	})

	if IsShuttingDown() {
		t.Fatal("expected default shutdown state to be false")
	}

	SetShuttingDown()

	if !IsShuttingDown() {
		t.Fatal("expected shutdown state to be true after SetShuttingDown")
	}
}
