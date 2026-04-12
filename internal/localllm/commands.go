package localllm

import (
	"context"
	"fmt"
	"time"

	"github.com/roelfdiedericks/goclaw/internal/bus"
	"github.com/roelfdiedericks/goclaw/internal/jobs"
)

type CommandRequest struct {
	RuntimeVersion string `json:"runtimeVersion,omitempty"`
	ModelID        string `json:"modelID,omitempty"`
	Host           string `json:"host,omitempty"`
	Port           int    `json:"port,omitempty"`
	ContextSize    int    `json:"contextSize,omitempty"`
	ModelAlias     string `json:"modelAlias,omitempty"`
}

func RegisterCommands() {
	bus.RegisterCommand("local_llm", "status", handleStatus)
	bus.RegisterCommand("local_llm", "ensure_runtime", handleEnsureRuntime)
	bus.RegisterCommand("local_llm", "download_model", handleDownloadModel)
	bus.RegisterCommand("local_llm", "start", handleStart)
	bus.RegisterCommand("local_llm", "stop", handleStop)
	bus.RegisterCommand("local_llm", "select_model", handleSelectModel)
}

func handleStatus(_ bus.Command) bus.CommandResult {
	status := GetManager().Status()
	return bus.CommandResult{
		Success: true,
		Message: "local llama.cpp status loaded",
		Data:    status,
	}
}

func handleEnsureRuntime(cmd bus.Command) bus.CommandResult {
	spec, err := decodeCommandRequest(cmd.Payload)
	if err != nil {
		return failureResult(err)
	}
	job := jobs.GetManager().Start(jobs.StartSpec{
		OwnerComponent: "local_llm",
		OwnerAction:    "ensure_runtime",
		InitialPhase:   "queued",
		InitialMessage: "Preparing managed local runtime",
		PollAfterMs:    1000,
		Cancelable:     true,
		Metadata:       localLLMJobMetadata(spec),
	}, func(ctx context.Context, reporter *jobs.Reporter) (interface{}, error) {
		timeoutCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
		defer cancel()
		status, err := GetManager().EnsureRuntimeWithProgress(timeoutCtx, spec, reporter.Update)
		if err != nil {
			return status, err
		}
		return status, nil
	})
	return bus.CommandResult{
		Success: true,
		Message: fmt.Sprintf("llama.cpp ensure-runtime job started: %s", job.JobID),
		Data:    job,
	}
}

func handleDownloadModel(cmd bus.Command) bus.CommandResult {
	spec, err := decodeCommandRequest(cmd.Payload)
	if err != nil {
		return failureResult(err)
	}
	if spec.ModelID == "" {
		return failureResult(fmt.Errorf("modelID is required"))
	}
	job := jobs.GetManager().Start(jobs.StartSpec{
		OwnerComponent: "local_llm",
		OwnerAction:    "download_model",
		InitialPhase:   "queued",
		InitialMessage: fmt.Sprintf("Preparing managed model %s", spec.ModelID),
		PollAfterMs:    1000,
		Cancelable:     true,
		Metadata:       localLLMJobMetadata(spec),
	}, func(ctx context.Context, reporter *jobs.Reporter) (interface{}, error) {
		timeoutCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
		defer cancel()
		status, err := GetManager().EnsureRuntimeWithProgress(timeoutCtx, ManagedSpec{
			ModelID:        spec.ModelID,
			RuntimeVersion: spec.RuntimeVersion,
		}, reporter.Update)
		if err != nil {
			return status, err
		}
		return status, nil
	})
	return bus.CommandResult{
		Success: true,
		Message: fmt.Sprintf("llama.cpp model download job started: %s", job.JobID),
		Data:    job,
	}
}

func handleStart(cmd bus.Command) bus.CommandResult {
	spec, err := decodeCommandRequest(cmd.Payload)
	if err != nil {
		return failureResult(err)
	}
	job := jobs.GetManager().Start(jobs.StartSpec{
		OwnerComponent: "local_llm",
		OwnerAction:    "start",
		InitialPhase:   "queued",
		InitialMessage: "Starting managed llama.cpp server",
		PollAfterMs:    1000,
		Cancelable:     true,
		Metadata:       localLLMJobMetadata(spec),
	}, func(ctx context.Context, reporter *jobs.Reporter) (interface{}, error) {
		timeoutCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
		defer cancel()
		status, err := GetManager().StartWithProgress(timeoutCtx, spec, reporter.Update)
		if err != nil {
			return status, err
		}
		return status, nil
	})
	return bus.CommandResult{
		Success: true,
		Message: fmt.Sprintf("llama.cpp start job started: %s", job.JobID),
		Data:    job,
	}
}

func handleStop(_ bus.Command) bus.CommandResult {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := GetManager().Stop(ctx); err != nil {
		return failureResult(err)
	}
	return bus.CommandResult{
		Success: true,
		Message: "llama.cpp server stopped",
		Data:    GetManager().Status(),
	}
}

func handleSelectModel(cmd bus.Command) bus.CommandResult {
	spec, err := decodeCommandRequest(cmd.Payload)
	if err != nil {
		return failureResult(err)
	}
	if spec.ModelID == "" {
		return failureResult(fmt.Errorf("modelID is required"))
	}
	status, err := GetManager().SelectModel(spec.ModelID)
	if err != nil {
		return failureResult(err)
	}
	return bus.CommandResult{
		Success: true,
		Message: fmt.Sprintf("selected model %s", status.ModelID),
		Data:    status,
	}
}

func decodeCommandRequest(payload any) (ManagedSpec, error) {
	switch v := payload.(type) {
	case nil:
		return ManagedSpec{}, nil
	case ManagedSpec:
		return v, nil
	case *ManagedSpec:
		if v == nil {
			return ManagedSpec{}, nil
		}
		return *v, nil
	case CommandRequest:
		return ManagedSpec{
			RuntimeVersion: v.RuntimeVersion,
			ModelID:        v.ModelID,
			Host:           v.Host,
			Port:           v.Port,
			ContextSize:    v.ContextSize,
			ModelAlias:     v.ModelAlias,
		}, nil
	case *CommandRequest:
		if v == nil {
			return ManagedSpec{}, nil
		}
		return ManagedSpec{
			RuntimeVersion: v.RuntimeVersion,
			ModelID:        v.ModelID,
			Host:           v.Host,
			Port:           v.Port,
			ContextSize:    v.ContextSize,
			ModelAlias:     v.ModelAlias,
		}, nil
	default:
		return ManagedSpec{}, fmt.Errorf("invalid local_llm payload type %T", payload)
	}
}

func failureResult(err error) bus.CommandResult {
	return bus.CommandResult{
		Error:   err,
		Message: err.Error(),
	}
}

func localLLMJobMetadata(spec ManagedSpec) map[string]any {
	metadata := map[string]any{}
	if spec.ModelID != "" {
		metadata["modelID"] = spec.ModelID
	}
	if spec.RuntimeVersion != "" {
		metadata["runtimeVersion"] = spec.RuntimeVersion
	}
	if spec.Port != 0 {
		metadata["port"] = spec.Port
	}
	if spec.Host != "" {
		metadata["host"] = spec.Host
	}
	if spec.ModelAlias != "" {
		metadata["modelAlias"] = spec.ModelAlias
	}
	return metadata
}
