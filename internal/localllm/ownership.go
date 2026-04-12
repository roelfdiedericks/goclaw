package localllm

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type OwnershipConflictError struct {
	Endpoint string
	Reason   string
}

func (e OwnershipConflictError) Error() string {
	if strings.TrimSpace(e.Endpoint) == "" {
		return "managed llama.cpp ownership conflict: " + strings.TrimSpace(e.Reason)
	}
	return fmt.Sprintf("managed llama.cpp ownership conflict on %s: %s", e.Endpoint, strings.TrimSpace(e.Reason))
}

type OwnedProcessState struct {
	OwnerPID     int       `json:"ownerPid"`
	PID          int       `json:"pid"`
	Host         string    `json:"host"`
	Port         int       `json:"port"`
	Endpoint     string    `json:"endpoint"`
	BinaryPath   string    `json:"binaryPath"`
	ModelPath    string    `json:"modelPath"`
	RuntimePath  string    `json:"runtimePath"`
	ModelID      string    `json:"modelID"`
	StartedAt    time.Time `json:"startedAt"`
	ManagedBy    string    `json:"managedBy"`
	CommandLine  string    `json:"commandLine,omitempty"`
}

func ownershipStatePath() (string, error) {
	layout, err := LocalStorageLayout()
	if err != nil {
		return "", err
	}
	return filepath.Join(layout.RootDir, "state", "llamacpp-owned.json"), nil
}

func loadOwnedProcessState() (OwnedProcessState, error) {
	path, err := ownershipStatePath()
	if err != nil {
		return OwnedProcessState{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return OwnedProcessState{}, err
	}
	var state OwnedProcessState
	if err := json.Unmarshal(data, &state); err != nil {
		return OwnedProcessState{}, err
	}
	return state, nil
}

func persistOwnedProcessState(state OwnedProcessState) error {
	path, err := ownershipStatePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func clearOwnedProcessState() error {
	path, err := ownershipStatePath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func portInUse(host string, port int) bool {
	if port <= 0 {
		return false
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err == nil {
		_ = ln.Close()
		return false
	}
	return true
}

func validateOwnedProcessState(state OwnedProcessState, expectedEndpoint string) error {
	if state.PID <= 0 {
		return fmt.Errorf("owned process state missing pid")
	}
	if state.OwnerPID <= 0 {
		return fmt.Errorf("owned process state missing owner pid")
	}
	if state.ManagedBy != "goclaw" {
		return fmt.Errorf("owned process state missing goclaw ownership marker")
	}
	if !pidExists(state.PID) {
		return fmt.Errorf("owned process pid %d is not alive", state.PID)
	}
	if expectedEndpoint != "" && state.Endpoint != "" && strings.TrimSpace(state.Endpoint) != strings.TrimSpace(expectedEndpoint) {
		return fmt.Errorf("owned process endpoint mismatch: state=%s expected=%s", state.Endpoint, expectedEndpoint)
	}
	if err := validateProcessFingerprint(state); err != nil {
		return err
	}
	return nil
}

func validateProcessFingerprint(state OwnedProcessState) error {
	if runtime.GOOS != "linux" {
		return nil
	}
	cmdline, err := readProcessCommandLine(state.PID)
	if err != nil {
		return fmt.Errorf("read pid %d command line: %w", state.PID, err)
	}
	if strings.TrimSpace(state.CommandLine) != "" && strings.TrimSpace(cmdline) != strings.TrimSpace(state.CommandLine) {
		return fmt.Errorf("owned process command line mismatch")
	}
	if strings.TrimSpace(state.BinaryPath) != "" && !strings.Contains(cmdline, strings.TrimSpace(state.BinaryPath)) {
		return fmt.Errorf("owned process binary mismatch")
	}
	if state.Port > 0 && !strings.Contains(cmdline, "--port "+strconv.Itoa(state.Port)) {
		return fmt.Errorf("owned process port mismatch")
	}
	return nil
}

func readProcessCommandLine(pid int) (string, error) {
	if pid <= 0 {
		return "", fmt.Errorf("invalid pid %d", pid)
	}
	path := filepath.Join("/proc", strconv.Itoa(pid), "cmdline")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	parts := strings.Split(string(data), "\x00")
	filtered := make([]string, 0, len(parts))
	for _, part := range parts {
		if strings.TrimSpace(part) == "" {
			continue
		}
		filtered = append(filtered, part)
	}
	return strings.Join(filtered, " "), nil
}

func endpointMatchesOwnedPort(endpoint string, state OwnedProcessState) bool {
	host, port, ok := parseEndpointHostPort(endpoint)
	if !ok || state.Port <= 0 {
		return false
	}
	return port == state.Port && defaultHost(host) == defaultHost(state.Host)
}

func sameManagedEndpoint(a, b OwnedProcessState) bool {
	return strings.TrimSpace(defaultHost(a.Host)) == strings.TrimSpace(defaultHost(b.Host)) && a.Port == b.Port
}

func ownerProcessAlive(state OwnedProcessState) bool {
	return state.OwnerPID > 0 && pidExists(state.OwnerPID)
}

func processAlreadyExited(err error) bool {
	return err == nil || err == os.ErrProcessDone || err == syscall.ESRCH
}

func parseEndpointHostPort(endpoint string) (string, int, bool) {
	if strings.TrimSpace(endpoint) == "" {
		return "", 0, false
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return "", 0, false
	}
	port := parsed.Port()
	if port == "" {
		return "", 0, false
	}
	got, err := strconv.Atoi(port)
	if err != nil {
		return "", 0, false
	}
	return parsed.Hostname(), got, true
}
