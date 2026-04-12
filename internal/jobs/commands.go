package jobs

import (
	"fmt"

	"github.com/roelfdiedericks/goclaw/internal/bus"
)

type StatusRequest struct {
	JobID string `json:"jobID"`
}

type CancelRequest struct {
	JobID string `json:"jobID"`
}

type ListRequest struct {
	OwnerComponent string `json:"ownerComponent,omitempty"`
}

func RegisterCommands() {
	bus.RegisterCommand("jobs", "status", handleStatus)
	bus.RegisterCommand("jobs", "cancel", handleCancel)
	bus.RegisterCommand("jobs", "list", handleList)
}

func handleStatus(cmd bus.Command) bus.CommandResult {
	req, err := decodeStatusRequest(cmd.Payload)
	if err != nil {
		return failureResult(err)
	}
	status, ok := GetManager().Status(req.JobID)
	if !ok {
		return failureResult(fmt.Errorf("%w: %s", ErrJobNotFound, req.JobID))
	}
	return bus.CommandResult{
		Success: true,
		Message: "job status loaded",
		Data:    status,
	}
}

func handleCancel(cmd bus.Command) bus.CommandResult {
	req, err := decodeCancelRequest(cmd.Payload)
	if err != nil {
		return failureResult(err)
	}
	status, err := GetManager().Cancel(req.JobID)
	if err != nil {
		return failureResult(err)
	}
	return bus.CommandResult{
		Success: true,
		Message: "job cancellation requested",
		Data:    status,
	}
}

func handleList(cmd bus.Command) bus.CommandResult {
	req, err := decodeListRequest(cmd.Payload)
	if err != nil {
		return failureResult(err)
	}
	return bus.CommandResult{
		Success: true,
		Message: "job list loaded",
		Data:    GetManager().List(req.OwnerComponent),
	}
}

func decodeStatusRequest(payload any) (StatusRequest, error) {
	switch v := payload.(type) {
	case StatusRequest:
		return v, nil
	case *StatusRequest:
		if v == nil {
			return StatusRequest{}, fmt.Errorf("jobID is required")
		}
		return *v, nil
	default:
		return StatusRequest{}, fmt.Errorf("invalid jobs.status payload type %T", payload)
	}
}

func decodeCancelRequest(payload any) (CancelRequest, error) {
	switch v := payload.(type) {
	case CancelRequest:
		return v, nil
	case *CancelRequest:
		if v == nil {
			return CancelRequest{}, fmt.Errorf("jobID is required")
		}
		return *v, nil
	default:
		return CancelRequest{}, fmt.Errorf("invalid jobs.cancel payload type %T", payload)
	}
}

func decodeListRequest(payload any) (ListRequest, error) {
	switch v := payload.(type) {
	case nil:
		return ListRequest{}, nil
	case ListRequest:
		return v, nil
	case *ListRequest:
		if v == nil {
			return ListRequest{}, nil
		}
		return *v, nil
	default:
		return ListRequest{}, fmt.Errorf("invalid jobs.list payload type %T", payload)
	}
}

func failureResult(err error) bus.CommandResult {
	return bus.CommandResult{
		Error:   err,
		Message: err.Error(),
	}
}
