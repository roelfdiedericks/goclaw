package delegatedrun

import (
	"context"
)

type Emitter interface {
	EmitStarted(ctx context.Context, ev StartedEvent) error
	EmitProgress(ctx context.Context, ev ProgressEvent) error
	EmitCompleted(ctx context.Context, ev CompletedEvent) error
	EmitFailed(ctx context.Context, ev FailedEvent) error
	EmitCanceled(ctx context.Context, ev CanceledEvent) error
}

type CompositeEmitter struct {
	emitters []Emitter
}

func NewCompositeEmitter(emitters ...Emitter) *CompositeEmitter {
	return &CompositeEmitter{emitters: emitters}
}

func (c *CompositeEmitter) EmitStarted(ctx context.Context, ev StartedEvent) error {
	for _, em := range c.emitters {
		_ = em.EmitStarted(ctx, ev)
	}
	return nil
}

func (c *CompositeEmitter) EmitProgress(ctx context.Context, ev ProgressEvent) error {
	for _, em := range c.emitters {
		_ = em.EmitProgress(ctx, ev)
	}
	return nil
}

func (c *CompositeEmitter) EmitCompleted(ctx context.Context, ev CompletedEvent) error {
	for _, em := range c.emitters {
		_ = em.EmitCompleted(ctx, ev)
	}
	return nil
}

func (c *CompositeEmitter) EmitFailed(ctx context.Context, ev FailedEvent) error {
	for _, em := range c.emitters {
		_ = em.EmitFailed(ctx, ev)
	}
	return nil
}

func (c *CompositeEmitter) EmitCanceled(ctx context.Context, ev CanceledEvent) error {
	for _, em := range c.emitters {
		_ = em.EmitCanceled(ctx, ev)
	}
	return nil
}

