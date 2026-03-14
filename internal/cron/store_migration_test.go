package cron

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadNativeCronFileUnchanged(t *testing.T) {
	t.Parallel()

	jobsPath := filepath.Join(t.TempDir(), "jobs.json")
	native := StoreFile{
		Version: CurrentStoreVersion,
		Jobs: []*CronJob{
			{
				ID:      "job-1",
				Name:    "native",
				Enabled: true,
				Schedule: Schedule{
					Kind:    ScheduleKindEvery,
					EveryMs: 60000,
				},
				Prompt: "say hello",
				Result: ResultPolicy{Mode: ResultModeStoreOnly},
			},
		},
	}
	original := mustMarshalIndent(t, native)
	if err := os.WriteFile(jobsPath, original, 0600); err != nil {
		t.Fatalf("write native jobs: %v", err)
	}

	store := NewStore(jobsPath, "")
	if err := store.Load(); err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	got := mustReadFile(t, jobsPath)
	if string(got) != string(original) {
		t.Fatalf("native file should remain unchanged\nwant:\n%s\n\ngot:\n%s", original, got)
	}
	if _, err := os.Stat(jobsPath + ".bak"); !os.IsNotExist(err) {
		t.Fatalf("expected no backup for native load, got err=%v", err)
	}
}

func TestLoadMigratesLegacyAgentTurnModes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		deliver    bool
		wantMode   ResultMode
		wantBackup bool
	}{
		{name: "store only", deliver: false, wantMode: ResultModeStoreOnly, wantBackup: true},
		{name: "deliver", deliver: true, wantMode: ResultModeDeliver, wantBackup: true},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			jobsPath := filepath.Join(t.TempDir(), "jobs.json")
			legacy := legacyStoreFile{
				Version: LegacyStoreVersion,
				Jobs: []*legacyCronJob{
					{
						ID:      "job-1",
						Name:    "legacy",
						Enabled: true,
						Schedule: Schedule{
							Kind:    ScheduleKindEvery,
							EveryMs: 300000,
						},
						Payload: legacyPayload{
							Kind:              "agentTurn",
							Message:           "legacy prompt",
							Deliver:           tc.deliver,
							TimeoutSeconds:    42,
							Channel:           "telegram",
							To:                "owner",
							BestEffortDeliver: true,
							Model:             "grok",
							Thinking:          "medium",
						},
					},
				},
			}
			legacyBytes := mustMarshalIndent(t, legacy)
			if err := os.WriteFile(jobsPath, legacyBytes, 0600); err != nil {
				t.Fatalf("write legacy jobs: %v", err)
			}

			store := NewStore(jobsPath, "")
			if err := store.Load(); err != nil {
				t.Fatalf("Load() error: %v", err)
			}

			job := store.GetJob("job-1")
			if job == nil {
				t.Fatalf("expected migrated job to be loaded")
			}
			if job.Prompt != "legacy prompt" {
				t.Fatalf("expected prompt to migrate, got %q", job.Prompt)
			}
			if job.Result.Mode != tc.wantMode {
				t.Fatalf("expected result mode %q, got %q", tc.wantMode, job.Result.Mode)
			}
			if job.Result.TimeoutSeconds != 42 || job.Result.Model != "grok" || job.Result.Thinking != "medium" {
				t.Fatalf("expected payload extras to migrate, got %#v", job.Result)
			}
			if tc.wantMode == ResultModeDeliver {
				if job.Result.Channel != "telegram" || job.Result.To != "owner" || !job.Result.BestEffort {
					t.Fatalf("expected delivery fields to migrate for deliver mode, got %#v", job.Result)
				}
			} else if job.Result.Channel != "" || job.Result.To != "" || job.Result.BestEffort {
				t.Fatalf("expected non-deliver legacy job to drop delivery targeting, got %#v", job.Result)
			}

			migrated := readStoreFileFromDisk(t, jobsPath)
			if migrated.Version != CurrentStoreVersion {
				t.Fatalf("expected migrated version %d, got %d", CurrentStoreVersion, migrated.Version)
			}

			backup := mustReadFile(t, jobsPath+".bak")
			if tc.wantBackup && string(backup) != string(legacyBytes) {
				t.Fatalf("expected backup to contain original legacy file")
			}
		})
	}
}

func TestLoadMigratesLegacySystemEventToHandoffMain(t *testing.T) {
	t.Parallel()

	jobsPath := filepath.Join(t.TempDir(), "jobs.json")
	legacy := legacyStoreFile{
		Version: LegacyStoreVersion,
		Jobs: []*legacyCronJob{
			{
				ID:      "job-1",
				Name:    "legacy wake",
				Enabled: true,
				Schedule: Schedule{
					Kind: ScheduleKindCron,
					Expr: "0 * * * *",
				},
				Payload: legacyPayload{
					Kind: "systemEvent",
					Text: "wake the main agent with this summary",
				},
			},
		},
	}
	if err := os.WriteFile(jobsPath, mustMarshalIndent(t, legacy), 0600); err != nil {
		t.Fatalf("write legacy systemEvent jobs: %v", err)
	}

	store := NewStore(jobsPath, "")
	if err := store.Load(); err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	job := store.GetJob("job-1")
	if job == nil {
		t.Fatalf("expected migrated job to load")
	}
	if job.Result.Mode != ResultModeHandoffMain {
		t.Fatalf("expected systemEvent to migrate to handoff_main, got %q", job.Result.Mode)
	}
	if job.Prompt != "wake the main agent with this summary" {
		t.Fatalf("expected payload text to become prompt, got %q", job.Prompt)
	}
}

func TestLoadMalformedLegacyCronFailsWithoutRewrite(t *testing.T) {
	t.Parallel()

	jobsPath := filepath.Join(t.TempDir(), "jobs.json")
	legacy := legacyStoreFile{
		Version: LegacyStoreVersion,
		Jobs: []*legacyCronJob{
			{
				ID:      "job-1",
				Name:    "broken legacy",
				Enabled: true,
				Schedule: Schedule{
					Kind:    ScheduleKindEvery,
					EveryMs: 60000,
				},
				Payload: legacyPayload{
					Kind: "agentTurn",
				},
			},
		},
	}
	legacyBytes := mustMarshalIndent(t, legacy)
	if err := os.WriteFile(jobsPath, legacyBytes, 0600); err != nil {
		t.Fatalf("write malformed legacy jobs: %v", err)
	}

	store := NewStore(jobsPath, "")
	if err := store.Load(); err == nil {
		t.Fatalf("expected malformed legacy load to fail")
	}

	got := mustReadFile(t, jobsPath)
	if string(got) != string(legacyBytes) {
		t.Fatalf("expected malformed legacy file to remain untouched")
	}
	if _, err := os.Stat(jobsPath + ".bak"); !os.IsNotExist(err) {
		t.Fatalf("expected no backup when migration fails, got err=%v", err)
	}
}

func TestLoadBootstrapsFromOpenClawWhenGoClawCronMissing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	openClawJobsPath := filepath.Join(home, ".openclaw", "cron", "jobs.json")
	legacy := legacyStoreFile{
		Version: LegacyStoreVersion,
		Jobs: []*legacyCronJob{
			{
				ID:      "job-1",
				Name:    "legacy bootstrap",
				Enabled: true,
				Schedule: Schedule{
					Kind:    ScheduleKindEvery,
					EveryMs: 60000,
				},
				Payload: legacyPayload{
					Kind:    "agentTurn",
					Message: "bootstrap me",
					Deliver: true,
				},
			},
		},
	}
	legacyBytes := mustMarshalIndent(t, legacy)
	if err := os.MkdirAll(filepath.Dir(openClawJobsPath), 0750); err != nil {
		t.Fatalf("mkdir openclaw cron dir: %v", err)
	}
	if err := os.WriteFile(openClawJobsPath, legacyBytes, 0600); err != nil {
		t.Fatalf("write openclaw jobs: %v", err)
	}

	store := NewStore("", "")
	if err := store.Load(); err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	goclawJobsPath := filepath.Join(home, ".goclaw", "cron", "jobs.json")
	job := store.GetJob("job-1")
	if job == nil {
		t.Fatalf("expected bootstrapped job to load")
	}
	if job.Prompt != "bootstrap me" || job.Result.Mode != ResultModeDeliver {
		t.Fatalf("expected legacy job to bootstrap and migrate, got %#v", job)
	}

	var goclawFile StoreFile
	if err := json.Unmarshal(mustReadFile(t, goclawJobsPath), &goclawFile); err != nil {
		t.Fatalf("unmarshal bootstrapped goclaw jobs: %v", err)
	}
	if goclawFile.Version != CurrentStoreVersion {
		t.Fatalf("expected bootstrapped file version %d, got %d", CurrentStoreVersion, goclawFile.Version)
	}

	if string(mustReadFile(t, openClawJobsPath)) != string(legacyBytes) {
		t.Fatalf("expected original OpenClaw jobs file to remain untouched")
	}
	if string(mustReadFile(t, goclawJobsPath+".bak")) != string(legacyBytes) {
		t.Fatalf("expected GoClaw backup to contain imported legacy content")
	}
}

func mustMarshalIndent(t *testing.T, v interface{}) []byte {
	t.Helper()
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return data
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file %s: %v", path, err)
	}
	return data
}

func readStoreFileFromDisk(t *testing.T, path string) StoreFile {
	t.Helper()
	var file StoreFile
	if err := json.Unmarshal(mustReadFile(t, path), &file); err != nil {
		t.Fatalf("unmarshal store file: %v", err)
	}
	return file
}
