package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/roelfdiedericks/goclaw/internal/delivery"
	"github.com/roelfdiedericks/goclaw/internal/update"
	"github.com/roelfdiedericks/goclaw/internal/user"
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

type postUpdateGatewayStub struct {
	systemUserIDs   []string
	systemMessages  []delivery.SystemMessage
	invokeSource    string
	invokePurpose   string
	invokeMessage   string
	invokeSuppress  string
	invokeCalls     int
	invokeErr       error
}

func (s *postUpdateGatewayStub) DeliverSystemMessage(ctx context.Context, userID string, msg delivery.SystemMessage) delivery.Report {
	s.systemUserIDs = append(s.systemUserIDs, userID)
	s.systemMessages = append(s.systemMessages, msg)
	return delivery.Report{
		Generated:   true,
		DeliveredTo: 1,
		Results: []delivery.Result{
			{Channel: "test", Attempted: true, Delivered: true},
		},
	}
}

func (s *postUpdateGatewayStub) InvokeAgent(ctx context.Context, source, purpose, message, suppressOn string) error {
	s.invokeCalls++
	s.invokeSource = source
	s.invokePurpose = purpose
	s.invokeMessage = message
	s.invokeSuppress = suppressOn
	return s.invokeErr
}

func testOwnerRegistry(t *testing.T) *user.Registry {
	t.Helper()

	thinking := false
	sandbox := true
	users := user.UsersConfig{
		"owner": {
			Name:          "Owner",
			Role:          "owner",
			Thinking:      &thinking,
			Sandbox:       &sandbox,
			ThinkingLevel: nil,
		},
		"owner2": {
			Name:          "Owner Two",
			Role:          "owner",
			Thinking:      &thinking,
			Sandbox:       &sandbox,
			ThinkingLevel: nil,
		},
		"user": {
			Name:          "Regular User",
			Role:          "user",
			Thinking:      &thinking,
			Sandbox:       &sandbox,
			ThinkingLevel: nil,
		},
	}
	roles := user.RolesConfig{
		"owner": {Tools: "*", Skills: "*", Memory: "full", Transcripts: "all", Commands: true},
		"user":  {Tools: "", Skills: "", Memory: "none", Transcripts: "none", Commands: false},
	}
	return user.NewRegistryFromUsers(users, roles)
}

func TestReadPostUpdateMarkerFromEnvParsesRequiredFields(t *testing.T) {
	t.Setenv(update.EnvPostUpdate, "1")
	t.Setenv(update.EnvPostUpdateVersion, "1.2.3")
	t.Setenv(update.EnvPostUpdateFromVersion, "1.2.2")
	t.Setenv(update.EnvPostUpdateChannel, "stable")
	t.Setenv(update.EnvPostUpdateNotify, "1")
	t.Setenv(update.EnvPostUpdateTool, "goclaw_update")
	t.Setenv(update.EnvPostUpdateTime, "2026-03-24T12:34:56Z")

	state, err := readPostUpdateMarkerFromEnv()
	if err != nil {
		t.Fatalf("readPostUpdateMarkerFromEnv returned error: %v", err)
	}
	if state == nil {
		t.Fatal("expected marker state")
	}
	if state.NewVersion != "1.2.3" || state.FromVersion != "1.2.2" {
		t.Fatalf("unexpected versions: %#v", state)
	}
	if state.Channel != "stable" || state.Tool != "goclaw_update" || !state.Notify {
		t.Fatalf("unexpected marker fields: %#v", state)
	}
	if got := state.Time.UTC().Format(time.RFC3339); got != "2026-03-24T12:34:56Z" {
		t.Fatalf("unexpected marker time %q", got)
	}
}

func TestHandlePostUpdateAfterStartupDeliversAndInvokesAndClearsEnv(t *testing.T) {
	t.Setenv(update.EnvPostUpdate, "1")
	t.Setenv(update.EnvPostUpdateVersion, "1.2.3")
	t.Setenv(update.EnvPostUpdateFromVersion, "1.2.2")
	t.Setenv(update.EnvPostUpdateChannel, "stable")
	t.Setenv(update.EnvPostUpdateNotify, "1")
	t.Setenv(update.EnvPostUpdateTool, "goclaw_update")
	t.Setenv(update.EnvPostUpdateTime, "2026-03-24T12:34:56Z")

	state, err := readPostUpdateMarkerFromEnv()
	if err != nil {
		t.Fatalf("read marker: %v", err)
	}

	stub := &postUpdateGatewayStub{}
	users := testOwnerRegistry(t)
	if err := handlePostUpdateAfterStartup(context.Background(), stub, users, state); err != nil {
		t.Fatalf("handlePostUpdateAfterStartup returned error: %v", err)
	}

	if len(stub.systemUserIDs) != 2 {
		t.Fatalf("expected two owner notifications, got %#v", stub.systemUserIDs)
	}
	if stub.invokeCalls != 1 {
		t.Fatalf("expected one invoke call, got %d", stub.invokeCalls)
	}
	if stub.invokeSource != "post_update" || stub.invokePurpose != "agent" {
		t.Fatalf("unexpected invoke routing: source=%q purpose=%q", stub.invokeSource, stub.invokePurpose)
	}
	if !strings.Contains(stub.invokeMessage, "Previous version: 1.2.2") || !strings.Contains(stub.invokeMessage, "New version: 1.2.3") {
		t.Fatalf("expected version details in prompt, got %q", stub.invokeMessage)
	}
	if got := os.Getenv(update.EnvPostUpdate); got != "" {
		t.Fatalf("expected post-update env to be cleared, got %q", got)
	}
}
