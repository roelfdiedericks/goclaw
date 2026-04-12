package localllm

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

type ManagedSpec struct {
	RuntimeVersion string
	ModelID        string
	Host           string
	Port           int
	ContextSize    int
	ModelAlias     string
}

type ManagerStatus struct {
	Configured           bool
	RuntimeVersion       string
	ModelID              string
	ModelPath            string
	MMProjPath           string
	RuntimePath          string
	Backend              Backend
	SystemProfile        SystemProfile
	LastError            string
	EffectiveContextSize int `json:"effectiveContextSize,omitempty"`
	Server               ServerStatus
}

type Manager struct {
	mu     sync.RWMutex
	spec   ManagedSpec
	status ManagerStatus
	server *ManagedServer
}

var (
	globalManager     = &Manager{}
	globalManagerOnce sync.Once
)

func GetManager() *Manager {
	globalManagerOnce.Do(func() {
		globalManager = &Manager{}
	})
	return globalManager
}

func (m *Manager) EnsureRuntime(ctx context.Context, spec ManagedSpec) (ManagerStatus, error) {
	return m.ensureRuntime(ctx, spec, nil)
}

func (m *Manager) EnsureRuntimeWithProgress(ctx context.Context, spec ManagedSpec, progress func(string, string, int)) (ManagerStatus, error) {
	return m.ensureRuntime(ctx, spec, progress)
}

func (m *Manager) ensureRuntime(ctx context.Context, spec ManagedSpec, progress func(string, string, int)) (ManagerStatus, error) {
	current := m.Status()
	m.mu.RLock()
	currentServer := m.server
	m.mu.RUnlock()
	if currentServer != nil && current.Configured && current.Server.PID > 0 && current.ModelID == specOrCurrentModelID(spec, m.spec) {
		return current, nil
	}

	reportProgress(progress, "resolving", "Resolving managed local runtime settings", 5)
	resolved, err := m.resolveSpec(ctx, spec)
	if err != nil {
		m.recordError(err)
		return m.Status(), err
	}

	profile := DetectSystemProfile()
	reportProgress(progress, "runtime_download", "Downloading or reusing llama.cpp runtime", 20)
	runtimePath, err := DownloadRuntime(ctx, resolved.RuntimeVersion, profile.OSFlavor, profile.Arch, profile.Recommended)
	if err != nil {
		err = fmt.Errorf("download runtime for %s/%s backend=%s: %w", profile.OSFlavor, profile.Arch, profile.Recommended, err)
		m.recordError(err)
		return m.Status(), err
	}

	modelSpec, err := ManagedModelByID(resolved.ModelID)
	if err != nil {
		m.recordError(err)
		return m.Status(), err
	}
	reportProgress(progress, "model_download", "Downloading or reusing managed model files", 55)
	modelPath, err := DownloadManagedModel(ctx, modelSpec)
	if err != nil {
		m.recordError(err)
		return m.Status(), err
	}
	mmprojPath, err := ManagedModelMMProjPath(modelSpec)
	if err != nil {
		m.recordError(err)
		return m.Status(), err
	}

	effectiveCtx := deriveManagedEffectiveContext(resolved.ContextSize, modelPath, modelSpec.FallbackContextTokens)
	L_debug("localllm: effective context size", "tokens", effectiveCtx, "modelID", resolved.ModelID, "override", resolved.ContextSize)

	reportProgress(progress, "persisting", "Saving managed local runtime state", 90)
	m.mu.Lock()
	m.spec = resolved
	m.status.Configured = true
	m.status.RuntimeVersion = resolved.RuntimeVersion
	m.status.ModelID = resolved.ModelID
	m.status.ModelPath = modelPath
	m.status.MMProjPath = mmprojPath
	m.status.RuntimePath = runtimePath
	m.status.Backend = profile.Recommended
	m.status.SystemProfile = profile
	m.status.EffectiveContextSize = effectiveCtx
	m.status.LastError = ""
	if m.server != nil {
		m.status.Server = m.server.Status()
	}
	out := m.status
	m.mu.Unlock()
	_ = m.persistStatus(out)

	L_info("localllm: runtime ensured", "version", out.RuntimeVersion, "modelID", out.ModelID, "backend", out.Backend)
	return out, nil
}

func (m *Manager) Start(ctx context.Context, spec ManagedSpec) (ManagerStatus, error) {
	return m.start(ctx, spec, nil)
}

func (m *Manager) StartWithProgress(ctx context.Context, spec ManagedSpec, progress func(string, string, int)) (ManagerStatus, error) {
	return m.start(ctx, spec, progress)
}

func (m *Manager) start(ctx context.Context, spec ManagedSpec, progress func(string, string, int)) (ManagerStatus, error) {
	status, err := m.ensureRuntime(ctx, spec, progress)
	if err != nil {
		return status, err
	}

	m.mu.Lock()
	currentSpec := m.spec
	currentServer := m.server
	modelSpec, specErr := ManagedModelByID(currentSpec.ModelID)
	fallbackCtx := 0
	if specErr == nil {
		fallbackCtx = modelSpec.FallbackContextTokens
	} else {
		L_warn("localllm: managed model spec missing for context sizing", "modelID", currentSpec.ModelID, "error", specErr)
	}
	effectiveCtx := deriveManagedEffectiveContext(currentSpec.ContextSize, status.ModelPath, fallbackCtx)
	m.status.EffectiveContextSize = effectiveCtx
	m.mu.Unlock()

	if currentServer != nil && currentServer.Status().State == "running" {
		current := currentServer.Status()
		if current.Endpoint == serverEndpoint(defaultHost(currentSpec.Host), currentSpec.Port) && currentSpec.ModelID == specOrCurrentModelID(spec, currentSpec) {
			out := m.Status()
			_ = m.persistStatus(out)
			return out, nil
		}
		stopCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		if err := currentServer.Stop(stopCtx); err != nil {
			L_warn("localllm: failed to stop existing server before restart", "error", err)
		}
	}

	server := NewManagedServer(ServerConfig{
		BinaryPath:   status.RuntimePath,
		ModelPath:    status.ModelPath,
		MMProjPath:   status.MMProjPath,
		ModelID:      status.ModelID,
		Alias:        currentSpec.ModelAlias,
		Host:         defaultHost(currentSpec.Host),
		Port:         currentSpec.Port,
		ContextSize:  effectiveCtx,
		Backend:      status.Backend,
		AutoRestart:  true,
		ReadyTimeout: 60 * time.Second,
	})
	reportProgress(progress, "server_start", "Starting managed llama.cpp server", 95)
	if err := server.Start(ctx); err != nil {
		m.mu.Lock()
		m.status.Server = server.Status()
		m.status.LastError = err.Error()
		m.server = server
		out := m.status
		m.mu.Unlock()
		_ = m.persistStatus(out)
		return out, err
	}

	m.mu.Lock()
	m.server = server
	m.status.Server = server.Status()
	m.status.LastError = ""
	out := m.status
	m.mu.Unlock()
	_ = m.persistStatus(out)
	return out, nil
}

func (m *Manager) Stop(ctx context.Context) error {
	m.mu.Lock()
	server := m.server
	status := m.status
	m.mu.Unlock()
	hasDeadline := false
	deadlineIn := time.Duration(0)
	if deadline, ok := ctx.Deadline(); ok {
		hasDeadline = true
		deadlineIn = time.Until(deadline).Round(time.Millisecond)
	}
	L_info("localllm: manager stop requested",
		"modelID", status.ModelID,
		"endpoint", status.Server.Endpoint,
		"pid", status.Server.PID,
		"inMemoryServer", server != nil,
		"hasDeadline", hasDeadline,
		"deadlineIn", deadlineIn,
	)
	if server == nil {
		status := m.Status()
		ownedState, err := loadOwnedProcessState()
		if err != nil {
			if status.Server.PID == 0 {
				L_info("localllm: manager stop no-op", "reason", "no managed server or ownership record")
				return nil
			}
			return OwnershipConflictError{
				Endpoint: status.Server.Endpoint,
				Reason:   "managed process exists without a GoClaw ownership record",
			}
		}
		if ownerProcessAlive(ownedState) && ownedState.OwnerPID != os.Getpid() {
			return OwnershipConflictError{
				Endpoint: ownedState.Endpoint,
				Reason:   fmt.Sprintf("owned by running GoClaw process pid %d", ownedState.OwnerPID),
			}
		}
		if pidExists(ownedState.PID) {
			L_info("localllm: manager stopping owned llama-server",
				"pid", ownedState.PID,
				"endpoint", ownedState.Endpoint,
				"ownerPID", ownedState.OwnerPID,
			)
			if err := stopPID(ctx, ownedState.PID); err != nil {
				L_warn("localllm: manager stop via ownership record failed",
					"pid", ownedState.PID,
					"endpoint", ownedState.Endpoint,
					"error", err,
				)
				return err
			}
		}
		status.Server.State = "stopped"
		status.Server.Healthy = false
		status.Server.PID = 0
		status.LastError = ""
		if err := clearOwnedProcessState(); err != nil {
			return err
		}
		if err := m.persistStatus(status); err != nil {
			return err
		}
		L_info("localllm: manager stop completed via ownership record",
			"endpoint", status.Server.Endpoint,
		)
		return nil
	}
	if err := server.Stop(ctx); err != nil {
		L_warn("localllm: manager stop failed", "error", err)
		return err
	}
	m.mu.Lock()
	m.status.Server = server.Status()
	m.status.LastError = ""
	out := m.status
	m.mu.Unlock()
	if err := m.persistStatus(out); err != nil {
		return err
	}
	L_info("localllm: manager stop completed",
		"endpoint", out.Server.Endpoint,
		"state", out.Server.State,
	)
	return nil
}

func (m *Manager) SelectModel(modelID string) (ManagerStatus, error) {
	modelSpec, err := ManagedModelByID(modelID)
	if err != nil {
		return m.Status(), err
	}
	m.mu.Lock()
	m.spec.ModelID = modelSpec.ID
	m.status.ModelID = modelSpec.ID
	out := m.status
	m.mu.Unlock()
	return out, nil
}

func (m *Manager) Status() ManagerStatus {
	m.mu.RLock()
	out := m.status
	server := m.server
	m.mu.RUnlock()
	if !out.Configured {
		if persisted, err := loadPersistedStatus(); err == nil {
			out = persisted
		}
	}
	if server != nil {
		out.Server = server.Status()
	} else if out.Server.Endpoint != "" {
		healthy, _ := serverHealthy(context.Background(), out.Server.Endpoint)
		ownedState, ownErr := loadOwnedProcessState()
		ownedValid := ownErr == nil && endpointMatchesOwnedPort(out.Server.Endpoint, ownedState) && validateOwnedProcessState(ownedState, out.Server.Endpoint) == nil
		if ownedValid {
			out.Server.PID = ownedState.PID
			if ownerProcessAlive(ownedState) || ownedState.OwnerPID == os.Getpid() {
				out.Server.Healthy = healthy
				if healthy {
					out.Server.State = "running"
				}
			} else if pidExists(ownedState.PID) {
				out.Server.State = "orphaned"
				out.Server.Healthy = false
				out.LastError = fmt.Sprintf("owned llama-server pid %d was left behind by dead GoClaw owner pid %d", ownedState.PID, ownedState.OwnerPID)
			}
		} else {
			out.Server.Healthy = false
			host, port, ok := parseEndpointHostPort(out.Server.Endpoint)
			if healthy || (ok && portInUse(defaultHost(host), port)) {
				out.Server.State = "conflict"
				if ownErr == nil && ownerProcessAlive(ownedState) {
					out.LastError = fmt.Sprintf("managed llama.cpp endpoint is owned by another running GoClaw process pid %d", ownedState.OwnerPID)
				} else {
					out.LastError = fmt.Sprintf("foreign listener already bound to %s", out.Server.Endpoint)
				}
			} else if out.Server.PID > 0 && !pidExists(out.Server.PID) {
				out.Server.State = "stopped"
				out.Server.PID = 0
			}
		}
		_ = m.persistStatus(out)
	}
	return out
}

func (m *Manager) resolveSpec(ctx context.Context, spec ManagedSpec) (ManagedSpec, error) {
	resolved := spec
	if resolved.ModelID == "" {
		catalog := ManagedModelCatalog()
		if len(catalog) == 0 {
			return ManagedSpec{}, fmt.Errorf("managed model catalog is empty")
		}
		resolved.ModelID = catalog[0].ID
	}
	if _, err := ManagedModelByID(resolved.ModelID); err != nil {
		return ManagedSpec{}, err
	}
	if resolved.Host == "" {
		resolved.Host = "127.0.0.1"
	}
	if resolved.Port == 0 {
		resolved.Port = 8080
	}
	if resolved.RuntimeVersion == "" {
		version, err := LatestRuntimeVersion(ctx)
		if err != nil {
			return ManagedSpec{}, fmt.Errorf("resolve llama.cpp runtime version: %w", err)
		}
		resolved.RuntimeVersion = version
	}
	return resolved, nil
}

func defaultHost(host string) string {
	if host == "" {
		return "127.0.0.1"
	}
	return host
}

func specOrCurrentModelID(spec, current ManagedSpec) string {
	if spec.ModelID != "" {
		return spec.ModelID
	}
	return current.ModelID
}

func (m *Manager) persistStatus(status ManagerStatus) error {
	path, err := managerStatePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(status, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func loadPersistedStatus() (ManagerStatus, error) {
	path, err := managerStatePath()
	if err != nil {
		return ManagerStatus{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ManagerStatus{}, err
	}
	var status ManagerStatus
	if err := json.Unmarshal(data, &status); err != nil {
		return ManagerStatus{}, err
	}
	return status, nil
}

func managerStatePath() (string, error) {
	layout, err := LocalStorageLayout()
	if err != nil {
		return "", err
	}
	return filepath.Join(layout.RootDir, "state", "llamacpp.json"), nil
}

func stopPID(ctx context.Context, pid int) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	L_debug("localllm: stopPID sending SIGTERM", "pid", pid)
	if err := proc.Signal(syscall.SIGTERM); err != nil && !processAlreadyExited(err) {
		return err
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		if !pidExists(pid) {
			L_debug("localllm: stopPID observed exit", "pid", pid)
			return nil
		}
		select {
		case <-ctx.Done():
			L_warn("localllm: stopPID timed out", "pid", pid, "error", ctx.Err())
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func pidExists(pid int) bool {
	if pid <= 0 {
		return false
	}
	if processIsZombie(pid) {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

func processIsZombie(pid int) bool {
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return false
	}
	parts := strings.Fields(string(data))
	return len(parts) > 2 && parts[2] == "Z"
}

func (m *Manager) recordError(err error) {
	if err == nil {
		return
	}
	m.mu.Lock()
	m.status.LastError = err.Error()
	out := m.status
	m.mu.Unlock()
	_ = m.persistStatus(out)
}

func reportProgress(progress func(string, string, int), phase, message string, percent int) {
	if progress != nil {
		progress(phase, message, percent)
	}
}
