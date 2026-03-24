package delegatedrun

import (
	"fmt"
	"strings"
)

// DispatchPath identifies a completion dispatch transport.
type DispatchPath string

const (
	DispatchPathNone   DispatchPath = "none"
	DispatchPathQueue  DispatchPath = "queue"
	DispatchPathDirect DispatchPath = "direct"
)

// CompletionDispatchPolicy controls primary and fallback dispatch paths.
type CompletionDispatchPolicy struct {
	Primary  DispatchPath
	Fallback DispatchPath
}

// CompletionDispatchPhase captures an attempted dispatch phase outcome.
type CompletionDispatchPhase struct {
	Phase     string
	Path      DispatchPath
	Delivered bool
	Error     string
}

func normalizeDispatchPath(path DispatchPath) DispatchPath {
	switch DispatchPath(strings.TrimSpace(string(path))) {
	case DispatchPathQueue:
		return DispatchPathQueue
	case DispatchPathDirect:
		return DispatchPathDirect
	default:
		return DispatchPathNone
	}
}

func phaseLabel(path DispatchPath, kind string) string {
	return fmt.Sprintf("%s_%s", path, kind)
}
