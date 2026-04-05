package a2a

import (
	"fmt"

	"github.com/roelfdiedericks/goclaw/internal/bus"
	"github.com/roelfdiedericks/goclaw/internal/config/forms"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

const (
	configPath              = "a2a"
	DefaultTransportLibp2p  = "libp2p"
	DefaultRPCProtocolID    = "/goclaw/a2a/rpc/1.0.0"
	DefaultRendezvousID     = "/goclaw/a2a/rendezvous/1.0.0"
	DefaultRendezvousNS     = "goclaw-a2a-v1"
	DefaultIdentityKeyFile  = "~/.goclaw/a2a/libp2p/identity.key"
	DefaultListenTCP        = "/ip4/0.0.0.0/tcp/4001"
	DefaultListenQUIC       = "/ip4/0.0.0.0/udp/4001/quic-v1"
	DefaultLocalListenTCP   = "/ip4/0.0.0.0/tcp/0"
	DefaultLocalListenQUIC  = "/ip4/0.0.0.0/udp/0/quic-v1"
	DefaultRetentionSeconds = 3600
	DefaultBootstrapSeedTXT = "p2p_boot.goclaw.org"
)

type Config struct {
	Enabled          bool         `json:"enabled" default:"false"`
	DefaultTransport string       `json:"defaultTransport" default:"libp2p"`
	Libp2p           Libp2pConfig `json:"libp2p"`
}

type Libp2pConfig struct {
	Enabled        bool                `json:"enabled" default:"false"`
	Identity       IdentityConfig      `json:"identity"`
	ListenAddrs    []string            `json:"listenAddrs"`
	BootstrapPeers []string            `json:"bootstrapPeers"`
	Discovery      DiscoveryConfig     `json:"discovery"`
	Relay          RelayConfig         `json:"relay"`
	TrustedPeers   []TrustedPeerConfig `json:"trustedPeers"`
	Protocol       ProtocolConfig      `json:"protocol"`
}

type IdentityConfig struct {
	KeyFile string `json:"keyFile" default:"~/.goclaw/a2a/libp2p/identity.key"`
	KeyType string `json:"keyType" default:"ed25519"`
}

type DiscoveryConfig struct {
	MDNSEnabled          bool   `json:"mdnsEnabled" default:"false"`
	ServiceName          string `json:"serviceName" default:"goclaw-a2a-v1"`
	DHTEnabled           bool   `json:"dhtEnabled" default:"false"`
	RendezvousEnabled    bool   `json:"rendezvousEnabled" default:"true"`
	RendezvousNamespace  string `json:"rendezvousNamespace" default:"goclaw-a2a-v1"`
	BootstrapSeedTXT     string `json:"bootstrapSeedTXT" default:"p2p_boot.goclaw.org"`
	RegisterIntervalSecs int    `json:"registerIntervalSeconds" default:"30"`
	QueryIntervalSecs    int    `json:"queryIntervalSeconds" default:"30"`
}

type RelayConfig struct {
	EnableClient bool     `json:"enableClient" default:"true"`
	EnableServer bool     `json:"enableServer" default:"false"`
	StaticRelays []string `json:"staticRelays"`
}

type ProtocolConfig struct {
	RPCProtocolID        string `json:"rpcProtocolId" default:"/goclaw/a2a/rpc/1.0.0"`
	RendezvousProtocolID string `json:"rendezvousProtocolId" default:"/goclaw/a2a/rendezvous/1.0.0"`
	StateRetentionSecs   int    `json:"stateRetentionSeconds" default:"3600"`
}

type TrustedPeerConfig struct {
	Alias     string   `json:"alias"`
	PeerID    string   `json:"peerId"`
	Addrs     []string `json:"addrs,omitempty"`
	LocalUser string   `json:"localUser"`
	Enabled   bool     `json:"enabled"`
	Notes     string   `json:"notes,omitempty"`
}

func (c *Config) Normalize() {
	if c.DefaultTransport == "" {
		c.DefaultTransport = DefaultTransportLibp2p
	}
	if c.Libp2p.Identity.KeyFile == "" {
		c.Libp2p.Identity.KeyFile = DefaultIdentityKeyFile
	}
	if c.Libp2p.Identity.KeyType == "" {
		c.Libp2p.Identity.KeyType = "ed25519"
	}
	if len(c.Libp2p.ListenAddrs) == 0 {
		c.Libp2p.ListenAddrs = []string{DefaultListenTCP, DefaultListenQUIC}
	}
	if c.Libp2p.Discovery.ServiceName == "" {
		c.Libp2p.Discovery.ServiceName = DefaultRendezvousNS
	}
	if c.Libp2p.Discovery.RendezvousNamespace == "" {
		c.Libp2p.Discovery.RendezvousNamespace = DefaultRendezvousNS
	}
	if c.Libp2p.Discovery.BootstrapSeedTXT == "" {
		c.Libp2p.Discovery.BootstrapSeedTXT = DefaultBootstrapSeedTXT
	}
	if c.Libp2p.Discovery.RegisterIntervalSecs <= 0 {
		c.Libp2p.Discovery.RegisterIntervalSecs = 30
	}
	if c.Libp2p.Discovery.QueryIntervalSecs <= 0 {
		c.Libp2p.Discovery.QueryIntervalSecs = 30
	}
	if c.Libp2p.Protocol.RPCProtocolID == "" {
		c.Libp2p.Protocol.RPCProtocolID = DefaultRPCProtocolID
	}
	if c.Libp2p.Protocol.RendezvousProtocolID == "" {
		c.Libp2p.Protocol.RendezvousProtocolID = DefaultRendezvousID
	}
	if c.Libp2p.Protocol.StateRetentionSecs <= 0 {
		c.Libp2p.Protocol.StateRetentionSecs = DefaultRetentionSeconds
	}
	for i := range c.Libp2p.TrustedPeers {
		if !c.Libp2p.TrustedPeers[i].Enabled {
			continue
		}
	}
}

func ConfigFormDef() forms.FormDef {
	return forms.FormDef{
		Title:       "A2A",
		Description: "Configure Agent2Agent networking, identity, bootstrap peers, relay behaviour, and trusted peer mappings.",
		Sections: []forms.Section{
			{
				Title: "General",
				Fields: []forms.Field{
					{Name: "enabled", Title: "Enable A2A", Type: forms.Toggle, Default: false, Desc: "Enable the A2A subsystem at startup."},
					{
						Name:    "defaultTransport",
						Title:   "Default Transport",
						Type:    forms.Select,
						Default: DefaultTransportLibp2p,
						Desc:    "The only active A2A transport in v1.",
						Options: []forms.Option{{Label: "libp2p", Value: DefaultTransportLibp2p}},
					},
				},
			},
			{
				Title: "libp2p - Identity",
				Fields: []forms.Field{
					{Name: "libp2p.enabled", Title: "Enable libp2p Driver", Type: forms.Toggle, Default: false, Desc: "Enable libp2p transport support."},
					{Name: "libp2p.identity.keyFile", Title: "Identity Key File", Type: forms.Text, Default: DefaultIdentityKeyFile, Desc: "Persistent libp2p private key path."},
					{
						Name:    "libp2p.identity.keyType",
						Title:   "Identity Key Type",
						Type:    forms.Select,
						Default: "ed25519",
						Desc:    "Persistent libp2p identity algorithm.",
						Options: []forms.Option{{Label: "ed25519", Value: "ed25519"}},
					},
					{Name: "libp2p.listenAddrs", Title: "Listen Addresses", Type: forms.StringList, Placeholder: DefaultListenTCP + ", " + DefaultListenQUIC, Desc: "Listen addresses for the libp2p host."},
				},
			},
			{
				Title: "libp2p - Bootstrap And Discovery",
				Fields: []forms.Field{
					{Name: "libp2p.bootstrapPeers", Title: "Bootstrap Peers", Type: forms.StringList, Placeholder: "/dns4/bootstrap.example.com/tcp/4001/p2p/<peerid>", Desc: "Explicit rendezvous-capable bootstrap peers. When set, DNS TXT fallback is skipped."},
					{Name: "libp2p.discovery.bootstrapSeedTXT", Title: "Bootstrap Seed TXT", Type: forms.Text, Default: DefaultBootstrapSeedTXT, Desc: "TXT record name to query for bootstrap multiaddrs when Bootstrap Peers is empty."},
					{Name: "libp2p.discovery.rendezvousEnabled", Title: "Enable Rendezvous", Type: forms.Toggle, Default: true, Desc: "Enable GoClaw-hosted rendezvous registration and lookup."},
					{Name: "libp2p.discovery.rendezvousNamespace", Title: "Rendezvous Namespace", Type: forms.Text, Default: DefaultRendezvousNS, Desc: "Namespace used for GoClaw peer discovery."},
					{Name: "libp2p.discovery.registerIntervalSeconds", Title: "Register Interval (seconds)", Type: forms.Number, Default: 30, Desc: "How often nodes refresh their rendezvous registration."},
					{Name: "libp2p.discovery.queryIntervalSeconds", Title: "Query Interval (seconds)", Type: forms.Number, Default: 30, Desc: "How often nodes query rendezvous peers for candidates."},
					{Name: "libp2p.discovery.mdnsEnabled", Title: "Enable mDNS", Type: forms.Toggle, Default: false, Desc: "Enable LAN-only peer discovery."},
					{Name: "libp2p.discovery.serviceName", Title: "mDNS Service Name", Type: forms.Text, Default: DefaultRendezvousNS, Desc: "Service name used for LAN discovery."},
				},
			},
			{
				Title: "libp2p - Relay And Protocol",
				Fields: []forms.Field{
					{Name: "libp2p.relay.enableClient", Title: "Enable Relay Client", Type: forms.Toggle, Default: true, Desc: "Allow relayed connectivity when direct paths fail."},
					{Name: "libp2p.relay.enableServer", Title: "Enable Relay Server", Type: forms.Toggle, Default: false, Desc: "Offer relay service from this node."},
					{Name: "libp2p.relay.staticRelays", Title: "Static Relays", Type: forms.StringList, Placeholder: "/dns4/relay.example.com/tcp/4001/p2p/<peerid>", Desc: "Optional relay peers to reserve with explicitly."},
					{Name: "libp2p.protocol.rpcProtocolId", Title: "RPC Protocol ID", Type: forms.Text, Default: DefaultRPCProtocolID, Desc: "Protocol ID used for A2A traffic."},
					{Name: "libp2p.protocol.rendezvousProtocolId", Title: "Rendezvous Protocol ID", Type: forms.Text, Default: DefaultRendezvousID, Desc: "Protocol ID used for GoClaw rendezvous."},
					{Name: "libp2p.protocol.stateRetentionSeconds", Title: "Task State Retention (seconds)", Type: forms.Number, Default: DefaultRetentionSeconds, Desc: "How long completed task snapshots remain resumable."},
				},
			},
		},
		Actions: []forms.ActionDef{
			{Name: "apply", Label: "Apply"},
		},
	}
}

func RegisterCommands() {
	bus.RegisterCommand(configPath, "apply", handleApply)
}

func UnregisterCommands() {
	bus.UnregisterComponent(configPath)
}

func handleApply(cmd bus.Command) bus.CommandResult {
	var cfg Config
	switch v := cmd.Payload.(type) {
	case Config:
		cfg = v
	case *Config:
		if v != nil {
			cfg = *v
		}
	default:
		return bus.CommandResult{
			Error:   fmt.Errorf("expected a2a.Config payload, got %T", cmd.Payload),
			Message: "invalid A2A config payload",
		}
	}
	cfg.Normalize()
	L_info("a2a: config applied",
		"enabled", cfg.Enabled,
		"defaultTransport", cfg.DefaultTransport,
		"libp2pEnabled", cfg.Libp2p.Enabled,
		"bootstrapPeers", len(cfg.Libp2p.BootstrapPeers),
		"trustedPeers", len(cfg.Libp2p.TrustedPeers),
	)
	bus.PublishEvent(configPath+".config.applied", &cfg)
	return bus.CommandResult{
		Success: true,
		Message: "A2A configuration applied",
	}
}
