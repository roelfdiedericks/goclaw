package localllm

import (
	"context"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestManagedServerStartHealthStop(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	origFactory := commandFactory
	commandFactory = func(name string, args ...string) *exec.Cmd {
		helperArgs := append([]string{"-test.run=TestManagedServerHelperProcess", "--"}, args...)
		cmd := exec.Command(os.Args[0], helperArgs...)
		cmd.Env = append(os.Environ(), "GOCLAW_TEST_HELPER=1")
		return cmd
	}
	t.Cleanup(func() { commandFactory = origFactory })

	server := NewManagedServer(ServerConfig{
		BinaryPath:   "llama-server",
		ModelPath:    "/tmp/model.gguf",
		Host:         "127.0.0.1",
		ReadyTimeout: 5 * time.Second,
		Backend:      BackendCPU,
	})

	if err := server.Start(context.Background()); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}

	healthy, err := server.Health(context.Background())
	if err != nil {
		t.Fatalf("Health returned error: %v", err)
	}
	if !healthy {
		t.Fatalf("expected server to be healthy")
	}

	status := server.Status()
	if status.State != "running" || !status.Healthy || status.PID == 0 {
		t.Fatalf("unexpected status %#v", status)
	}

	stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Stop(stopCtx); err != nil {
		t.Fatalf("Stop returned error: %v", err)
	}

	status = server.Status()
	if status.State != "stopped" || status.Healthy {
		t.Fatalf("unexpected stopped status %#v", status)
	}
}

func TestWaitForServerReady(t *testing.T) {
	ready := make(chan struct{})
	srv := &http.Server{
		Addr: "127.0.0.1:0",
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-ready:
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"status":"ok"}`))
			default:
				w.WriteHeader(http.StatusServiceUnavailable)
			}
		}),
	}

	ln, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() { _ = srv.Serve(ln) }()
	defer srv.Shutdown(context.Background())

	go func() {
		time.Sleep(200 * time.Millisecond)
		close(ready)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := waitForServerReady(ctx, "http://"+ln.Addr().String()); err != nil {
		t.Fatalf("waitForServerReady returned error: %v", err)
	}
}

func TestManagedServerArgsIncludesCtxSize(t *testing.T) {
	s := NewManagedServer(ServerConfig{
		BinaryPath:  "llama-server",
		ModelPath:   "/tmp/model.gguf",
		Host:        "127.0.0.1",
		Port:        8080,
		ContextSize: 131072,
		Backend:     BackendCPU,
	})
	args := s.args()
	want := []string{"--ctx-size", "131072"}
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--ctx-size" && args[i+1] == want[1] {
			return
		}
	}
	t.Fatalf("expected %v in args, got %#v", want, args)
}

func TestNextRestartDelay(t *testing.T) {
	if got := nextRestartDelay(0); got != time.Second {
		t.Fatalf("expected 1s, got %v", got)
	}
	if got := nextRestartDelay(time.Second); got != 2*time.Second {
		t.Fatalf("expected 2s, got %v", got)
	}
	if got := nextRestartDelay(20 * time.Second); got != 30*time.Second {
		t.Fatalf("expected capped 30s, got %v", got)
	}
}

func TestRuntimeLaunchEnvAddsRuntimeDir(t *testing.T) {
	key := "LD_LIBRARY_PATH"
	if runtime.GOOS == "darwin" {
		key = "DYLD_LIBRARY_PATH"
	} else if runtime.GOOS == "windows" {
		key = "PATH"
	}
	runtimeDir := filepath.Join(t.TempDir(), "runtime")
	env := runtimeLaunchEnv([]string{"FOO=bar"}, runtimeDir)
	found := ""
	for _, item := range env {
		if strings.HasPrefix(item, key+"=") {
			found = strings.TrimPrefix(item, key+"=")
			break
		}
	}
	if found == "" {
		t.Fatalf("expected %s in environment", key)
	}
	if !strings.Contains(found, runtimeDir) {
		t.Fatalf("expected %s to contain runtime dir %q, got %q", key, runtimeDir, found)
	}
}

func TestManagedServerStartFailsOnForeignListener(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	port := ln.Addr().(*net.TCPAddr).Port
	server := NewManagedServer(ServerConfig{
		BinaryPath:   "llama-server",
		ModelPath:    "/tmp/model.gguf",
		Host:         "127.0.0.1",
		Port:         port,
		ReadyTimeout: 2 * time.Second,
		Backend:      BackendCPU,
	})

	err = server.Start(context.Background())
	if err == nil {
		t.Fatalf("expected start conflict error")
	}
	if _, ok := err.(OwnershipConflictError); !ok {
		t.Fatalf("expected OwnershipConflictError, got %T: %v", err, err)
	}
	status := server.Status()
	if status.State != "conflict" {
		t.Fatalf("expected conflict state, got %#v", status)
	}
}

func TestManagedServerStartReplacesStaleOwnedProcess(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("stale ownership fingerprint test requires linux /proc")
	}
	t.Setenv("HOME", t.TempDir())

	origFactory := commandFactory
	commandFactory = func(name string, args ...string) *exec.Cmd {
		helperArgs := append([]string{"-test.run=TestManagedServerHelperProcess", "--"}, args...)
		cmd := exec.Command(os.Args[0], helperArgs...)
		cmd.Env = append(os.Environ(), "GOCLAW_TEST_HELPER=1")
		return cmd
	}
	t.Cleanup(func() { commandFactory = origFactory })

	portLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := portLn.Addr().(*net.TCPAddr).Port
	_ = portLn.Close()

	staleCmd := commandFactory("llama-server", "--host", "127.0.0.1", "--port", strconv.Itoa(port), "--model", "/tmp/old.gguf")
	if err := staleCmd.Start(); err != nil {
		t.Fatalf("start stale helper: %v", err)
	}
	t.Cleanup(func() {
		_ = staleCmd.Process.Kill()
		_, _ = staleCmd.Process.Wait()
	})
	if err := persistOwnedProcessState(OwnedProcessState{
		OwnerPID:    999999,
		PID:         staleCmd.Process.Pid,
		Host:        "127.0.0.1",
		Port:        port,
		Endpoint:    "http://127.0.0.1:" + strconv.Itoa(port),
		BinaryPath:  "llama-server",
		ModelPath:   "/tmp/old.gguf",
		RuntimePath: ".",
		ModelID:     "old",
		StartedAt:   time.Now(),
		ManagedBy:   "goclaw",
		CommandLine: func() string {
			cmdline, _ := readProcessCommandLine(staleCmd.Process.Pid)
			return cmdline
		}(),
	}); err != nil {
		t.Fatalf("persist ownership: %v", err)
	}

	server := NewManagedServer(ServerConfig{
		BinaryPath:   "llama-server",
		ModelPath:    "/tmp/new.gguf",
		ModelID:      "new",
		Host:         "127.0.0.1",
		Port:         port,
		ReadyTimeout: 5 * time.Second,
		Backend:      BackendCPU,
	})
	if err := server.Start(context.Background()); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Stop(stopCtx)
	})

	status := server.Status()
	if status.PID == 0 || status.PID == staleCmd.Process.Pid {
		t.Fatalf("expected stale pid %d to be replaced, got %#v", staleCmd.Process.Pid, status)
	}
}

func TestManagedServerHelperProcess(t *testing.T) {
	if os.Getenv("GOCLAW_TEST_HELPER") != "1" {
		return
	}

	host := "127.0.0.1"
	port := 0
	for i := 0; i < len(os.Args); i++ {
		if os.Args[i] == "--host" && i+1 < len(os.Args) {
			host = os.Args[i+1]
		}
		if os.Args[i] == "--port" && i+1 < len(os.Args) {
			p, err := strconv.Atoi(os.Args[i+1])
			if err == nil {
				port = p
			}
		}
	}
	if port == 0 {
		os.Exit(2)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	srv := &http.Server{Addr: host + ":" + strconv.Itoa(port), Handler: mux}
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigCh
		_ = srv.Shutdown(context.Background())
	}()

	if err := srv.ListenAndServe(); err != nil && !strings.Contains(err.Error(), "Server closed") {
		os.Exit(3)
	}
	os.Exit(0)
}
