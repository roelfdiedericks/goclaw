package update

import (
	"os"
	"testing"
	"time"
)

func TestSetPostUpdateMarkerEnvAndReadBack(t *testing.T) {
	t.Cleanup(ClearPostUpdateMarkerEnv)

	info := &UpdateInfo{
		CurrentVersion: "1.2.2",
		NewVersion:     "1.2.3",
		Channel:        "stable",
	}
	at := time.Date(2026, time.March, 24, 12, 34, 56, 0, time.UTC)

	if err := SetPostUpdateMarkerEnv(info, "goclaw_update", at); err != nil {
		t.Fatalf("SetPostUpdateMarkerEnv returned error: %v", err)
	}

	state, err := ReadPostUpdateMarkerFromEnv()
	if err != nil {
		t.Fatalf("ReadPostUpdateMarkerFromEnv returned error: %v", err)
	}
	if state == nil {
		t.Fatal("expected marker state")
	}
	if state.NewVersion != info.NewVersion || state.FromVersion != info.CurrentVersion {
		t.Fatalf("unexpected state versions: %#v", state)
	}
	if state.Tool != "goclaw_update" || state.Channel != "stable" {
		t.Fatalf("unexpected state metadata: %#v", state)
	}
	if !state.Time.Equal(at) {
		t.Fatalf("expected time %s, got %s", at, state.Time)
	}
}

func TestClearPostUpdateMarkerEnvRemovesAllFields(t *testing.T) {
	t.Setenv(EnvPostUpdate, "1")
	t.Setenv(EnvPostUpdateVersion, "1.2.3")
	t.Setenv(EnvPostUpdateFromVersion, "1.2.2")
	t.Setenv(EnvPostUpdateChannel, "stable")
	t.Setenv(EnvPostUpdateNotify, "1")
	t.Setenv(EnvPostUpdateTool, "goclaw_update")
	t.Setenv(EnvPostUpdateTime, "2026-03-24T12:34:56Z")

	ClearPostUpdateMarkerEnv()

	for _, key := range []string{
		EnvPostUpdate,
		EnvPostUpdateVersion,
		EnvPostUpdateFromVersion,
		EnvPostUpdateChannel,
		EnvPostUpdateNotify,
		EnvPostUpdateTool,
		EnvPostUpdateTime,
	} {
		if got := os.Getenv(key); got != "" {
			t.Fatalf("expected %s to be cleared, got %q", key, got)
		}
	}
}
