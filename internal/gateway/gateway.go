package gateway

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/roelfdiedericks/goclaw/internal/commands"
	"github.com/roelfdiedericks/goclaw/internal/config"
	gcontext "github.com/roelfdiedericks/goclaw/internal/context"
	"github.com/roelfdiedericks/goclaw/internal/cron"
	"github.com/roelfdiedericks/goclaw/internal/llm"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/media"
	"github.com/roelfdiedericks/goclaw/internal/memory"
	"github.com/roelfdiedericks/goclaw/internal/session"
	"github.com/roelfdiedericks/goclaw/internal/skills"
	"github.com/roelfdiedericks/goclaw/internal/tools"
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
}

// Gateway is the central service layer that coordinates the agent loop
type Gateway struct {
	sessions            *session.Manager
	users               *user.Registry
	llm                 *llm.Client
	tools               *tools.Registry
	channels            map[string]Channel
	config              *config.Config
	startTime           time.Time
	checkpointGenerator *session.CheckpointGenerator
	compactor           *session.Compactor
	promptCache         *gcontext.PromptCache
	mediaStore          *media.MediaStore
	memoryManager       *memory.Manager
	ollamaClient        *llm.OllamaClient
	commandHandler      *commands.Handler
	skillManager        *skills.Manager
	cronService         *cron.Service
	lastOpenClawUserMsg string // Track user messages for mirroring
}

// Regex for detecting context overflow errors
var (
	// Matches "prompt is too long: 200170 tokens > 200000 maximum"
	promptTooLongRe = regexp.MustCompile(`prompt is too long:\s*(\d+)\s*tokens?\s*>\s*(\d+)`)
)

// New creates a new Gateway instance
func New(cfg *config.Config, users *user.Registry, llmClient *llm.Client, toolsReg *tools.Registry) (*Gateway, error) {
	g := &Gateway{
		users:     users,
		llm:       llmClient,
		tools:     toolsReg,
		channels:  make(map[string]Channel),
		config:    cfg,
		startTime: time.Now(),
	}

	// Determine store type
	storeType := cfg.Session.Store
	if storeType == "" {
		storeType = cfg.Session.Storage // Legacy field
	}
	if storeType == "" {
		storeType = "sqlite" // Default
	}

	// Determine store path
	storePath := cfg.Session.StorePath
	if storePath == "" {
		storePath = cfg.Session.Path
	}

	// Initialize session manager with config
	managerCfg := &session.ManagerConfig{
		StoreType:   storeType,
		StorePath:   storePath,
		SessionsDir: cfg.Session.Path, // For JSONL sessions
		InheritFrom: cfg.Session.InheritFrom,
		WorkingDir:  cfg.Gateway.WorkingDir,
	}

	var err error
	g.sessions, err = session.NewManagerWithConfig(managerCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create session manager: %w", err)
	}
	L_info("session: storage backend ready",
		"store", storeType,
		"path", storePath)

	// Inherit from OpenClaw session if configured
	if cfg.Session.Inherit && cfg.Session.InheritFrom != "" && cfg.Session.Path != "" {
		if err := g.sessions.InheritOpenClawSession(cfg.Session.Path, cfg.Session.InheritFrom); err != nil {
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

	// Initialize checkpoint generator
	checkpointCfg := &session.CheckpointGeneratorConfig{
		Enabled:                cfg.Session.Checkpoint.Enabled,
		Model:                  cfg.Session.Checkpoint.Model,
		FallbackToMain:         cfg.Session.Checkpoint.FallbackToMain,
		TokenThresholdPercents: cfg.Session.Checkpoint.TokenThresholdPercents,
		TurnThreshold:          cfg.Session.Checkpoint.TurnThreshold,
		MinTokensForGen:        cfg.Session.Checkpoint.MinTokensForGen,
	}
	g.checkpointGenerator = session.NewCheckpointGenerator(checkpointCfg)
	L_debug("session: checkpoint generator configured",
		"enabled", cfg.Session.Checkpoint.Enabled,
		"model", cfg.Session.Checkpoint.Model,
		"tokenThresholds", cfg.Session.Checkpoint.TokenThresholdPercents,
		"turnThreshold", cfg.Session.Checkpoint.TurnThreshold)

	// Initialize compaction manager
	// Handles all sessions - uses sess.Key for multi-user support
	compactorCfg := &session.CompactionManagerConfig{
		ReserveTokens:          cfg.Session.Compaction.ReserveTokens,
		MaxMessages:            cfg.Session.Compaction.MaxMessages,
		PreferCheckpoint:       cfg.Session.Compaction.PreferCheckpoint,
		RetryIntervalSeconds:   cfg.Session.Compaction.RetryIntervalSeconds,
		OllamaFailureThreshold: cfg.Session.Compaction.OllamaFailureThreshold,
		OllamaResetMinutes:     cfg.Session.Compaction.OllamaResetMinutes,
	}
	g.compactor = session.NewCompactionManager(compactorCfg)
	g.compactor.SetStore(g.sessions.GetStore())
	L_debug("session: compaction manager configured",
		"reserveTokens", cfg.Session.Compaction.ReserveTokens,
		"maxMessages", cfg.Session.Compaction.MaxMessages,
		"preferCheckpoint", cfg.Session.Compaction.PreferCheckpoint,
		"retryInterval", cfg.Session.Compaction.RetryIntervalSeconds,
		"ollamaFailureThreshold", cfg.Session.Compaction.OllamaFailureThreshold,
		"ollamaResetMinutes", cfg.Session.Compaction.OllamaResetMinutes)

	// Create main model adapter (always available as fallback)
	mainAdapter := session.NewLLMAdapterFunc(llmClient.SimpleMessage, llmClient.Model())
	g.compactor.SetMainClient(mainAdapter)
	g.checkpointGenerator.SetLLMClients(mainAdapter, mainAdapter)

	// Initialize Ollama client for compaction/checkpoints if configured
	if cfg.Session.Compaction.Ollama.URL != "" && cfg.Session.Compaction.Ollama.Model != "" {
		L_info("compaction: using ollama as primary model",
			"url", cfg.Session.Compaction.Ollama.URL,
			"model", cfg.Session.Compaction.Ollama.Model)

		ollamaClient := llm.NewOllamaClient(
			cfg.Session.Compaction.Ollama.URL,
			cfg.Session.Compaction.Ollama.Model,
			cfg.Session.Compaction.Ollama.TimeoutSeconds,
			cfg.Session.Compaction.Ollama.ContextTokens,
		)
		g.ollamaClient = ollamaClient

		// Create adapter and set as primary for compaction (main is already set as fallback)
		ollamaAdapter := session.NewLLMAdapterFunc(ollamaClient.SimpleMessage, ollamaClient.Model())
		g.compactor.SetOllamaClient(ollamaAdapter)
		g.checkpointGenerator.SetLLMClients(ollamaAdapter, mainAdapter)

		L_info("compaction: fallback to main model after failures",
			"threshold", compactorCfg.OllamaFailureThreshold,
			"resetMinutes", compactorCfg.OllamaResetMinutes)
	} else {
		L_debug("compaction: using main model only (ollama not configured)")
	}

	// Initialize memory manager if enabled
	if cfg.MemorySearch.Enabled {
		L_info("memory: initializing manager", "workspace", cfg.Gateway.WorkingDir)
		memMgr, err := memory.NewManager(cfg.MemorySearch, cfg.Gateway.WorkingDir)
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
	mediaStore, err := media.NewMediaStore(media.MediaConfig{
		Dir:     cfg.Media.Dir,
		TTL:     cfg.Media.TTL,
		MaxSize: cfg.Media.MaxSize,
	})
	if err != nil {
		L_warn("failed to create media store", "error", err)
	} else {
		g.mediaStore = mediaStore
		L_info("media: store initialized",
			"dir", cfg.Media.Dir,
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
		"checkpoints", cfg.Session.Checkpoint.Enabled,
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
			skillMgrCfg.WorkspaceDir = cfg.Gateway.WorkingDir + "/skills"
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
	eligibleSkills := g.skillManager.GetEligibleSkills()
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

	// NOTE: Cron is NOT started here - call StartCron() after channels are registered
}

// StartCron initializes and starts the cron scheduler.
func (g *Gateway) StartCron(ctx context.Context) error {
	if g.cronService != nil && g.cronService.IsRunning() {
		return fmt.Errorf("cron service already running")
	}

	// Create store with default paths
	store := cron.NewStore("", "")

	// Create and start service
	g.cronService = cron.NewService(store, g)

	// Set up channel provider for delivery
	g.cronService.SetChannelProvider(&gatewayCronChannelProvider{g: g})

	// Set up heartbeat config if enabled
	if g.config.Cron.Heartbeat.Enabled {
		heartbeatCfg := &cron.HeartbeatConfig{
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

	// Convert cron request to gateway request
	req := AgentRequest{
		Source:       cronReq.Source,
		UserMsg:      cronReq.UserMsg,
		SessionID:    cronReq.SessionID,
		FreshContext: cronReq.FreshContext,
		User:         reqUser,
		IsHeartbeat:  cronReq.IsHeartbeat,
	}

	// Create internal events channel
	events := make(chan AgentEvent, 100)

	// Run the agent in a goroutine
	go g.RunAgent(ctx, req, events)

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

// Shutdown gracefully shuts down the gateway
func (g *Gateway) Shutdown() {
	L_info("gateway: shutting down")

	// Stop cron service
	g.StopCron()

	// Stop skill manager
	if g.skillManager != nil {
		g.skillManager.Stop()
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
		g.memoryManager.Close()
	}

	if g.sessions != nil {
		g.sessions.Close()
	}
}

// isContextOverflowError checks if an error indicates context overflow
func isContextOverflowError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()

	// Anthropic format: "prompt is too long: 200170 tokens > 200000 maximum"
	if promptTooLongRe.MatchString(errStr) {
		return true
	}

	// Also check for common variations
	if strings.Contains(errStr, "prompt is too long") ||
		strings.Contains(errStr, "context_length_exceeded") ||
		strings.Contains(errStr, "maximum context length") {
		return true
	}

	return false
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

	events <- EventAgentStart{
		RunID:      runID,
		Source:     req.Source,
		SessionKey: sessionKey,
	}

	// Get or create session
	// Use GetFresh for isolated cron jobs to ensure clean context
	var sess *session.Session
	if req.FreshContext {
		sess = g.sessions.GetFresh(sessionKey)
	} else {
		sess = g.sessions.Get(sessionKey)
	}

	// Ensure session has the model's context window size set
	if sess.GetMaxTokens() == 0 && g.llm != nil {
		sess.SetMaxTokens(g.llm.ContextTokens())
	}

	// For heartbeat: snapshot message count so we can rollback after (ephemeral)
	messageCountBefore := 0
	if req.IsHeartbeat {
		messageCountBefore = sess.MessageCount()
	}
	
	// Add user message with images if any
	if len(req.Images) > 0 {
		sess.AddUserMessageWithImages(req.UserMsg, req.Source, req.Images)
	} else {
		sess.AddUserMessage(req.UserMsg, req.Source)
	}
	
	// Persist user message to SQLite (skip for heartbeat - ephemeral)
	if !req.IsHeartbeat {
		g.persistMessage(ctx, sessionKey, "user", req.UserMsg, req.Source, "", "", nil, "")
	}

	// Build system prompt
	var workspaceFiles []gcontext.WorkspaceFile
	if g.promptCache != nil {
		workspaceFiles = g.promptCache.GetWorkspaceFiles()
	}

	// Get skills prompt
	skillsPrompt := g.GetSkillsPrompt()

	systemPrompt := gcontext.BuildSystemPrompt(gcontext.PromptParams{
		WorkspaceDir:   g.config.Gateway.WorkingDir,
		Tools:          g.tools,
		Model:          g.llm.Model(),
		Channel:        req.Source,
		User:           req.User,
		TotalTokens:    sess.GetTotalTokens(),
		MaxTokens:      sess.GetMaxTokens(),
		WorkspaceFiles: workspaceFiles,
		SkillsPrompt:   skillsPrompt,
	})

	// Append media storage instructions
	systemPrompt += g.buildMediaInstructions()

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
				"emergencyTruncation", result.EmergencyTruncation)

			// Notify user if emergency truncation was used
			if result.EmergencyTruncation {
				events <- EventTextDelta{
					RunID: runID,
					Delta: "[System: Compaction failed - session memory truncated to last 20%. Some context may be lost.]\n\n",
				}
			}
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

	var finalText string
	const maxOverflowRetries = 2 // Max times to retry after compaction

	// Agent loop - keep going until no more tool use
	for {
		// Build context from session (messages and tool definitions)
		messages := sess.GetMessages()
		toolDefs := g.tools.Definitions()

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
					result, err := g.compactor.Compact(ctx, sess, sess.SessionFile)
					if err != nil {
						L_error("pre-flight compaction failed", "error", err)
					} else {
						// Refresh messages after compaction
						messages = sess.GetMessages()
						L_info("pre-flight compaction completed",
							"newTokens", sess.GetTotalTokens(),
							"newUsage", fmt.Sprintf("%.1f%%", sess.GetContextUsage()*100))
						// Notify user if emergency truncation
						if result.EmergencyTruncation {
							events <- EventTextDelta{
								RunID: runID,
								Delta: "[System: Compaction failed - session memory truncated to last 20%. Some context may be lost.]\n\n",
							}
						}
					}
				}
			}
		}

		// Stream from LLM with overflow retry logic
		var response *llm.Response
		var llmErr error
		for retry := 0; retry <= maxOverflowRetries; retry++ {
			response, llmErr = g.llm.StreamMessage(ctx, messages, toolDefs, systemPrompt, func(delta string) {
				events <- EventTextDelta{RunID: runID, Delta: delta}
			})

			if llmErr == nil {
				break // Success
			}

			// Check if this is a context overflow error
			if isContextOverflowError(llmErr) {
				if retry < maxOverflowRetries && g.compactor != nil {
					L_warn("context overflow detected, attempting recovery compaction",
						"retry", retry+1,
						"maxRetries", maxOverflowRetries,
						"error", llmErr.Error())

					// Perform emergency compaction
					result, compactErr := g.compactor.Compact(ctx, sess, sess.SessionFile)
					if compactErr != nil {
						L_error("recovery compaction failed", "error", compactErr)
						break // Can't recover
					}

					// Refresh messages after compaction
					messages = sess.GetMessages()
					L_info("recovery compaction completed, retrying API call",
						"newTokens", sess.GetTotalTokens(),
						"newMessages", len(messages))

					// Notify user if emergency truncation
					if result.EmergencyTruncation {
						events <- EventTextDelta{
							RunID: runID,
							Delta: "[System: Compaction failed - session memory truncated to last 20%. Some context may be lost.]\n\n",
						}
					}
					continue // Retry the API call
				}
				L_error("context overflow: max retries exceeded", "retries", retry)
			}
			break // Non-overflow error or max retries reached
		}

		if llmErr != nil {
			events <- EventAgentError{RunID: runID, Error: llmErr.Error()}
			return llmErr
		}

		// Update token tracking
		sess.UpdateTokens(response.InputTokens, response.OutputTokens)

		// Handle tool use
		if response.HasToolUse() {
			// Check permissions
			if !req.User.CanUseTool(response.ToolName) {
				result := fmt.Sprintf("Permission denied: %s cannot use tool %s", req.User.Name, response.ToolName)
				events <- EventToolEnd{
					RunID:    runID,
					ToolName: response.ToolName,
					ToolID:   response.ToolUseID,
					Result:   result,
					Error:    "permission_denied",
				}
			sess.AddToolUse(response.ToolUseID, response.ToolName, response.ToolInput)
			sess.AddToolResult(response.ToolUseID, result)
			// Persist denied tool use/result (skip for heartbeat - ephemeral)
			if !req.IsHeartbeat {
				g.persistMessage(ctx, sessionKey, "tool_use", "", req.Source, response.ToolUseID, response.ToolName, response.ToolInput, "")
				g.persistMessage(ctx, sessionKey, "tool_result", result, req.Source, response.ToolUseID, "", nil, "")
			}
			continue
		}

		events <- EventToolStart{
				RunID:    runID,
				ToolName: response.ToolName,
				ToolID:   response.ToolUseID,
				Input:    response.ToolInput,
			}

			// Execute tool with session context
			toolStartTime := time.Now()
			ownerChatID := ""
			if owner := g.users.Owner(); owner != nil {
				ownerChatID = owner.TelegramID
			}
			toolCtx := tools.WithSessionContext(ctx, &tools.SessionContext{
				Channel:     req.Source,
				ChatID:      req.ChatID,
				OwnerChatID: ownerChatID,
				User:        req.User,
			})
			result, err := g.tools.Execute(toolCtx, response.ToolName, response.ToolInput)
			toolDuration := time.Since(toolStartTime)

			errStr := ""
			if err != nil {
				errStr = err.Error()
				result = fmt.Sprintf("Error: %s", err.Error())
			}

			// Check for media in tool output
			if req.OnMediaToSend != nil {
				parseResult := media.SplitMediaFromOutput(result)
				result = parseResult.Text
				for _, mediaPath := range parseResult.MediaURLs {
					if mediaErr := req.OnMediaToSend(mediaPath, ""); mediaErr != nil {
						L_warn("failed to send media", "path", mediaPath, "error", mediaErr)
					}
				}
			}

			events <- EventToolEnd{
				RunID:      runID,
				ToolName:   response.ToolName,
				ToolID:     response.ToolUseID,
				Result:     result,
				Error:      errStr,
				DurationMs: toolDuration.Milliseconds(),
			}

			// Add to session and continue loop
			sess.AddToolUse(response.ToolUseID, response.ToolName, response.ToolInput)
			sess.AddToolResult(response.ToolUseID, result)
			// Persist tool use and result to SQLite (skip for heartbeat - ephemeral)
			if !req.IsHeartbeat {
				g.persistMessage(ctx, sessionKey, "tool_use", "", req.Source, response.ToolUseID, response.ToolName, response.ToolInput, "")
				g.persistMessage(ctx, sessionKey, "tool_result", result, req.Source, response.ToolUseID, "", nil, errStr)
			}
			continue
		}

		// No tool use - we're done
		finalText = response.Text
		sess.AddAssistantMessage(finalText)
		// Persist assistant message (skip for heartbeat - ephemeral)
		if !req.IsHeartbeat {
			g.persistMessage(ctx, sessionKey, "assistant", finalText, "", "", "", nil, "")
		}
		break
	}

	if finalText == "" {
		L_warn("agent run completed with empty response", "runID", runID, "messages", sess.MessageCount())
	}
	L_info("agent run completed", "runID", runID, "responseLen", len(finalText))

	// Enrich media references: {{media:path}} -> {{media:mime:'path'}}
	finalText = g.enrichMediaRefs(finalText)

	// For heartbeat: rollback in-memory session to before the run (ephemeral)
	if req.IsHeartbeat && messageCountBefore > 0 {
		sess.TruncateMessages(messageCountBefore)
		L_debug("heartbeat: rolled back session messages", "before", messageCountBefore, "after", sess.MessageCount())
	}

	events <- EventAgentEnd{RunID: runID, FinalText: finalText}

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

	// Mirror to other channels (not for group chats)
	if !req.IsGroup {
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

// CommandHandler returns the unified command handler
func (g *Gateway) CommandHandler() *commands.Handler {
	return g.commandHandler
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
		ch.SendMirror(ctx, req.Source, req.UserMsg, response)
	}
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
func (g *Gateway) persistMessage(ctx context.Context, sessionKey, role, content, source, toolCallID, toolName string, toolInput []byte, toolError string) {
	store := g.sessions.GetStore()
	if store == nil {
		return // No store configured
	}

	msg := &session.StoredMessage{
		ID:         session.GenerateRecordID(),
		SessionKey: sessionKey,
		Timestamp:  time.Now(),
		Role:       role,
		Content:    content,
		Source:     source,
		ToolCallID: toolCallID,
		ToolName:   toolName,
		ToolInput:  toolInput,
	}

	// For tool_result, store the result in ToolResult field and mark errors
	if role == "tool_result" {
		msg.ToolResult = content // Store actual result
		msg.Content = ""        // Keep content empty for tool results
		if toolError != "" {
			msg.ToolIsError = true
		}
	}

	if err := store.AppendMessage(ctx, sessionKey, msg); err != nil {
		L_warn("failed to persist message to SQLite", "role", role, "error", err)
	} else {
		L_trace("message persisted to SQLite", "role", role, "toolName", toolName)
	}
}

// buildMediaInstructions returns media storage instructions for the system prompt.
func (g *Gateway) buildMediaInstructions() string {
	if g.mediaStore == nil {
		return ""
	}

	return fmt.Sprintf(`

## Media Storage

Media root: %s

Subdirectory conventions:
- browser/     - browser screenshots (BrowserTool uses this)
- screenshots/ - general screenshots, screen captures
- camera/      - camera/security captures
- inbound/     - user-uploaded media (Telegram photos, HTTP paste)
- generated/   - AI-generated images
- downloads/   - downloaded files

When saving media, use appropriate subdirectory.

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
