package localllm

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

type ServerConfig struct {
	BinaryPath   string
	ModelPath    string
	MMProjPath   string
	Alias        string
	Host         string
	Port         int
	ContextSize  int
	Backend      Backend
	AutoRestart  bool
	ReadyTimeout time.Duration
}

type ServerStatus struct {
	State        string
	Endpoint     string
	Healthy      bool
	PID          int
	RestartCount int
	LastError    string
	RecentLogs   string
}

type ManagedServer struct {
	cfg ServerConfig

	mu             sync.RWMutex
	cmd            *exec.Cmd
	waitCh         chan error
	status         ServerStatus
	logBuf         bytes.Buffer
	stopping       bool
	restartBackoff time.Duration
}

var commandFactory = exec.Command

func NewManagedServer(cfg ServerConfig) *ManagedServer {
	host := strings.TrimSpace(cfg.Host)
	if host == "" {
		host = "127.0.0.1"
	}
	if cfg.ReadyTimeout <= 0 {
		cfg.ReadyTimeout = 60 * time.Second
	}
	cfg.Host = host

	return &ManagedServer{
		cfg: cfg,
		status: ServerStatus{
			State:    "stopped",
			Endpoint: serverEndpoint(host, cfg.Port),
		},
	}
}

func (s *ManagedServer) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.cmd != nil && s.cmd.Process != nil {
		s.mu.Unlock()
		return nil
	}
	if s.cfg.Port == 0 {
		port, err := reservePort(s.cfg.Host)
		if err != nil {
			s.mu.Unlock()
			return err
		}
		s.cfg.Port = port
	}
	s.status.Endpoint = serverEndpoint(s.cfg.Host, s.cfg.Port)
	s.status.State = "starting"
	s.status.Healthy = false
	s.stopping = false

	cmd := commandFactory(s.cfg.BinaryPath, s.args()...)
	writer := io.MultiWriter(&s.logBuf)
	cmd.Stdout = writer
	cmd.Stderr = writer
	if err := cmd.Start(); err != nil {
		s.status.State = "error"
		s.status.LastError = err.Error()
		s.mu.Unlock()
		return err
	}
	s.cmd = cmd
	s.waitCh = make(chan error, 1)
	s.status.PID = cmd.Process.Pid
	s.mu.Unlock()

	L_info("localllm: server starting", "pid", cmd.Process.Pid, "endpoint", s.status.Endpoint)
	go s.monitorProcess(cmd, s.waitCh)

	readyCtx, cancel := context.WithTimeout(ctx, s.cfg.ReadyTimeout)
	defer cancel()
	if err := waitForServerReady(readyCtx, s.status.Endpoint); err != nil {
		s.mu.Lock()
		s.status.State = "error"
		s.status.LastError = err.Error()
		s.mu.Unlock()
		return err
	}

	s.mu.Lock()
	s.status.State = "running"
	s.status.Healthy = true
	s.status.LastError = ""
	s.restartBackoff = 0
	s.mu.Unlock()
	L_info("localllm: server ready", "endpoint", s.status.Endpoint, "pid", cmd.Process.Pid)
	return nil
}

func (s *ManagedServer) Stop(ctx context.Context) error {
	s.mu.Lock()
	cmd := s.cmd
	waitCh := s.waitCh
	if cmd == nil || cmd.Process == nil {
		s.status.State = "stopped"
		s.status.Healthy = false
		s.mu.Unlock()
		return nil
	}
	s.stopping = true
	s.status.State = "stopping"
	s.mu.Unlock()

	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
		L_warn("localllm: graceful stop failed, killing", "error", err)
		_ = cmd.Process.Kill()
	}

	select {
	case err := <-waitCh:
		s.mu.Lock()
		s.cmd = nil
		s.waitCh = nil
		s.status.State = "stopped"
		s.status.Healthy = false
		s.status.PID = 0
		s.mu.Unlock()
		return normalizeExitErr(err)
	case <-ctx.Done():
		_ = cmd.Process.Kill()
		return ctx.Err()
	}
}

func (s *ManagedServer) Restart(ctx context.Context) error {
	if err := s.Stop(ctx); err != nil {
		return err
	}
	return s.Start(ctx)
}

func (s *ManagedServer) Health(ctx context.Context) (bool, error) {
	s.mu.RLock()
	endpoint := s.status.Endpoint
	s.mu.RUnlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"/health", nil)
	if err != nil {
		return false, err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK, nil
}

func (s *ManagedServer) Status() ServerStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := s.status
	out.RecentLogs = s.logBuf.String()
	return out
}

func (s *ManagedServer) args() []string {
	args := []string{
		"--host", s.cfg.Host,
		"--port", strconv.Itoa(s.cfg.Port),
		"--model", s.cfg.ModelPath,
	}
	if s.cfg.MMProjPath != "" {
		args = append(args, "--mmproj", s.cfg.MMProjPath)
	}
	if s.cfg.ContextSize > 0 {
		args = append(args, "--ctx-size", strconv.Itoa(s.cfg.ContextSize))
	}
	if s.cfg.Alias != "" {
		args = append(args, "--alias", s.cfg.Alias)
	}
	switch s.cfg.Backend {
	case BackendCPU:
		args = append(args, "--n-gpu-layers", "0")
	default:
		args = append(args, "--n-gpu-layers", "auto")
	}
	return args
}

func (s *ManagedServer) monitorProcess(cmd *exec.Cmd, waitCh chan error) {
	err := normalizeExitErr(cmd.Wait())
	waitCh <- err

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cmd != cmd {
		return
	}
	s.cmd = nil
	s.waitCh = nil
	s.status.PID = 0
	s.status.Healthy = false

	if s.stopping {
		s.status.State = "stopped"
		s.stopping = false
		return
	}

	s.status.State = "crashed"
	if err != nil {
		s.status.LastError = err.Error()
	} else {
		s.status.LastError = "llama-server exited unexpectedly"
	}

	if !s.cfg.AutoRestart {
		return
	}

	delay := nextRestartDelay(s.restartBackoff)
	s.restartBackoff = delay
	s.status.RestartCount++
	s.status.State = "restart-wait"
	L_warn("localllm: server crashed, scheduling restart", "delay", delay, "error", s.status.LastError)

	go func() {
		time.Sleep(delay)
		if err := s.Start(context.Background()); err != nil {
			L_error("localllm: automatic restart failed", "error", err)
		}
	}()
}

func nextRestartDelay(previous time.Duration) time.Duration {
	if previous <= 0 {
		return 1 * time.Second
	}
	next := previous * 2
	if next > 30*time.Second {
		return 30 * time.Second
	}
	return next
}

func waitForServerReady(ctx context.Context, endpoint string) error {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		healthy, err := serverHealthy(ctx, endpoint)
		if err == nil && healthy {
			return nil
		}

		select {
		case <-ctx.Done():
			if err != nil {
				return fmt.Errorf("server did not become ready: %w", err)
			}
			return fmt.Errorf("server did not become ready: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func serverHealthy(ctx context.Context, endpoint string) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"/health", nil)
	if err != nil {
		return false, err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		return true, nil
	case http.StatusServiceUnavailable:
		return false, nil
	default:
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return false, fmt.Errorf("unexpected health status %d: %s", resp.StatusCode, string(body))
	}
}

func reservePort(host string) (int, error) {
	ln, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		return 0, fmt.Errorf("unexpected listener addr %T", ln.Addr())
	}
	return addr.Port, nil
}

func serverEndpoint(host string, port int) string {
	return fmt.Sprintf("http://%s:%d", host, port)
}

func normalizeExitErr(err error) error {
	if err == nil || errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	return err
}
