package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/creasty/defaults"
	"github.com/roelfdiedericks/goclaw/internal/a2a"
	"github.com/roelfdiedericks/goclaw/internal/acp"
	"github.com/roelfdiedericks/goclaw/internal/auth"
	httpconfig "github.com/roelfdiedericks/goclaw/internal/channels/http/config"
	telegramconfig "github.com/roelfdiedericks/goclaw/internal/channels/telegram/config"
	tuiconfig "github.com/roelfdiedericks/goclaw/internal/channels/tui/config"
	whatsappconfig "github.com/roelfdiedericks/goclaw/internal/channels/whatsapp/config"
	"github.com/roelfdiedericks/goclaw/internal/cron"
	gwtypes "github.com/roelfdiedericks/goclaw/internal/gateway/types"
	"github.com/roelfdiedericks/goclaw/internal/hass"
	"github.com/roelfdiedericks/goclaw/internal/llm"
	"github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/media"
	"github.com/roelfdiedericks/goclaw/internal/memory"
	"github.com/roelfdiedericks/goclaw/internal/memorygraph"
	"github.com/roelfdiedericks/goclaw/internal/paths"
	"github.com/roelfdiedericks/goclaw/internal/sandbox"
	"github.com/roelfdiedericks/goclaw/internal/session"
	"github.com/roelfdiedericks/goclaw/internal/skills"
	"github.com/roelfdiedericks/goclaw/internal/stt"
	toolsconfig "github.com/roelfdiedericks/goclaw/internal/tools/config"
	"github.com/roelfdiedericks/goclaw/internal/transcript"
	"github.com/roelfdiedericks/goclaw/internal/user"
	"github.com/roelfdiedericks/goclaw/internal/voicellm"
)

// LoadResult contains the loaded config and metadata about where it came from
type LoadResult struct {
	Config     *Config
	SourcePath string // Path to goclaw.json that was loaded
}

// IsMissingOrIncompleteConfigError reports whether a config load error means
// "no usable config exists yet" as opposed to a malformed or otherwise broken config.
func IsMissingOrIncompleteConfigError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "no goclaw.json") || strings.Contains(msg, "empty or incomplete")
}

// loadedConfigPath stores the path of the most recently loaded config
// Set by LoadRuntime() for use by setup/editor components
var loadedConfigPath string

// GetLoadedConfigPath returns the path of the most recently loaded config file
func GetLoadedConfigPath() string {
	if loadedConfigPath != "" {
		return loadedConfigPath
	}
	// Fallback to default path
	path, _ := paths.DefaultConfigPath()
	return path
}

// isMinimalJSON checks if JSON content is essentially empty (just {} or whitespace)
// Returns false for parse errors so we can give better error messages later
func isMinimalJSON(data []byte) bool {
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		return false // Parse error - let Load() handle it with better error message
	}
	return len(m) == 0
}

// formatJSONError enhances JSON parsing errors with line/column info and context
func formatJSONError(data []byte, err error) error {
	if err == nil {
		return nil
	}

	// Check if it's a syntax error with offset
	if syntaxErr, ok := err.(*json.SyntaxError); ok {
		return formatJSONSyntaxError(data, syntaxErr)
	}

	// Check for unmarshal type errors
	if typeErr, ok := err.(*json.UnmarshalTypeError); ok {
		line, col := offsetToLineCol(data, typeErr.Offset)
		return fmt.Errorf("JSON type error at line %d, column %d: expected %s but got %s for field '%s'",
			line, col, typeErr.Type, typeErr.Value, typeErr.Field)
	}

	return err
}

// formatJSONSyntaxError creates a detailed error message for JSON syntax errors
func formatJSONSyntaxError(data []byte, syntaxErr *json.SyntaxError) error {
	line, col := offsetToLineCol(data, syntaxErr.Offset)

	// Get the problematic line for context
	lines := splitLines(data)
	var context string
	if line > 0 && line <= len(lines) {
		problemLine := lines[line-1]
		// Truncate very long lines
		if len(problemLine) > 80 {
			if col > 40 {
				start := col - 40
				problemLine = "..." + problemLine[start:]
				col = 43 // Adjust for "..."
			}
			if len(problemLine) > 80 {
				problemLine = problemLine[:77] + "..."
			}
		}
		// Build pointer line
		pointer := ""
		for i := 0; i < col-1 && i < len(problemLine); i++ {
			if problemLine[i] == '\t' {
				pointer += "\t"
			} else {
				pointer += " "
			}
		}
		pointer += "^"
		context = fmt.Sprintf("\n  %s\n  %s", problemLine, pointer)
	}

	return fmt.Errorf("JSON syntax error at line %d, column %d: %s%s",
		line, col, syntaxErr.Error(), context)
}

// offsetToLineCol converts a byte offset to line and column numbers (1-indexed)
func offsetToLineCol(data []byte, offset int64) (line, col int) {
	line = 1
	col = 1
	for i := int64(0); i < offset && i < int64(len(data)); i++ {
		if data[i] == '\n' {
			line++
			col = 1
		} else {
			col++
		}
	}
	return line, col
}

// splitLines splits data into lines, preserving empty lines
func splitLines(data []byte) []string {
	var lines []string
	start := 0
	for i := 0; i < len(data); i++ {
		if data[i] == '\n' {
			lines = append(lines, string(data[start:i]))
			start = i + 1
		}
	}
	// Don't forget the last line if it doesn't end with newline
	if start < len(data) {
		lines = append(lines, string(data[start:]))
	}
	return lines
}

// ChannelsConfig aggregates all channel configurations.
// This lives in config package to avoid import cycles (channel packages import gateway).
type ChannelsConfig struct {
	Telegram telegramconfig.Config `json:"telegram"`
	WhatsApp whatsappconfig.Config `json:"whatsapp"`
	HTTP     httpconfig.Config     `json:"http"`
	TUI      tuiconfig.Config      `json:"tui"`
}

// Config represents the merged goclaw configuration
type Config struct {
	A2A           a2a.Config                  `json:"a2a"`
	Gateway       gwtypes.GatewayConfig       `json:"gateway"`
	ACP           acp.Config                  `json:"acp"`
	Agent         gwtypes.AgentIdentityConfig `json:"agent"`
	LLM           llm.LLMConfig               `json:"llm"`
	VoiceLLM      voicellm.Config             `json:"voicellm"`      // Real-time voice LLM configuration
	HomeAssistant hass.HomeAssistantConfig    `json:"homeassistant"` // Top-level Home Assistant config
	Tools         toolsconfig.ToolsConfig     `json:"tools"`
	Channels      ChannelsConfig              `json:"channels"` // All channel configs (telegram, http, tui)
	Session       session.SessionConfig       `json:"session"`
	Memory        memory.MemorySearchConfig   `json:"memory"`
	MemoryGraph   memorygraph.Config          `json:"memoryGraph"`
	Transcript    transcript.TranscriptConfig `json:"transcript"`
	PromptCache   gwtypes.PromptCacheConfig   `json:"promptCache"`
	Media         media.MediaConfig           `json:"media"`
	STT           stt.Config                  `json:"stt"`
	Skills        skills.SkillsConfig         `json:"skills"`
	Cron          cron.CronConfig             `json:"cron"`
	Supervision   gwtypes.SupervisionConfig   `json:"supervision"`
	Roles         user.RolesConfig            `json:"roles"`    // Role-based access control
	Auth          auth.AuthConfig             `json:"auth"`     // Role elevation authentication
	Sandbox       sandbox.Config              `json:"sandbox"`  // Sandbox and bubblewrap configuration
	Safety        gwtypes.SafetyConfig        `json:"safety"`   // Emergency stop / panic phrase config
	Security      gwtypes.SecurityConfig      `json:"security"` // Security policies (tool restrictions per purpose)
}

// Load reads configuration from goclaw.json.
// If no config file exists, returns an error directing user to run 'goclaw setup'.
func Load() (*LoadResult, error) {
	home, _ := os.UserHomeDir()
	goclawDir, _ := paths.BaseDir()
	goclawGlobalPath, _ := paths.DefaultConfigPath()
	goclawLocalPath := "goclaw.json" // current working directory

	logging.L_debug("config: checking files", "goclawDir", goclawDir, "cwd", mustGetwd())

	// Determine which goclaw.json to use (local takes priority)
	var goclawPath string
	var goclawData []byte
	var goclawExists bool

	if data, err := os.ReadFile(goclawLocalPath); err == nil {
		absPath, _ := filepath.Abs(goclawLocalPath)
		goclawPath = absPath
		goclawData = data
		goclawExists = true
		logging.L_debug("config: found local goclaw.json", "path", absPath, "size", len(data))
	} else if data, err := os.ReadFile(goclawGlobalPath); err == nil {
		goclawPath = goclawGlobalPath
		goclawData = data
		goclawExists = true
		logging.L_debug("config: found global goclaw.json", "path", goclawGlobalPath, "size", len(data))
	}

	// No config found - tell user to run setup
	if !goclawExists {
		return nil, fmt.Errorf("no goclaw.json configuration found. Run 'goclaw setup' to create one")
	}

	// Check for minimal/empty config
	if isMinimalJSON(goclawData) {
		return nil, fmt.Errorf("goclaw.json is empty or incomplete. Run 'goclaw setup' to configure")
	}

	logging.L_debug("config: loading from goclaw.json")

	// Initialize config with struct tag defaults
	cfg := &Config{}
	if err := defaults.Set(cfg); err != nil {
		return nil, fmt.Errorf("failed to set config defaults: %w", err)
	}

	goclawData, err := migrateSandboxConfigJSON(goclawData)
	if err != nil {
		return nil, fmt.Errorf("migrate sandbox config: %w", err)
	}
	goclawData, err = migrateACPConfigJSON(goclawData)
	if err != nil {
		return nil, fmt.Errorf("migrate ACP config: %w", err)
	}

	// Unmarshal JSON - only overwrites fields actually present in the JSON
	if err := json.Unmarshal(goclawData, cfg); err != nil {
		logging.L_error("config: failed to parse goclaw.json", "path", goclawPath, "error", err)
		return nil, formatJSONError(goclawData, err)
	}
	logging.L_debug("config: loaded from goclaw.json", "path", goclawPath)

	// Apply runtime defaults that cannot be struct tags (paths, slices, maps)
	applyRuntimeDefaults(cfg, goclawDir, home)
	llm.EnsureBuiltInEmbeddingProvider(&cfg.LLM)
	if err := normalizeTildePaths(cfg); err != nil {
		return nil, fmt.Errorf("normalize config paths: %w", err)
	}

	// Log final config summary
	agentModel := ""
	if len(cfg.LLM.Agent.Models) > 0 {
		agentModel = cfg.LLM.Agent.Models[0]
	}
	logging.L_debug("config: loaded",
		"agentModel", agentModel,
		"providers", len(cfg.LLM.Providers),
		"telegramEnabled", cfg.Channels.Telegram.Enabled,
		"workingDir", cfg.Gateway.WorkingDir,
	)

	return &LoadResult{
		Config:     cfg,
		SourcePath: goclawPath,
	}, nil
}

// LoadFromPath reads configuration from an explicit goclaw.json path.
// Unlike Load(), this never searches local/global fallback locations.
func LoadFromPath(path string) (*LoadResult, error) {
	if path == "" {
		return nil, fmt.Errorf("config path is required")
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolving config path: %w", err)
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("no goclaw.json found at %s", absPath)
		}
		return nil, fmt.Errorf("reading goclaw.json at %s: %w", absPath, err)
	}

	if isMinimalJSON(data) {
		return nil, fmt.Errorf("goclaw.json at %s is empty or incomplete. Run 'goclaw setup' to configure", absPath)
	}

	home, _ := os.UserHomeDir()
	goclawDir, _ := paths.BaseDir()

	cfg := &Config{}
	if err := defaults.Set(cfg); err != nil {
		return nil, fmt.Errorf("failed to set config defaults: %w", err)
	}
	data, err = migrateSandboxConfigJSON(data)
	if err != nil {
		return nil, fmt.Errorf("migrate sandbox config: %w", err)
	}
	data, err = migrateACPConfigJSON(data)
	if err != nil {
		return nil, fmt.Errorf("migrate ACP config: %w", err)
	}
	if err := json.Unmarshal(data, cfg); err != nil {
		logging.L_error("config: failed to parse explicit config path", "path", absPath, "error", err)
		return nil, formatJSONError(data, err)
	}

	applyRuntimeDefaults(cfg, goclawDir, home)
	llm.EnsureBuiltInEmbeddingProvider(&cfg.LLM)
	if err := normalizeTildePaths(cfg); err != nil {
		return nil, fmt.Errorf("normalize config paths: %w", err)
	}

	return &LoadResult{
		Config:     cfg,
		SourcePath: absPath,
	}, nil
}

// LoadDefaults builds a fully defaulted Config without reading from disk.
// This is useful for setup flows that need the real config defaults even when
// no goclaw.json exists yet.
func LoadDefaults() (*LoadResult, error) {
	home, _ := os.UserHomeDir()
	goclawDir, _ := paths.BaseDir()

	cfg := &Config{}
	if err := defaults.Set(cfg); err != nil {
		return nil, fmt.Errorf("failed to set config defaults: %w", err)
	}
	applyRuntimeDefaults(cfg, goclawDir, home)
	llm.EnsureBuiltInEmbeddingProvider(&cfg.LLM)
	if err := normalizeTildePaths(cfg); err != nil {
		return nil, fmt.Errorf("normalize config paths: %w", err)
	}

	return &LoadResult{
		Config:     cfg,
		SourcePath: "",
	}, nil
}

// LoadRuntime reads configuration from goclaw.json with environment variable expansion.
// Use this for gateway startup and CLI commands that execute functionality.
// Use Load() instead for setup wizard/editor where ${VAR} placeholders must be preserved.
//
// Environment variables are referenced using ${VAR_NAME} syntax. If any referenced
// variable is not set, an error is returned.
func LoadRuntime() (*LoadResult, error) {
	home, _ := os.UserHomeDir()
	goclawDir, _ := paths.BaseDir()
	goclawGlobalPath, _ := paths.DefaultConfigPath()
	goclawLocalPath := "goclaw.json"

	logging.L_debug("config: checking files (runtime)", "goclawDir", goclawDir, "cwd", mustGetwd())

	// Determine which goclaw.json to use (local takes priority)
	var goclawPath string
	var goclawData []byte
	var goclawExists bool

	if data, err := os.ReadFile(goclawLocalPath); err == nil {
		absPath, _ := filepath.Abs(goclawLocalPath)
		goclawPath = absPath
		goclawData = data
		goclawExists = true
		logging.L_debug("config: found local goclaw.json", "path", absPath, "size", len(data))
	} else if data, err := os.ReadFile(goclawGlobalPath); err == nil {
		goclawPath = goclawGlobalPath
		goclawData = data
		goclawExists = true
		logging.L_debug("config: found global goclaw.json", "path", goclawGlobalPath, "size", len(data))
	}

	if !goclawExists {
		return nil, fmt.Errorf("no goclaw.json configuration found. Run 'goclaw setup' to create one")
	}

	if isMinimalJSON(goclawData) {
		return nil, fmt.Errorf("goclaw.json is empty or incomplete. Run 'goclaw setup' to configure")
	}

	// Expand ${VAR} environment variable references
	if HasEnvVars(goclawData) {
		logging.L_debug("config: expanding environment variables")
		expanded, err := ExpandEnvVars(goclawData)
		if err != nil {
			return nil, err
		}
		goclawData = expanded
	}

	logging.L_debug("config: loading from goclaw.json (runtime)")

	cfg := &Config{}
	if err := defaults.Set(cfg); err != nil {
		return nil, fmt.Errorf("failed to set config defaults: %w", err)
	}

	migratedData, err := migrateSandboxConfigJSON(goclawData)
	if err != nil {
		return nil, fmt.Errorf("migrate sandbox config: %w", err)
	}
	goclawData = migratedData
	goclawData, err = migrateACPConfigJSON(goclawData)
	if err != nil {
		return nil, fmt.Errorf("migrate ACP config: %w", err)
	}

	if err := json.Unmarshal(goclawData, cfg); err != nil {
		logging.L_error("config: failed to parse goclaw.json", "path", goclawPath, "error", err)
		return nil, formatJSONError(goclawData, err)
	}
	logging.L_debug("config: loaded from goclaw.json (runtime)", "path", goclawPath)

	applyRuntimeDefaults(cfg, goclawDir, home)
	llm.EnsureBuiltInEmbeddingProvider(&cfg.LLM)
	if err := normalizeTildePaths(cfg); err != nil {
		return nil, fmt.Errorf("normalize config paths: %w", err)
	}

	agentModel := ""
	if len(cfg.LLM.Agent.Models) > 0 {
		agentModel = cfg.LLM.Agent.Models[0]
	}
	logging.L_debug("config: loaded (runtime)",
		"agentModel", agentModel,
		"providers", len(cfg.LLM.Providers),
		"telegramEnabled", cfg.Channels.Telegram.Enabled,
		"workingDir", cfg.Gateway.WorkingDir,
	)

	// Store loaded path for setup/editor access
	loadedConfigPath = goclawPath

	return &LoadResult{
		Config:     cfg,
		SourcePath: goclawPath,
	}, nil
}

// applyRuntimeDefaults sets defaults that cannot be expressed as struct tags
// (file paths, slices, maps, function-derived values)
func applyRuntimeDefaults(cfg *Config, goclawDir, home string) {
	// Path defaults
	if cfg.Gateway.LogFile == "" {
		cfg.Gateway.LogFile = filepath.Join(goclawDir, "goclaw.log")
	}
	if cfg.Gateway.PIDFile == "" {
		cfg.Gateway.PIDFile = filepath.Join(goclawDir, "goclaw.pid")
	}
	if cfg.Gateway.WorkingDir == "" {
		cfg.Gateway.WorkingDir = filepath.Join(goclawDir, "workspace")
	}
	if cfg.Session.StorePath == "" {
		cfg.Session.StorePath = filepath.Join(goclawDir, "sessions.db")
	}
	if cfg.Session.InheritPath == "" {
		cfg.Session.InheritPath = filepath.Join(home, ".openclaw", "agents", "main", "sessions")
	}
	if cfg.A2A.DefaultTransport == "" {
		cfg.A2A.DefaultTransport = a2a.DefaultTransportLibp2p
	}
	if cfg.A2A.Libp2p.Identity.KeyFile == "" {
		cfg.A2A.Libp2p.Identity.KeyFile = a2a.DefaultIdentityKeyFile
	}
	if cfg.A2A.Libp2p.Identity.KeyType == "" {
		cfg.A2A.Libp2p.Identity.KeyType = "ed25519"
	}
	if cfg.A2A.Libp2p.ListenAddrs == nil {
		cfg.A2A.Libp2p.ListenAddrs = []string{a2a.DefaultLocalListenTCP, a2a.DefaultLocalListenQUIC}
	}
	if cfg.A2A.Libp2p.AnnounceAddrs == nil {
		cfg.A2A.Libp2p.AnnounceAddrs = []string{}
	}
	if cfg.A2A.Libp2p.BootstrapPeers == nil {
		cfg.A2A.Libp2p.BootstrapPeers = []string{}
	}
	if cfg.A2A.Libp2p.Discovery.ServiceName == "" {
		cfg.A2A.Libp2p.Discovery.ServiceName = a2a.DefaultRendezvousNS
	}
	if cfg.A2A.Libp2p.Discovery.RendezvousNamespace == "" {
		cfg.A2A.Libp2p.Discovery.RendezvousNamespace = a2a.DefaultRendezvousNS
	}
	if cfg.A2A.Libp2p.Discovery.RendezvousAdmissionMode == "" {
		cfg.A2A.Libp2p.Discovery.RendezvousAdmissionMode = a2a.DefaultRendezvousAdmissionMode
	}
	if cfg.A2A.Libp2p.Discovery.BootstrapSeedTXT == "" {
		cfg.A2A.Libp2p.Discovery.BootstrapSeedTXT = a2a.DefaultBootstrapSeedTXT
	}
	if cfg.A2A.Libp2p.Discovery.RegisterIntervalSecs == 0 {
		cfg.A2A.Libp2p.Discovery.RegisterIntervalSecs = 30
	}
	if cfg.A2A.Libp2p.Discovery.QueryIntervalSecs == 0 {
		cfg.A2A.Libp2p.Discovery.QueryIntervalSecs = 30
	}
	if cfg.A2A.Libp2p.Protocol.RPCProtocolID == "" {
		cfg.A2A.Libp2p.Protocol.RPCProtocolID = a2a.DefaultRPCProtocolID
	}
	if cfg.A2A.Libp2p.Protocol.RendezvousProtocolID == "" {
		cfg.A2A.Libp2p.Protocol.RendezvousProtocolID = a2a.DefaultRendezvousID
	}
	if cfg.A2A.Libp2p.Protocol.StateRetentionSecs == 0 {
		cfg.A2A.Libp2p.Protocol.StateRetentionSecs = a2a.DefaultRetentionSeconds
	}

	// Map defaults
	if cfg.LLM.Providers == nil {
		cfg.LLM.Providers = map[string]llm.LLMProviderConfig{
			"anthropic": {
				Driver:        "anthropic",
				PromptCaching: true,
			},
		}
	}
	if cfg.LLM.Agent.Models == nil {
		cfg.LLM.Agent.Models = []string{"anthropic/claude-sonnet-4-20250514"}
	}
	if cfg.Tools.Browser.ProfileDomains == nil {
		cfg.Tools.Browser.ProfileDomains = map[string]string{}
	}
	if cfg.Skills.Entries == nil {
		cfg.Skills.Entries = make(map[string]skills.SkillEntryConfig)
	}

	// Slice defaults
	if cfg.Sandbox.Bubblewrap.Volumes == nil {
		cfg.Sandbox.Bubblewrap.Volumes = sandbox.DefaultVolumes()
	}
	if cfg.MemoryGraph.LiveExtraction.ExcludeSources == nil {
		cfg.MemoryGraph.LiveExtraction.ExcludeSources = memorygraph.DefaultExcludeSources()
	}

	// Backward compatibility: hydrate new multi-provider web search key path from
	// legacy tools.web.braveApiKey so config forms can display existing values.
	if strings.TrimSpace(cfg.Tools.Web.Search.Providers.Brave.APIKey) == "" &&
		strings.TrimSpace(cfg.Tools.Web.BraveAPIKey) != "" {
		cfg.Tools.Web.Search.Providers.Brave.APIKey = cfg.Tools.Web.BraveAPIKey
	}

	if cfg.Session.Summarization.Checkpoint.Thresholds == nil {
		cfg.Session.Summarization.Checkpoint.Thresholds = []int{25, 50, 75}
	}
	if cfg.Session.MemoryFlush.Thresholds == nil {
		cfg.Session.MemoryFlush.Thresholds = []session.FlushThreshold{
			{
				Percent:      50,
				Prompt:       "Context at 50%. Consider noting key decisions to memory.",
				InjectAs:     session.FlushInjectSystem,
				OncePerCycle: true,
			},
			{
				Percent:      75,
				Prompt:       "Context at 75%. Write important context to memory/YYYY-MM-DD.md now.",
				InjectAs:     session.FlushInjectSystem,
				OncePerCycle: true,
			},
			{
				Percent:      90,
				Prompt:       "[Context pressure: 90%] Compaction imminent.\nBefore responding, save important session context to memory/YYYY-MM-DD.md (create memory/ if needed).\nSave: key decisions, user-shared context, current work state.\nSkip: secrets, trivial details, info already in files.\nAfter saving (or if nothing to save), respond to the user's message normally.",
				InjectAs:     session.FlushInjectSystem,
				OncePerCycle: true,
			},
		}
	}

	cfg.Media.Normalize()
}

func migrateSandboxConfigJSON(data []byte) ([]byte, error) {
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}

	sandboxNode, ok := raw["sandbox"].(map[string]any)
	if !ok {
		return data, nil
	}

	generalNode, _ := sandboxNode["general"].(map[string]any)
	if generalNode == nil {
		generalNode = map[string]any{}
		sandboxNode["general"] = generalNode
	}

	bubblewrapNode, _ := sandboxNode["bubblewrap"].(map[string]any)
	if bubblewrapNode == nil {
		bubblewrapNode = map[string]any{}
		sandboxNode["bubblewrap"] = bubblewrapNode
	}

	seatbeltNode, _ := sandboxNode["seatbelt"].(map[string]any)
	if seatbeltNode == nil {
		seatbeltNode = map[string]any{}
		sandboxNode["seatbelt"] = seatbeltNode
	}

	moveIfMissing(generalNode, "mode", bubblewrapNode, "mode")
	moveIfMissing(generalNode, "dataDir", bubblewrapNode, "dataDir")
	moveIfMissing(generalNode, "extraPaths", bubblewrapNode, "extraPaths")

	if _, ok := generalNode["execEnabled"]; !ok {
		if enabled, ok := nestedValue(raw, "tools", "exec", "bubblewrap", "enabled"); ok {
			generalNode["execEnabled"] = enabled
		}
	}
	if _, ok := generalNode["browserEnabled"]; !ok {
		if enabled, ok := nestedValue(raw, "tools", "browser", "bubblewrap", "enabled"); ok {
			generalNode["browserEnabled"] = enabled
		}
	}

	if _, ok := seatbeltNode["path"]; !ok {
		if path, ok := bubblewrapNode["path"]; ok {
			seatbeltNode["path"] = path
		}
	}

	raw["sandbox"] = sandboxNode
	return json.Marshal(raw)
}

func migrateACPConfigJSON(data []byte) ([]byte, error) {
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	gatewayNode, ok := raw["gateway"].(map[string]any)
	if !ok {
		return data, nil
	}
	legacyModel, _ := gatewayNode["acpCursorModel"].(string)
	legacyModel = strings.TrimSpace(legacyModel)
	if legacyModel == "" {
		return data, nil
	}
	acpNode, _ := raw["acp"].(map[string]any)
	if acpNode == nil {
		acpNode = map[string]any{}
		raw["acp"] = acpNode
	}
	driversNode, _ := acpNode["drivers"].(map[string]any)
	if driversNode == nil {
		driversNode = map[string]any{}
		acpNode["drivers"] = driversNode
	}
	cursorNode, _ := driversNode["cursor"].(map[string]any)
	if cursorNode == nil {
		cursorNode = map[string]any{}
		driversNode["cursor"] = cursorNode
	}
	currentModel, _ := cursorNode["model"].(string)
	if strings.TrimSpace(currentModel) == "" {
		cursorNode["model"] = legacyModel
	}
	return json.Marshal(raw)
}

func moveIfMissing(dst map[string]any, dstKey string, src map[string]any, srcKey string) {
	if _, ok := dst[dstKey]; ok {
		return
	}
	if value, ok := src[srcKey]; ok {
		dst[dstKey] = value
	}
}

func nestedValue(root map[string]any, path ...string) (any, bool) {
	var current any = root
	for _, part := range path {
		node, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = node[part]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

// normalizeTildePaths expands "~" for path-like configuration fields.
// This keeps runtime behavior consistent across all subsystems and avoids
// creating literal "./~/" directories when a caller uses shell-style paths.
func normalizeTildePaths(cfg *Config) error {
	if cfg == nil {
		return nil
	}
	return normalizeTildeValue(reflect.ValueOf(cfg).Elem(), "config")
}

func normalizeTildeValue(v reflect.Value, fieldPath string) error {
	if !v.IsValid() {
		return nil
	}

	switch v.Kind() {
	case reflect.Ptr:
		if v.IsNil() {
			return nil
		}
		return normalizeTildeValue(v.Elem(), fieldPath)

	case reflect.Struct:
		t := v.Type()
		for i := 0; i < v.NumField(); i++ {
			fieldInfo := t.Field(i)
			if fieldInfo.PkgPath != "" {
				continue
			}
			childPath := fieldPath + "." + configFieldName(fieldInfo)
			if err := normalizeTildeField(v.Field(i), childPath, fieldInfo); err != nil {
				return err
			}
		}
	}

	return nil
}

func normalizeTildeField(v reflect.Value, fieldPath string, fieldInfo reflect.StructField) error {
	if !v.IsValid() {
		return nil
	}

	switch v.Kind() {
	case reflect.Ptr:
		if v.IsNil() {
			return nil
		}
		return normalizeTildeField(v.Elem(), fieldPath, fieldInfo)

	case reflect.Struct:
		return normalizeTildeValue(v, fieldPath)

	case reflect.String:
		if !isPathLikeField(fieldInfo) {
			return nil
		}
		return expandTildeString(v, fieldPath)

	case reflect.Slice:
		if v.Type().Elem().Kind() == reflect.String && isPathLikeField(fieldInfo) {
			for i := 0; i < v.Len(); i++ {
				if err := expandTildeString(v.Index(i), fmt.Sprintf("%s[%d]", fieldPath, i)); err != nil {
					return err
				}
			}
			return nil
		}
		for i := 0; i < v.Len(); i++ {
			if err := normalizeTildeValue(v.Index(i), fmt.Sprintf("%s[%d]", fieldPath, i)); err != nil {
				return err
			}
		}
		return nil

	case reflect.Map:
		iter := v.MapRange()
		for iter.Next() {
			key := iter.Key()
			value := iter.Value()
			if !value.IsValid() {
				continue
			}
			copyValue := reflect.New(value.Type()).Elem()
			copyValue.Set(value)
			if err := normalizeTildeValue(copyValue, fmt.Sprintf("%s[%v]", fieldPath, key.Interface())); err != nil {
				return err
			}
			v.SetMapIndex(key, copyValue)
		}
		return nil
	}

	return nil
}

func expandTildeString(v reflect.Value, fieldPath string) error {
	if !v.CanSet() {
		return nil
	}
	current := v.String()
	if !strings.HasPrefix(current, "~") {
		return nil
	}
	expanded, err := paths.ExpandTilde(current)
	if err != nil {
		return fmt.Errorf("%s: %w", fieldPath, err)
	}
	v.SetString(expanded)
	return nil
}

func configFieldName(fieldInfo reflect.StructField) string {
	tag := fieldInfo.Tag.Get("json")
	if tag != "" {
		name := strings.Split(tag, ",")[0]
		if name != "" && name != "-" {
			return name
		}
	}
	return fieldInfo.Name
}

func isPathLikeField(fieldInfo reflect.StructField) bool {
	name := strings.ToLower(configFieldName(fieldInfo))
	return strings.Contains(name, "path") ||
		strings.Contains(name, "dir") ||
		strings.Contains(name, "file") ||
		name == "script" ||
		strings.Contains(name, "volume")
}

// DefaultConfigTemplate is a minimal config struct for template generation.
// Only includes fields that users typically need to customize.
// The full defaults are applied by Load() when merging.
type DefaultConfigTemplate struct {
	A2A      a2a.Config              `json:"a2a,omitempty"`
	LLM      DefaultLLMTemplate      `json:"llm"`
	Gateway  DefaultGatewayTemplate  `json:"gateway,omitempty"`
	ACP      acp.Config              `json:"acp,omitempty"`
	Channels DefaultChannelsTemplate `json:"channels,omitempty"`
	Roles    user.RolesConfig        `json:"roles,omitempty"`
}

type DefaultLLMTemplate struct {
	Providers  map[string]llm.LLMProviderConfig `json:"providers"`
	Agent      llm.LLMPurposeConfig             `json:"agent"`
	Embeddings llm.LLMPurposeConfig             `json:"embeddings,omitempty"`
}

type DefaultGatewayTemplate struct {
	WorkingDir string `json:"workingDir,omitempty"`
}

type DefaultChannelsTemplate struct {
	HTTP DefaultHTTPTemplate `json:"http,omitempty"`
}

type DefaultHTTPTemplate struct {
	Listen string `json:"listen,omitempty"`
}

// DefaultConfig returns a minimal config template with sensible defaults.
// Only includes fields that users typically need to customize.
// The apiKey field has a placeholder that must be replaced.
func DefaultConfig() *DefaultConfigTemplate {
	return &DefaultConfigTemplate{
		A2A: a2a.Config{
			Enabled:          false,
			DefaultTransport: a2a.DefaultTransportLibp2p,
			Libp2p: a2a.Libp2pConfig{
				Enabled:        false,
				ListenAddrs:    []string{a2a.DefaultLocalListenTCP, a2a.DefaultLocalListenQUIC},
				AnnounceAddrs:  []string{},
				NATPortMap:     true,
				BootstrapPeers: []string{},
				Identity: a2a.IdentityConfig{
					KeyFile: a2a.DefaultIdentityKeyFile,
					KeyType: "ed25519",
				},
				Discovery: a2a.DiscoveryConfig{
					RendezvousEnabled:       true,
					RendezvousNamespace:     a2a.DefaultRendezvousNS,
					RendezvousAdmissionMode: a2a.DefaultRendezvousAdmissionMode,
					BootstrapSeedTXT:        a2a.DefaultBootstrapSeedTXT,
					ServiceName:             a2a.DefaultRendezvousNS,
					RegisterIntervalSecs:    30,
					QueryIntervalSecs:       30,
				},
				Relay: a2a.RelayConfig{
					EnableClient:    true,
					EnableServer:    false,
					EnableAutoRelay: true,
					EnableHolePunch: true,
				},
				Protocol: a2a.ProtocolConfig{
					RPCProtocolID:        a2a.DefaultRPCProtocolID,
					RendezvousProtocolID: a2a.DefaultRendezvousID,
					StateRetentionSecs:   a2a.DefaultRetentionSeconds,
				},
			},
		},
		LLM: DefaultLLMTemplate{
			Providers: map[string]llm.LLMProviderConfig{
				"anthropic": {
					Driver:        "anthropic",
					APIKey:        "YOUR_ANTHROPIC_API_KEY",
					PromptCaching: true,
				},
				llm.BuiltInHugotProviderAlias: {
					Driver:        "hugot",
					EmbeddingOnly: true,
					Subtype:       "hugot",
				},
			},
			Agent: llm.LLMPurposeConfig{
				Models: []string{"anthropic/claude-sonnet-4-20250514"},
			},
			Embeddings: llm.LLMPurposeConfig{
				Models: []string{llm.BuiltInHugotProviderAlias + "/" + llm.DefaultHugotEmbeddingModel},
			},
		},
		Gateway: DefaultGatewayTemplate{
			WorkingDir: "~/.goclaw/workspace",
		},
		ACP: acp.Config{
			DefaultDriver: acp.DriverCursor,
			Drivers: acp.DriversConfig{
				Cursor: acp.CursorConfig{
					Model: acp.DefaultCursorModel,
				},
			},
		},
		Channels: DefaultChannelsTemplate{
			HTTP: DefaultHTTPTemplate{
				Listen: ":1337",
			},
		},
		Roles: user.RolesConfig{
			"owner": user.RoleConfig{
				Tools:       "*",
				Skills:      "*",
				Memory:      "full",
				Transcripts: "all",
				Commands:    true,
			},
			"user": user.RoleConfig{
				Tools:       []interface{}{"read_file", "write_file", "web_search", "web_fetch"},
				Skills:      "*",
				Memory:      "none",
				Transcripts: "own",
				Commands:    true,
			},
		},
	}
}

// mustGetwd returns the current working directory or "unknown" on error
func mustGetwd() string {
	if cwd, err := os.Getwd(); err == nil {
		return cwd
	}
	return "unknown"
}
