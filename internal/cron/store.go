package cron

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/paths"
)

const CurrentStoreVersion = 2

// DefaultJobsPath returns the fallback path for jobs.json.
// Note: The gateway resolves cron paths based on workspace location:
//   - Side-by-side with OpenClaw: ~/.openclaw/cron/jobs.json
//   - Standalone: ~/.goclaw/cron/jobs.json
func DefaultJobsPath() string {
	p, err := paths.DataPath("cron/jobs.json")
	if err != nil {
		L_warn("cron: failed to get jobs path", "error", err)
		return ""
	}
	return p
}

// DefaultRunsDir returns the fallback directory for run logs.
// Note: The gateway resolves cron paths based on workspace location.
func DefaultRunsDir() string {
	p, err := paths.DataPath("cron/runs")
	if err != nil {
		L_warn("cron: failed to get runs dir", "error", err)
		return ""
	}
	return p
}

// Store manages cron job persistence.
type Store struct {
	path     string
	runsDir  string
	mu       sync.RWMutex
	jobs     map[string]*CronJob // keyed by job ID
	modified bool
}

// NewStore creates a new cron store.
func NewStore(jobsPath, runsDir string) *Store {
	if jobsPath == "" {
		jobsPath = DefaultJobsPath()
	}
	if runsDir == "" {
		runsDir = DefaultRunsDir()
	}
	return &Store{
		path:    jobsPath,
		runsDir: runsDir,
		jobs:    make(map[string]*CronJob),
	}
}

// Load reads jobs from the JSON file.
func (s *Store) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			bootstrapped, bootstrapData, bootstrapErr := s.bootstrapFromOpenClawLocked()
			if bootstrapErr != nil {
				return bootstrapErr
			}
			if !bootstrapped {
				L_debug("cron: jobs file not found, starting empty", "path", s.path)
				s.jobs = make(map[string]*CronJob)
				return nil
			}
			data = bootstrapData
		} else {
			return fmt.Errorf("failed to read jobs file: %w", err)
		}
	}

	format, err := detectStoreFormat(data)
	if err != nil {
		return err
	}

	if format == storeFormatLegacy {
		migrated, summary, err := migrateLegacyStore(data)
		if err != nil {
			return fmt.Errorf("failed to migrate legacy cron jobs: %w", err)
		}
		if err := writeMigrationBackup(s.path, data); err != nil {
			return fmt.Errorf("failed to back up legacy cron jobs: %w", err)
		}
		if err := writeStoreFileAtomic(s.path, migrated); err != nil {
			return fmt.Errorf("failed to rewrite migrated cron jobs: %w", err)
		}
		L_info("cron: migrated legacy jobs file", "path", s.path, "converted", summary.Converted)
		data, err = os.ReadFile(s.path)
		if err != nil {
			return fmt.Errorf("failed to read migrated jobs file: %w", err)
		}
	}

	var file StoreFile
	if err := json.Unmarshal(data, &file); err != nil {
		return fmt.Errorf("failed to parse jobs file: %w", err)
	}
	if file.Version != 0 && file.Version != CurrentStoreVersion {
		return fmt.Errorf("unsupported cron jobs file version: %d", file.Version)
	}

	s.jobs = make(map[string]*CronJob, len(file.Jobs))
	for i, job := range file.Jobs {
		if job == nil {
			return fmt.Errorf("invalid cron job at index %d: null job", i)
		}
		if job.ID == "" {
			return fmt.Errorf("invalid cron job at index %d: missing id", i)
		}
		if err := job.Validate(); err != nil {
			return fmt.Errorf("invalid cron job %q: %w", job.ID, err)
		}
		if _, exists := s.jobs[job.ID]; exists {
			return fmt.Errorf("duplicate cron job id: %s", job.ID)
		}
		s.jobs[job.ID] = job
	}

	L_info("cron: loaded jobs", "count", len(s.jobs), "path", s.path)
	return nil
}

func (s *Store) bootstrapFromOpenClawLocked() (bool, []byte, error) {
	defaultPath := DefaultJobsPath()
	if defaultPath == "" || filepath.Clean(s.path) != filepath.Clean(defaultPath) {
		return false, nil, nil
	}

	sourcePath, err := paths.OpenClawDataPath("cron/jobs.json")
	if err != nil {
		return false, nil, fmt.Errorf("failed to resolve OpenClaw cron path: %w", err)
	}

	data, err := os.ReadFile(sourcePath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil, nil
		}
		return false, nil, fmt.Errorf("failed to read OpenClaw cron file: %w", err)
	}

	if err := writeBytesAtomic(s.path, data); err != nil {
		return false, nil, fmt.Errorf("failed to bootstrap GoClaw cron store from OpenClaw: %w", err)
	}

	L_info("cron: bootstrapped jobs file from OpenClaw", "source", sourcePath, "destination", s.path)
	return true, data, nil
}

// Save writes jobs to the JSON file.
func (s *Store) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.saveLocked()
}

func (s *Store) saveLocked() error {
	file := StoreFile{
		Version: CurrentStoreVersion,
		Jobs:    make([]*CronJob, 0, len(s.jobs)),
	}
	for _, job := range s.jobs {
		file.Jobs = append(file.Jobs, job)
	}

	if err := writeStoreFileAtomic(s.path, file); err != nil {
		return err
	}

	s.modified = false
	L_trace("cron: saved jobs", "count", len(s.jobs), "path", s.path)
	return nil
}

func writeStoreFileAtomic(path string, file StoreFile) error {
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal jobs: %w", err)
	}
	return writeBytesAtomic(path, data)
}

func writeMigrationBackup(path string, data []byte) error {
	if err := paths.EnsureParentDir(path); err != nil {
		return fmt.Errorf("failed to create cron directory: %w", err)
	}
	backupPath := path + ".bak"
	if err := os.WriteFile(backupPath, data, 0600); err != nil {
		return fmt.Errorf("failed to write backup file: %w", err)
	}
	L_info("cron: wrote migration backup", "path", backupPath)
	return nil
}

func writeBytesAtomic(path string, data []byte) error {
	if err := paths.EnsureParentDir(path); err != nil {
		return fmt.Errorf("failed to create cron directory: %w", err)
	}

	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0600); err != nil {
		return fmt.Errorf("failed to write temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to rename temp file: %w", err)
	}
	return nil
}

// GetJob returns a job by ID.
func (s *Store) GetJob(id string) *CronJob {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.jobs[id]
}

// GetAllJobs returns all jobs.
func (s *Store) GetAllJobs() []*CronJob {
	s.mu.RLock()
	defer s.mu.RUnlock()

	jobs := make([]*CronJob, 0, len(s.jobs))
	for _, job := range s.jobs {
		jobs = append(jobs, job)
	}
	return jobs
}

// GetEnabledJobs returns all enabled jobs.
func (s *Store) GetEnabledJobs() []*CronJob {
	s.mu.RLock()
	defer s.mu.RUnlock()

	jobs := make([]*CronJob, 0)
	for _, job := range s.jobs {
		if job.Enabled {
			jobs = append(jobs, job)
		}
	}
	return jobs
}

// GetDueJobs returns jobs that should run now.
func (s *Store) GetDueJobs(now time.Time) []*CronJob {
	s.mu.RLock()
	defer s.mu.RUnlock()

	nowMs := now.UnixMilli()
	jobs := make([]*CronJob, 0)
	for _, job := range s.jobs {
		if !job.Enabled {
			continue
		}
		if job.State.NextRunAtMs == nil {
			continue
		}
		if *job.State.NextRunAtMs <= nowMs {
			jobs = append(jobs, job)
		}
	}
	return jobs
}

// AddJob adds a new job.
func (s *Store) AddJob(job *CronJob) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if job.ID == "" {
		job.ID = uuid.New().String()
	}
	if _, exists := s.jobs[job.ID]; exists {
		return fmt.Errorf("job with ID %s already exists", job.ID)
	}
	if err := job.Validate(); err != nil {
		return fmt.Errorf("invalid cron job: %w", err)
	}

	now := time.Now().UnixMilli()
	if job.CreatedAtMs == 0 {
		job.CreatedAtMs = now
	}
	job.UpdatedAtMs = now

	s.jobs[job.ID] = job
	s.modified = true

	return s.saveLocked()
}

// UpdateJob updates an existing job.
func (s *Store) UpdateJob(job *CronJob) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.jobs[job.ID]; !exists {
		return fmt.Errorf("job with ID %s not found", job.ID)
	}
	if err := job.Validate(); err != nil {
		return fmt.Errorf("invalid cron job: %w", err)
	}

	job.UpdatedAtMs = time.Now().UnixMilli()
	s.jobs[job.ID] = job
	s.modified = true

	return s.saveLocked()
}

// UpdateJobState updates only the state of a job (without full save).
func (s *Store) UpdateJobState(id string, state JobState) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	job, exists := s.jobs[id]
	if !exists {
		return fmt.Errorf("job with ID %s not found", id)
	}

	job.State = state
	job.UpdatedAtMs = time.Now().UnixMilli()
	s.modified = true

	return s.saveLocked()
}

// DeleteJob removes a job.
func (s *Store) DeleteJob(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.jobs[id]; !exists {
		return fmt.Errorf("job with ID %s not found", id)
	}

	delete(s.jobs, id)
	s.modified = true

	return s.saveLocked()
}

// DisableJob disables a job.
func (s *Store) DisableJob(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	job, exists := s.jobs[id]
	if !exists {
		return fmt.Errorf("job with ID %s not found", id)
	}

	job.Enabled = false
	job.UpdatedAtMs = time.Now().UnixMilli()
	s.modified = true

	return s.saveLocked()
}

// Count returns the number of jobs.
func (s *Store) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.jobs)
}

// EnabledCount returns the number of enabled jobs.
func (s *Store) EnabledCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	count := 0
	for _, job := range s.jobs {
		if job.Enabled {
			count++
		}
	}
	return count
}

// Path returns the store file path.
func (s *Store) Path() string {
	return s.path
}
