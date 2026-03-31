package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCollectRuntimeStatusMissingConfigReturnsStructuredState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	workdir := t.TempDir()
	if err := os.Chdir(workdir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(cwd)
	})

	status, err := collectRuntimeStatus()
	if err != nil {
		t.Fatalf("collectRuntimeStatus returned error: %v", err)
	}
	if status.Configured {
		t.Fatalf("expected configured=false, got true")
	}
	if status.Running {
		t.Fatalf("expected running=false, got true")
	}
	if status.Version != version {
		t.Fatalf("expected version %q, got %q", version, status.Version)
	}

	expectedConfigPath := filepath.Join(home, ".goclaw", "goclaw.json")
	if status.ConfigPath != expectedConfigPath {
		t.Fatalf("expected config path %q, got %q", expectedConfigPath, status.ConfigPath)
	}
}

func TestRuntimeStatusFieldValueSupportsInstallerFields(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	status := &RuntimeStatus{
		Configured:               true,
		Running:                  true,
		ConfigPath:               "/tmp/goclaw.json",
		DataDir:                  "/tmp/data",
		PidFile:                  "/tmp/data/goclaw.pid",
		LogFile:                  "/tmp/data/goclaw.log",
		Version:                  "0.1.9",
		SupervisorPID:            101,
		GatewayPID:               202,
		StartedAt:                &now,
		Uptime:                   "2m3s",
		UptimeSeconds:            123,
		CrashCount:               4,
		SupervisorStateAvailable: true,
	}

	cases := map[string]string{
		"configured":               "true",
		"running":                  "true",
		"configPath":               "/tmp/goclaw.json",
		"dataDir":                  "/tmp/data",
		"pidFile":                  "/tmp/data/goclaw.pid",
		"logFile":                  "/tmp/data/goclaw.log",
		"version":                  "0.1.9",
		"supervisorPid":            "101",
		"gateway_pid":              "202",
		"uptime":                   "2m3s",
		"uptime-seconds":           "123",
		"crashCount":               "4",
		"supervisorStateAvailable": "true",
	}

	for field, want := range cases {
		got, err := runtimeStatusFieldValue(status, field)
		if err != nil {
			t.Fatalf("field %q returned error: %v", field, err)
		}
		if got != want {
			t.Fatalf("field %q expected %q, got %q", field, want, got)
		}
	}
}

func TestRestartCmdRunStopsWaitsAndStartsWhenRunning(t *testing.T) {
	origLoader := runtimePathsLoader
	origStarter := runtimeStarter
	origStopper := runtimeStopper
	origWaiter := processExitWaiter
	t.Cleanup(func() {
		runtimePathsLoader = origLoader
		runtimeStarter = origStarter
		runtimeStopper = origStopper
		processExitWaiter = origWaiter
	})

	paths := &RuntimePaths{
		DataDir: "/tmp/data",
		PidFile: "/tmp/data/goclaw.pid",
		LogFile: "/tmp/data/goclaw.log",
	}

	var stopCalled, waitCalled, startCalled bool

	runtimePathsLoader = func() (*RuntimePaths, error) {
		return paths, nil
	}
	runtimeStopper = func(got *RuntimePaths, removePIDFile bool) (int, bool, error) {
		stopCalled = true
		if got != paths {
			t.Fatalf("stop got unexpected paths pointer")
		}
		if removePIDFile {
			t.Fatalf("restart should not eagerly remove the pid file before waiting")
		}
		return 4242, true, nil
	}
	processExitWaiter = func(pid int, timeout time.Duration) error {
		waitCalled = true
		if pid != 4242 {
			t.Fatalf("expected pid 4242, got %d", pid)
		}
		if timeout != 10*time.Second {
			t.Fatalf("expected 10s timeout, got %s", timeout)
		}
		return nil
	}
	runtimeStarter = func(got *RuntimePaths) error {
		startCalled = true
		if got != paths {
			t.Fatalf("start got unexpected paths pointer")
		}
		return nil
	}

	if err := (&RestartCmd{}).Run(nil); err != nil {
		t.Fatalf("RestartCmd.Run returned error: %v", err)
	}
	if !stopCalled || !waitCalled || !startCalled {
		t.Fatalf("expected stop/wait/start to be called, got stop=%v wait=%v start=%v", stopCalled, waitCalled, startCalled)
	}
}

func TestRestartCmdRunStartsImmediatelyWhenNotRunning(t *testing.T) {
	origLoader := runtimePathsLoader
	origStarter := runtimeStarter
	origStopper := runtimeStopper
	origWaiter := processExitWaiter
	t.Cleanup(func() {
		runtimePathsLoader = origLoader
		runtimeStarter = origStarter
		runtimeStopper = origStopper
		processExitWaiter = origWaiter
	})

	paths := &RuntimePaths{DataDir: "/tmp/data"}
	var waitCalled, startCalled bool

	runtimePathsLoader = func() (*RuntimePaths, error) {
		return paths, nil
	}
	runtimeStopper = func(got *RuntimePaths, removePIDFile bool) (int, bool, error) {
		return 0, false, nil
	}
	processExitWaiter = func(pid int, timeout time.Duration) error {
		waitCalled = true
		return nil
	}
	runtimeStarter = func(got *RuntimePaths) error {
		startCalled = true
		if got != paths {
			t.Fatalf("start got unexpected paths pointer")
		}
		return nil
	}

	if err := (&RestartCmd{}).Run(nil); err != nil {
		t.Fatalf("RestartCmd.Run returned error: %v", err)
	}
	if waitCalled {
		t.Fatalf("did not expect wait to be called when nothing was running")
	}
	if !startCalled {
		t.Fatalf("expected start to be called")
	}
}
