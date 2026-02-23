package gateway

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/roelfdiedericks/goclaw/internal/commands"
	"github.com/roelfdiedericks/goclaw/internal/config"
	gcontext "github.com/roelfdiedericks/goclaw/internal/context"
	"github.com/roelfdiedericks/goclaw/internal/cron"
	"github.com/roelfdiedericks/goclaw/internal/embeddings"
	"github.com/roelfdiedericks/goclaw/internal/hass"
	"github.com/roelfdiedericks/goclaw/internal/llm"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/media"
	"github.com/roelfdiedericks/goclaw/internal/memory"
	"github.com/roelfdiedericks/goclaw/internal/paths"
	"github.com/roelfdiedericks/goclaw/internal/session"
	"github.com/roelfdiedericks/goclaw/internal/skills"
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

// Channel is the interface for messaging channels (TUI, Telegram, etc.)
type Channel interface {
	Name() string
	Send(ctx context.Context, msg string) error
	SendMirror(ctx context.Context, source, userMsg, response string) error
	HasUser(u *user.User) bool

	// StreamEvent streams a single agent event to the user (for real-time updates).
	// Returns true if the channel supports streaming and delivered the event.
	// Batch-only channels (Telegram, TUI) should return false.
	StreamEvent(u *user.User, event AgentEvent) bool

	// DeliverGhostwrite sends a ghostwritten message with appropriate UX
	// (typing indicator, delay, etc.). Used for supervision ghostwriting.
	DeliverGhostwrite(ctx context.Context, u *user.User, message string) error
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
		StoreType:   storeType,
		StorePath:   cfg.Session.StorePath,
		SessionsDir: cfg.Session.InheritPath, // For OpenClaw inheritance
		InheritFrom: cfg.Session.InheritFrom,
		WorkingDir:  cfg.Gateway.WorkingDir,
	}

	g.sessions, err = session.NewManagerWithConfig(managerCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create session manager: %w", err)
	}
	L_info("session: storage backend ready",
		"store", storeType,
		"path", cfg.Session.StorePath)

	// Inherit from OpenClaw session if configured
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
		Dir:     mediaDir,
		TTL:     cfg.Media.TTL,
		MaxSize: cfg.Media.MaxSize,
	})
	if err != nil {
		L_warn("failed to create media store", "error", err)
	} else {
		g.mediaStore = mediaStore
		L_info("media: store initialized",
			"dir", mediaDir,
			"ttl", cfg.Media.TTL,
			"maxSize", cfg.Media.MaxSize)
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
	L_debug("command handler initialized")

	// Initialize skill manager (skills are config - load early)
	if cfg.Skills.Enabled {
		// Build skill configs map
		skillConfigs := make(map[string]*skills.SkillEntryConfig)
		for name, entry := range cfg.Skills.Entries {
			skillConfigs[name] = &skills.SkillEntryConfig{
				Enabled: entry.Enabled,
				APIKey:  entry.APIKey,
				Env:     entry.Env,
				Config:  entry.Config,
			}
		}

		skillMgrCfg := skills.ManagerConfig{
			Enabled:       cfg.Skills.Enabled,
			BundledDir:    cfg.Skills.BundledDir,
			ManagedDir:    cfg.Skills.ManagedDir,
			WorkspaceDir:  cfg.Skills.WorkspaceDir,
			ExtraDirs:     cfg.Skills.ExtraDirs,
			WatchEnabled:  cfg.Skills.Watch,
			WatchDebounce: cfg.Skills.WatchDebounce,
			SkillConfigs:  skillConfigs,
		}

		// Set default workspace skills dir if not overridden
		if skillMgrCfg.WorkspaceDir == "" {
			skillMgrCfg.WorkspaceDir = filepath.Join(cfg.Gateway.WorkingDir, "skills")
		}

		// Set default managed skills dir based on workspace location
		// If running side-by-side with OpenClaw, use ~/.openclaw/skills
		// Otherwise use ~/.goclaw/skills
		if skillMgrCfg.ManagedDir == "" {
			skillMgrCfg.ManagedDir, _ = paths.ContextualDataPath("skills", cfg.Gateway.WorkingDir)
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

// MediaStore returns the media store
func (g *Gateway) MediaStore() *media.MediaStore {
	return g.mediaStore
}

// MemoryManager returns the memory manager
func (g *Gateway) MemoryManager() *memory.Manager {
	return g.memoryManager
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

// GetSkillsPromptForUser returns the formatted skills section filtered by user's role
func (g *Gateway) GetSkillsPromptForUser(u *user.User) string {
	if g.skillManager == nil {
		return ""
	}
	return g.skillManager.FormatPromptForUser(u, g.users.GetRolesConfig())
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

	result := &commands.SkillsListResult{
		Total:       len(allSkills),
		Eligible:    len(eligibleSkills) - whitelistedCount, // Don't double-count whitelisted
		Ineligible:  len(allSkills) - len(eligibleSkills) - len(flaggedSkills),
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
			// Manually enabled despite audit flags
			info.Status = "whitelisted"
			if len(s.AuditFlags) > 0 {
				info.Reason = s.AuditFlags[0].Pattern
			}
		} else if flaggedSet[s.Name] {
			info.Status = "flagged"
			if len(s.AuditFlags) > 0 {
				info.Reason = s.AuditFlags[0].Pattern
			}
		} else if eligibleSet[s.Name] {
			info.Status = "ready"
		} else {
			info.Status = "ineligible"
			// Get reason for ineligibility
			missing := s.GetMissingRequirements(skills.EligibilityContext{})
			if len(missing) > 0 {
				info.Reason = missing[0]
			}
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

			for _, ch := range g.channels {
				if err := ch.SendMirror(ctx, "openclaw", userMsg, content); err != nil {
					L_debug("session: mirror send failed", "channel", ch.Name(), "error", err)
				}
			}
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
	if !cfg.GetAutoRebuild() {
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

	// Determine cron paths based on workspace location
	// If running side-by-side with OpenClaw, use ~/.openclaw/cron/
	// Otherwise use ~/.goclaw/cron/
	cronJobsPath, _ := paths.ContextualDataPath("cron/jobs.json", g.config.Gateway.WorkingDir)
	cronRunsDir, _ := paths.ContextualDataPath("cron/runs", g.config.Gateway.WorkingDir)

	// Create store with resolved paths
	store := cron.NewStore(cronJobsPath, cronRunsDir)

	// Create and start service
	g.cronService = cron.NewService(store, g)

	// Set up channel provider for delivery
	g.cronService.SetChannelProvider(&gatewayCronChannelProvider{g: g})

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

// gatewayCronChannelProvider wraps gateway channels for cron delivery.
type gatewayCronChannelProvider struct {
	g *Gateway
}

func (p *gatewayCronChannelProvider) Channels() map[string]cron.Channel {
	result := make(map[string]cron.Channel)
	for name, ch := range p.g.channels {
		result[name] = &cronChannelAdapter{ch: ch}
	}
	return result
}

// cronChannelAdapter wraps a gateway.Channel as a cron.Channel.
type cronChannelAdapter struct {
	ch Channel
}

func (a *cronChannelAdapter) Name() string {
	return a.ch.Name()
}

func (a *cronChannelAdapter) Send(ctx context.Context, msg string) error {
	return a.ch.Send(ctx, msg)
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
		for _, ch := range g.channels {
			ch.Send(ctx, statusMsg) //nolint:errcheck // fire-and-forget status broadcast
		}
	}

	// Convert cron request to gateway request
	req := AgentRequest{
		Source:         cronReq.Source,
		UserMsg:        cronReq.UserMsg,
		SessionID:      cronReq.SessionID,
		FreshContext:   cronReq.FreshContext,
		User:           reqUser,
		IsHeartbeat:    cronReq.IsHeartbeat,
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
		sess.AddUserMessage(msg.Text, msg.Source)
		addedMessage = true
		L_debug("gateway: ProcessMessage added user message", "session", sessionKey, "source", msg.Source, "textLen", len(msg.Text))

		// Persist the message
		g.persistMessage(ctx, sessionKey, "user", msg.Text, msg.Source, "", "", nil, "", "", "", "")
	}

	// Build AgentRequest
	req := AgentRequest{
		User:           msg.User,
		Source:         msg.Source,
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

		// Then central suppression (HEARTBEAT_OK, SILENT_OK, etc.)
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
			for name, ch := range g.channels {
				if !ch.HasUser(msg.User) {
					continue
				}
				result := types.DeliveryResult{Channel: name}
				if err := ch.Send(ctx, finalText); err != nil {
					L_error("gateway: ProcessMessage delivery failed", "channel", name, "error", err)
					result.Error = err.Error()
				} else {
					result.Success = true
					L_debug("gateway: ProcessMessage delivered", "channel", name)
				}
				report.Results = append(report.Results, result)
			}
		}

		return report, nil
	}
}

// InvokeAgent implements types.EventInjector interface.
// It runs the agent with a message and delivers the response to channels.
// Uses owner user and primary session.
func (g *Gateway) InvokeAgent(ctx context.Context, source, message, suppressOn string) error {
	u := g.users.Owner()
	if u == nil {
		return fmt.Errorf("no owner user configured")
	}

	msg := types.NewInboundMessage(source, u, message)
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

	if g.memoryManager != nil {
		g.memoryManager.Close() //nolint:errcheck // shutdown cleanup
	}

	if g.sessions != nil {
		g.sessions.Close() //nolint:errcheck // shutdown cleanup
	}
}

// suppressionTokens are response markers indicating agent has nothing meaningful to deliver.
// If a response contains any of these, it should not be sent to the user.
var suppressionTokens = []string{
	"SILENT_OK",    // Agent has nothing to say (regular chat)
	"HEARTBEAT_OK", // Heartbeat/cron - nothing needs attention
	"NO_REPLY",     // Memory flush - nothing to save
	"EVENT_OK",     // HASS event - no action needed
}

// shouldSuppressResponse returns true if the response contains any suppression token.
// Agents are instructed to use these tokens as the ENTIRE response, but often add
// extra text. Using Contains catches "blah blah HEARTBEAT_OK" patterns.
func shouldSuppressResponse(response string) bool {
	upper := strings.ToUpper(response)
	for _, token := range suppressionTokens {
		if strings.Contains(upper, token) {
			return true
		}
	}
	return false
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
	sessionKey := g.sessionKeyFor(req)

	// Get or create session first so we can check supervision
	var sess *session.Session
	if req.FreshContext {
		sess = g.sessions.GetFresh(sessionKey)
	} else {
		sess = g.sessions.Get(sessionKey)
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

	sendEvent(EventAgentStart{
		RunID:      runID,
		Source:     req.Source,
		SessionKey: sessionKey,
	})

	// Ensure session has the model's context window size set
	if sess.GetMaxTokens() == 0 && g.llm != nil {
		sess.SetMaxTokens(g.llm.ContextTokens())
	}

	// For heartbeat: snapshot message count so we can rollback after (ephemeral)
	messageCountBefore := 0
	if req.IsHeartbeat {
		messageCountBefore = sess.MessageCount()
	}

	// Add user message with content blocks if any (skip if already added by supervision)
	if !req.SkipAddMessage {
		L_debug("RunAgent: adding user message", "session", sessionKey, "source", req.Source, "msgLen", len(req.UserMsg))
		if len(req.ContentBlocks) > 0 {
			sess.AddUserMessageWithContent(req.UserMsg, req.Source, req.ContentBlocks)
		} else {
			sess.AddUserMessage(req.UserMsg, req.Source)
		}

		// Send user message to supervision if active
		if supervision := sess.GetSupervision(); supervision != nil {
			supervision.SendEvent(EventUserMessage{Content: req.UserMsg, Source: req.Source})
		}

		// Persist user message to SQLite (skip for heartbeat - ephemeral)
		if !req.IsHeartbeat {
			g.persistMessage(ctx, sessionKey, "user", req.UserMsg, req.Source, "", "", nil, "", "", "", "")
		}
	} else {
		L_debug("RunAgent: skipping message add (already in session)", "session", sessionKey, "source", req.Source)
	}

	// Build system prompt
	var workspaceFiles []gcontext.WorkspaceFile
	if g.promptCache != nil {
		workspaceFiles = g.promptCache.GetWorkspaceFiles()
	}

	// Get skills prompt (filtered by user's role)
	skillsPrompt := g.GetSkillsPromptForUser(req.User)

	// Determine memory access and role prompts from resolved role
	includeMemory := true
	var roleSystemPrompt, roleSystemPromptFile string
	if resolvedRole, err := g.users.ResolveUserRole(req.User); err == nil {
		includeMemory = resolvedRole.HasMemoryAccess()
		roleSystemPrompt = resolvedRole.SystemPrompt
		roleSystemPromptFile = resolvedRole.SystemPromptFile
	}

	systemPrompt := gcontext.BuildSystemPrompt(gcontext.PromptParams{
		WorkspaceDir:         g.config.Gateway.WorkingDir,
		Tools:                g.tools,
		Model:                g.llm.Model(),
		Channel:              req.Source,
		User:                 req.User,
		TotalTokens:          sess.GetTotalTokens(),
		MaxTokens:            sess.GetMaxTokens(),
		WorkspaceFiles:       workspaceFiles,
		SkillsPrompt:         skillsPrompt,
		IncludeMemory:        includeMemory,
		RoleSystemPrompt:     roleSystemPrompt,
		RoleSystemPromptFile: roleSystemPromptFile,
	})

	// Append media storage instructions
	systemPrompt += g.buildMediaInstructions()

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

	// Create cancellable context for supervision interrupt support
	agentCtx, agentCancel := context.WithCancel(ctx)
	defer agentCancel()

	// Store cancel function if supervised (for interrupt support)
	if supervision := sess.GetSupervision(); supervision != nil {
		supervision.SetCancelFunc(agentCancel)
		defer supervision.ClearCancelFunc()
	}

	var finalText string
	const maxOverflowRetries = 2 // Max times to retry after compaction

	// Agent loop - keep going until no more tool use
	for {
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

		L_debug("invoking LLM",
			"provider", g.llm.Name(),
			"model", g.llm.Model(),
			"messages", len(messages),
			"tools", len(toolDefs),
			"contextTokens", contextTokens,
			"contextWindow", contextWindow,
			"contextUsage", fmt.Sprintf("%.1f%%", contextUsage),
			"thinking", enableThinking,
			"thinkingLevel", thinkingLevel,
		)

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
					sendEvent(EventToolEnd{RunID: runID, ToolName: name, ToolID: "", Result: result, Error: errMsg})
				}
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

		var response *llm.Response
		var failoverResult *llm.FailoverResult
		var llmErr error
		for retry := 0; retry <= maxOverflowRetries; retry++ {
			failoverResult, llmErr = g.registry.StreamMessageWithFailover(
				agentCtx,
				"agent",
				stateAccessor,
				messages,
				toolDefs,
				systemPrompt,
				func(delta string) {
					sendEvent(EventTextDelta{RunID: runID, Delta: delta})
				},
				streamOpts,
			)

			if llmErr == nil {
				response = failoverResult.Response
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

		// Handle tool use
		if response.HasToolUse() {
			// Check permissions
			if !req.User.CanUseTool(response.ToolName) {
				result := fmt.Sprintf("Permission denied: %s cannot use tool %s", req.User.Name, response.ToolName)
				sendEvent(EventToolEnd{
					RunID:    runID,
					ToolName: response.ToolName,
					ToolID:   response.ToolUseID,
					Result:   result,
					Error:    "permission_denied",
				})
				sess.AddToolUse(response.ToolUseID, response.ToolName, response.ToolInput, response.Thinking)
				sess.AddToolResult(response.ToolUseID, result)
				// Persist denied tool use/result (skip for heartbeat - ephemeral)
				if !req.IsHeartbeat {
					g.persistMessage(ctx, sessionKey, "tool_use", "", req.Source, response.ToolUseID, response.ToolName, response.ToolInput, "", response.Thinking, "", "")
					g.persistMessage(ctx, sessionKey, "tool_result", result, req.Source, response.ToolUseID, "", nil, "", "", "", "")
				}
				continue
			}

			sendEvent(EventToolStart{
				RunID:    runID,
				ToolName: response.ToolName,
				ToolID:   response.ToolUseID,
				Input:    response.ToolInput,
			})

			// Execute tool with session context
			toolStartTime := time.Now()
			ownerChatID := ""
			if owner := g.users.Owner(); owner != nil {
				ownerChatID = owner.TelegramID
			}
			// Get transcript scope from resolved role
			transcriptScope := "own" // Default to restrictive
			if resolvedRole, err := g.users.ResolveUserRole(req.User); err == nil {
				transcriptScope = resolvedRole.GetTranscriptScope()
			}
			toolCtx := tools.WithSessionContext(ctx, &tools.SessionContext{
			Channel:         req.Source,
			ChatID:          req.ChatID,
			OwnerChatID:     ownerChatID,
			User:            req.User,
			TranscriptScope: transcriptScope,
			Session:         sess,
		})
		toolResult, err := g.tools.Execute(toolCtx, response.ToolName, response.ToolInput)
		toolDuration := time.Since(toolStartTime)

		errStr := ""
		if err != nil {
			errStr = err.Error()
			toolResult = types.ErrorResult(err.Error())
		}

		// Get text content for downstream processing
		resultText := toolResult.GetText()

		// Check for media in tool output
		if req.OnMediaToSend != nil {
			parseResult := media.SplitMediaFromOutput(resultText)
			resultText = parseResult.Text
			for _, mediaPath := range parseResult.MediaURLs {
				if mediaErr := req.OnMediaToSend(mediaPath, ""); mediaErr != nil {
					L_warn("failed to send media", "path", mediaPath, "error", mediaErr)
				}
			}
		}

		sendEvent(EventToolEnd{
			RunID:      runID,
			ToolName:   response.ToolName,
			ToolID:     response.ToolUseID,
			Result:     resultText,
			Error:      errStr,
			DurationMs: toolDuration.Milliseconds(),
		})

		// Add to session and continue loop
		sess.AddToolUse(response.ToolUseID, response.ToolName, response.ToolInput, response.Thinking)
		sess.AddToolResult(response.ToolUseID, resultText)
		// Persist tool use and result to SQLite (skip for heartbeat - ephemeral)
		if !req.IsHeartbeat {
			g.persistMessage(ctx, sessionKey, "tool_use", "", req.Source, response.ToolUseID, response.ToolName, response.ToolInput, "", response.Thinking, "", "")
			g.persistMessage(ctx, sessionKey, "tool_result", resultText, req.Source, response.ToolUseID, "", nil, errStr, "", "", "")
		}
		continue
	}

		// No tool use - we're done
		finalText = response.Text
		sess.AddAssistantMessage(finalText)
		// Persist assistant message (skip for heartbeat - ephemeral)
		if !req.IsHeartbeat {
			g.persistMessage(ctx, sessionKey, "assistant", finalText, "", "", "", nil, "", "", "", "")
		}
		break
	}

	if finalText == "" {
		L_warn("agent run completed with empty response", "runID", runID, "messages", sess.MessageCount())
	}
	L_info("agent run completed", "runID", runID, "responseLen", len(finalText))

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

	// For heartbeat: rollback in-memory session to before the run (ephemeral)
	if req.IsHeartbeat && messageCountBefore > 0 {
		sess.TruncateMessages(messageCountBefore)
		L_debug("heartbeat: rolled back session messages", "before", messageCountBefore, "after", sess.MessageCount())
	}

	// Check for suppression tokens - if response contains any, suppress delivery
	// These tokens indicate agent has nothing meaningful to say
	if shouldSuppressResponse(finalText) {
		L_debug("gateway: response suppressed (contains suppression token)", "session", sessionKey, "responseLen", len(finalText))
		finalText = ""
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

	// Mirror response to other channels (not for group chats, not if caller handles delivery)
	if !req.IsGroup && !req.SkipMirror {
		g.mirrorToOthers(ctx, req, finalText)
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
	for _, ch := range g.channels {
		if ch.HasUser(u) {
			ch.Send(ctx, msg) //nolint:errcheck // fire-and-forget notification
		}
	}
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

// mirrorToOthers sends a mirror of the conversation to other channels
func (g *Gateway) mirrorToOthers(ctx context.Context, req AgentRequest, response string) {
	if req.User == nil {
		return
	}

	for name, ch := range g.channels {
		if name == req.Source {
			continue // don't mirror to source
		}

		if !ch.HasUser(req.User) {
			continue // skip channels user isn't connected to
		}

		L_debug("mirror: sending", "from", req.Source, "to", name)
		ch.SendMirror(ctx, req.Source, req.UserMsg, response) //nolint:errcheck // fire-and-forget mirror
	}
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
		sess.AddSupervisionUserMessage(prefixedMessage, "guidance", supervisorName, "guidance")
		L_debug("gateway: added guidance to session", "session", sessionKey, "prefixedLen", len(prefixedMessage))

		// Persist with supervision metadata
		g.persistMessage(ctx, sessionKey, "user", prefixedMessage, "guidance", "", "", nil, "", "", supervisorName, "guidance")

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
				for name, ch := range g.channels {
					if !ch.HasUser(u) || streamedChannels[name] {
						continue // Skip if user not on channel or already streamed
					}
					if err := ch.Send(ctx, finalText); err != nil {
						L_error("gateway: guidance delivery failed", "channel", name, "error", err)
					} else {
						L_debug("gateway: guidance delivered", "channel", name)
					}
				}
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
		sess.AddSupervisionAssistantMessage(message, supervisorName, "ghostwrite")
		L_debug("gateway: added ghostwrite to session", "session", sessionKey, "messageLen", len(message))

		// Persist with supervision metadata
		g.persistMessage(ctx, sessionKey, "assistant", message, "ghostwrite", "", "", nil, "", "", supervisorName, "ghostwrite")

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

// persistMessage writes a message to SQLite storage for audit trail
func (g *Gateway) persistMessage(ctx context.Context, sessionKey, role, content, source, toolCallID, toolName string, toolInput []byte, toolError, thinking, supervisor, interventionType string) {
	store := g.sessions.GetStore()
	if store == nil {
		return // No store configured
	}

	msg := &session.StoredMessage{
		ID:               session.GenerateRecordID(),
		SessionKey:       sessionKey,
		Timestamp:        time.Now(),
		Role:             role,
		Content:          content,
		Source:           source,
		ToolCallID:       toolCallID,
		ToolName:         toolName,
		ToolInput:        toolInput,
		Thinking:         thinking,
		Supervisor:       supervisor,
		InterventionType: interventionType,
	}

	// For tool_result, store the result in ToolResult field and mark errors
	if role == "tool_result" {
		msg.ToolResult = content // Store actual result
		msg.Content = ""         // Keep content empty for tool results
		if toolError != "" {
			msg.ToolIsError = true
		}
	}

	if err := store.AppendMessage(ctx, sessionKey, msg); err != nil {
		L_warn("failed to persist message to SQLite", "role", role, "error", err)
	} else {
		L_trace("message persisted to SQLite", "role", role, "toolName", toolName, "supervisor", supervisor)
	}
}

// buildMediaInstructions returns media storage instructions for the system prompt.
// resolveThinkingLevel determines the effective thinking level based on priority hierarchy:
// Request Override > User Preference > Provider Default > Global Default
// Note: Request.ThinkingLevel is set by channel handlers based on per-session preferences
func (g *Gateway) resolveThinkingLevel(req AgentRequest, providerName string) llm.ThinkingLevel {
	// 1. Request override (set via /thinking command by channel handler)
	if req.ThinkingLevel != "" {
		L_trace("thinking: using request override", "level", req.ThinkingLevel)
		return llm.ParseThinkingLevel(req.ThinkingLevel)
	}

	// 2. User preference
	if req.User != nil && req.User.ThinkingLevel != "" {
		L_trace("thinking: using user preference", "user", req.User.ID, "level", req.User.ThinkingLevel)
		return llm.ParseThinkingLevel(req.User.ThinkingLevel)
	}

	// 3. Provider default (from config)
	if providerName != "" {
		if providerCfg, ok := g.config.LLM.Providers[providerName]; ok && providerCfg.ThinkingLevel != "" {
			L_trace("thinking: using provider default", "provider", providerName, "level", providerCfg.ThinkingLevel)
			return llm.ParseThinkingLevel(providerCfg.ThinkingLevel)
		}
	}

	// 4. Global default from config
	if g.config.LLM.Thinking.DefaultLevel != "" {
		L_trace("thinking: using global default", "level", g.config.LLM.Thinking.DefaultLevel)
		return llm.ParseThinkingLevel(g.config.LLM.Thinking.DefaultLevel)
	}

	// 5. Fallback to hardcoded default
	L_trace("thinking: using hardcoded default", "level", llm.DefaultThinkingLevel)
	return llm.DefaultThinkingLevel
}

func (g *Gateway) buildMediaInstructions() string {
	if g.mediaStore == nil {
		return ""
	}

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

1. **Inline (conversational):** Write {{media:path}} in your response
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
