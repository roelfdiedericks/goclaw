package main

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	osexec "os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/alecthomas/kong"
	"github.com/go-rod/rod/lib/proto"
	_ "github.com/mattn/go-sqlite3"
	"github.com/sevlyar/go-daemon"
	"golang.org/x/term"

	"github.com/roelfdiedericks/goclaw/internal/a2a"
	a2ainfratui "github.com/roelfdiedericks/goclaw/internal/a2a/infratui"
	"github.com/roelfdiedericks/goclaw/internal/a2apeers"
	"github.com/roelfdiedericks/goclaw/internal/acp"
	"github.com/roelfdiedericks/goclaw/internal/auth"
	"github.com/roelfdiedericks/goclaw/internal/browser"
	"github.com/roelfdiedericks/goclaw/internal/bus"
	"github.com/roelfdiedericks/goclaw/internal/channels"
	goclawhttp "github.com/roelfdiedericks/goclaw/internal/channels/http"
	httpconfig "github.com/roelfdiedericks/goclaw/internal/channels/http/config"
	"github.com/roelfdiedericks/goclaw/internal/channels/telegram"
	telegramconfig "github.com/roelfdiedericks/goclaw/internal/channels/telegram/config"
	"github.com/roelfdiedericks/goclaw/internal/channels/tui"
	tuiconfig "github.com/roelfdiedericks/goclaw/internal/channels/tui/config"
	"github.com/roelfdiedericks/goclaw/internal/channels/whatsapp"
	"github.com/roelfdiedericks/goclaw/internal/config"
	"github.com/roelfdiedericks/goclaw/internal/configapply"
	"github.com/roelfdiedericks/goclaw/internal/cron"
	"github.com/roelfdiedericks/goclaw/internal/delivery"
	"github.com/roelfdiedericks/goclaw/internal/embeddings"
	"github.com/roelfdiedericks/goclaw/internal/gateway"
	"github.com/roelfdiedericks/goclaw/internal/hass"
	"github.com/roelfdiedericks/goclaw/internal/llm"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/media"
	"github.com/roelfdiedericks/goclaw/internal/memorygraph"
	"github.com/roelfdiedericks/goclaw/internal/metrics"
	"github.com/roelfdiedericks/goclaw/internal/paths"
	"github.com/roelfdiedericks/goclaw/internal/runtimeinfo"
	"github.com/roelfdiedericks/goclaw/internal/sandbox"
	sbruntime "github.com/roelfdiedericks/goclaw/internal/sandbox/runtime"
	"github.com/roelfdiedericks/goclaw/internal/session"
	"github.com/roelfdiedericks/goclaw/internal/setup"
	setupweb "github.com/roelfdiedericks/goclaw/internal/setup/web"
	"github.com/roelfdiedericks/goclaw/internal/skills"
	"github.com/roelfdiedericks/goclaw/internal/stt"
	"github.com/roelfdiedericks/goclaw/internal/supervisor"
	"github.com/roelfdiedericks/goclaw/internal/tools"
	toolacpcontrol "github.com/roelfdiedericks/goclaw/internal/tools/acp_control"
	toolacpinspect "github.com/roelfdiedericks/goclaw/internal/tools/acp_inspect"
	toolcron "github.com/roelfdiedericks/goclaw/internal/tools/cron"
	"github.com/roelfdiedericks/goclaw/internal/tools/edit"
	"github.com/roelfdiedericks/goclaw/internal/tools/exec"
	toolhass "github.com/roelfdiedericks/goclaw/internal/tools/hass"
	"github.com/roelfdiedericks/goclaw/internal/tools/jq"
	toolmedia "github.com/roelfdiedericks/goclaw/internal/tools/media"
	toolmediadisplay "github.com/roelfdiedericks/goclaw/internal/tools/media_display"
	"github.com/roelfdiedericks/goclaw/internal/tools/memoryget"
	toolmemorygraph "github.com/roelfdiedericks/goclaw/internal/tools/memorygraph"
	"github.com/roelfdiedericks/goclaw/internal/tools/memorysearch"
	toolmessage "github.com/roelfdiedericks/goclaw/internal/tools/message"
	"github.com/roelfdiedericks/goclaw/internal/tools/read"
	toolskills "github.com/roelfdiedericks/goclaw/internal/tools/skills"
	toolsubagentcancel "github.com/roelfdiedericks/goclaw/internal/tools/subagent_cancel"
	toolsubagentfanout "github.com/roelfdiedericks/goclaw/internal/tools/subagent_fanout"
	toolsubagentspawn "github.com/roelfdiedericks/goclaw/internal/tools/subagent_spawn"
	toolsubagentstatus "github.com/roelfdiedericks/goclaw/internal/tools/subagent_status"
	tooltranscript "github.com/roelfdiedericks/goclaw/internal/tools/transcript"
	toolupdate "github.com/roelfdiedericks/goclaw/internal/tools/update"
	"github.com/roelfdiedericks/goclaw/internal/tools/userauth"
	"github.com/roelfdiedericks/goclaw/internal/tools/webfetch"
	"github.com/roelfdiedericks/goclaw/internal/tools/websearch"
	"github.com/roelfdiedericks/goclaw/internal/tools/write"
	"github.com/roelfdiedericks/goclaw/internal/tools/xaiimagine"
	"github.com/roelfdiedericks/goclaw/internal/tools/xaivideo"
	"github.com/roelfdiedericks/goclaw/internal/transcript"
	"github.com/roelfdiedericks/goclaw/internal/update"
	"github.com/roelfdiedericks/goclaw/internal/user"
)

// version is set by goreleaser via ldflags: -X main.version=...
// Default "dev" indicates a local/non-release build
var version = "dev"

// RuntimePaths holds derived paths for daemon operation
type RuntimePaths struct {
	DataDir string // Directory for all runtime files
	PidFile string
	LogFile string
}

type RuntimeStatus struct {
	Configured               bool       `json:"configured"`
	Running                  bool       `json:"running"`
	ConfigPath               string     `json:"configPath,omitempty"`
	DataDir                  string     `json:"dataDir,omitempty"`
	PidFile                  string     `json:"pidFile,omitempty"`
	LogFile                  string     `json:"logFile,omitempty"`
	Version                  string     `json:"version"`
	SupervisorPID            int        `json:"supervisorPid,omitempty"`
	GatewayPID               int        `json:"gatewayPid,omitempty"`
	StartedAt                *time.Time `json:"startedAt,omitempty"`
	Uptime                   string     `json:"uptime,omitempty"`
	UptimeSeconds            int64      `json:"uptimeSeconds,omitempty"`
	CrashCount               int        `json:"crashCount,omitempty"`
	LastCrashAt              *time.Time `json:"lastCrashAt,omitempty"`
	SupervisorStateAvailable bool       `json:"supervisorStateAvailable"`
}

var (
	runtimePathsLoader = loadRuntimePaths
	runtimeStarter     = startDaemon
	runtimeStopper     = stopDaemon
	processExitWaiter  = waitForPIDExit
)

type postUpdateGateway interface {
	DeliverSystemMessage(ctx context.Context, userID string, msg delivery.SystemMessage) delivery.Report
	InvokeAgent(ctx context.Context, source, purpose, message, suppressOn string) error
}

func readPostUpdateMarkerFromEnv() (*update.PostUpdateMarker, error) {
	return update.ReadPostUpdateMarkerFromEnv()
}

func ownerUsers(users *user.Registry) []*user.User {
	if users == nil {
		return nil
	}

	owners := make([]*user.User, 0, 1)
	for _, u := range users.List() {
		if u != nil && u.IsOwner() {
			owners = append(owners, u)
		}
	}
	return owners
}

func buildPostUpdateSystemMessage(state *update.PostUpdateMarker) string {
	content := fmt.Sprintf("GoClaw restarted successfully after a self-update and is now running version %s", state.NewVersion)
	if strings.TrimSpace(state.FromVersion) != "" {
		content += fmt.Sprintf(" (was %s)", state.FromVersion)
	}
	if strings.TrimSpace(state.Channel) != "" {
		content += fmt.Sprintf(" on the %s channel.", state.Channel)
	} else {
		content += "."
	}
	return content
}

func buildPostUpdateAgentPrompt(state *update.PostUpdateMarker) string {
	return fmt.Sprintf(
		"GoClaw restarted successfully after a self-update.\n\n"+
			"A deterministic system status message has already been sent to the owner channel(s).\n\n"+
			"Update details:\n"+
			"- Previous version: %s\n"+
			"- New version: %s\n"+
			"- Channel: %s\n"+
			"- Initiator: %s\n"+
			"- Restart marker time: %s\n\n"+
			"Send a concise user-facing follow-up acknowledging the successful update and restart. Mention the new version. Keep it short and useful. Do not reply with SILENT_OK.",
		state.FromVersion,
		state.NewVersion,
		state.Channel,
		state.Tool,
		state.Time.UTC().Format(time.RFC3339),
	)
}

func handlePostUpdateAfterStartup(ctx context.Context, gw postUpdateGateway, users *user.Registry, state *update.PostUpdateMarker) error {
	if state == nil || !state.Notify {
		return nil
	}
	defer update.ClearPostUpdateMarkerEnv()

	owners := ownerUsers(users)
	if len(owners) == 0 {
		return fmt.Errorf("no owner users configured for post-update notification")
	}

	content := buildPostUpdateSystemMessage(state)
	deliveredOwners := 0
	for _, owner := range owners {
		report := gw.DeliverSystemMessage(ctx, owner.ID, delivery.SystemMessage{
			Kind:    delivery.SystemKindStatus,
			Source:  "post-update",
			Title:   "GoClaw Updated",
			Content: content,
		})
		if report.Delivered() {
			deliveredOwners++
			continue
		}
		L_warn("post-update: deterministic owner notification was not delivered",
			"user", owner.ID,
			"results", len(report.Results),
		)
	}

	L_info("post-update: deterministic owner notification attempted",
		"owners", len(owners),
		"deliveredOwners", deliveredOwners,
		"newVersion", state.NewVersion,
	)

	if err := gw.InvokeAgent(ctx, "post_update", "agent", buildPostUpdateAgentPrompt(state), ""); err != nil {
		return fmt.Errorf("invoke post-update follow-up: %w", err)
	}

	return nil
}

// loadRuntimePaths loads config and derives all runtime paths from session.storePath
func loadRuntimePaths() (*RuntimePaths, error) {
	loadResult, err := config.LoadRuntime()
	if err != nil {
		// Don't wrap - config.LoadRuntime() error already includes "Run 'goclaw setup'" hint
		return nil, err
	}

	// Derive data directory from session store path
	storePath := loadResult.Config.Session.GetStorePath()
	dataDir := filepath.Dir(storePath)

	return &RuntimePaths{
		DataDir: dataDir,
		PidFile: filepath.Join(dataDir, "goclaw.pid"),
		LogFile: filepath.Join(dataDir, "goclaw.log"),
	}, nil
}

func runtimePathsFromStorePath(storePath string) *RuntimePaths {
	dataDir := filepath.Dir(storePath)
	return &RuntimePaths{
		DataDir: dataDir,
		PidFile: filepath.Join(dataDir, "goclaw.pid"),
		LogFile: filepath.Join(dataDir, "goclaw.log"),
	}
}

// CLI defines the command-line interface.
// Kong derives command names from field names via kebab-case. CamelCase word
// boundaries produce hyphens (e.g. WhatsApp → whats-app). To get a single
// word like "whatsapp", use a flat name: Whatsapp, not WhatsApp.
type CLI struct {
	Debug bool `help:"Enable debug logging" short:"d"`
	Trace bool `help:"Enable trace logging" short:"t"`

	Gateway    GatewayCmd    `cmd:"" help:"Run the gateway (foreground by default)"`
	Start      StartCmd      `cmd:"" help:"Start gateway as background daemon"`
	Stop       StopCmd       `cmd:"" help:"Stop the background daemon"`
	Restart    RestartCmd    `cmd:"" help:"Restart the background daemon"`
	Status     StatusCmd     `cmd:"" help:"Show gateway status"`
	Version    VersionCmd    `cmd:"" help:"Show version"`
	Update     UpdateCmd     `cmd:"" help:"Check for and install updates"`
	Cron       CronCmd       `cmd:"" help:"Manage cron jobs"`
	User       UserCmd       `cmd:"" help:"Manage users"`
	Whatsapp   WhatsAppCmd   `cmd:"" help:"Manage WhatsApp connection"`
	Browser    BrowserCmd    `cmd:"" help:"Manage browser (download, profiles, setup)"`
	Embeddings EmbeddingsCmd `cmd:"" help:"Manage embeddings (status, rebuild)"`
	Graph      GraphCmd      `cmd:"" help:"Memory graph operations (ingest, search, bulletin, stats)"`
	A2A        A2ACmd        `cmd:"" help:"A2A libp2p runtime, status, and infra modes"`
	Setup      SetupCmd      `cmd:"" help:"Interactive setup wizard"`
	Onboard    OnboardCmd    `cmd:"" help:"Run onboarding wizard"`
	Config     ConfigCmd     `cmd:"" help:"View configuration"`
	Sandbox    SandboxCmd    `cmd:"" help:"Sandbox diagnostics and interactive testing"`
	TUI        TUICmd        `cmd:"tui" help:"Run gateway with interactive TUI"`
}

// GatewayCmd runs gateway in foreground
type GatewayCmd struct {
	Run GatewayRunCmd `cmd:"" default:"withargs" help:"Run gateway in foreground"`
	TUI GatewayTUICmd `cmd:"tui" help:"Run gateway with interactive TUI"`
}

// GatewayRunCmd runs gateway in foreground (default)
type GatewayRunCmd struct {
	TUI bool `help:"Run with interactive TUI" short:"i" name:"interactive"`
	Dev bool `help:"Development mode: reload HTML templates from disk on each request"`
}

func (g *GatewayRunCmd) Run(ctx *Context) error {
	return runGateway(ctx, g.TUI, g.Dev)
}

// GatewayTUICmd runs gateway with TUI (goclaw gateway tui)
type GatewayTUICmd struct {
	Dev bool `help:"Development mode: reload HTML templates from disk on each request"`
}

func (g *GatewayTUICmd) Run(ctx *Context) error {
	return runGateway(ctx, true, g.Dev)
}

type A2ACmd struct {
	Status A2AStatusCmd `cmd:"" help:"Show configured A2A runtime status"`
	Libp2p A2ALibp2pCmd `cmd:"" help:"Run dedicated libp2p infra modes"`
}

type A2AStatusCmd struct{}

func (c *A2AStatusCmd) Run(ctx *Context) error {
	loadResult, err := config.LoadRuntime()
	if err != nil {
		return err
	}
	loadResult.Config.A2A.Normalize()
	peerRegistry, err := a2apeers.LoadForConfig(loadResult.SourcePath)
	if err != nil {
		return err
	}
	status := a2a.NewManager(loadResult.Config.A2A, nil, peerRegistry).Status()
	L_info("a2a status",
		"enabled", status.Enabled,
		"transport", status.ActiveTransport,
		"lifecycle", status.LifecycleState,
		"ready", status.Ready,
		"warmupComplete", status.WarmupComplete,
		"bootstrapPeers", status.BootstrapPeers,
		"trustedPeers", status.TrustedPeers,
		"rendezvousEnabled", status.RendezvousEnabled,
		"rendezvousNamespace", status.RendezvousNamespace,
	)
	return nil
}

type A2ALibp2pCmd struct {
	Bootstrap A2ALibp2pBootstrapCmd `cmd:"" help:"Run infra-only bootstrap and rendezvous mode"`
	Relay     A2ALibp2pRelayCmd     `cmd:"" help:"Run infra-only relay mode"`
	Both      A2ALibp2pBothCmd      `cmd:"" help:"Run infra-only bootstrap, rendezvous, and relay mode"`
}

type A2AInfraFlags struct {
	Port int  `help:"Override infra listen port for both TCP and QUIC."`
	TUI  bool `help:"Run a dedicated infra monitor TUI."`
}

type A2ALibp2pBootstrapCmd struct{ A2AInfraFlags }
type A2ALibp2pRelayCmd struct{ A2AInfraFlags }
type A2ALibp2pBothCmd struct{ A2AInfraFlags }

func (c *A2ALibp2pBootstrapCmd) Run(ctx *Context) error {
	return runA2AInfra(ctx, a2a.RuntimeModeBootstrap, c.Port, c.TUI)
}
func (c *A2ALibp2pRelayCmd) Run(ctx *Context) error {
	return runA2AInfra(ctx, a2a.RuntimeModeRelay, c.Port, c.TUI)
}
func (c *A2ALibp2pBothCmd) Run(ctx *Context) error {
	return runA2AInfra(ctx, a2a.RuntimeModeBoth, c.Port, c.TUI)
}

const a2aInfraMonitorInterval = 10 * time.Second

func runA2AInfra(ctx *Context, mode a2a.RuntimeMode, port int, useTUI bool) error {
	loadResult, err := config.LoadRuntime()
	if err != nil {
		return err
	}
	loadResult.Config.A2A.Enabled = true
	loadResult.Config.A2A.Libp2p.Enabled = true
	overrideInfraListenAddrs(&loadResult.Config.A2A, port)
	peerRegistry, err := a2apeers.LoadForConfig(loadResult.SourcePath)
	if err != nil {
		return err
	}
	manager := a2a.NewManager(loadResult.Config.A2A, nil, peerRegistry)
	runCtx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	if err := manager.StartInfra(runCtx, mode); err != nil {
		return err
	}
	status, err := awaitA2AInfraHostReady(runCtx, manager, 5*time.Second)
	if err != nil {
		return err
	}
	if useTUI {
		return a2ainfratui.Run(runCtx, manager, a2aInfraStartupLines(status))
	}
	L_info("a2a infra mode started",
		"mode", status.RuntimeMode,
		"lifecycle", status.LifecycleState,
		"ready", status.Ready,
		"peerID", status.LocalPeerID,
	)
	for _, addr := range status.AdvertisedAddrs {
		L_info("a2a infra advertise address", "addr", addr)
	}
	return monitorA2AInfraCLI(runCtx, manager)
}

func monitorA2AInfraCLI(ctx context.Context, manager *a2a.Manager) error {
	snapshot := manager.InfraSnapshot()
	lastFingerprint := snapshot.Fingerprint()
	logA2AInfraSnapshot(snapshot)

	ticker := time.NewTicker(a2aInfraMonitorInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			snapshot = manager.InfraSnapshot()
			fingerprint := snapshot.Fingerprint()
			if fingerprint == lastFingerprint {
				continue
			}
			lastFingerprint = fingerprint
			logA2AInfraSnapshot(snapshot)
		}
	}
}

func logA2AInfraSnapshot(snapshot a2a.InfraSnapshot) {
	L_info("a2a infra summary",
		"mode", snapshot.Status.RuntimeMode,
		"lifecycle", snapshot.Status.LifecycleState,
		"ready", snapshot.Status.Ready,
		"connected", snapshot.Summary.ConnectedPeers,
		"direct", snapshot.Summary.ConnectedDirectPeers,
		"relayed", snapshot.Summary.ConnectedRelayedPeers,
		"connectedByState", formatInfraCounts(snapshot.Summary.ConnectedPeerStateCount),
		"rendezvousEntries", snapshot.Summary.RendezvousEntries,
		"rendezvousNamespaces", snapshot.Summary.RendezvousNamespaces,
		"rendezvousByNamespace", formatInfraCounts(snapshot.Summary.RendezvousByNamespace),
	)
	if len(snapshot.Peers) == 0 {
		L_info("a2a infra connected peers", "count", 0)
	} else {
		for _, peer := range snapshot.Peers {
			L_info("a2a infra connected peer",
				"peerID", peer.PeerID,
				"alias", peer.Alias,
				"state", peer.State,
				"relayed", peer.Relayed,
				"authorized", peer.Authorized,
				"addrs", strings.Join(peer.Addrs, ", "),
			)
		}
	}
	if len(snapshot.Rendezvous) == 0 {
		L_info("a2a infra rendezvous entries", "count", 0)
		return
	}
	for _, namespace := range snapshot.Rendezvous {
		if len(namespace.Entries) == 0 {
			L_info("a2a infra rendezvous namespace", "namespace", namespace.Namespace, "entries", 0)
			continue
		}
		for _, entry := range namespace.Entries {
			L_info("a2a infra rendezvous entry",
				"namespace", entry.Namespace,
				"peerID", entry.PeerID,
				"expiresAt", entry.ExpiresAt.Format(time.RFC3339),
				"addrs", strings.Join(entry.Addrs, ", "),
			)
		}
	}
}

func formatInfraCounts(values map[string]int) string {
	if len(values) == 0 {
		return ""
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", key, values[key]))
	}
	return strings.Join(parts, ", ")
}

func a2aInfraStartupLines(status a2a.Status) []string {
	lines := []string{
		fmt.Sprintf("%s [INFO] a2a infra mode started mode=%s lifecycle=%s ready=%t peerID=%s",
			time.Now().Format("15:04:05"),
			status.RuntimeMode,
			status.LifecycleState,
			status.Ready,
			status.LocalPeerID,
		),
	}
	for _, addr := range status.AdvertisedAddrs {
		lines = append(lines, fmt.Sprintf("%s [INFO] a2a infra advertise address addr=%s", time.Now().Format("15:04:05"), addr))
	}
	return lines
}

func awaitA2AInfraHostReady(ctx context.Context, manager *a2a.Manager, timeout time.Duration) (a2a.Status, error) {
	deadline := time.Now().Add(timeout)
	for {
		status := manager.Status()
		if status.LifecycleState == a2a.LifecycleStateFailed {
			if strings.TrimSpace(status.LastError) != "" {
				return status, fmt.Errorf("A2A infra startup failed: %s", status.LastError)
			}
			return status, fmt.Errorf("A2A infra startup failed")
		}
		if status.Ready || status.LifecycleState == a2a.LifecycleStateRunning || status.LifecycleState == a2a.LifecycleStateDegraded {
			return status, nil
		}
		if time.Now().After(deadline) {
			L_info("a2a infra startup still in progress",
				"mode", status.RuntimeMode,
				"lifecycle", status.LifecycleState,
				"ready", status.Ready,
			)
			return status, nil
		}
		select {
		case <-ctx.Done():
			return status, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func overrideInfraListenAddrs(cfg *a2a.Config, port int) {
	if cfg == nil {
		return
	}
	useInfraDefaults := len(cfg.Libp2p.ListenAddrs) == 0 || usesLocalA2AListenDefaults(cfg.Libp2p.ListenAddrs)
	switch {
	case port > 0:
		cfg.Libp2p.ListenAddrs = []string{
			fmt.Sprintf("/ip4/0.0.0.0/tcp/%d", port),
			fmt.Sprintf("/ip4/0.0.0.0/udp/%d/quic-v1", port),
		}
	case useInfraDefaults:
		cfg.Libp2p.ListenAddrs = []string{a2a.DefaultListenTCP, a2a.DefaultListenQUIC}
	}
}

func usesLocalA2AListenDefaults(addrs []string) bool {
	if len(addrs) != 2 {
		return false
	}
	hasLocalTCP := false
	hasLocalQUIC := false
	for _, addr := range addrs {
		switch addr {
		case a2a.DefaultLocalListenTCP:
			hasLocalTCP = true
		case a2a.DefaultLocalListenQUIC:
			hasLocalQUIC = true
		}
	}
	return hasLocalTCP && hasLocalQUIC
}

// StartCmd daemonizes the gateway with supervision
type StartCmd struct{}

func (s *StartCmd) Run(ctx *Context) error {
	// Load config to get runtime paths
	paths, err := runtimePathsLoader()
	if err != nil {
		// Print user-friendly message (config.Load already includes setup hint)
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return err
	}

	return runtimeStarter(paths)
}

// StopCmd stops the daemon
type StopCmd struct{}

func (s *StopCmd) Run(ctx *Context) error {
	// Load config to get runtime paths
	paths, err := runtimePathsLoader()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return err
	}

	_, _, err = runtimeStopper(paths, true)
	return err
}

// StatusCmd shows gateway status
type StatusCmd struct {
	JSON  bool   `help:"Print machine-readable JSON status"`
	Field string `help:"Print a single machine-readable status field"`
}

func (s *StatusCmd) Run(ctx *Context) error {
	if s.JSON && strings.TrimSpace(s.Field) != "" {
		err := fmt.Errorf("choose either --json or --field")
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return err
	}

	if s.JSON || strings.TrimSpace(s.Field) != "" {
		snapshot, err := collectRuntimeStatus()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			return err
		}
		if s.JSON {
			return writeRuntimeStatusJSON(os.Stdout, snapshot)
		}
		value, err := runtimeStatusFieldValue(snapshot, s.Field)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			return err
		}
		fmt.Fprintln(os.Stdout, value)
		return nil
	}

	// Load config to get runtime paths
	paths, err := runtimePathsLoader()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return err
	}

	pid, running := getPidFromFile(paths.PidFile)

	if !running {
		L_info("gateway not running")
		return nil
	}

	// Load supervisor state
	state, err := supervisor.LoadState(paths.DataDir)

	if err != nil {
		// Fall back to basic status if supervisor.json not available
		L_info("gateway running", "pid", pid)
		return nil
	}

	// Calculate uptime
	uptime := time.Since(state.StartedAt).Round(time.Second)

	// Format status output
	fmt.Println("Gateway:  running")
	if state.GatewayPID > 0 {
		fmt.Printf("PID:      %d (supervisor), %d (gateway)\n", state.PID, state.GatewayPID)
	} else {
		fmt.Printf("PID:      %d (supervisor)\n", state.PID)
	}
	fmt.Printf("Uptime:   %s\n", formatDuration(uptime))

	if state.CrashCount > 0 {
		lastCrash := "unknown"
		if state.LastCrashAt != nil {
			lastCrash = formatTimeAgo(*state.LastCrashAt)
		}
		fmt.Printf("Crashes:  %d this session (last: %s)\n", state.CrashCount, lastCrash)
	} else {
		fmt.Println("Crashes:  0 this session")
	}

	return nil
}

// RestartCmd restarts the daemonized gateway supervisor.
type RestartCmd struct{}

func (r *RestartCmd) Run(ctx *Context) error {
	paths, err := runtimePathsLoader()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return err
	}

	pid, running, err := runtimeStopper(paths, false)
	if err != nil {
		return err
	}
	if running {
		if err := processExitWaiter(pid, 10*time.Second); err != nil {
			return err
		}
		_ = os.Remove(paths.PidFile)
	}

	return runtimeStarter(paths)
}

func startDaemon(paths *RuntimePaths) error {
	// Ensure data directory exists
	if err := os.MkdirAll(paths.DataDir, 0750); err != nil {
		L_error("failed to create data directory", "error", err)
		return err
	}

	// Check if already running
	if isRunningAt(paths.PidFile) {
		L_error("gateway already running")
		return fmt.Errorf("already running")
	}

	cntxt := &daemon.Context{
		PidFileName: paths.PidFile,
		PidFilePerm: 0644,
		LogFileName: paths.LogFile,
		LogFilePerm: 0640,
		WorkDir:     "./",
		Umask:       027,
	}

	d, err := cntxt.Reborn()
	if err != nil {
		L_fatal("daemonize failed", "error", err)
	}
	if d != nil {
		// Parent process
		L_info("gateway started", "pid", d.Pid, "dataDir", paths.DataDir)
		return nil
	}
	// Child process continues as supervisor
	defer cntxt.Release() //nolint:errcheck // daemon cleanup

	L_info("supervisor: started", "pid", os.Getpid(), "dataDir", paths.DataDir)

	// Run supervisor loop (spawns gateway subprocesses)
	sup := supervisor.New(paths.DataDir)
	return sup.Run()
}

func stopDaemon(paths *RuntimePaths, removePIDFile bool) (int, bool, error) {
	pid, running := getPidFromFile(paths.PidFile)
	if !running {
		L_info("gateway not running")
		return 0, false, nil
	}

	process, err := os.FindProcess(pid)
	if err != nil {
		return pid, true, fmt.Errorf("process not found: %w", err)
	}

	if err := process.Signal(syscall.SIGTERM); err != nil {
		return pid, true, fmt.Errorf("failed to stop: %w", err)
	}

	L_info("gateway stopped", "pid", pid)
	if removePIDFile {
		_ = os.Remove(paths.PidFile)
	}
	return pid, true, nil
}

func waitForPIDExit(pid int, timeout time.Duration) error {
	if pid <= 0 {
		return nil
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return nil
	}

	deadline := time.Now().Add(timeout)
	for {
		if err := process.Signal(syscall.Signal(0)); err != nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for process %d to exit", pid)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func collectRuntimeStatus() (*RuntimeStatus, error) {
	defaultConfigPath, err := paths.DefaultConfigPath()
	if err != nil {
		return nil, err
	}
	status := &RuntimeStatus{
		ConfigPath: defaultConfigPath,
		Version:    version,
	}

	loadResult, err := config.LoadRuntime()
	if err != nil {
		if config.IsMissingOrIncompleteConfigError(err) {
			return status, nil
		}
		return nil, err
	}

	runtimePaths := runtimePathsFromStorePath(loadResult.Config.Session.GetStorePath())
	status.Configured = true
	status.ConfigPath = loadResult.SourcePath
	status.DataDir = runtimePaths.DataDir
	status.PidFile = runtimePaths.PidFile
	status.LogFile = runtimePaths.LogFile

	pid, running := getPidFromFile(runtimePaths.PidFile)
	status.Running = running
	if !running {
		return status, nil
	}

	status.SupervisorPID = pid

	state, err := supervisor.LoadState(runtimePaths.DataDir)
	if err != nil {
		return status, nil
	}

	status.SupervisorStateAvailable = true
	if state.PID > 0 {
		status.SupervisorPID = state.PID
	}
	status.GatewayPID = state.GatewayPID
	status.StartedAt = &state.StartedAt
	status.UptimeSeconds = int64(time.Since(state.StartedAt).Seconds())
	status.Uptime = formatDuration(time.Since(state.StartedAt).Round(time.Second))
	status.CrashCount = state.CrashCount
	status.LastCrashAt = state.LastCrashAt

	return status, nil
}

func writeRuntimeStatusJSON(w io.Writer, status *RuntimeStatus) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(status)
}

func runtimeStatusFieldValue(status *RuntimeStatus, field string) (string, error) {
	switch normalizeRuntimeStatusField(field) {
	case "configured":
		return strconv.FormatBool(status.Configured), nil
	case "running":
		return strconv.FormatBool(status.Running), nil
	case "configpath":
		return status.ConfigPath, nil
	case "datadir":
		return status.DataDir, nil
	case "pidfile":
		return status.PidFile, nil
	case "logfile":
		return status.LogFile, nil
	case "version":
		return status.Version, nil
	case "supervisorpid":
		if status.SupervisorPID == 0 {
			return "", nil
		}
		return strconv.Itoa(status.SupervisorPID), nil
	case "gatewaypid":
		if status.GatewayPID == 0 {
			return "", nil
		}
		return strconv.Itoa(status.GatewayPID), nil
	case "uptime":
		return status.Uptime, nil
	case "uptimeseconds":
		if status.UptimeSeconds == 0 {
			return "", nil
		}
		return strconv.FormatInt(status.UptimeSeconds, 10), nil
	case "crashcount":
		return strconv.Itoa(status.CrashCount), nil
	case "supervisorstateavailable":
		return strconv.FormatBool(status.SupervisorStateAvailable), nil
	default:
		return "", fmt.Errorf("unknown status field %q", field)
	}
}

func normalizeRuntimeStatusField(field string) string {
	field = strings.TrimSpace(strings.ToLower(field))
	field = strings.ReplaceAll(field, "-", "")
	field = strings.ReplaceAll(field, "_", "")
	return field
}

// formatDuration formats a duration in human-readable form
func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	hours := int(d.Hours())
	mins := int(d.Minutes()) % 60
	if hours >= 24 {
		days := hours / 24
		hours = hours % 24
		return fmt.Sprintf("%dd%dh%dm", days, hours, mins)
	}
	return fmt.Sprintf("%dh%dm", hours, mins)
}

// formatTimeAgo formats a time as "X ago"
func formatTimeAgo(t time.Time) string {
	d := time.Since(t)
	if d < time.Minute {
		return "just now"
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}

// VersionCmd shows version info
type VersionCmd struct{}

func (v *VersionCmd) Run(ctx *Context) error {
	fmt.Printf("goclaw %s\n", version)
	return nil
}

// UpdateCmd checks for and installs updates
type UpdateCmd struct {
	Check     bool   `help:"Check for updates without installing"`
	Channel   string `help:"Update channel (stable, beta)" default:"stable"`
	NoRestart bool   `help:"Update but don't restart" name:"no-restart"`
	Force     bool   `help:"Update even if already on latest version"`
}

func (u *UpdateCmd) Run(ctx *Context) error {
	return runUpdate(u.Check, u.Channel, u.NoRestart, u.Force)
}

// CronCmd manages cron jobs
type CronCmd struct {
	List   CronListCmd   `cmd:"" help:"List all cron jobs"`
	Add    CronAddCmd    `cmd:"" help:"Add a new cron job"`
	Edit   CronEditCmd   `cmd:"" help:"Edit an existing cron job"`
	Remove CronRemoveCmd `cmd:"" help:"Remove a cron job"`
	Run    CronRunCmd    `cmd:"" help:"Run a job immediately"`
	Runs   CronRunsCmd   `cmd:"" help:"View job execution history"`
	Kill   CronKillCmd   `cmd:"" help:"Clear stuck running state for a job"`
}

// CronListCmd lists all cron jobs
type CronListCmd struct{}

func (c *CronListCmd) Run(ctx *Context) error {
	store := cron.NewStore("", "")
	if err := store.Load(); err != nil {
		return fmt.Errorf("failed to load jobs: %w", err)
	}

	jobs := store.GetAllJobs()
	if len(jobs) == 0 {
		fmt.Println("No cron jobs configured.")
		return nil
	}

	fmt.Printf("Found %d job(s):\n\n", len(jobs))
	for _, job := range jobs {
		status := "enabled"
		if !job.Enabled {
			status = "disabled"
		}
		fmt.Printf("%s (%s)\n", job.Name, status)
		fmt.Printf("  ID: %s\n", job.ID)
		fmt.Printf("  Result: %s\n", job.ResultMode())
		fmt.Printf("  Schedule: %s\n", formatCronSchedule(&job.Schedule))
		if job.IsRunning() {
			runningFor := time.Since(time.UnixMilli(*job.State.RunningAtMs))
			fmt.Printf("  RUNNING: for %s (use 'goclaw cron kill %s' to clear)\n", runningFor.Round(time.Second), job.ID)
		}
		if job.State.NextRunAtMs != nil {
			fmt.Printf("  Next run: %s\n", time.UnixMilli(*job.State.NextRunAtMs).Format(time.RFC3339))
		}
		if job.State.LastRunAtMs != nil {
			fmt.Printf("  Last run: %s (%s)\n", time.UnixMilli(*job.State.LastRunAtMs).Format(time.RFC3339), job.State.LastStatus)
		}
		fmt.Println()
	}
	return nil
}

// CronAddCmd adds a new cron job
type CronAddCmd struct {
	Name       string `arg:"" help:"Job name"`
	Prompt     string `arg:"" help:"Assistant task prompt to execute"`
	Every      string `help:"Run every interval (e.g., 5m, 2h, 1d)" xor:"schedule"`
	At         string `help:"Run once at time (+5m, 2024-01-01T12:00:00Z)" xor:"schedule"`
	Cron       string `help:"Run on cron schedule (e.g., '0 9 * * 1-5')" xor:"schedule"`
	Tz         string `help:"Timezone for cron schedule"`
	ResultMode string `help:"What to do with the result: store_only, deliver, handoff_main" default:"store_only"`
	NoPersist  bool   `help:"Do not persist the cron run result"`
}

func (c *CronAddCmd) Run(ctx *Context) error {
	store := cron.NewStore("", "")
	if err := store.Load(); err != nil {
		return fmt.Errorf("failed to load jobs: %w", err)
	}

	schedule, err := buildScheduleFromFlags(c.Every, c.At, c.Cron, c.Tz)
	if err != nil {
		return err
	}

	resultMode := cron.ResultMode(c.ResultMode)
	switch resultMode {
	case cron.ResultModeStoreOnly, cron.ResultModeDeliver, cron.ResultModeHandoffMain:
	default:
		return fmt.Errorf("invalid result mode: %s", c.ResultMode)
	}

	var persist *bool
	if c.NoPersist {
		falseVal := false
		persist = &falseVal
	}

	job := &cron.CronJob{
		Name:     c.Name,
		Enabled:  true,
		Schedule: schedule,
		Prompt:   c.Prompt,
		Result: cron.ResultPolicy{
			Mode:    resultMode,
			Persist: persist,
		},
	}

	// Calculate initial next run
	next, err := cron.NextRunTime(job, time.Now())
	if err != nil {
		return fmt.Errorf("invalid schedule: %w", err)
	}
	job.SetNextRun(next)

	if err := store.AddJob(job); err != nil {
		return fmt.Errorf("failed to add job: %w", err)
	}

	fmt.Printf("Job created successfully.\n")
	fmt.Printf("ID: %s\n", job.ID)
	fmt.Printf("Name: %s\n", job.Name)
	fmt.Printf("Schedule: %s\n", formatCronSchedule(&job.Schedule))
	if next != nil {
		fmt.Printf("Next run: %s\n", next.Format(time.RFC3339))
	}
	return nil
}

// CronEditCmd edits an existing cron job
type CronEditCmd struct {
	ID      string  `arg:"" help:"Job ID to edit"`
	Name    *string `help:"New job name"`
	Prompt  *string `help:"New prompt message"`
	Enabled *bool   `help:"Enable or disable job"`
}

func (c *CronEditCmd) Run(ctx *Context) error {
	store := cron.NewStore("", "")
	if err := store.Load(); err != nil {
		return fmt.Errorf("failed to load jobs: %w", err)
	}

	job := store.GetJob(c.ID)
	if job == nil {
		return fmt.Errorf("job not found: %s", c.ID)
	}

	if c.Name != nil {
		job.Name = *c.Name
	}
	if c.Prompt != nil {
		job.Prompt = *c.Prompt
	}
	if c.Enabled != nil {
		job.Enabled = *c.Enabled
	}

	if err := store.UpdateJob(job); err != nil {
		return fmt.Errorf("failed to update job: %w", err)
	}

	fmt.Printf("Job updated: %s\n", job.Name)
	return nil
}

// CronRemoveCmd removes a cron job
type CronRemoveCmd struct {
	ID string `arg:"" help:"Job ID to remove"`
}

func (c *CronRemoveCmd) Run(ctx *Context) error {
	store := cron.NewStore("", "")
	if err := store.Load(); err != nil {
		return fmt.Errorf("failed to load jobs: %w", err)
	}

	job := store.GetJob(c.ID)
	if job == nil {
		return fmt.Errorf("job not found: %s", c.ID)
	}

	name := job.Name
	if err := store.DeleteJob(c.ID); err != nil {
		return fmt.Errorf("failed to remove job: %w", err)
	}

	fmt.Printf("Job '%s' removed.\n", name)
	return nil
}

// CronRunCmd runs a job immediately
type CronRunCmd struct {
	ID string `arg:"" help:"Job ID to run"`
}

func (c *CronRunCmd) Run(ctx *Context) error {
	// Note: This requires the gateway to be running
	// For now, just print a message
	fmt.Printf("To run a job immediately, use the cron tool via the agent.\n")
	fmt.Printf("The gateway must be running for job execution.\n")
	return nil
}

// CronRunsCmd shows job execution history
type CronRunsCmd struct {
	ID string `arg:"" help:"Job ID to show history for"`
}

func (c *CronRunsCmd) Run(ctx *Context) error {
	store := cron.NewStore("", "")
	if err := store.Load(); err != nil {
		return fmt.Errorf("failed to load jobs: %w", err)
	}

	job := store.GetJob(c.ID)
	if job == nil {
		return fmt.Errorf("job not found: %s", c.ID)
	}

	fmt.Printf("Run history for '%s' (ID: %s)\n\n", job.Name, job.ID)
	if job.State.LastRunAtMs != nil {
		fmt.Printf("Last run: %s\n", time.UnixMilli(*job.State.LastRunAtMs).Format(time.RFC3339))
		fmt.Printf("Status: %s\n", job.State.LastStatus)
		fmt.Printf("Duration: %dms\n", job.State.LastDurationMs)
		if job.State.LastError != "" {
			fmt.Printf("Error: %s\n", job.State.LastError)
		}
	} else {
		fmt.Println("No runs recorded yet.")
	}
	return nil
}

// CronKillCmd clears the stuck running state for a job
type CronKillCmd struct {
	ID string `arg:"" help:"Job ID to kill (clear running state)"`
}

func (c *CronKillCmd) Run(ctx *Context) error {
	store := cron.NewStore("", "")
	if err := store.Load(); err != nil {
		return fmt.Errorf("failed to load jobs: %w", err)
	}

	job := store.GetJob(c.ID)
	if job == nil {
		return fmt.Errorf("job not found: %s", c.ID)
	}

	if !job.IsRunning() {
		fmt.Printf("Job '%s' is not currently marked as running.\n", job.Name)
		return nil
	}

	// Get running duration for info
	runningFor := time.Since(time.UnixMilli(*job.State.RunningAtMs))

	// Clear the running state
	job.ClearRunning()
	if err := store.UpdateJob(job); err != nil {
		return fmt.Errorf("failed to update job: %w", err)
	}

	fmt.Printf("Cleared running state for job '%s' (was running for %s).\n", job.Name, runningFor.Round(time.Second))
	fmt.Printf("Note: If the job is actually still executing, it will continue until completion or timeout.\n")
	return nil
}

func buildScheduleFromFlags(every, at, cronExpr, tz string) (cron.Schedule, error) {
	if every != "" {
		dur, err := cron.ParseDuration(every)
		if err != nil {
			return cron.Schedule{}, fmt.Errorf("invalid interval: %w", err)
		}
		return cron.Schedule{
			Kind:    cron.ScheduleKindEvery,
			EveryMs: dur.Milliseconds(),
		}, nil
	}

	if at != "" {
		atTime, err := cron.ParseAt(at, time.Now())
		if err != nil {
			return cron.Schedule{}, fmt.Errorf("invalid time: %w", err)
		}
		return cron.Schedule{
			Kind: cron.ScheduleKindAt,
			AtMs: atTime.UnixMilli(),
		}, nil
	}

	if cronExpr != "" {
		return cron.Schedule{
			Kind: cron.ScheduleKindCron,
			Expr: cronExpr,
			Tz:   tz,
		}, nil
	}

	return cron.Schedule{}, fmt.Errorf("must specify --every, --at, or --cron")
}

func formatCronSchedule(s *cron.Schedule) string {
	switch s.Kind {
	case cron.ScheduleKindAt:
		return fmt.Sprintf("at %s", time.UnixMilli(s.AtMs).Format(time.RFC3339))
	case cron.ScheduleKindEvery:
		return fmt.Sprintf("every %s", time.Duration(s.EveryMs)*time.Millisecond)
	case cron.ScheduleKindCron:
		if s.Tz != "" {
			return fmt.Sprintf("cron '%s' (%s)", s.Expr, s.Tz)
		}
		return fmt.Sprintf("cron '%s'", s.Expr)
	default:
		return "unknown"
	}
}

// UserCmd manages users
type UserCmd struct {
	Add         UserAddCmd      `cmd:"" help:"Add a new user"`
	List        UserListCmd     `cmd:"" help:"List all users"`
	Delete      UserDeleteCmd   `cmd:"" help:"Delete a user"`
	SetTelegram UserTelegramCmd `cmd:"set-telegram" help:"Set Telegram ID"`
	SetWhatsapp UserWhatsAppCmd `cmd:"" help:"Set WhatsApp ID"`
	SetPassword UserPasswordCmd `cmd:"set-password" help:"Set HTTP password"`
}

// UserAddCmd adds a new user
type UserAddCmd struct {
	Username string `arg:"" help:"Username (lowercase, alphanumeric + underscore, starts with letter)"`
	Name     string `help:"Display name" required:""`
	Role     string `help:"Role: 'owner' (full access) or 'user' (limited)" default:"user" enum:"owner,user"`
}

func (u *UserAddCmd) Run(ctx *Context) error {
	// Validate username
	if err := user.ValidateUsername(u.Username); err != nil {
		return err
	}

	// Load existing users
	users, err := user.LoadUsers()
	if err != nil {
		return fmt.Errorf("failed to load users: %w", err)
	}

	// Check if user already exists
	if _, exists := users[u.Username]; exists {
		return fmt.Errorf("user %q already exists", u.Username)
	}

	// Add new user
	users[u.Username] = &user.UserEntry{
		Name: u.Name,
		Role: u.Role,
	}

	// Save
	path := user.GetUsersFilePath()
	if err := user.SaveUsers(users, path); err != nil {
		return err
	}

	fmt.Printf("User %q added. Use 'goclaw user set-telegram' or 'goclaw user set-http' to add credentials.\n", u.Username)
	return nil
}

// UserListCmd lists all users
type UserListCmd struct{}

func (u *UserListCmd) Run(ctx *Context) error {
	users, err := user.LoadUsers()
	if err != nil {
		return fmt.Errorf("failed to load users: %w", err)
	}

	if len(users) == 0 {
		fmt.Println("No users configured.")
		return nil
	}

	fmt.Printf("Found %d user(s):\n\n", len(users))
	for username, entry := range users {
		fmt.Printf("%s (%s)\n", username, entry.Role)
		fmt.Printf("  Name: %s\n", entry.Name)
		if entry.TelegramID != "" {
			fmt.Printf("  Telegram: %s\n", entry.TelegramID)
		}
		if entry.WhatsAppID != "" {
			fmt.Printf("  WhatsApp: %s\n", entry.WhatsAppID)
		}
		if entry.HTTPPasswordHash != "" {
			fmt.Printf("  HTTP: configured\n")
		}
		fmt.Println()
	}
	return nil
}

// UserTelegramCmd sets a user's Telegram ID
type UserTelegramCmd struct {
	Username   string `arg:"" help:"Username"`
	TelegramID string `arg:"" help:"Telegram user ID (numeric)"`
}

func (u *UserTelegramCmd) Run(ctx *Context) error {
	users, err := user.LoadUsers()
	if err != nil {
		return fmt.Errorf("failed to load users: %w", err)
	}

	entry, exists := users[u.Username]
	if !exists {
		return fmt.Errorf("user %q not found", u.Username)
	}

	entry.TelegramID = u.TelegramID

	path := user.GetUsersFilePath()
	if err := user.SaveUsers(users, path); err != nil {
		return err
	}

	fmt.Printf("Telegram ID set for user %q.\n", u.Username)
	return nil
}

// UserWhatsAppCmd sets a user's WhatsApp ID
type UserWhatsAppCmd struct {
	Username   string `arg:"" help:"Username"`
	WhatsappID string `arg:"" help:"WhatsApp phone number (international format, e.g. 27821234567)"`
}

func (u *UserWhatsAppCmd) Run(ctx *Context) error {
	users, err := user.LoadUsers()
	if err != nil {
		return fmt.Errorf("failed to load users: %w", err)
	}

	entry, exists := users[u.Username]
	if !exists {
		return fmt.Errorf("user %q not found", u.Username)
	}

	entry.WhatsAppID = u.WhatsappID

	path := user.GetUsersFilePath()
	if err := user.SaveUsers(users, path); err != nil {
		return err
	}

	fmt.Printf("WhatsApp ID set for user %q.\n", u.Username)
	return nil
}

// UserPasswordCmd sets a user's HTTP password
type UserPasswordCmd struct {
	Username string `arg:"" help:"Username"`
	Password string `arg:"" optional:"" help:"Password (omit to prompt interactively)"`
}

func (u *UserPasswordCmd) Run(ctx *Context) error {
	users, err := user.LoadUsers()
	if err != nil {
		return fmt.Errorf("failed to load users: %w", err)
	}

	entry, exists := users[u.Username]
	if !exists {
		return fmt.Errorf("user %q not found", u.Username)
	}

	password := u.Password
	if password == "" {
		// Prompt for password interactively (hidden input)
		fmt.Print("Enter HTTP password: ")
		pwBytes, err := readPassword()
		if err != nil {
			return fmt.Errorf("failed to read password: %w", err)
		}
		fmt.Println() // newline after hidden input
		password = string(pwBytes)
		if password == "" {
			return fmt.Errorf("password cannot be empty")
		}

		fmt.Print("Confirm password: ")
		confirmBytes, err := readPassword()
		if err != nil {
			return fmt.Errorf("failed to read password: %w", err)
		}
		fmt.Println() // newline after hidden input
		if password != string(confirmBytes) {
			return fmt.Errorf("passwords do not match")
		}
	}

	// Hash password
	hash, err := user.HashPassword(password)
	if err != nil {
		return fmt.Errorf("failed to hash password: %w", err)
	}

	entry.HTTPPasswordHash = hash

	path := user.GetUsersFilePath()
	if err := user.SaveUsers(users, path); err != nil {
		return err
	}

	fmt.Printf("HTTP password set for user %q.\n", u.Username)
	return nil
}

// UserDeleteCmd deletes a user
type UserDeleteCmd struct {
	Username string `arg:"" help:"Username to delete"`
	Force    bool   `help:"Force deletion even if user is owner"`
	Purge    bool   `help:"Also delete user's session data (irreversible)"`
}

func (u *UserDeleteCmd) Run(ctx *Context) error {
	users, err := user.LoadUsers()
	if err != nil {
		return fmt.Errorf("failed to load users: %w", err)
	}

	entry, exists := users[u.Username]
	if !exists {
		return fmt.Errorf("user %q not found", u.Username)
	}

	// Check if owner
	if entry.Role == "owner" && !u.Force {
		return fmt.Errorf("cannot delete owner without --force flag")
	}

	// Confirm deletion
	fmt.Printf("Delete user %q? [y/N]: ", u.Username)
	var confirm string
	if _, err := fmt.Scanln(&confirm); err != nil {
		fmt.Fprintf(os.Stderr, "Input error: %v\n", err)
		os.Exit(1)
	}
	if confirm != "y" && confirm != "Y" {
		fmt.Println("Cancelled.")
		return nil
	}

	// Delete user
	delete(users, u.Username)

	path := user.GetUsersFilePath()
	if err := user.SaveUsers(users, path); err != nil {
		return err
	}

	fmt.Printf("User %q deleted.\n", u.Username)

	if u.Purge {
		// TODO: Delete session data from SQLite
		fmt.Println("Note: Session data purging not yet implemented.")
	} else {
		fmt.Println("Session data preserved. Use --purge to delete (irreversible).")
	}

	return nil
}

// BrowserCmd manages browser (download, profiles, setup)
type BrowserCmd struct {
	Download BrowserDownloadCmd `cmd:"" help:"Download/update Chromium browser"`
	Setup    BrowserSetupCmd    `cmd:"" help:"Launch browser for profile setup (login, cookies, etc.)"`
	Profiles BrowserProfilesCmd `cmd:"" help:"List browser profiles"`
	Clear    BrowserClearCmd    `cmd:"" help:"Clear profile data (cookies, cache, etc.)"`
	Status   BrowserStatusCmd   `cmd:"" help:"Show browser status (running instances, download state)"`
	Migrate  BrowserMigrateCmd  `cmd:"" help:"Import profiles from OpenClaw"`
}

// BrowserDownloadCmd downloads Chromium
type BrowserDownloadCmd struct {
	Force bool `help:"Force re-download even if already present"`
}

func (b *BrowserDownloadCmd) Run(ctx *Context) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %w", err)
	}

	// Load config to get browser settings
	loadResult, err := config.LoadRuntime()
	if err != nil {
		return err
	}
	cfg := loadResult.Config

	// Create browser config adapter
	browserCfg := browser.ToolsConfigAdapter{
		Dir:                           cfg.Tools.Browser.Dir,
		AutoDownload:                  true, // Force for this command
		Revision:                      cfg.Tools.Browser.Revision,
		Headless:                      cfg.Tools.Browser.Headless,
		NoSandbox:                     cfg.Tools.Browser.NoSandbox,
		DefaultProfile:                cfg.Tools.Browser.DefaultProfile,
		Timeout:                       cfg.Tools.Browser.Timeout,
		Stealth:                       cfg.Tools.Browser.Stealth,
		Device:                        cfg.Tools.Browser.Device,
		ProfileDomains:                cfg.Tools.Browser.ProfileDomains,
		ChromeCDP:                     cfg.Tools.Browser.ChromeCDP,
		AllowAgentProfiles:            cfg.Tools.Browser.AllowAgentProfiles,
		RemoteEnabled:                 cfg.Tools.Browser.Remote.Enabled,
		RemoteProfilesText:            cfg.Tools.Browser.Remote.ProfilesText,
		RemoteAllowedHosts:            cfg.Tools.Browser.Remote.AllowedHosts,
		RemoteAllowDirectEndpoints:    cfg.Tools.Browser.Remote.AllowDirectEndpoints,
		RemoteAllowHTTPDiscovery:      cfg.Tools.Browser.Remote.AllowHTTPDiscovery,
		RemoteConnectionTimeout:       cfg.Tools.Browser.Remote.ConnectionTimeout,
		AdvancedNetworkCaptureEnabled: cfg.Tools.Browser.Advanced.NetworkCaptureEnabled,
		AdvancedNetworkCaptureMax:     cfg.Tools.Browser.Advanced.NetworkCaptureMax,
		AdvancedConsoleCaptureEnabled: cfg.Tools.Browser.Advanced.ConsoleCaptureEnabled,
		AdvancedConsoleCaptureMax:     cfg.Tools.Browser.Advanced.ConsoleCaptureMax,
		AdvancedTraceDir:              cfg.Tools.Browser.Advanced.TraceDir,
		AdvancedTraceRetention:        cfg.Tools.Browser.Advanced.TraceRetention,
	}.ToConfig()

	// Create downloader
	binDir := browserCfg.ResolveBinDir(home)
	downloader := browser.NewDownloader(binDir, browserCfg.Revision)

	if b.Force {
		fmt.Println("Force downloading Chromium...")
		path, err := downloader.ForceDownload()
		if err != nil {
			return fmt.Errorf("download failed: %w", err)
		}
		fmt.Printf("Chromium downloaded to: %s\n", path)
	} else {
		// Check if already downloaded
		if path, err := downloader.FindExistingBrowser(); err == nil {
			fmt.Printf("Chromium already downloaded: %s\n", path)
			fmt.Println("Use --force to re-download.")
			return nil
		}

		fmt.Println("Downloading Chromium...")
		path, err := downloader.EnsureBrowser()
		if err != nil {
			return fmt.Errorf("download failed: %w", err)
		}
		fmt.Printf("Chromium downloaded to: %s\n", path)
	}

	return nil
}

// BrowserSetupCmd launches browser for profile setup
type BrowserSetupCmd struct {
	Profile string `arg:"" optional:"" help:"Profile name (default: 'default')"`
	URL     string `arg:"" optional:"" help:"Starting URL (optional)"`
}

func (b *BrowserSetupCmd) Run(ctx *Context) error {
	profile := b.Profile
	if profile == "" {
		profile = "default"
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %w", err)
	}

	// Load config
	loadResult, err := config.LoadRuntime()
	if err != nil {
		return err
	}
	cfg := loadResult.Config

	// Create browser config
	browserCfg := browser.ToolsConfigAdapter{
		Dir:                           cfg.Tools.Browser.Dir,
		AutoDownload:                  cfg.Tools.Browser.AutoDownload,
		Revision:                      cfg.Tools.Browser.Revision,
		Headless:                      false, // Headed for setup
		NoSandbox:                     cfg.Tools.Browser.NoSandbox,
		DefaultProfile:                cfg.Tools.Browser.DefaultProfile,
		Timeout:                       cfg.Tools.Browser.Timeout,
		Stealth:                       cfg.Tools.Browser.Stealth,
		Device:                        cfg.Tools.Browser.Device,
		ProfileDomains:                cfg.Tools.Browser.ProfileDomains,
		ChromeCDP:                     cfg.Tools.Browser.ChromeCDP,
		AllowAgentProfiles:            cfg.Tools.Browser.AllowAgentProfiles,
		RemoteEnabled:                 cfg.Tools.Browser.Remote.Enabled,
		RemoteProfilesText:            cfg.Tools.Browser.Remote.ProfilesText,
		RemoteAllowedHosts:            cfg.Tools.Browser.Remote.AllowedHosts,
		RemoteAllowDirectEndpoints:    cfg.Tools.Browser.Remote.AllowDirectEndpoints,
		RemoteAllowHTTPDiscovery:      cfg.Tools.Browser.Remote.AllowHTTPDiscovery,
		RemoteConnectionTimeout:       cfg.Tools.Browser.Remote.ConnectionTimeout,
		AdvancedNetworkCaptureEnabled: cfg.Tools.Browser.Advanced.NetworkCaptureEnabled,
		AdvancedNetworkCaptureMax:     cfg.Tools.Browser.Advanced.NetworkCaptureMax,
		AdvancedConsoleCaptureEnabled: cfg.Tools.Browser.Advanced.ConsoleCaptureEnabled,
		AdvancedConsoleCaptureMax:     cfg.Tools.Browser.Advanced.ConsoleCaptureMax,
		AdvancedTraceDir:              cfg.Tools.Browser.Advanced.TraceDir,
		AdvancedTraceRetention:        cfg.Tools.Browser.Advanced.TraceRetention,
	}.ToConfig()

	// Initialize manager
	mgr, err := browser.InitManager(browserCfg)
	if err != nil {
		return fmt.Errorf("failed to initialize browser manager: %w", err)
	}

	// Ensure browser is downloaded
	if _, err := mgr.EnsureBrowser(); err != nil {
		return fmt.Errorf("failed to ensure browser: %w", err)
	}

	profileDir := browserCfg.ResolveProfileDir(home, profile)
	fmt.Printf("Launching browser for profile: %s\n", profile)
	fmt.Printf("Profile directory: %s\n", profileDir)
	if b.URL != "" {
		fmt.Printf("Starting URL: %s\n", b.URL)
	}
	fmt.Println("\nLog in, set cookies, etc. Close the browser when done.")
	fmt.Println("Press Ctrl+C to cancel.")
	fmt.Println()
	fmt.Println("Starting browser, please wait...")

	// Launch headed browser via the manager's unified API
	browserInstance, err := mgr.GetBrowser(profile, true)
	if err != nil {
		return fmt.Errorf("failed to launch browser: %w", err)
	}

	// Navigate to start URL if provided
	if b.URL != "" {
		_, err := browserInstance.Page(proto.TargetCreateTarget{URL: b.URL})
		if err != nil {
			L_warn("browser setup: failed to open start URL", "url", b.URL, "error", err)
		}
	}

	// Wait for browser to close (user closes window) or Ctrl+C
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Use the browser's context to detect when it dies (user closes window)
	browserCtx := browserInstance.GetContext()
	doneChan := browserCtx.Done()

	select {
	case <-sigChan:
		fmt.Println("\nCancelled.")
	case <-doneChan:
		fmt.Println("\nBrowser window closed.")
	}

	browserInstance.Close() //nolint:errcheck // cleanup

	fmt.Printf("\nProfile '%s' is ready to use.\n", profile)
	return nil
}

// BrowserProfilesCmd lists browser profiles
type BrowserProfilesCmd struct{}

func (b *BrowserProfilesCmd) Run(ctx *Context) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %w", err)
	}

	// Load config
	loadResult, err := config.LoadRuntime()
	if err != nil {
		return err
	}
	cfg := loadResult.Config

	// Create browser config
	browserCfg := browser.ToolsConfigAdapter{
		Dir:                           cfg.Tools.Browser.Dir,
		DefaultProfile:                cfg.Tools.Browser.DefaultProfile,
		ChromeCDP:                     cfg.Tools.Browser.ChromeCDP,
		AllowAgentProfiles:            cfg.Tools.Browser.AllowAgentProfiles,
		RemoteEnabled:                 cfg.Tools.Browser.Remote.Enabled,
		RemoteProfilesText:            cfg.Tools.Browser.Remote.ProfilesText,
		RemoteAllowedHosts:            cfg.Tools.Browser.Remote.AllowedHosts,
		RemoteAllowDirectEndpoints:    cfg.Tools.Browser.Remote.AllowDirectEndpoints,
		RemoteAllowHTTPDiscovery:      cfg.Tools.Browser.Remote.AllowHTTPDiscovery,
		RemoteConnectionTimeout:       cfg.Tools.Browser.Remote.ConnectionTimeout,
		AdvancedNetworkCaptureEnabled: cfg.Tools.Browser.Advanced.NetworkCaptureEnabled,
		AdvancedNetworkCaptureMax:     cfg.Tools.Browser.Advanced.NetworkCaptureMax,
		AdvancedConsoleCaptureEnabled: cfg.Tools.Browser.Advanced.ConsoleCaptureEnabled,
		AdvancedConsoleCaptureMax:     cfg.Tools.Browser.Advanced.ConsoleCaptureMax,
		AdvancedTraceDir:              cfg.Tools.Browser.Advanced.TraceDir,
		AdvancedTraceRetention:        cfg.Tools.Browser.Advanced.TraceRetention,
	}.ToConfig()

	profilesDir := browserCfg.ResolveProfilesDir(home)
	profileMgr := browser.NewProfileManager(profilesDir)

	profiles, err := profileMgr.ListProfiles()
	if err != nil {
		return fmt.Errorf("failed to list profiles: %w", err)
	}

	if len(profiles) == 0 {
		fmt.Println("No browser profiles found.")
		fmt.Printf("Profiles directory: %s\n", profilesDir)
		fmt.Println("\nUse 'goclaw browser setup [profile]' to create a profile.")
		return nil
	}

	fmt.Printf("Browser profiles (%d):\n\n", len(profiles))
	for _, p := range profiles {
		marker := ""
		if p.Name == cfg.Tools.Browser.DefaultProfile {
			marker = " (default)"
		}
		lastUsed := "never"
		if !p.LastUsed.IsZero() {
			lastUsed = p.LastUsed.Format("2006-01-02 15:04")
		}
		fmt.Printf("  %s%s\n", p.Name, marker)
		fmt.Printf("    Size: %s, Last used: %s\n", browser.FormatSize(p.Size), lastUsed)
		fmt.Printf("    Path: %s\n\n", p.Path)
	}

	// Show domain mappings if any
	if len(cfg.Tools.Browser.ProfileDomains) > 0 {
		fmt.Println("Domain mappings:")
		for domain, profile := range cfg.Tools.Browser.ProfileDomains {
			fmt.Printf("  %s → %s\n", domain, profile)
		}
	}

	return nil
}

// BrowserClearCmd clears profile data
type BrowserClearCmd struct {
	Profile string `arg:"" help:"Profile name to clear"`
	Force   bool   `help:"Skip confirmation"`
}

func (b *BrowserClearCmd) Run(ctx *Context) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %w", err)
	}

	// Load config
	loadResult, err := config.LoadRuntime()
	if err != nil {
		return err
	}
	cfg := loadResult.Config

	// Create browser config
	browserCfg := browser.ToolsConfigAdapter{
		Dir:                           cfg.Tools.Browser.Dir,
		DefaultProfile:                cfg.Tools.Browser.DefaultProfile,
		ChromeCDP:                     cfg.Tools.Browser.ChromeCDP,
		AllowAgentProfiles:            cfg.Tools.Browser.AllowAgentProfiles,
		RemoteEnabled:                 cfg.Tools.Browser.Remote.Enabled,
		RemoteProfilesText:            cfg.Tools.Browser.Remote.ProfilesText,
		RemoteAllowedHosts:            cfg.Tools.Browser.Remote.AllowedHosts,
		RemoteAllowDirectEndpoints:    cfg.Tools.Browser.Remote.AllowDirectEndpoints,
		RemoteAllowHTTPDiscovery:      cfg.Tools.Browser.Remote.AllowHTTPDiscovery,
		RemoteConnectionTimeout:       cfg.Tools.Browser.Remote.ConnectionTimeout,
		AdvancedNetworkCaptureEnabled: cfg.Tools.Browser.Advanced.NetworkCaptureEnabled,
		AdvancedNetworkCaptureMax:     cfg.Tools.Browser.Advanced.NetworkCaptureMax,
		AdvancedConsoleCaptureEnabled: cfg.Tools.Browser.Advanced.ConsoleCaptureEnabled,
		AdvancedConsoleCaptureMax:     cfg.Tools.Browser.Advanced.ConsoleCaptureMax,
		AdvancedTraceDir:              cfg.Tools.Browser.Advanced.TraceDir,
		AdvancedTraceRetention:        cfg.Tools.Browser.Advanced.TraceRetention,
	}.ToConfig()

	profilesDir := browserCfg.ResolveProfilesDir(home)
	profileMgr := browser.NewProfileManager(profilesDir)

	if !profileMgr.ProfileExists(b.Profile) {
		return fmt.Errorf("profile '%s' does not exist", b.Profile)
	}

	if !b.Force {
		fmt.Printf("Clear all data for profile '%s'? This will delete cookies, cache, and login sessions.\n", b.Profile)
		fmt.Print("Type 'yes' to confirm: ")
		var confirm string
		if _, err := fmt.Scanln(&confirm); err != nil {
			fmt.Fprintf(os.Stderr, "Input error: %v\n", err)
			os.Exit(1)
		}
		if confirm != "yes" {
			fmt.Println("Cancelled.")
			return nil
		}
	}

	if err := profileMgr.ClearProfile(b.Profile); err != nil {
		return fmt.Errorf("failed to clear profile: %w", err)
	}

	fmt.Printf("Profile '%s' cleared.\n", b.Profile)
	return nil
}

// BrowserStatusCmd shows browser status
type BrowserStatusCmd struct{}

func (b *BrowserStatusCmd) Run(ctx *Context) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %w", err)
	}

	// Load config
	loadResult, err := config.LoadRuntime()
	if err != nil {
		return err
	}
	cfg := loadResult.Config

	fmt.Println("Browser Status")
	fmt.Println("==============")
	fmt.Println()

	// Check if browser is enabled
	if !cfg.Tools.Browser.Enabled {
		fmt.Println("Browser: DISABLED (set tools.browser.enabled=true to enable)")
		return nil
	}
	fmt.Println("Browser: ENABLED")

	// Create browser config
	browserCfg := browser.ToolsConfigAdapter{
		Dir:                           cfg.Tools.Browser.Dir,
		AutoDownload:                  cfg.Tools.Browser.AutoDownload,
		Revision:                      cfg.Tools.Browser.Revision,
		DefaultProfile:                cfg.Tools.Browser.DefaultProfile,
		ChromeCDP:                     cfg.Tools.Browser.ChromeCDP,
		AllowAgentProfiles:            cfg.Tools.Browser.AllowAgentProfiles,
		RemoteEnabled:                 cfg.Tools.Browser.Remote.Enabled,
		RemoteProfilesText:            cfg.Tools.Browser.Remote.ProfilesText,
		RemoteAllowedHosts:            cfg.Tools.Browser.Remote.AllowedHosts,
		RemoteAllowDirectEndpoints:    cfg.Tools.Browser.Remote.AllowDirectEndpoints,
		RemoteAllowHTTPDiscovery:      cfg.Tools.Browser.Remote.AllowHTTPDiscovery,
		RemoteConnectionTimeout:       cfg.Tools.Browser.Remote.ConnectionTimeout,
		AdvancedNetworkCaptureEnabled: cfg.Tools.Browser.Advanced.NetworkCaptureEnabled,
		AdvancedNetworkCaptureMax:     cfg.Tools.Browser.Advanced.NetworkCaptureMax,
		AdvancedConsoleCaptureEnabled: cfg.Tools.Browser.Advanced.ConsoleCaptureEnabled,
		AdvancedConsoleCaptureMax:     cfg.Tools.Browser.Advanced.ConsoleCaptureMax,
		AdvancedTraceDir:              cfg.Tools.Browser.Advanced.TraceDir,
		AdvancedTraceRetention:        cfg.Tools.Browser.Advanced.TraceRetention,
	}.ToConfig()

	// Check download status
	binDir := browserCfg.ResolveBinDir(home)
	downloader := browser.NewDownloader(binDir, browserCfg.Revision)

	if binPath, err := downloader.FindExistingBrowser(); err == nil {
		fmt.Printf("Chromium: DOWNLOADED\n")
		fmt.Printf("  Path: %s\n", binPath)
	} else {
		if cfg.Tools.Browser.AutoDownload {
			fmt.Println("Chromium: NOT DOWNLOADED (will auto-download on first use)")
		} else {
			fmt.Println("Chromium: NOT DOWNLOADED (run 'goclaw browser download')")
		}
	}

	// Check profiles
	profilesDir := browserCfg.ResolveProfilesDir(home)
	profileMgr := browser.NewProfileManager(profilesDir)
	profiles, _ := profileMgr.ListProfiles()

	fmt.Printf("\nProfiles: %d\n", len(profiles))
	if len(profiles) > 0 {
		for _, p := range profiles {
			marker := ""
			if p.Name == cfg.Tools.Browser.DefaultProfile {
				marker = " (default)"
			}
			fmt.Printf("  - %s%s (%s)\n", p.Name, marker, browser.FormatSize(p.Size))
		}
	}

	// Note about running instances
	fmt.Println("\nNote: Running browser instances are managed by the gateway.")
	fmt.Println("Use 'goclaw status' to check if the gateway is running.")

	return nil
}

// BrowserMigrateCmd imports profiles from OpenClaw
type BrowserMigrateCmd struct{}

func (b *BrowserMigrateCmd) Run(ctx *Context) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %w", err)
	}

	// OpenClaw profile location
	openclawProfilesDir := filepath.Join(home, ".openclaw", "browser", "profiles")

	// GoClaw profile location
	goclawProfilesDir := filepath.Join(home, ".openclaw", "goclaw", "browser", "profiles")

	// Check if OpenClaw profiles exist
	if _, err := os.Stat(openclawProfilesDir); os.IsNotExist(err) {
		fmt.Println("No OpenClaw profiles found at:", openclawProfilesDir)
		return nil
	}

	// List OpenClaw profiles
	entries, err := os.ReadDir(openclawProfilesDir)
	if err != nil {
		return fmt.Errorf("failed to read OpenClaw profiles: %w", err)
	}

	var profiles []string
	for _, entry := range entries {
		if entry.IsDir() {
			profiles = append(profiles, entry.Name())
		}
	}

	if len(profiles) == 0 {
		fmt.Println("No profiles found in OpenClaw directory")
		return nil
	}

	fmt.Println("Found OpenClaw profiles:")
	for _, p := range profiles {
		srcPath := filepath.Join(openclawProfilesDir, p)
		size := getDirSize(srcPath)
		fmt.Printf("  - %s (%s)\n", p, browser.FormatSize(size))
	}
	fmt.Println()

	// Ensure GoClaw profiles directory exists
	if err := os.MkdirAll(goclawProfilesDir, 0750); err != nil {
		return fmt.Errorf("failed to create GoClaw profiles directory: %w", err)
	}

	// Process each profile
	reader := bufio.NewReader(os.Stdin)
	for _, p := range profiles {
		srcPath := filepath.Join(openclawProfilesDir, p)

		// Suggest renaming "openclaw" to "default"
		destName := p
		if p == "openclaw" {
			fmt.Printf("\nProfile '%s' found. Import as:\n", p)
			fmt.Println("  [1] 'default' (recommended - GoClaw's default profile name)")
			fmt.Println("  [2] 'openclaw' (keep original name)")
			fmt.Println("  [3] Skip this profile")
			fmt.Print("Choice [1]: ")

			choice, _ := reader.ReadString('\n')
			choice = strings.TrimSpace(choice)

			switch choice {
			case "", "1":
				destName = "default"
			case "2":
				destName = "openclaw"
			case "3":
				fmt.Printf("Skipped '%s'\n", p)
				continue
			default:
				fmt.Printf("Invalid choice, skipping '%s'\n", p)
				continue
			}
		} else {
			fmt.Printf("\nImport '%s'? [Y/n]: ", p)
			answer, _ := reader.ReadString('\n')
			answer = strings.TrimSpace(strings.ToLower(answer))
			if answer == "n" || answer == "no" {
				fmt.Printf("Skipped '%s'\n", p)
				continue
			}
		}

		destPath := filepath.Join(goclawProfilesDir, destName)

		// Check if destination exists
		if _, err := os.Stat(destPath); err == nil {
			fmt.Printf("  Warning: '%s' already exists in GoClaw. Overwrite? [y/N]: ", destName)
			answer, _ := reader.ReadString('\n')
			answer = strings.TrimSpace(strings.ToLower(answer))
			if answer != "y" && answer != "yes" {
				fmt.Printf("  Skipped (destination exists)\n")
				continue
			}
			// Remove existing
			os.RemoveAll(destPath)
		}

		// Copy the profile directory
		fmt.Printf("  Copying '%s' -> '%s'...", p, destName)
		if err := copyDir(srcPath, destPath); err != nil {
			fmt.Printf(" FAILED: %v\n", err)
			continue
		}
		fmt.Println(" OK")
	}

	fmt.Println("\nMigration complete!")
	fmt.Println("\nNext steps:")
	fmt.Println("  1. Run 'goclaw browser profiles' to verify imported profiles")
	fmt.Println("  2. Update profileDomains in goclaw.json to map domains to profiles")
	fmt.Println("  3. Or set allowAgentProfiles: true to let agent specify profiles directly")

	return nil
}

// getDirSize calculates the total size of a directory
func getDirSize(path string) int64 {
	var size int64
	filepath.Walk(path, func(_ string, info os.FileInfo, err error) error { //nolint:errcheck // errors handled in callback
		if err != nil {
			return nil
		}
		if !info.IsDir() {
			size += info.Size()
		}
		return nil
	})
	return size
}

// copyDir recursively copies a directory
func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Calculate destination path
		relPath, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		destPath := filepath.Join(dst, relPath)

		if info.IsDir() {
			return os.MkdirAll(destPath, info.Mode())
		}

		// Copy file
		srcFile, err := os.Open(path)
		if err != nil {
			return err
		}
		defer srcFile.Close()

		destFile, err := os.Create(destPath)
		if err != nil {
			return err
		}
		defer destFile.Close()

		if _, err := io.Copy(destFile, srcFile); err != nil {
			return err
		}

		return os.Chmod(destPath, info.Mode())
	})
}

// EmbeddingsCmd manages embeddings (status, rebuild)
type EmbeddingsCmd struct {
	Status  EmbeddingsStatusCmd  `cmd:"" help:"Show embeddings status"`
	Rebuild EmbeddingsRebuildCmd `cmd:"" help:"Rebuild embeddings to primary model"`
}

// EmbeddingsStatusCmd shows embedding status
type EmbeddingsStatusCmd struct{}

func (e *EmbeddingsStatusCmd) Run(ctx *Context) error {
	return runEmbeddingsStatus()
}

// EmbeddingsRebuildCmd rebuilds embeddings
type EmbeddingsRebuildCmd struct {
	BatchSize int `help:"Batch size for processing" default:"50"`
}

func (e *EmbeddingsRebuildCmd) Run(ctx *Context) error {
	return runEmbeddingsRebuild(e.BatchSize)
}

// GraphCmd manages memory graph operations
type GraphCmd struct {
	Ingest   GraphIngestCmd   `cmd:"" help:"Ingest content into memory graph"`
	Bulletin GraphBulletinCmd `cmd:"" help:"Generate memory bulletins"`
	Search   GraphSearchCmd   `cmd:"" help:"Search the memory graph"`
	Stats    GraphStatsCmd    `cmd:"" default:"withargs" help:"Show memory graph statistics"`
}

// GraphIngestCmd ingests content into the memory graph
type GraphIngestCmd struct {
	Source string `help:"Source to ingest: markdown, transcript, or all" default:"all" enum:"markdown,transcript,all"`
	User   string `help:"Username to ingest for (defaults to owner)"`
	MaxAge int    `help:"Maximum age in days for transcript ingestion (0 = no limit)" default:"0"`
}

func (g *GraphIngestCmd) Run(ctx *Context) error {
	return runGraphIngest(g.Source, g.User, g.MaxAge)
}

// GraphBulletinCmd generates memory bulletins (raw structured output, same as agent sees)
type GraphBulletinCmd struct {
	Type string `arg:"" help:"Bulletin type: memory or context" enum:"memory,context"`
	User string `help:"Username (defaults to owner)"`
}

func (g *GraphBulletinCmd) Run(ctx *Context) error {
	return runGraphBulletin(g.Type, g.User)
}

// GraphSearchCmd searches the memory graph
type GraphSearchCmd struct {
	Query string `arg:"" help:"Search query"`
	User  string `help:"Username (defaults to owner)"`
	Limit int    `help:"Maximum results" default:"10"`
}

func (g *GraphSearchCmd) Run(ctx *Context) error {
	return runGraphSearch(g.Query, g.User, g.Limit)
}

// GraphStatsCmd shows memory graph statistics
type GraphStatsCmd struct{}

func (g *GraphStatsCmd) Run(ctx *Context) error {
	return runGraphStats()
}

// WhatsAppCmd manages WhatsApp connection
type WhatsAppCmd struct {
	Link   WhatsAppLinkCmd   `cmd:"link" help:"Pair with WhatsApp via QR code"`
	Unlink WhatsAppUnlinkCmd `cmd:"unlink" help:"Remove WhatsApp pairing"`
	Status WhatsAppStatusCmd `cmd:"status" default:"withargs" help:"Show WhatsApp pairing status"`
}

// WhatsAppLinkCmd pairs a new WhatsApp device via QR code
type WhatsAppLinkCmd struct{}

func (w *WhatsAppLinkCmd) Run(ctx *Context) error {
	return whatsapp.LinkDevice()
}

// WhatsAppUnlinkCmd removes the WhatsApp session
type WhatsAppUnlinkCmd struct{}

func (w *WhatsAppUnlinkCmd) Run(ctx *Context) error {
	return whatsapp.UnlinkDevice()
}

// WhatsAppStatusCmd shows WhatsApp pairing status
type WhatsAppStatusCmd struct{}

func (w *WhatsAppStatusCmd) Run(ctx *Context) error {
	return whatsapp.DeviceStatus()
}

// runEmbeddingsStatus shows detailed embedding status
func runEmbeddingsStatus() error {
	// Load config
	loadResult, err := config.LoadRuntime()
	if err != nil {
		return err
	}
	cfg := loadResult.Config

	// Check if embeddings are configured
	if len(cfg.LLM.Embeddings.Models) == 0 {
		fmt.Println("Embeddings not configured (no models in llm.embeddings.models)")
		return nil
	}

	// Open sessions DB (transcript_chunks)
	sessionsDB, err := openSessionsDB(cfg)
	if err != nil {
		return fmt.Errorf("open sessions DB: %w", err)
	}
	defer sessionsDB.Close()

	// Open memory DB if enabled
	var memoryDB *sql.DB
	if cfg.Memory.Enabled {
		memoryDB, err = openMemoryDB(cfg)
		if err != nil {
			L_warn("embeddings: failed to open memory DB", "error", err)
			// Continue without memory DB
		} else {
			defer memoryDB.Close()
		}
	}

	// Get status
	status, err := embeddings.GetStatus(sessionsDB, memoryDB, cfg.LLM.Embeddings)
	if err != nil {
		return fmt.Errorf("get status: %w", err)
	}

	// Print status
	fmt.Println("Embeddings Status")
	fmt.Println("=================")
	fmt.Println()
	fmt.Println("Configuration:")
	fmt.Printf("  Primary model: %s\n", status.PrimaryModel)
	fmt.Printf("  Auto-rebuild:  %v\n", status.AutoRebuild)
	fmt.Println()

	// Models in database
	fmt.Println("Models in Database:")
	allModels := make(map[string]int)
	for _, m := range status.Transcript.Models {
		allModels[m.Model] += m.Count
	}
	for _, m := range status.Memory.Models {
		allModels[m.Model] += m.Count
	}
	for model, count := range allModels {
		if model == status.PrimaryModel {
			fmt.Printf("  ✓ %s: %d chunks (primary)\n", model, count)
		} else {
			fmt.Printf("  ⚠ %s: %d chunks (needs rebuild)\n", model, count)
		}
	}
	fmt.Println()

	// Transcript status
	fmt.Println("Transcript Embeddings:")
	fmt.Printf("  Total chunks:     %d\n", status.Transcript.TotalChunks)
	if status.Transcript.TotalChunks > 0 {
		primaryPct := float64(status.Transcript.PrimaryModelCount) / float64(status.Transcript.TotalChunks) * 100
		fmt.Printf("  Primary model:    %d (%.1f%%)\n", status.Transcript.PrimaryModelCount, primaryPct)
		fmt.Printf("  Needs rebuild:    %d (%.1f%%)\n", status.Transcript.NeedsRebuildCount, 100-primaryPct)
	}
	fmt.Println()

	// Memory status
	if memoryDB != nil {
		fmt.Println("Memory Embeddings:")
		fmt.Printf("  Total chunks:     %d\n", status.Memory.TotalChunks)
		if status.Memory.TotalChunks > 0 {
			primaryPct := float64(status.Memory.PrimaryModelCount) / float64(status.Memory.TotalChunks) * 100
			fmt.Printf("  Primary model:    %d (%.1f%%)\n", status.Memory.PrimaryModelCount, primaryPct)
			fmt.Printf("  Needs rebuild:    %d (%.1f%%)\n", status.Memory.NeedsRebuildCount, 100-primaryPct)
		}
	} else {
		fmt.Println("Memory Embeddings: disabled")
	}

	return nil
}

// buildLLMRegistry creates an LLM registry from config
func buildLLMRegistry(cfg *config.Config) (*llm.Registry, error) {
	regCfg := llm.RegistryConfig{
		Providers:        cfg.LLM.Providers,
		Agent:            cfg.LLM.Agent,
		Summarization:    cfg.LLM.Summarization,
		Embeddings:       cfg.LLM.Embeddings,
		Heartbeat:        cfg.LLM.Heartbeat,
		Cron:             cfg.LLM.Cron,
		Hass:             cfg.LLM.Hass,
		MemoryExtraction: cfg.LLM.MemoryExtraction,
	}
	return llm.NewRegistry(regCfg)
}

// runEmbeddingsRebuild rebuilds all non-primary embeddings
func runEmbeddingsRebuild(batchSize int) error {
	// Load config
	loadResult, err := config.LoadRuntime()
	if err != nil {
		return err
	}
	cfg := loadResult.Config

	// Check if embeddings are configured
	if len(cfg.LLM.Embeddings.Models) == 0 {
		fmt.Println("Embeddings not configured (no models in llm.embeddings.models)")
		return nil
	}

	primaryModel := cfg.LLM.Embeddings.Models[0]
	fmt.Printf("Rebuilding embeddings to primary model: %s\n", primaryModel)

	// Initialize LLM registry
	registry, err := buildLLMRegistry(cfg)
	if err != nil {
		return fmt.Errorf("create LLM registry: %w", err)
	}
	llm.SetGlobalRegistry(registry)

	// Open sessions DB (transcript_chunks)
	sessionsDB, err := openSessionsDB(cfg)
	if err != nil {
		return fmt.Errorf("open sessions DB: %w", err)
	}
	defer sessionsDB.Close()

	// Open memory DB if enabled
	var memoryDB *sql.DB
	if cfg.Memory.Enabled {
		memoryDB, err = openMemoryDB(cfg)
		if err != nil {
			L_warn("embeddings: failed to open memory DB", "error", err)
			// Continue without memory DB
		} else {
			defer memoryDB.Close()
		}
	}

	// Progress callback
	onProgress := func(processed, total int, err error, done bool) {
		if err != nil {
			fmt.Printf("\nError: %v\n", err)
			return
		}
		if done {
			fmt.Printf("\nRebuild complete. %d chunks processed.\n", processed)
			return
		}
		// Periodic progress update
		fmt.Printf("  %d/%d (%.1f%%)\n", processed, total, float64(processed)/float64(total)*100)
	}

	// Run rebuild (CLI always forces full rebuild)
	ctx := context.Background()
	fmt.Printf("Processing chunks in batches of %d...\n\n", batchSize)

	err = embeddings.Rebuild(ctx, sessionsDB, memoryDB, cfg.LLM.Embeddings, registry, batchSize, true, onProgress)
	if err != nil {
		return fmt.Errorf("rebuild failed: %w", err)
	}

	return nil
}

// openSessionsDB opens the sessions database
func openSessionsDB(cfg *config.Config) (*sql.DB, error) {
	storePath := cfg.Session.GetStorePath()
	return sql.Open("sqlite3", storePath+"?_journal_mode=WAL&_busy_timeout=5000")
}

// openMemoryDB opens the memory database
func openMemoryDB(cfg *config.Config) (*sql.DB, error) {
	dbPath := cfg.Memory.DbPath
	if dbPath == "" {
		var err error
		dbPath, err = paths.DataPath("memory.db")
		if err != nil {
			return nil, fmt.Errorf("get memory db path: %w", err)
		}
	} else if strings.HasPrefix(dbPath, "~") {
		var err error
		dbPath, err = paths.ExpandTilde(dbPath)
		if err != nil {
			return nil, fmt.Errorf("expand memory db path: %w", err)
		}
	}
	return sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
}

// runGraphIngest ingests content into the memory graph
func runGraphIngest(source, username string, maxAgeDays int) error {
	loadResult, err := config.LoadRuntime()
	if err != nil {
		return err
	}
	cfg := loadResult.Config

	// Get owner username if not specified
	if username == "" {
		users, err := user.LoadUsers()
		if err != nil {
			return fmt.Errorf("load users: %w", err)
		}
		username = users.GetOwner()
		if username == "" {
			return fmt.Errorf("no owner user found and --user not specified")
		}
	}

	// Initialize memory graph manager
	mgr, err := memorygraph.NewManager(cfg.MemoryGraph)
	if err != nil {
		return fmt.Errorf("init memory graph: %w", err)
	}
	if mgr == nil {
		return fmt.Errorf("memory graph is disabled in configuration")
	}
	defer mgr.Close() //nolint:errcheck // best-effort cleanup

	// Initialize LLM registry - ExtractionLoop uses GetProvider("summarization") internally
	registry, err := buildLLMRegistry(cfg)
	if err != nil {
		return fmt.Errorf("create LLM registry: %w", err)
	}
	llm.SetGlobalRegistry(registry)

	ctx := context.Background()

	// Ingest markdown if requested
	if source == "markdown" || source == "all" {
		fmt.Printf("Ingesting markdown files for user: %s\n", username)
		mdIngester := memorygraph.NewMarkdownIngester(cfg.Gateway.WorkingDir, cfg.MemoryGraph.Ingestion)
		report, err := memorygraph.Ingest(ctx, mgr, mdIngester, username)
		if err != nil {
			return fmt.Errorf("markdown ingestion failed: %w", err)
		}
		fmt.Printf("  Scanned: %d, Skipped: %d, Extracted: %d, Errors: %d (%.2fs)\n",
			report.Scanned, report.Skipped, report.Extracted, report.Errors, report.Duration.Seconds())
	}

	// Ingest transcripts if requested
	if source == "transcript" || source == "all" {
		batchSize := cfg.MemoryGraph.Ingestion.TranscriptBatchSize
		if batchSize < 1 {
			batchSize = 25 // Default
		}

		sessionsDB, err := openSessionsDB(cfg)
		if err != nil {
			return fmt.Errorf("open sessions DB: %w", err)
		}
		defer sessionsDB.Close()

		// Calculate minimum timestamp if maxAge specified (timestamps are in seconds)
		var minTimestamp int64
		if maxAgeDays > 0 {
			minTimestamp = time.Now().AddDate(0, 0, -maxAgeDays).Unix()
		}

		// Count total chunks for progress display (with age filter)
		var totalChunks int
		var countQuery string
		var countArgs []interface{}
		if minTimestamp > 0 {
			countQuery = "SELECT COUNT(*) FROM transcript_chunks WHERE (user_id = ? OR user_id = '' OR user_id IS NULL) AND timestamp_start >= ?"
			countArgs = []interface{}{username, minTimestamp}
		} else {
			countQuery = "SELECT COUNT(*) FROM transcript_chunks WHERE user_id = ? OR user_id = '' OR user_id IS NULL"
			countArgs = []interface{}{username}
		}
		row := sessionsDB.QueryRow(countQuery, countArgs...)
		if err := row.Scan(&totalChunks); err != nil {
			totalChunks = 0 // Unknown
		}

		// Count already-processed chunks
		var alreadyProcessed int
		processedRow := mgr.DB().QueryRow(`SELECT COUNT(*) FROM ingestion_state WHERE source_type = 'transcript'`)
		if err := processedRow.Scan(&alreadyProcessed); err != nil {
			alreadyProcessed = 0
		}

		remaining := totalChunks - alreadyProcessed
		if remaining < 0 {
			remaining = 0
		}

		fmt.Printf("Ingesting transcript chunks for user: %s\n", username)
		if maxAgeDays > 0 {
			fmt.Printf("  Max age: %d days\n", maxAgeDays)
		}
		fmt.Printf("  Total chunks: %d, Already processed: %d, Remaining: ~%d\n", totalChunks, alreadyProcessed, remaining)

		txIngester := memorygraph.NewTranscriptIngesterWithAge(sessionsDB, username, minTimestamp)
		report, err := memorygraph.IngestWithBatchingAndTotal(ctx, mgr, txIngester, username, batchSize, totalChunks, alreadyProcessed)
		if err != nil {
			return fmt.Errorf("transcript ingestion failed: %w", err)
		}
		fmt.Printf("  Scanned: %d, Skipped: %d, Extracted: %d, Errors: %d (%.2fs)\n",
			report.Scanned, report.Skipped, report.Extracted, report.Errors, report.Duration.Seconds())
	}

	fmt.Println("Ingestion complete.")
	return nil
}

// runGraphBulletin generates a memory bulletin (raw structured output, same as agent sees)
func runGraphBulletin(bulletinType, username string) error {
	loadResult, err := config.LoadRuntime()
	if err != nil {
		return err
	}
	cfg := loadResult.Config

	// Get owner username if not specified
	if username == "" {
		users, err := user.LoadUsers()
		if err != nil {
			return fmt.Errorf("load users: %w", err)
		}
		username = users.GetOwner()
		if username == "" {
			return fmt.Errorf("no owner user found and --user not specified")
		}
	}

	// Initialize memory graph manager
	mgr, err := memorygraph.NewManager(cfg.MemoryGraph)
	if err != nil {
		return fmt.Errorf("init memory graph: %w", err)
	}
	if mgr == nil {
		return fmt.Errorf("memory graph is disabled in configuration")
	}
	defer mgr.Close() //nolint:errcheck // best-effort cleanup

	ctx := context.Background()

	// Get bulletin config from manager
	bulletinCfg := mgr.Config().Bulletin

	switch bulletinType {
	case "memory":
		bulletin, err := memorygraph.BuildMemoryBulletinWithConfig(ctx, mgr, username, bulletinCfg)
		if err != nil {
			return fmt.Errorf("build memory bulletin: %w", err)
		}
		if bulletin == "" {
			fmt.Println("(no memory data found)")
		} else {
			fmt.Println(bulletin)
		}

	case "context":
		// For CLI, include header (omitHeader=false)
		bulletin, err := memorygraph.BuildContextBulletinWithConfig(mgr, username, bulletinCfg, false)
		if err != nil {
			return fmt.Errorf("build context bulletin: %w", err)
		}
		if bulletin == "" {
			fmt.Println("(no context data found)")
		} else {
			fmt.Println(bulletin)
		}

	default:
		return fmt.Errorf("unknown bulletin type: %s", bulletinType)
	}

	return nil
}

// runGraphSearch searches the memory graph
func runGraphSearch(query, username string, limit int) error {
	loadResult, err := config.LoadRuntime()
	if err != nil {
		return err
	}
	cfg := loadResult.Config

	// Get owner username if not specified
	if username == "" {
		users, err := user.LoadUsers()
		if err != nil {
			return fmt.Errorf("load users: %w", err)
		}
		username = users.GetOwner()
		if username == "" {
			return fmt.Errorf("no owner user found and --user not specified")
		}
	}

	// Initialize memory graph manager
	mgr, err := memorygraph.NewManager(cfg.MemoryGraph)
	if err != nil {
		return fmt.Errorf("init memory graph: %w", err)
	}
	if mgr == nil {
		return fmt.Errorf("memory graph is disabled in configuration")
	}
	defer mgr.Close() //nolint:errcheck // best-effort cleanup

	// Initialize LLM for semantic search
	registry, err := buildLLMRegistry(cfg)
	if err == nil {
		llm.SetGlobalRegistry(registry)
	}

	ctx := context.Background()
	results, err := mgr.Search(ctx, memorygraph.SearchOptions{
		Query:      query,
		Username:   username,
		MaxResults: limit,
	})
	if err != nil {
		return fmt.Errorf("search failed: %w", err)
	}

	if len(results) == 0 {
		fmt.Println("No results found.")
		return nil
	}

	fmt.Printf("Found %d results:\n\n", len(results))
	for i, r := range results {
		fmt.Printf("%d. [%s] (score: %.2f, importance: %.0f%%)\n", i+1, r.Memory.Type, r.Score, r.Memory.Importance*100)
		fmt.Printf("   %s\n", r.Memory.Content)
		fmt.Printf("   ID: %s | Created: %s\n\n", r.Memory.UUID, r.Memory.CreatedAt.Format("2006-01-02 15:04"))
	}

	return nil
}

// runGraphStats shows memory graph statistics
func runGraphStats() error {
	loadResult, err := config.LoadRuntime()
	if err != nil {
		return err
	}
	cfg := loadResult.Config

	// Initialize memory graph manager
	mgr, err := memorygraph.NewManager(cfg.MemoryGraph)
	if err != nil {
		return fmt.Errorf("init memory graph: %w", err)
	}
	if mgr == nil {
		fmt.Println("Memory graph is disabled in configuration")
		return nil
	}
	defer mgr.Close() //nolint:errcheck // best-effort cleanup

	summary, err := memorygraph.BuildStatsSummary(mgr)
	if err != nil {
		return fmt.Errorf("build stats: %w", err)
	}

	fmt.Println(summary)
	return nil
}

// SetupCmd is the interactive setup wizard
type SetupCmd struct {
	Auto     SetupAutoCmd     `cmd:"" default:"withargs" help:"Run setup (auto-detect mode)"`
	Wizard   SetupWizardCmd   `cmd:"wizard" help:"Run full setup wizard (even if config exists)"`
	Edit     SetupEditCmd     `cmd:"edit" help:"Edit existing configuration"`
	Generate SetupGenerateCmd `cmd:"generate" help:"Output default config template to stdout"`
}

// SetupAutoCmd auto-detects mode: wizard if no config, edit if exists
type SetupAutoCmd struct {
	Web bool `help:"Force web browser interface"`
	TUI bool `help:"Force terminal interface"`
	Dev bool `help:"Enable developer tools (right-click inspect)"`
}

func (s *SetupAutoCmd) Run(ctx *Context) error {
	if s.Web && s.TUI {
		return fmt.Errorf("cannot use --web and --tui together")
	}

	// Match the same web-first semantics as `setup wizard` and `setup edit`.
	// If config exists, open editor; otherwise open wizard. Fall back to TUI if no UI is available.
	existingConfigPath, err := paths.ConfigPath()
	if err != nil {
		return fmt.Errorf("failed to check config path: %w", err)
	}

	configPath := setupweb.DefaultConfigPath()

	if existingConfigPath != "" {
		if s.TUI {
			return setup.RunEdit()
		}

		err := setupweb.RunWebEditorWithOptions(configPath, s.Dev)
		if s.Web && err == setupweb.ErrNoUIAvailable {
			fmt.Println("\nCould not open web editor.")
			fmt.Println("The web-based editor requires either:")
			fmt.Println("  1. webkit2gtk installed (apt install libwebkit2gtk-4.1-0)")
			fmt.Println("  2. A web browser (xdg-open)")
			fmt.Println("\nUse --tui flag for terminal interface instead:")
			fmt.Println("  goclaw setup --tui")
			return err
		}
		if err == setupweb.ErrNoUIAvailable {
			L_debug("web editor not available, falling back to TUI")
			return setup.RunEdit()
		}
		return err
	}

	if s.TUI {
		return setup.RunWizard()
	}

	err = setupweb.RunWebWizardWithOptions(configPath, s.Dev)
	if s.Web && err == setupweb.ErrNoUIAvailable {
		fmt.Println("\nError: Cannot open web interface.")
		fmt.Println("\nTo use the web wizard, install one of:")
		fmt.Println("  - webkit2gtk: sudo apt install libwebkit2gtk-4.1-0")
		fmt.Println("  - Or any web browser")
		fmt.Println("\nAlternatively, run: goclaw setup --tui")
		return err
	}
	if err == setupweb.ErrNoUIAvailable {
		L_debug("web wizard not available, falling back to TUI")
		return setup.RunWizard()
	}
	return err
}

// SetupWizardCmd forces the full wizard
type SetupWizardCmd struct {
	Web bool `help:"Force web browser interface"`
	TUI bool `help:"Force terminal interface"`
	Dev bool `help:"Enable developer tools (right-click inspect)"`
}

func (s *SetupWizardCmd) Run(ctx *Context) error {
	return runSetupWizardInterface(s.Web, s.TUI, s.Dev, "goclaw setup wizard --tui")
}

// SetupEditCmd edits existing config
type SetupEditCmd struct {
	Web bool `help:"Force web browser interface"`
	TUI bool `help:"Force terminal interface"`
	Dev bool `help:"Enable developer tools (right-click inspect)"`
}

func (s *SetupEditCmd) Run(ctx *Context) error {
	if s.TUI {
		return setup.RunEdit()
	}

	configPath := setupweb.DefaultConfigPath()

	if s.Web {
		// Force web mode - fail with helpful message if not available
		err := setupweb.RunWebEditorWithOptions(configPath, s.Dev)
		if err == setupweb.ErrNoUIAvailable {
			fmt.Println("\nCould not open web editor.")
			fmt.Println("The web-based editor requires either:")
			fmt.Println("  1. webkit2gtk installed (apt install libwebkit2gtk-4.1-0)")
			fmt.Println("  2. A web browser (xdg-open)")
			fmt.Println("\nUse --tui flag for terminal interface instead:")
			fmt.Println("  goclaw setup edit --tui")
			return err
		}
		return err
	}

	// Auto-detect mode: try web, silently fallback to TUI
	err := setupweb.RunWebEditorWithOptions(configPath, s.Dev)
	if err == setupweb.ErrNoUIAvailable {
		L_debug("web editor not available, falling back to TUI")
		return setup.RunEdit()
	}
	return err
}

// SetupGenerateCmd outputs default config template
type SetupGenerateCmd struct {
	Users        bool `help:"Generate users.json instead of goclaw.json"`
	WithPassword bool `help:"Generate a random password for the owner (users.json only)"`
}

func (s *SetupGenerateCmd) Run(ctx *Context) error {
	if s.Users {
		return setup.GenerateDefaultUsers(s.WithPassword)
	}
	return setup.GenerateDefault()
}

// OnboardCmd runs the onboarding wizard
type OnboardCmd struct{}

func (o *OnboardCmd) Run(ctx *Context) error {
	return runSetupWizardInterface(false, false, false, "goclaw onboard --tui")
}

func runSetupWizardInterface(forceWeb, forceTUI, dev bool, tuiHint string) error {
	if forceWeb && forceTUI {
		return fmt.Errorf("cannot use --web and --tui together")
	}
	if forceTUI {
		return setup.RunWizard()
	}

	configPath := setupweb.DefaultConfigPath()

	if forceWeb {
		err := setupweb.RunWebWizardWithOptions(configPath, dev)
		if err == setupweb.ErrNoUIAvailable {
			fmt.Println("\nError: Cannot open web interface.")
			fmt.Println("\nTo use the web wizard, install one of:")
			fmt.Println("  - webkit2gtk: sudo apt install libwebkit2gtk-4.1-0")
			fmt.Println("  - Or any web browser")
			fmt.Printf("\nAlternatively, run: %s\n", tuiHint)
			return err
		}
		return err
	}

	err := setupweb.RunWebWizardWithOptions(configPath, dev)
	if err == setupweb.ErrNoUIAvailable {
		L_debug("web wizard not available, falling back to TUI")
		return setup.RunWizard()
	}
	return err
}

// ConfigCmd shows configuration
type ConfigCmd struct {
	Show ConfigShowCmd `cmd:"" default:"withargs" help:"Show current configuration"`
	Path ConfigPathCmd `cmd:"path" help:"Show path to goclaw.json"`
}

// ConfigShowCmd shows the current configuration
type ConfigShowCmd struct{}

func (c *ConfigShowCmd) Run(ctx *Context) error {
	return setup.ShowConfig()
}

// ConfigPathCmd shows the config file path
type ConfigPathCmd struct{}

func (c *ConfigPathCmd) Run(ctx *Context) error {
	return setup.ShowConfigPath()
}

// SandboxCmd provides CLI helpers for sandbox diagnostics.
type SandboxCmd struct {
	Exec SandboxExecCmd `cmd:"" default:"withargs" help:"Launch an interactive shell as if the exec tool were invoked"`
}

// SandboxExecCmd runs an interactive or one-shot command using exec-tool sandbox bootstrapping.
type SandboxExecCmd struct {
	Mode    string `help:"Optional sandbox mode override for this run (home, autodocs-read, autodocs-write, volumes, ephemeral). Platform rules still apply."`
	CWD     string `help:"Working directory inside command (default: gateway.workingDir from config)"`
	Command string `short:"c" help:"One-shot shell command to execute instead of launching an interactive shell"`
}

func (s *SandboxExecCmd) Run(ctx *Context) error {
	loadResult, err := config.LoadRuntime()
	if err != nil {
		return err
	}
	cfg := loadResult.Config

	if s.Mode != "" {
		allowed := false
		for _, opt := range sandbox.SupportedModeOptions() {
			if opt.Value == s.Mode {
				allowed = true
				break
			}
		}
		if !allowed {
			return fmt.Errorf("invalid mode %q for this platform", s.Mode)
		}
		cfg.Sandbox.General.Mode = s.Mode
	}

	workDir := cfg.Gateway.WorkingDir
	if s.CWD != "" {
		workDir = s.CWD
	}

	sandbox.InitManager(cfg.Sandbox, cfg.Gateway.WorkingDir)
	mgr := sandbox.GetManager()

	useSandbox := cfg.Sandbox.IsExecEnabled()
	if useSandbox {
		if !sbruntime.ExecSandboxAvailable(cfg.Sandbox.GetBackendPath()) {
			L_warn("sandbox exec: backend unavailable, running unsandboxed", "backend", sbruntime.SandboxBackendName())
			useSandbox = false
		}
	}

	command := s.Command
	if command == "" {
		shell := os.Getenv("SHELL")
		if shell == "" {
			if runtime.GOOS == "darwin" {
				shell = "/bin/zsh"
			} else {
				shell = "/bin/bash"
			}
		}
		command = shell + " -i"
	}

	L_info("sandbox exec: launching",
		"sandboxed", useSandbox,
		"mode", mgr.GetMode(),
		"workDir", workDir,
		"command", command,
	)

	var cmd *osexec.Cmd
	if useSandbox {
		policy := mgr.ResolvePolicy()
		autoDocsRoots := mgr.GetAutoDocsRoots()
		extraBind := append([]string{}, cfg.Tools.Exec.Bubblewrap.ExtraBind...)
		extraRoBind := append([]string{}, cfg.Tools.Exec.Bubblewrap.ExtraRoBind...)
		if mgr.IsAutoDocsWriteMode() {
			extraBind = append(extraBind, autoDocsRoots...)
		} else {
			extraRoBind = append(extraRoBind, autoDocsRoots...)
		}
		pathValue := os.Getenv("PATH")
		if runtime.GOOS == "darwin" {
			pathValue = mgr.BuildSandboxPATH(policy.VisibleHomeDir)
		}

		cmd, err = sbruntime.BuildExecCommand(command, sbruntime.ExecLaunchOptions{
			BackendPath:    cfg.Sandbox.GetBackendPath(),
			SandboxMode:    mgr.GetMode(),
			WorkspaceDir:   cfg.Gateway.WorkingDir,
			WorkDir:        workDir,
			VisibleHomeDir: policy.VisibleHomeDir,
			BackingHomeDir: policy.BackingHomeDir,
			PathValue:      pathValue,
			Volumes:        runtimeVolumesForSandboxExec(mgr.GetVolumes()),
			ProtectedDirs:  mgr.GetProtectedDirs(),
			ClearEnv:       cfg.Tools.Exec.Bubblewrap.ClearEnv,
			AllowNetwork:   cfg.Tools.Exec.Bubblewrap.AllowNetwork,
			ExtraEnv:       cfg.Tools.Exec.Bubblewrap.ExtraEnv,
			ExtraBind:      extraBind,
			ExtraRoBind:    extraRoBind,
		})
		if err != nil {
			return err
		}
		if cmd == nil {
			useSandbox = false
		}
	}

	if !useSandbox {
		// G204: command is intentionally user-provided via CLI (goclaw exec "...")
		cmd = osexec.Command("bash", "-lc", command) //nolint:gosec
		cmd.Dir = workDir
	}

	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*osexec.ExitError); ok {
			return &exec.Error{ExitCode: exitErr.ExitCode(), Err: err}
		}
		return err
	}
	return nil
}

func runtimeVolumesForSandboxExec(vols []sandbox.SandboxVolume) []sbruntime.SandboxVolume {
	out := make([]sbruntime.SandboxVolume, 0, len(vols))
	for _, vol := range vols {
		out = append(out, sbruntime.SandboxVolume{
			MountPoint: vol.MountPoint,
			Source:     vol.Source,
		})
	}
	return out
}

// TUICmd is a top-level shortcut for goclaw tui
type TUICmd struct {
	Dev bool `help:"Development mode: reload HTML templates from disk on each request"`
}

func (t *TUICmd) Run(ctx *Context) error {
	return runGateway(ctx, true, t.Dev)
}

// Context passed to all commands
type Context struct {
	Debug bool
	Trace bool
}

// runGateway is the actual gateway logic
func runGateway(ctx *Context, useTUI bool, devMode bool) error {
	L_info("starting gateway", "version", version)

	// Load config (handles bootstrap from openclaw.json if needed)
	loadResult, err := config.LoadRuntime()
	if err != nil {
		return err
	}
	cfg := loadResult.Config
	L_debug("config loaded", "path", loadResult.SourcePath)

	postUpdateState, err := readPostUpdateMarkerFromEnv()
	if err != nil {
		L_warn("failed to read post-update marker", "error", err)
		update.ClearPostUpdateMarkerEnv()
		postUpdateState = nil
	}

	// Self-heal older installs by backfilling missing workspace templates
	// and required subdirectories without overwriting existing files.
	if err := setup.CreateWorkspace(cfg.Gateway.WorkingDir); err != nil {
		L_warn("failed to ensure workspace", "dir", cfg.Gateway.WorkingDir, "error", err)
	}

	// Change process working directory to workspace
	// This ensures agent's view matches reality for any code using os.Getwd()
	if err := os.Chdir(cfg.Gateway.WorkingDir); err != nil {
		L_warn("failed to chdir to workspace", "dir", cfg.Gateway.WorkingDir, "error", err)
	} else {
		L_debug("changed working directory", "dir", cfg.Gateway.WorkingDir)
	}

	// Load users from users.json (new format)
	usersConfig, err := user.LoadUsers()
	if err != nil {
		L_error("failed to load users", "error", err)
		return err
	}

	// Create user registry from users.json with roles from config
	users := user.NewRegistryFromUsers(usersConfig, cfg.Roles)
	L_debug("user registry created", "users", users.Count())

	// Create LLM registry from config
	if len(cfg.LLM.Providers) == 0 {
		L_error("no LLM providers configured")
		return fmt.Errorf("llm.providers must be configured in goclaw.json")
	}

	llmRegistry, err := buildLLMRegistry(cfg)
	if err != nil {
		L_error("failed to create LLM registry", "error", err)
		return err
	}
	llm.SetGlobalRegistry(llmRegistry)
	L_info("LLM registry created", "providers", len(cfg.LLM.Providers))

	// Initialize sandbox manager singleton before checking backend availability.
	sandbox.InitManager(cfg.Sandbox, cfg.Gateway.WorkingDir)

	// Check sandbox backend availability for managed exec/browser sandboxing.
	execSandboxEnabled := cfg.Sandbox.IsExecEnabled()
	browserSandboxEnabled := cfg.Sandbox.IsBrowserEnabled()
	sandboxDisabledReason := "" // Track if sandbox was disabled for later warning
	if execSandboxEnabled || browserSandboxEnabled {
		backendPath := cfg.Sandbox.GetBackendPath()
		execAvailable := sbruntime.ExecSandboxAvailable(backendPath)
		browserAvailable := sbruntime.BrowserSandboxAvailable(backendPath)
		if !execAvailable && !browserAvailable {
			L_warn("sandbox: managed sandbox backend unavailable, disabling",
				"backend", sbruntime.SandboxBackendName())
			cfg.Sandbox.General.ExecEnabled = false
			cfg.Sandbox.General.BrowserEnabled = false
			sandboxDisabledReason = sbruntime.SandboxBackendName() + " unavailable"
		} else {
			if execSandboxEnabled && !execAvailable {
				L_warn("sandbox: exec sandbox backend unavailable, disabling exec sandbox",
					"backend", sbruntime.SandboxBackendName())
				cfg.Sandbox.General.ExecEnabled = false
			}
			if browserSandboxEnabled && !browserAvailable {
				L_warn("sandbox: browser sandbox backend unavailable, disabling browser sandbox",
					"backend", sbruntime.SandboxBackendName())
				cfg.Sandbox.General.BrowserEnabled = false
			}
		}
		if cfg.Sandbox.IsExecEnabled() || cfg.Sandbox.IsBrowserEnabled() {
			L_info("sandbox: backend available",
				"backend", sbruntime.SandboxBackendName(),
				"execEnabled", execSandboxEnabled,
				"browserEnabled", browserSandboxEnabled)
		}
	}

	// Initialize browser manager for web_fetch fallback and browser tool
	if cfg.Tools.Browser.Enabled {
		browserCfg := browser.ToolsConfigAdapter{
			Dir:                           cfg.Tools.Browser.Dir,
			AutoDownload:                  cfg.Tools.Browser.AutoDownload,
			Revision:                      cfg.Tools.Browser.Revision,
			Headless:                      cfg.Tools.Browser.Headless,
			NoSandbox:                     cfg.Tools.Browser.NoSandbox,
			DefaultProfile:                cfg.Tools.Browser.DefaultProfile,
			Timeout:                       cfg.Tools.Browser.Timeout,
			Stealth:                       cfg.Tools.Browser.Stealth,
			Device:                        cfg.Tools.Browser.Device,
			ProfileDomains:                cfg.Tools.Browser.ProfileDomains,
			ChromeCDP:                     cfg.Tools.Browser.ChromeCDP,
			AllowAgentProfiles:            cfg.Tools.Browser.AllowAgentProfiles,
			RemoteEnabled:                 cfg.Tools.Browser.Remote.Enabled,
			RemoteProfilesText:            cfg.Tools.Browser.Remote.ProfilesText,
			RemoteAllowedHosts:            cfg.Tools.Browser.Remote.AllowedHosts,
			RemoteAllowDirectEndpoints:    cfg.Tools.Browser.Remote.AllowDirectEndpoints,
			RemoteAllowHTTPDiscovery:      cfg.Tools.Browser.Remote.AllowHTTPDiscovery,
			RemoteConnectionTimeout:       cfg.Tools.Browser.Remote.ConnectionTimeout,
			AdvancedNetworkCaptureEnabled: cfg.Tools.Browser.Advanced.NetworkCaptureEnabled,
			AdvancedNetworkCaptureMax:     cfg.Tools.Browser.Advanced.NetworkCaptureMax,
			AdvancedConsoleCaptureEnabled: cfg.Tools.Browser.Advanced.ConsoleCaptureEnabled,
			AdvancedConsoleCaptureMax:     cfg.Tools.Browser.Advanced.ConsoleCaptureMax,
			AdvancedTraceDir:              cfg.Tools.Browser.Advanced.TraceDir,
			AdvancedTraceRetention:        cfg.Tools.Browser.Advanced.TraceRetention,
			Workspace:                     cfg.Gateway.WorkingDir,
			BubblewrapEnabled:             cfg.Sandbox.IsBrowserEnabled(),
			BubblewrapPath:                cfg.Sandbox.GetBackendPath(),
			BubblewrapGPU:                 cfg.Tools.Browser.Bubblewrap.GPU,
			ExtraRoBind:                   cfg.Tools.Browser.Bubblewrap.ExtraRoBind,
			ExtraBind:                     cfg.Tools.Browser.Bubblewrap.ExtraBind,
		}.ToConfig()

		browserMgr, err := browser.InitManager(browserCfg)
		if err != nil {
			L_warn("browser: failed to initialize manager", "error", err)
		} else {
			defer browserMgr.CloseAll()
			L_info("browser: manager initialized",
				"headless", cfg.Tools.Browser.Headless,
				"sandbox", cfg.Sandbox.IsBrowserEnabled())
		}
	} else {
		L_info("browser: disabled by configuration")
	}

	// Create tool registry (tools registered after gateway is ready)
	toolsReg := tools.NewRegistry()

	// Create gateway (creates MediaStore internally)
	gw, err := gateway.New(cfg, loadResult.SourcePath, users, llmRegistry, toolsReg)
	if err != nil {
		L_error("failed to create gateway", "error", err)
		return fmt.Errorf("failed to create gateway: %w", err)
	}
	L_info("gateway initialized")

	acp.InitManager(cfg.Gateway.WorkingDir, cfg.ACP.Drivers.Cursor.Model)

	// Register all tools now that gateway and managers are ready
	messageTool, transcriptMgr := registerTools(toolsReg, cfg, gw, version)

	// Register component config commands
	// These allow config forms to trigger test/apply actions via the bus
	media.RegisterCommands()
	httpconfig.RegisterCommands()
	tuiconfig.RegisterCommands()
	telegramconfig.RegisterCommands()
	session.RegisterCommands()
	skills.RegisterCommands()
	cron.RegisterCommands()
	auth.RegisterCommands()
	acp.RegisterCommands()
	a2a.RegisterCommands()
	gateway.RegisterCommands()
	transcript.RegisterCommands()
	llm.RegisterCommands()
	stt.RegisterCommands()
	L_debug("config commands registered")

	// Setup context with cancellation for graceful shutdown
	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start session file watcher for live OpenClaw sync
	if err := gw.StartSessionWatcher(runCtx); err != nil {
		L_warn("failed to start session watcher", "error", err)
		// Continue anyway - we just won't get live updates
	}

	// Start gateway background tasks (compaction retry, etc.)
	gw.Start(runCtx)

	// Start memory graph background tasks (maintenance + live extraction)
	if mgraphMgr := gw.MemoryGraphManager(); mgraphMgr != nil {
		mgraphMgr.Start(runCtx)
	}

	// Handle signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigCh
		L_info("received signal", "signal", sig)
		signal.Stop(sigCh) // Prevent handling the same signal twice
		runtimeinfo.SetShuttingDown()
		L_info("application shutting down")
		// Cancel runCtx FIRST so goroutines (compaction retry, etc.) can exit.
		// Otherwise Shutdown blocks waiting for them while they wait on I/O.
		cancel()
		// Stop transcript manager BEFORE gateway shutdown (uses gateway's SQLite DB)
		if transcriptMgr != nil {
			transcriptMgr.Stop()
		}
		// Stop memory graph background processes
		if mgraphMgr := gw.MemoryGraphManager(); mgraphMgr != nil {
			mgraphMgr.Stop()
		}
		metrics.GetInstance().Close() //nolint:errcheck // shutdown cleanup
		gw.Shutdown()
	}()

	// Create channel manager
	chanMgr := channels.NewManager(gw, users)

	// Subscribe to channel events to update message tool adapters
	bus.SubscribeEvent("channels.telegram.started", func(event bus.Event) {
		if bot := chanMgr.GetTelegram(); bot != nil {
			if mediaStore := gw.MediaStore(); mediaStore != nil {
				adapter := telegram.NewMessageChannelAdapter(bot, mediaStore.BaseDir())
				messageTool.SetChannel("telegram", adapter)
			}
		}
	})
	bus.SubscribeEvent("channels.telegram.stopped", func(event bus.Event) {
		messageTool.RemoveChannel("telegram")
	})
	bus.SubscribeEvent("channels.whatsapp.started", func(event bus.Event) {
		if bot := chanMgr.GetWhatsApp(); bot != nil {
			if mediaStore := gw.MediaStore(); mediaStore != nil {
				adapter := whatsapp.NewMessageChannelAdapter(bot, mediaStore.BaseDir())
				messageTool.SetChannel("whatsapp", adapter)
			}
		}
	})
	bus.SubscribeEvent("channels.whatsapp.stopped", func(event bus.Event) {
		messageTool.RemoveChannel("whatsapp")
	})
	bus.SubscribeEvent("channels.http.started", func(event bus.Event) {
		if srv := chanMgr.GetHTTP(); srv != nil {
			adapter := goclawhttp.NewMessageChannelAdapter(srv.Channel(), "/api/media")
			messageTool.SetChannel("http", adapter)
		}
	})
	bus.SubscribeEvent("channels.http.stopped", func(event bus.Event) {
		messageTool.RemoveChannel("http")
	})
	L_debug("message tool registered (channels will be added dynamically)")

	// Start all enabled channels via manager
	if err := chanMgr.StartAll(runCtx, cfg.Channels, channels.RuntimeOptions{DevMode: devMode}); err != nil {
		L_error("channels: failed to start", "error", err)
	}

	// Initialize VoiceLLM after HTTP server is running
	if err := chanMgr.InitVoiceLLM(runCtx, cfg.VoiceLLM); err != nil {
		L_error("voicellm: failed to initialize", "error", err)
	}

	// Start cron service AFTER channels are registered
	if cfg.Cron.Enabled {
		if err := gw.StartCron(runCtx); err != nil {
			L_error("cron: failed to start service", "error", err)
		} else if cronSvc := gw.CronService(); cronSvc != nil {
			cronSvc.RegisterOperationalCommands()
		}
	} else {
		L_info("cron: disabled by configuration")
	}

	if err := handlePostUpdateAfterStartup(runCtx, gw, users, postUpdateState); err != nil {
		L_warn("post-update handling failed", "error", err)
	}

	if useTUI {
		// Run TUI mode
		L_info("starting TUI mode")
		return runTUI(runCtx, gw, users, cfg.Channels.TUI.ShowLogs, sandboxDisabledReason)
	}

	// Non-TUI mode: just wait for signals
	if sandboxDisabledReason != "" {
		L_warn("sandbox: sandboxing is disabled", "reason", sandboxDisabledReason,
			"hint", "install bubblewrap and restart to enable")
	}
	L_info("gateway ready")
	L_info("press Ctrl+C to stop")

	<-runCtx.Done()
	L_info("gateway shutting down")

	// Stop all channels via manager
	chanMgr.StopAll()

	if configapply.RestartRequested() {
		L_info("gateway restart requested")
		return configapply.ErrRestartRequested
	}

	return nil
}

// runTUI runs the interactive TUI mode
func runTUI(ctx context.Context, gw *gateway.Gateway, users *user.Registry, showLogs bool, sandboxDisabledReason string) error {
	owner := users.Owner()
	if owner == nil {
		return fmt.Errorf("no owner user configured")
	}

	// Log sandbox warning after TUI starts (in a goroutine so TUI can initialize first)
	if sandboxDisabledReason != "" {
		go func() {
			time.Sleep(100 * time.Millisecond) // Brief delay for TUI to initialize
			L_warn("sandbox: sandboxing is disabled", "reason", sandboxDisabledReason,
				"hint", "install bubblewrap and restart to enable")
		}()
	}

	return tui.Run(ctx, gw, owner, showLogs)
}

// getPidFromFile returns the pid and whether the process is running
func getPidFromFile(pidFile string) (int, bool) {
	pidBytes, err := os.ReadFile(pidFile)
	if err != nil {
		return 0, false
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(pidBytes)))
	if err != nil {
		return 0, false
	}

	// Check if process is alive
	process, err := os.FindProcess(pid)
	if err != nil {
		return pid, false
	}

	err = process.Signal(syscall.Signal(0))
	if err != nil {
		os.Remove(pidFile)
		return pid, false
	}

	return pid, true
}

// isRunningAt checks if gateway is already running using the given pid file
func isRunningAt(pidFile string) bool {
	_, running := getPidFromFile(pidFile)
	return running
}

func main() {
	cli := CLI{}
	ctx := kong.Parse(&cli,
		kong.Name("goclaw"),
		kong.Description("A Go rewrite of OpenClaw"),
		kong.UsageOnError(),
	)

	// Initialize logging based on flags
	level := LevelInfo
	if cli.Trace {
		level = LevelTrace
	} else if cli.Debug {
		level = LevelDebug
	}

	Init(&LogConfig{
		Level:      level,
		ShowCaller: true,
	})
	RedirectStdlibLog()

	if cwd, err := os.Getwd(); err != nil {
		L_warn("failed to capture launch cwd", "error", err)
	} else {
		runtimeinfo.SetLaunchCwd(cwd)
		L_debug("captured launch cwd", "dir", cwd)
	}

	// Hard-stop contract gate: any invalid setup FormDef/ShowWhen path aborts process.
	if err := setupweb.ValidateAllSectionContractsStrict(); err != nil {
		L_error("startup: strict setup contract validation failed", "error", err)
		fmt.Fprintf(os.Stderr, "startup aborted: strict setup contract validation failed: %v\n", err)
		os.Exit(1)
	}

	// Run the selected command
	err := ctx.Run(&Context{
		Debug: cli.Debug,
		Trace: cli.Trace,
	})
	if err != nil {
		if configapply.IsRestartRequestedError(err) {
			os.Exit(configapply.RestartExitCode)
		}
		// Print user-facing errors cleanly without log formatting
		errMsg := err.Error()
		if strings.HasPrefix(errMsg, "no goclaw.json") ||
			strings.HasPrefix(errMsg, "goclaw.json is empty") ||
			strings.HasPrefix(errMsg, "at least one") ||
			strings.HasPrefix(errMsg, "setup:") ||
			strings.Contains(errMsg, "user aborted") {
			fmt.Fprintln(os.Stderr, errMsg)
			os.Exit(1)
		}
		L_fatal("command failed", "error", err)
	}
}

// readPassword reads a password from stdin without echoing
func readPassword() ([]byte, error) {
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		return term.ReadPassword(fd)
	}
	// Fallback for non-terminal (piped input)
	var password string
	if _, err := fmt.Scanln(&password); err != nil {
		return nil, fmt.Errorf("failed to read password: %w", err)
	}
	return []byte(password), nil
}

// runUpdate handles the update command
func runUpdate(checkOnly bool, channel string, noRestart, force bool) error {
	// Check if system-managed
	if update.IsSystemManaged() {
		exePath, _ := update.GetExecutablePath()
		fmt.Println("GoClaw is installed at a system-managed location:")
		fmt.Printf("  %s\n\n", exePath)
		fmt.Println("Please update using your package manager:")
		fmt.Println()
		fmt.Println("  # For Debian/Ubuntu:")
		fmt.Println("  sudo apt update && sudo apt upgrade goclaw")
		fmt.Println()
		fmt.Println("  # Or download the latest .deb from:")
		fmt.Println("  https://github.com/roelfdiedericks/goclaw/releases/latest")
		return nil
	}

	updater := update.NewUpdater(version)

	fmt.Printf("Checking for updates (channel: %s)...\n", channel)

	info, err := updater.CheckForUpdate(channel)
	if err != nil {
		return fmt.Errorf("failed to check for updates: %w", err)
	}

	fmt.Printf("Current version: %s\n", info.CurrentVersion)
	fmt.Printf("Latest version:  %s (%s)\n", info.NewVersion, info.Channel)

	if !info.IsNewer && !force {
		fmt.Println("\nYou are already running the latest version.")
		return nil
	}

	if !info.IsNewer && force {
		fmt.Println("\nForcing reinstall of current version.")
	} else {
		fmt.Println("\nA new version is available!")
	}

	// Show changelog preview
	if info.Changelog != "" {
		fmt.Println("\nChangelog:")
		fmt.Println("----------")
		// Truncate changelog if too long
		changelog := info.Changelog
		if len(changelog) > 1000 {
			changelog = changelog[:1000] + "\n..."
		}
		fmt.Println(changelog)
		fmt.Println()
	}

	if checkOnly {
		fmt.Println("Run 'goclaw update' to install the update.")
		return nil
	}

	// Download with progress
	fmt.Println("Downloading...")
	binaryPath, err := updater.Download(info, func(downloaded, total int64) {
		if total > 0 {
			pct := float64(downloaded) / float64(total) * 100
			fmt.Printf("\r  %.1f%% (%d / %d bytes)", pct, downloaded, total)
		}
	})
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	fmt.Println("\n  Download complete!")

	// Apply update
	fmt.Println("Installing...")
	if err := updater.Apply(binaryPath, info, noRestart, "goclaw update"); err != nil {
		return fmt.Errorf("failed to apply update: %w", err)
	}

	if noRestart {
		fmt.Println("\nUpdate installed successfully!")
		fmt.Println("Restart GoClaw to use the new version.")
	}
	// If not noRestart, the process will be replaced via exec and this line won't be reached

	return nil
}

// registerTools registers all agent tools in one place, after the gateway and all
// managers are ready. Returns the message tool (for dynamic channel updates) and
// the transcript manager (for shutdown cleanup).
func registerTools(reg *tools.Registry, cfg *config.Config, gw *gateway.Gateway, version string) (*toolmessage.Tool, *transcript.Manager) {
	// File tools
	reg.Register(read.NewTool(cfg.Gateway.WorkingDir))
	reg.Register(write.NewTool(cfg.Gateway.WorkingDir))
	reg.Register(edit.NewTool(cfg.Gateway.WorkingDir))

	// Exec tool
	execTimeout := 30 * time.Minute
	if cfg.Tools.Exec.Timeout > 0 {
		execTimeout = time.Duration(cfg.Tools.Exec.Timeout) * time.Second
	}
	execRunner := exec.NewRunner(exec.RunnerConfig{
		WorkingDir:     cfg.Gateway.WorkingDir,
		Timeout:        execTimeout,
		BubblewrapPath: cfg.Sandbox.GetBackendPath(),
		Bubblewrap: exec.BubblewrapConfig{
			Enabled:      cfg.Sandbox.IsExecEnabled(),
			ExtraRoBind:  cfg.Tools.Exec.Bubblewrap.ExtraRoBind,
			ExtraBind:    cfg.Tools.Exec.Bubblewrap.ExtraBind,
			ExtraEnv:     cfg.Tools.Exec.Bubblewrap.ExtraEnv,
			AllowNetwork: cfg.Tools.Exec.Bubblewrap.AllowNetwork,
			ClearEnv:     cfg.Tools.Exec.Bubblewrap.ClearEnv,
		},
	})
	reg.Register(exec.NewToolWithRunner(execRunner))

	// JQ tool (shares exec runner for sandbox)
	reg.Register(jq.NewTool(cfg.Gateway.WorkingDir, execRunner))

	// Web search
	if cfg.Tools.Web.Search.Enabled {
		reg.Register(websearch.NewTool(cfg.Tools.Web, cfg.LLM.Providers))
	}

	// Web fetch
	reg.Register(webfetch.NewToolWithConfig(webfetch.ToolConfig{
		UseBrowser: cfg.Tools.Web.UseBrowser,
		Profile:    cfg.Tools.Web.Profile,
		Headless:   cfg.Tools.Web.Headless,
	}))

	// Cron tool
	reg.Register(toolcron.NewTool())
	reg.Register(toolacpcontrol.NewTool())
	reg.Register(toolacpinspect.NewTool())
	if cfg.Tools.Subagent.Enabled && cfg.Gateway.DelegatedRuns.Enabled {
		reg.Register(toolsubagentspawn.NewTool(
			func(ctx context.Context, u *user.User, source, sessionKey, runID, message, toolError string) error {
				return gw.InjectDelegatedReturnToSession(ctx, u, source, sessionKey, runID, message, toolError)
			},
			func(ctx context.Context, u *user.User, source, message string) error {
				return gw.DeliverToolMessageToChannel(ctx, gateway.ToolMessageParams{
					User:    u,
					Source:  source,
					Message: message,
				}, source)
			},
		))
		reg.Register(toolsubagentstatus.NewTool())
		reg.Register(toolsubagentcancel.NewTool())
		reg.Register(toolsubagentfanout.NewToolWithReturnToRequester(
			func(ctx context.Context, u *user.User, source, sessionKey, runID, message, toolError string) error {
				return gw.InjectDelegatedReturnToSession(ctx, u, source, sessionKey, runID, message, toolError)
			},
			func(ctx context.Context, u *user.User, source, message string) error {
				return gw.DeliverToolMessageToChannel(ctx, gateway.ToolMessageParams{
					User:    u,
					Source:  source,
					Message: message,
				}, source)
			},
		))
	} else if cfg.Tools.Subagent.Enabled && !cfg.Gateway.DelegatedRuns.Enabled {
		L_warn("tools: subagent tools requested but delegated runs are disabled; skipping subagent tool registration",
			"tools.subagent.enabled", cfg.Tools.Subagent.Enabled,
			"gateway.delegatedRuns.enabled", cfg.Gateway.DelegatedRuns.Enabled)
	}

	// GoClaw update tool
	reg.Register(toolupdate.NewTool(version))

	// Browser tool
	if cfg.Tools.Browser.Enabled {
		if mediaStore := gw.MediaStore(); mediaStore != nil {
			if browserMgr := browser.GetManager(); browserMgr != nil {
				reg.Register(browser.NewTool(browserMgr, mediaStore))
			}
		}
	}

	// Memory tools
	if memMgr := gw.MemoryManager(); memMgr != nil {
		reg.Register(memorysearch.NewTool(memMgr))
		reg.Register(memoryget.NewTool(memMgr))
	}

	// Memory graph tools
	if mgraphMgr := gw.MemoryGraphManager(); mgraphMgr != nil {
		// Connect to sessions.db for live extraction
		if sessDB := gw.SessionDB(); sessDB != nil {
			mgraphMgr.SetSessionsDB(sessDB)
		}
		// Register tools (new recall-first versions)
		reg.Register(memorygraph.NewRecallTool(mgraphMgr))
		reg.Register(memorygraph.NewQueryTool(mgraphMgr))
		reg.Register(memorygraph.NewStoreTool(mgraphMgr))
		reg.Register(memorygraph.NewUpdateTool(mgraphMgr))
		reg.Register(memorygraph.NewForgetTool(mgraphMgr))
		// Keep search tool for backwards compatibility
		reg.Register(toolmemorygraph.NewSearchTool())
	}

	// Skills tool
	if skillsMgr := gw.SkillManager(); skillsMgr != nil {
		reg.Register(toolskills.NewTool(skillsMgr))
		skillsMgr.RegisterOperationalCommands()
	}

	// User auth tool
	if cfg.Auth.Enabled && cfg.Auth.Script != "" {
		reg.Register(userauth.NewTool(cfg.Auth, cfg.Roles))
	}

	// Home Assistant tool
	if cfg.HomeAssistant.Enabled && cfg.HomeAssistant.Token != "" {
		if mediaStore := gw.MediaStore(); mediaStore != nil {
			wsClient := hass.NewWSClient(cfg.HomeAssistant)
			dataDir, _ := paths.BaseDir()
			hassManager := hass.NewManager(cfg.HomeAssistant, gw, dataDir)
			gw.SetHassManager(hassManager)
			if err := gw.StartHassManager(context.Background()); err != nil {
				L_warn("hass: failed to start manager", "error", err)
			}
			hassTool, err := toolhass.NewTool(cfg.HomeAssistant, mediaStore, wsClient, hassManager)
			if err != nil {
				L_warn("hass: tool not registered", "error", err)
			} else {
				reg.Register(hassTool)
			}
		}
	}

	// xAI Imagine tool
	if cfg.Tools.XAIImagine.Enabled {
		if mediaStore := gw.MediaStore(); mediaStore != nil {
			xaiImagineTool, err := xaiimagine.NewTool(cfg.Tools.XAIImagine, mediaStore)
			if err != nil {
				L_warn("xai_imagine: tool not registered", "error", err)
			} else {
				reg.Register(xaiImagineTool)
			}
		}
	}

	// xAI Video tool
	if cfg.Tools.XAIVideo.Enabled {
		if mediaStore := gw.MediaStore(); mediaStore != nil {
			xaiVideoTool, err := xaivideo.NewTool(cfg.Tools.XAIVideo, mediaStore)
			if err != nil {
				L_warn("xai_video: tool not registered", "error", err)
			} else {
				reg.Register(xaiVideoTool)
			}
		}
	}

	// Message tool (channels added dynamically via bus events)
	messageTool := toolmessage.NewTool(nil)
	if mediaStore := gw.MediaStore(); mediaStore != nil {
		reg.Register(toolmedia.NewTool(mediaStore))
		messageTool.SetMediaRoot(mediaStore.BaseDir())
	}
	reg.Register(messageTool)

	// Media display tool (for voice sessions - creates synthetic {{media:}} messages)
	// Uses DeliverToolMessage to persist and deliver to ALL channels (including source)
	reg.Register(toolmediadisplay.NewTool(func(ctx context.Context, u *user.User, source, msg string) error {
		return gw.DeliverToolMessage(ctx, gateway.ToolMessageParams{
			User:    u,
			Source:  source,
			Message: msg,
		})
	}))

	// Transcript tool (also creates the manager)
	var transcriptMgr *transcript.Manager
	if cfg.Transcript.Enabled {
		if db := gw.SessionDB(); db != nil {
			var embeddingProvider llm.EmbeddingProvider
			if memMgr := gw.MemoryManager(); memMgr != nil {
				embeddingProvider = memMgr.Provider()
			}
			var err error
			transcriptMgr, err = transcript.NewManager(db, embeddingProvider, cfg.Transcript)
			if err != nil {
				L_warn("transcript: failed to initialize", "error", err)
			} else {
				if cfg.Agent.Name != "" {
					transcriptMgr.SetAgentName(cfg.Agent.Name)
				}
				transcriptMgr.Start()
				transcriptMgr.RegisterOperationalCommands()
				reg.Register(tooltranscript.NewTool(transcriptMgr))
			}
		}
	}

	L_info("tools: registered", "count", reg.Count())
	return messageTool, transcriptMgr
}
