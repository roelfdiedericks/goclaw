package cron

import (
	"encoding/json"
	"fmt"
	"strings"
)

const LegacyStoreVersion = 1

type storeFormat string

const (
	storeFormatUnknown storeFormat = "unknown"
	storeFormatNative  storeFormat = "native"
	storeFormatLegacy  storeFormat = "legacy"
)

type storeFormatProbe struct {
	Version int                          `json:"version"`
	Jobs    []map[string]json.RawMessage `json:"jobs"`
}

type legacyStoreFile struct {
	Version int              `json:"version"`
	Jobs    []*legacyCronJob `json:"jobs"`
}

type legacyCronJob struct {
	ID             string           `json:"id"`
	AgentID        string           `json:"agentId,omitempty"`
	Name           string           `json:"name"`
	Description    string           `json:"description,omitempty"`
	Enabled        bool             `json:"enabled"`
	CreatedAtMs    int64            `json:"createdAtMs"`
	UpdatedAtMs    int64            `json:"updatedAtMs"`
	Schedule       Schedule         `json:"schedule"`
	SessionTarget  string           `json:"sessionTarget,omitempty"`
	WakeMode       string           `json:"wakeMode,omitempty"`
	Payload        legacyPayload    `json:"payload"`
	DeleteAfterRun bool             `json:"deleteAfterRun,omitempty"`
	Isolation      *legacyIsolation `json:"isolation,omitempty"`
	State          JobState         `json:"state"`
}

type legacyPayload struct {
	Kind              string `json:"kind"`
	Text              string `json:"text,omitempty"`
	Message           string `json:"message,omitempty"`
	Model             string `json:"model,omitempty"`
	Thinking          string `json:"thinking,omitempty"`
	TimeoutSeconds    int    `json:"timeoutSeconds,omitempty"`
	Deliver           bool   `json:"deliver,omitempty"`
	Channel           string `json:"channel,omitempty"`
	To                string `json:"to,omitempty"`
	BestEffortDeliver bool   `json:"bestEffortDeliver,omitempty"`
}

type legacyIsolation struct {
	PostToMainPrefix   string `json:"postToMainPrefix,omitempty"`
	PostToMainMode     string `json:"postToMainMode,omitempty"`
	PostToMainMaxChars int    `json:"postToMainMaxChars,omitempty"`
}

type migrationSummary struct {
	Converted int
}

func detectStoreFormat(data []byte) (storeFormat, error) {
	var probe storeFormatProbe
	if err := json.Unmarshal(data, &probe); err != nil {
		return storeFormatUnknown, fmt.Errorf("failed to parse jobs file probe: %w", err)
	}

	if len(probe.Jobs) == 0 {
		if probe.Version >= CurrentStoreVersion {
			return storeFormatNative, nil
		}
		if probe.Version > 0 && probe.Version < CurrentStoreVersion {
			return storeFormatLegacy, nil
		}
		return storeFormatNative, nil
	}

	sawNative := false
	sawLegacy := false
	for _, job := range probe.Jobs {
		if _, ok := job["prompt"]; ok {
			sawNative = true
		}
		if _, ok := job["result"]; ok {
			sawNative = true
		}
		if _, ok := job["payload"]; ok {
			sawLegacy = true
		}
		if _, ok := job["sessionTarget"]; ok {
			sawLegacy = true
		}
		if _, ok := job["wakeMode"]; ok {
			sawLegacy = true
		}
		if _, ok := job["isolation"]; ok {
			sawLegacy = true
		}
	}

	switch {
	case sawNative && sawLegacy:
		return storeFormatUnknown, fmt.Errorf("jobs file mixes native and legacy cron schema")
	case sawNative:
		return storeFormatNative, nil
	case sawLegacy:
		return storeFormatLegacy, nil
	case probe.Version > 0 && probe.Version < CurrentStoreVersion:
		return storeFormatLegacy, nil
	default:
		return storeFormatNative, nil
	}
}

func migrateLegacyStore(data []byte) (StoreFile, migrationSummary, error) {
	var legacy legacyStoreFile
	if err := json.Unmarshal(data, &legacy); err != nil {
		return StoreFile{}, migrationSummary{}, fmt.Errorf("failed to parse legacy jobs file: %w", err)
	}

	out := StoreFile{
		Version: CurrentStoreVersion,
		Jobs:    make([]*CronJob, 0, len(legacy.Jobs)),
	}
	summary := migrationSummary{}

	for i, oldJob := range legacy.Jobs {
		if oldJob == nil {
			return StoreFile{}, migrationSummary{}, fmt.Errorf("legacy job %d is null", i)
		}
		job, err := convertLegacyJob(oldJob)
		if err != nil {
			return StoreFile{}, migrationSummary{}, fmt.Errorf("legacy job %q conversion failed: %w", oldJob.Name, err)
		}
		out.Jobs = append(out.Jobs, job)
		summary.Converted++
	}

	return out, summary, nil
}

func convertLegacyJob(oldJob *legacyCronJob) (*CronJob, error) {
	if strings.TrimSpace(oldJob.ID) == "" {
		return nil, fmt.Errorf("missing id")
	}
	if strings.TrimSpace(oldJob.Name) == "" {
		return nil, fmt.Errorf("missing name")
	}

	prompt := strings.TrimSpace(oldJob.Payload.Message)
	if prompt == "" {
		prompt = strings.TrimSpace(oldJob.Payload.Text)
	}
	if prompt == "" {
		return nil, fmt.Errorf("missing payload prompt")
	}

	resultMode := ResultModeStoreOnly
	switch strings.TrimSpace(oldJob.Payload.Kind) {
	case "", "agentTurn":
		if oldJob.Payload.Deliver {
			resultMode = ResultModeDeliver
		}
	case "systemEvent":
		resultMode = ResultModeHandoffMain
	default:
		return nil, fmt.Errorf("unsupported legacy payload kind %q", oldJob.Payload.Kind)
	}

	job := &CronJob{
		ID:             oldJob.ID,
		AgentID:        oldJob.AgentID,
		Name:           oldJob.Name,
		Description:    oldJob.Description,
		Enabled:        oldJob.Enabled,
		CreatedAtMs:    oldJob.CreatedAtMs,
		UpdatedAtMs:    oldJob.UpdatedAtMs,
		Schedule:       oldJob.Schedule,
		Prompt:         prompt,
		DeleteAfterRun: oldJob.DeleteAfterRun,
		State:          oldJob.State,
		Result: ResultPolicy{
			Mode:           resultMode,
			TimeoutSeconds: oldJob.Payload.TimeoutSeconds,
			Model:          oldJob.Payload.Model,
			Thinking:       oldJob.Payload.Thinking,
		},
	}

	if resultMode == ResultModeDeliver {
		job.Result.Channel = oldJob.Payload.Channel
		job.Result.To = oldJob.Payload.To
		job.Result.BestEffort = oldJob.Payload.BestEffortDeliver
	}

	if err := job.Validate(); err != nil {
		return nil, err
	}

	return job, nil
}
