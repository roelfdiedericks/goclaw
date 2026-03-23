package delegatedrun

import "time"

const EventSchemaVersion = 1

type StartedEvent struct {
	RunID         string    `json:"runId"`
	ParentRunID   string    `json:"parentRunId,omitempty"`
	RequesterType string    `json:"requesterType"`
	RequesterID   string    `json:"requesterId"`
	State         RunState  `json:"state"`
	SessionKey    string    `json:"sessionKey"`
	Purpose       string    `json:"purpose"`
	StartedAt     time.Time `json:"startedAt"`
	SchemaVersion int       `json:"schemaVersion"`
}

type ProgressEvent struct {
	RunID         string    `json:"runId"`
	ParentRunID   string    `json:"parentRunId,omitempty"`
	RequesterType string    `json:"requesterType"`
	RequesterID   string    `json:"requesterId"`
	State         RunState  `json:"state"`
	Message       string    `json:"message"`
	At            time.Time `json:"at"`
	SchemaVersion int       `json:"schemaVersion"`
}

type CompletedEvent struct {
	RunID         string    `json:"runId"`
	ParentRunID   string    `json:"parentRunId,omitempty"`
	RequesterType string    `json:"requesterType"`
	RequesterID   string    `json:"requesterId"`
	State         RunState  `json:"state"`
	StartedAt     time.Time `json:"startedAt"`
	FinishedAt    time.Time `json:"finishedAt"`
	Usage         RunUsage  `json:"usage"`
	SchemaVersion int       `json:"schemaVersion"`
}

type FailedEvent struct {
	RunID         string    `json:"runId"`
	ParentRunID   string    `json:"parentRunId,omitempty"`
	RequesterType string    `json:"requesterType"`
	RequesterID   string    `json:"requesterId"`
	State         RunState  `json:"state"`
	Error         string    `json:"error"`
	StartedAt     time.Time `json:"startedAt"`
	FinishedAt    time.Time `json:"finishedAt"`
	SchemaVersion int       `json:"schemaVersion"`
}

type CanceledEvent struct {
	RunID         string    `json:"runId"`
	ParentRunID   string    `json:"parentRunId,omitempty"`
	RequesterType string    `json:"requesterType"`
	RequesterID   string    `json:"requesterId"`
	State         RunState  `json:"state"`
	Error         string    `json:"error,omitempty"`
	StartedAt     time.Time `json:"startedAt"`
	FinishedAt    time.Time `json:"finishedAt"`
	SchemaVersion int       `json:"schemaVersion"`
}

