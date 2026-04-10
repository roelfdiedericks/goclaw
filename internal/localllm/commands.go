package localllm

import (
	"context"
	"fmt"
	"time"

	"github.com/roelfdiedericks/goclaw/internal/bus"
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
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	status, err := GetManager().EnsureRuntime(ctx, spec)
	if err != nil {
		return failureResult(err)
	}
	return bus.CommandResult{
		Success: true,
		Message: fmt.Sprintf("llama.cpp runtime ensured for %s", status.ModelID),
		Data:    status,
	}
}

func handleDownloadModel(cmd bus.Command) bus.CommandResult {
	spec, err := decodeCommandRequest(cmd.Payload)
	if err != nil {
		return failureResult(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	if spec.ModelID == "" {
		return failureResult(fmt.Errorf("modelID is required"))
	}
	status, err := GetManager().EnsureRuntime(ctx, ManagedSpec{ModelID: spec.ModelID, RuntimeVersion: spec.RuntimeVersion})
	if err != nil {
		return failureResult(err)
	}
	return bus.CommandResult{
		Success: true,
		Message: fmt.Sprintf("model %s downloaded", status.ModelID),
		Data:    status,
	}
}

func handleStart(cmd bus.Command) bus.CommandResult {
	spec, err := decodeCommandRequest(cmd.Payload)
	if err != nil {
		return failureResult(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	status, err := GetManager().Start(ctx, spec)
	if err != nil {
		return failureResult(err)
	}
	return bus.CommandResult{
		Success: true,
		Message: fmt.Sprintf("llama.cpp server started at %s", status.Server.Endpoint),
		Data:    status,
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
