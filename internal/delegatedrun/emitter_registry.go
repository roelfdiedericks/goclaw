package delegatedrun

import "context"

type RegistryEmitter struct {
	reg Registry
}

func NewRegistryEmitter(reg Registry) *RegistryEmitter {
	return &RegistryEmitter{reg: reg}
}

func (e *RegistryEmitter) EmitStarted(_ context.Context, _ StartedEvent) error {
	return nil
}

func (e *RegistryEmitter) EmitProgress(_ context.Context, _ ProgressEvent) error {
	return nil
}

func (e *RegistryEmitter) EmitCompleted(_ context.Context, _ CompletedEvent) error {
	return nil
}

func (e *RegistryEmitter) EmitFailed(_ context.Context, _ FailedEvent) error {
	return nil
}

func (e *RegistryEmitter) EmitCanceled(_ context.Context, _ CanceledEvent) error {
	return nil
}
