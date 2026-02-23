package config

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// DefaultBackupCount is the default number of backup versions to keep.
const DefaultBackupCount = 5

// BackupInfo describes a config backup file.
type BackupInfo struct {
	Path    string    // Full path to backup file
	Index   int       // 0 = .bak (newest), 1 = .bak.1, etc.
	ModTime time.Time // Last modification time
	Size    int64     // File size in bytes
}

// AtomicWriteJSON marshals data as JSON and writes it atomically.
// Uses temp file + rename pattern for crash safety.
func AtomicWriteJSON(path string, data interface{}, perm os.FileMode) error {
	jsonData, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal JSON: %w", err)
	}

	return AtomicWrite(path, jsonData, perm)
}

// AtomicWrite writes data to path atomically using temp file + rename.
func AtomicWrite(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)

	// Ensure directory exists
	if err := os.MkdirAll(dir, 0750); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	// Create temp file in same directory (same filesystem for atomic rename)
	tmp, err := os.CreateTemp(dir, ".goclaw-*.tmp")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmp.Name()

	// Clean up temp file on any error
	success := false
	defer func() {
		if !success {
			os.Remove(tmpPath)
		}
	}()

	// Set permissions
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return fmt.Errorf("failed to set permissions: %w", err)
	}

	// Write data
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("failed to write temp file: %w", err)
	}

	// Sync to disk for durability
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("failed to sync temp file: %w", err)
	}

	if err := tmp.Close(); err != nil {
		return fmt.Errorf("failed to close temp file: %w", err)
	}

	// Atomic rename
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("failed to rename temp to target: %w", err)
	}

	success = true
	return nil
}

// BackupAndWriteJSON creates a backup of the existing file (if any),
// then atomically writes the new data.
func BackupAndWriteJSON(path string, data interface{}, maxBackups int) error {
	if maxBackups <= 0 {
		maxBackups = DefaultBackupCount
	}

	// Create backup if file exists
	if _, err := os.Stat(path); err == nil {
		if err := createBackup(path, maxBackups); err != nil {
			L_warn("config: backup failed, continuing with save", "error", err)
		}
	}

	// Atomic write
	if err := AtomicWriteJSON(path, data, 0600); err != nil {
		return err
	}

	L_debug("config: saved", "path", path)
	return nil
}

// createBackup rotates existing backups and copies current file to .bak
func createBackup(path string, maxBackups int) error {
	// Rotate existing backups
	RotateBackups(path, maxBackups)

	// Copy current to .bak
	backupPath := path + ".bak"
	if err := copyFile(path, backupPath); err != nil {
		return fmt.Errorf("failed to create backup: %w", err)
	}

	L_debug("config: created backup", "path", backupPath)
	return nil
}

// RotateBackups rotates backup files.
// .bak.N (oldest) gets deleted, .bak.N-1 -> .bak.N, ..., .bak -> .bak.1
func RotateBackups(path string, maxBackups int) {
	if maxBackups <= 1 {
		return
	}

	backupBase := path + ".bak"
	maxIndex := maxBackups - 1

	// Delete oldest
	oldestPath := fmt.Sprintf("%s.%d", backupBase, maxIndex)
	if err := os.Remove(oldestPath); err != nil && !os.IsNotExist(err) {
		L_trace("config: failed to remove oldest backup", "path", oldestPath, "error", err)
	}

	// Rotate: .bak.N-1 -> .bak.N, .bak.N-2 -> .bak.N-1, etc.
	for i := maxIndex - 1; i >= 1; i-- {
		src := fmt.Sprintf("%s.%d", backupBase, i)
		dst := fmt.Sprintf("%s.%d", backupBase, i+1)
		if err := os.Rename(src, dst); err != nil && !os.IsNotExist(err) {
			L_trace("config: failed to rotate backup", "src", src, "dst", dst, "error", err)
		}
	}

	// .bak -> .bak.1
	if err := os.Rename(backupBase, backupBase+".1"); err != nil && !os.IsNotExist(err) {
		L_trace("config: failed to rotate .bak to .bak.1", "error", err)
	}
}

// ListBackups returns available backups for a file, newest first.
func ListBackups(path string) []BackupInfo {
	var backups []BackupInfo
	backupBase := path + ".bak"

	// Check .bak (newest)
	if info, err := os.Stat(backupBase); err == nil {
		backups = append(backups, BackupInfo{
			Path:    backupBase,
			Index:   0,
			ModTime: info.ModTime(),
			Size:    info.Size(),
		})
	}

	// Check .bak.1 through .bak.N
	for i := 1; i < 100; i++ { // reasonable upper bound
		bakPath := fmt.Sprintf("%s.%d", backupBase, i)
		info, err := os.Stat(bakPath)
		if err != nil {
			break // no more backups
		}
		backups = append(backups, BackupInfo{
			Path:    bakPath,
			Index:   i,
			ModTime: info.ModTime(),
			Size:    info.Size(),
		})
	}

	// Sort by mod time descending (newest first)
	sort.Slice(backups, func(i, j int) bool {
		return backups[i].ModTime.After(backups[j].ModTime)
	})

	return backups
}

// RestoreBackup restores a backup by index.
// Creates a backup of the current file before restoring.
func RestoreBackup(path string, index int) error {
	backups := ListBackups(path)

	var backup *BackupInfo
	for _, b := range backups {
		if b.Index == index {
			backup = &b
			break
		}
	}

	if backup == nil {
		return fmt.Errorf("backup index %d not found", index)
	}

	// Read backup
	data, err := os.ReadFile(backup.Path)
	if err != nil {
		return fmt.Errorf("failed to read backup: %w", err)
	}

	// Validate it's valid JSON
	var jsonData interface{}
	if err := json.Unmarshal(data, &jsonData); err != nil {
		return fmt.Errorf("backup contains invalid JSON: %w", err)
	}

	// Create backup of current before restoring
	if _, err := os.Stat(path); err == nil {
		if err := createBackup(path, DefaultBackupCount); err != nil {
			L_warn("config: failed to backup current before restore", "error", err)
		}
	}

	// Write restored data
	if err := AtomicWrite(path, data, 0600); err != nil {
		return fmt.Errorf("failed to write restored config: %w", err)
	}

	L_info("config: restored backup", "from", backup.Path, "to", path)
	return nil
}

// copyFile copies a file from src to dst, preserving permissions.
func copyFile(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	srcInfo, err := srcFile.Stat()
	if err != nil {
		return err
	}

	dstFile, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, srcInfo.Mode().Perm())
	if err != nil {
		return err
	}
	defer dstFile.Close()

	_, err = io.Copy(dstFile, srcFile)
	return err
}
