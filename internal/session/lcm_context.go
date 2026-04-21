package session

import (
	"fmt"
	"sort"
	"strings"
	"time"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// BuildLCMSummaryContext assembles the XML-wrapped summary blocks that get
// prepended to the fresh tail of the conversation. mode selects frontier (DAG
// roots fit to budget) vs all (every block). maxTokens caps the total. Both
// inputs are expected to already be normalized by NormalizeSessionConfig.
func BuildLCMSummaryContext(compactions []StoredCompaction, mode string, maxTokens int) (string, CompactionDAGStats) {
	stats := buildCompactionDAGStats(compactions)
	if len(compactions) == 0 {
		return "", stats
	}

	mode = normalizeLCMSummaryInjectionMode(mode)
	if maxTokens <= 0 {
		maxTokens = defaultLCMBudgetTokens
	}

	byID := make(map[string]StoredCompaction, len(compactions))
	for _, comp := range compactions {
		byID[comp.ID] = comp
	}

	selected := compactions
	if mode == LCMSummaryInjectionModeFrontier {
		frontier := selectLCMFrontier(compactions, byID)
		selected = fitLCMFrontierBudget(frontier, byID, maxTokens)
		L_info("lcm: assembled frontier summary context",
			"totalBlocks", len(compactions),
			"frontierBlocks", len(frontier),
			"injectedBlocks", len(selected),
			"maxInjectedSummaryTokens", maxTokens)
	} else {
		L_info("lcm: assembled full summary context", "blocks", len(compactions))
	}

	if len(selected) == 0 {
		L_warn("lcm: no summary blocks selected for injection",
			"mode", mode,
			"availableBlocks", len(compactions))
		return "", stats
	}

	descendantMemo := make(map[string]int, len(compactions))
	blocks := make([]string, 0, len(selected))
	debugIDs := make([]string, 0, len(selected))
	totalTokens := 0
	var earliest, latest time.Time
	for _, comp := range selected {
		block := renderCompactionXML(comp, byID, descendantMemo)
		blocks = append(blocks, block)
		totalTokens += estimateLCMTextTokens(block)
		debugIDs = append(debugIDs, fmt.Sprintf("%s[%s..%s]",
			comp.ID,
			formatOptionalTime(comp.EarliestMessageAt),
			formatOptionalTime(comp.LatestMessageAt),
		))
		if comp.EarliestMessageAt != nil {
			if earliest.IsZero() || comp.EarliestMessageAt.Before(earliest) {
				earliest = comp.EarliestMessageAt.UTC()
			}
		}
		if comp.LatestMessageAt != nil {
			if latest.IsZero() || comp.LatestMessageAt.After(latest) {
				latest = comp.LatestMessageAt.UTC()
			}
		}
	}

	L_info("lcm: assembled summary context",
		"mode", mode,
		"leaves", stats.Leaves,
		"condensed", stats.Condensed,
		"maxDepth", stats.MaxDepth,
		"injectedBlocks", len(selected),
		"totalTokens", totalTokens,
		"earliestAt", formatOptionalTimeValue(earliest),
		"latestAt", formatOptionalTimeValue(latest))
	L_debug("lcm: injected blocks", "ids", strings.Join(debugIDs, ","))

	return strings.Join(blocks, "\n\n") + "\n\n<fresh_tail>", stats
}

func buildCompactionDAGStats(compactions []StoredCompaction) CompactionDAGStats {
	stats := CompactionDAGStats{
		CondensedByDepth:           make(map[int]int),
		UnparentedCondensedByDepth: make(map[int]int),
	}

	childSet := make(map[string]bool, len(compactions))
	for _, comp := range compactions {
		for _, childID := range comp.ChildCompactionIDs {
			childSet[childID] = true
		}
	}

	for _, comp := range compactions {
		unparented := !childSet[comp.ID] && !comp.NeedsSummaryRetry
		if comp.Kind == CompactionKindCondensed {
			stats.Condensed++
			stats.CondensedByDepth[comp.Depth]++
			if comp.Depth > stats.MaxDepth {
				stats.MaxDepth = comp.Depth
			}
			if unparented {
				stats.UnparentedCondensedByDepth[comp.Depth]++
			}
		} else {
			stats.Leaves++
			if unparented {
				stats.UnparentedLeaves++
			}
		}
		if comp.NeedsSummaryRetry {
			stats.Pending++
		}
	}
	return stats
}

func formatOptionalTime(t *time.Time) string {
	if t == nil {
		return "-"
	}
	return t.UTC().Format(timeRFC3339)
}

func formatOptionalTimeValue(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Format(timeRFC3339)
}

func selectLCMFrontier(compactions []StoredCompaction, byID map[string]StoredCompaction) []StoredCompaction {
	covered := make(map[string]bool, len(compactions))
	selected := make(map[string]bool, len(compactions))

	var condensed []StoredCompaction
	for _, comp := range compactions {
		if comp.Kind == CompactionKindCondensed {
			condensed = append(condensed, comp)
		}
	}
	sort.SliceStable(condensed, func(i, j int) bool {
		if condensed[i].Depth != condensed[j].Depth {
			return condensed[i].Depth > condensed[j].Depth
		}
		return lessLCMByCoverageTime(condensed[i], condensed[j])
	})

	for _, comp := range condensed {
		if covered[comp.ID] {
			continue
		}
		selected[comp.ID] = true
		markLCMDescendantsCovered(comp, byID, covered)
	}

	frontier := make([]StoredCompaction, 0, len(compactions))
	for _, comp := range compactions {
		if comp.Kind == "" {
			frontier = append(frontier, comp)
			continue
		}
		if selected[comp.ID] {
			frontier = append(frontier, comp)
			continue
		}
		if !covered[comp.ID] && comp.Kind != CompactionKindCondensed {
			frontier = append(frontier, comp)
		}
	}

	sortLCMCompactionsByCoverageTime(frontier)
	return frontier
}

func markLCMDescendantsCovered(comp StoredCompaction, byID map[string]StoredCompaction, covered map[string]bool) {
	for _, childID := range comp.ChildCompactionIDs {
		covered[childID] = true
		child, ok := byID[childID]
		if !ok {
			continue
		}
		markLCMDescendantsCovered(child, byID, covered)
	}
}

// fitLCMFrontierBudget selects blocks from the (oldest-first) frontier that
// fit under maxTokens. The greedy pick iterates newest-first so "balanced"
// intuitively shows recent context under a tight budget. After selection, the
// blocks are re-sorted oldest-first so the rendered prompt reads as a natural
// timeline.
//
// If even the single newest block exceeds the budget, it is still injected
// (oversized first-block guard) so the agent never gets an empty summary
// context when summaries exist.
func fitLCMFrontierBudget(frontier []StoredCompaction, byID map[string]StoredCompaction, maxTokens int) []StoredCompaction {
	if len(frontier) == 0 {
		return nil
	}

	descendantMemo := make(map[string]int, len(byID))
	freshTailTokens := estimateLCMTextTokens("\n\n<fresh_tail>")
	selected := make([]StoredCompaction, 0, len(frontier))
	usedTokens := 0

	for i := len(frontier) - 1; i >= 0; i-- {
		comp := frontier[i]
		block := renderCompactionXML(comp, byID, descendantMemo)
		blockTokens := estimateLCMTextTokens(block)
		separatorTokens := 0
		if len(selected) > 0 {
			separatorTokens = estimateLCMTextTokens("\n\n")
		}
		projected := usedTokens + separatorTokens + blockTokens + freshTailTokens
		if projected > maxTokens && len(selected) > 0 {
			L_debug("lcm: frontier budget reached (recent-first)",
				"usedTokens", usedTokens,
				"nextBlockTokens", blockTokens,
				"maxTokens", maxTokens,
				"injectedBlocks", len(selected))
			break
		}
		selected = append(selected, comp)
		usedTokens += separatorTokens + blockTokens
		if projected > maxTokens {
			L_warn("lcm: injecting oversized newest frontier block to avoid empty summary context",
				"blockID", comp.ID,
				"projectedTokens", projected,
				"maxTokens", maxTokens)
			break
		}
	}

	sortLCMCompactionsByCoverageTime(selected)
	return selected
}

// lessLCMByCoverageTime orders compactions oldest-first by coverage-start, then
// timestamp, then ID as a deterministic tie-break. Shared by the frontier
// selection's same-depth ordering and the render-time sort.
func lessLCMByCoverageTime(a, b StoredCompaction) bool {
	ta := compactionCoverageStart(a)
	tb := compactionCoverageStart(b)
	if !ta.Equal(tb) {
		return ta.Before(tb)
	}
	if !a.Timestamp.Equal(b.Timestamp) {
		return a.Timestamp.Before(b.Timestamp)
	}
	return a.ID < b.ID
}

func sortLCMCompactionsByCoverageTime(compactions []StoredCompaction) {
	sort.SliceStable(compactions, func(i, j int) bool {
		return lessLCMByCoverageTime(compactions[i], compactions[j])
	})
}

func compactionCoverageStart(comp StoredCompaction) time.Time {
	if comp.EarliestMessageAt != nil {
		return comp.EarliestMessageAt.UTC()
	}
	return comp.Timestamp.UTC()
}

func estimateLCMTextTokens(text string) int {
	if text == "" {
		return 0
	}
	return (len(text) + 3) / 4
}

func renderLegacySummaryXML(comp StoredCompaction) string {
	return fmt.Sprintf(
		"<summary id=%q kind=%q depth=%q>\n  <content>\n  %s\n  </content>\n</summary>",
		xmlEscapeAttr(FormatSummaryID(comp.ID)),
		xmlEscapeAttr(string(CompactionKindLeaf)),
		"0",
		indentMultiline(xmlEscapeText(comp.Summary), "  "),
	)
}

func renderCompactionXML(comp StoredCompaction, byID map[string]StoredCompaction, memo map[string]int) string {
	if comp.Kind == "" {
		L_debug("lcm: legacy compaction rendered without DAG fields", "id", comp.ID)
		return renderLegacySummaryXML(comp)
	}
	return renderSummaryXML(comp, byID, memo)
}

func renderSummaryXML(comp StoredCompaction, byID map[string]StoredCompaction, memo map[string]int) string {
	var attrs []string
	attrs = append(attrs, fmt.Sprintf("id=%q", xmlEscapeAttr(FormatSummaryID(comp.ID))))
	attrs = append(attrs, fmt.Sprintf("kind=%q", xmlEscapeAttr(string(comp.Kind))))
	attrs = append(attrs, fmt.Sprintf("depth=%q", xmlEscapeAttr(fmt.Sprintf("%d", comp.Depth))))
	attrs = append(attrs, fmt.Sprintf("descendant_count=%q", xmlEscapeAttr(fmt.Sprintf("%d", descendantCount(comp, byID, memo)))))
	if comp.EarliestMessageAt != nil {
		attrs = append(attrs, fmt.Sprintf("earliest_at=%q", xmlEscapeAttr(comp.EarliestMessageAt.UTC().Format(timeRFC3339))))
	}
	if comp.LatestMessageAt != nil {
		attrs = append(attrs, fmt.Sprintf("latest_at=%q", xmlEscapeAttr(comp.LatestMessageAt.UTC().Format(timeRFC3339))))
	}
	if comp.Kind == CompactionKindLeaf && len(comp.SourceMessageIDs) > 0 {
		msgs := fmt.Sprintf("%s..%s", FormatMessageID(comp.SourceMessageIDs[0]), FormatMessageID(comp.SourceMessageIDs[len(comp.SourceMessageIDs)-1]))
		attrs = append(attrs, fmt.Sprintf("msgs=%q", xmlEscapeAttr(msgs)))
	}

	var b strings.Builder
	b.WriteString("<summary ")
	b.WriteString(strings.Join(attrs, " "))
	b.WriteString(">\n")

	if comp.Kind == CompactionKindCondensed && len(comp.ChildCompactionIDs) > 0 {
		b.WriteString("  <children>\n")
		for _, childID := range comp.ChildCompactionIDs {
			b.WriteString(fmt.Sprintf("    <summary_ref id=%q />\n", xmlEscapeAttr(FormatSummaryID(childID))))
		}
		b.WriteString("  </children>\n")
	}

	b.WriteString("  <content>\n")
	b.WriteString(indentMultiline(xmlEscapeText(comp.Summary), "  "))
	b.WriteString("\n  </content>\n</summary>")
	return b.String()
}

const timeRFC3339 = "2006-01-02T15:04:05Z07:00"

func descendantCount(comp StoredCompaction, byID map[string]StoredCompaction, memo map[string]int) int {
	if v, ok := memo[comp.ID]; ok {
		return v
	}
	if comp.Kind != CompactionKindCondensed {
		count := len(comp.SourceMessageIDs)
		memo[comp.ID] = count
		return count
	}
	total := 0
	for _, childID := range comp.ChildCompactionIDs {
		child, ok := byID[childID]
		if !ok {
			continue
		}
		total += descendantCount(child, byID, memo)
	}
	memo[comp.ID] = total
	return total
}

func xmlEscapeText(s string) string {
	replacer := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
	)
	return replacer.Replace(s)
}

func xmlEscapeAttr(s string) string {
	replacer := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
	)
	return replacer.Replace(s)
}

func indentMultiline(s, indent string) string {
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = indent + lines[i]
	}
	return strings.Join(lines, "\n")
}
