package seatbelt

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

func FindSandboxExec(customPath string) (string, error) {
	if customPath != "" {
		if _, err := os.Stat(customPath); err == nil {
			L_debug("seatbelt: using custom sandbox-exec path", "path", customPath)
			return customPath, nil
		}
		return "", fmt.Errorf("sandbox-exec not found at custom path %s", customPath)
	}
	path, err := exec.LookPath("sandbox-exec")
	if err != nil {
		return "", fmt.Errorf("sandbox-exec not found in PATH")
	}
	L_debug("seatbelt: found sandbox-exec", "path", path)
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
	profilePath := filepath.Clean(file.Name())
	L_trace("seatbelt: wrote profile", "path", profilePath, "content", content)
	return profilePath, nil
}
