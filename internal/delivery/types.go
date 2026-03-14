package delivery

import (
	"fmt"
	"strings"
)

const (
	ReasonNotRequested = "not_requested"
	ReasonNoUser       = "no_user"
	ReasonNoContent    = "no_content"
	ReasonExcluded     = "excluded"
	ReasonHasNoUser    = "channel_has_no_user"
	ReasonUnreachable  = "unreachable"
	ReasonError        = "error"
)

type AssistantMessage struct {
	Source            string
	Content           string
	Persist           bool
	PersistKind       string
	PersistContent    string
	ExcludeChannels   []string
	TargetChannel     string
	TargetIdentity    string
	BestEffortDeliver bool
}

type SystemKind string

const (
	SystemKindStatus    SystemKind = "status"
	SystemKindCron      SystemKind = "cron"
	SystemKindHeartbeat SystemKind = "heartbeat"
	SystemKindCommand   SystemKind = "command"
)

type SystemMessage struct {
	Kind           SystemKind
	Source         string
	Title          string
	Content        string
	Persist        bool
	PersistKind    string
	PersistContent string
}

func (m SystemMessage) DisplayText() string {
	title := strings.TrimSpace(m.Title)
	content := strings.TrimSpace(m.Content)
	if title == "" {
		return content
	}
	if content == "" {
		return fmt.Sprintf("**[%s]**", title)
	}
	return fmt.Sprintf("**[%s]**\n\n%s", title, content)
}

func (m SystemMessage) ContentForPersistence() string {
	if strings.TrimSpace(m.PersistContent) != "" {
		return m.PersistContent
	}
	return m.Content
}

type Result struct {
	Channel   string
	Attempted bool
	Delivered bool
	Error     string
	Reason    string
}

type Report struct {
	Generated   bool
	Persisted   bool
	Results     []Result
	DeliveredTo int
}

func (r Report) Delivered() bool {
	return r.DeliveredTo > 0
}
