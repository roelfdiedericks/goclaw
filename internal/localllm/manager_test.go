package localllm

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"
)

func TestManagerStatusLoadsPersistedState(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	// Use a port that is not listening so Status() clears a stale PID instead of reporting conflict.
	endpoint := fmt.Sprintf("http://127.0.0.1:%d", port)

	mgr := &Manager{}
	want := ManagerStatus{
		Configured:     true,
		RuntimeVersion: "b7777",
		ModelID:        "gemma4-e2b",
		RuntimePath:    "/tmp/llama-server",
		ModelPath:      "/tmp/model.gguf",
		Server: ServerStatus{
			State:    "running",
			Endpoint: endpoint,
			PID:      999999, // guaranteed to be absent; status should downgrade to stopped
			Healthy:  false,
		},
	}

	if err := mgr.persistStatus(want); err != nil {
		t.Fatalf("persistStatus returned error: %v", err)
	}

	got := (&Manager{}).Status()
	if !got.Configured {
		t.Fatalf("expected configured status to be loaded from disk")
	}
	if got.RuntimeVersion != want.RuntimeVersion {
		t.Fatalf("expected runtime version %q, got %q", want.RuntimeVersion, got.RuntimeVersion)
	}
	if got.ModelID != want.ModelID {
		t.Fatalf("expected model ID %q, got %q", want.ModelID, got.ModelID)
	}
	if got.Server.PID != 0 {
		t.Fatalf("expected stale pid to be cleared, got %d", got.Server.PID)
	}
	if got.Server.State != "stopped" {
		t.Fatalf("expected stale persisted server to downgrade to stopped, got %q", got.Server.State)
	}
}

func TestManagerStatusMarksForeignListenerConflict(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	mgr := &Manager{}
	endpoint := "http://" + ln.Addr().String()
	want := ManagerStatus{
		Configured:     true,
		RuntimeVersion: "b7777",
		ModelID:        "gemma4-e2b",
		RuntimePath:    "/tmp/llama-server",
		ModelPath:      "/tmp/model.gguf",
		Server: ServerStatus{
			State:    "running",
			Endpoint: endpoint,
		},
	}

	if err := mgr.persistStatus(want); err != nil {
		t.Fatalf("persistStatus returned error: %v", err)
	}

	got := (&Manager{}).Status()
	if got.Server.State != "conflict" {
		t.Fatalf("expected conflict state, got %#v", got.Server)
	}
	if got.LastError == "" {
		t.Fatalf("expected conflict status to include an error message")
	}
}

func TestManagerStopStopsOwnedProcessWithoutInMemoryServer(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	portLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := portLn.Addr().(*net.TCPAddr).Port
	_ = portLn.Close()

	helperArgs := []string{"-test.run=TestManagedServerHelperProcess", "--", "--host", "127.0.0.1", "--port", strconv.Itoa(port), "--model", "/tmp/model.gguf"}
	cmd := exec.Command(os.Args[0], helperArgs...)
	cmd.Env = append(os.Environ(), "GOCLAW_TEST_HELPER=1")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start helper: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})

	endpoint := "http://127.0.0.1:" + strconv.Itoa(port)
	if err := persistOwnedProcessState(OwnedProcessState{
		OwnerPID:    os.Getpid(),
		PID:         cmd.Process.Pid,
		Host:        "127.0.0.1",
		Port:        port,
		Endpoint:    endpoint,
		BinaryPath:  "llama-server",
		ModelPath:   "/tmp/model.gguf",
		RuntimePath: ".",
		ModelID:     "gemma4-e2b",
		StartedAt:   time.Now(),
		ManagedBy:   "goclaw",
	}); err != nil {
		t.Fatalf("persist ownership: %v", err)
	}

	mgr := &Manager{}
	mgr.status = ManagerStatus{
		Configured: true,
		ModelID:    "gemma4-e2b",
		Server: ServerStatus{
			State:    "running",
			Endpoint: endpoint,
			PID:      cmd.Process.Pid,
		},
	}

	stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := mgr.Stop(stopCtx); err != nil {
		t.Fatalf("Manager.Stop returned error: %v", err)
	}

	status := mgr.Status()
	if status.Server.PID != 0 || status.Server.State != "stopped" {
		t.Fatalf("expected stopped status, got %#v", status.Server)
	}
	if _, err := loadOwnedProcessState(); err == nil {
		t.Fatalf("expected ownership state to be cleared")
	}
}
