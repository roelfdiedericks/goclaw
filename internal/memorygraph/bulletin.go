package memorygraph

import (
	"context"
	"fmt"
	"strings"
	"time"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// BuildMemoryBulletinWithConfig generates a memory bulletin using configurable limits
// Returns empty string if no sections have content (not a sentinel message)
func BuildMemoryBulletinWithConfig(ctx context.Context, mgr *Manager, username string, cfg BulletinConfig) (string, error) {
	if mgr == nil {
		return "", fmt.Errorf("no memory graph manager")
	}

	L_debug("memorygraph: building memory bulletin", "username", username)

	var sections []string
	var seenUUIDs map[string]bool
	if cfg.Deduplicate {
		seenUUIDs = make(map[string]bool)
	}

	// Helper to filter already-seen memories
	filterSeen := func(memories []*Memory) []*Memory {
		if seenUUIDs == nil {
			return memories
		}
		var filtered []*Memory
		for _, m := range memories {
			if !seenUUIDs[m.UUID] {
				filtered = append(filtered, m)
				seenUUIDs[m.UUID] = true
			}
		}
		return filtered
	}

	// Section order: Identity -> High Priority -> Goals -> Preferences -> Recent Events -> Decisions

	// 1. Identity (by importance)
	if cfg.IdentityLimit > 0 {
		identities, err := Query().
			Username(username).
			Types(TypeIdentity).
			OrderBy("importance").
			Descending().
			Limit(cfg.IdentityLimit).
			Execute(mgr.DB())
		if err == nil {
			identities = filterSeen(identities)
			if len(identities) > 0 {
				var items []string
				for _, m := range identities {
					items = append(items, "- "+m.Content)
				}
				sections = append(sections, "## Identity\n"+strings.Join(items, "\n"))
			}
		}
	}

	// 2. High Priority (importance >= threshold, all types)
	if cfg.HighPriorityLimit > 0 {
		important, err := Query().
			Username(username).
			MinImportance(float32(cfg.HighPriorityThreshold)).
			OrderBy("importance").
			Descending().
			Limit(cfg.HighPriorityLimit).
			Execute(mgr.DB())
		if err == nil {
			important = filterSeen(important)
			if len(important) > 0 {
				var items []string
				for _, m := range important {
					items = append(items, fmt.Sprintf("- [%.0f%%] %s", m.Importance*100, m.Content))
				}
				sections = append(sections, "## High Priority\n"+strings.Join(items, "\n"))
			}
		}
	}

	// 3. Goals (by importance)
	if cfg.GoalsLimit > 0 {
		goals, err := Query().
			Username(username).
			Types(TypeGoal).
			OrderBy("importance").
			Descending().
			Limit(cfg.GoalsLimit).
			Execute(mgr.DB())
		if err == nil {
			goals = filterSeen(goals)
			if len(goals) > 0 {
				var items []string
				for _, m := range goals {
					items = append(items, "- "+m.Content)
				}
				sections = append(sections, "## Active Goals\n"+strings.Join(items, "\n"))
			}
		}
	}

	// 4. Preferences (by importance)
	if cfg.PreferencesLimit > 0 {
		preferences, err := Query().
			Username(username).
			Types(TypePreference).
			OrderBy("importance").
			Descending().
			Limit(cfg.PreferencesLimit).
			Execute(mgr.DB())
		if err == nil {
			preferences = filterSeen(preferences)
			if len(preferences) > 0 {
				var items []string
				for _, m := range preferences {
					items = append(items, "- "+m.Content)
				}
				sections = append(sections, "## Preferences\n"+strings.Join(items, "\n"))
			}
		}
	}

	// 5. Recent Events (type=event, time-bounded)
	if cfg.RecentEventsLimit > 0 {
		cutoff := time.Now().AddDate(0, 0, -cfg.RecentEventsDays)
		events, err := Query().
			Username(username).
			Types(TypeEvent).
			SinceOccurred(cutoff).
			OrderBy("occurred_at").
			Descending().
			Limit(cfg.RecentEventsLimit).
			Execute(mgr.DB())
		if err == nil {
			events = filterSeen(events)
			if len(events) > 0 {
				var items []string
				for _, m := range events {
					items = append(items, fmt.Sprintf("- [%s] %s", m.Type, m.Content))
				}
				sections = append(sections, "## Recent Events\n"+strings.Join(items, "\n"))
			}
		}
	}

	// 6. Decisions (time-bounded)
	if cfg.DecisionsLimit > 0 {
		cutoff := time.Now().AddDate(0, 0, -cfg.DecisionsDays)
		decisions, err := Query().
			Username(username).
			Types(TypeDecision).
			SinceOccurred(cutoff).
			OrderBy("occurred_at").
			Descending().
			Limit(cfg.DecisionsLimit).
			Execute(mgr.DB())
		if err == nil {
			decisions = filterSeen(decisions)
			if len(decisions) > 0 {
				var items []string
				for _, m := range decisions {
					items = append(items, "- "+m.Content)
				}
				sections = append(sections, "## Recent Decisions\n"+strings.Join(items, "\n"))
			}
		}
	}

	// Return empty string if no sections (not a sentinel message)
	if len(sections) == 0 {
		return "", nil
	}

	return strings.Join(sections, "\n\n"), nil
}

// BuildContextBulletinWithConfig generates a context bulletin using configurable limits
// If omitHeader is true, skips the "# Context Bulletin for X" header (for injection)
// Returns empty string if no sections have content
func BuildContextBulletinWithConfig(mgr *Manager, username string, cfg BulletinConfig, omitHeader bool) (string, error) {
	if mgr == nil {
		return "", fmt.Errorf("no memory graph manager")
	}

	L_debug("memorygraph: building context bulletin", "username", username)

	var sections []string
	now := time.Now()

	// 1. Active routines (confidence > 0.5)
	if cfg.RoutinesLimit > 0 {
		routines, err := Query().
			Username(username).
			Types(TypeRoutine).
			MinConfidence(0.5).
			OrderBy("confidence").
			Descending().
			Limit(cfg.RoutinesLimit).
			Execute(mgr.DB())
		if err == nil && len(routines) > 0 {
			var items []string
			for _, m := range routines {
				conf := ""
				if m.Confidence >= 0 {
					conf = fmt.Sprintf(" (%.0f%% confidence)", m.Confidence*100)
				}
				items = append(items, "- "+m.Content+conf)
			}
			sections = append(sections, "## Active Routines\n"+strings.Join(items, "\n"))
		}
	}

	// 2. Upcoming predictions (next 2 hours)
	if cfg.PredictionsLimit > 0 {
		twoHoursLater := now.Add(2 * time.Hour)
		predictions, err := Query().
			Username(username).
			Types(TypePrediction).
			HasTriggerBefore(twoHoursLater).
			OrderBy("importance").
			Descending().
			Limit(cfg.PredictionsLimit).
			Execute(mgr.DB())
		if err == nil && len(predictions) > 0 {
			var items []string
			for _, m := range predictions {
				trigger := ""
				if m.NextTriggerAt != nil {
					trigger = fmt.Sprintf(" [due: %s]", m.NextTriggerAt.Format("15:04"))
				}
				items = append(items, "- "+m.Content+trigger)
			}
			sections = append(sections, "## Upcoming Predictions\n"+strings.Join(items, "\n"))
		}
	}

	// 3. Known correlations (confidence > 0.6)
	if cfg.CorrelationsLimit > 0 {
		correlations, err := Query().
			Username(username).
			Types(TypeCorrelation).
			MinConfidence(0.6).
			OrderBy("confidence").
			Descending().
			Limit(cfg.CorrelationsLimit).
			Execute(mgr.DB())
		if err == nil && len(correlations) > 0 {
			var items []string
			for _, m := range correlations {
				conf := ""
				if m.Confidence >= 0 {
					conf = fmt.Sprintf(" (%.0f%% confidence)", m.Confidence*100)
				}
				items = append(items, "- "+m.Content+conf)
			}
			sections = append(sections, "## Known Correlations\n"+strings.Join(items, "\n"))
		}
	}

	// 4. Recent anomalies (last 24h)
	if cfg.AnomaliesLimit > 0 {
		yesterday := now.Add(-24 * time.Hour)
		anomalies, err := Query().
			Username(username).
			Types(TypeAnomaly).
			SinceOccurred(yesterday).
			OrderBy("occurred_at").
			Descending().
			Limit(cfg.AnomaliesLimit).
			Execute(mgr.DB())
		if err == nil && len(anomalies) > 0 {
			var items []string
			for _, m := range anomalies {
				items = append(items, "- "+m.Content)
			}
			sections = append(sections, "## Recent Anomalies\n"+strings.Join(items, "\n"))
		}
	}

	// 5. Pending todos
	if cfg.TodosLimit > 0 {
		todos, err := Query().
			Username(username).
			Types(TypeTodo).
			OrderBy("importance").
			Descending().
			Limit(cfg.TodosLimit).
			Execute(mgr.DB())
		if err == nil && len(todos) > 0 {
			var items []string
			for _, m := range todos {
				items = append(items, "- "+m.Content)
			}
			sections = append(sections, "## Pending Todos\n"+strings.Join(items, "\n"))
		}
	}

	// Return empty string if no sections
	if len(sections) == 0 {
		return "", nil
	}

	// Build result
	result := strings.Join(sections, "\n\n")

	// Add header unless omitted (for injection, header is added by the wrapper)
	if !omitHeader {
		header := fmt.Sprintf("# Context Bulletin for %s\nGenerated: %s\n\n", username, now.Format(time.RFC3339))
		result = header + result
	}

	return result, nil
}

// BuildStatsSummary returns a brief statistics summary of the memory graph
func BuildStatsSummary(mgr *Manager) (string, error) {
	if mgr == nil {
		return "", fmt.Errorf("no memory graph manager")
	}

	stats, err := mgr.Stats()
	if err != nil {
		return "", fmt.Errorf("get stats: %w", err)
	}

	var sb strings.Builder
	sb.WriteString("# Memory Graph Statistics\n\n")
	sb.WriteString(fmt.Sprintf("- Total Memories: %d\n", stats.TotalMemories))
	sb.WriteString(fmt.Sprintf("- Total Associations: %d\n", stats.TotalAssociations))
	sb.WriteString(fmt.Sprintf("- With Embeddings: %d\n", stats.WithEmbeddings))

	if len(stats.ByType) > 0 {
		sb.WriteString("\n## By Type\n")
		for t, count := range stats.ByType {
			sb.WriteString(fmt.Sprintf("- %s: %d\n", t, count))
		}
	}

	// Get ingestion stats
	ingestionStats, err := GetIngestionStats(mgr.DB())
	if err == nil && len(ingestionStats) > 0 {
		sb.WriteString("\n## Ingestion\n")
		for k, v := range ingestionStats {
			sb.WriteString(fmt.Sprintf("- %s: %d\n", k, v))
		}
	}

	return sb.String(), nil
}
