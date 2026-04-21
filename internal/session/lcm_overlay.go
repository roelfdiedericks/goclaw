package session

import (
	"context"
	"fmt"
	"strings"
	"time"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

const compactionSummaryMessageID = "compaction-summary"

func loadCompactionContextFromStore(ctx context.Context, store Store, sessionKey string) (*StoredCompaction, []StoredCompaction, error) {
	if store == nil {
		return nil, nil, nil
	}

	comp, err := store.GetLatestCompaction(ctx, sessionKey)
	if err != nil || comp == nil {
		return comp, nil, err
	}

	compactions := []StoredCompaction{*comp}
	allCompactions, err := store.GetCompactions(ctx, sessionKey)
	if err != nil {
		L_warn("lcm: failed to load all compactions, using latest only", "sessionKey", sessionKey, "error", err)
		return comp, compactions, nil
	}
	if len(allCompactions) > 0 {
		compactions = allCompactions
	}

	return comp, compactions, nil
}

// applyCompactionContextData rewrites sess.Messages to include either a legacy
// summary message (LCM disabled) or an XML summary context block (LCM enabled)
// before the fresh tail. enabled gates the legacy vs LCM path. mode/maxTokens
// are passed through to BuildLCMSummaryContext and are expected to already be
// normalized by NormalizeSessionConfig.
func applyCompactionContextData(sess *Session, comp *StoredCompaction, compactions []StoredCompaction, enabled bool, mode string, maxTokens int) {
	if sess == nil {
		return
	}

	if comp != nil && len(compactions) == 0 {
		compactions = []StoredCompaction{*comp}
	}

	sess.mu.Lock()
	defer sess.mu.Unlock()

	filtered := make([]Message, 0, len(sess.Messages))
	for _, msg := range sess.Messages {
		if msg.ID == compactionSummaryMessageID && msg.Source == "system" {
			continue
		}
		filtered = append(filtered, msg)
	}
	sess.Messages = filtered
	sess.CompactionMaxDepth = 0
	sess.CompactionCondensed = 0
	sess.CompactionDAGStats = nil

	if comp == nil {
		sess.CompactionCount = 0
		return
	}

	if !enabled {
		L_debug("lcm: applying legacy summary message because LCM is disabled")
	}

	if comp.Summary != "" && !enabled {
		summaryMsg := Message{
			ID:        compactionSummaryMessageID,
			Role:      "user",
			Content:   fmt.Sprintf("[Previous context summary]\n%s", comp.Summary),
			Source:    "system",
			Timestamp: comp.Timestamp,
		}
		sess.Messages = append([]Message{summaryMsg}, sess.Messages...)
		L_info("session: prepended compaction summary",
			"summaryLen", len(comp.Summary),
			"totalMessages", len(sess.Messages))
	}

	if enabled {
		contextText, dagStats := BuildLCMSummaryContext(compactions, mode, maxTokens)
		sess.CompactionMaxDepth = dagStats.MaxDepth
		sess.CompactionCondensed = dagStats.Condensed
		sess.CompactionDAGStats = &dagStats
		if contextText != "" {
			summaryMsg := Message{
				ID:        compactionSummaryMessageID,
				Role:      "user",
				Content:   contextText,
				Source:    "system",
				Timestamp: comp.Timestamp,
			}
			sess.Messages = append([]Message{summaryMsg}, sess.Messages...)

			injectedTokens := estimateLCMTextTokens(contextText)
			injectedIDs, earliest, latest := summarizeInjectedBlocks(contextText, compactions)
			L_info("lcm: prepended XML compaction context",
				"availableBlocks", len(compactions),
				"maxDepth", dagStats.MaxDepth,
				"condensed", dagStats.Condensed,
				"summaryInjectionMode", mode,
				"maxInjectedSummaryTokens", maxTokens,
				"totalTokens", injectedTokens,
				"injectedIDs", injectedIDs,
				"earliestAt", earliest,
				"latestAt", latest)
		}
	}

	compID := comp.ID
	sess.LastRecordID = &compID
	sess.CompactionCount = len(compactions)
	sess.UpdatedAt = time.Now()
}

// summarizeInjectedBlocks scans the compactions slice for IDs referenced in
// contextText and returns a terse comma-joined ID list plus the overall
// earliest/latest coverage range. Used for observability only.
func summarizeInjectedBlocks(contextText string, compactions []StoredCompaction) (ids string, earliestAt string, latestAt string) {
	earliest := time.Time{}
	latest := time.Time{}
	idList := make([]string, 0, len(compactions))
	for _, comp := range compactions {
		marker := fmt.Sprintf(`id=%q`, FormatSummaryID(comp.ID))
		if !strings.Contains(contextText, marker) {
			continue
		}
		idList = append(idList, comp.ID)
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
	return strings.Join(idList, ","), formatOptionalTimeValue(earliest), formatOptionalTimeValue(latest)
}

func refreshSessionCompactionContext(ctx context.Context, sess *Session, store Store, sessionKey string, mode string, maxTokens int) error {
	comp, compactions, err := loadCompactionContextFromStore(ctx, store, sessionKey)
	if err != nil {
		L_warn("lcm: falling back to single latest compaction",
			"sessionKey", sessionKey,
			"error", err)
		return err
	}
	// Refresh path is always for enabled LCM; callers gate this on IsLCMEnabled.
	applyCompactionContextData(sess, comp, compactions, true, mode, maxTokens)
	return nil
}
