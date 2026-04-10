package localllm

import "testing"

func TestManagerStatusLoadsPersistedState(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	mgr := &Manager{}
	want := ManagerStatus{
		Configured:     true,
		RuntimeVersion: "b7777",
		ModelID:        "gemma4-e2b",
		RuntimePath:    "/tmp/llama-server",
		ModelPath:      "/tmp/model.gguf",
		Server: ServerStatus{
			State:    "running",
			Endpoint: "http://127.0.0.1:8080",
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
