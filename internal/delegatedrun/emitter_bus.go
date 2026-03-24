package delegatedrun

import (
	"context"

	"github.com/roelfdiedericks/goclaw/internal/bus"
)

type BusBridgeEmitter struct{}

func NewBusBridgeEmitter() *BusBridgeEmitter {
	return &BusBridgeEmitter{}
}

func (e *BusBridgeEmitter) EmitStarted(_ context.Context, ev StartedEvent) error {
	bus.PublishEvent("delegated.run.started", ev)
	return nil
}

func (e *BusBridgeEmitter) EmitProgress(_ context.Context, ev ProgressEvent) error {
	bus.PublishEvent("delegated.run.progress", ev)
	return nil
}

func (e *BusBridgeEmitter) EmitCompleted(_ context.Context, ev CompletedEvent) error {
	bus.PublishEvent("delegated.run.completed", ev)
	return nil
}

func (e *BusBridgeEmitter) EmitFailed(_ context.Context, ev FailedEvent) error {
	bus.PublishEvent("delegated.run.failed", ev)
	return nil
}

func (e *BusBridgeEmitter) EmitCanceled(_ context.Context, ev CanceledEvent) error {
	bus.PublishEvent("delegated.run.canceled", ev)
	return nil
}
