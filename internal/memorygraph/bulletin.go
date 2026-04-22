package memorygraph

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jmcarbo/stopwords"
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

	// Section order: Identity -> High Priority -> Goals -> Upcoming Events -> Preferences -> Recent Events -> Decisions

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
	if cfg.UpcomingEventsLimit > 0 {
		now := time.Now()
		cutoff := now.AddDate(0, 0, cfg.UpcomingEventsDays)
		upcoming, err := Query().
			Username(username).
			SinceHappens(now).
			UntilHappens(cutoff).
			OrderBy("happens_at").
			Ascending().
			ThenBy("importance", true).
			Limit(cfg.UpcomingEventsLimit).
			Execute(mgr.DB())
		if err == nil {
			upcoming = filterSeen(upcoming)
			if len(upcoming) > 0 {
				var items []string
				for _, m := range upcoming {
					if m.HappensAt == nil {
						continue
					}
					items = append(items, fmt.Sprintf("- [%s] %s (%s)", m.Type, m.Content, formatUpcomingBulletinTime(now, *m.HappensAt)))
				}
				if len(items) > 0 {
					sections = append(sections, "## Upcoming Events\n"+strings.Join(items, "\n"))
				}
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

	// 0. Today's Schedule — structured routines (days + time_start) for today,
	// with time-aware annotations, tomorrow fallback, and a 10-item budget.
	if cfg.RoutinesLimit > 0 {
		if scheduleSection := buildTodaysSchedule(mgr, username, now); scheduleSection != "" {
			sections = append(sections, scheduleSection)
		}
	}

	// 1. Active routines (confidence > 0.5). Structured routines get a slim
	// cadence annotation so the agent knows they exist even on off-days.
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
			store := mgr.Store()
			var items []string
			for _, m := range routines {
				conf := ""
				if m.Confidence >= 0 {
					conf = fmt.Sprintf(" (%.0f%% confidence)", m.Confidence*100)
				}
				cadence := ""
				if meta, err := store.GetRoutineMetadata(m.UUID); err == nil && meta != nil {
					cadence = formatRoutineCadence(meta)
				}
				items = append(items, "- "+m.Content+cadence+conf)
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
			OrderBy("occurred_at").
			Descending().
			ThenBy("importance", true).
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

// BuildChatContextSection generates a chat context section by querying memories
// relevant to the user's current message using FTS (no embeddings, fast).
// Returns empty string if no relevant memories found or feature disabled.
func BuildChatContextSection(ctx context.Context, mgr *Manager, username, message string, cfg BulletinConfig) string {
	if mgr == nil || !cfg.ChatContextEnabled || cfg.ChatContextLimit <= 0 {
		return ""
	}

	// Extract keywords from message using stopwords removal
	maxKeywords := cfg.ChatContextMaxKeywords
	if maxKeywords <= 0 {
		maxKeywords = 8
	}
	keywords := ExtractKeywords(message, cfg.ChatContextLanguage, maxKeywords)
	if keywords == "" {
		L_debug("memorygraph: chat context - no keywords extracted", "message", truncateForLog(message, 50))
		return ""
	}

	L_debug("memorygraph: chat context query",
		"username", username,
		"keywords", keywords,
		"maxKeywords", maxKeywords,
		"language", cfg.ChatContextLanguage,
	)

	// Sanitize for FTS5
	ftsQuery := sanitizeFTSQuery(keywords)
	if ftsQuery == "" {
		return ""
	}

	// Query using FTS only (fast, no embedding call)
	query := `
		SELECT m.uuid, m.content, m.memory_type, bm25(memories_fts) as score
		FROM memories_fts f
		JOIN memories m ON m.id = f.rowid
		WHERE memories_fts MATCH ? AND m.forgotten = 0 AND m.username = ?
		ORDER BY score
		LIMIT ?
	`

	rows, err := mgr.DB().QueryContext(ctx, query, ftsQuery, username, cfg.ChatContextLimit)
	if err != nil {
		L_warn("memorygraph: chat context query failed", "error", err)
		return ""
	}
	defer rows.Close()

	var items []string
	for rows.Next() {
		var uuid, content, memType string
		var score float64
		if err := rows.Scan(&uuid, &content, &memType, &score); err != nil {
			continue
		}
		items = append(items, fmt.Sprintf("- [%s] %s", memType, content))
	}
	if err := rows.Err(); err != nil {
		L_warn("memorygraph: chat context iteration error", "error", err)
	}

	if len(items) == 0 {
		L_debug("memorygraph: chat context - no matches", "keywords", keywords)
		return ""
	}

	L_debug("memorygraph: chat context results", "count", len(items), "keywords", keywords)
	return "## Chat Memory Context\nUse this information before using the memory_graph_search_tool, unless nothing is relevant.\n" + strings.Join(items, "\n")
}

// ExtractKeywords removes stopwords from text and returns top N longest keywords
func ExtractKeywords(text, language string, maxKeywords int) string {
	if text == "" {
		return ""
	}

	// Default to English if language not specified
	if language == "" {
		language = "en"
	}

	// Default max keywords
	if maxKeywords <= 0 {
		maxKeywords = 8
	}

	// Remove stopwords (also strips HTML if any)
	cleaned := stopwords.CleanString(text, language, true)

	// Trim and normalize whitespace
	cleaned = strings.TrimSpace(cleaned)
	if cleaned == "" {
		return ""
	}

	// Split into words
	words := strings.Fields(cleaned)
	if len(words) == 0 {
		return ""
	}

	// If within limit, return all
	if len(words) <= maxKeywords {
		return strings.Join(words, " ")
	}

	// Sort by length (longest first) to keep most meaningful words
	type wordLen struct {
		word string
		len  int
	}
	wl := make([]wordLen, len(words))
	for i, w := range words {
		wl[i] = wordLen{word: w, len: len(w)}
	}

	// Sort by length descending
	for i := 0; i < len(wl)-1; i++ {
		for j := i + 1; j < len(wl); j++ {
			if wl[j].len > wl[i].len {
				wl[i], wl[j] = wl[j], wl[i]
			}
		}
	}

	// Take top N longest words
	result := make([]string, maxKeywords)
	for i := 0; i < maxKeywords; i++ {
		result[i] = wl[i].word
	}

	return strings.Join(result, " ")
}

// truncateForLog truncates a string for logging purposes
func truncateForLog(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func formatUpcomingBulletinTime(now, scheduled time.Time) string {
	if scheduled.Year() == now.Year() && scheduled.YearDay() == now.YearDay() {
		if hasExplicitClockTime(scheduled) {
			return "today " + scheduled.Format("15:04")
		}
		return "today"
	}

	tomorrow := now.AddDate(0, 0, 1)
	if scheduled.Year() == tomorrow.Year() && scheduled.YearDay() == tomorrow.YearDay() {
		if hasExplicitClockTime(scheduled) {
			return "tomorrow " + scheduled.Format("15:04")
		}
		return "tomorrow"
	}

	if scheduled.Before(now.AddDate(0, 0, 7)) {
		if hasExplicitClockTime(scheduled) {
			return scheduled.Format("Mon 15:04")
		}
		return scheduled.Format("Mon")
	}

	if scheduled.Year() == now.Year() {
		if hasExplicitClockTime(scheduled) {
			return scheduled.Format("Jan 2 15:04")
		}
		return scheduled.Format("Jan 2")
	}

	if hasExplicitClockTime(scheduled) {
		return scheduled.Format("2006-01-02 15:04")
	}
	return scheduled.Format("2006-01-02")
}

func hasExplicitClockTime(t time.Time) bool {
	return t.Hour() != 0 || t.Minute() != 0 || t.Second() != 0 || t.Nanosecond() != 0
}

// scheduleBudget is the maximum number of entries the Today's Schedule section
// renders across today + tomorrow + lookahead, to keep token cost bounded.
const scheduleBudget = 10

// collapseHour is the server-local hour at which completed items collapse from
// "[done]" to "Earlier: ... ✓" in Today's Schedule.
const collapseHour = 18

// buildTodaysSchedule renders Section 0 of the context bulletin: structured
// routines scheduled for today (with time-aware state annotations), tomorrow's
// schedule, and a short lookahead window (up to 3 days) when both are empty.
// Returns "" when the user has no structured routines that surface today or
// within the lookahead window.
func buildTodaysSchedule(mgr *Manager, username string, now time.Time) string {
	local := now.In(time.Local)
	todayName := dayNameNormalize(local.Weekday().String())
	store := mgr.Store()
	if store == nil {
		return ""
	}

	todayRoutines, err := store.GetRoutinesForDay(username, todayName)
	if err != nil {
		L_warn("memorygraph: GetRoutinesForDay failed", "error", err, "day", todayName)
		return ""
	}
	todayRoutines = filterRoutinesForDate(todayRoutines, local)

	// Fetch today's fires so done/earlier lines can surface non-success
	// outcomes. Non-fatal on error — formatter simply skips annotations.
	fires, ferr := store.TriggersFiredForUserOnDate(username, local)
	if ferr != nil {
		L_warn("memorygraph: TriggersFiredForUserOnDate failed", "error", ferr)
		fires = nil
	}

	// Render today body into a sub-builder so we can choose the header (with
	// or without legend) after knowing whether any annotation was emitted.
	var todayBody strings.Builder
	hadAnnotation := false
	used := 0
	if len(todayRoutines) == 0 {
		todayBody.WriteString("Today: —\n")
	} else {
		cap := scheduleBudget
		if len(todayRoutines) < cap {
			cap = len(todayRoutines)
		}
		for i := 0; i < cap; i++ {
			r := todayRoutines[i]
			line, annotated := formatTodayScheduleLine(r, local, fires)
			if line == "" {
				continue
			}
			todayBody.WriteString("- ")
			todayBody.WriteString(line)
			todayBody.WriteByte('\n')
			if annotated {
				hadAnnotation = true
			}
			used++
		}
	}

	var sb strings.Builder
	if hadAnnotation {
		sb.WriteString("## Today's Schedule (legend: [silent]=agent stayed quiet, [skipped]=poller skipped as stale, [err]=invocation error)\n")
	} else {
		sb.WriteString("## Today's Schedule\n")
	}
	sb.WriteString(todayBody.String())

	// Tomorrow subsection, if budget remains.
	if used < scheduleBudget {
		tomorrow := local.AddDate(0, 0, 1)
		tomorrowName := dayNameNormalize(tomorrow.Weekday().String())
		tomorrowRoutines, err := store.GetRoutinesForDay(username, tomorrowName)
		if err == nil {
			tomorrowRoutines = filterRoutinesForDate(tomorrowRoutines, tomorrow)
			if len(tomorrowRoutines) > 0 {
				sb.WriteString("\nTomorrow:\n")
				remaining := scheduleBudget - used
				cap := len(tomorrowRoutines)
				if cap > remaining {
					cap = remaining
				}
				for i := 0; i < cap; i++ {
					r := tomorrowRoutines[i]
					label := formatTomorrowScheduleLine(r)
					if label == "" {
						continue
					}
					sb.WriteString("- ")
					sb.WriteString(label)
					sb.WriteByte('\n')
					used++
				}
			}
		}
	}

	// Lookahead (up to 3 days out) when both today and tomorrow are empty.
	if used == 0 {
		for offset := 2; offset <= 3; offset++ {
			day := local.AddDate(0, 0, offset)
			name := dayNameNormalize(day.Weekday().String())
			rs, err := store.GetRoutinesForDay(username, name)
			if err != nil {
				continue
			}
			rs = filterRoutinesForDate(rs, day)
			if len(rs) == 0 {
				continue
			}
			sb.WriteString(fmt.Sprintf("\nNext (%s):\n", day.Format("Mon Jan 2")))
			cap := len(rs)
			if cap > scheduleBudget-used {
				cap = scheduleBudget - used
			}
			for i := 0; i < cap; i++ {
				r := rs[i]
				label := formatTomorrowScheduleLine(r)
				if label == "" {
					continue
				}
				sb.WriteString("- ")
				sb.WriteString(label)
				sb.WriteByte('\n')
				used++
			}
			break
		}
	}

	// If absolutely nothing — not even a placeholder — skip the section.
	if used == 0 && len(todayRoutines) == 0 {
		return ""
	}
	return strings.TrimRight(sb.String(), "\n")
}

// filterRoutinesForDate drops routines whose bounds exclude the target date.
// skip_dates applies only to the specific occurrence date in question.
func filterRoutinesForDate(rs []*RoutineWithMetadata, date time.Time) []*RoutineWithMetadata {
	if len(rs) == 0 {
		return nil
	}
	out := make([]*RoutineWithMetadata, 0, len(rs))
	for _, r := range rs {
		if r == nil || r.Meta == nil {
			continue
		}
		if !r.Meta.inBounds(date) {
			continue
		}
		if r.Meta.isSkipped(date) {
			continue
		}
		out = append(out, r)
	}
	return out
}

// formatTodayScheduleLine renders one routine on today's schedule with a
// time-aware state annotation. After the collapse hour, completed items are
// rendered as "Earlier: {content} ✓" without times.
//
// fires is an optional per-UUID slice of today's fire rows. When supplied and
// this line is in done/earlier state, a fire matching the line's start time
// (±30 minutes) with a non-success outcome appends a [silent]/[skipped]/[err]
// annotation. The second return value indicates whether an annotation was
// emitted (so the section builder can decide whether to show a legend header).
func formatTodayScheduleLine(r *RoutineWithMetadata, now time.Time, fires map[string][]*TriggerFired) (string, bool) {
	if r == nil || r.Memory == nil || r.Meta == nil {
		return "", false
	}
	content := r.Memory.Content
	startH, startM, err := parseClockTime(r.Meta.TimeStart)
	if err != nil {
		return content, false
	}
	start := time.Date(now.Year(), now.Month(), now.Day(), startH, startM, 0, 0, now.Location())
	end := routineEndTime(r.Meta, start)
	state := scheduleState(now, start, end)
	done := state == "[done]" || (now.Hour() >= collapseHour && now.After(end))

	annotation := ""
	if done {
		annotation = outcomeLabelForLine(fires, r.Memory.UUID, start)
	}

	// After 18:00, collapse done items.
	if now.Hour() >= collapseHour && now.After(end) {
		base := "Earlier: " + content + " ✓"
		if annotation != "" {
			return base + " " + annotation, true
		}
		return base, false
	}

	timeStr := start.Format("15:04")
	if !end.Equal(start) {
		timeStr = timeStr + "-" + end.Format("15:04")
	}
	loc := ""
	if r.Meta.Location != "" {
		loc = " @ " + r.Meta.Location
	}
	line := fmt.Sprintf("%s %s%s %s", timeStr, content, loc, state)
	if annotation != "" {
		return line + " " + annotation, true
	}
	return line, false
}

// outcomeLabelForLine selects the fire row for this occurrence (closest
// scheduled_for within ±30 minutes of start) and maps its outcome to a
// user-facing label. Returns "" when no fire row matches or when outcome is
// "fired" (success is implicit in the existing ✓ / [done] rendering).
func outcomeLabelForLine(fires map[string][]*TriggerFired, memoryUUID string, start time.Time) string {
	if fires == nil {
		return ""
	}
	rows, ok := fires[memoryUUID]
	if !ok || len(rows) == 0 {
		return ""
	}

	const tolerance = 30 * time.Minute
	var best *TriggerFired
	var bestDelta time.Duration
	for _, t := range rows {
		if t == nil {
			continue
		}
		delta := t.ScheduledFor.Sub(start)
		if delta < 0 {
			delta = -delta
		}
		if delta > tolerance {
			continue
		}
		if best == nil || delta < bestDelta {
			best = t
			bestDelta = delta
		}
	}
	if best == nil {
		return ""
	}
	switch best.Outcome {
	case "silent":
		return "[silent]"
	case "missed_grace":
		return "[skipped]"
	case "error":
		return "[err]"
	default:
		return ""
	}
}

// formatTomorrowScheduleLine renders a brief entry for tomorrow / lookahead
// days — no state annotation, but keeps the clock time and location so the
// agent can plan.
func formatTomorrowScheduleLine(r *RoutineWithMetadata) string {
	if r == nil || r.Memory == nil || r.Meta == nil {
		return ""
	}
	content := r.Memory.Content
	if r.Meta.TimeStart == "" {
		return content
	}
	timeStr := r.Meta.TimeStart
	if r.Meta.TimeEnd != "" {
		timeStr = timeStr + "-" + r.Meta.TimeEnd
	}
	loc := ""
	if r.Meta.Location != "" {
		loc = " @ " + r.Meta.Location
	}
	return fmt.Sprintf("%s %s%s", timeStr, content, loc)
}

// routineEndTime computes an effective end time for today's occurrence from
// TimeEnd first, DurationMinutes next, else the start time itself (single
// instant — the caller treats start==end as "no end shown").
func routineEndTime(meta *RoutineMetadata, start time.Time) time.Time {
	if strings.TrimSpace(meta.TimeEnd) != "" {
		if h, m, err := parseClockTime(meta.TimeEnd); err == nil {
			return time.Date(start.Year(), start.Month(), start.Day(), h, m, 0, 0, start.Location())
		}
	}
	if meta.DurationMinutes != nil && *meta.DurationMinutes > 0 {
		return start.Add(time.Duration(*meta.DurationMinutes) * time.Minute)
	}
	return start
}

// scheduleState returns the time-aware annotation for a schedule item.
func scheduleState(now, start, end time.Time) string {
	delta := start.Sub(now)
	switch {
	case delta > 15*time.Minute:
		return fmt.Sprintf("[upcoming in %s]", humaniseDuration(delta))
	case delta > 0:
		return "[coming up!]"
	case !end.Equal(start) && now.Before(end):
		return "[in progress]"
	case now.After(end) || now.Equal(end):
		return "[done]"
	default:
		return "[now]"
	}
}

// humaniseDuration prints a compact "in 2h", "in 45m", "in 1h30m" style string.
func humaniseDuration(d time.Duration) string {
	if d < time.Minute {
		return "<1m"
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	hours := int(d.Hours())
	mins := int(d.Minutes()) - hours*60
	if mins == 0 {
		return fmt.Sprintf("%dh", hours)
	}
	return fmt.Sprintf("%dh%02dm", hours, mins)
}

// formatRoutineCadence returns a parenthesised cadence annotation for the
// Active Routines section, e.g. " (Tue, Thu @ 17:45)". Empty when the routine
// has no structured recurrence.
func formatRoutineCadence(meta *RoutineMetadata) string {
	if meta == nil {
		return ""
	}
	if len(meta.Days) == 0 && strings.TrimSpace(meta.TimeStart) == "" {
		return ""
	}
	var parts []string
	if len(meta.Days) > 0 {
		short := make([]string, 0, len(meta.Days))
		for _, d := range meta.Days {
			n := dayNameNormalize(d)
			if n == "" {
				continue
			}
			// Short form: first 3 letters, capitalised.
			if len(n) >= 3 {
				short = append(short, strings.ToUpper(n[:1])+n[1:3])
			}
		}
		if len(short) > 0 {
			parts = append(parts, strings.Join(short, ", "))
		}
	}
	if strings.TrimSpace(meta.TimeStart) != "" {
		parts = append(parts, "@ "+meta.TimeStart)
	}
	if len(parts) == 0 {
		return ""
	}
	return " (" + strings.Join(parts, " ") + ")"
}
