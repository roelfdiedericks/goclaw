package session

import (
	"fmt"
	"strings"
	"time"
)

type CompactionKind string

const (
	CompactionKindLeaf      CompactionKind = "leaf"
	CompactionKindCondensed CompactionKind = "condensed"
)

// CompactionKindOrLeaf returns the kind if set, or CompactionKindLeaf for legacy
// rows persisted before the kind field was introduced.
func CompactionKindOrLeaf(kind CompactionKind) CompactionKind {
	if kind == "" {
		return CompactionKindLeaf
	}
	return kind
}

const (
	summaryIDPrefix = "sum_"
	messageIDPrefix = "msg_"
)

type CompactionSearchMode string

const (
	CompactionSearchModeFTS   CompactionSearchMode = "full_text"
	CompactionSearchModeRegex CompactionSearchMode = "regex"
)

type CompactionSearchSort string

const (
	CompactionSearchSortRecency   CompactionSearchSort = "recency"
	CompactionSearchSortRelevance CompactionSearchSort = "relevance"
	CompactionSearchSortHybrid    CompactionSearchSort = "hybrid"
)

type CompactionSearchResult struct {
	Compaction   StoredCompaction
	MatchSource  string
	Relevance    float64
	MatchedQuery string
}

type CompactionDAGStats struct {
	Leaves           int
	Condensed        int
	CondensedByDepth map[int]int
	Pending          int
	MaxDepth         int
	FTSRows          int

	// UnparentedLeaves is the count of leaf compactions that have no parent
	// condensed node and are not waiting on a summary retry — i.e. the set
	// eligible for the next depth-1 condensation batch.
	UnparentedLeaves int
	// UnparentedCondensedByDepth mirrors the above for condensed nodes at
	// each depth; key d holds the count of depth-d condensed nodes whose
	// parent has not yet been built.
	UnparentedCondensedByDepth map[int]int

	// NextBatchSize is the number of candidates the condensation loop will
	// consume on its next tick, or 0 if nothing is eligible. NextBatchNewDepth
	// is the depth of the node that batch will produce (0 when idle).
	// These two fields require the manager's fanout/depth config to compute
	// and are populated by CompactionManager.AnnotateNextBatchHint.
	NextBatchSize     int
	NextBatchNewDepth int
}

func FormatSummaryID(id string) string {
	if id == "" {
		return ""
	}
	if strings.HasPrefix(id, summaryIDPrefix) {
		return id
	}
	return summaryIDPrefix + id
}

func ParseSummaryID(raw string) (string, error) {
	if raw == "" {
		return "", fmt.Errorf("summary id is required")
	}
	if !strings.HasPrefix(raw, summaryIDPrefix) {
		return "", fmt.Errorf("summary id must start with %q", summaryIDPrefix)
	}
	id := strings.TrimPrefix(raw, summaryIDPrefix)
	if id == "" {
		return "", fmt.Errorf("summary id is empty after prefix")
	}
	return id, nil
}

func FormatMessageID(id string) string {
	if id == "" {
		return ""
	}
	if strings.HasPrefix(id, messageIDPrefix) {
		return id
	}
	return messageIDPrefix + id
}

func ParseMessageID(raw string) (string, error) {
	if raw == "" {
		return "", fmt.Errorf("message id is required")
	}
	if !strings.HasPrefix(raw, messageIDPrefix) {
		return "", fmt.Errorf("message id must start with %q", messageIDPrefix)
	}
	id := strings.TrimPrefix(raw, messageIDPrefix)
	if id == "" {
		return "", fmt.Errorf("message id is empty after prefix")
	}
	return id, nil
}

func cloneTimePtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	v := *t
	return &v
}
