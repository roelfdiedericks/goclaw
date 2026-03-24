package delegatedrun

import "strings"

// DescendantCoordinator answers descendant-activity questions for orchestration gates.
type DescendantCoordinator interface {
	HasActiveDescendants(rootRunID string) (active bool, count int)
}

// GraphDescendantCoordinator is a registry-backed coordinator over a run snapshot.
type GraphDescendantCoordinator struct {
	records []RunRecord
}

func NewGraphDescendantCoordinator(records []RunRecord) *GraphDescendantCoordinator {
	return &GraphDescendantCoordinator{records: records}
}

func (g *GraphDescendantCoordinator) HasActiveDescendants(rootRunID string) (bool, int) {
	rootRunID = strings.TrimSpace(rootRunID)
	if rootRunID == "" {
		return false, 0
	}
	children := map[string][]string{}
	stateByID := map[string]RunState{}
	for _, rec := range g.records {
		id := strings.TrimSpace(rec.RunID)
		if id == "" {
			continue
		}
		stateByID[id] = rec.State
		parent := strings.TrimSpace(rec.ParentRunID)
		if parent != "" {
			children[parent] = append(children[parent], id)
		}
	}
	queue := append([]string{}, children[rootRunID]...)
	count := 0
	for i := 0; i < len(queue); i++ {
		id := queue[i]
		if IsActiveState(stateByID[id]) {
			count++
		}
		queue = append(queue, children[id]...)
	}
	return count > 0, count
}
