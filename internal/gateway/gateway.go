package gateway

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/roelfdiedericks/goclaw/internal/acp"
	"github.com/roelfdiedericks/goclaw/internal/commands"
	"github.com/roelfdiedericks/goclaw/internal/config"
	"github.com/roelfdiedericks/goclaw/internal/contentguard"
	gcontext "github.com/roelfdiedericks/goclaw/internal/context"
	"github.com/roelfdiedericks/goclaw/internal/cron"
	"github.com/roelfdiedericks/goclaw/internal/delegatedrun"
	"github.com/roelfdiedericks/goclaw/internal/delivery"
	"github.com/roelfdiedericks/goclaw/internal/embeddings"
	gwtypes "github.com/roelfdiedericks/goclaw/internal/gateway/types"
	"github.com/roelfdiedericks/goclaw/internal/hass"
	"github.com/roelfdiedericks/goclaw/internal/llm"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/media"
	"github.com/roelfdiedericks/goclaw/internal/memory"
	"github.com/roelfdiedericks/goclaw/internal/memorygraph"
	"github.com/roelfdiedericks/goclaw/internal/metrics"
	"github.com/roelfdiedericks/goclaw/internal/paths"
	"github.com/roelfdiedericks/goclaw/internal/sandbox"
	"github.com/roelfdiedericks/goclaw/internal/security"
	"github.com/roelfdiedericks/goclaw/internal/session"
	"github.com/roelfdiedericks/goclaw/internal/skills"
	"github.com/roelfdiedericks/goclaw/internal/stt"
	"github.com/roelfdiedericks/goclaw/internal/tokens"
	"github.com/roelfdiedericks/goclaw/internal/tools"
	"github.com/roelfdiedericks/goclaw/internal/types"
	"github.com/roelfdiedericks/goclaw/internal/user"
)

// Context keys for session info
type contextKey string

const (
	ContextKeyChannel contextKey = "channel"
	ContextKeyChatID  contextKey = "chatID"
)

func acpToolLocations(locations []acp.ToolLocation) []ToolLocation {
	if len(locations) == 0 {
		return nil
	}
	out := make([]ToolLocation, 0, len(locations))
	for _, location := range locations {
		var line *int64
		if location.Line != nil {
			v := *location.Line
			line = &v
		}
		out = append(out, ToolLocation{
			Path: location.Path,
			Line: line,
		})
	}
	return out
}

func renderToolJSON(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return ""
	}
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return asString
	}
	var formatted bytes.Buffer
	if err := json.Indent(&formatted, raw, "", "  "); err == nil {
		return formatted.String()
	}
	return string(raw)
}

func acpToolDisplayResult(contentText string, rawOutput json.RawMessage) string {
	if text := strings.TrimSpace(contentText); text != "" {
		return text
	}
	return strings.TrimSpace(renderToolJSON(rawOutput))
}

func summarizeGatewayTraceText(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	const max = 512
	if len(s) <= max {
		return s
	}
	return s[:max] + "...(truncated)"
}

func mapACPEventToGatewayEvents(runID string, ev acp.ACPEvent) []AgentEvent {
	switch ev.Type {
	case acp.EventTextDelta:
		if payload, ok := ev.Payload.(acp.TextDeltaPayload); ok {
			L_trace("gateway: ACP text delta", "runID", runID, "deltaLen", len(payload.Text), "delta", summarizeGatewayTraceText(payload.Text))
			return []AgentEvent{EventTextDelta{RunID: runID, Delta: payload.Text}}
		}
	case acp.EventThought:
		if payload, ok := ev.Payload.(acp.ThoughtDeltaPayload); ok {
			L_trace("gateway: ACP thought delta", "runID", runID, "deltaLen", len(payload.Text), "delta", summarizeGatewayTraceText(payload.Text))
			return []AgentEvent{EventThinkingDelta{RunID: runID, Delta: payload.Text}}
		}
	case acp.EventToolStart:
		if payload, ok := ev.Payload.(acp.ToolStartPayload); ok {
			event := EventToolStart{
				RunID:     runID,
				ToolName:  payload.Title,
				ToolID:    payload.ToolCallID,
				Status:    payload.Status,
				Input:     payload.Input,
				Content:   payload.Content,
				Meta:      payload.Meta,
				RawOutput: payload.RawOutput,
				Kind:      payload.Kind,
				Locations: acpToolLocations(payload.Locations),
			}
			L_debug("gateway: mapped ACP tool start", "runID", runID, "toolCallID", payload.ToolCallID, "title", payload.Title, "status", payload.Status)
			L_trace("gateway: ACP tool start detail", "runID", runID, "toolCallID", payload.ToolCallID, "inputBytes", len(payload.Input), "contentBytes", len(payload.Content), "rawOutputBytes", len(payload.RawOutput), "locations", len(payload.Locations), "kind", payload.Kind)
			return []AgentEvent{event}
		}
	case acp.EventToolUpdate:
		if payload, ok := ev.Payload.(acp.ToolUpdatePayload); ok {
			displayResult := acpToolDisplayResult(payload.ContentText, payload.RawOutput)
			if payload.IsTerminal {
				event := EventToolEnd{
					RunID:         runID,
					ToolName:      payload.Title,
					ToolID:        payload.ToolCallID,
					Status:        payload.Status,
					Result:        displayResult,
					DisplayResult: displayResult,
					Input:         payload.Input,
					Content:       payload.Content,
					Meta:          payload.Meta,
					RawOutput:     payload.RawOutput,
					Kind:          payload.Kind,
					Locations:     acpToolLocations(payload.Locations),
				}
				if !payload.IsSuccessful {
					event.Error = displayResult
					event.DisplayResult = ""
				}
				L_debug("gateway: mapped ACP tool end", "runID", runID, "toolCallID", payload.ToolCallID, "title", payload.Title, "status", payload.Status, "hasError", event.Error != "")
				L_trace("gateway: ACP tool end detail", "runID", runID, "toolCallID", payload.ToolCallID, "displayLen", len(displayResult), "inputBytes", len(payload.Input), "contentBytes", len(payload.Content), "rawOutputBytes", len(payload.RawOutput), "locations", len(payload.Locations), "kind", payload.Kind)
				return []AgentEvent{event}
			}
			event := EventToolProgress{
				RunID:         runID,
				ToolName:      payload.Title,
				ToolID:        payload.ToolCallID,
				Status:        payload.Status,
				Result:        displayResult,
				DisplayResult: displayResult,
				Content:       payload.Content,
				Meta:          payload.Meta,
				Input:         payload.Input,
				RawOutput:     payload.RawOutput,
				Kind:          payload.Kind,
				Locations:     acpToolLocations(payload.Locations),
			}
			L_debug("gateway: mapped ACP tool progress", "runID", runID, "toolCallID", payload.ToolCallID, "title", payload.Title, "status", payload.Status)
			L_trace("gateway: ACP tool progress detail", "runID", runID, "toolCallID", payload.ToolCallID, "displayLen", len(displayResult), "inputBytes", len(payload.Input), "contentBytes", len(payload.Content), "rawOutputBytes", len(payload.RawOutput), "locations", len(payload.Locations), "kind", payload.Kind)
			return []AgentEvent{event}
		}
	}
	return nil
}

// Channel is the interface for messaging channels (TUI, Telegram, etc.)
type Channel interface {
	Name() string
	Send(ctx context.Context, msg string) error
	SendMirror(ctx context.Context, source, userMsg string) error
	HasUser(u *user.User) bool

	// StreamEvent streams a single agent event to the user (for real-time updates).
	// Returns true if the channel supports streaming and delivered the event.
	// Batch-only channels (Telegram, TUI) should return false.
	StreamEvent(u *user.User, event AgentEvent) bool

	// DeliverAssistantMessage delivers final assistant/user-facing output to the user.
	DeliverAssistantMessage(ctx context.Context, u *user.User, message string) error

	// DeliverSystemMessage delivers non-conversation system/status output to the user.
	DeliverSystemMessage(ctx context.Context, u *user.User, msg delivery.SystemMessage) error

	// DeliverGhostwrite sends a ghostwritten message with appropriate UX
	// (typing indicator, delay, etc.). Used for supervision ghostwriting.
	DeliverGhostwrite(ctx context.Context, u *user.User, message string) error
}

type deliveryReachability interface {
	DeliveryReachable(u *user.User) (bool, string)
}

// Gateway is the central service layer that coordinates the agent loop
type Gateway struct {
	sessions            *session.Manager
	users               *user.Registry
	llm                 llm.Provider  // Primary LLM provider for agent (any provider type)
	registry            *llm.Registry // Unified provider registry
	tools               *tools.Registry
	channels            map[string]Channel
	config              *config.Config
	startTime           time.Time
	checkpointGenerator *session.CheckpointGenerator
	compactor           *session.Compactor
	promptCache         *gcontext.PromptCache
	mediaStore          *media.MediaStore
	memoryManager       *memory.Manager
	memoryGraphManager  *memorygraph.Manager
	commandHandler      *commands.Handler
	skillManager        *skills.Manager
	cronService         *cron.Service
	hassManager         *hass.Manager // Home Assistant event subscription manager
	lastOpenClawUserMsg string        // Track user messages for mirroring
}

// providerStateAccessor implements llm.ProviderStateAccessor using session store.
// It wraps a session key and store to provide stateful providers access to their
// persisted state (e.g., xAI's responseID for context preservation).
type providerStateAccessor struct {
	sessionKey string
	store      session.Store
}

// GetProviderState retrieves state for a provider key.
// providerKey format: "providerName:model" (e.g., "xai:grok-4-1-fast-reasoning")
func (a *providerStateAccessor) GetProviderState(providerKey string) map[string]any {
	state, err := a.store.GetProviderState(context.Background(), a.sessionKey, providerKey)
	if err != nil {
		L_warn("failed to get provider state", "sessionKey", a.sessionKey, "providerKey", providerKey, "error", err)
		return nil
	}
	return state
}

// SetProviderState saves state for a provider key.
// providerKey format: "providerName:model" (e.g., "xai:grok-4-1-fast-reasoning")
func (a *providerStateAccessor) SetProviderState(providerKey string, state map[string]any) {
	if err := a.store.SetProviderState(context.Background(), a.sessionKey, providerKey, state); err != nil {
		L_warn("failed to set provider state", "sessionKey", a.sessionKey, "providerKey", providerKey, "error", err)
	}
}

// New creates a new Gateway instance
func New(cfg *config.Config, users *user.Registry, registry *llm.Registry, toolsReg *tools.Registry) (*Gateway, error) {
	// Get agent provider from registry (supports any provider type)
	agentProvider, err := registry.GetProvider("agent")
	if err != nil {
		return nil, fmt.Errorf("failed to get agent provider: %w", err)
	}

	g := &Gateway{
		users:     users,
		llm:       agentProvider,
		registry:  registry,
		tools:     toolsReg,
		channels:  make(map[string]Channel),
		config:    cfg,
		startTime: time.Now(),
	}

	// Determine store type
	storeType := cfg.Session.Store
	if storeType == "" {
		storeType = "sqlite" // Default
	}

	// Initialize session manager with config
	managerCfg := &session.ManagerConfig{
		StoreType:  storeType,
		StorePath:  cfg.Session.StorePath,
		WorkingDir: cfg.Gateway.WorkingDir,
	}
	if cfg.Session.Inherit {
		managerCfg.SessionsDir = cfg.Session.InheritPath
		managerCfg.InheritFrom = cfg.Session.InheritFrom
	}

	g.sessions, err = session.NewManagerWithConfig(managerCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create session manager: %w", err)
	}
	L_info("session: storage backend ready",
		"store", storeType,
		"path", cfg.Session.StorePath)

	// Load primary session: either from SQLite only, or merged with OpenClaw history
	if cfg.Session.Inherit && cfg.Session.InheritFrom != "" && cfg.Session.InheritPath != "" {
		if err := g.sessions.InheritOpenClawSession(cfg.Session.InheritPath, cfg.Session.InheritFrom); err != nil {
			L_warn("session: failed to inherit OpenClaw session (starting fresh)",
				"inheritFrom", cfg.Session.InheritFrom,
				"error", err)
		} else {
			primary := g.sessions.GetPrimary()
			if primary != nil {
				L_info("session: inherited OpenClaw history",
					"inheritFrom", cfg.Session.InheritFrom,
					"messages", len(primary.Messages),
					"file", primary.SessionFile)
			}
		}
	} else {
		if err := g.sessions.LoadPrimarySession(); err != nil {
			L_warn("session: failed to load primary session", "error", err)
		}
	}

	sumCfg := cfg.Session.Summarization

	// Initialize checkpoint generator
	checkpointCfg := &session.CheckpointGeneratorConfig{
		Enabled:         sumCfg.Checkpoint.Enabled,
		Thresholds:      sumCfg.Checkpoint.Thresholds,
		TurnThreshold:   sumCfg.Checkpoint.TurnThreshold,
		MinTokensForGen: sumCfg.Checkpoint.MinTokensForGen,
	}
	g.checkpointGenerator = session.NewCheckpointGenerator(checkpointCfg)
	L_debug("session: checkpoint generator configured",
		"enabled", sumCfg.Checkpoint.Enabled,
		"thresholds", sumCfg.Checkpoint.Thresholds,
		"turnThreshold", sumCfg.Checkpoint.TurnThreshold)

	// Initialize compaction manager
	compactorCfg := &session.CompactionManagerConfig{
		ReserveTokens:        sumCfg.Compaction.ReserveTokens,
		MaxMessages:          sumCfg.Compaction.MaxMessages,
		PreferCheckpoint:     sumCfg.Compaction.PreferCheckpoint,
		KeepPercent:          sumCfg.Compaction.KeepPercent,
		MinMessages:          sumCfg.Compaction.MinMessages,
		RetryIntervalSeconds: sumCfg.RetryIntervalSeconds,
	}
	g.compactor = session.NewCompactionManager(compactorCfg)
	g.compactor.SetStore(g.sessions.GetStore())
	L_debug("session: compaction manager configured",
		"reserveTokens", sumCfg.Compaction.ReserveTokens,
		"maxMessages", sumCfg.Compaction.MaxMessages,
		"preferCheckpoint", sumCfg.Compaction.PreferCheckpoint,
		"keepPercent", sumCfg.Compaction.KeepPercent,
		"minMessages", sumCfg.Compaction.MinMessages,
		"retryInterval", sumCfg.RetryIntervalSeconds)

	allowList := g.parallelToolAllowlistNames()
	allowSource := "default"
	if len(cfg.Gateway.ToolExecution.ParallelAllowlist) > 0 {
		allowSource = "config"
	}
	L_info("gateway: tool execution config",
		"parallelEnabled", cfg.Gateway.ToolExecution.ParallelEnabled,
		"maxConcurrent", g.parallelToolMaxConcurrent(),
		"allowlistSource", allowSource,
		"allowlist", allowList)

	// Summarization uses llm.GetRegistry() directly - no setup needed here
	L_info("summarization: will use registry for lazy provider resolution")

	// Set MaxTokens on primary session from agent provider and run proactive compaction if needed
	// This MUST happen before any user messages are processed to prevent context overflow
	if primary := g.sessions.GetPrimary(); primary != nil {
		contextTokens := agentProvider.ContextTokens()
		primary.SetMaxTokens(contextTokens)
		L_debug("session: set context window from agent provider",
			"contextTokens", contextTokens,
			"totalTokens", primary.GetTotalTokens())

		// Proactive startup compaction: if estimated tokens exceed 50% of context, compact immediately
		// We use 50% because internal token estimates undercount by ~40% compared to actual API usage
		// (e.g., internal 141k tokens = ~200k actual Anthropic tokens)
		// This prevents the "first message fails" scenario when inheriting large sessions
		proactiveThreshold := (contextTokens * 50) / 100
		currentTokens := primary.GetTotalTokens()
		if currentTokens > proactiveThreshold {
			L_info("session: proactive startup compaction needed",
				"currentTokens", currentTokens,
				"threshold", proactiveThreshold,
				"contextWindow", contextTokens,
				"usage", fmt.Sprintf("%.1f%%", float64(currentTokens)/float64(contextTokens)*100))

			// Keep compacting until we're under both token AND message thresholds
			// This handles the case where 50% compaction isn't enough
			targetTokens := (contextTokens * 40) / 100 // Target 40% of context after compaction
			maxMessages := g.compactor.GetMaxMessages()
			compactionRounds := 0
			maxRounds := 5 // Allow more rounds for very large sessions

			needsCompaction := func() bool {
				if currentTokens > targetTokens {
					return true
				}
				if maxMessages > 0 && len(primary.Messages) > maxMessages {
					return true
				}
				return false
			}

			for needsCompaction() && compactionRounds < maxRounds {
				compactionRounds++
				result, err := g.compactor.Compact(context.Background(), primary, primary.SessionFile)
				if err != nil {
					L_error("session: proactive startup compaction failed",
						"round", compactionRounds,
						"error", err)
					break
				}

				newTokens := primary.GetTotalTokens()
				L_info("session: proactive startup compaction round completed",
					"round", compactionRounds,
					"tokensBefore", currentTokens,
					"tokensAfter", newTokens,
					"messagesAfter", len(primary.Messages),
					"model", result.Model)

				if newTokens >= currentTokens {
					// Compaction didn't reduce tokens - break to avoid infinite loop
					L_warn("session: compaction didn't reduce tokens, stopping", "tokens", newTokens)
					break
				}
				currentTokens = newTokens
			}

			if !needsCompaction() {
				L_info("session: proactive compaction completed",
					"finalTokens", currentTokens,
					"finalMessages", len(primary.Messages),
					"targetTokens", targetTokens,
					"maxMessages", maxMessages,
					"rounds", compactionRounds)
			} else {
				L_warn("session: proactive compaction incomplete",
					"finalTokens", currentTokens,
					"finalMessages", len(primary.Messages),
					"targetTokens", targetTokens,
					"maxMessages", maxMessages,
					"rounds", compactionRounds)
			}
		}
	}

	// Initialize memory manager if enabled
	// Memory manager now calls llm.GetRegistry() directly (cycle broken by types.ToolDefinition)
	if cfg.Memory.Enabled {
		L_info("memory: initializing manager", "workspace", cfg.Gateway.WorkingDir)

		memMgr, err := memory.NewManager(cfg.Memory, cfg.Gateway.WorkingDir)
		if err != nil {
			L_warn("failed to create memory manager", "error", err)
		} else {
			g.memoryManager = memMgr
		}
	}

	// Initialize memory graph manager if enabled
	if cfg.MemoryGraph.Enabled {
		L_info("memorygraph: initializing manager")

		mgraphMgr, err := memorygraph.NewManager(cfg.MemoryGraph)
		if err != nil {
			L_warn("failed to create memory graph manager", "error", err)
		} else {
			g.memoryGraphManager = mgraphMgr
		}
	}

	// Initialize prompt cache
	promptCache, err := gcontext.NewPromptCache(cfg.Gateway.WorkingDir, cfg.PromptCache.PollInterval)
	if err != nil {
		L_warn("failed to create prompt cache", "error", err)
	} else {
		g.promptCache = promptCache
		L_info("promptcache: initialized",
			"workspaceDir", cfg.Gateway.WorkingDir,
			"pollInterval", cfg.PromptCache.PollInterval)
	}

	// Initialize media store
	// Resolve media dir: if not set, default to <workspace>/media
	mediaDir := cfg.Media.Dir
	if mediaDir == "" {
		mediaDir = filepath.Join(cfg.Gateway.WorkingDir, "media")
	} else if !filepath.IsAbs(mediaDir) && !strings.HasPrefix(mediaDir, "~") {
		// Relative path - resolve against workspace
		mediaDir = filepath.Join(cfg.Gateway.WorkingDir, mediaDir)
	}
	mediaStore, err := media.NewMediaStore(media.MediaConfig{
		Dir:        mediaDir,
		MaxSize:    cfg.Media.MaxSize,
		Cleanup:    cfg.Media.Cleanup,
		Quotas:     cfg.Media.Quotas,
		Categories: cfg.Media.Categories,
		TTL:        cfg.Media.TTL,
	})
	if err != nil {
		L_warn("failed to create media store", "error", err)
	} else {
		g.mediaStore = mediaStore
		L_info("media: store initialized",
			"dir", mediaDir,
			"maxSize", cfg.Media.MaxSize,
			"cleanupEnabled", cfg.Media.Cleanup.Enabled,
			"cleanupInterval", cfg.Media.Cleanup.Interval)
	}

	// Initialize STT provider
	if err := stt.ApplyConfig(cfg.STT); err != nil {
		L_warn("stt: failed to initialize", "error", err)
	} else if stt.GetProvider() != nil {
		L_info("stt: provider initialized", "provider", stt.GetProvider().Name())
	}

	// Log memory flush config
	L_debug("session: memory flush configured",
		"enabled", cfg.Session.MemoryFlush.Enabled,
		"thresholds", len(cfg.Session.MemoryFlush.Thresholds),
		"showInSystemPrompt", cfg.Session.MemoryFlush.ShowInSystemPrompt)

	L_info("session management initialized",
		"store", storeType,
		"checkpoints", cfg.Session.Summarization.Checkpoint.Enabled,
		"memoryFlush", cfg.Session.MemoryFlush.Enabled)

	// Initialize command handler
	g.commandHandler = commands.NewHandler(g)
	g.commandHandler.GetManager().SetPanicConfig(
		cfg.Safety.GetPanicPhrases(),
		cfg.Safety.PanicEnabled,
	)
	g.commandHandler.GetManager().SetShutdownConfig(
		cfg.Safety.GetShutdownPhrases(),
		cfg.Safety.ShutdownEnabled,
	)
	L_debug("command handler initialized")

	// Initialize skill manager (skills are config - load early)
	if cfg.Skills.Enabled {
		// Build skill configs map
		skillConfigs := make(map[string]*skills.SkillEntryConfig)
		for name, entry := range cfg.Skills.Entries {
			skillConfigs[name] = &skills.SkillEntryConfig{
				Enabled: entry.Enabled,
			}
		}

		skillMgrCfg := skills.ManagerConfig{
			Enabled:        cfg.Skills.Enabled,
			WorkspaceDir:   cfg.Skills.WorkspaceDir,
			ExtraDirs:      cfg.Skills.ExtraDirs,
			WatchEnabled:   cfg.Skills.Watch,
			WatchDebounce:  cfg.Skills.WatchDebounce,
			SkillConfigs:   skillConfigs,
			SandboxBinDirs: sandbox.GetManager().GetBinSearchDirs(),
		}

		// Set default workspace skills dir if not overridden
		if skillMgrCfg.WorkspaceDir == "" {
			skillMgrCfg.WorkspaceDir = filepath.Join(cfg.Gateway.WorkingDir, "skills")
		}

		skillMgr, err := skills.NewManager(skillMgrCfg)
		if err != nil {
			L_warn("failed to create skill manager", "error", err)
		} else {
			g.skillManager = skillMgr

			// Load skills synchronously (they're config, load them early)
			if err := skillMgr.Load(); err != nil {
				L_warn("failed to load skills", "error", err)
			} else {
				stats := skillMgr.GetStats()
				L_info("skills: loaded",
					"total", stats.TotalSkills,
					"eligible", stats.EligibleSkills,
					"flagged", stats.FlaggedSkills,
					"watchEnabled", cfg.Skills.Watch)
			}

			// Register extraDirs as protected in sandbox manager
			for _, dir := range cfg.Skills.ExtraDirs {
				if err := sandbox.GetManager().RegisterProtectedDir(dir); err != nil {
					L_warn("sandbox: failed to register extraDir as protected", "dir", dir, "error", err)
				}
			}
		}
	} else {
		L_info("skills: disabled by configuration")
	}

	return g, nil
}

// RegisterChannel registers a channel for mirroring
func (g *Gateway) RegisterChannel(ch Channel) {
	g.channels[ch.Name()] = ch
	L_debug("channel registered", "channel", ch.Name())
}

// UnregisterChannel removes a channel from mirroring
func (g *Gateway) UnregisterChannel(name string) {
	delete(g.channels, name)
	L_debug("channel unregistered", "channel", name)
}

// Channels returns all registered channels (for cron delivery)
func (g *Gateway) Channels() map[string]Channel {
	return g.channels
}

// allChannels returns a slice of all registered channels.
// Use this when you need to deliver to ALL channels (including source).
func (g *Gateway) allChannels() []Channel {
	result := make([]Channel, 0, len(g.channels))
	for _, ch := range g.channels {
		result = append(result, ch)
	}
	return result
}

// channelsExcept returns all channels except the one matching the given source name.
// Use this when mirroring/broadcasting to OTHER channels (excluding the originator).
func (g *Gateway) channelsExcept(source string) []Channel {
	result := make([]Channel, 0, len(g.channels))
	for name, ch := range g.channels {
		if name != source {
			result = append(result, ch)
		}
	}
	return result
}

// mirrorUserMessage sends a user message (with source label) to the specified channels.
// This is the primitive for showing "what user said on another channel".
// Channels filter by HasUser internally if needed.
func (g *Gateway) mirrorUserMessage(ctx context.Context, source, userMsg string, u *user.User, channels []Channel) {
	if userMsg == "" {
		return
	}
	for _, ch := range channels {
		if u != nil && !ch.HasUser(u) {
			continue
		}
		L_debug("mirror: sending user message", "from", source, "to", ch.Name())
		if err := ch.SendMirror(ctx, source, userMsg); err != nil {
			L_warn("mirror: failed to send user message", "from", source, "to", ch.Name(), "error", err)
		}
	}
}

// deliverAssistantMessage sends assistant/user-facing output (no source label) to the specified channels.
// This is the primitive for delivering final assistant responses and similar content.
// Channels filter by HasUser internally if needed.
func (g *Gateway) deliverAssistantMessage(ctx context.Context, u *user.User, message string, channels []Channel) {
	if message == "" {
		return
	}
	for _, ch := range channels {
		if u != nil && !ch.HasUser(u) {
			continue
		}
		L_debug("deliver: sending assistant message", "to", ch.Name(), "msgLen", len(message))
		if err := ch.DeliverAssistantMessage(ctx, u, message); err != nil {
			L_warn("deliver: failed to send assistant message", "to", ch.Name(), "error", err)
		}
	}
}

// MirrorUserMessageToOthers mirrors a user message to all channels except the source.
// Public wrapper for voice channel to call when user transcript is finalized.
func (g *Gateway) MirrorUserMessageToOthers(ctx context.Context, source, userMsg string, u *user.User) {
	g.mirrorUserMessage(ctx, source, userMsg, u, g.channelsExcept(source))
}

// DeliverAssistantMessageToOthers delivers assistant output to all channels except the source.
// Public wrapper for voice channel to call when assistant response is complete.
func (g *Gateway) DeliverAssistantMessageToOthers(ctx context.Context, source string, u *user.User, message string) {
	g.deliverAssistantMessage(ctx, u, message, g.channelsExcept(source))
}

// BroadcastConversationTurn persists a conversation turn and distributes to OTHER channels.
// - Persists both user and assistant messages to storage
// - Mirrors user message (with source label) to other channels
// - Delivers agent response (no label) to other channels
// Use this for normal conversation flow where source channel already has the messages.
func (g *Gateway) BroadcastConversationTurn(ctx context.Context, params BroadcastParams) error {
	if params.User == nil {
		return fmt.Errorf("user required for broadcast")
	}

	// Persist the conversation turn
	enrichedAssistant, err := g.PersistConversationTurn(ctx, PersistParams{
		User:             params.User,
		Source:           params.Source,
		UserMessage:      params.UserMessage,
		AssistantMessage: params.AssistantMessage,
	})
	if err != nil {
		return fmt.Errorf("persist failed: %w", err)
	}

	// Get channels excluding the source
	otherChannels := g.channelsExcept(params.Source)

	// Mirror user message to other channels (with source label)
	g.mirrorUserMessage(ctx, params.Source, params.UserMessage, params.User, otherChannels)

	// Deliver assistant response to other channels (no label)
	g.deliverAssistantMessage(ctx, params.User, enrichedAssistant, otherChannels)

	L_debug("broadcast: conversation turn distributed",
		"source", params.Source,
		"userMsgLen", len(params.UserMessage),
		"assistantMsgLen", len(enrichedAssistant),
		"targetChannels", len(otherChannels))

	return nil
}

// DeliverToolMessage persists and delivers a tool-generated message to ALL channels.
// Unlike BroadcastConversationTurn, this includes the source channel in delivery.
// Used by tools like media_display that need their output visible everywhere.
func (g *Gateway) DeliverToolMessage(ctx context.Context, params ToolMessageParams) error {
	if params.User == nil {
		return fmt.Errorf("user required for tool message delivery")
	}

	// Persist as assistant message (no user message for tool output)
	enrichedMsg, err := g.PersistConversationTurn(ctx, PersistParams{
		User:             params.User,
		Source:           params.Source,
		UserMessage:      "",
		AssistantMessage: params.Message,
		SkipUserMessage:  true,
	})
	if err != nil {
		return fmt.Errorf("persist failed: %w", err)
	}

	// Deliver to ALL channels (including source)
	allCh := g.allChannels()
	g.deliverAssistantMessage(ctx, params.User, enrichedMsg, allCh)

	L_debug("deliver: tool message distributed",
		"source", params.Source,
		"msgLen", len(enrichedMsg),
		"targetChannels", len(allCh))

	return nil
}

// DeliverToolMessageToChannel persists a tool-generated message and delivers it
// only to a single named channel. Use this for requester-scoped callbacks.
func (g *Gateway) DeliverToolMessageToChannel(ctx context.Context, params ToolMessageParams, channelName string) error {
	if params.User == nil {
		return fmt.Errorf("user required for tool message delivery")
	}
	channelName = strings.TrimSpace(channelName)
	if channelName == "" {
		return fmt.Errorf("target channel required for scoped tool message delivery")
	}
	message := formatScopedToolMessage(params.Message)

	// Persist as assistant message (no user message for tool output)
	enrichedMsg, err := g.PersistConversationTurn(ctx, PersistParams{
		User:             params.User,
		Source:           params.Source,
		UserMessage:      "",
		AssistantMessage: message,
		SkipUserMessage:  true,
	})
	if err != nil {
		return fmt.Errorf("persist failed: %w", err)
	}

	ch, ok := g.channels[channelName]
	if !ok {
		return fmt.Errorf("target channel not found: %s", channelName)
	}
	if !ch.HasUser(params.User) {
		return fmt.Errorf("target channel %s has no reachable user", channelName)
	}
	if aware, ok := ch.(deliveryReachability); ok {
		if reachable, reason := aware.DeliveryReachable(params.User); !reachable {
			if strings.TrimSpace(reason) == "" {
				reason = delivery.ReasonUnreachable
			}
			return delegatedrun.NewNonRetryableDispatchError(
				delegatedrun.DispatchErrDirectChannelUnreachable,
				delegatedrun.DispatchPathDirect,
				reason,
				nil,
			)
		}
	}

	g.deliverAssistantMessage(ctx, params.User, enrichedMsg, []Channel{ch})
	L_debug("deliver: scoped tool message delivered",
		"source", params.Source,
		"targetChannel", channelName,
		"msgLen", len(enrichedMsg))
	return nil
}

func formatScopedToolMessage(message string) string {
	return strings.TrimSpace(message)
}

// InjectDelegatedReturnToSession appends a synthetic tool_use/tool_result pair
// to the requester session so delegated completion is represented as tool output.
func (g *Gateway) InjectDelegatedReturnToSession(
	ctx context.Context,
	u *user.User,
	source, sessionKey, runID, message, toolError string,
) error {
	sessionKey = strings.TrimSpace(sessionKey)
	runID = strings.TrimSpace(runID)
	if sessionKey == "" {
		return fmt.Errorf("session key is required")
	}
	if runID == "" {
		return fmt.Errorf("run ID is required")
	}
	if g.sessions == nil {
		return fmt.Errorf("sessions manager unavailable")
	}

	toolCallID := fmt.Sprintf("delegated_return:%s", runID)
	toolInput, _ := json.Marshal(map[string]any{
		"action":     "return_to_requester",
		"runId":      runID,
		"source":     strings.TrimSpace(source),
		"session":    sessionKey,
		"injectMode": "tool_result",
		"schema":     "delegated_completion.v1",
	})
	toolResultPayload := buildDelegatedCompletionToolResultPayload(runID, source, sessionKey, message, toolError)

	sess := g.sessions.Get(sessionKey)
	toolUseMsgID := sess.AddToolUse(toolCallID, "subagent_spawn", toolInput, "", "")
	toolResultMsgID := sess.AddToolResult(toolCallID, toolResultPayload, nil, "")

	userID := ""
	if u != nil {
		userID = u.ID
	}
	g.persistMessage(ctx, PersistMessageParams{
		MsgID:      toolUseMsgID,
		SessionKey: sessionKey,
		UserID:     userID,
		Role:       "tool_use",
		Source:     "delegated_return",
		ToolCallID: toolCallID,
		ToolName:   "subagent_spawn",
		ToolInput:  toolInput,
	})
	g.persistMessage(ctx, PersistMessageParams{
		MsgID:      toolResultMsgID,
		SessionKey: sessionKey,
		UserID:     userID,
		Role:       "tool_result",
		Content:    toolResultPayload,
		Source:     "delegated_return",
		ToolCallID: toolCallID,
		ToolError:  strings.TrimSpace(toolError),
	})
	L_info("delegated: return_to_requester injected",
		"runID", runID,
		"sessionKey", sessionKey,
		"toolError", strings.TrimSpace(toolError) != "")
	return nil
}

const delegatedToolResultMaxChars = 12000

func buildDelegatedCompletionToolResultPayload(runID, source, sessionKey, message, toolError string) string {
	raw := strings.TrimSpace(message)
	truncated := false
	if len(raw) > delegatedToolResultMaxChars {
		raw = raw[:delegatedToolResultMaxChars] + "...(truncated)"
		truncated = true
	}
	payload := map[string]any{
		"schema": delegatedrun.CompletionPayloadSchema,
		"kind":   delegatedrun.CompletionPayloadKind,
		"meta": map[string]any{
			"runId":      strings.TrimSpace(runID),
			"source":     strings.TrimSpace(source),
			"session":    strings.TrimSpace(sessionKey),
			"toolError":  strings.TrimSpace(toolError) != "",
			"injectMode": "tool_result",
		},
		"replyInstruction": delegatedrun.DefaultReplyInstruction,
		"untrustedChildOutput": map[string]any{
			"format":    "text/plain",
			"truncated": truncated,
			"text":      raw,
		},
	}
	b, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Sprintf("{\"schema\":%q,\"kind\":%q,\"meta\":{\"runId\":%q},\"untrustedChildOutput\":{\"format\":\"text/plain\",\"truncated\":false,\"text\":%q}}", delegatedrun.CompletionPayloadSchema, delegatedrun.CompletionPayloadKind, strings.TrimSpace(runID), raw)
	}
	return string(b)
}

// MediaStore returns the media store
func (g *Gateway) MediaStore() *media.MediaStore {
	return g.mediaStore
}

// resolveMediaContent resolves FilePath references to base64 Data in message ContentBlocks.
// Returns a new slice with resolved media; original messages are not modified.
// This is called before sending messages to the LLM so media can be injected ephemerally.
func (g *Gateway) resolveMediaContent(messages []types.Message, provider llm.Provider) []types.Message {
	resolved := make([]types.Message, len(messages))
	copy(resolved, messages)

	supportsVision := llm.SupportsVision(provider)
	supportsToolImages := llm.SupportsToolResultImages(provider)
	sttProvider := stt.GetProvider()

	L_debug("resolveMediaContent: starting",
		"messageCount", len(messages),
		"supportsVision", supportsVision,
		"supportsToolImages", supportsToolImages,
		"sttEnabled", sttProvider != nil,
	)

	for i := range resolved {
		msg := &resolved[i]
		if msg.Role == "tool_result" && msg.Content != "" {
			sanitized := contentguard.ToolResultText(msg.Content)
			if sanitized.Changed {
				L_warn("resolveMediaContent: sanitized tool_result content",
					"reason", sanitized.Reason,
					"mime", sanitized.MIME,
					"bytes", sanitized.OriginalBytes)
				msg.Content = sanitized.Text
			}
		}

		// Skip messages without content blocks
		if len(msg.ContentBlocks) == 0 {
			continue
		}

		L_trace("resolveMediaContent: message has content blocks",
			"role", msg.Role,
			"blockCount", len(msg.ContentBlocks),
		)

		// Check if this message type supports images
		canHaveImages := false
		switch msg.Role {
		case "user":
			canHaveImages = supportsVision
		case "tool_result":
			canHaveImages = supportsToolImages
		}

		// Resolve content blocks
		resolvedBlocks := make([]types.ContentBlock, 0, len(msg.ContentBlocks))
		for _, block := range msg.ContentBlocks {
			// Skip image blocks if provider doesn't support them
			if block.Type == "image" && !canHaveImages {
				L_debug("gateway: skipping image block (provider doesn't support)", "role", msg.Role)
				continue
			}

			// Handle audio blocks: transcribe to text if STT is available
			if block.Type == "audio" && block.FilePath != "" {
				resolvedBlock := g.resolveAudioBlock(block, sttProvider)
				resolvedBlocks = append(resolvedBlocks, resolvedBlock)
				continue
			}

			// Opaque file attachments (HTTP multipart, etc.): path + MIME + name as text for the LLM
			if block.Type == "file" && block.FilePath != "" {
				if msg.Role != "user" {
					resolvedBlocks = append(resolvedBlocks, block)
					continue
				}
				rel := block.FilePath
				if g.mediaStore != nil {
					if rp := g.mediaStore.RelativePath(block.FilePath); rp != "" {
						rel = rp
					}
				}
				name := block.FileName
				if name == "" {
					name = filepath.Base(block.FilePath)
				}
				mime := block.MimeType
				if mime == "" {
					mime = "application/octet-stream"
				}
				summary := fmt.Sprintf("[Attached file: `%s` MIME: %s Path: %s]", name, mime, rel)
				resolvedBlocks = append(resolvedBlocks, types.ContentBlock{Type: "text", Text: summary})
				continue
			}

			// Resolve FilePath to Data for image blocks
			if block.Type == "image" && block.FilePath != "" && block.Data == "" {
				data, err := os.ReadFile(block.FilePath)
				if err != nil {
					L_warn("gateway: failed to read media file", "path", block.FilePath, "error", err)
					continue
				}
				block.Data = base64.StdEncoding.EncodeToString(data)

				// Detect MIME type if not set
				if block.MimeType == "" {
					block.MimeType = media.DetectMIME(data)
				}

				L_debug("gateway: resolved image", "path", block.FilePath, "size", len(data))
			}

			resolvedBlocks = append(resolvedBlocks, block)
		}

		if len(resolvedBlocks) != len(msg.ContentBlocks) {
			L_debug("resolveMediaContent: blocks filtered",
				"role", msg.Role,
				"original", len(msg.ContentBlocks),
				"resolved", len(resolvedBlocks),
			)
		}
		msg.ContentBlocks = resolvedBlocks
	}

	return resolved
}

// resolveAudioBlock handles audio content blocks, transcribing if STT is available.
// Returns a text block with JSON containing transcription and metadata.
func (g *Gateway) resolveAudioBlock(block types.ContentBlock, sttProvider stt.Provider) types.ContentBlock {
	// Build audio metadata for the JSON response
	audioMeta := map[string]interface{}{
		"audio":    []string{block.FilePath},
		"duration": block.Duration,
		"source":   block.Source,
	}

	// Attempt transcription if STT provider is available
	if sttProvider != nil {
		L_debug("gateway: transcribing audio", "path", block.FilePath, "provider", sttProvider.Name())

		transcription, err := sttProvider.Transcribe(block.FilePath)
		if err != nil {
			L_warn("gateway: STT transcription failed", "path", block.FilePath, "error", err)
			audioMeta["transcription_error"] = err.Error()
		} else {
			audioMeta["transcription"] = transcription
			L_debug("gateway: transcription complete", "path", block.FilePath, "length", len(transcription))
		}
	} else {
		L_debug("gateway: no STT provider, skipping transcription", "path", block.FilePath)
	}

	// Convert to JSON text block
	jsonBytes, err := json.Marshal(audioMeta)
	if err != nil {
		L_error("gateway: failed to marshal audio metadata", "error", err)
		return types.ContentBlock{
			Type: "text",
			Text: fmt.Sprintf("[Audio file: %s, duration: %ds]", block.FilePath, block.Duration),
		}
	}

	return types.ContentBlock{
		Type: "text",
		Text: string(jsonBytes),
	}
}

// MemoryManager returns the memory manager
func (g *Gateway) MemoryManager() *memory.Manager {
	return g.memoryManager
}

// MemoryGraphManager returns the memory graph manager
func (g *Gateway) MemoryGraphManager() *memorygraph.Manager {
	return g.memoryGraphManager
}

// HassManager returns the Home Assistant event subscription manager
func (g *Gateway) HassManager() *hass.Manager {
	return g.hassManager
}

// SetHassManager sets the Home Assistant event subscription manager
func (g *Gateway) SetHassManager(m *hass.Manager) {
	g.hassManager = m
}

// StartHassManager starts the Home Assistant event subscription manager
func (g *Gateway) StartHassManager(ctx context.Context) error {
	if g.hassManager == nil {
		return fmt.Errorf("Home Assistant manager not configured")
	}
	return g.hassManager.Start(ctx)
}

// AgentIdentity returns the agent identity configuration
func (g *Gateway) AgentIdentity() *AgentIdentityConfig {
	return &g.config.Agent
}

// SupervisionConfig returns the supervision configuration
func (g *Gateway) SupervisionConfig() *SupervisionConfig {
	return &g.config.Supervision
}

// Config returns the full configuration
func (g *Gateway) Config() *config.Config {
	return g.config
}

// SetRegistry sets the LLM provider registry
func (g *Gateway) SetRegistry(r *llm.Registry) {
	g.registry = r
}

// Registry returns the LLM provider registry
func (g *Gateway) Registry() *llm.Registry {
	return g.registry
}

// SkillManager returns the skill manager
func (g *Gateway) SkillManager() *skills.Manager {
	return g.skillManager
}

// GetSkillsStartupWarning returns any security warnings about skills
func (g *Gateway) GetSkillsStartupWarning() string {
	if g.skillManager == nil {
		return ""
	}
	return g.skillManager.GetStartupWarning()
}

// GetSkillsPrompt returns the formatted skills section for system prompt
func (g *Gateway) GetSkillsPrompt() string {
	if g.skillManager == nil {
		return ""
	}
	return g.skillManager.FormatPrompt()
}

// GetSkillsPromptForUser returns the formatted skills section filtered by user's role.
// hasSkillsTool indicates whether the user has access to the skills management tool.
func (g *Gateway) GetSkillsPromptForUser(u *user.User, hasSkillsTool bool) string {
	if g.skillManager == nil {
		return ""
	}
	return g.skillManager.FormatPromptForUser(u, g.users.GetRolesConfig(), hasSkillsTool)
}

// GetSkillsStatusSection returns the skills section for /status output
func (g *Gateway) GetSkillsStatusSection() string {
	if g.skillManager == nil {
		return ""
	}
	return g.skillManager.FormatStatusSection()
}

// GetSkillsListForCommand returns skill info for /skills command
func (g *Gateway) GetSkillsListForCommand() *commands.SkillsListResult {
	if g.skillManager == nil {
		return nil
	}

	allSkills := g.skillManager.GetAllSkills()
	eligibleSkills := g.skillManager.GetEligibleSkills(nil, nil) // No user filtering for stats
	flaggedSkills := g.skillManager.GetFlaggedSkills()

	// Count whitelisted skills (eligible but have audit flags)
	whitelistedCount := 0
	for _, s := range eligibleSkills {
		if s.Whitelisted {
			whitelistedCount++
		}
	}

	// Count ineligible (not eligible, not flagged)
	ineligibleCount := 0
	for _, s := range allSkills {
		if !s.Eligible {
			ineligibleCount++
		}
	}

	result := &commands.SkillsListResult{
		Total:       len(allSkills),
		Eligible:    len(eligibleSkills) - whitelistedCount, // Don't double-count whitelisted
		Ineligible:  ineligibleCount,
		Flagged:     len(flaggedSkills),
		Whitelisted: whitelistedCount,
		Skills:      make([]commands.SkillInfo, 0, len(allSkills)),
	}

	// Create a set of eligible skill names for quick lookup
	eligibleSet := make(map[string]bool)
	for _, s := range eligibleSkills {
		eligibleSet[s.Name] = true
	}

	// Create a set of flagged skill names
	flaggedSet := make(map[string]bool)
	for _, s := range flaggedSkills {
		flaggedSet[s.Name] = true
	}

	for _, s := range allSkills {
		info := commands.SkillInfo{
			Name:        s.Name,
			Description: s.Description,
			Source:      string(s.Source),
		}

		if s.Metadata != nil && s.Metadata.Emoji != "" {
			info.Emoji = s.Metadata.Emoji
		}

		if s.Whitelisted {
			info.Status = "whitelisted"
			if len(s.AuditFlags) > 0 {
				info.Reason = s.AuditFlags[0].Pattern
			}
		} else if !s.Eligible {
			info.Status = "ineligible"
			missing := s.GetMissingRequirements(skills.EligibilityContext{OS: runtime.GOOS})
			if len(missing) > 0 {
				info.Reason = missing[0]
			}
		} else if len(s.AuditFlags) > 0 && !s.Enabled {
			info.Status = "flagged"
			info.Reason = s.AuditFlags[0].Pattern
		} else if s.Eligible && s.Enabled {
			info.Status = "ready"
		} else {
			info.Status = "disabled"
		}

		result.Skills = append(result.Skills, info)
	}

	return result
}

// StartSessionWatcher starts the session file watcher for live OpenClaw sync
func (g *Gateway) StartSessionWatcher(ctx context.Context) error {
	sess := g.sessions.GetPrimary()
	if sess == nil || sess.SessionFile == "" {
		return nil
	}

	L_info("session: starting file watcher for live OpenClaw sync",
		"file", sess.SessionFile,
		"inheritKey", g.config.Session.InheritFrom)

	// Start watching with a callback to handle new records
	return g.sessions.StartWatching(ctx, sess.SessionFile, func(records []session.Record) {
		L_debug("session: received new OpenClaw records", "count", len(records))
		g.mirrorOpenClawRecords(ctx, records)
	})
}

// mirrorOpenClawRecords sends new OpenClaw messages to all channels
func (g *Gateway) mirrorOpenClawRecords(ctx context.Context, records []session.Record) {
	for _, r := range records {
		msgRec, ok := r.(*session.MessageRecord)
		if !ok {
			continue
		}

		// Extract text content from message
		var content string
		for _, c := range msgRec.Message.Content {
			if c.Type == "text" {
				content = c.Text
				break
			}
		}

		L_trace("session: processing OpenClaw record",
			"role", msgRec.Message.Role,
			"hasContent", content != "",
			"contentLen", len(content))

		if content == "" {
			continue
		}

		switch msgRec.Message.Role {
		case "user":
			// Store user message for pairing with next assistant response
			// Strip OpenClaw metadata formatting from user messages
			g.lastOpenClawUserMsg = stripOpenClawMetadata(content)
			L_debug("session: tracked OpenClaw user message", "length", len(g.lastOpenClawUserMsg))

		case "assistant":
			// Mirror assistant response paired with the stored user message
			userMsg := g.lastOpenClawUserMsg
			if userMsg == "" {
				L_debug("session: mirroring assistant without user message (may have been from previous session)")
			}
			g.lastOpenClawUserMsg = "" // Reset after pairing

			// Use primitives to mirror to all channels (nil user = no HasUser filter)
			allCh := g.allChannels()
			g.mirrorUserMessage(ctx, "openclaw", userMsg, nil, allCh)
			g.deliverAssistantMessage(ctx, nil, content, allCh)
		}
	}
}

// stripOpenClawMetadata removes OpenClaw's message metadata from user messages.
// Handles: channel metadata, message IDs, media attachments, and injected instructions.
func stripOpenClawMetadata(msg string) string {
	lines := strings.Split(msg, "\n")
	var result []string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Skip empty lines
		if trimmed == "" {
			continue
		}

		// Skip [message_id: N] lines
		if strings.HasPrefix(trimmed, "[message_id:") {
			continue
		}

		// Skip [media attached: ...] lines
		if strings.HasPrefix(trimmed, "[media attached:") {
			continue
		}

		// Skip OpenClaw's media instruction lines
		if strings.HasPrefix(trimmed, "To send an image back") ||
			strings.Contains(trimmed, "MEDIA:/path") ||
			strings.Contains(trimmed, "media/path/filePath") {
			continue
		}

		// Strip leading metadata bracket from content lines
		// Format: [Telegram Roelf Diedericks id:123456789 +53s 2026-02-02 21:58 GMT+2] actual content
		if idx := strings.Index(line, "] "); idx != -1 && strings.HasPrefix(line, "[") {
			// Check if this looks like metadata (contains "id:" or timestamp-like content)
			prefix := line[:idx]
			if strings.Contains(prefix, "id:") || strings.Contains(prefix, "20") {
				line = strings.TrimSpace(line[idx+2:])
			}
		}

		if line != "" {
			result = append(result, line)
		}
	}

	return strings.Join(result, "\n")
}

// Start begins background tasks (call after New)
// Note: Call StartCron separately AFTER channels are registered
func (g *Gateway) Start(ctx context.Context) {
	L_info("gateway: starting background tasks")

	// Start skill watcher for live reloads (skills already loaded in New)
	if g.skillManager != nil {
		g.skillManager.StartWatcher()
	}

	// Start compaction manager background retry
	if g.compactor != nil {
		g.compactor.Start(ctx)
	}

	// Check embeddings model mismatch and auto-rebuild if configured
	g.checkEmbeddingsMismatch(ctx)

	// NOTE: Cron is NOT started here - call StartCron() after channels are registered
}

// checkEmbeddingsMismatch checks if any chunks use non-primary embedding models
// and optionally triggers auto-rebuild based on config.
func (g *Gateway) checkEmbeddingsMismatch(ctx context.Context) {
	cfg := g.config.LLM.Embeddings
	if len(cfg.Models) == 0 {
		return // Embeddings not configured
	}

	sessionsDB := g.SessionDB()
	if sessionsDB == nil {
		return // No sessions DB
	}

	var memoryDB *sql.DB
	if g.memoryManager != nil {
		memoryDB = g.memoryManager.DB()
	}

	// Get status
	status, err := embeddings.GetStatus(sessionsDB, memoryDB, cfg)
	if err != nil {
		L_warn("embeddings: failed to get status", "error", err)
		return
	}

	// Check if any chunks need rebuild
	needsRebuild := status.Transcript.NeedsRebuildCount + status.Memory.NeedsRebuildCount
	if needsRebuild == 0 {
		L_info("embeddings: all chunks using primary model", "model", status.PrimaryModel)
		return
	}

	// Log mismatch details
	L_info("embeddings: model mismatch detected",
		"primary", status.PrimaryModel,
		"transcriptNeedsRebuild", status.Transcript.NeedsRebuildCount,
		"memoryNeedsRebuild", status.Memory.NeedsRebuildCount,
		"total", needsRebuild)

	// Check auto-rebuild setting
	if !cfg.AutoRebuild {
		L_warn("embeddings: auto-rebuild disabled, run '/embeddings rebuild' or 'goclaw embeddings rebuild' for consistency")
		return
	}

	// Start background rebuild
	L_info("embeddings: starting auto-rebuild", "autoRebuild", true)
	go g.runEmbeddingsRebuild(ctx, sessionsDB, memoryDB)
}

// runEmbeddingsRebuild runs the embeddings rebuild in background
func (g *Gateway) runEmbeddingsRebuild(ctx context.Context, sessionsDB, memoryDB *sql.DB) {
	cfg := g.config.LLM.Embeddings
	batchSize := 50 // Default batch size

	// Progress callback - log only (auto-rebuild has no user to notify)
	onProgress := func(processed, total int, err error, done bool) {
		if !done {
			return
		}

		if err != nil {
			L_error("embeddings: auto-rebuild failed", "error", err, "processed", processed, "total", total)
		} else {
			L_info("embeddings: auto-rebuild completed", "processed", processed)
		}
	}

	err := embeddings.Rebuild(ctx, sessionsDB, memoryDB, cfg, g.registry, batchSize, false, onProgress)
	if err != nil {
		L_error("embeddings: rebuild failed", "error", err)
	}
}

// StartCron initializes and starts the cron scheduler.
func (g *Gateway) StartCron(ctx context.Context) error {
	if g.cronService != nil && g.cronService.IsRunning() {
		return fmt.Errorf("cron service already running")
	}

	// Cron state is always GoClaw-owned.
	// If no GoClaw cron store exists yet, the store loader may bootstrap from
	// OpenClaw's jobs.json once, then continue using ~/.goclaw/cron/ thereafter.
	cronJobsPath, _ := paths.DataPath("cron/jobs.json")
	cronRunsDir, _ := paths.DataPath("cron/runs")

	// Create store with resolved paths
	store := cron.NewStore(cronJobsPath, cronRunsDir)

	// Create and start service
	g.cronService = cron.NewService(store, g)
	delegatedRegistryPath := filepath.Join(filepath.Dir(g.config.Session.StorePath), "delegated_runs.db")
	g.cronService.SetDelegatedRunsEnabled(g.config.Gateway.DelegatedRuns.Enabled, delegatedRegistryPath, delegatedrun.SpawnLimits{
		MaxSpawnDepth:              g.config.Gateway.DelegatedRuns.MaxSpawnDepth,
		MaxActiveChildrenPerParent: g.config.Gateway.DelegatedRuns.MaxActiveChildrenPerParent,
		MaxConcurrentRuns:          g.config.Gateway.DelegatedRuns.MaxConcurrentRuns,
		DefaultTimeoutSeconds:      g.config.Gateway.DelegatedRuns.DefaultTimeoutSeconds,
		MaxTimeoutSeconds:          g.config.Gateway.DelegatedRuns.MaxTimeoutSeconds,
	})

	// Set job timeout if configured
	if g.config.Cron.JobTimeoutMinutes > 0 {
		g.cronService.SetJobTimeout(g.config.Cron.JobTimeoutMinutes)
	}

	// Set up heartbeat config if enabled
	if g.config.Cron.Heartbeat.Enabled {
		heartbeatCfg := &cron.HeartbeatState{
			Enabled:         g.config.Cron.Heartbeat.Enabled,
			IntervalMinutes: g.config.Cron.Heartbeat.IntervalMinutes,
			Prompt:          g.config.Cron.Heartbeat.Prompt,
			WorkspaceDir:    g.config.Gateway.WorkingDir, // Workspace for checking HEARTBEAT.md
		}
		g.cronService.SetHeartbeatConfig(heartbeatCfg)
	}

	if err := g.cronService.Start(ctx); err != nil {
		g.cronService = nil
		return err
	}

	return nil
}

// StopCron stops the cron scheduler.
func (g *Gateway) StopCron() {
	if g.cronService != nil {
		g.cronService.Stop()
		g.cronService = nil
	}
}

// CronService returns the cron service (may be nil if not started).
func (g *Gateway) CronService() *cron.Service {
	return g.cronService
}

// ListDelegatedRuns returns delegated run records from cron's delegated runner registry.
func (g *Gateway) ListDelegatedRuns() []delegatedrun.RunRecord {
	if g.cronService == nil {
		return nil
	}
	return g.cronService.ListDelegatedRuns()
}

// GetDelegatedRun returns a delegated run by ID.
func (g *Gateway) GetDelegatedRun(runID string) (delegatedrun.RunRecord, bool) {
	if g.cronService == nil {
		return delegatedrun.RunRecord{}, false
	}
	return g.cronService.GetDelegatedRun(runID)
}

// CancelDelegatedRun cancels a delegated run by ID.
func (g *Gateway) CancelDelegatedRun(runID string) error {
	if g.cronService == nil {
		return fmt.Errorf("cron service unavailable")
	}
	return g.cronService.CancelDelegatedRun(runID)
}

// ListDelegatedRunEvents returns delegated run events after sinceID.
func (g *Gateway) ListDelegatedRunEvents(sinceID int64, limit int) []delegatedrun.RunEvent {
	if g.cronService == nil {
		return nil
	}
	return g.cronService.ListDelegatedRunEvents(sinceID, limit)
}

// RunAgentForCron implements the cron.GatewayRunner interface.
// It converts between cron and gateway types and runs the agent.
func (g *Gateway) RunAgentForCron(ctx context.Context, cronReq cron.AgentRequest, cronEvents chan<- cron.AgentEvent) {
	// Look up the user
	var reqUser *user.User
	if cronReq.UserID != "" {
		reqUser = g.users.Get(cronReq.UserID)
	}
	if reqUser == nil {
		cronEvents <- cron.AgentErrorEvent{Error: "no authenticated user"}
		close(cronEvents)
		return
	}

	// If thinking enabled, send status message to channels
	if reqUser.Thinking && len(g.channels) > 0 {
		jobDesc := cronReq.JobName
		if jobDesc == "" {
			jobDesc = cronReq.Source
		}
		statusMsg := fmt.Sprintf("💭 Running cron: %s...", jobDesc)
		_ = g.DeliverSystemMessage(ctx, reqUser.ID, delivery.SystemMessage{
			Kind:    delivery.SystemKindStatus,
			Source:  "cron-status",
			Title:   "Cron Status",
			Content: statusMsg,
		})
	}

	// Convert cron request to gateway request
	req := AgentRequest{
		Source:         cronReq.Source,
		Purpose:        cronReq.Purpose,
		UserMsg:        cronReq.UserMsg,
		SessionID:      cronReq.SessionID,
		FreshContext:   cronReq.FreshContext,
		User:           reqUser,
		IsHeartbeat:    cronReq.IsHeartbeat,
		Ephemeral:      cronReq.Ephemeral,
		EnableThinking: cronReq.EnableThinking || reqUser.Thinking, // Use cron setting or user preference
		SkipMirror:     cronReq.SkipMirror,
	}

	// Create internal events channel
	events := make(chan AgentEvent, 100)

	// Run the agent in a goroutine
	go g.RunAgent(ctx, req, events) //nolint:errcheck // fire-and-forget goroutine

	// Forward events, converting types
	for event := range events {
		switch e := event.(type) {
		case EventAgentEnd:
			cronEvents <- cron.AgentEndEvent{FinalText: e.FinalText}
		case EventAgentError:
			cronEvents <- cron.AgentErrorEvent{Error: e.Error}
		}
	}

	// Close the cron events channel when done
	close(cronEvents)
}

func (g *Gateway) resolveDeliveryUser(userID string) (*user.User, string) {
	if strings.TrimSpace(userID) == "" {
		return nil, delivery.ReasonNoUser
	}
	u := g.users.Get(userID)
	if u == nil {
		return nil, delivery.ReasonNoUser
	}
	return u, ""
}

func (g *Gateway) deliverToUserChannels(
	ctx context.Context,
	u *user.User,
	exclude map[string]struct{},
	deliver func(Channel) error,
) delivery.Report {
	report := delivery.Report{Generated: true}

	for name, ch := range g.channels {
		result := delivery.Result{Channel: name}
		if _, skip := exclude[name]; skip {
			result.Reason = delivery.ReasonExcluded
			report.Results = append(report.Results, result)
			continue
		}
		if !ch.HasUser(u) {
			result.Reason = delivery.ReasonHasNoUser
			report.Results = append(report.Results, result)
			continue
		}
		if aware, ok := ch.(deliveryReachability); ok {
			reachable, reason := aware.DeliveryReachable(u)
			if !reachable {
				result.Reason = reason
				if result.Reason == "" {
					result.Reason = delivery.ReasonUnreachable
				}
				report.Results = append(report.Results, result)
				continue
			}
		}
		result.Attempted = true
		if err := deliver(ch); err != nil {
			result.Error = err.Error()
			result.Reason = delivery.ReasonError
			L_error("gateway: delivery failed", "channel", name, "user", u.ID, "error", err)
		} else {
			result.Delivered = true
			report.DeliveredTo++
			L_debug("gateway: delivery succeeded", "channel", name, "user", u.ID)
		}
		report.Results = append(report.Results, result)
	}
	return report
}

func (g *Gateway) deliverAssistantToUser(
	ctx context.Context,
	userID string,
	msg delivery.AssistantMessage,
) delivery.Report {
	report := delivery.Report{Generated: strings.TrimSpace(msg.Content) != ""}
	if !report.Generated {
		return report
	}

	u, reason := g.resolveDeliveryUser(userID)
	if u == nil {
		report.Results = append(report.Results, delivery.Result{Reason: reason})
		return report
	}

	exclude := make(map[string]struct{}, len(msg.ExcludeChannels))
	for _, name := range msg.ExcludeChannels {
		if trimmed := strings.TrimSpace(name); trimmed != "" {
			exclude[trimmed] = struct{}{}
		}
	}

	report = g.deliverToUserChannels(ctx, u, exclude, func(ch Channel) error {
		return ch.DeliverAssistantMessage(ctx, u, msg.Content)
	})
	if msg.Persist && report.Delivered() {
		kind := msg.PersistKind
		if strings.TrimSpace(kind) == "" {
			kind = "delivered"
		}
		persistContent := msg.PersistContent
		if strings.TrimSpace(persistContent) == "" {
			persistContent = msg.Content
		}
		if err := g.PersistDeliveredMessage(ctx, persistContent, kind); err != nil {
			L_warn("gateway: failed to persist delivered assistant output", "source", msg.Source, "error", err)
		} else {
			report.Persisted = true
		}
	}
	return report
}

// DeliverAssistantOutput delivers final assistant/user-facing content to the user's
// channels using the assistant surface, and optionally persists the raw content.
func (g *Gateway) DeliverAssistantOutput(ctx context.Context, userID string, msg delivery.AssistantMessage) delivery.Report {
	return g.deliverAssistantToUser(ctx, userID, msg)
}

// DeliverSystemMessage delivers system/status output to the user's channels using
// the dedicated system surface, and optionally persists a separate raw content payload.
func (g *Gateway) DeliverSystemMessage(ctx context.Context, userID string, msg delivery.SystemMessage) delivery.Report {
	report := delivery.Report{Generated: strings.TrimSpace(msg.Content) != ""}
	if !report.Generated {
		return report
	}

	u, reason := g.resolveDeliveryUser(userID)
	if u == nil {
		report.Results = append(report.Results, delivery.Result{Reason: reason})
		return report
	}

	report = g.deliverToUserChannels(ctx, u, nil, func(ch Channel) error {
		return ch.DeliverSystemMessage(ctx, u, msg)
	})
	if msg.Persist && report.Delivered() {
		kind := msg.PersistKind
		if strings.TrimSpace(kind) == "" {
			kind = "system"
		}
		if err := g.PersistDeliveredMessage(ctx, msg.ContentForPersistence(), kind); err != nil {
			L_warn("gateway: failed to persist delivered system message", "source", msg.Source, "error", err)
		} else {
			report.Persisted = true
		}
	}
	return report
}

func convertDeliveryResults(results []delivery.Result) []types.DeliveryResult {
	out := make([]types.DeliveryResult, 0, len(results))
	for _, result := range results {
		errText := result.Error
		if errText == "" && !result.Delivered && result.Reason != "" {
			errText = result.Reason
		}
		out = append(out, types.DeliveryResult{
			Channel: result.Channel,
			Success: result.Delivered,
			Error:   errText,
		})
	}
	return out
}

// GetOwnerUserID returns the owner user ID for cron jobs.
func (g *Gateway) GetOwnerUserID() string {
	owner := g.users.Owner()
	if owner == nil {
		return ""
	}
	return owner.ID
}

// InjectSystemEvent implements cron.GatewayRunner interface.
// It injects a system event message into the primary session (no agent run).
func (g *Gateway) InjectSystemEvent(ctx context.Context, text string) error {
	msg := types.NewInboundMessage("system", nil, text).WithoutRunAgent()
	_, err := g.ProcessMessage(ctx, msg, nil)
	return err
}

// HandoffCronResult implements cron.GatewayRunner interface.
// It forwards a completed cron task result into the main agent flow.
func (g *Gateway) HandoffCronResult(ctx context.Context, jobName, result string) error {
	jobName = strings.TrimSpace(jobName)
	result = strings.TrimSpace(result)
	if result == "" {
		return nil
	}

	prompt := buildCronHandoffPrompt(jobName, result)
	return g.InvokeAgent(ctx, "cron_handoff", "agent", prompt, canonicalSilentToken)
}

func buildCronHandoffPrompt(jobName, result string) string {
	header := "A scheduled cron task completed and produced the result below."
	if jobName != "" {
		header = fmt.Sprintf("%s\nJob: %s", header, jobName)
	}

	return header + "\n\n" +
		"This result has NOT been delivered to the user yet.\n" +
		"Decide what to do next:\n" +
		"1. Send the user a concise useful message if the result is user-relevant.\n" +
		"2. Take any necessary follow-up tool actions if action is warranted.\n" +
		"3. Update files or memory if that is the useful outcome.\n" +
		"4. Reply exactly SILENT_OK only if no user-facing message, tool action, or file/memory update is warranted.\n\n" +
		"Result:\n" + result
}

// PersistDeliveredMessage saves a delivered message to the primary session for transcript indexing.
// Used by cron and heartbeat to persist messages that were sent to channels.
func (g *Gateway) PersistDeliveredMessage(ctx context.Context, content, source string) error {
	store := g.sessions.GetStore()
	if store == nil {
		return nil
	}

	msg := &session.StoredMessage{
		ID:         session.GenerateMessageID(),
		SessionKey: session.PrimarySession,
		Timestamp:  time.Now(),
		Role:       "assistant",
		Content:    content,
		Source:     source,
		UserID:     g.GetOwnerUserID(),
	}

	if err := store.AppendMessage(ctx, session.PrimarySession, msg); err != nil {
		L_warn("gateway: failed to persist delivered message", "error", err)
		return err
	}

	L_debug("gateway: persisted delivered message", "source", source, "contentLen", len(content))
	return nil
}

// ProcessMessage is the unified entry point for agent processing.
//
// Parameters:
//   - msg: The inbound message to process
//   - events: Optional event channel for streaming (nil = batch mode)
//
// Behavior:
//   - ALWAYS blocks until agent completes
//   - ALWAYS returns DeliveryReport with FinalText
//   - If events is nil: batch mode - ProcessMessage delivers to channels
//   - If events is non-nil: streaming mode - caller handles delivery via events
//
// Text handling:
//   - Text non-empty: add to session, then process
//   - Text empty + RunAgent=true: process existing session (supervision case)
//   - Text empty + RunAgent=false: nothing to do
func (g *Gateway) ProcessMessage(ctx context.Context, msg *types.InboundMessage, events chan<- AgentEvent) (*types.DeliveryReport, error) {
	// Validate AgentID (only "main" or empty supported for now)
	if msg.AgentID != "" && msg.AgentID != "main" {
		return nil, fmt.Errorf("unsupported agent ID: %s (only 'main' supported)", msg.AgentID)
	}

	// Resolve session key
	sessionKey := msg.SessionKey
	if sessionKey == "" {
		if msg.User != nil {
			sessionKey = "user:" + msg.User.ID
		} else {
			sessionKey = session.PrimarySession
		}
	}

	// Get session (creates if doesn't exist)
	sess := g.sessions.Get(sessionKey)
	if sess == nil {
		return nil, fmt.Errorf("failed to get session: %s", sessionKey)
	}

	// Send status message if set
	if msg.StatusMessage != "" {
		g.SendStatusMessage(ctx, msg.User, msg.StatusMessage)
	}

	// If RunAgent == false, just inject to context (no agent run)
	if !msg.RunAgent {
		if msg.Text != "" {
			sess.AddSystemMessage(msg.Text)
			L_info("gateway: ProcessMessage injected system message", "session", sessionKey, "textLen", len(msg.Text))
		}
		return &types.DeliveryReport{SessionKey: sessionKey}, nil
	}

	// RunAgent == true: run the agent
	if msg.User == nil {
		return nil, fmt.Errorf("user required for agent run")
	}

	// Add message to session if Text is non-empty
	addedMessage := false
	if msg.Text != "" {
		msgID := sess.AddUserMessage(msg.Text, msg.Source)
		addedMessage = true
		L_debug("gateway: ProcessMessage added user message", "session", sessionKey, "source", msg.Source, "textLen", len(msg.Text))

		// Persist the message
		g.persistMessage(ctx, PersistMessageParams{
			MsgID:      msgID,
			SessionKey: sessionKey,
			UserID:     msg.User.ID,
			Role:       "user",
			Content:    msg.Text,
			Source:     msg.Source,
		})
	}

	// Build AgentRequest
	req := AgentRequest{
		User:           msg.User,
		Source:         msg.Source,
		Purpose:        msg.Purpose,
		UserMsg:        msg.Text,
		SessionID:      sessionKey,
		FreshContext:   msg.FreshContext,
		IsHeartbeat:    msg.Ephemeral,
		SkipAddMessage: addedMessage, // We already added it above
		EnableThinking: msg.EnableThinking,
		SkipMirror:     msg.SkipMirror,
	}

	// Add content blocks if present
	if len(msg.ContentBlocks) > 0 {
		req.ContentBlocks = msg.ContentBlocks
	}

	var finalText string
	var runID string

	if events != nil {
		// STREAMING MODE: caller handles delivery
		// Run agent with caller's events channel
		internalEvents := make(chan AgentEvent, 100)

		// Forward events to caller and collect finalText
		go func() {
			for event := range internalEvents {
				// Forward to caller
				events <- event
				// Collect final text
				if e, ok := event.(EventAgentStart); ok {
					runID = e.RunID
				}
				if e, ok := event.(EventAgentEnd); ok {
					finalText = e.FinalText
				}
			}
		}()

		// Run agent (blocking)
		err := g.RunAgent(ctx, req, internalEvents)
		if err != nil {
			return nil, fmt.Errorf("agent run failed: %w", err)
		}

		// Return report without delivery (caller handles it)
		return &types.DeliveryReport{
			SessionKey: sessionKey,
			RunID:      runID,
			FinalText:  finalText,
		}, nil

	} else {
		// BATCH MODE: we handle delivery
		internalEvents := make(chan AgentEvent, 100)
		done := make(chan struct{})

		go func() {
			defer close(done)
			for event := range internalEvents {
				if e, ok := event.(EventAgentStart); ok {
					runID = e.RunID
				}
				if e, ok := event.(EventAgentEnd); ok {
					finalText = e.FinalText
				}
			}
		}()

		// Run agent (blocking)
		err := g.RunAgent(ctx, req, internalEvents)
		if err != nil {
			return nil, fmt.Errorf("agent run failed: %w", err)
		}

		<-done

		// Check suppression
		suppressed := false

		// Custom suppression first (caller-specified)
		if msg.SuppressDeliveryOn != "" {
			if strings.Contains(strings.ToUpper(finalText), strings.ToUpper(msg.SuppressDeliveryOn)) {
				suppressed = true
				L_debug("gateway: custom suppression matched", "match", msg.SuppressDeliveryOn)
			}
		}

		// Then canonical/alias silent-token suppression.
		if !suppressed && shouldSuppressResponse(finalText) {
			suppressed = true
			L_debug("gateway: central suppression matched")
		}

		// Build delivery report
		report := &types.DeliveryReport{
			SessionKey: sessionKey,
			RunID:      runID,
			FinalText:  finalText,
			Suppressed: suppressed,
		}

		// Deliver if not suppressed
		if !suppressed && finalText != "" {
			deliveryReport := g.DeliverAssistantOutput(ctx, msg.User.ID, delivery.AssistantMessage{
				Source:         msg.Source,
				Content:        finalText,
				Persist:        false,
				PersistKind:    "conversation",
				PersistContent: finalText,
			})
			report.Results = append(report.Results, convertDeliveryResults(deliveryReport.Results)...)
		}

		return report, nil
	}
}

// InvokeAgent implements types.EventInjector interface.
// It runs the agent with a message and delivers the response to channels.
// Uses owner user and primary session.
func (g *Gateway) InvokeAgent(ctx context.Context, source, purpose, message, suppressOn string) error {
	u := g.users.Owner()
	if u == nil {
		return fmt.Errorf("no owner user configured")
	}

	msg := types.NewInboundMessage(source, u, message)
	msg.Purpose = purpose
	msg.SkipMirror = true // We handle delivery ourselves

	if suppressOn != "" {
		msg.WithSuppressDeliveryOn(suppressOn)
	}

	// HASS debug status (backward compat)
	if strings.HasPrefix(source, "hass:") && g.hassManager != nil && g.hassManager.IsDebug() {
		msg.StatusMessage = fmt.Sprintf("💭 Running %s...", source)
	}

	_, err := g.ProcessMessage(ctx, msg, nil) // Batch mode
	return err
}

// Shutdown gracefully shuts down the gateway
func (g *Gateway) Shutdown() {
	L_info("gateway: shutting down")

	// Stop Home Assistant manager
	if g.hassManager != nil {
		g.hassManager.Stop()
	}

	// Stop cron service
	g.StopCron()

	// Stop skill manager
	if g.skillManager != nil {
		g.skillManager.Stop() //nolint:errcheck // shutdown cleanup
	}

	// Stop compaction manager background tasks
	if g.compactor != nil {
		g.compactor.Stop()
	}

	if g.promptCache != nil {
		g.promptCache.Close()
	}

	if g.mediaStore != nil {
		g.mediaStore.Close()
	}

	// Close STT provider
	stt.Close()

	if g.memoryManager != nil {
		g.memoryManager.Close() //nolint:errcheck // shutdown cleanup
	}

	if g.memoryGraphManager != nil {
		g.memoryGraphManager.Close() //nolint:errcheck // shutdown cleanup
	}

	if g.sessions != nil {
		g.sessions.Close() //nolint:errcheck // shutdown cleanup
	}
}

const canonicalSilentToken = "SILENT_OK"

var suppressionAliases = map[string]string{
	"SILENT_OK":    canonicalSilentToken,
	"HEARTBEAT_OK": canonicalSilentToken,
	"EVENT_OK":     canonicalSilentToken,
	"NO_REPLY":     canonicalSilentToken,
}

func normalizeSuppressionToken(response string) (string, bool) {
	token := strings.ToUpper(strings.TrimSpace(response))
	canonical, ok := suppressionAliases[token]
	return canonical, ok
}

func shouldSuppressResponse(response string) bool {
	_, ok := normalizeSuppressionToken(response)
	return ok
}

// filterToolsForUser returns tool definitions filtered by the user's role permissions.
// Tools not allowed by the role are excluded from the list (never shown to LLM).
func (g *Gateway) filterToolsForUser(u *user.User) []tools.ToolDefinition {
	allDefs := g.tools.Definitions()

	// No user = no filtering (shouldn't happen, but be safe)
	if u == nil {
		return allDefs
	}

	// Resolve user's role
	resolvedRole, err := g.users.ResolveUserRole(u)
	if err != nil {
		L_error("filterToolsForUser: failed to resolve role, returning all tools", "user", u.Name, "role", u.Role, "error", err)
		return allDefs
	}

	// AllTools = no filtering needed
	if resolvedRole.AllTools {
		return allDefs
	}

	// Filter to only allowed tools
	filtered := make([]tools.ToolDefinition, 0, len(resolvedRole.Tools))
	for _, def := range allDefs {
		if resolvedRole.CanUseTool(def.Name) {
			// Additional filter: if memory=none, exclude memory tools
			if resolvedRole.Memory == "none" && isMemoryTool(def.Name) {
				L_debug("filterToolsForUser: excluding memory tool", "user", u.Name, "tool", def.Name)
				continue
			}
			// If transcripts=none, exclude transcript tool
			if resolvedRole.Transcripts == "none" && def.Name == "transcript" {
				L_debug("filterToolsForUser: excluding transcript tool", "user", u.Name)
				continue
			}
			filtered = append(filtered, def)
		}
	}

	L_debug("filterToolsForUser: filtered tools", "user", u.Name, "role", resolvedRole.Name, "total", len(allDefs), "allowed", len(filtered))
	return filtered
}

// isMemoryTool returns true if the tool is a memory-related tool
func isMemoryTool(name string) bool {
	return name == "memory_search" || name == "memory_get"
}

// GetToolDefinitionsForUser returns tool definitions filtered by the user's role permissions.
// This is the public API for external callers (like voice channel) to get available tools.
func (g *Gateway) GetToolDefinitionsForUser(u *user.User) []types.ToolDefinition {
	return g.filterToolsForUser(u)
}

// ToolExecutionParams contains parameters for executing a tool outside the main agent loop.
type ToolExecutionParams struct {
	Name   string     // Tool name
	Input  string     // JSON input string
	User   *user.User // User context
	Source string     // Channel source (e.g., "http_voice")
	ChatID string     // Chat ID (optional)
}

// ExecuteTool executes a tool with the given parameters and returns the result.
// This is a simplified tool execution for external callers (like voice channel).
// It sets up proper session context but doesn't do event broadcasting or security wrapping.
func (g *Gateway) ExecuteTool(ctx context.Context, params ToolExecutionParams) (*types.ToolResult, error) {
	if params.Name == "" {
		return nil, fmt.Errorf("tool name required")
	}

	// Get owner chat ID for fallback (used by telegram in cron/heartbeat)
	ownerChatID := ""
	if owner := g.users.Owner(); owner != nil {
		ownerChatID = owner.TelegramID
	}

	// Resolve transcript scope for user
	transcriptScope := "none"
	if params.User != nil {
		if resolvedRole, err := g.users.ResolveUserRole(params.User); err == nil {
			transcriptScope = resolvedRole.Transcripts
		}
	}

	// Build session context
	reserveTokens := 0
	if g.compactor != nil {
		reserveTokens = g.compactor.GetReserveTokens()
	}
	toolCtx := tools.WithSessionContext(ctx, &tools.SessionContext{
		Channel:         params.Source,
		ChatID:          params.ChatID,
		OwnerChatID:     ownerChatID,
		SessionKey:      "",
		RunID:           "",
		ReserveTokens:   reserveTokens,
		User:            params.User,
		TranscriptScope: transcriptScope,
	})

	// Execute tool - convert string input to json.RawMessage
	result, err := g.tools.Execute(toolCtx, params.Name, json.RawMessage(params.Input))
	if err != nil {
		userName := ""
		if params.User != nil {
			userName = params.User.Name
		}
		L_warn("ExecuteTool: failed", "tool", params.Name, "error", err, "user", userName)
		return types.ErrorResult(err.Error()), err
	}

	userName := ""
	if params.User != nil {
		userName = params.User.Name
	}
	L_debug("ExecuteTool: success", "tool", params.Name, "user", userName, "source", params.Source)
	return result, nil
}

// Hardcoded default tool restrictions per purpose.
// User config overrides these entirely per purpose key.
var defaultToolRestrictions = map[string]gwtypes.ToolRestriction{
	"hass":    {Deny: []string{"exec", "write", "edit"}},
	"webhook": {Deny: []string{"exec", "write", "edit", "cron"}},
}

// defaultParallelToolAllowlist is the conservative readonly set for parallel batches.
var defaultParallelToolAllowlist = map[string]bool{
	"read":          true,
	"web_search":    true,
	"web_fetch":     true,
	"memory_get":    true,
	"memory_search": true,
	"transcript":    true,
}

func (g *Gateway) parallelToolAllowlist() map[string]bool {
	cfg := g.config.Gateway.ToolExecution.ParallelAllowlist
	if len(cfg) == 0 {
		return defaultParallelToolAllowlist
	}
	out := make(map[string]bool, len(cfg))
	for _, name := range cfg {
		n := strings.TrimSpace(name)
		if n == "" {
			continue
		}
		out[n] = true
	}
	return out
}

func (g *Gateway) parallelToolAllowlistNames() []string {
	allow := g.parallelToolAllowlist()
	names := make([]string, 0, len(allow))
	for name := range allow {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (g *Gateway) parallelToolMaxConcurrent() int {
	n := g.config.Gateway.ToolExecution.MaxConcurrent
	if n < 1 {
		return 1
	}
	if n > 16 {
		return 16
	}
	return n
}

func (g *Gateway) shouldRunToolsInParallel(calls []llm.ToolCallInfo) bool {
	if !g.config.Gateway.ToolExecution.ParallelEnabled {
		return false
	}
	if len(calls) < 2 {
		return false
	}
	allow := g.parallelToolAllowlist()
	for _, tc := range calls {
		if !allow[tc.Name] {
			return false
		}
	}
	return true
}

// getToolRestriction returns the tool restriction for a purpose, checking
// user config first, then falling back to hardcoded defaults.
func (g *Gateway) getToolRestriction(purpose string) *gwtypes.ToolRestriction {
	if r, ok := g.config.Security.ToolRestrictions[purpose]; ok {
		return &r
	}
	if r, ok := defaultToolRestrictions[purpose]; ok {
		return &r
	}
	return nil
}

// filterToolsForPurpose removes tools that are denied for the given purpose.
func (g *Gateway) filterToolsForPurpose(defs []tools.ToolDefinition, purpose string) []tools.ToolDefinition {
	restriction := g.getToolRestriction(purpose)
	if restriction == nil || len(restriction.Deny) == 0 {
		return defs
	}

	denied := make(map[string]bool, len(restriction.Deny))
	for _, name := range restriction.Deny {
		denied[name] = true
	}

	filtered := make([]tools.ToolDefinition, 0, len(defs))
	for _, def := range defs {
		if denied[def.Name] {
			L_debug("filterToolsForPurpose: excluded", "tool", def.Name, "purpose", purpose)
			continue
		}
		filtered = append(filtered, def)
	}

	if len(filtered) < len(defs) {
		L_debug("filterToolsForPurpose: filtered tools", "purpose", purpose, "total", len(defs), "allowed", len(filtered))
	}
	return filtered
}

// isToolDeniedForPurpose checks if a specific tool is denied for the purpose.
// Used as a runtime safety net in case the LLM hallucinates a hidden tool name.
func (g *Gateway) isToolDeniedForPurpose(toolName, purpose string) bool {
	restriction := g.getToolRestriction(purpose)
	if restriction == nil {
		return false
	}
	for _, name := range restriction.Deny {
		if name == toolName {
			return true
		}
	}
	return false
}

// CanUserUseCommands checks if a user has permission to use slash commands
func (g *Gateway) CanUserUseCommands(u *user.User) bool {
	if u == nil {
		return false
	}
	resolvedRole, err := g.users.ResolveUserRole(u)
	if err != nil {
		L_warn("gateway: failed to resolve role for command permission check", "user", u.Name, "error", err)
		return false
	}
	return resolvedRole.CanUseCommands()
}

// RunAgent executes an agent turn, streaming events to the channel
func (g *Gateway) RunAgent(ctx context.Context, req AgentRequest, events chan<- AgentEvent) error {
	defer close(events)

	// Validate request
	if req.User == nil {
		events <- EventAgentError{Error: "no authenticated user"}
		return fmt.Errorf("no authenticated user")
	}

	runID := uuid.New().String()
	runStart := time.Now()
	sessionKey := g.sessionKeyFor(req)

	// Get or create session first so we can check supervision
	var sess *session.Session
	if req.FreshContext {
		sess = g.sessions.GetFresh(sessionKey)
	} else {
		sess = g.sessions.Get(sessionKey)
	}

	// Set user on session so CancelAllForUser can find it for emergency stop
	sess.SetUser(req.User)

	// Extract userID for message persistence
	userID := ""
	if req.User != nil {
		userID = req.User.ID
	}

	// Resolve and store user's role permissions on the session
	if resolvedRole, err := g.users.ResolveUserRole(req.User); err == nil {
		sess.SetResolvedRole(resolvedRole)
	}

	// Create provider state accessor for stateful providers (e.g., xAI)
	stateAccessor := &providerStateAccessor{
		sessionKey: sessionKey,
		store:      g.sessions.GetStore(),
	}

	// Helper to send events to both the caller and supervision (if active)
	sendEvent := func(ev AgentEvent) {
		events <- ev
		if supervision := sess.GetSupervision(); supervision != nil {
			supervision.SendEvent(ev)
		}
	}

	// Respect /stop pause latch.
	if sess.IsPaused() {
		sendEvent(EventAgentStart{
			RunID:      runID,
			Source:     req.Source,
			SessionKey: sessionKey,
		})
		sendEvent(EventAgentEnd{RunID: runID, FinalText: "Tasks are stopped. Send /resume to continue."})
		return nil
	}

	sendEvent(EventAgentStart{
		RunID:      runID,
		Source:     req.Source,
		SessionKey: sessionKey,
	})

	// Ensure session has the model's context window size set
	if sess.GetMaxTokens() == 0 && g.llm != nil {
		sess.SetMaxTokens(g.llm.ContextTokens())
	}

	// For ephemeral runs: snapshot message count so we can roll back after.
	messageCountBefore := 0
	if req.IsHeartbeat || req.Ephemeral {
		messageCountBefore = sess.MessageCount()
	}

	// Track current message IDs for memory provenance (used by memory_graph_store)
	var currentMsgIDs []string

	ephemeralRun := req.IsHeartbeat || req.Ephemeral

	// Add user message with content blocks if any (skip if already added by supervision)
	if !req.SkipAddMessage {
		L_debug("RunAgent: adding user message", "session", sessionKey, "source", req.Source, "msgLen", len(req.UserMsg))
		var userMsgID string
		if len(req.ContentBlocks) > 0 {
			userMsgID = sess.AddUserMessageWithContent(req.UserMsg, req.Source, req.ContentBlocks)
		} else {
			userMsgID = sess.AddUserMessage(req.UserMsg, req.Source)
		}

		// Send user message to supervision if active
		if supervision := sess.GetSupervision(); supervision != nil {
			supervision.SendEvent(EventUserMessage{Content: req.UserMsg, Source: req.Source})
		}

		// Persist user message to SQLite (skip for ephemeral runs)
		if !ephemeralRun {
			g.persistMessage(ctx, PersistMessageParams{
				MsgID:      userMsgID,
				SessionKey: sessionKey,
				UserID:     userID,
				Role:       "user",
				Content:    req.UserMsg,
				Source:     req.Source,
			})
			// Track message ID for memory provenance
			if userMsgID != "" {
				currentMsgIDs = []string{userMsgID}
			}
		}
	} else {
		L_debug("RunAgent: skipping message add (already in session)", "session", sessionKey, "source", req.Source)
	}

	// Mirror user input to other channels immediately so peers see it without
	// waiting for the agent turn to finish.
	if !req.IsGroup && !req.SkipMirror {
		g.mirrorUserMessage(ctx, req.Source, req.UserMsg, req.User, g.channelsExcept(req.Source))
	}

	// Re-estimate session tokens BEFORE compaction check.
	// TotalTokens is normally updated from API response (after the call), but we need
	// an accurate count now to prevent context overflow. Without this, compaction check
	// uses stale counts from the previous turn and may miss that we're over the limit.
	estimator := session.GetTokenEstimator()
	estimatedTokens := estimator.EstimateSessionTokens(sess)
	sess.SetTotalTokens(estimatedTokens)
	L_debug("session: pre-flight token estimate",
		"estimated", estimatedTokens,
		"maxTokens", sess.GetMaxTokens(),
		"messages", sess.MessageCount())

	// Build system prompt
	var workspaceFiles []gcontext.WorkspaceFile
	if g.promptCache != nil {
		workspaceFiles = g.promptCache.GetWorkspaceFiles()
	}

	// Get skills prompt (filtered by user's role, with skills tool awareness)
	hasSkillsTool := sess.HasToolAccess("skills")
	skillsPrompt := g.GetSkillsPromptForUser(req.User, hasSkillsTool)

	// Determine memory access and role prompts from resolved role (reuse session's cached role)
	includeMemory := true
	var roleSystemPrompt, roleSystemPromptFile string
	if cachedRole := sess.ResolvedRole; cachedRole != nil {
		includeMemory = cachedRole.HasMemoryAccess()
		roleSystemPrompt = cachedRole.SystemPrompt
		roleSystemPromptFile = cachedRole.SystemPromptFile
	}

	// Check if agent-driven memory extraction is enabled
	agentExtraction := false
	var bulletinCfg memorygraph.BulletinConfig
	var memoryBulletin, contextBulletin string

	if mgr := memorygraph.GetManager(); mgr != nil {
		agentExtraction = mgr.Config().LiveExtraction.AgentExtraction
		bulletinCfg = mgr.Config().Bulletin

		// Determine if we should inject bulletins
		isHeartbeat := req.Purpose == "heartbeat"
		isCron := strings.HasPrefix(req.Purpose, "cron")

		shouldInject := bulletinCfg.Enabled && userID != ""
		if shouldInject && isHeartbeat && !bulletinCfg.InjectForHeartbeat {
			shouldInject = false
		}
		if shouldInject && isCron && !bulletinCfg.InjectForCron {
			shouldInject = false
		}

		if shouldInject {
			memoryBulletin, contextBulletin, _ = mgr.GetBulletins(ctx, userID)
			L_debug("bulletin: fetched for injection",
				"user", userID,
				"memoryLen", len(memoryBulletin),
				"contextLen", len(contextBulletin),
				"memoryMode", bulletinCfg.MemoryInjection,
				"contextMode", bulletinCfg.ContextInjection)
		}
	}

	// Build prompt params with bulletin injection based on config
	promptParams := gcontext.PromptParams{
		WorkspaceDir:          g.config.Gateway.WorkingDir,
		VisibleHomeDir:        sandbox.GetManager().ResolvePolicy().VisibleHomeDir,
		SandboxMode:           sandbox.GetManager().GetMode(),
		Tools:                 g.tools,
		Model:                 g.llm.Model(),
		Channel:               req.Source,
		User:                  req.User,
		TotalTokens:           sess.GetTotalTokens(),
		MaxTokens:             sess.GetMaxTokens(),
		WorkspaceFiles:        workspaceFiles,
		SkillsPrompt:          skillsPrompt,
		IncludeMemory:         includeMemory,
		RoleSystemPrompt:      roleSystemPrompt,
		RoleSystemPromptFile:  roleSystemPromptFile,
		TimeInSystemPrompt:    g.config.PromptCache.TimeInSystemPrompt,
		AgentExtraction:       agentExtraction,
		ParallelToolBatching:  true,
		ParallelExecution:     g.config.Gateway.ToolExecution.ParallelEnabled,
		ParallelMaxConcurrent: g.parallelToolMaxConcurrent(),
		ParallelEligibleTools: g.parallelToolAllowlistNames(),
	}

	// Inject bulletins into prompt params based on injection mode (only if not empty)
	if bulletinCfg.MemoryInjection == "prompt" && memoryBulletin != "" {
		promptParams.MemoryBulletin = memoryBulletin
	}
	if bulletinCfg.ContextInjection == "prompt" && contextBulletin != "" {
		promptParams.ContextBulletin = contextBulletin
	}

	systemPrompt := gcontext.BuildSystemPrompt(promptParams)

	// Append media storage instructions (text channels use inline {{media:}} syntax)
	systemPrompt += g.buildMediaInstructions(MediaPromptOptions{IsVoice: false})

	// Check if session is supervised - inject supervision prompt
	if sess.IsSupervised() {
		if supervision := sess.GetSupervision(); supervision != nil {
			systemPrompt += "\n\n" + gcontext.BuildSupervisionSection(supervision.GetSupervisorID())
			L_debug("supervision: prompt injected", "session", sessionKey, "supervisor", supervision.GetSupervisorID())
		}
	}

	// Check if LLM is disabled (ghostwriting mode)
	llmEnabled := sess.IsLLMEnabled()
	L_debug("RunAgent: LLM enabled check", "session", sessionKey, "llmEnabled", llmEnabled, "supervised", sess.IsSupervised())
	if !llmEnabled {
		L_info("supervision: LLM disabled, skipping generation", "session", sessionKey)
		sendEvent(EventAgentEnd{RunID: runID, FinalText: ""})
		return nil
	}

	// Consume pending guidance and inject as system messages
	if supervision := sess.GetSupervision(); supervision != nil && supervision.HasPendingGuidance() {
		guidance := supervision.ConsumePendingGuidance()
		for _, g := range guidance {
			guidanceMsg := fmt.Sprintf("[Supervisor: %s]: %s", g.From, g.Content)
			sess.AddUserMessage(guidanceMsg, "supervisor")
			L_info("supervision: guidance injected", "session", sessionKey, "from", g.From, "contentLen", len(g.Content))
		}
	}

	// Check if compaction is needed before proceeding
	if g.compactor != nil && g.compactor.ShouldCompact(sess) {
		L_info("compaction needed, running compaction", "runID", runID,
			"tokensBefore", sess.GetTotalTokens(),
			"messagesBefore", sess.MessageCount())
		result, err := g.compactor.Compact(ctx, sess, sess.SessionFile)
		if err != nil {
			L_error("compaction failed", "error", err)
			// Continue anyway - we'll try again next turn
		} else {
			L_info("compaction completed",
				"tokensAfter", sess.GetTotalTokens(),
				"messagesAfter", sess.MessageCount(),
				"fromCheckpoint", result.FromCheckpoint,
				"summaryModel", result.Model)
		}
	}

	// Check memory flush thresholds
	L_trace("session: checking memory flush thresholds",
		"usage", fmt.Sprintf("%.1f%%", sess.GetContextUsage()*100),
		"flushedThresholds", sess.FlushedThresholds)

	flushConfig := g.buildMemoryFlushConfig()
	flushResult := session.CheckMemoryFlushThresholds(sess, flushConfig)

	// If 90% threshold triggers a user message, inject it now
	if flushResult != nil && flushResult.UserMessage != "" {
		L_info("injecting memory flush user message", "percent", flushResult.Threshold.Percent)
		sess.AddUserMessage(flushResult.UserMessage, "system")
		session.MarkThresholdFired(sess, flushResult.Threshold.Percent)
	}

	// Inject session context into ctx for tools
	ctx = context.WithValue(ctx, ContextKeyChannel, req.Source)
	ctx = context.WithValue(ctx, ContextKeyChatID, req.ChatID)

	// Create cancellable context for emergency stop / supervision interrupt
	agentCtx, agentCancel := context.WithCancel(ctx)
	defer agentCancel()

	// Store cancel on session so /stop and panic phrase can reach it
	sess.SetCancelFunc(runID, agentCancel)
	defer sess.ClearCancelFunc(runID)
	runStopGeneration := sess.StopGeneration()

	// Also store on supervision if supervised (keeps existing interrupt behavior)
	if supervision := sess.GetSupervision(); supervision != nil {
		supervision.SetCancelFunc(agentCancel)
		defer supervision.ClearCancelFunc()
	}

	var (
		finalText  string
		finalPhase string
	)
	const maxOverflowRetries = 2 // Max times to retry after compaction

	if acpMgr := acp.GetManager(); acpMgr != nil && acpMgr.IsAttached(sessionKey) {
		if len(req.ContentBlocks) > 0 {
			err := fmt.Errorf("ACP MVP does not support content blocks yet")
			sendEvent(EventAgentError{RunID: runID, Error: err.Error()})
			return err
		}

		result, err := acpMgr.Prompt(agentCtx, sessionKey, req.UserMsg, acp.PromptOptions{
			OnEvent: func(ev acp.ACPEvent) {
				for _, mapped := range mapACPEventToGatewayEvents(runID, ev) {
					sendEvent(mapped)
				}
			},
		})
		if err != nil {
			sendEvent(EventAgentError{RunID: runID, Error: err.Error()})
			return err
		}

		finalText = result.FinalText
		L_trace("gateway: ACP final text", "runID", runID, "finalTextLen", len(strings.TrimSpace(finalText)), "finalText", summarizeGatewayTraceText(finalText))
		if !ephemeralRun && finalText != "" {
			assistantMsgID := sess.AddAssistantMessage(finalText)
			g.persistMessage(ctx, PersistMessageParams{
				MsgID:      assistantMsgID,
				SessionKey: sessionKey,
				UserID:     userID,
				Role:       "assistant",
				Content:    finalText,
				Source:     req.Source,
			})
		}
		if !req.IsGroup && !req.SkipMirror && finalText != "" {
			g.deliverAssistantMessage(ctx, req.User, finalText, g.channelsExcept(req.Source))
		}
		sendEvent(EventAgentEnd{RunID: runID, FinalText: finalText})
		return nil
	}

	// Resolve purpose once before the loop
	purpose := req.Purpose
	if purpose == "" {
		purpose = "agent"
	}

	// Agent loop - keep going until no more tool use
	for {
		// STOP generation changed: this run is stale and must exit.
		if sess.StopGeneration() != runStopGeneration {
			L_info("agent: stop generation changed, exiting run", "session", sessionKey, "runID", runID)
			sendEvent(EventAgentEnd{RunID: runID, FinalText: ""})
			return nil
		}

		// Check for cancellation (emergency stop, /stop command, panic phrase)
		select {
		case <-agentCtx.Done():
			L_info("agent: cancelled", "session", sessionKey)
			sendEvent(EventAgentEnd{RunID: runID, FinalText: ""})
			return nil
		default:
		}

		// Check for supervision interrupt request
		if supervision := sess.GetSupervision(); supervision != nil && supervision.HasInterruptRequest() {
			L_info("supervision: interrupt requested, stopping generation", "session", sessionKey)
			agentCancel()
			sendEvent(EventAgentEnd{RunID: runID, FinalText: ""})
			return nil
		}
		// Build context from session (messages and tool definitions)
		messages := sess.GetMessages()
		toolDefs := g.filterToolsForUser(req.User)
		toolDefs = g.filterToolsForPurpose(toolDefs, purpose)

		// Pre-flight check: estimate if we're approaching context limit
		estimatedTokens := sess.GetTotalTokens()
		maxTokens := sess.GetMaxTokens()
		if maxTokens > 0 && estimatedTokens > 0 {
			usagePercent := float64(estimatedTokens) / float64(maxTokens)
			if usagePercent > 0.95 {
				L_warn("pre-flight: context usage critical, compacting before API call",
					"estimatedTokens", estimatedTokens,
					"maxTokens", maxTokens,
					"usage", fmt.Sprintf("%.1f%%", usagePercent*100))
				if g.compactor != nil {
					_, err := g.compactor.Compact(ctx, sess, sess.SessionFile)
					if err != nil {
						L_error("pre-flight compaction failed", "error", err)
					} else {
						// Refresh messages after compaction
						messages = sess.GetMessages()
						L_info("pre-flight compaction completed",
							"newTokens", sess.GetTotalTokens(),
							"newUsage", fmt.Sprintf("%.1f%%", sess.GetContextUsage()*100))
					}
				}

				// Re-check after compaction - refuse if still way over context
				postCompactTokens := sess.GetTotalTokens()
				postCompactUsage := float64(postCompactTokens) / float64(maxTokens)
				if postCompactUsage > 1.1 {
					errMsg := fmt.Sprintf("Context overflow: %d tokens exceeds %d context window (%.0f%%). "+
						"Message may be too large to process. Try with a shorter message.",
						postCompactTokens, maxTokens, postCompactUsage*100)
					L_error("pre-flight: refusing API call due to context overflow",
						"tokens", postCompactTokens,
						"maxTokens", maxTokens,
						"usage", fmt.Sprintf("%.1f%%", postCompactUsage*100))
					sendEvent(EventAgentError{Error: errMsg})
					return fmt.Errorf("context overflow: %d tokens (%.0f%% of %d limit)",
						postCompactTokens, postCompactUsage*100, maxTokens)
				}
			}
		}

		// Stream from LLM with failover and overflow retry logic
		contextTokens := sess.GetTotalTokens()
		contextWindow := sess.GetMaxTokens()
		contextUsage := sess.GetContextUsage() * 100.0
		// Resolve thinking level using priority hierarchy
		thinkingLevel := g.resolveThinkingLevel(req, g.llm.Name())
		enableThinking := req.EnableThinking || thinkingLevel.IsEnabled()

		// Show user message preview in green for easy spotting
		preview := req.UserMsg
		if preview == "" {
			for i := len(messages) - 1; i >= 0; i-- {
				if messages[i].Role == "user" {
					preview = messages[i].Content
					break
				}
			}
		}
		const previewLen = 100
		if len(preview) > previewLen {
			preview = preview[:previewLen] + "..."
		}
		if len(preview) < previewLen {
			preview = preview + strings.Repeat(" ", previewLen-len(preview))
		}
		if preview != "" {
			// Bold black on bright green background - impossible to miss
			L_info("user", "msg", "\033[1;30;102m "+preview+" \033[0m")
		}

		estimatedInputCost := llm.EstimateInputCost(g.llm.MetadataProvider(), g.llm.Model(), contextTokens)
		systemPromptTokens := tokens.Estimate(systemPrompt)
		L_debug("invoking LLM",
			"provider", g.llm.Name(),
			"model", g.llm.Model(),
			"messages", len(messages),
			"tools", len(toolDefs),
			"systemPromptTokens", systemPromptTokens,
			"contextTokens", contextTokens,
			"contextWindow", contextWindow,
			"contextUsage", fmt.Sprintf("%.1f%%", contextUsage),
			"thinking", enableThinking,
			"thinkingLevel", thinkingLevel,
			"estimatedInputCost", fmt.Sprintf("$%.4f", estimatedInputCost),
		)

		metrics.MetricSet("session", "messages", int64(len(messages)))
		metrics.MetricSet("session", "context_tokens", int64(contextTokens))
		metrics.MetricSet("session", "context_window", int64(contextWindow))
		metrics.MetricSet("session", "system_prompt_tokens", int64(systemPromptTokens))
		metrics.MetricInc("session", "agent_runs")

		// Build stream options (OnServerToolCall always; thinking opts when enabled)
		streamOpts := &llm.StreamOptions{
			OnServerToolCall: func(name, args, status, errMsg string) {
				if status == "pending" {
					sendEvent(EventToolStart{RunID: runID, ToolName: name, ToolID: "", Input: json.RawMessage(args)})
				} else {
					result := "(server executed)"
					if status == "failed" {
						result = ""
					}
					sendEvent(EventToolEnd{RunID: runID, ToolName: name, ToolID: "", Result: result, DisplayResult: result, Error: errMsg})
				}
			},
			OnBeforeModelAttempt: func(modelRef string, modelContextWindow int) error {
				if modelContextWindow <= 0 {
					return nil
				}
				estimated := sess.GetTotalTokens()
				if estimated <= 0 {
					return nil
				}
				// Session token estimates can undercount provider-side tokenization.
				// Inflate before checking smaller failover windows.
				projected := int(float64(estimated) * 1.4)
				safetyBuffer := 4000
				limit := modelContextWindow - safetyBuffer
				if limit < modelContextWindow/2 {
					limit = modelContextWindow / 2
				}
				if projected > limit {
					L_warn("pre-failover context check: projected overflow",
						"model", modelRef,
						"estimated", estimated,
						"projected", projected,
						"contextWindow", modelContextWindow,
						"limit", limit)
					return fmt.Errorf("context overflow: projected %d tokens exceeds %d for model %s", projected, modelContextWindow, modelRef)
				}
				return nil
			},
		}
		if enableThinking {
			streamOpts.EnableThinking = true
			streamOpts.ThinkingLevel = thinkingLevel.String()
			streamOpts.ThinkingBudget = thinkingLevel.AnthropicBudgetTokens()
			streamOpts.OnThinkingDelta = func(delta string) {
				sendEvent(EventThinkingDelta{RunID: runID, Delta: delta})
			}
		}

		// Resolve media content (FilePath -> base64 Data) before sending to LLM
		resolvedMessages := g.resolveMediaContent(messages, g.llm)

		// Build ephemeral messages (time, bulletins) to inject before the last user message
		// This placement maximizes prompt cache efficiency: conversation history stays
		// cacheable, while dynamic content is placed near the current turn.
		var ephemeralMessages []types.Message

		// System time and uptime (if enabled)
		if g.config.PromptCache.TimeInUserMessage {
			ts := time.Now().Format("Mon 2006-01-02 15:04 MST")
			content := "[Current Time: " + ts
			if g.config.PromptCache.ShowUptime {
				content += " | Uptime: " + formatUptime(time.Since(g.startTime))
			}
			content += "]"
			ephemeralMessages = append(ephemeralMessages, types.Message{
				Role:      "system",
				Content:   content,
				Timestamp: time.Now(),
			})
		}

		// Memory bulletin (if configured for message injection)
		if bulletinCfg.MemoryInjection == "message" && memoryBulletin != "" {
			ephemeralMessages = append(ephemeralMessages, types.Message{
				Role:      "system",
				Content:   "[Memory Bulletin]\n" + memoryBulletin,
				Timestamp: time.Now(),
			})
			L_debug("bulletin: prepared memory for injection", "len", len(memoryBulletin))
		}

		// Context bulletin (if configured for message injection)
		if bulletinCfg.ContextInjection == "message" && contextBulletin != "" {
			ephemeralMessages = append(ephemeralMessages, types.Message{
				Role:      "system",
				Content:   "[Context Update]\n" + contextBulletin,
				Timestamp: time.Now(),
			})
			L_debug("bulletin: prepared context for injection", "len", len(contextBulletin))
		}

		// Chat context section (query-driven, not cached)
		// Uses FTS search based on user's latest message
		if mgr := memorygraph.GetManager(); mgr != nil && bulletinCfg.ChatContextEnabled {
			// Find the last user message content
			var lastUserMessage string
			for i := len(resolvedMessages) - 1; i >= 0; i-- {
				if resolvedMessages[i].Role == "user" && resolvedMessages[i].Content != "" {
					lastUserMessage = resolvedMessages[i].Content
					break
				}
			}

			if lastUserMessage != "" {
				chatContext := memorygraph.BuildChatContextSection(agentCtx, mgr, userID, lastUserMessage, bulletinCfg)
				if chatContext != "" {
					ephemeralMessages = append(ephemeralMessages, types.Message{
						Role:      "system",
						Content:   "[Relevant Memories]\nUse this information before using the memory_graph_search_tool, unless nothing is relevant.\n" + chatContext,
						Timestamp: time.Now(),
					})
					// Log actual FTS keywords and preview of results
					maxKw := bulletinCfg.ChatContextMaxKeywords
					if maxKw <= 0 {
						maxKw = 8
					}
					ftsKeywords := memorygraph.ExtractKeywords(lastUserMessage, bulletinCfg.ChatContextLanguage, maxKw)
					contextPreview := chatContext
					if len(contextPreview) > 300 {
						contextPreview = contextPreview[:300] + "..."
					}
					L_debug("bulletin: chat context",
						"ftsQuery", ftsKeywords,
						"preview", contextPreview,
						"len", len(chatContext))
				}
			}
		}

		// Inject all ephemeral messages just before the last user message
		if len(ephemeralMessages) > 0 {
			resolvedMessages = injectEphemeralBeforeLastUser(resolvedMessages, ephemeralMessages...)
			L_debug("ephemeral: injected before last user message", "count", len(ephemeralMessages))
		}

		var response *llm.Response
		var failoverResult *llm.FailoverResult
		var llmErr error
		var successfulStreamText string
		for retry := 0; retry <= maxOverflowRetries; retry++ {
			var attemptStream strings.Builder
			var attemptStreamMu sync.Mutex
			failoverResult, llmErr = g.registry.StreamMessageWithFailover(
				agentCtx,
				purpose,
				stateAccessor,
				resolvedMessages,
				toolDefs,
				systemPrompt,
				func(delta string) {
					attemptStreamMu.Lock()
					attemptStream.WriteString(delta)
					attemptStreamMu.Unlock()
					sendEvent(EventTextDelta{RunID: runID, Delta: delta})
				},
				streamOpts,
			)

			if llmErr == nil {
				response = failoverResult.Response
				attemptStreamMu.Lock()
				successfulStreamText = attemptStream.String()
				attemptStreamMu.Unlock()
				break // Success
			}

			// Check if this is a context overflow error (not handled by failover)
			errType := llm.ClassifyError(llmErr.Error())
			if errType == llm.ErrorTypeContextOverflow {
				if retry < maxOverflowRetries && g.compactor != nil {
					L_warn("context overflow detected, attempting recovery compaction",
						"retry", retry+1,
						"maxRetries", maxOverflowRetries,
						"error", llmErr.Error())

					// Perform emergency compaction
					_, compactErr := g.compactor.Compact(ctx, sess, sess.SessionFile)
					if compactErr != nil {
						L_error("recovery compaction failed", "error", compactErr)
						break // Can't recover
					}

					// Refresh messages after compaction
					messages = sess.GetMessages()
					resolvedMessages = g.resolveMediaContent(messages, g.llm)
					L_info("recovery compaction completed, retrying API call",
						"newTokens", sess.GetTotalTokens(),
						"newMessages", len(messages))
					continue // Retry the API call
				}
				L_error("context overflow: max retries exceeded", "retries", retry)
			}
			break // Non-overflow error or max retries reached
		}

		if llmErr != nil {
			// Format user-friendly error message
			errType := llm.ClassifyError(llmErr.Error())
			userMsg := llm.FormatErrorForUser(llmErr.Error(), errType)
			sendEvent(EventAgentError{RunID: runID, Error: userMsg})
			return fmt.Errorf("%s", userMsg)
		}

		// Log which model was used (for diagnostics)
		if failoverResult != nil && failoverResult.ModelUsed != "" {
			L_debug("llm response",
				"model", failoverResult.ModelUsed,
				"failedOver", failoverResult.FailedOver,
				"stopReason", response.StopReason)

			// Send failover/recovery notifications (only when thinking enabled)
			if req.User.Thinking {
				// Failover notification
				if failoverResult.FailedOver && len(failoverResult.Attempts) > 0 {
					var reasons []string
					for _, a := range failoverResult.Attempts {
						if a.Skipped {
							reasons = append(reasons, fmt.Sprintf("%s (cooldown)", a.Model))
						} else if a.Reason != "" {
							reasons = append(reasons, fmt.Sprintf("%s (%s)", a.Model, a.Reason))
						}
					}
					if len(reasons) > 0 {
						msg := fmt.Sprintf("[goclaw system] ⚠️ Switched to %s (%s)", failoverResult.ModelUsed, strings.Join(reasons, " → "))
						g.SendStatusMessage(ctx, req.User, msg)
					}
				}

				// Recovery notification
				if failoverResult.Recovered != nil {
					msg := fmt.Sprintf("[goclaw system] ✓ %s recovered (was: %s)", failoverResult.Recovered.Provider, failoverResult.Recovered.WasReason)
					g.SendStatusMessage(ctx, req.User, msg)
				}
			}
		}

		// Update token tracking
		sess.UpdateTokens(response.InputTokens, response.OutputTokens)
		// Also update TotalTokens (current context size) for compaction threshold checking
		if response.InputTokens > 0 {
			sess.SetTotalTokens(response.InputTokens)
		}

		// Emit thinking event if we have reasoning content
		if response.Thinking != "" {
			sendEvent(EventThinking{RunID: runID, Content: response.Thinking})
		}

		// Handle tool use - process ALL tool calls from this response
		if response.HasToolUse() {
			// Generate a single responseGroupID for all tools in this batch
			responseGroupID := session.GenerateResponseGroupID()

			// Prepare session context once (reused for all tools in batch)
			ownerChatID := ""
			if owner := g.users.Owner(); owner != nil {
				ownerChatID = owner.TelegramID
			}
			reserveTokens := 0
			if g.compactor != nil {
				reserveTokens = g.compactor.GetReserveTokens()
			}
			transcriptScope := "own" // Default to restrictive
			if resolvedRole, err := g.users.ResolveUserRole(req.User); err == nil {
				transcriptScope = resolvedRole.GetTranscriptScope()
			}

			// Conservative parallel mode: only enabled + allowlisted readonly tool batches.
			if g.shouldRunToolsInParallel(response.ToolCalls) {
				L_info("tools: executing batch in parallel",
					"count", len(response.ToolCalls),
					"maxConcurrent", g.parallelToolMaxConcurrent(),
					"responseGroupID", responseGroupID)

				type parallelToolResult struct {
					resultText  string
					displayText string
					content     []types.ContentBlock
					errStr      string
					durationMs  int64
					handled     bool
				}
				results := make([]parallelToolResult, len(response.ToolCalls))
				execIndexes := make([]int, 0, len(response.ToolCalls))

				// Pre-check and emit starts in original order.
				for i, tc := range response.ToolCalls {
					if !req.User.CanUseTool(tc.Name) {
						result := fmt.Sprintf("Permission denied: %s cannot use tool %s", req.User.Name, tc.Name)
						sendEvent(EventToolEnd{
							RunID:         runID,
							ToolName:      tc.Name,
							ToolID:        tc.ID,
							Result:        result,
							DisplayResult: result,
							Error:         "permission_denied",
						})
						results[i] = parallelToolResult{
							resultText:  result,
							displayText: result,
							errStr:      "permission_denied",
							handled:     true,
						}
						continue
					}
					if g.isToolDeniedForPurpose(tc.Name, purpose) {
						result := fmt.Sprintf("Permission denied: tool %s is not available for purpose %q", tc.Name, purpose)
						sendEvent(EventToolEnd{
							RunID:         runID,
							ToolName:      tc.Name,
							ToolID:        tc.ID,
							Result:        result,
							DisplayResult: result,
							Error:         "purpose_denied",
						})
						results[i] = parallelToolResult{
							resultText:  result,
							displayText: result,
							errStr:      "purpose_denied",
							handled:     true,
						}
						continue
					}
					sendEvent(EventToolStart{
						RunID:    runID,
						ToolName: tc.Name,
						ToolID:   tc.ID,
						Input:    tc.Input,
					})
					execIndexes = append(execIndexes, i)
				}

				sem := make(chan struct{}, g.parallelToolMaxConcurrent())
				var wg sync.WaitGroup
				for _, idx := range execIndexes {
					tc := response.ToolCalls[idx]
					wg.Add(1)
					go func(i int, tc llm.ToolCallInfo) {
						defer wg.Done()
						select {
						case sem <- struct{}{}:
							defer func() { <-sem }()
						case <-agentCtx.Done():
							sendEvent(EventToolEnd{
								RunID:         runID,
								ToolName:      tc.Name,
								ToolID:        tc.ID,
								Result:        "Tool execution cancelled",
								DisplayResult: "Tool execution cancelled",
								Error:         "cancelled",
							})
							results[i] = parallelToolResult{
								resultText:  "Tool execution cancelled",
								displayText: "Tool execution cancelled",
								errStr:      "cancelled",
								handled:     true,
							}
							return
						}

						toolStartTime := time.Now()
						toolCtx := tools.WithSessionContext(agentCtx, &tools.SessionContext{
							Channel:           req.Source,
							ChatID:            req.ChatID,
							OwnerChatID:       ownerChatID,
							SessionKey:        sessionKey,
							RunID:             runID,
							TotalTokens:       sess.GetTotalTokens(),
							MaxTokens:         sess.GetMaxTokens(),
							ReserveTokens:     reserveTokens,
							User:              req.User,
							TranscriptScope:   transcriptScope,
							Session:           sess,
							CurrentMessageIDs: currentMsgIDs,
						})
						toolResult, err := g.tools.Execute(toolCtx, tc.Name, tc.Input)
						toolDuration := time.Since(toolStartTime)

						errStr := ""
						if err != nil {
							errStr = err.Error()
							toolResult = types.ErrorResult(err.Error())
						}

						resultText := toolResult.GetText()
						displayText := resultText
						if toolResult.ExternalContent {
							wrapped, spoofed := security.WrapExternalContent(resultText, toolResult.ExternalSource, tc.Name)
							if spoofed {
								L_warn("security: marker spoofing detected, content blocked",
									"tool", tc.Name, "source", toolResult.ExternalSource)
								g.SendStatusMessage(ctx, req.User,
									"⚠️ Security: Marker spoofing attack detected in content from "+tc.Name+". Content discarded.")
							}
							resultText = wrapped
						}
						if req.OnMediaToSend != nil {
							parseResultWrapped := media.SplitMediaFromOutput(resultText)
							resultText = parseResultWrapped.Text
							parseResultDisplay := media.SplitMediaFromOutput(displayText)
							displayText = parseResultDisplay.Text
							for _, mediaPath := range parseResultWrapped.MediaURLs {
								if mediaErr := req.OnMediaToSend(mediaPath, ""); mediaErr != nil {
									L_warn("failed to send media", "path", mediaPath, "error", mediaErr)
								}
							}
						}

						sendEvent(EventToolEnd{
							RunID:         runID,
							ToolName:      tc.Name,
							ToolID:        tc.ID,
							Result:        resultText,
							DisplayResult: displayText,
							Error:         errStr,
							DurationMs:    toolDuration.Milliseconds(),
						})
						results[i] = parallelToolResult{
							resultText:  resultText,
							displayText: displayText,
							content:     toolResult.Content,
							errStr:      errStr,
							durationMs:  toolDuration.Milliseconds(),
							handled:     true,
						}
					}(idx, tc)
				}
				wg.Wait()

				// Persist/session writes in original tool-call order for deterministic history.
				for i, tc := range response.ToolCalls {
					r := results[i]
					if !r.handled {
						r.resultText = "Tool execution failed: no result"
						r.errStr = "no_result"
					}

					toolUseID := sess.AddToolUse(tc.ID, tc.Name, tc.Input, response.Thinking, responseGroupID)
					toolResultID := sess.AddToolResult(tc.ID, r.resultText, r.content, responseGroupID)
					if !ephemeralRun {
						g.persistMessage(ctx, PersistMessageParams{
							MsgID:           toolUseID,
							SessionKey:      sessionKey,
							UserID:          userID,
							Role:            "tool_use",
							Source:          req.Source,
							ToolCallID:      tc.ID,
							ToolName:        tc.Name,
							ToolInput:       tc.Input,
							Thinking:        response.Thinking,
							ResponseGroupID: responseGroupID,
						})
						g.persistMessage(ctx, PersistMessageParams{
							MsgID:           toolResultID,
							SessionKey:      sessionKey,
							UserID:          userID,
							Role:            "tool_result",
							Content:         r.resultText,
							Source:          req.Source,
							ToolCallID:      tc.ID,
							ToolError:       r.errStr,
							ResponseGroupID: responseGroupID,
						})
						if r.errStr == "" && tc.Name == "message" {
							if sentText := extractMessageToolText(tc.Input); sentText != "" {
								g.persistMessage(ctx, PersistMessageParams{
									SessionKey: sessionKey,
									UserID:     userID,
									Role:       "assistant",
									Content:    sentText,
									Source:     "message_tool",
								})
							}
						}
					}
				}

				// Batch complete; ask the LLM for the next turn with tool results in context.
				continue
			}

			// Execute ALL tool calls sequentially
			for _, tc := range response.ToolCalls {
				// Check for cancellation BEFORE each tool
				select {
				case <-agentCtx.Done():
					L_info("agent: cancelled before tool execution", "session", sessionKey, "tool", tc.Name)
					sendEvent(EventAgentEnd{RunID: runID, FinalText: ""})
					return nil
				default:
				}

				// Check permissions for this tool
				if !req.User.CanUseTool(tc.Name) {
					result := fmt.Sprintf("Permission denied: %s cannot use tool %s", req.User.Name, tc.Name)
					sendEvent(EventToolEnd{
						RunID:         runID,
						ToolName:      tc.Name,
						ToolID:        tc.ID,
						Result:        result,
						DisplayResult: result,
						Error:         "permission_denied",
					})
					toolUseID := sess.AddToolUse(tc.ID, tc.Name, tc.Input, response.Thinking, responseGroupID)
					toolResultID := sess.AddToolResult(tc.ID, result, nil, responseGroupID)
					if !ephemeralRun {
						g.persistMessage(ctx, PersistMessageParams{
							MsgID:           toolUseID,
							SessionKey:      sessionKey,
							UserID:          userID,
							Role:            "tool_use",
							Source:          req.Source,
							ToolCallID:      tc.ID,
							ToolName:        tc.Name,
							ToolInput:       tc.Input,
							Thinking:        response.Thinking,
							ResponseGroupID: responseGroupID,
						})
						g.persistMessage(ctx, PersistMessageParams{
							MsgID:           toolResultID,
							SessionKey:      sessionKey,
							UserID:          userID,
							Role:            "tool_result",
							Content:         result,
							Source:          req.Source,
							ToolCallID:      tc.ID,
							ResponseGroupID: responseGroupID,
						})
					}
					continue // Continue to next tool in batch
				}

				// Runtime safety net: deny tools restricted by purpose
				if g.isToolDeniedForPurpose(tc.Name, purpose) {
					L_warn("gateway: tool denied for purpose", "tool", tc.Name, "purpose", purpose)
					result := fmt.Sprintf("Permission denied: tool %s is not available for purpose %q", tc.Name, purpose)
					sendEvent(EventToolEnd{
						RunID:         runID,
						ToolName:      tc.Name,
						ToolID:        tc.ID,
						Result:        result,
						DisplayResult: result,
						Error:         "purpose_denied",
					})
					toolUseID := sess.AddToolUse(tc.ID, tc.Name, tc.Input, response.Thinking, responseGroupID)
					toolResultID := sess.AddToolResult(tc.ID, result, nil, responseGroupID)
					if !ephemeralRun {
						g.persistMessage(ctx, PersistMessageParams{
							MsgID:           toolUseID,
							SessionKey:      sessionKey,
							UserID:          userID,
							Role:            "tool_use",
							Source:          req.Source,
							ToolCallID:      tc.ID,
							ToolName:        tc.Name,
							ToolInput:       tc.Input,
							Thinking:        response.Thinking,
							ResponseGroupID: responseGroupID,
						})
						g.persistMessage(ctx, PersistMessageParams{
							MsgID:           toolResultID,
							SessionKey:      sessionKey,
							UserID:          userID,
							Role:            "tool_result",
							Content:         result,
							Source:          req.Source,
							ToolCallID:      tc.ID,
							ResponseGroupID: responseGroupID,
						})
					}
					continue // Continue to next tool in batch
				}

				sendEvent(EventToolStart{
					RunID:    runID,
					ToolName: tc.Name,
					ToolID:   tc.ID,
					Input:    tc.Input,
				})

				// Execute tool with session context
				toolStartTime := time.Now()
				toolCtx := tools.WithSessionContext(agentCtx, &tools.SessionContext{
					Channel:           req.Source,
					ChatID:            req.ChatID,
					OwnerChatID:       ownerChatID,
					SessionKey:        sessionKey,
					RunID:             runID,
					TotalTokens:       sess.GetTotalTokens(),
					MaxTokens:         sess.GetMaxTokens(),
					ReserveTokens:     reserveTokens,
					User:              req.User,
					TranscriptScope:   transcriptScope,
					Session:           sess,
					CurrentMessageIDs: currentMsgIDs,
				})
				toolResult, err := g.tools.Execute(toolCtx, tc.Name, tc.Input)
				toolDuration := time.Since(toolStartTime)

				errStr := ""
				if err != nil {
					errStr = err.Error()
					toolResult = types.ErrorResult(err.Error())
				}

				// Get text content for downstream processing
				resultText := toolResult.GetText()
				displayText := resultText

				// Wrap external content with security boundaries
				if toolResult.ExternalContent {
					wrapped, spoofed := security.WrapExternalContent(resultText, toolResult.ExternalSource, tc.Name)
					if spoofed {
						L_warn("security: marker spoofing detected, content blocked",
							"tool", tc.Name, "source", toolResult.ExternalSource)
						g.SendStatusMessage(ctx, req.User,
							"⚠️ Security: Marker spoofing attack detected in content from "+tc.Name+". Content discarded.")
					} else {
						L_debug("gateway: wrapped external content",
							"tool", tc.Name, "source", toolResult.ExternalSource)
					}
					resultText = wrapped
				}

				// Check for media in tool output
				if req.OnMediaToSend != nil {
					parseResultWrapped := media.SplitMediaFromOutput(resultText)
					resultText = parseResultWrapped.Text
					parseResultDisplay := media.SplitMediaFromOutput(displayText)
					displayText = parseResultDisplay.Text
					for _, mediaPath := range parseResultWrapped.MediaURLs {
						if mediaErr := req.OnMediaToSend(mediaPath, ""); mediaErr != nil {
							L_warn("failed to send media", "path", mediaPath, "error", mediaErr)
						}
					}
				}

				sendEvent(EventToolEnd{
					RunID:         runID,
					ToolName:      tc.Name,
					ToolID:        tc.ID,
					Result:        resultText,
					DisplayResult: displayText,
					Error:         errStr,
					DurationMs:    toolDuration.Milliseconds(),
				})

				// Add to session
				toolUseID := sess.AddToolUse(tc.ID, tc.Name, tc.Input, response.Thinking, responseGroupID)
				toolResultID := sess.AddToolResult(tc.ID, resultText, toolResult.Content, responseGroupID)

				// Debug: log ContentBlocks being stored
				if len(toolResult.Content) > 0 {
					for i, block := range toolResult.Content {
						L_debug("tool result content block",
							"tool", tc.Name,
							"blockIndex", i,
							"type", block.Type,
							"hasFilePath", block.FilePath != "",
							"hasData", block.Data != "",
							"mimeType", block.MimeType,
						)
					}
				}

				// Persist tool use and result to SQLite (skip for heartbeat - ephemeral)
				if !ephemeralRun {
					g.persistMessage(ctx, PersistMessageParams{
						MsgID:           toolUseID,
						SessionKey:      sessionKey,
						UserID:          userID,
						Role:            "tool_use",
						Source:          req.Source,
						ToolCallID:      tc.ID,
						ToolName:        tc.Name,
						ToolInput:       tc.Input,
						Thinking:        response.Thinking,
						ResponseGroupID: responseGroupID,
					})
					g.persistMessage(ctx, PersistMessageParams{
						MsgID:           toolResultID,
						SessionKey:      sessionKey,
						UserID:          userID,
						Role:            "tool_result",
						Content:         resultText,
						Source:          req.Source,
						ToolCallID:      tc.ID,
						ToolError:       errStr,
						ResponseGroupID: responseGroupID,
					})

					// Persist sent message content as a first-class assistant message for transcript searchability
					// Check EACH tool in batch for "message" tool
					if errStr == "" && tc.Name == "message" {
						if sentText := extractMessageToolText(tc.Input); sentText != "" {
							g.persistMessage(ctx, PersistMessageParams{
								SessionKey: sessionKey,
								UserID:     userID,
								Role:       "assistant",
								Content:    sentText,
								Source:     "message_tool",
							})
							L_debug("gateway: persisted message tool send as assistant message", "session", sessionKey, "contentLen", len(sentText))
						}
					}
				}
			}
			// All tools in batch executed, continue to next LLM call
			continue
		}

		// No tool use - we're done
		finalText = response.Text
		finalPhase = response.Phase
		if finalText == "" && successfulStreamText != "" {
			L_info("agent: using streamed delta fallback for final text",
				"runID", runID,
				"streamedLen", len(successfulStreamText))
			finalText = successfulStreamText
		}
		break
	}

	if finalText == "" {
		L_warn("agent run completed with empty response", "runID", runID, "messages", sess.MessageCount())
	}
	runElapsed := time.Since(runStart)
	L_info("agent run completed", "runID", runID, "responseLen", len(finalText), "elapsed", runElapsed.Round(time.Millisecond))
	metrics.MetricDuration("session", "agent_response_time", runElapsed)

	// Agent response preview (same style as user message - green, 100 chars)
	if finalText != "" {
		const previewLen = 100
		preview := finalText
		if len(preview) > previewLen {
			preview = preview[:previewLen] + "..."
		}
		if len(preview) < previewLen {
			preview = preview + strings.Repeat(" ", previewLen-len(preview))
		}
		L_info("agent", "msg", "\033[1;30;104m "+preview+" \033[0m")
	}

	// Enrich media references: {{media:path}} -> {{media:mime:'path'}}
	finalText = g.enrichMediaRefs(finalText)

	// For ephemeral runs: rollback in-memory session to before the run.
	if ephemeralRun && messageCountBefore > 0 {
		sess.TruncateMessages(messageCountBefore)
		L_debug("heartbeat: rolled back session messages", "before", messageCountBefore, "after", sess.MessageCount())
	}

	// Check for suppression tokens - if response contains any, suppress delivery
	// These tokens indicate agent has nothing meaningful to say
	if shouldSuppressResponse(finalText) {
		L_debug("gateway: response suppressed (matched silent token)", "session", sessionKey, "responseLen", len(finalText))
		finalText = ""
	} else if finalText != "" {
		assistantMsgID := sess.AddAssistantMessageWithPhase(finalText, finalPhase)
		// Persist assistant message (skip for ephemeral runs)
		if !ephemeralRun {
			g.persistMessage(ctx, PersistMessageParams{
				MsgID:      assistantMsgID,
				SessionKey: sessionKey,
				UserID:     userID,
				Role:       "assistant",
				Content:    finalText,
				Phase:      finalPhase,
			})
		}
	}

	sendEvent(EventAgentEnd{RunID: runID, FinalText: finalText})

	// Check if checkpoint should be generated (async, non-blocking)
	if g.checkpointGenerator != nil {
		shouldCheckpoint := g.checkpointGenerator.ShouldCheckpoint(sess)
		L_trace("session: checking checkpoint trigger",
			"shouldCheckpoint", shouldCheckpoint,
			"usage", fmt.Sprintf("%.1f%%", sess.GetContextUsage()*100))

		if shouldCheckpoint {
			L_info("generating checkpoint async", "runID", runID)
			g.checkpointGenerator.GenerateAsync(sess, sess.SessionFile)
		}
	}

	// Reset flush thresholds if context dropped (e.g., after compaction)
	session.ResetThresholdsIfNeeded(sess)

	// Distribute assistant response to other channels (not for group chats, not if caller handles delivery).
	// User message mirroring is intentionally done earlier (pre-turn) for better symmetry.
	if !req.IsGroup && !req.SkipMirror {
		otherChannels := g.channelsExcept(req.Source)
		g.deliverAssistantMessage(ctx, req.User, finalText, otherChannels)
	}

	return nil
}

// SessionInfo contains session status information
type SessionInfo struct {
	SessionKey      string
	Messages        int
	TotalTokens     int
	MaxTokens       int
	UsagePercent    float64
	CompactionCount int
	LastCompaction  *session.StoredCompaction
}

// ForceCompact triggers compaction for a session regardless of token threshold
func (g *Gateway) ForceCompact(ctx context.Context, sessionKey string) (*session.CompactionResult, error) {
	if g.compactor == nil {
		return nil, fmt.Errorf("compactor not configured")
	}

	sess := g.sessions.Get(sessionKey)
	if sess == nil {
		return nil, fmt.Errorf("session not found: %s", sessionKey)
	}

	if sess.MessageCount() < 4 {
		return nil, fmt.Errorf("session too short to compact (need at least 4 messages)")
	}

	L_info("force compaction requested",
		"sessionKey", sessionKey,
		"messages", sess.MessageCount(),
		"tokens", sess.GetTotalTokens())

	result, err := g.compactor.Compact(ctx, sess, sess.SessionFile)
	if err != nil {
		return nil, fmt.Errorf("compaction failed: %w", err)
	}

	return result, nil
}

// GetSessionInfo returns info about a session including last compaction
func (g *Gateway) GetSessionInfo(ctx context.Context, sessionKey string) (*SessionInfo, error) {
	sess := g.sessions.Get(sessionKey)
	if sess == nil {
		return nil, fmt.Errorf("session not found: %s", sessionKey)
	}

	// Ensure session has MaxTokens set
	if sess.GetMaxTokens() == 0 && g.llm != nil {
		sess.SetMaxTokens(g.llm.ContextTokens())
	}

	info := &SessionInfo{
		SessionKey:      sessionKey,
		Messages:        sess.MessageCount(),
		TotalTokens:     sess.GetTotalTokens(),
		MaxTokens:       sess.GetMaxTokens(),
		UsagePercent:    sess.GetContextUsage() * 100,
		CompactionCount: sess.CompactionCount,
	}

	// Get last compaction from store
	store := g.sessions.GetStore()
	if store != nil {
		compactions, err := store.GetCompactions(ctx, session.PrimarySession)
		if err == nil && len(compactions) > 0 {
			info.LastCompaction = &compactions[len(compactions)-1]
		}
	}

	return info, nil
}

// GetCompactionStatus returns the current compaction manager health status
func (g *Gateway) GetCompactionStatus(ctx context.Context) session.CompactionStatus {
	if g.compactor == nil {
		return session.CompactionStatus{}
	}
	return g.compactor.GetStatus(ctx)
}

// GetSessionInfoForCommands returns session info in the format expected by the commands package
func (g *Gateway) GetSessionInfoForCommands(ctx context.Context, sessionKey string) (*commands.SessionInfo, error) {
	info, err := g.GetSessionInfo(ctx, sessionKey)
	if err != nil {
		return nil, err
	}
	return &commands.SessionInfo{
		SessionKey:      info.SessionKey,
		Messages:        info.Messages,
		TotalTokens:     info.TotalTokens,
		MaxTokens:       info.MaxTokens,
		UsagePercent:    info.UsagePercent,
		CompactionCount: info.CompactionCount,
		LastCompaction:  info.LastCompaction,
	}, nil
}

func (g *Gateway) resolveACPUser(userID string) (*user.User, error) {
	u := g.users.Get(userID)
	if u == nil {
		return nil, fmt.Errorf("user not found: %s", userID)
	}
	return u, nil
}

func (g *Gateway) ACPAttach(ctx context.Context, sessionKey string, userID string, driver string, cwd string, mode string, sessionID string) (*acp.AttachmentInfo, error) {
	mgr := acp.GetManager()
	if mgr == nil {
		return nil, fmt.Errorf("ACP manager not initialized")
	}
	u, err := g.resolveACPUser(userID)
	if err != nil {
		return nil, err
	}
	return mgr.Attach(ctx, acp.AttachRequest{
		SessionKey: sessionKey,
		User:       u,
		DriverID:   driver,
		Transport:  acp.TransportLocalStdio,
		CWD:        cwd,
		Mode:       mode,
		SessionID:  sessionID,
	})
}

func (g *Gateway) ACPDetach(sessionKey string) (*acp.AttachmentInfo, error) {
	mgr := acp.GetManager()
	if mgr == nil {
		return nil, fmt.Errorf("ACP manager not initialized")
	}
	return mgr.Detach(sessionKey)
}

func (g *Gateway) ACPInspect(sessionKey string) (*acp.AttachmentInfo, error) {
	mgr := acp.GetManager()
	if mgr == nil {
		return nil, fmt.Errorf("ACP manager not initialized")
	}
	return mgr.Inspect(sessionKey)
}

func (g *Gateway) ACPClose(ctx context.Context, sessionKey string) error {
	mgr := acp.GetManager()
	if mgr == nil {
		return fmt.Errorf("ACP manager not initialized")
	}
	return mgr.Close(ctx, sessionKey)
}

func (g *Gateway) ACPSetMode(ctx context.Context, sessionKey string, mode string) (*acp.AttachmentInfo, error) {
	mgr := acp.GetManager()
	if mgr == nil {
		return nil, fmt.Errorf("ACP manager not initialized")
	}
	return mgr.SetMode(ctx, sessionKey, mode)
}

func (g *Gateway) ACPSteer(ctx context.Context, sessionKey string, text string) (*acp.PromptResult, error) {
	mgr := acp.GetManager()
	if mgr == nil {
		return nil, fmt.Errorf("ACP manager not initialized")
	}
	return mgr.Steer(ctx, sessionKey, text, acp.PromptOptions{})
}

func (g *Gateway) ACPCancel(ctx context.Context, sessionKey string) error {
	mgr := acp.GetManager()
	if mgr == nil {
		return fmt.Errorf("ACP manager not initialized")
	}
	return mgr.Cancel(ctx, sessionKey)
}

// TriggerHeartbeat manually triggers a heartbeat check
func (g *Gateway) TriggerHeartbeat(ctx context.Context) error {
	if g.cronService == nil {
		return fmt.Errorf("cron service not running")
	}
	return g.cronService.TriggerHeartbeatNow(ctx)
}

// GetHassInfo returns Home Assistant connection status for /hass command
func (g *Gateway) GetHassInfo() *commands.HassInfo {
	if g.hassManager == nil {
		return &commands.HassInfo{Configured: false}
	}
	return &commands.HassInfo{
		Configured:    true,
		State:         g.hassManager.GetState(),
		Endpoint:      g.hassManager.GetEndpoint(),
		Uptime:        g.hassManager.GetUptime(),
		LastError:     g.hassManager.GetLastError(),
		Reconnects:    g.hassManager.GetReconnects(),
		Subscriptions: g.hassManager.SubscriptionCount(),
		Debug:         g.hassManager.IsDebug(),
	}
}

// SetHassDebug enables/disables HASS debug status messages
func (g *Gateway) SetHassDebug(enabled bool) {
	if g.hassManager != nil {
		g.hassManager.SetDebug(enabled)
	}
}

// ListHassSubscriptions returns active HASS subscriptions for /hass subs command
func (g *Gateway) ListHassSubscriptions() []commands.HassSubscriptionInfo {
	if g.hassManager == nil {
		return nil
	}
	subs := g.hassManager.GetSubscriptions()
	result := make([]commands.HassSubscriptionInfo, len(subs))
	for i, sub := range subs {
		result[i] = commands.HassSubscriptionInfo{
			ID:       sub.ID,
			Pattern:  sub.Pattern,
			Regex:    sub.Regex,
			Prompt:   sub.Prompt,
			Wake:     sub.Wake,
			Interval: sub.IntervalSeconds,
			Debounce: sub.DebounceSeconds,
			Enabled:  sub.Enabled,
		}
	}
	return result
}

// CommandHandler returns the unified command handler
func (g *Gateway) CommandHandler() *commands.Handler {
	return g.commandHandler
}

// SendStatusMessage sends a status message to all channels the user is connected to.
// Used for system notifications like failover, HASS debug status, etc.
func (g *Gateway) SendStatusMessage(ctx context.Context, u *user.User, msg string) {
	if u == nil || msg == "" {
		return
	}
	_ = g.DeliverSystemMessage(ctx, u.ID, delivery.SystemMessage{
		Kind:    delivery.SystemKindStatus,
		Source:  "status",
		Title:   "Status",
		Content: msg,
	})
}

// GetLLMProviderStatus returns the status of all LLM providers for /llm command
func (g *Gateway) GetLLMProviderStatus() *commands.LLMProviderStatusResult {
	if g.registry == nil {
		return nil
	}

	statuses := g.registry.GetProviderStatus()
	result := &commands.LLMProviderStatusResult{
		Providers:          make([]commands.LLMProviderInfo, len(statuses)),
		AgentChain:         g.registry.ListModelsForPurpose("agent"),
		SummarizationChain: g.registry.ListModelsForPurpose("summarization"),
	}

	for i, s := range statuses {
		result.Providers[i] = commands.LLMProviderInfo{
			Alias:      s.Alias,
			InCooldown: s.InCooldown,
			Until:      s.Until,
			Reason:     string(s.Reason),
			ErrorCount: s.ErrorCount,
		}
	}

	return result
}

// ResetLLMCooldowns clears all provider cooldowns for /llm reset command
func (g *Gateway) ResetLLMCooldowns() int {
	if g.registry == nil {
		return 0
	}
	return g.registry.ClearAllCooldowns()
}

// GetEmbeddingsStatus returns embeddings status for /embeddings command
func (g *Gateway) GetEmbeddingsStatus() *commands.EmbeddingsStatusResult {
	cfg := g.config.LLM.Embeddings
	if len(cfg.Models) == 0 {
		return &commands.EmbeddingsStatusResult{Configured: false}
	}

	sessionsDB := g.SessionDB()
	if sessionsDB == nil {
		return &commands.EmbeddingsStatusResult{Configured: false}
	}

	var memoryDB *sql.DB
	if g.memoryManager != nil {
		memoryDB = g.memoryManager.DB()
	}

	status, err := embeddings.GetStatus(sessionsDB, memoryDB, cfg)
	if err != nil {
		L_warn("embeddings: failed to get status", "error", err)
		return &commands.EmbeddingsStatusResult{Configured: true, PrimaryModel: cfg.Models[0]}
	}

	result := &commands.EmbeddingsStatusResult{
		Configured:             true,
		PrimaryModel:           status.PrimaryModel,
		AutoRebuild:            status.AutoRebuild,
		TranscriptTotal:        status.Transcript.TotalChunks,
		TranscriptPrimary:      status.Transcript.PrimaryModelCount,
		TranscriptNeedsRebuild: status.Transcript.NeedsRebuildCount,
		MemoryTotal:            status.Memory.TotalChunks,
		MemoryPrimary:          status.Memory.PrimaryModelCount,
		MemoryNeedsRebuild:     status.Memory.NeedsRebuildCount,
	}

	// Combine models from both tables
	modelCounts := make(map[string]int)
	for _, m := range status.Transcript.Models {
		modelCounts[m.Model] += m.Count
	}
	for _, m := range status.Memory.Models {
		modelCounts[m.Model] += m.Count
	}

	for model, count := range modelCounts {
		result.Models = append(result.Models, commands.EmbeddingsModelInfo{
			Model:     model,
			Count:     count,
			IsPrimary: model == status.PrimaryModel,
		})
	}

	return result
}

// TriggerEmbeddingsRebuild starts a background embeddings rebuild
func (g *Gateway) TriggerEmbeddingsRebuild() error {
	cfg := g.config.LLM.Embeddings
	if len(cfg.Models) == 0 {
		return fmt.Errorf("embeddings not configured")
	}

	sessionsDB := g.SessionDB()
	if sessionsDB == nil {
		return fmt.Errorf("sessions DB not available")
	}

	var memoryDB *sql.DB
	if g.memoryManager != nil {
		memoryDB = g.memoryManager.DB()
	}

	// Run in background
	go g.runEmbeddingsRebuild(context.Background(), sessionsDB, memoryDB)

	return nil
}

// buildMemoryFlushConfig builds the memory flush config from gateway config
func (g *Gateway) buildMemoryFlushConfig() *session.MemoryFlushConfig {
	if !g.config.Session.MemoryFlush.Enabled {
		return nil
	}

	thresholds := make([]session.FlushThreshold, 0, len(g.config.Session.MemoryFlush.Thresholds))
	for _, t := range g.config.Session.MemoryFlush.Thresholds {
		thresholds = append(thresholds, session.FlushThreshold{
			Percent:      t.Percent,
			Prompt:       t.Prompt,
			InjectAs:     session.FlushInjectType(t.InjectAs), // Convert string to FlushInjectType
			OncePerCycle: t.OncePerCycle,
		})
	}

	return &session.MemoryFlushConfig{
		Enabled:            g.config.Session.MemoryFlush.Enabled,
		ShowInSystemPrompt: g.config.Session.MemoryFlush.ShowInSystemPrompt,
		Thresholds:         thresholds,
	}
}

// sessionKeyFor determines the session key for a request
func (g *Gateway) sessionKeyFor(req AgentRequest) string {
	// If a specific session ID is provided (e.g., cron jobs), use it
	if req.SessionID != "" {
		return req.SessionID
	}
	if req.IsGroup {
		return fmt.Sprintf("group:%s", req.ChatID)
	}
	// Owner uses "primary" session (shared across all channels)
	if req.User != nil && req.User.IsOwner() {
		return session.PrimarySession
	}
	// Non-owner users get their own session keyed by username
	if req.User != nil {
		return fmt.Sprintf("user:%s", req.User.ID)
	}
	// Fallback (shouldn't happen - requests without user should be rejected earlier)
	return session.PrimarySession
}

// PersistConversationTurn persists a conversation turn to storage WITHOUT distributing.
// This is the pure persistence primitive - use BroadcastConversationTurn for distribution.
// Returns the enriched assistant message (with media refs resolved).
func (g *Gateway) PersistConversationTurn(ctx context.Context, params PersistParams) (enrichedAssistantMsg string, err error) {
	if params.User == nil {
		return "", fmt.Errorf("user required for conversation turn")
	}

	// Get session key using same logic as text channels
	sessionKey := g.sessionKeyFor(AgentRequest{User: params.User, Source: params.Source})
	sess := g.sessions.Get(sessionKey)

	userID := ""
	if params.User != nil {
		userID = params.User.ID
	}

	// Add user message to session (if not already added)
	if !params.SkipUserMessage && params.UserMessage != "" {
		msgID := sess.AddUserMessage(params.UserMessage, params.Source)

		if !params.Ephemeral {
			g.persistMessage(ctx, PersistMessageParams{
				MsgID:      msgID,
				SessionKey: sessionKey,
				UserID:     userID,
				Role:       "user",
				Content:    params.UserMessage,
				Source:     params.Source,
			})
		}
	}

	// Enrich media references in assistant message: {{media:path}} -> {{media:mime:'path'}}
	enrichedAssistantMsg = g.enrichMediaRefs(params.AssistantMessage)

	// Add assistant message to session
	if enrichedAssistantMsg != "" {
		msgID := sess.AddAssistantMessage(enrichedAssistantMsg)

		if !params.Ephemeral {
			g.persistMessage(ctx, PersistMessageParams{
				MsgID:      msgID,
				SessionKey: sessionKey,
				UserID:     userID,
				Role:       "assistant",
				Content:    enrichedAssistantMsg,
				Source:     params.Source,
			})
		}
	}

	L_debug("conversation turn persisted",
		"source", params.Source,
		"user", userID,
		"userMsgLen", len(params.UserMessage),
		"assistantMsgLen", len(params.AssistantMessage),
		"ephemeral", params.Ephemeral)

	return enrichedAssistantMsg, nil
}

// VoicePromptParams contains parameters for building a voice session system prompt
type VoicePromptParams struct {
	User     *user.User
	Source   string // channel name (e.g., "http_voice")
	Language string // e.g., "English", empty for default
	// MaxSentences is a hint for response length
	MaxSentences int
	// Pronunciations is a map of words to phonetic spellings
	Pronunciations map[string]string
	// AdditionalInstructions are appended to voice instructions
	AdditionalInstructions string
}

// BuildSystemPromptForVoice builds a complete system prompt for a voice session.
// It uses the full prompt (not minimal) with memory/context bulletins,
// then appends voice-specific instructions.
func (g *Gateway) BuildSystemPromptForVoice(ctx context.Context, params VoicePromptParams) string {
	// Get workspace files
	var workspaceFiles []gcontext.WorkspaceFile
	if g.promptCache != nil {
		workspaceFiles = g.promptCache.GetWorkspaceFiles()
	}

	// Get skills prompt for user
	skillsPrompt := g.GetSkillsPromptForUser(params.User, false) // voice doesn't have skills tool

	// Determine memory access and role prompts
	includeMemory := true
	var roleSystemPrompt, roleSystemPromptFile string
	if params.User != nil {
		if role, err := g.users.ResolveUserRole(params.User); err == nil && role != nil {
			includeMemory = role.HasMemoryAccess()
			roleSystemPrompt = role.SystemPrompt
			roleSystemPromptFile = role.SystemPromptFile
		}
	}

	// Get bulletins from memory graph
	var memoryBulletin, contextBulletin string
	var bulletinCfg memorygraph.BulletinConfig
	agentExtraction := false
	userID := ""
	if params.User != nil {
		userID = params.User.ID
	}

	if mgr := memorygraph.GetManager(); mgr != nil {
		agentExtraction = mgr.Config().LiveExtraction.AgentExtraction
		bulletinCfg = mgr.Config().Bulletin

		if bulletinCfg.Enabled && userID != "" {
			memoryBulletin, contextBulletin, _ = mgr.GetBulletins(ctx, userID)
			L_debug("voice: bulletin fetched",
				"user", userID,
				"memoryLen", len(memoryBulletin),
				"contextLen", len(contextBulletin))
		}
	}

	// Build base prompt params
	promptParams := gcontext.PromptParams{
		WorkspaceDir:          g.config.Gateway.WorkingDir,
		VisibleHomeDir:        sandbox.GetManager().ResolvePolicy().VisibleHomeDir,
		SandboxMode:           sandbox.GetManager().GetMode(),
		Tools:                 g.tools,
		Model:                 g.llm.Model(),
		Channel:               params.Source,
		User:                  params.User,
		TotalTokens:           0, // voice sessions don't track tokens the same way
		MaxTokens:             0,
		WorkspaceFiles:        workspaceFiles,
		SkillsPrompt:          skillsPrompt,
		IncludeMemory:         includeMemory,
		RoleSystemPrompt:      roleSystemPrompt,
		RoleSystemPromptFile:  roleSystemPromptFile,
		TimeInSystemPrompt:    g.config.PromptCache.TimeInSystemPrompt,
		AgentExtraction:       agentExtraction,
		ParallelToolBatching:  true,
		ParallelExecution:     g.config.Gateway.ToolExecution.ParallelEnabled,
		ParallelMaxConcurrent: g.parallelToolMaxConcurrent(),
		ParallelEligibleTools: g.parallelToolAllowlistNames(),
	}

	// Inject bulletins based on mode
	if bulletinCfg.MemoryInjection == "prompt" && memoryBulletin != "" {
		promptParams.MemoryBulletin = memoryBulletin
	}
	if bulletinCfg.ContextInjection == "prompt" && contextBulletin != "" {
		promptParams.ContextBulletin = contextBulletin
	}

	// Build the base system prompt
	systemPrompt := gcontext.BuildSystemPrompt(promptParams)

	// Add media instructions (voice channels use media_display tool instead of inline syntax)
	systemPrompt += g.buildMediaInstructions(MediaPromptOptions{IsVoice: true})

	// Append voice-specific instructions
	systemPrompt += buildVoiceInstructions(params)

	return systemPrompt
}

// buildVoiceInstructions creates the voice-specific prompt section
func buildVoiceInstructions(params VoicePromptParams) string {
	var sb strings.Builder
	sb.WriteString("\n\n## Voice Session\n\n")
	sb.WriteString("You are currently in a real-time voice conversation. ")
	sb.WriteString("The user is speaking to you directly through audio.\n\n")
	sb.WriteString("**Voice Guidelines:**\n")
	sb.WriteString("- Keep responses concise and natural for spoken delivery\n")
	sb.WriteString("- Avoid long lists, code blocks, or complex formatting\n")
	sb.WriteString("- Use conversational language appropriate for speaking\n")
	sb.WriteString("- Tools are implementation details - don't announce or acknowledge tool usage\n")
	sb.WriteString("- After using tools, respond naturally with the relevant information\n")
	sb.WriteString("- For visual tools like media_display, invoke the tool, with a brief caption and continue the conversation\n")

	// Language instruction
	if params.Language != "" {
		sb.WriteString(fmt.Sprintf("\n**Language:** Respond in %s.\n", params.Language))
	}

	// Response length hint
	if params.MaxSentences > 0 {
		sb.WriteString(fmt.Sprintf("\n**Response Length:** Keep responses to approximately %d sentences unless the topic requires more detail.\n", params.MaxSentences))
	}

	// Pronunciations
	if len(params.Pronunciations) > 0 {
		sb.WriteString("\n**Pronunciation Guide:**\n")
		for word, pronunciation := range params.Pronunciations {
			sb.WriteString(fmt.Sprintf("- %s: %s\n", word, pronunciation))
		}
	}

	// Additional instructions
	if params.AdditionalInstructions != "" {
		sb.WriteString("\n**Additional Instructions:**\n")
		sb.WriteString(params.AdditionalInstructions)
		sb.WriteString("\n")
	}

	return sb.String()
}

// InjectMessage injects a message into a user's session and delivers appropriately.
//
// If invokeLLM is true (guidance):
//   - Message is added as user message with configured prefix
//   - Agent run is triggered ONCE via ProcessMessage
//   - Response is fanned out to all user's channels (streaming for HTTP, batch for others)
//
// If invokeLLM is false (ghostwrite):
//   - Message is added as assistant message
//   - Delivered directly to all user's channels via DeliverGhostwrite
//
// The supervisor parameter identifies who performed the injection (for audit logging).
func (g *Gateway) InjectMessage(ctx context.Context, sessionKey, message string, invokeLLM bool, supervisor *user.User) error {
	if message == "" {
		return fmt.Errorf("empty message")
	}

	// Determine the user from session key
	var u *user.User
	if sessionKey == session.PrimarySession {
		u = g.users.Owner()
	} else if strings.HasPrefix(sessionKey, "user:") {
		userID := strings.TrimPrefix(sessionKey, "user:")
		u = g.users.Get(userID)
	} else {
		u = g.users.Owner()
	}

	if u == nil {
		return fmt.Errorf("could not determine user for session: %s", sessionKey)
	}

	// Get the session
	sess := g.sessions.Get(sessionKey)
	if sess == nil {
		return fmt.Errorf("session not found: %s", sessionKey)
	}

	// Get supervisor name for logging/events
	supervisorName := ""
	if supervisor != nil {
		supervisorName = supervisor.Name
		if supervisorName == "" {
			supervisorName = supervisor.ID
		}
	}

	L_info("gateway: inject message",
		"session", sessionKey,
		"user", u.ID,
		"invokeLLM", invokeLLM,
		"supervisor", supervisorName,
		"messageLen", len(message))

	if invokeLLM {
		// GUIDANCE: Add message to session, run agent ONCE, fan out to channels

		// Add message as user message with prefix and supervision metadata
		prefix := g.config.Supervision.Guidance.Prefix
		prefixedMessage := prefix + message
		guidanceMsgID := sess.AddSupervisionUserMessage(prefixedMessage, "guidance", supervisorName, "guidance")
		L_debug("gateway: added guidance to session", "session", sessionKey, "prefixedLen", len(prefixedMessage))

		// Persist with supervision metadata
		g.persistMessage(ctx, PersistMessageParams{
			MsgID:            guidanceMsgID,
			SessionKey:       sessionKey,
			UserID:           u.ID,
			Role:             "user",
			Content:          prefixedMessage,
			Source:           "guidance",
			Supervisor:       supervisorName,
			InterventionType: "guidance",
		})

		// Send to supervision stream so supervisor sees the guidance they sent
		if supervision := sess.GetSupervision(); supervision != nil {
			supervision.SendEvent(EventUserMessage{
				Content:    prefixedMessage,
				Source:     "guidance",
				Supervisor: supervisorName,
			})
		}

		// Run agent ONCE via ProcessMessage, fan out to all channels
		msg := &types.InboundMessage{
			SessionKey:       sessionKey,
			User:             u,
			Source:           "guidance",
			Text:             "", // Empty = message already in session
			RunAgent:         true,
			EnableThinking:   u.Thinking,
			Supervisor:       supervisor,
			InterventionType: "guidance",
			SkipMirror:       true, // We handle delivery ourselves
		}

		events := make(chan AgentEvent, 100)
		done := make(chan struct{}) // Signal when fan-out completes

		// Fan out events to channels in background
		go func() {
			defer close(done)
			var finalText string
			streamedChannels := make(map[string]bool) // Track which channels got streaming

			for event := range events {
				// Stream to channels that support it
				for name, ch := range g.channels {
					if ch.HasUser(u) && ch.StreamEvent(u, event) {
						streamedChannels[name] = true
					}
				}
				// Collect final text for batch delivery
				if e, ok := event.(EventAgentEnd); ok {
					finalText = e.FinalText
				}
			}

			// Deliver final text to batch channels (those that didn't stream)
			if finalText != "" {
				exclude := make(map[string]struct{}, len(streamedChannels))
				for name := range streamedChannels {
					exclude[name] = struct{}{}
				}
				g.deliverToUserChannels(ctx, u, exclude, func(ch Channel) error {
					return ch.DeliverAssistantMessage(ctx, u, finalText)
				})
			}
		}()

		// Run agent (blocking until complete)
		_, err := g.ProcessMessage(ctx, msg, events)

		// Wait for fan-out goroutine to finish all deliveries
		<-done

		if err != nil {
			L_error("gateway: guidance agent run failed", "session", sessionKey, "error", err)
			return err
		}

		L_info("gateway: guidance complete", "session", sessionKey)

	} else {
		// GHOSTWRITE: Add message to session, deliver directly to channels

		// Add message as assistant message with supervision metadata
		ghostwriteMsgID := sess.AddSupervisionAssistantMessage(message, supervisorName, "ghostwrite")
		L_debug("gateway: added ghostwrite to session", "session", sessionKey, "messageLen", len(message))

		// Persist with supervision metadata
		g.persistMessage(ctx, PersistMessageParams{
			MsgID:            ghostwriteMsgID,
			SessionKey:       sessionKey,
			UserID:           u.ID,
			Role:             "assistant",
			Content:          message,
			Source:           "ghostwrite",
			Supervisor:       supervisorName,
			InterventionType: "ghostwrite",
		})

		// Send to supervision stream so supervisor sees the ghostwrite they sent
		if supervision := sess.GetSupervision(); supervision != nil {
			supervision.SendEvent(EventUserMessage{
				Content:    message,
				Source:     "ghostwrite",
				Supervisor: supervisorName,
			})
		}

		// Deliver to all channels via DeliverGhostwrite
		delivered := 0
		for name, ch := range g.channels {
			if !ch.HasUser(u) {
				continue
			}
			L_debug("gateway: ghostwriting to channel", "channel", name, "user", u.ID)
			if err := ch.DeliverGhostwrite(ctx, u, message); err != nil {
				L_error("gateway: ghostwrite delivery failed", "channel", name, "user", u.ID, "error", err)
			} else {
				delivered++
			}
		}
		L_info("gateway: ghostwrite complete", "session", sessionKey, "channels", delivered)
	}

	return nil
}

// Sessions returns info about all sessions
func (g *Gateway) Sessions() []session.SessionInfo {
	return g.sessions.List()
}

// History returns the messages for a specific session
func (g *Gateway) History(sessionID string) ([]session.Message, error) {
	messages, ok := g.sessions.History(sessionID)
	if !ok {
		return nil, fmt.Errorf("session not found: %s", sessionID)
	}
	return messages, nil
}

// ResetSession clears a session
func (g *Gateway) ResetSession(sessionID string) error {
	if !g.sessions.Reset(sessionID) {
		return fmt.Errorf("session not found: %s", sessionID)
	}
	return nil
}

// StopAllUserSessions cancels all running agent sessions for a user.
func (g *Gateway) StopAllUserSessions(userID string) (int, error) {
	cancelled := g.sessions.CancelAllForUser(userID)
	if cancelled > 0 {
		L_info("gateway: emergency stop", "user", userID, "cancelled", cancelled)
	} else {
		L_info("gateway: emergency stop set pause latch", "user", userID)
	}
	return cancelled, nil
}

// ResumeAllUserSessions clears STOP pause latches for a user.
func (g *Gateway) ResumeAllUserSessions(userID string) (int, error) {
	resumed := g.sessions.ResumeAllForUser(userID)
	L_info("gateway: resume requested", "user", userID, "resumed", resumed)
	return resumed, nil
}

// RequestShutdown requests graceful process shutdown (owner-only).
func (g *Gateway) RequestShutdown(userID string) error {
	owner := g.users.Owner()
	if owner == nil || owner.ID != userID {
		return fmt.Errorf("shutdown denied: owner only")
	}
	L_warn("gateway: shutdown requested", "user", userID)
	go func() {
		time.Sleep(200 * time.Millisecond)
		proc, err := os.FindProcess(os.Getpid())
		if err != nil {
			L_error("gateway: failed to find current process for shutdown", "error", err)
			return
		}
		if err := proc.Signal(os.Interrupt); err != nil {
			L_error("gateway: failed to signal shutdown", "error", err)
		}
	}()
	return nil
}

// CleanOrphanedToolMessages deletes orphaned tool_use/tool_result messages from a session
func (g *Gateway) CleanOrphanedToolMessages(ctx context.Context, sessionKey string) (int, error) {
	return g.sessions.CleanOrphanedToolMessages(ctx, sessionKey)
}

// Health returns the gateway health status
func (g *Gateway) Health() HealthStatus {
	return HealthStatus{
		Status:       "healthy",
		SessionCount: g.sessions.Count(),
		UserCount:    g.users.Count(),
		Uptime:       int64(time.Since(g.startTime).Seconds()),
	}
}

// Users returns the user registry
func (g *Gateway) Users() *user.Registry {
	return g.users
}

// SessionManager returns the session manager
func (g *Gateway) SessionManager() *session.Manager {
	return g.sessions
}

// RunAgentForSession triggers an agent run for a specific session.
// Used by supervision to trigger agent response after guidance injection.
// The session should already have the message to respond to.
func (g *Gateway) RunAgentForSession(ctx context.Context, sessionKey string, events chan<- AgentEvent) error {
	L_debug("RunAgentForSession: called", "session", sessionKey)

	// Get the session to verify it exists
	sess := g.sessions.Get(sessionKey)
	if sess == nil {
		L_error("RunAgentForSession: session not found", "session", sessionKey)
		close(events)
		return fmt.Errorf("session not found: %s", sessionKey)
	}
	L_debug("RunAgentForSession: session found", "session", sessionKey, "messageCount", sess.MessageCount())

	// Determine the user for this session
	var reqUser *user.User
	if sessionKey == session.PrimarySession {
		// Primary session belongs to owner
		reqUser = g.users.Owner()
		L_debug("RunAgentForSession: using owner for primary session", "session", sessionKey)
	} else if strings.HasPrefix(sessionKey, "user:") {
		// User session - extract user ID and look up
		userID := strings.TrimPrefix(sessionKey, "user:")
		reqUser = g.users.Get(userID)
		L_debug("RunAgentForSession: looked up user", "session", sessionKey, "userID", userID, "found", reqUser != nil)
	} else if strings.HasPrefix(sessionKey, "group:") {
		// Group session - use owner for now (groups need special handling)
		reqUser = g.users.Owner()
		L_debug("RunAgentForSession: using owner for group session", "session", sessionKey)
	} else {
		// Unknown format - use owner
		reqUser = g.users.Owner()
		L_debug("RunAgentForSession: unknown session format, using owner", "session", sessionKey)
	}

	if reqUser == nil {
		L_error("RunAgentForSession: could not determine user", "session", sessionKey)
		close(events)
		return fmt.Errorf("could not determine user for session: %s", sessionKey)
	}

	L_info("supervision: triggering agent run", "session", sessionKey, "user", reqUser.ID)

	// Clear any stale interrupt flag before starting new run
	// (The interrupt was meant for a previous generation, not this new one)
	if supervision := sess.GetSupervision(); supervision != nil {
		if supervision.HasInterruptRequest() {
			L_debug("RunAgentForSession: cleared stale interrupt flag", "session", sessionKey)
		}
	}

	// Create agent request - message already in session, skip adding
	req := AgentRequest{
		User:           reqUser,
		Source:         "supervision",
		SessionID:      sessionKey,
		SkipAddMessage: true, // Message already added by supervision
	}
	L_debug("RunAgentForSession: about to call RunAgent", "session", sessionKey, "user", reqUser.ID, "skipAddMessage", req.SkipAddMessage)

	err := g.RunAgent(ctx, req, events)
	if err != nil {
		L_error("RunAgentForSession: RunAgent returned error", "session", sessionKey, "error", err)
	} else {
		L_debug("RunAgentForSession: RunAgent completed", "session", sessionKey)
	}
	return err
}

// SessionDB returns the SQLite database for the session store, or nil if not using SQLite
func (g *Gateway) SessionDB() *sql.DB {
	if g.sessions == nil {
		return nil
	}
	store := g.sessions.GetStore()
	if store == nil {
		return nil
	}
	// Type assert to SQLiteStore to get DB
	if sqliteStore, ok := store.(*session.SQLiteStore); ok {
		return sqliteStore.DB()
	}
	return nil
}

// PersistMessageParams contains parameters for persisting a message to SQLite.
type PersistMessageParams struct {
	MsgID            string // Message ID (empty = auto-generate)
	SessionKey       string
	UserID           string
	Role             string // "user", "assistant", "tool_use", "tool_result"
	Content          string
	Phase            string // Assistant phase metadata ("commentary", "final_answer")
	Source           string
	ToolCallID       string
	ToolName         string
	ToolInput        []byte
	ToolError        string
	Thinking         string
	Supervisor       string
	InterventionType string
	ResponseGroupID  string // Groups tool calls from same LLM response
}

// persistMessage writes a message to SQLite storage for audit trail.
// If MsgID is empty, generates a new ID (for transcript-only entries without session Add*).
func (g *Gateway) persistMessage(ctx context.Context, p PersistMessageParams) {
	store := g.sessions.GetStore()
	if store == nil {
		return // No store configured
	}

	msgID := p.MsgID
	if msgID == "" {
		msgID = session.GenerateMessageID()
	}

	msg := &session.StoredMessage{
		ID:               msgID,
		SessionKey:       p.SessionKey,
		Timestamp:        time.Now(),
		Role:             p.Role,
		Content:          p.Content,
		Phase:            p.Phase,
		Source:           p.Source,
		UserID:           p.UserID,
		ToolCallID:       p.ToolCallID,
		ToolName:         p.ToolName,
		ToolInput:        p.ToolInput,
		Thinking:         p.Thinking,
		Supervisor:       p.Supervisor,
		InterventionType: p.InterventionType,
		ResponseGroupID:  p.ResponseGroupID,
	}

	// For tool_result, store the result in ToolResult field and mark errors
	if p.Role == "tool_result" {
		sanitized := contentguard.ToolResultText(p.Content)
		if sanitized.Changed {
			L_warn("persistMessage: sanitized tool_result",
				"reason", sanitized.Reason,
				"mime", sanitized.MIME,
				"bytes", sanitized.OriginalBytes)
		}
		msg.ToolResult = sanitized.Text // Store sanitized result
		msg.Content = ""                // Keep content empty for tool results
		if p.ToolError != "" {
			msg.ToolIsError = true
		}
	}

	if err := store.AppendMessage(ctx, p.SessionKey, msg); err != nil {
		L_warn("failed to persist message to SQLite", "role", p.Role, "error", err)
	} else {
		L_trace("message persisted to SQLite", "role", p.Role, "toolName", p.ToolName, "supervisor", p.Supervisor)
	}
}

// injectEphemeralBeforeLastUser inserts ephemeral messages just before the last
// user message. This placement maximizes prompt caching efficiency by keeping
// the conversation history (cacheable prefix) intact, while placing dynamic
// content (bulletins, context updates) near the current turn.
func injectEphemeralBeforeLastUser(messages []types.Message, ephemeral ...types.Message) []types.Message {
	if len(ephemeral) == 0 {
		return messages
	}

	// Find the last user message index
	lastUserIdx := -1
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			lastUserIdx = i
			break
		}
	}

	// If no user message found, append ephemeral messages at the end
	if lastUserIdx == -1 {
		result := make([]types.Message, 0, len(messages)+len(ephemeral))
		result = append(result, messages...)
		result = append(result, ephemeral...)
		return result
	}

	// Insert ephemeral messages just before the last user message
	result := make([]types.Message, 0, len(messages)+len(ephemeral))
	result = append(result, messages[:lastUserIdx]...)
	result = append(result, ephemeral...)
	result = append(result, messages[lastUserIdx:]...)
	return result
}

// formatUptime returns a human-readable uptime string like "2d 5h" or "45m"
func formatUptime(d time.Duration) string {
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	minutes := int(d.Minutes()) % 60

	if days > 0 {
		return fmt.Sprintf("%dd %dh", days, hours)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, minutes)
	}
	return fmt.Sprintf("%dm", minutes)
}

// extractMessageToolText extracts the sent text from a message tool's input JSON.
// Returns empty string if the action isn't "send" or no message text is found.
func extractMessageToolText(toolInput []byte) string {
	var input struct {
		Action  string `json:"action"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(toolInput, &input); err != nil {
		return ""
	}
	if input.Action != "send" {
		return ""
	}
	return input.Message
}

// resolveThinkingLevel determines the effective thinking level based on priority hierarchy:
// Request Override > User Preference > Provider Default > Global Default
// Note: Request.ThinkingLevel is set by channel handlers based on per-session preferences
func (g *Gateway) resolveThinkingLevel(req AgentRequest, providerName string) llm.ThinkingLevel {
	var level llm.ThinkingLevel

	// 1. Request override (set via /thinking command by channel handler)
	if req.ThinkingLevel != "" {
		L_trace("thinking: using request override", "level", req.ThinkingLevel)
		level = llm.ParseThinkingLevel(req.ThinkingLevel)
	} else if req.User != nil && req.User.ThinkingLevel != "" {
		// 2. User preference
		L_trace("thinking: using user preference", "user", req.User.ID, "level", req.User.ThinkingLevel)
		level = llm.ParseThinkingLevel(req.User.ThinkingLevel)
	} else if providerName != "" {
		// 3. Provider default (from config)
		if providerCfg, ok := g.config.LLM.Providers[providerName]; ok && providerCfg.ThinkingLevel != "" {
			L_trace("thinking: using provider default", "provider", providerName, "level", providerCfg.ThinkingLevel)
			level = llm.ParseThinkingLevel(providerCfg.ThinkingLevel)
		}
	}
	if level == "" && g.config.LLM.Thinking.DefaultLevel != "" {
		// 4. Global default from config
		L_trace("thinking: using global default", "level", g.config.LLM.Thinking.DefaultLevel)
		level = llm.ParseThinkingLevel(g.config.LLM.Thinking.DefaultLevel)
	}
	if level == "" {
		// 5. Fallback to hardcoded default
		L_trace("thinking: using hardcoded default", "level", llm.DefaultThinkingLevel)
		level = llm.DefaultThinkingLevel
	}

	// Normalize contradictory state: thinking toggle on but resolved level is disabled.
	// This can happen when old user/session state has showThinking=true + thinkingLevel=off.
	if req.EnableThinking && !level.IsEnabled() {
		L_warn("thinking: enable requested with disabled level; normalizing to default",
			"requestedLevel", level.String(),
			"fallbackLevel", llm.DefaultThinkingLevel.String())
		return llm.DefaultThinkingLevel
	}
	return level
}

// MediaPromptOptions configures channel-specific media prompt generation.
type MediaPromptOptions struct {
	IsVoice bool // Voice channel (real-time audio) - uses media_display tool instead of inline syntax
}

// buildMediaInstructions returns media instructions appropriate for the channel type.
func (g *Gateway) buildMediaInstructions(opts MediaPromptOptions) string {
	if g.mediaStore == nil {
		return ""
	}

	if opts.IsVoice {
		return g.buildVoiceMediaInstructions()
	}
	return g.buildTextMediaInstructions()
}

// buildTextMediaInstructions returns media instructions for text-based channels.
// Text channels use inline {{media:path}} syntax for conversational media embedding.
func (g *Gateway) buildTextMediaInstructions() string {
	return fmt.Sprintf(`

## Media Storage

Media root: %s

**IMPORTANT:** When saving images, screenshots, or media files:
- **ALWAYS** save to media/ subdirectories, NEVER /tmp/ or /var/tmp/
- /tmp/ is sandboxed - files saved there cannot be accessed for inline display

Subdirectory mapping:
- camera/      - camera/security captures
- browser/     - browser screenshots
- screenshots/ - general screenshots, screen captures
- inbound/     - user-uploaded media (Telegram photos, HTTP paste)
- generated/   - AI-generated images
- downloads/   - downloaded files (default fallback if path isn't obvious)

When saving media, use appropriate subdirectory. If unsure, use downloads/.

## Media References

Two ways to send media:

1. **Inline (preferred):** Write {{media:path}} in your response
   - Goes to whoever you're talking to
   - Gateway enriches with mimetype, channels render appropriately
   - Example: Here's the screenshot: {{media:screenshots/desktop.png}}

2. **Message tool (explicit):** Two options:
   - Simple: {"action":"send", "filePath":"screenshots/file.png", "caption":"optional"}
   - Mixed content: {"action":"send", "content":[{"type":"text","text":"Before"},{"type":"media","path":"screenshots/file.png"},{"type":"text","text":"After"}]}
   - Use for specific channel/chat targeting, programmatic sends, delivery confirmation

Prefer inline {{media:}} for conversational flow. Use message tool for explicit sends.
`, g.mediaStore.BaseDir())
}

// buildVoiceMediaInstructions returns media instructions for voice channels.
// Voice channels use the media_display tool to avoid vocalizing {{media:}} syntax.
func (g *Gateway) buildVoiceMediaInstructions() string {
	return fmt.Sprintf(`

## Media Display

Media root: %s

Subdirectory mapping:
- camera/      - camera/security captures
- browser/     - browser screenshots
- screenshots/ - general screenshots, screen captures
- inbound/     - user-uploaded media (Telegram photos, HTTP paste)
- generated/   - AI-generated images
- downloads/   - downloaded files (default fallback if path isn't obvious)

To show images, screenshots, or media to the user:
- Use the media_display tool with the file path
- The media appears on the user's screen silently while you continue speaking
- Do NOT mention file paths or technical syntax in your speech
- Example: call media_display(path="camera/driveway.jpg"), then say "Here's the driveway view"

Do NOT use {{media:path}} syntax in voice mode - it will be spoken aloud.
`, g.mediaStore.BaseDir())
}

// enrichMediaRefs finds {{media:path}} in text and enriches to {{media:mime:'path'}}
// Also validates that files exist and marks missing files with error mime type.
func (g *Gateway) enrichMediaRefs(text string) string {
	if g.mediaStore == nil {
		L_debug("media: enrichMediaRefs skipped - no media store")
		return text
	}

	// Pattern: {{media:path}} where path doesn't contain }, ', or :
	// This is the simple form the agent writes
	pattern := regexp.MustCompile(`\{\{media:([^}'":]+)\}\}`)

	if !pattern.MatchString(text) {
		return text
	}
	L_debug("media: enriching refs in text")

	return pattern.ReplaceAllStringFunc(text, func(match string) string {
		// Extract path from match
		submatch := pattern.FindStringSubmatch(match)
		if len(submatch) < 2 {
			return match
		}
		path := strings.TrimSpace(submatch[1])

		// Skip conversational/example uses - require path to look like a file
		// Must contain '/' (subdirectory) or '.' (file extension)
		if !strings.Contains(path, "/") && !strings.Contains(path, ".") {
			L_trace("media: skipping non-file-like path (conversational)", "path", path)
			return match // leave unchanged
		}

		// Resolve to absolute path
		absPath, err := media.ResolveMediaPath(g.mediaStore.BaseDir(), path)
		if err != nil {
			L_trace("media: skipping invalid path", "path", path, "error", err)
			return match // leave unchanged for conversational use
		}

		// Check if file exists - if not, leave unchanged (might be conversational)
		if !media.FileExists(absPath) {
			L_trace("media: file not found, leaving unchanged", "path", path)
			return match
		}

		// Detect mimetype
		mimeType, err := media.DetectMimeType(absPath)
		if err != nil {
			L_warn("media: failed to detect mimetype", "path", path, "error", err)
			mimeType = "application/octet-stream"
		}

		L_debug("media: enriched ref", "path", path, "mime", mimeType)
		return fmt.Sprintf("{{media:%s:'%s'}}", mimeType, media.EscapePath(path))
	})
}
