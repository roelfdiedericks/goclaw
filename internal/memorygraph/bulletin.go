package memorygraph

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/roelfdiedericks/goclaw/internal/llm"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// BuildMemoryBulletin generates an LLM-synthesized memory summary for a user
// wordLimit controls the target word count (0 = no limit)
func BuildMemoryBulletin(ctx context.Context, mgr *Manager, provider llm.Provider, username string, wordLimit int) (string, error) {
	if mgr == nil {
		return "", fmt.Errorf("no memory graph manager")
	}

	L_debug("memorygraph: building memory bulletin", "username", username)

	// Gather memories for the bulletin
	var sections []string

	// Identity memories (top 5 by importance)
	identities, err := Query().
		Username(username).
		Types(TypeIdentity).
		OrderBy("importance").
		Descending().
		Limit(5).
		Execute(mgr.DB())
	if err == nil && len(identities) > 0 {
		var items []string
		for _, m := range identities {
			items = append(items, "- "+m.Content)
		}
		sections = append(sections, "## Identity\n"+strings.Join(items, "\n"))
	}

	// Recent memories (last 10)
	recent, err := Query().
		Username(username).
		OrderBy("created_at").
		Descending().
		Limit(10).
		Execute(mgr.DB())
	if err == nil && len(recent) > 0 {
		var items []string
		for _, m := range recent {
			items = append(items, fmt.Sprintf("- [%s] %s", m.Type, m.Content))
		}
		sections = append(sections, "## Recent Memories\n"+strings.Join(items, "\n"))
	}

	// Decisions (last 5)
	decisions, err := Query().
		Username(username).
		Types(TypeDecision).
		OrderBy("created_at").
		Descending().
		Limit(5).
		Execute(mgr.DB())
	if err == nil && len(decisions) > 0 {
		var items []string
		for _, m := range decisions {
			items = append(items, "- "+m.Content)
		}
		sections = append(sections, "## Recent Decisions\n"+strings.Join(items, "\n"))
	}

	// High importance memories (>0.8, top 10)
	important, err := Query().
		Username(username).
		MinImportance(0.8).
		OrderBy("importance").
		Descending().
		Limit(10).
		Execute(mgr.DB())
	if err == nil && len(important) > 0 {
		var items []string
		for _, m := range important {
			items = append(items, fmt.Sprintf("- [%.0f%%] %s", m.Importance*100, m.Content))
		}
		sections = append(sections, "## High Priority\n"+strings.Join(items, "\n"))
	}

	// Preferences (top 5)
	preferences, err := Query().
		Username(username).
		Types(TypePreference).
		OrderBy("importance").
		Descending().
		Limit(5).
		Execute(mgr.DB())
	if err == nil && len(preferences) > 0 {
		var items []string
		for _, m := range preferences {
			items = append(items, "- "+m.Content)
		}
		sections = append(sections, "## Preferences\n"+strings.Join(items, "\n"))
	}

	// Goals (top 3)
	goals, err := Query().
		Username(username).
		Types(TypeGoal).
		OrderBy("importance").
		Descending().
		Limit(3).
		Execute(mgr.DB())
	if err == nil && len(goals) > 0 {
		var items []string
		for _, m := range goals {
			items = append(items, "- "+m.Content)
		}
		sections = append(sections, "## Active Goals\n"+strings.Join(items, "\n"))
	}

	if len(sections) == 0 {
		return "No memories found for this user.", nil
	}

	// If no LLM provider, return the raw structured data
	if provider == nil {
		return strings.Join(sections, "\n\n"), nil
	}

	// Use LLM to synthesize into natural language
	var lengthInstruction string
	if wordLimit > 0 {
		lengthInstruction = fmt.Sprintf("about %d words", wordLimit)
	} else {
		lengthInstruction = "as comprehensive as needed"
	}

	prompt := fmt.Sprintf(`Synthesize the following memory data into a natural language summary (%s). 
Write as if describing what you know about this person - their identity, preferences, goals, and recent activities.
Be conversational but informative. Don't list items, integrate them into flowing prose.

%s`, lengthInstruction, strings.Join(sections, "\n\n"))

	const systemPrompt = `You are a memory synthesis assistant. Your task is to convert structured memory data into a flowing, natural language summary. Focus on the most important and interesting aspects. Be concise but thorough.`

	response, err := provider.SimpleMessage(ctx, prompt, systemPrompt)
	if err != nil {
		L_warn("memorygraph: LLM synthesis failed, returning raw data", "error", err)
		return strings.Join(sections, "\n\n"), nil
	}

	return response, nil
}

// BuildContextBulletin generates a programmatic context bulletin for anticipatory intelligence
// Returns structured data about routines, predictions, and correlations
func BuildContextBulletin(mgr *Manager, username string) (string, error) {
	if mgr == nil {
		return "", fmt.Errorf("no memory graph manager")
	}

	L_debug("memorygraph: building context bulletin", "username", username)

	var sections []string
	now := time.Now()

	// Active routines (confidence > 0.5)
	routines, err := Query().
		Username(username).
		Types(TypeRoutine).
		MinConfidence(0.5).
		OrderBy("confidence").
		Descending().
		Limit(10).
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

	// Pending predictions (next 2 hours)
	twoHoursLater := now.Add(2 * time.Hour)
	predictions, err := Query().
		Username(username).
		Types(TypePrediction).
		HasTriggerBefore(twoHoursLater).
		OrderBy("importance").
		Descending().
		Limit(5).
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

	// Active correlations (confidence > 0.6)
	correlations, err := Query().
		Username(username).
		Types(TypeCorrelation).
		MinConfidence(0.6).
		OrderBy("confidence").
		Descending().
		Limit(5).
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

	// Recent anomalies (last 24h)
	yesterday := now.Add(-24 * time.Hour)
	anomalies, err := Query().
		Username(username).
		Types(TypeAnomaly).
		SinceCreated(yesterday).
		OrderBy("created_at").
		Descending().
		Limit(5).
		Execute(mgr.DB())
	if err == nil && len(anomalies) > 0 {
		var items []string
		for _, m := range anomalies {
			items = append(items, "- "+m.Content)
		}
		sections = append(sections, "## Recent Anomalies\n"+strings.Join(items, "\n"))
	}

	// Pending todos
	todos, err := Query().
		Username(username).
		Types(TypeTodo).
		OrderBy("importance").
		Descending().
		Limit(5).
		Execute(mgr.DB())
	if err == nil && len(todos) > 0 {
		var items []string
		for _, m := range todos {
			items = append(items, "- "+m.Content)
		}
		sections = append(sections, "## Pending Todos\n"+strings.Join(items, "\n"))
	}

	if len(sections) == 0 {
		return "No context data found for this user.", nil
	}

	// Context bulletin is always structured, no LLM synthesis
	header := fmt.Sprintf("# Context Bulletin for %s\nGenerated: %s\n", username, now.Format(time.RFC3339))
	return header + "\n" + strings.Join(sections, "\n\n"), nil
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
	sb.WriteString(fmt.Sprintf("# Memory Graph Statistics\n\n"))
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
