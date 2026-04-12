package localllm

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/roelfdiedericks/goclaw/internal/paths"
)

type StorageLayout struct {
	RootDir          string
	BinRootDir       string
	ModelsRootDir    string
	DownloadsRootDir string
}

func LocalStorageLayout() (StorageLayout, error) {
	root, err := paths.DataPath("local")
	if err != nil {
		return StorageLayout{}, err
	}

	return StorageLayout{
		RootDir:          root,
		BinRootDir:       filepath.Join(root, "bin"),
		ModelsRootDir:    filepath.Join(root, "models"),
		DownloadsRootDir: filepath.Join(root, "downloads"),
	}, nil
}

func RuntimeInstallDir(version string, osFlavor OSFlavor, arch Arch, backend Backend) (string, error) {
	layout, err := LocalStorageLayout()
	if err != nil {
		return "", err
	}
	key := runtimeArtifactKey(version, osFlavor, arch, backend)
	return filepath.Join(layout.BinRootDir, "llama.cpp", key), nil
}

func RuntimeBinaryPath(version string, osFlavor OSFlavor, arch Arch, backend Backend) (string, error) {
	installDir, err := RuntimeInstallDir(version, osFlavor, arch, backend)
	if err != nil {
		return "", err
	}

	name := "llama-server"
	if osFlavor == OSWindows {
		name += ".exe"
	}
	return filepath.Join(installDir, name), nil
}

func ManagedModelDir(modelID string) (string, error) {
	layout, err := LocalStorageLayout()
	if err != nil {
		return "", err
	}
	return filepath.Join(layout.ModelsRootDir, modelID), nil
}

func ManagedModelPath(spec ManagedModelSpec) (string, error) {
	dir, err := ManagedModelDir(spec.ID)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, spec.PreferredFilename), nil
}

func ManagedModelMMProjPath(spec ManagedModelSpec) (string, error) {
	if strings.TrimSpace(spec.MMProjFilename) == "" {
		return "", nil
	}
	dir, err := ManagedModelDir(spec.ID)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, spec.MMProjFilename), nil
}

func runtimeArtifactKey(version string, osFlavor OSFlavor, arch Arch, backend Backend) string {
	cleanVersion := strings.TrimSpace(version)
	if cleanVersion == "" {
		cleanVersion = "unknown"
	}
	return fmt.Sprintf("%s-%s-%s-%s", cleanVersion, osFlavor, arch, backend)
}
