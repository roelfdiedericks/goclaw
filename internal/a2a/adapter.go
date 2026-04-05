package a2a

import (
	"context"

	a2aproto "github.com/a2aproject/a2a-go/v2/a2a"
)

type ExecutionAdapter interface {
	Execute(ctx context.Context, req ExecutionRequest, emit func(a2aproto.Event)) error
}

type GatewayAdapter struct {
	executor Executor
}

func NewGatewayAdapter(executor Executor) *GatewayAdapter {
	return &GatewayAdapter{executor: executor}
}

func (a *GatewayAdapter) Execute(ctx context.Context, req ExecutionRequest, emit func(a2aproto.Event)) error {
	return a.executor.ExecuteTask(ctx, req, emit)
}
