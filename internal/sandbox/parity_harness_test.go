package sandbox

import (
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	sbruntime "github.com/roelfdiedericks/goclaw/internal/sandbox/runtime"
)

type parityFixture struct {
	home          string
	workspace     string
	backingHome   string
	desktopDir    string
	documentsDir  string
	desktopDoc    string
	secretSSHKey  string
	secretConfig  string
	volumesData   string
	volumesSource string
}

func makeParityFixture(t *testing.T) parityFixture {
	t.Helper()

	home := t.TempDir()
	t.Setenv("HOME", home)

	workspace := filepath.Join(home, "workspace")
	desktopDir := filepath.Join(home, "Desktop")
	documentsDir := filepath.Join(home, "Documents")
	sshDir := filepath.Join(home, ".ssh")
	hiddenCfgDir := filepath.Join(home, ".goclaw")
	backingHome := filepath.Join(t.TempDir(), "sandbox-home")
	volumesData := filepath.Join(t.TempDir(), "sandbox-data")
	volumesSource := filepath.Join(volumesData, "volumes", "Desktop")

	for _, dir := range []string{workspace, desktopDir, documentsDir, sshDir, hiddenCfgDir, backingHome, volumesSource} {
		if err := os.MkdirAll(dir, 0750); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}

	desktopDoc := filepath.Join(desktopDir, "doc.txt")
	secretSSHKey := filepath.Join(sshDir, "id_ed25519")
	secretConfig := filepath.Join(hiddenCfgDir, "goclaw.json")

	if err := os.WriteFile(desktopDoc, []byte("desktop\n"), 0600); err != nil {
		t.Fatalf("write desktop doc: %v", err)
	}
	if err := os.WriteFile(secretSSHKey, []byte("secret-key\n"), 0600); err != nil {
		t.Fatalf("write ssh key: %v", err)
	}
	if err := os.WriteFile(secretConfig, []byte(`{"token":"secret"}`+"\n"), 0600); err != nil {
		t.Fatalf("write hidden config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(volumesSource, "doc.txt"), []byte("vol-doc\n"), 0600); err != nil {
		t.Fatalf("write volumes doc: %v", err)
	}

	return parityFixture{
		home:          home,
		workspace:     workspace,
		backingHome:   backingHome,
		desktopDir:    desktopDir,
		documentsDir:  documentsDir,
		desktopDoc:    desktopDoc,
		secretSSHKey:  secretSSHKey,
		secretConfig:  secretConfig,
		volumesData:   volumesData,
		volumesSource: volumesSource,
	}
}

func makeParityManager(mode string, fx parityFixture) *Manager {
	homeDir := fx.backingHome
	if mode == ModeVolumes || mode == ModeEphemeral {
		homeDir = ""
	}
	return &Manager{
		config: Config{
			General: GeneralConfig{
				Enabled:         true,
				Mode:            mode,
				ExecEnabled:     true,
				BrowserEnabled:  true,
				FileToolsEnabled: true,
			},
		},
		workspaceRoot: fx.workspace,
		homeDir:       homeDir,
		protectedDirs: map[string]string{},
	}
}

func runSandboxedCommand(t *testing.T, command string, opts sbruntime.ExecLaunchOptions) (exitCode int, output string) {
	t.Helper()

	cmd, err := sbruntime.BuildExecCommand(command, opts)
	if err != nil {
		t.Fatalf("build sandbox command: %v", err)
	}
	if cmd == nil {
		t.Skip("sandbox backend unavailable on this host")
	}

	out, runErr := cmd.CombinedOutput()
	if runErr == nil {
		return 0, string(out)
	}
	if exitErr, ok := runErr.(*exec.ExitError); ok {
		return exitErr.ExitCode(), string(out)
	}
	t.Fatalf("sandbox command failed unexpectedly: %v, out=%s", runErr, string(out))
	return -1, string(out)
}

func resetSandboxManagerForTest() {
	instance = nil
	once = sync.Once{}
}
