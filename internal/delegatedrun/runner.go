package delegatedrun

import (
	"context"
	"time"
)

type Runner interface {
	Start(ctx context.Context, spec RunSpec) (runID string, err error)
	Cancel(runID string) error
	Wait(ctx context.Context, runID string) (RunResult, RunState, error)
}

type ExecuteFunc func(ctx context.Context, spec RunSpec) (RunResult, error)

type waitResult struct {
	result RunResult
	state  RunState
	err    error
}

type activeRun struct {
	cancel context.CancelFunc
	done   chan waitResult
	start  time.Time
	spec   RunSpec
}

