package seatbelt

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

func FindSandboxExec(customPath string) (string, error) {
	if customPath != "" {
		if _, err := os.Stat(customPath); err == nil {
			return customPath, nil
		}
		return "", fmt.Errorf("sandbox-exec not found at custom path %s", customPath)
	}
	path, err := exec.LookPath("sandbox-exec")
	if err != nil {
		return "", fmt.Errorf("sandbox-exec not found in PATH")
	}
	return path, nil
}

func IsAvailable(customPath string) bool {
	_, err := FindSandboxExec(customPath)
	return err == nil
}

func WriteProfile(baseDir, prefix, content string) (string, error) {
	if err := os.MkdirAll(baseDir, 0750); err != nil {
		return "", err
	}
	file, err := os.CreateTemp(baseDir, prefix+"-*.sb")
	if err != nil {
		return "", err
	}
	defer file.Close()
	if _, err := file.WriteString(content); err != nil {
		return "", err
	}
	return filepath.Clean(file.Name()), nil
}
