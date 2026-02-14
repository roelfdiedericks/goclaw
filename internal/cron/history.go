package cron

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

const (
	// MaxSummaryChars is the maximum length for run summaries
	MaxSummaryChars = 2000

	// MaxHistoryBytes is the maximum size for history files (2MB)
	MaxHistoryBytes = 2 * 1024 * 1024

	// MaxHistoryLines is the maximum number of lines to keep
	MaxHistoryLines = 2000
)

// HistoryManager manages run history logs.
type HistoryManager struct {
	runsDir string
}

// NewHistoryManager creates a new history manager.
func NewHistoryManager(runsDir string) *HistoryManager {
	if runsDir == "" {
		runsDir = DefaultRunsDir()
	}
	return &HistoryManager{runsDir: runsDir}
}

// LogRun appends a run entry to the job's history file.
func (h *HistoryManager) LogRun(jobID string, entry RunLogEntry) error {
	// Ensure runs directory exists
	if err := os.MkdirAll(h.runsDir, 0750); err != nil {
		return fmt.Errorf("failed to create runs directory: %w", err)
	}

	// Truncate summary if needed
	if len(entry.Summary) > MaxSummaryChars {
		entry.Summary = entry.Summary[:MaxSummaryChars-3] + "..."
	}

	// Marshal entry
	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("failed to marshal entry: %w", err)
	}

	// Append to history file
	historyPath := h.historyPath(jobID)
	f, err := os.OpenFile(historyPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("failed to open history file: %w", err)
	}
	defer f.Close()

	if _, err := f.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("failed to write entry: %w", err)
	}

	// Check if pruning is needed
	stat, err := f.Stat()
	if err == nil && stat.Size() > MaxHistoryBytes {
		L_debug("cron: history file exceeds size limit, pruning", "job", jobID, "size", stat.Size())
		go h.pruneHistory(jobID) // Prune asynchronously
	}

	return nil
}

// GetRuns returns recent runs for a job.
func (h *HistoryManager) GetRuns(jobID string, limit int) ([]RunLogEntry, error) {
	historyPath := h.historyPath(jobID)

	f, err := os.Open(historyPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // No history yet
		}
		return nil, fmt.Errorf("failed to open history file: %w", err)
	}
	defer f.Close()

	var entries []RunLogEntry
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var entry RunLogEntry
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			continue // Skip malformed entries
		}
		entries = append(entries, entry)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed to read history: %w", err)
	}

	// Return last N entries (most recent first)
	if limit > 0 && len(entries) > limit {
		entries = entries[len(entries)-limit:]
	}

	// Reverse to show most recent first
	for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
		entries[i], entries[j] = entries[j], entries[i]
	}

	return entries, nil
}

// GetRecentRuns is a convenience method to get the last 10 runs.
func (h *HistoryManager) GetRecentRuns(jobID string) ([]RunLogEntry, error) {
	return h.GetRuns(jobID, 10)
}

// pruneHistory truncates the history file to keep only the last MaxHistoryLines entries.
func (h *HistoryManager) pruneHistory(jobID string) {
	historyPath := h.historyPath(jobID)

	// Read all entries
	f, err := os.Open(historyPath)
	if err != nil {
		L_error("cron: failed to open history for pruning", "job", jobID, "error", err)
		return
	}

	var entries [][]byte
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		entries = append(entries, append([]byte{}, scanner.Bytes()...))
	}
	f.Close()

	if len(entries) <= MaxHistoryLines {
		return // No pruning needed
	}

	// Keep only the last MaxHistoryLines entries
	entries = entries[len(entries)-MaxHistoryLines:]

	// Write back to temp file
	tmpPath := historyPath + ".tmp"
	tmpFile, err := os.Create(tmpPath)
	if err != nil {
		L_error("cron: failed to create temp file for pruning", "job", jobID, "error", err)
		return
	}

	for _, entry := range entries {
		if _, err := tmpFile.Write(entry); err != nil {
			L_warn("cron: failed to write history entry", "job", jobID, "error", err)
		}
		if _, err := tmpFile.Write([]byte{'\n'}); err != nil {
			L_warn("cron: failed to write newline", "job", jobID, "error", err)
		}
	}
	tmpFile.Close()

	// Rename temp to original
	if err := os.Rename(tmpPath, historyPath); err != nil {
		L_error("cron: failed to rename pruned history", "job", jobID, "error", err)
		os.Remove(tmpPath)
		return
	}

	L_debug("cron: pruned history", "job", jobID, "keptEntries", len(entries))
}

// DeleteHistory removes the history file for a job.
func (h *HistoryManager) DeleteHistory(jobID string) error {
	historyPath := h.historyPath(jobID)
	err := os.Remove(historyPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete history: %w", err)
	}
	return nil
}

func (h *HistoryManager) historyPath(jobID string) string {
	return filepath.Join(h.runsDir, jobID+".jsonl")
}

// TruncateSummary truncates text to MaxSummaryChars.
func TruncateSummary(text string) string {
	if len(text) <= MaxSummaryChars {
		return text
	}
	return text[:MaxSummaryChars-3] + "..."
}

// CreateRunEntry creates a RunLogEntry from execution results.
func CreateRunEntry(startTime time.Time, duration time.Duration, status, summary, errorMsg string) RunLogEntry {
	return RunLogEntry{
		Ts:         startTime.UnixMilli(),
		Status:     status,
		DurationMs: duration.Milliseconds(),
		Summary:    TruncateSummary(summary),
		Error:      errorMsg,
	}
}
