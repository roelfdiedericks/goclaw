package localllm

import (
	"context"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestManagedServerStartHealthStop(t *testing.T) {
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
