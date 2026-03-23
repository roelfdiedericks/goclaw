package delegatedrun

import "time"

type RunState string

const (
	RunStateQueued    RunState = "queued"
	RunStateRunning   RunState = "running"
	RunStateCompleted RunState = "completed"
	RunStateFailed    RunState = "failed"
	RunStateTimeout   RunState = "timeout"
	RunStateCanceled  RunState = "canceled"
)

type RunUsage struct {
	InputTokens            int64 `json:"inputTokens"`
	OutputTokens           int64 `json:"outputTokens"`
	CacheReadTokens        int64 `json:"cacheReadTokens"`
	CacheWriteTokens       int64 `json:"cacheWriteTokens"`
	EstimatedCostMicroUSD  int64 `json:"estimatedCostMicroUsd"`
}

// RunSpec is the minimal delegated run input contract.
// Keep this focused on shared execution concerns.
type RunSpec struct {
	ParentRunID    string
	RequesterType  string
	RequesterID    string
	RequesterSessionKey string
	SessionKey     string
	Prompt         string
	Purpose        string
	ResultMode     string
	DispatchOrder  string
	FallbackMode   string
	InjectMode     string
	CompletionDispatchSeq int
	FreshContext   bool
	Ephemeral      bool
	TimeoutSeconds int
	UserID         string
	EnableThinking bool
	SkipMirror     bool
	JobName        string
}

type RunResult struct {
	FinalText string
	Error     string
	Usage     RunUsage
}

type RunRecord struct {
	RunID         string
	ParentRunID   string
	RequesterType string
	RequesterID   string
	RequesterSessionKey string
	SessionKey    string
	Purpose       string
	ResultMode    string
	DispatchOrder string
	FallbackMode  string
	InjectMode    string
	CompletionDispatchKey string
	CompletionDispatchSeq int
	State         RunState
	StartedAt     time.Time
	FinishedAt    *time.Time
	Result        RunResult
}

