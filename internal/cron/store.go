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
)

// DefaultJobsPath returns the default path for jobs.json.
func DefaultJobsPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".openclaw", "cron", "jobs.json")
}

// DefaultRunsDir returns the default directory for run logs.
func DefaultRunsDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".openclaw", "cron", "runs")
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
			L_debug("cron: jobs file not found, starting empty", "path", s.path)
			s.jobs = make(map[string]*CronJob)
			return nil
		}
		return fmt.Errorf("failed to read jobs file: %w", err)
	}

	var file StoreFile
	if err := json.Unmarshal(data, &file); err != nil {
		return fmt.Errorf("failed to parse jobs file: %w", err)
	}

	s.jobs = make(map[string]*CronJob, len(file.Jobs))
	for _, job := range file.Jobs {
		if job.ID == "" {
			continue // Skip invalid jobs
		}
		s.jobs[job.ID] = job
	}

	L_info("cron: loaded jobs", "count", len(s.jobs), "path", s.path)
	return nil
}

// Save writes jobs to the JSON file.
func (s *Store) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.saveLocked()
}

func (s *Store) saveLocked() error {
	// Ensure directory exists
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create cron directory: %w", err)
	}

	// Build file structure
	file := StoreFile{
		Version: 1,
		Jobs:    make([]*CronJob, 0, len(s.jobs)),
	}
	for _, job := range s.jobs {
		file.Jobs = append(file.Jobs, job)
	}

	// Marshal with indentation for readability
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal jobs: %w", err)
	}

	// Write atomically via temp file
	tmpPath := s.path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write temp file: %w", err)
	}

	if err := os.Rename(tmpPath, s.path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to rename temp file: %w", err)
	}

	s.modified = false
	L_debug("cron: saved jobs", "count", len(s.jobs), "path", s.path)
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
