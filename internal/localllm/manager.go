package localllm

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"sync"
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
	Configured      bool
	RuntimeVersion  string
	ModelID         string
	ModelPath       string
	MMProjPath      string
	RuntimePath     string
	Backend         Backend
	SystemProfile   SystemProfile
	LastError       string
	Server          ServerStatus
}

type Manager struct {
	mu      sync.RWMutex
	spec    ManagedSpec
	status  ManagerStatus
	server  *ManagedServer
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
	current := m.Status()
	if current.Configured && current.Server.PID > 0 && current.ModelID == specOrCurrentModelID(spec, m.spec) {
		return current, nil
	}

	resolved, err := m.resolveSpec(ctx, spec)
	if err != nil {
		m.recordError(err)
		return m.Status(), err
	}

	profile := DetectSystemProfile()
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
	status, err := m.EnsureRuntime(ctx, spec)
	if err != nil {
		return status, err
	}

	m.mu.Lock()
	currentSpec := m.spec
	currentServer := m.server
	m.mu.Unlock()

	if currentServer != nil && currentServer.Status().State == "running" {
		current := currentServer.Status()
		if current.Endpoint == serverEndpoint(defaultHost(currentSpec.Host), currentSpec.Port) && currentSpec.ModelID == specOrCurrentModelID(spec, currentSpec) {
			return m.Status(), nil
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
		Alias:        currentSpec.ModelAlias,
		Host:         defaultHost(currentSpec.Host),
		Port:         currentSpec.Port,
		ContextSize:  currentSpec.ContextSize,
		Backend:      status.Backend,
		AutoRestart:  true,
		ReadyTimeout: 60 * time.Second,
	})
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
	m.mu.Unlock()
	if server == nil {
		status := m.Status()
		if status.Server.PID == 0 {
			return nil
		}
		if err := stopPID(ctx, status.Server.PID); err != nil {
			return err
		}
		status.Server.State = "stopped"
		status.Server.Healthy = false
		status.Server.PID = 0
		status.LastError = ""
		return m.persistStatus(status)
	}
	if err := server.Stop(ctx); err != nil {
		return err
	}
	m.mu.Lock()
	m.status.Server = server.Status()
	m.status.LastError = ""
	out := m.status
	m.mu.Unlock()
	return m.persistStatus(out)
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
		out.Server.Healthy = healthy
		if !out.Server.Healthy && out.Server.PID > 0 && !pidExists(out.Server.PID) {
			out.Server.State = "stopped"
			out.Server.PID = 0
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
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		return err
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		if !pidExists(pid) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func pidExists(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
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
