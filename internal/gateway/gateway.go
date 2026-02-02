package gateway

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/roelfdiedericks/goclaw/internal/commands"
	"github.com/roelfdiedericks/goclaw/internal/config"
	gcontext "github.com/roelfdiedericks/goclaw/internal/context"
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
	jsonlWriter         *session.JSONLWriter
	compactor           *session.Compactor
	promptCache         *gcontext.PromptCache
	mediaStore          *media.MediaStore
	memoryManager       *memory.Manager
	ollamaClient        *llm.OllamaClient
	commandHandler      *commands.Handler
	skillManager        *skills.Manager
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
		WriteToKey:  cfg.Session.WriteToKey,
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

	// Initialize JSONL writer for legacy support
	if cfg.Session.Path != "" {
		g.jsonlWriter = session.NewJSONLWriter(cfg.Session.Path)
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
	g.checkpointGenerator = session.NewCheckpointGenerator(checkpointCfg, g.jsonlWriter)
	L_debug("session: checkpoint generator configured",
		"enabled", cfg.Session.Checkpoint.Enabled,
		"model", cfg.Session.Checkpoint.Model,
		"tokenThresholds", cfg.Session.Checkpoint.TokenThresholdPercents,
		"turnThreshold", cfg.Session.Checkpoint.TurnThreshold)

	// Initialize compaction manager with fallback support
	sessionKey := cfg.Session.WriteToKey
	if sessionKey == "" {
		sessionKey = session.PrimarySession
	}
	compactorCfg := &session.CompactionManagerConfig{
		ReserveTokens:          cfg.Session.Compaction.ReserveTokens,
		PreferCheckpoint:       cfg.Session.Compaction.PreferCheckpoint,
		RetryIntervalSeconds:   cfg.Session.Compaction.RetryIntervalSeconds,
		OllamaFailureThreshold: cfg.Session.Compaction.OllamaFailureThreshold,
		OllamaResetMinutes:     cfg.Session.Compaction.OllamaResetMinutes,
		SessionKey:             sessionKey,
	}
	g.compactor = session.NewCompactionManager(compactorCfg)
	g.compactor.SetWriter(g.jsonlWriter)
	g.compactor.SetStore(g.sessions.GetStore())
	L_debug("session: compaction manager configured",
		"reserveTokens", cfg.Session.Compaction.ReserveTokens,
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
		// Handle new records from OpenClaw (e.g., mirror to other channels)
		L_debug("session: received new OpenClaw records", "count", len(records))
	})
}

// Start begins background tasks (call after New)
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
}

// Shutdown gracefully shuts down the gateway
func (g *Gateway) Shutdown() {
	L_info("gateway: shutting down")

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
	sess := g.sessions.Get(sessionKey)
	
	// Add user message with images if any
	if len(req.Images) > 0 {
		sess.AddUserMessageWithImages(req.UserMsg, req.Source, req.Images)
	} else {
		sess.AddUserMessage(req.UserMsg, req.Source)
	}
	
	// Persist user message to SQLite
	g.persistMessage(ctx, sessionKey, "user", req.UserMsg, req.Source, "", "", nil, "")

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
			// Persist denied tool use/result
			g.persistMessage(ctx, sessionKey, "tool_use", "", req.Source, response.ToolUseID, response.ToolName, response.ToolInput, "")
			g.persistMessage(ctx, sessionKey, "tool_result", result, req.Source, response.ToolUseID, "", nil, "")
			continue
		}

		events <- EventToolStart{
				RunID:    runID,
				ToolName: response.ToolName,
				ToolID:   response.ToolUseID,
				Input:    response.ToolInput,
			}

			// Execute tool with session context
			result, err := g.tools.Execute(ctx, response.ToolName, response.ToolInput)

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
				RunID:    runID,
				ToolName: response.ToolName,
				ToolID:   response.ToolUseID,
				Result:   result,
				Error:    errStr,
			}

			// Add to session and continue loop
			sess.AddToolUse(response.ToolUseID, response.ToolName, response.ToolInput)
			sess.AddToolResult(response.ToolUseID, result)
			// Persist tool use and result to SQLite
			g.persistMessage(ctx, sessionKey, "tool_use", "", req.Source, response.ToolUseID, response.ToolName, response.ToolInput, "")
			g.persistMessage(ctx, sessionKey, "tool_result", result, req.Source, response.ToolUseID, "", nil, errStr)
			continue
		}

		// No tool use - we're done
		finalText = response.Text
		sess.AddAssistantMessage(finalText)
		// Persist assistant message
		g.persistMessage(ctx, sessionKey, "assistant", finalText, "", "", "", nil, "")
		break
	}

	if finalText == "" {
		L_warn("agent run completed with empty response", "runID", runID, "messages", sess.MessageCount())
	}
	L_info("agent run completed", "runID", runID, "responseLen", len(finalText))
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

	// Mirror to other channels if enabled
	if g.config.Mirroring.Enabled && !req.IsGroup {
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
	writeKey := g.config.Session.WriteToKey
	if store != nil && writeKey != "" {
		compactions, err := store.GetCompactions(ctx, writeKey)
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
	if req.IsGroup {
		return fmt.Sprintf("group:%s", req.ChatID)
	}
	// Use primary session for direct messages
	return session.PrimarySession
}

// mirrorToOthers sends a mirror of the conversation to other channels
func (g *Gateway) mirrorToOthers(ctx context.Context, req AgentRequest, response string) {
	for name, ch := range g.channels {
		if name == req.Source {
			continue // don't mirror to source
		}

		mirrorCfg, ok := g.config.Mirroring.Channels[name]
		if !ok || !mirrorCfg.Mirror {
			continue
		}

		// Only mirror to channels where this user is connected
		if !ch.HasUser(req.User) {
			continue
		}

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
