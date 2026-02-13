// Package supervisor manages gateway subprocess lifecycle with auto-restart.
package supervisor

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

const (
	initialBackoff = 1 * time.Second
	maxBackoff     = 5 * time.Minute
	resetThreshold = 5 * time.Minute // Healthy run resets backoff
	outputLines    = 50              // Lines to capture for crash log
)

// State represents the supervisor's current state (persisted to supervisor.json)
type State struct {
	PID         int        `json:"pid"`
	GatewayPID  int        `json:"gateway_pid,omitempty"`
	StartedAt   time.Time  `json:"started_at"`
	CrashCount  int        `json:"crash_count"`
	LastCrashAt *time.Time `json:"last_crash_at,omitempty"`
}

// Supervisor manages gateway subprocess lifecycle
type Supervisor struct {
	dataDir string
	binary  string   // Path to goclaw binary
	args    []string // Additional args to pass to gateway

	state   State
	stateMu sync.Mutex

	// Current gateway process
	cmd   *exec.Cmd
	cmdMu sync.Mutex

	// Output capture
	outputBuf *CircularBuffer

	// Shutdown
	stopCh  chan struct{}
	stopped bool
}

// New creates a new supervisor
func New(dataDir string) *Supervisor {
	binary, _ := os.Executable()

	return &Supervisor{
		dataDir:   dataDir,
		binary:    binary,
		outputBuf: NewCircularBuffer(outputLines),
		stopCh:    make(chan struct{}),
	}
}

// Run starts the supervisor loop
func (s *Supervisor) Run() error {
	// Initialize state
	s.state = State{
		PID:       os.Getpid(),
		StartedAt: time.Now(),
	}
	s.saveState()

	// Set up signal handling
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	go func() {
		<-sigCh
		L_info("supervisor: received shutdown signal")
		s.Stop()
	}()

	L_info("supervisor: started", "pid", s.state.PID)

	backoff := initialBackoff

	for {
		select {
		case <-s.stopCh:
			L_info("supervisor: stopping")
			return nil
		default:
		}

		startTime := time.Now()

		// Spawn gateway subprocess
		exitCode, err := s.runGateway()
		runDuration := time.Since(startTime)

		// Check if we're stopping
		select {
		case <-s.stopCh:
			L_info("supervisor: gateway stopped for shutdown")
			return nil
		default:
		}

		// Clean exit (exit code 0) → stop supervisor
		if exitCode == 0 {
			L_info("supervisor: gateway exited cleanly", "ran_for", runDuration)
			return nil
		}

		// Crash → log and restart with backoff
		s.stateMu.Lock()
		s.state.CrashCount++
		now := time.Now()
		s.state.LastCrashAt = &now
		crashCount := s.state.CrashCount
		s.stateMu.Unlock()
		s.saveState()

		// Log crash
		s.logCrash(startTime, runDuration, exitCode, err, crashCount)

		L_error("supervisor: gateway crashed",
			"exit_code", exitCode,
			"ran_for", runDuration,
			"crash_count", crashCount,
			"backoff", backoff,
		)

		// Reset backoff if it ran long enough (was healthy)
		if runDuration > resetThreshold {
			backoff = initialBackoff
			L_debug("supervisor: backoff reset (healthy run)")
		}

		L_info("supervisor: restarting gateway", "backoff", backoff)

		// Wait with backoff (interruptible)
		select {
		case <-s.stopCh:
			L_info("supervisor: stopping during backoff")
			return nil
		case <-time.After(backoff):
		}

		// Exponential backoff with cap
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

// Stop signals the supervisor to stop
func (s *Supervisor) Stop() {
	s.cmdMu.Lock()
	if s.stopped {
		s.cmdMu.Unlock()
		return
	}
	s.stopped = true
	close(s.stopCh)

	// Forward signal to gateway subprocess if running
	if s.cmd != nil && s.cmd.Process != nil {
		L_debug("supervisor: forwarding SIGTERM to gateway", "pid", s.cmd.Process.Pid)
		s.cmd.Process.Signal(syscall.SIGTERM)
	}
	s.cmdMu.Unlock()
}

// runGateway spawns and waits for the gateway subprocess
func (s *Supervisor) runGateway() (int, error) {
	s.outputBuf.Reset()

	// Build command
	args := append([]string{"gateway"}, s.args...)
	cmd := exec.Command(s.binary, args...) //nolint:gosec // G204: binary is from os.Executable() - self-spawning

	// Capture stdout/stderr
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()

	s.cmdMu.Lock()
	s.cmd = cmd
	s.cmdMu.Unlock()

	// Start process
	if err := cmd.Start(); err != nil {
		return -1, fmt.Errorf("failed to start gateway: %w", err)
	}

	// Update state with gateway PID
	s.stateMu.Lock()
	s.state.GatewayPID = cmd.Process.Pid
	s.stateMu.Unlock()
	s.saveState()

	L_info("supervisor: gateway started", "pid", cmd.Process.Pid)

	// Capture output in background
	var wg sync.WaitGroup
	wg.Add(2)
	go s.captureOutput(stdout, &wg)
	go s.captureOutput(stderr, &wg)

	// Wait for process
	err := cmd.Wait()
	wg.Wait()

	s.cmdMu.Lock()
	s.cmd = nil
	s.cmdMu.Unlock()

	// Clear gateway PID from state
	s.stateMu.Lock()
	s.state.GatewayPID = 0
	s.stateMu.Unlock()
	s.saveState()

	exitCode := 0
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	}

	return exitCode, err
}

// captureOutput reads from a pipe and stores in circular buffer
func (s *Supervisor) captureOutput(r io.Reader, wg *sync.WaitGroup) {
	defer wg.Done()

	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		s.outputBuf.Write(line)
		// Also write to stdout for daemon log
		fmt.Println(line)
	}
}

// logCrash appends a crash entry to crash.log
func (s *Supervisor) logCrash(startTime time.Time, duration time.Duration, exitCode int, err error, crashCount int) {
	crashLogPath := filepath.Join(s.dataDir, "crash.log")

	f, openErr := os.OpenFile(crashLogPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if openErr != nil {
		L_error("supervisor: failed to open crash.log", "error", openErr)
		return
	}
	defer f.Close()

	errStr := ""
	if err != nil {
		errStr = err.Error()
	}

	// Write crash header
	fmt.Fprintf(f, "\n=== CRASH %s ===\n", startTime.Format("2006-01-02 15:04:05"))
	fmt.Fprintf(f, "Crash #:   %d (this session)\n", crashCount)
	fmt.Fprintf(f, "Ran for:   %s\n", duration.Round(time.Second))
	fmt.Fprintf(f, "Exit code: %d\n", exitCode)
	if errStr != "" {
		fmt.Fprintf(f, "Error:     %s\n", errStr)
	}
	fmt.Fprintf(f, "Last %d lines of output:\n", outputLines)
	fmt.Fprintln(f, "---")

	// Write captured output
	for _, line := range s.outputBuf.Lines() {
		fmt.Fprintln(f, line)
	}
	fmt.Fprintln(f, "---")

	L_debug("supervisor: crash logged", "path", crashLogPath)
}

// saveState persists supervisor state to supervisor.json
func (s *Supervisor) saveState() {
	s.stateMu.Lock()
	state := s.state
	s.stateMu.Unlock()

	statePath := filepath.Join(s.dataDir, "supervisor.json")

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		L_error("supervisor: failed to marshal state", "error", err)
		return
	}

	if err := os.WriteFile(statePath, data, 0600); err != nil {
		L_error("supervisor: failed to write state", "error", err)
	}
}

// LoadState reads supervisor state from supervisor.json
func LoadState(dataDir string) (*State, error) {
	statePath := filepath.Join(dataDir, "supervisor.json")

	data, err := os.ReadFile(statePath)
	if err != nil {
		return nil, err
	}

	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}

	return &state, nil
}

// CircularBuffer stores the last N lines
type CircularBuffer struct {
	lines []string
	size  int
	pos   int
	count int
	mu    sync.Mutex
}

// NewCircularBuffer creates a new circular buffer
func NewCircularBuffer(size int) *CircularBuffer {
	return &CircularBuffer{
		lines: make([]string, size),
		size:  size,
	}
}

// Write adds a line to the buffer
func (b *CircularBuffer) Write(line string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.lines[b.pos] = line
	b.pos = (b.pos + 1) % b.size
	if b.count < b.size {
		b.count++
	}
}

// Lines returns all lines in order (oldest first)
func (b *CircularBuffer) Lines() []string {
	b.mu.Lock()
	defer b.mu.Unlock()

	result := make([]string, 0, b.count)

	if b.count < b.size {
		// Buffer not full yet
		result = append(result, b.lines[:b.count]...)
	} else {
		// Buffer full, read from pos to end, then start to pos
		result = append(result, b.lines[b.pos:]...)
		result = append(result, b.lines[:b.pos]...)
	}

	return result
}

// Reset clears the buffer
func (b *CircularBuffer) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.pos = 0
	b.count = 0
}
