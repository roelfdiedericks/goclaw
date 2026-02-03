cat api.go
package httpserver

import (
        "encoding/binary"
        "encoding/json"
        "fmt"
        "net"
        "net/http"
        "sort"
        "strconv"
        "strings"
        "time"

        "github.com/roelfdiedericks/goppp/internal/auth"
        "github.com/roelfdiedericks/goppp/internal/config"
        "github.com/roelfdiedericks/goppp/internal/ippool"
        "github.com/roelfdiedericks/goppp/internal/ipv6"
        . "github.com/roelfdiedericks/goppp/internal/logging"
        "github.com/roelfdiedericks/goppp/internal/metrics"
        "github.com/roelfdiedericks/goppp/internal/protocols"
        "github.com/roelfdiedericks/goppp/internal/sessions"
        "github.com/roelfdiedericks/goppp/internal/vlan"
        "github.com/roelfdiedericks/goppp/internal/vpp"
)

// SessionInfo represents session information for API response
type SessionInfo struct {
        SessionID       uint16    `json:"session_id"`
        Transport       string    `json:"transport"`        // "PPPoE" or "L2TP"
        ClientMAC       string    `json:"client_mac"`
        ServerMAC       string    `json:"server_mac"`
        Username        string    `json:"username"`
        State           string    `json:"state"`
        Protocol        string    `json:"protocol"`
        ClientIPv4      string    `json:"client_ipv4,omitempty"`
        ServerIPv4      string    `json:"server_ipv4,omitempty"`
        ClientIID       string    `json:"client_iid,omitempty"`  // IPv6 Interface ID (link-local)
        ServerIID       string    `json:"server_iid,omitempty"`  // IPv6 Interface ID (link-local)
        ClientNA        string    `json:"client_na,omitempty"`   // IPv6 Non-temporary Address (DHCPv6)
        ClientPD        string    `json:"client_pd,omitempty"`   // IPv6 Delegated Prefix (DHCPv6)
        IPv6Mode        string    `json:"ipv6_mode,omitempty"`   // IPv6 allocation mode
        SLAACPrefix     string    `json:"slaac_prefix,omitempty"` // SLAAC /64 prefix
        RASent          uint32    `json:"ra_sent,omitempty"`     // Number of RAs sent
        RSReceived      uint32    `json:"rs_received,omitempty"` // Number of RSs received
        StartTime       time.Time `json:"start_time"`
        Duration        string    `json:"duration"`
        IPCPCompleteTime *time.Time `json:"ipcp_complete_time,omitempty"`  // When IPCP reached Completed state
        IPCPDuration    string    `json:"ipcp_duration,omitempty"`        // Duration since IPCP completed
        BytesIn         uint64    `json:"bytes_in"`
        BytesOut        uint64    `json:"bytes_out"`
        PacketsIn       uint64    `json:"packets_in"`
        PacketsOut      uint64    `json:"packets_out"`
        VPPInterface    uint32    `json:"vpp_interface"`
        NegotiatedMTU   uint16    `json:"negotiated_mtu"`     // MTU negotiated during LCP
        // Traffic rates
        UploadRate      float64   `json:"upload_rate_mbps"`   // Current upload rate in Mbps
        DownloadRate    float64   `json:"download_rate_mbps"` // Current download rate in Mbps
        LastUpdate      time.Time `json:"last_update"`        // When stats were last updated
        // Interface hierarchy
        Interface       string    `json:"interface"`              // Full interface name (e.g., "GigabitEthernet9/0/0.69")
        BasePhysical    string    `json:"base_physical"`          // Base physical interface
        OuterVLAN       uint16    `json:"outer_vlan,omitempty"`  // Outer VLAN (0 for untagged)
        InnerVLAN       uint16    `json:"inner_vlan,omitempty"`  // Inner VLAN for QinQ
        InterfaceType   string    `json:"interface_type"`         // "untagged", "vlan", "qinq"
        // Policer information
        PolicerEnabled  bool      `json:"policer_enabled"`
        PolicerType     string    `json:"policer_type,omitempty"`
        PolicerIngress  uint64    `json:"policer_ingress_rate,omitempty"`  // Ingress rate in bps
        PolicerEgress   uint64    `json:"policer_egress_rate,omitempty"`   // Egress rate in bps
        PolicerInfo     string    `json:"policer_info,omitempty"`          // Human-readable summary
        // LCP Echo metrics
        EchoTimeouts    uint32        `json:"echo_timeouts"`       // Total number of echo timeouts
        EchoRTTMin      time.Duration `json:"echo_rtt_min_ns"`     // Minimum RTT in nanoseconds
        EchoRTTMax      time.Duration `json:"echo_rtt_max_ns"`     // Maximum RTT in nanoseconds
        EchoRTTAvg      time.Duration `json:"echo_rtt_avg_ns"`     // Moving average RTT in nanoseconds
        EchoRTTLast     time.Duration `json:"echo_rtt_last_ns"`    // Last measured RTT in nanoseconds
        EchoRTTSamples  uint32        `json:"echo_rtt_samples"`    // Number of RTT samples
        LastEchoReply   *time.Time    `json:"last_echo_reply,omitempty"` // Timestamp of last echo reply
        // Bidirectional echo tracking
        EchoReqReceived  uint32        `json:"echo_req_received"`   // Echo requests received from client
        EchoReplysSent   uint32        `json:"echo_replies_sent"`   // Echo replies sent to client
        EchoReqSent      uint32        `json:"echo_req_sent"`       // Echo requests sent by server
        EchoReplyReceived uint32       `json:"echo_reply_received"` // Echo replies received from client
        LastEchoReqTime  *time.Time    `json:"last_echo_req_time,omitempty"` // Last echo request received
        EchoHealth       string        `json:"echo_health"`         // "healthy", "warning", "critical", "none"
        // Monitor TAP state
        MonitorActive     bool          `json:"monitor_active"`        // Is monitor currently active
        MonitorRemaining  int           `json:"monitor_remaining_seconds,omitempty"` // Remaining seconds for monitor
        MonitorEnabledBy  string        `json:"monitor_enabled_by,omitempty"` // Who enabled monitor
        MonitorTAPName    string        `json:"monitor_tap_name,omitempty"` // Linux TAP interface name
        // Relay information for LAC sessions
        RelayInfo         *RelayInfo    `json:"relay_info,omitempty"`       // LAC relay information
        // Transport-specific details
        TransportDetails  interface{}   `json:"transport_details,omitempty"` // Transport-specific information
}

// RelayInfo contains information about LAC relay sessions
type RelayInfo struct {
        Mode             string `json:"mode"`              // "LAC" or "LNS"
        LNSAddress       string `json:"lns_address"`       // Target LNS IP
        RelayReason      string `json:"relay_reason"`      // Why it was relayed (RADIUS/File)
        RelayCriteria    string `json:"relay_criteria"`    // The tunnel endpoint from RADIUS/File
        L2TPTunnelID     uint16 `json:"l2tp_tunnel_id"`    // L2TP tunnel ID
        L2TPSessionID    uint16 `json:"l2tp_session_id"`   // L2TP session ID
        RelayEstablished bool   `json:"relay_established"` // Is relay established
        ExtractedDNS1    string `json:"dns1,omitempty"`    // Primary DNS from relay
        ExtractedDNS2    string `json:"dns2,omitempty"`    // Secondary DNS from relay
}

// L2TPDetails contains L2TP-specific session information
type L2TPDetails struct {
        TunnelID         uint16 `json:"tunnel_id"`        // Remote tunnel ID
        SessionID        uint16 `json:"session_id"`       // Remote session ID
        LocalTunnelID    uint16 `json:"local_tunnel_id"`  // Our tunnel ID
        LocalSessionID   uint16 `json:"local_session_id"` // Our session ID
        PeerIP           string `json:"peer_ip"`          // Peer IP address
        PeerPort         uint16 `json:"peer_port"`        // Peer port
        LocalIP          string `json:"local_ip"`         // Local IP address
        LocalPort        uint16 `json:"local_port"`       // Local port
}

// PPPoEDetails contains PPPoE-specific session information
type PPPoEDetails struct {
        ClientMAC        string `json:"client_mac"`       // Client MAC address
        ServerMAC        string `json:"server_mac"`       // Server MAC address
        OuterVLAN        uint16 `json:"outer_vlan,omitempty"` // Outer VLAN (0 for untagged)
        InnerVLAN        uint16 `json:"inner_vlan,omitempty"` // Inner VLAN for QinQ
        InterfaceType    string `json:"interface_type"`   // "untagged", "vlan", "qinq"
}

// StatsInfo represents system statistics
type StatsInfo struct {
        ActiveSessions    int       `json:"active_sessions"`
        TotalSessions     uint64    `json:"total_sessions"`
        AuthSuccess       uint64    `json:"auth_success"`
        AuthFailures      uint64    `json:"auth_failures"`
        PADIsReceived     uint64    `json:"padis_received"`
        PADOsSent         uint64    `json:"pados_sent"`
        PADRsReceived     uint64    `json:"padrs_received"`
        PADSSent          uint64    `json:"pads_sent"`
        ServerStartTime   time.Time `json:"server_start_time"`
        Uptime            string    `json:"uptime"`
        PADOsInWild       int       `json:"pados_in_wild"`
        PADSInFlight      int       `json:"pads_in_flight"`
}

// formatRate converts bits per second to a human-readable format
func formatRate(bps uint64) string {
        if bps >= 1000000000 { // >= 1 Gbps
                return fmt.Sprintf("%.1fG", float64(bps)/1000000000)
        } else if bps >= 1000000 { // >= 1 Mbps
                return fmt.Sprintf("%.1fM", float64(bps)/1000000)
        } else if bps >= 1000 { // >= 1 Kbps
                return fmt.Sprintf("%.1fK", float64(bps)/1000)
        }
        return fmt.Sprintf("%d", bps)
}

// TerminateRequest represents a session termination request
type TerminateRequest struct {
        SessionID   *uint16 `json:"session_id,omitempty"`
        MACAddress  string  `json:"mac_address,omitempty"`
        Username    string  `json:"username,omitempty"`
        Reason      string  `json:"reason,omitempty"`
}

// TerminateResponse represents the response to a termination request
type TerminateResponse struct {
        Success         bool   `json:"success"`
        Message         string `json:"message"`
        SessionsAffected int   `json:"sessions_affected"`
}

// handleAPISessions returns session information as JSON
func (s *Server) handleAPISessions(w http.ResponseWriter, r *http.Request) {
        // Only allow GET requests
        if r.Method != http.MethodGet {
                http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
                return
        }

        // Get all active sessions from manager
        activeSessions := s.sessionMgr.GetActiveSessions()

        // Convert to API format
        apiSessions := make([]SessionInfo, 0, len(activeSessions))
        for _, sess := range activeSessions {
                // Calculate duration, handling zero StartTime
                var duration string
                if sess.StartTime.IsZero() {
                        duration = "N/A"
                } else {
                        duration = time.Since(sess.StartTime).Round(time.Second).String()
                }

                // Determine transport type
                transportType := "PPPoE"
                if sess.Key.Transport == 1 {
                        transportType = "L2TP"
                }

                // For relay sessions, use relay-extracted metadata
                isRelay := sess.State == sessions.SessionStateRelaying

                // Determine which values to use based on relay state
                mtu := sess.NegotiatedMTU
                username := sess.Username
                clientIPv4 := sess.ClientIPv4

                if isRelay {
                        // Use relay-extracted values for relay sessions
                        if sess.RelayNegotiatedMTU > 0 {
                                mtu = sess.RelayNegotiatedMTU
                        }
                        if sess.RelayAuthUsername != "" {
                                username = sess.RelayAuthUsername
                        }
                        if sess.RelayExtractedIP != nil && !sess.RelayExtractedIP.IsUnspecified() {
                                clientIPv4 = sess.RelayExtractedIP
                        }
                }

                info := SessionInfo{
                        SessionID:    sess.Key.SessionId,
                        Transport:    transportType,
                        ClientMAC:    fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x",
                                sess.Key.ClientMAC[0], sess.Key.ClientMAC[1], sess.Key.ClientMAC[2],
                                sess.Key.ClientMAC[3], sess.Key.ClientMAC[4], sess.Key.ClientMAC[5]),
                        ServerMAC:    fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x",
                                sess.ServerMAC[0], sess.ServerMAC[1], sess.ServerMAC[2],
                                sess.ServerMAC[3], sess.ServerMAC[4], sess.ServerMAC[5]),
                        Username:     username,
                        State:        sess.GetStateString(),
                        StartTime:    sess.StartTime,
                        Duration:     duration,
                        VPPInterface: sess.VPPIfIndex,
                        NegotiatedMTU: mtu,
                        BytesIn:      sess.BytesIn.Load(),
                        BytesOut:     sess.BytesOut.Load(),
                        PacketsIn:    sess.PacketsIn.Load(),
                        PacketsOut:   sess.PacketsOut.Load(),
                        // LCP Echo metrics
                        EchoTimeouts:   sess.Stats.EchoTimeouts,
                        EchoRTTMin:     sess.Stats.EchoRTTMin,
                        EchoRTTMax:     sess.Stats.EchoRTTMax,
                        EchoRTTAvg:     sess.Stats.EchoRTTAvg,
                        EchoRTTLast:    sess.Stats.EchoRTTLast,
                        EchoRTTSamples: sess.Stats.EchoRTTSamples,
                        // Bidirectional echo tracking
                        EchoReqReceived:   sess.Stats.EchoReqReceived,
                        EchoReplysSent:    sess.Stats.EchoReplysSent,
                        EchoReqSent:       sess.Stats.EchoReqSent,
                        EchoReplyReceived: sess.Stats.EchoReplyReceived,
                        // Interface hierarchy
                        Interface:     sess.InterfaceName,
                        BasePhysical:  sess.PhysicalInterface,
                        OuterVLAN:     sess.Key.OuterVLAN,
                        InnerVLAN:     sess.Key.InnerVLAN,
                        InterfaceType: sess.Key.GetVLANType(),
                }

                // Add relay information for VPDN sessions
                if isRelay {
                        relayReason := "VPDN"  // Post-auth VPDN relay
                        if sess.RelayMatchReason != "" {
                                relayReason = sess.RelayMatchReason + " match"
                        }

                        lnsAddr := ""
                        if sess.LNSAddress != nil {
                                lnsAddr = sess.LNSAddress.String()
                        }

                        relayInfo := &RelayInfo{
                                Mode:             "LAC",
                                LNSAddress:       lnsAddr,
                                RelayReason:      relayReason,
                                RelayCriteria:    sess.RelayMatchValue,
                                L2TPTunnelID:     uint16(sess.L2tpLocalTunnelID),
                                L2TPSessionID:    uint16(sess.L2tpLocalSessionID),
                                RelayEstablished: sess.RelayIPCPState == "opened",
                        }

                        // Add DNS servers if extracted
                        if sess.RelayExtractedDNS1 != nil && !sess.RelayExtractedDNS1.IsUnspecified() {
                                relayInfo.ExtractedDNS1 = sess.RelayExtractedDNS1.String()
                        }
                        if sess.RelayExtractedDNS2 != nil && !sess.RelayExtractedDNS2.IsUnspecified() {
                                relayInfo.ExtractedDNS2 = sess.RelayExtractedDNS2.String()
                        }

                        info.RelayInfo = relayInfo
                }

                // Add transport-specific details
                if sess.Key.Transport == 1 { // L2TP
                        // Get L2TP-specific information from the transport
                        if sess.Transport != nil {
                                // Try to get L2TP-specific info
                                if l2tpTransport, ok := sess.Transport.(interface{
                                        GetLocalSessionID() uint16
                                        GetPeerAddress() string
                                        GetLocalAddress() string
                                }); ok {
                                        // Parse peer address (format: "IP:port")
                                        peerAddr := l2tpTransport.GetPeerAddress()
                                        localAddr := l2tpTransport.GetLocalAddress()

                                        // Extract IP and port from peer address
                                        peerIP := peerAddr
                                        peerPort := uint16(1701) // default L2TP port
                                        if idx := strings.LastIndex(peerAddr, ":"); idx != -1 {
                                                peerIP = peerAddr[:idx]
                                                if port, err := strconv.Atoi(peerAddr[idx+1:]); err == nil {
                                                        peerPort = uint16(port)
                                                }
                                        }

                                        // Extract IP and port from local address
                                        localIP := localAddr
                                        localPort := uint16(1701) // default L2TP port
                                        if idx := strings.LastIndex(localAddr, ":"); idx != -1 {
                                                localIP = localAddr[:idx]
                                                if port, err := strconv.Atoi(localAddr[idx+1:]); err == nil {
                                                        localPort = uint16(port)
                                                }
                                        }

                                        info.TransportDetails = L2TPDetails{
                                                TunnelID:       sess.Key.SessionId,     // Remote session ID
                                                SessionID:      sess.Key.SessionId,      // Remote session ID
                                                LocalTunnelID:  sess.Key.TunnelID,       // Our tunnel ID
                                                LocalSessionID: l2tpTransport.GetLocalSessionID(),
                                                PeerIP:         peerIP,
                                                PeerPort:       peerPort,
                                                LocalIP:        localIP,
                                                LocalPort:      localPort,
                                        }

                                        // Override interface field for L2TP to show peer IP
                                        info.Interface = fmt.Sprintf("l2tp-%s", peerIP)
                                }
                        }
                } else { // PPPoE
                        info.TransportDetails = PPPoEDetails{
                                ClientMAC:     info.ClientMAC,
                                ServerMAC:     info.ServerMAC,
                                OuterVLAN:     sess.Key.OuterVLAN,
                                InnerVLAN:     sess.Key.InnerVLAN,
                                InterfaceType: sess.Key.GetVLANType(),
                        }
                }

                // Add LastEchoReply if available
                if !sess.Stats.LastEchoReply.IsZero() {
                        info.LastEchoReply = &sess.Stats.LastEchoReply
                }

                // Add LastEchoReqTime if available
                if !sess.Stats.LastEchoReqTime.IsZero() {
                        info.LastEchoReqTime = &sess.Stats.LastEchoReqTime
                }

                // Calculate echo health status
                if sess.Stats.EchoReqReceived > 0 || sess.Stats.EchoReqSent > 0 {
                        timeSinceLastEcho := time.Duration(0)
                        if !sess.Stats.LastEchoReqTime.IsZero() {
                                timeSinceLastEcho = time.Since(sess.Stats.LastEchoReqTime)
                        }

                        // Calculate loss percentages
                        var serverToClientLoss float64
                        if sess.Stats.EchoReqSent > 0 {
                                serverToClientLoss = float64(sess.Stats.EchoReqSent-sess.Stats.EchoReplyReceived) / float64(sess.Stats.EchoReqSent) * 100
                        }

                        // Determine health status
                        if timeSinceLastEcho > 90*time.Second || serverToClientLoss > 50 {
                                info.EchoHealth = "critical"
                        } else if timeSinceLastEcho > 45*time.Second || serverToClientLoss > 5 {
                                info.EchoHealth = "warning"
                        } else {
                                info.EchoHealth = "healthy"
                        }
                } else {
                        info.EchoHealth = "none"
                }

                // Add monitor TAP state
                info.MonitorActive = sess.IsMonitorActive()
                if info.MonitorActive {
                        info.MonitorRemaining = sess.GetMonitorRemainingSeconds()
                        info.MonitorEnabledBy = sess.MonitorEnabledBy
                        info.MonitorTAPName = sess.MonitorTAPName
                }

                // Calculate traffic rates
                lastUpdateUnix := sess.LastAccountingTime.Load()
                if lastUpdateUnix > 0 {
                        info.LastUpdate = time.Unix(lastUpdateUnix, 0)

                        // Try to calculate instantaneous rate first (based on delta since last update)
                        prevUpdateUnix := sess.PrevUpdateTime.Load()
                        if prevUpdateUnix > 0 && lastUpdateUnix > prevUpdateUnix {
                                // We have previous values, calculate instantaneous rate
                                prevBytesIn := sess.PrevBytesIn.Load()
                                prevBytesOut := sess.PrevBytesOut.Load()
                                timeDelta := float64(lastUpdateUnix - prevUpdateUnix)

                                // Only use delta calculation if we have at least 1 second delta and valid byte counts
                                if timeDelta >= 1.0 && info.BytesIn >= prevBytesIn && info.BytesOut >= prevBytesOut {
                                        // Calculate rate based on delta (with overflow protection)
                                        bytesInDelta := info.BytesIn - prevBytesIn
                                        bytesOutDelta := info.BytesOut - prevBytesOut

                                        // Convert to Mbps (bytes * 8 / seconds / 1_000_000)
                                        info.UploadRate = float64(bytesInDelta) * 8 / timeDelta / 1_000_000
                                        info.DownloadRate = float64(bytesOutDelta) * 8 / timeDelta / 1_000_000
                                } else {
                                        // Fall back to average rate if delta is too small or invalid
                                        sessionDuration := time.Since(sess.StartTime).Seconds()
                                        if sessionDuration > 0 {
                                                info.UploadRate = float64(info.BytesIn) * 8 / sessionDuration / 1_000_000
                                                info.DownloadRate = float64(info.BytesOut) * 8 / sessionDuration / 1_000_000
                                        }
                                }
                        } else {
                                // No valid previous values, use average rate over session lifetime
                                sessionDuration := time.Since(sess.StartTime).Seconds()
                                if sessionDuration > 0 {
                                        // Calculate average rates over session lifetime
                                        // Note: BytesIn = RX from client perspective = Upload
                                        // Note: BytesOut = TX from server perspective = Download
                                        info.UploadRate = float64(info.BytesIn) * 8 / sessionDuration / 1_000_000    // Convert to Mbps
                                        info.DownloadRate = float64(info.BytesOut) * 8 / sessionDuration / 1_000_000 // Convert to Mbps
                                }
                        }
                }

                // Add IPCP completion time if available
                if !sess.IPCPCompleteTime.IsZero() {
                        info.IPCPCompleteTime = &sess.IPCPCompleteTime
                        info.IPCPDuration = time.Since(sess.IPCPCompleteTime).Round(time.Second).String()
                }

                // Get the current protocol state from the session
                // This uses the centralized state logic in GetStateString()
                info.Protocol = sess.GetStateString()

                // TODO: Handle VPDN relay sessions
                if false { // sess.IsRelaySession removed
                        // Override with relay-specific state
                        if sess.RelayIPCPState == "opened" {
                                info.Protocol = "IPCP"  // Show as established via IPCP
                        } else if sess.RelayIPCPState == "negotiating" {
                                info.Protocol = "Relaying (IPCP negotiating)"
                        } else {
                                info.Protocol = "Relaying"
                        }
                }

                // Add IPv4 addresses
                // Use the determined client IP (either local or relay-extracted)
                if clientIPv4 != nil && !clientIPv4.IsUnspecified() {
                        info.ClientIPv4 = clientIPv4.String()
                }

                // Server IPv4 can be derived from IPCP config if available
                if sess.IPCPConfig != nil {
                        if ipcpCfg, ok := sess.IPCPConfig.(*protocols.IPCPConfig); ok && ipcpCfg != nil {
                                if ipcpCfg.LocalIP != 0 {
                                        ip := make(net.IP, 4)
                                        binary.BigEndian.PutUint32(ip, ipcpCfg.LocalIP)
                                        info.ServerIPv4 = ip.String()
                                }
                        }
                }

                // Add IPv6 Interface IDs
                if isRelay {
                        // For relay sessions, use relay-extracted IIDs
                        var zeroIID [8]byte
                        if sess.RelayClientIID != zeroIID {
                                // Construct link-local address fe80::IID
                                clientIID := net.IP{0xfe, 0x80, 0, 0, 0, 0, 0, 0,
                                        sess.RelayClientIID[0], sess.RelayClientIID[1], sess.RelayClientIID[2], sess.RelayClientIID[3],
                                        sess.RelayClientIID[4], sess.RelayClientIID[5], sess.RelayClientIID[6], sess.RelayClientIID[7]}
                                info.ClientIID = clientIID.String()
                        }
                        if sess.RelayServerIID != zeroIID {
                                // Construct link-local address fe80::IID
                                serverIID := net.IP{0xfe, 0x80, 0, 0, 0, 0, 0, 0,
                                        sess.RelayServerIID[0], sess.RelayServerIID[1], sess.RelayServerIID[2], sess.RelayServerIID[3],
                                        sess.RelayServerIID[4], sess.RelayServerIID[5], sess.RelayServerIID[6], sess.RelayServerIID[7]}
                                info.ServerIID = serverIID.String()
                        }
                } else if sess.IPV6CPConfig != nil {
                        // For local termination, use IPv6CP config
                        if ipv6cpCfg, ok := sess.IPV6CPConfig.(*protocols.IPV6CPConfig); ok && ipv6cpCfg != nil {
                                // Client Interface ID
                                if ipv6cpCfg.RemoteIID != 0 {
                                        // Convert uint64 IID to bytes
                                        iidBytes := make([]byte, 8)
                                        binary.BigEndian.PutUint64(iidBytes, ipv6cpCfg.RemoteIID)

                                        // Construct link-local address fe80::IID
                                        clientIID := net.IP{0xfe, 0x80, 0, 0, 0, 0, 0, 0,
                                                iidBytes[0], iidBytes[1], iidBytes[2], iidBytes[3],
                                                iidBytes[4], iidBytes[5], iidBytes[6], iidBytes[7]}
                                        info.ClientIID = clientIID.String()
                                }

                                // Server Interface ID
                                if ipv6cpCfg.LocalIID != 0 {
                                        // Convert uint64 IID to bytes
                                        iidBytes := make([]byte, 8)
                                        binary.BigEndian.PutUint64(iidBytes, ipv6cpCfg.LocalIID)

                                        serverIID := net.IP{0xfe, 0x80, 0, 0, 0, 0, 0, 0,
                                                iidBytes[0], iidBytes[1], iidBytes[2], iidBytes[3],
                                                iidBytes[4], iidBytes[5], iidBytes[6], iidBytes[7]}
                                        info.ServerIID = serverIID.String()
                                }
                        }
                }

                // Add DHCPv6 info if available
                if sess.DHCPv6FSM != nil {
                        if dhcpv6FSM, ok := sess.DHCPv6FSM.(*protocols.DHCPv6FSM); ok && dhcpv6FSM != nil && dhcpv6FSM.PDConfig != nil {
                                // Non-temporary address (IA_NA)
                                if dhcpv6FSM.PDConfig.DelegatedAddress != nil && len(dhcpv6FSM.PDConfig.DelegatedAddress) > 0 {
                                        info.ClientNA = dhcpv6FSM.PDConfig.DelegatedAddress.String()
                                }

                                // Delegated Prefix (IA_PD)
                                if dhcpv6FSM.PDConfig.DelegatedPrefix != nil {
                                        info.ClientPD = dhcpv6FSM.PDConfig.DelegatedPrefix.String()
                                }
                        }

                        // SLAAC information
                        if sess.IPv6Mode != "" {
                                info.IPv6Mode = sess.IPv6Mode
                        }
                        if sess.SLAACPrefix != nil {
                                info.SLAACPrefix = sess.SLAACPrefix.String()
                        }
                        if sess.RACount > 0 {
                                info.RASent = sess.RACount
                        }
                        if sess.RSCount > 0 {
                                info.RSReceived = sess.RSCount
                        }
                }

                // Add policer information if available
                if userAttrs, ok := sess.UserAttributes.(*auth.UserAttributes); ok && userAttrs != nil && userAttrs.Policer.Enabled {
                        policer := userAttrs.Policer
                        info.PolicerEnabled = true
                        info.PolicerType = policer.Type

                        // Get rates - use asymmetric rates directly
                        uploadRate := policer.UploadCIR
                        downloadRate := policer.DownloadCIR

                        info.PolicerIngress = uploadRate    // Keep field names for backward compatibility
                        info.PolicerEgress = downloadRate

                        // Create human-readable summary
                        // Download (↓), Upload (↑)
                        if uploadRate > 0 && downloadRate > 0 {
                                if uploadRate == downloadRate {
                                        info.PolicerInfo = fmt.Sprintf("%s: %s symmetric",
                                                info.PolicerType, formatRate(uploadRate))
                                } else {
                                        // Show as download↓/upload↑
                                        info.PolicerInfo = fmt.Sprintf("%s: %s↓/%s↑",
                                                info.PolicerType, formatRate(downloadRate), formatRate(uploadRate))
                                }
                        } else if uploadRate > 0 {
                                // Upload only
                                info.PolicerInfo = fmt.Sprintf("%s: %s upload",
                                        info.PolicerType, formatRate(uploadRate))
                        } else if downloadRate > 0 {
                                // Download only
                                info.PolicerInfo = fmt.Sprintf("%s: %s download",
                                        info.PolicerType, formatRate(downloadRate))
                        } else {
                                info.PolicerInfo = fmt.Sprintf("%s: enabled", info.PolicerType)
                        }
                }

                apiSessions = append(apiSessions, info)
        }

        // Set content type and encode response
        w.Header().Set("Content-Type", "application/json")
        w.Header().Set("Access-Control-Allow-Origin", "*") // CORS for development

        if err := json.NewEncoder(w).Encode(apiSessions); err != nil {
                L_error("Failed to encode sessions JSON: %v", err)
                http.Error(w, "Internal server error", http.StatusInternalServerError)
                return
        }
}

// handleAPIStats returns system statistics as JSON
func (s *Server) handleAPIStats(w http.ResponseWriter, r *http.Request) {
        // Only allow GET requests
        if r.Method != http.MethodGet {
                http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
                return
        }

        // Gather statistics
        stats := StatsInfo{
                ActiveSessions:   s.sessionMgr.GetActiveSessionCount(),
                TotalSessions:    s.sessionMgr.GetTotalSessionCount(),
                AuthSuccess:      s.sessionMgr.GetAuthSuccessCount(),
                AuthFailures:     s.sessionMgr.GetAuthFailureCount(),
                PADIsReceived:    s.sessionMgr.GetPADICount(),
                PADOsSent:        s.sessionMgr.GetPADOCount(),
                PADRsReceived:    s.sessionMgr.GetPADRCount(),
                PADSSent:         s.sessionMgr.GetPADSCount(),
                ServerStartTime:  s.sessionMgr.GetStartTime(),
                Uptime:           time.Since(s.sessionMgr.GetStartTime()).Round(time.Second).String(),
                PADOsInWild:      s.sessionMgr.GetPADOsInWild(),
                PADSInFlight:     s.sessionMgr.GetPADSInFlight(),
        }

        // Set content type and encode response
        w.Header().Set("Content-Type", "application/json")
        w.Header().Set("Access-Control-Allow-Origin", "*") // CORS for development

        if err := json.NewEncoder(w).Encode(stats); err != nil {
                L_error("Failed to encode stats JSON: %v", err)
                http.Error(w, "Internal server error", http.StatusInternalServerError)
                return
        }
}

// handleAPITerminate handles session termination requests
func (s *Server) handleAPITerminate(w http.ResponseWriter, r *http.Request) {
        var req TerminateRequest

        // Parse request based on method
        switch r.Method {
        case http.MethodGet:
                // Parse query parameters
                query := r.URL.Query()

                // Parse session_id if provided
                if sidStr := query.Get("session_id"); sidStr != "" {
                        var sid uint16
                        if _, err := fmt.Sscanf(sidStr, "%d", &sid); err == nil {
                                req.SessionID = &sid
                        } else {
                                http.Error(w, "Invalid session_id format", http.StatusBadRequest)
                                return
                        }
                }

                req.MACAddress = query.Get("mac_address")
                req.Username = query.Get("username")
                req.Reason = query.Get("reason")

        case http.MethodPost:
                // Parse JSON body
                if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
                        http.Error(w, "Invalid JSON request body", http.StatusBadRequest)
                        return
                }
                defer r.Body.Close()

        default:
                http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
                return
        }

        // Validate that at least one criteria is specified
        if req.SessionID == nil && req.MACAddress == "" && req.Username == "" {
                resp := TerminateResponse{
                        Success: false,
                        Message: "At least one of session_id, mac_address, or username must be specified",
                }
                w.Header().Set("Content-Type", "application/json")
                json.NewEncoder(w).Encode(resp)
                return
        }

        // Set default reason if not provided
        if req.Reason == "" {
                req.Reason = "API termination request"
        }

        // Track sessions to terminate
        sessionsTerminated := 0
        var errorMessages []string

        // Terminate by session ID
        if req.SessionID != nil {
                if key, found := s.sessionMgr.FindSessionKeyByID(*req.SessionID); found {
                        if err := s.sessionMgr.TerminateSession(key, req.Reason, 3*time.Second); err != nil {
                                errorMessages = append(errorMessages, fmt.Sprintf("Failed to terminate session %d: %v", *req.SessionID, err))
                        } else {
                                sessionsTerminated++
                        }
                } else {
                        errorMessages = append(errorMessages, fmt.Sprintf("Session ID %d not found", *req.SessionID))
                }
        }

        // Terminate by MAC address
        if req.MACAddress != "" {
                keys := s.sessionMgr.FindSessionKeysByMAC(req.MACAddress)
                if len(keys) == 0 {
                        if req.SessionID == nil { // Only add error if this was the only criteria
                                errorMessages = append(errorMessages, fmt.Sprintf("No sessions found for MAC %s", req.MACAddress))
                        }
                } else {
                        for _, key := range keys {
                                if err := s.sessionMgr.TerminateSession(key, req.Reason, 3*time.Second); err != nil {
                                        errorMessages = append(errorMessages, fmt.Sprintf("Failed to terminate session %d: %v", key.SessionId, err))
                                } else {
                                        sessionsTerminated++
                                }
                        }
                }
        }

        // Terminate by username
        if req.Username != "" {
                keys := s.sessionMgr.FindSessionKeysByUsername(req.Username)
                if len(keys) == 0 {
                        if req.SessionID == nil && req.MACAddress == "" { // Only add error if this was the only criteria
                                errorMessages = append(errorMessages, fmt.Sprintf("No sessions found for username %s", req.Username))
                        }
                } else {
                        for _, key := range keys {
                                if err := s.sessionMgr.TerminateSession(key, req.Reason, 3*time.Second); err != nil {
                                        errorMessages = append(errorMessages, fmt.Sprintf("Failed to terminate session %d: %v", key.SessionId, err))
                                } else {
                                        sessionsTerminated++
                                }
                        }
                }
        }

        // Prepare response
        var resp TerminateResponse
        if sessionsTerminated > 0 {
                resp.Success = true
                resp.Message = fmt.Sprintf("Successfully terminated %d session(s)", sessionsTerminated)
                resp.SessionsAffected = sessionsTerminated
        } else if len(errorMessages) > 0 {
                resp.Success = false
                resp.Message = errorMessages[0] // Return first error
                resp.SessionsAffected = 0
        } else {
                resp.Success = false
                resp.Message = "No sessions found matching the criteria"
                resp.SessionsAffected = 0
        }

        // Log the action
        L_info("API termination request: criteria=%+v, result=%+v", req, resp)

        // Send response
        w.Header().Set("Content-Type", "application/json")
        if err := json.NewEncoder(w).Encode(resp); err != nil {
                L_error("Failed to encode terminate response: %v", err)
                http.Error(w, "Internal server error", http.StatusInternalServerError)
        }
}

// PoolInfo represents pool statistics
type PoolInfo struct {
        Name              string                 `json:"name"`
        Type              string                 `json:"type"` // "ipv4", "ipv6_na", "ipv6_pd"
        BasePrefix        string                 `json:"base_prefix,omitempty"`
        TotalCapacity     int64                  `json:"total_capacity"`
        Allocated         int64                  `json:"allocated"`
        Available         int64                  `json:"available"`
        UsagePercent      float64                `json:"usage_percent"`
        Allocations       []PoolAllocation       `json:"allocations,omitempty"`
        PoolConfig        map[string]interface{} `json:"config,omitempty"`
}

// PoolAllocation represents a single allocation from a pool
type PoolAllocation struct {
        Address    string    `json:"address,omitempty"`
        Prefix     string    `json:"prefix,omitempty"`
        Username   string    `json:"username"`
        SessionID  uint16    `json:"session_id"`
        AllocatedAt time.Time `json:"allocated_at"`
        ExpiresAt  time.Time `json:"expires_at,omitempty"`
        Source     string    `json:"source"` // "pool" or "static"
}

// handleAPIPoolsIPv4 returns IPv4 pool statistics
func (s *Server) handleAPIPoolsIPv4(w http.ResponseWriter, r *http.Request) {
        // Check for brief parameter
        brief := r.URL.Query().Get("brief") == "1"

        // Get all IPv4 pools
        pools := ippool.GetAllIPv4Pools()

        var poolInfos []PoolInfo

        for name, pool := range pools {
                total, allocated, available, usagePercent := pool.GetStats()

                info := PoolInfo{
                        Name:          name,
                        Type:          "ipv4",
                        BasePrefix:    pool.Prefix.String(),
                        TotalCapacity: int64(total),
                        Allocated:     int64(allocated),
                        Available:     int64(available),
                        UsagePercent:  usagePercent,
                }

                // Add allocations only if not brief mode
                if !brief {
                        allocations := pool.GetAllocations()
                        // Note: IPv4 allocations are stored differently than IPv6
                        // The key is the session ID and value is the IP
                        for sessionID, ip := range allocations {
                                info.Allocations = append(info.Allocations, PoolAllocation{
                                        Address:   ip.String(),
                                        Username:  sessionID, // This is actually session ID or username
                                        Source:    "pool",
                                })
                        }
                }

                poolInfos = append(poolInfos, info)
        }

        // If no pools, return empty array rather than nil
        if poolInfos == nil {
                poolInfos = []PoolInfo{}
        }

        // Send response
        w.Header().Set("Content-Type", "application/json")
        w.Header().Set("Access-Control-Allow-Origin", "*")
        if err := json.NewEncoder(w).Encode(poolInfos); err != nil {
                L_error("Failed to encode IPv4 pool info: %v", err)
                http.Error(w, "Internal server error", http.StatusInternalServerError)
        }
}

// handleAPIPoolsIPv6NA returns IPv6 NA (address) pool statistics
func (s *Server) handleAPIPoolsIPv6NA(w http.ResponseWriter, r *http.Request) {
        // Check for brief parameter
        brief := r.URL.Query().Get("brief") == "1"

        // Get IPv6 pool manager
        poolMgr := ippool.GetIPv6PoolManager()
        if poolMgr == nil {
                http.Error(w, "IPv6 pool manager not initialized", http.StatusServiceUnavailable)
                return
        }

        // Get all pools
        pools := poolMgr.GetAllPools()

        var poolInfos []PoolInfo

        for name, pool := range pools {
                stats := pool.GetStats()

                info := PoolInfo{
                        Name:          name,
                        Type:          "ipv6_na",
                        BasePrefix:    stats.BasePrefix,
                        TotalCapacity: stats.TotalAddresses,
                        Allocated:     stats.AllocatedAddresses,
                        Available:     stats.AvailableAddresses,
                        UsagePercent:  stats.UsagePercent,
                }

                // Add allocations only if not brief mode
                if !brief {
                        allocations := pool.GetAllocations()
                        for _, alloc := range allocations {
                                info.Allocations = append(info.Allocations, PoolAllocation{
                                        Address:     alloc.Address.String(),
                                        Username:    alloc.Username,
                                        SessionID:   alloc.SessionID,
                                        AllocatedAt: alloc.AllocatedAt,
                                        Source:      alloc.Source,
                                })
                        }
                }

                poolInfos = append(poolInfos, info)
        }

        // If no pools, return empty array rather than nil
        if poolInfos == nil {
                poolInfos = []PoolInfo{}
        }

        // Send response
        w.Header().Set("Content-Type", "application/json")
        w.Header().Set("Access-Control-Allow-Origin", "*")
        if err := json.NewEncoder(w).Encode(poolInfos); err != nil {
                L_error("Failed to encode IPv6 NA pool info: %v", err)
                http.Error(w, "Internal server error", http.StatusInternalServerError)
        }
}

// handleAPIPoolsIPv6PD returns IPv6 PD (prefix delegation) pool statistics
func (s *Server) handleAPIPoolsIPv6PD(w http.ResponseWriter, r *http.Request) {
        // Check for brief parameter
        brief := r.URL.Query().Get("brief") == "1"

        // Get PD pool manager
        poolMgr := ippool.GetPrefixPoolManager()
        if poolMgr == nil {
                http.Error(w, "IPv6 PD pool manager not initialized", http.StatusServiceUnavailable)
                return
        }

        // Get all pools
        pools := poolMgr.GetAllPools()

        var poolInfos []PoolInfo

        for name, pool := range pools {
                stats := pool.GetStats()

                info := PoolInfo{
                        Name:             name,
                        Type:             "ipv6_pd",
                        BasePrefix:       stats.BasePrefix,
                        TotalCapacity:    stats.TotalPrefixes,
                        Allocated:        stats.AllocatedPrefixes,
                        Available:        stats.AvailablePrefixes,
                        UsagePercent:     stats.UsagePercent,
                        PoolConfig: map[string]interface{}{
                                "prefix_size": stats.PrefixSize,
                        },
                }

                // Add allocations only if not brief mode
                if !brief {
                        allocations := pool.GetAllocations()
                        for _, alloc := range allocations {
                                info.Allocations = append(info.Allocations, PoolAllocation{
                                        Prefix:      alloc.Prefix.String(),
                                        Username:    alloc.Username,
                                        SessionID:   alloc.SessionID,
                                        AllocatedAt: alloc.AllocatedAt,
                                        ExpiresAt:   alloc.ExpiresAt,
                                        Source:      "pool",
                                })
                        }
                }

                poolInfos = append(poolInfos, info)
        }

        // If no pools, return empty array rather than nil
        if poolInfos == nil {
                poolInfos = []PoolInfo{}
        }

        // Send response
        w.Header().Set("Content-Type", "application/json")
        w.Header().Set("Access-Control-Allow-Origin", "*")
        if err := json.NewEncoder(w).Encode(poolInfos); err != nil {
                L_error("Failed to encode IPv6 PD pool info: %v", err)
                http.Error(w, "Internal server error", http.StatusInternalServerError)
        }
}

// handleAPIPhysicalInterfaces returns VPP statistics for physical interfaces only
// This shows ALL traffic (not just PPPoE) directly from VPP
func (s *Server) handleAPIPhysicalInterfaces(w http.ResponseWriter, r *http.Request) {
        // Get list of physical interfaces from config
        physicalInterfaces := []string{}
        physicalInterfacesSeen := make(map[string]bool)

        for _, ic := range config.Get().InterfaceConfigs {
                // Physical interfaces don't contain dots
                if !strings.Contains(ic.Name, ".") && !physicalInterfacesSeen[ic.Name] {
                        physicalInterfaces = append(physicalInterfaces, ic.Name)
                        physicalInterfacesSeen[ic.Name] = true
                }
        }

        // Also ensure all interfaces from interface_roles are included
        // interface_roles only contains physical interfaces
        if config.Get().InterfaceRoles.PPPoEInterfaces != nil {
                for _, iface := range config.Get().InterfaceRoles.PPPoEInterfaces {
                        if !physicalInterfacesSeen[iface] {
                                physicalInterfaces = append(physicalInterfaces, iface)
                                physicalInterfacesSeen[iface] = true
                        }
                }
        }
        if config.Get().InterfaceRoles.IPUplinkInterfaces != nil {
                for _, iface := range config.Get().InterfaceRoles.IPUplinkInterfaces {
                        if !physicalInterfacesSeen[iface] {
                                physicalInterfaces = append(physicalInterfaces, iface)
                                physicalInterfacesSeen[iface] = true
                        }
                }
        }

        // Get VPP stats for physical interfaces
        var stats []*vpp.PPPoEInterfaceStats
        var err error

        if len(physicalInterfaces) > 0 {
                stats, err = vpp.Get().GetPPPoEInterfaceStats(physicalInterfaces)
                if err != nil {
                        L_error("Failed to get physical interface stats from VPP: %v", err)
                        // Return empty array on error
                        stats = []*vpp.PPPoEInterfaceStats{}
                }
        }

        // Get TX drops from performance monitor if available
        var interfaceTXDrops map[string]uint64
        if vpp.Get().GetPerformanceMonitor() != nil {
                perfData := vpp.Get().GetPerformanceMonitor().GetHealthSummary()
                if txDropsMap, ok := perfData["interface_tx_drops"].(map[string]uint64); ok {
                        interfaceTXDrops = txDropsMap
                }
        }

        // Mark these as physical interfaces and add role information
        for _, stat := range stats {
                stat.InterfaceType = "physical"

                // Add TX drops if available
                if interfaceTXDrops != nil {
                        if txDrops, exists := interfaceTXDrops[stat.InterfaceName]; exists {
                                stat.TxDrops = txDrops
                        }
                }

                // Add interface role information
                // Check if it's a PPPoE interface
                for _, pppoeIface := range config.Get().InterfaceRoles.PPPoEInterfaces {
                        // Direct comparison - physical interfaces only
                        if stat.InterfaceName == pppoeIface {
                                stat.InterfaceRole = "pppoe"
                                break
                        }
                }

                // Check if it's an IP uplink interface
                if stat.InterfaceRole == "" {
                        for _, uplinkIface := range config.Get().InterfaceRoles.IPUplinkInterfaces {
                                // Direct comparison - physical interfaces only
                                if stat.InterfaceName == uplinkIface {
                                        stat.InterfaceRole = "ip_uplink"
                                        break
                                }
                        }
                }
        }

        // Encode and send response
        w.Header().Set("Content-Type", "application/json")
        if err := json.NewEncoder(w).Encode(stats); err != nil {
                L_error("Failed to encode physical interface stats: %v", err)
                http.Error(w, "Internal server error", http.StatusInternalServerError)
        }
}

// handleAPIVPPPerformance returns VPP performance metrics
func (s *Server) handleAPIVPPPerformance(w http.ResponseWriter, r *http.Request) {
        // Only allow GET requests
        if r.Method != http.MethodGet {
                http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
                return
        }

        // Get performance metrics from VPP API
        var performanceData map[string]interface{}

        if vpp.Get().GetPerformanceMonitor() != nil {
                performanceData = vpp.Get().GetPerformanceMonitor().GetHealthSummary()
        } else {
                // Return empty/placeholder data if performance monitoring not available
                performanceData = map[string]interface{}{
                        "enabled":          false,
                        "message":          "VPP performance monitoring not available",
                        "timestamp":        time.Now().Unix(),
                        "overload_status":  "unknown",
                        "worker_balance":   0.0,
                        "rx_drops":         0,
                        "buffer_usage":     0.0,
                        "recommendations":  []string{"Performance monitoring not initialized"},
                }
        }

        // Set JSON content type
        w.Header().Set("Content-Type", "application/json")
        w.Header().Set("Access-Control-Allow-Origin", "*")

        // Encode and send response
        if err := json.NewEncoder(w).Encode(performanceData); err != nil {
                L_error("Failed to encode VPP performance data: %v", err)
                http.Error(w, "Internal server error", http.StatusInternalServerError)
        }
}

// handleAPIAdvisor returns VPP performance advisories
func (s *Server) handleAPIAdvisor(w http.ResponseWriter, r *http.Request) {
        // Only allow GET requests
        if r.Method != http.MethodGet {
                http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
                return
        }

        // Check if detailed analysis is requested
        detailed := r.URL.Query().Get("detailed") == "true"

        var response map[string]interface{}

        // Create advisor and run analysis
        interfaceRoles := &config.Get().InterfaceRoles
        advisor := vpp.NewAdvisor(vpp.Get(), interfaceRoles)

                var advisories []vpp.Advisory
                if detailed {
                        advisories = advisor.AnalyzeWithDiagnostics()
                } else {
                        advisories = advisor.Analyze()
                }

                // Count by severity level
                summary := map[string]int{
                        "critical": 0,
                        "warning":  0,
                        "info":     0,
                        "total":    len(advisories),
                }

                // Category counts
                categories := make(map[string]int)

                for _, adv := range advisories {
                        // Count by level
                        switch adv.Level {
                        case vpp.AdvisoryCritical:
                                summary["critical"]++
                        case vpp.AdvisoryWarning:
                                summary["warning"]++
                        case vpp.AdvisoryInfo:
                                summary["info"]++
                        }

                        // Count by category
                        categories[adv.Category]++
                }

                response = map[string]interface{}{
                        "timestamp":     time.Now().Unix(),
                        "advisories":    advisories,
                        "summary":       summary,
                        "categories":    categories,
                        "has_critical":  summary["critical"] > 0,
                        "detailed_mode": detailed,
                        "environment":   advisor.GetEnvironmentInfo(),
                }

        // Set JSON content type
        w.Header().Set("Content-Type", "application/json")
        w.Header().Set("Access-Control-Allow-Origin", "*")

        // Encode and send response
        if err := json.NewEncoder(w).Encode(response); err != nil {
                L_error("Failed to encode advisor data: %v", err)
                http.Error(w, "Internal server error", http.StatusInternalServerError)
        }
}

// handleAPIPPPoEInterfaces returns PPPoE session statistics for all interfaces
// It uses aggregated session statistics (active + cumulative) for accurate totals
func (s *Server) handleAPIPPPoEInterfaces(w http.ResponseWriter, r *http.Request) {
        // Use aggregated session stats by default (can be disabled with ?aggregated=false)
        useAggregated := r.URL.Query().Get("aggregated") != "false"

        var stats []*vpp.PPPoEInterfaceStats

        if useAggregated {
                // Use aggregated session stats for accurate TX on VLAN subinterfaces
                // Get aggregated stats from session manager
                aggregatedStats := s.sessionMgr.GetAggregatedInterfaceStats()

                // Convert to PPPoEInterfaceStats format
                statsMap := make(map[string]*vpp.PPPoEInterfaceStats)
                for _, aggStat := range aggregatedStats {
                        stat := &vpp.PPPoEInterfaceStats{
                                InterfaceName:  aggStat["interface_name"].(string),
                                InterfaceType:  aggStat["interface_type"].(string),
                                RxBytes:        aggStat["rx_bytes"].(uint64),
                                TxBytes:        aggStat["tx_bytes"].(uint64),
                                RxPackets:      aggStat["rx_packets"].(uint64),
                                TxPackets:      aggStat["tx_packets"].(uint64),
                                LastUpdate:     aggStat["last_update"].(int64),
                        }

                        // Add VLAN info if present
                        if outerVLAN, ok := aggStat["outer_vlan"].(uint16); ok {
                                stat.OuterVLAN = int(outerVLAN)
                                stat.VLANID = int(outerVLAN) // For single VLAN compatibility
                        }
                        if innerVLAN, ok := aggStat["inner_vlan"].(uint16); ok {
                                stat.InnerVLAN = int(innerVLAN)
                        }

                        // Add session count if available
                        if sessionCount, ok := aggStat["session_count"].(int); ok {
                                stat.SessionCount = sessionCount
                        }

                        // Mark all as aggregated PPPoE stats
                        stat.IsAggregated = true

                        statsMap[stat.InterfaceName] = stat
                }

                // Ensure all configured physical interfaces are included (even with 0 sessions)
                for _, ic := range config.Get().InterfaceConfigs {
                        // Physical interfaces don't contain dots
                        if !strings.Contains(ic.Name, ".") {
                                if _, exists := statsMap[ic.Name]; !exists {
                                        // Create empty stats for this interface
                                        statsMap[ic.Name] = &vpp.PPPoEInterfaceStats{
                                                InterfaceName: ic.Name,
                                                InterfaceType: "physical",
                                                LastUpdate:    time.Now().Unix(),
                                                IsAggregated:  true,
                                                SessionCount:  0,
                                        }
                                }
                        }
                }

                // Also ensure PPPoE interfaces from interface_roles are included (but NOT IP uplink interfaces!)
                // IP uplink interfaces will never have PPPoE sessions, so they shouldn't appear here
                for _, iface := range config.Get().InterfaceRoles.PPPoEInterfaces {
                        // interface_roles only contains physical interfaces
                        if _, exists := statsMap[iface]; !exists {
                                statsMap[iface] = &vpp.PPPoEInterfaceStats{
                                        InterfaceName: iface,
                                        InterfaceType: "physical",
                                        InterfaceRole: "pppoe",
                                        LastUpdate:    time.Now().Unix(),
                                        IsAggregated:  true,
                                        SessionCount:  0,
                                }
                        } else if statsMap[iface].InterfaceRole == "" {
                                statsMap[iface].InterfaceRole = "pppoe"
                        }
                }

                // Do NOT include IP uplink interfaces - they belong in physical interface stats only

                // Add interface role information to all stats (PPPoE interfaces only)
                for _, stat := range statsMap {
                        if stat.InterfaceRole == "" {
                                // Check if this is a PPPoE physical interface
                                for _, pppoeIface := range config.Get().InterfaceRoles.PPPoEInterfaces {
                                        if stat.InterfaceName == pppoeIface {
                                                stat.InterfaceRole = "pppoe"
                                                break
                                        }
                                }

                                // For VLAN interfaces, check if they're on a PPPoE physical interface
                                if stat.InterfaceRole == "" && strings.Contains(stat.InterfaceName, ".") {
                                        // Extract the physical interface name from the VLAN interface
                                        physicalInterface := strings.Split(stat.InterfaceName, ".")[0]
                                        for _, pppoeIface := range config.Get().InterfaceRoles.PPPoEInterfaces {
                                                if physicalInterface == pppoeIface {
                                                        stat.InterfaceRole = "pppoe"
                                                        break
                                                }
                                        }
                                }

                                // Do NOT check for IP uplink interfaces - they don't belong in PPPoE session stats
                        }
                }

                // Convert map to slice
                stats = make([]*vpp.PPPoEInterfaceStats, 0, len(statsMap))
                for _, stat := range statsMap {
                        stats = append(stats, stat)
                }
        } else {
                // Fall back to VPP-only method (legacy mode, inaccurate for VLAN TX)
                L_debug("Using VPP statistics only (legacy mode - TX may be inaccurate for VLANs)")

                // Use a map to deduplicate interfaces
                interfaceMap := make(map[string]bool)

                // Add configured interfaces from config
                for _, ic := range config.Get().InterfaceConfigs {
                        interfaceMap[ic.Name] = true
                }

                // Also include dynamically created VLAN and QinQ interfaces
        vlanMgr := vlan.GetManager()
        if vlanMgr != nil {
                        // Get all discovered interfaces (includes both single VLAN and QinQ)
                        discoveredInterfaces := vlanMgr.GetAllDiscoveredInterfaces()
                        for _, iface := range discoveredInterfaces {
                                interfaceMap[iface] = true
                        }
                }

                // Convert map keys to slice
                interfaces := make([]string, 0, len(interfaceMap))
                for iface := range interfaceMap {
                        interfaces = append(interfaces, iface)
        }

        // Get stats from VPP for all interfaces (configured + dynamic VLANs)
                var err error
                stats, err = vpp.Get().GetPPPoEInterfaceStats(interfaces)
        if err != nil {
                L_error("Failed to get PPPoE interface stats: %v", err)
                http.Error(w, "Failed to retrieve interface statistics", http.StatusInternalServerError)
                return
                }
        }

        // Enrich stats with VLAN information from the VLAN manager (only in legacy mode)
        vlanMgr := vlan.GetManager()
        if vlanMgr != nil && !useAggregated {
                for _, stat := range stats {
                        if stat.InterfaceType == "vlan" || stat.InterfaceType == "qinq" {
                                if vlanInfo := vlanMgr.GetVLANInfoByInterface(stat.InterfaceName); vlanInfo != nil {
                                        if vlanInfo.InterfaceType == "qinq" {
                                                stat.OuterVLAN = int(vlanInfo.OuterVLAN)
                                                stat.InnerVLAN = int(vlanInfo.InnerVLAN)
                                        } else if vlanInfo.InterfaceType == "vlan" {
                                                stat.VLANID = int(vlanInfo.OuterVLAN)
                                        }
                                }
                        }
                }
        }

        // Calculate totals across all interfaces
        var totalRxBytes, totalTxBytes, totalRxPackets, totalTxPackets uint64
        var totalSessions int

        for _, stat := range stats {
                totalRxBytes += stat.RxBytes
                totalTxBytes += stat.TxBytes
                totalRxPackets += stat.RxPackets
                totalTxPackets += stat.TxPackets
                totalSessions += stat.SessionCount
        }

        // Create response with individual stats and totals
        response := map[string]interface{}{
                "interfaces": stats,
                "total": map[string]interface{}{
                        "rx_bytes":      totalRxBytes,
                        "tx_bytes":      totalTxBytes,
                        "rx_packets":    totalRxPackets,
                        "tx_packets":    totalTxPackets,
                        "session_count": totalSessions,
                        "interface_count": len(stats),
                },
        }

        // Send response
        w.Header().Set("Content-Type", "application/json")
        w.Header().Set("Access-Control-Allow-Origin", "*")
        if err := json.NewEncoder(w).Encode(response); err != nil {
                L_error("Failed to encode PPPoE interface stats: %v", err)
                http.Error(w, "Internal server error", http.StatusInternalServerError)
        }
}

// handleAPIMetrics handles the /api/metrics.json endpoint
func (s *Server) handleAPIMetrics(w http.ResponseWriter, r *http.Request) {
        // Get metrics snapshot
        snapshot := metrics.GetInstance().GetSnapshot()

        // Send response
        w.Header().Set("Content-Type", "application/json")
        w.Header().Set("Access-Control-Allow-Origin", "*")
        if err := json.NewEncoder(w).Encode(snapshot); err != nil {
                L_error("Failed to encode metrics: %v", err)
                http.Error(w, "Internal server error", http.StatusInternalServerError)
        }
}

// handleAPIRadiusStats returns RADIUS statistics as JSON
func (s *Server) handleAPIRadiusStats(w http.ResponseWriter, r *http.Request) {
        // Only allow GET requests
        if r.Method != http.MethodGet {
                http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
                return
        }

        // Get RADIUS client from auth manager
        authMgr := auth.GetInstance()
        if authMgr == nil {
                http.Error(w, "Auth manager not available", http.StatusInternalServerError)
                return
        }

        // Get RADIUS stats through reflection (to avoid circular dependency)
        // The auth manager has a radiusClient field that we can access
        radiusStats := getRadiusStats(authMgr)

        // Set JSON content type
        w.Header().Set("Content-Type", "application/json")

        // Encode and send response
        if err := json.NewEncoder(w).Encode(radiusStats); err != nil {
                L_error("Failed to encode RADIUS stats: %v", err)
                http.Error(w, "Internal server error", http.StatusInternalServerError)
        }
}

// handleAPIL2TPTunnels returns L2TP tunnel and session statistics as JSON
func (s *Server) handleAPIL2TPTunnels(w http.ResponseWriter, r *http.Request) {
        // Get L2TP tunnel information
        tunnelData := s.getL2TPTunnelData()

        w.Header().Set("Content-Type", "application/json")
        w.Header().Set("Cache-Control", "no-cache")
        json.NewEncoder(w).Encode(tunnelData)
}

// handleAPIRelays returns LAC relay statistics as JSON
func (s *Server) handleAPIRelays(w http.ResponseWriter, r *http.Request) {
        // Get relay information
        relayData := s.getRelayData()

        w.Header().Set("Content-Type", "application/json")
        w.Header().Set("Cache-Control", "no-cache")
        json.NewEncoder(w).Encode(relayData)
}

// getL2TPTunnelData collects L2TP tunnel and session information
func (s *Server) getL2TPTunnelData() map[string]interface{} {
        result := map[string]interface{}{
                "tunnels":       []map[string]interface{}{},
                "tunnel_count":  0,
                "session_count": 0,
                "unique_peers":  0,
        }

        // Get all active sessions
        sessions := s.sessionMgr.GetActiveSessions()

        // Group sessions by tunnel
        tunnelMap := make(map[string]map[string]interface{})
        uniquePeers := make(map[string]bool)
        totalSessions := 0

        for _, sess := range sessions {
                // Only process L2TP sessions
                if sess.Key.Transport != 1 { // 1 = L2TP
                        continue
                }

                totalSessions++

                // Create tunnel key from peer and tunnel ID
                peerAddr := sess.Key.L2TPPeer.String()
                tunnelKey := fmt.Sprintf("%s:%d", peerAddr, sess.Key.TunnelID)
                uniquePeers[peerAddr] = true

                // Initialize tunnel entry if not exists
                if _, exists := tunnelMap[tunnelKey]; !exists {
                        tunnelMap[tunnelKey] = map[string]interface{}{
                                "our_tunnel_id":   sess.Key.TunnelID,
                                "peer_tunnel_id":  0, // Will be filled from transport if available
                                "peer_addr":       peerAddr,
                                "peer_port":       1701, // Default L2TP port
                                "state":           "established",
                                "session_count":   0,
                                "bytes_in":        uint64(0),
                                "bytes_out":       uint64(0),
                                "uptime_seconds":  0,
                                "vpp_index":       0,
                        }
                }

                tunnel := tunnelMap[tunnelKey]

                // Try to get peer tunnel ID and hostname from transport
                if l2tpTransport, ok := sess.Transport.(interface{
                        GetTunnelInfo() (uint16, uint16)
                        GetPeerHostname() string
                }); ok {
                        peerTunnelID, _ := l2tpTransport.GetTunnelInfo()
                        tunnel["peer_tunnel_id"] = peerTunnelID

                        // Get and set hostname if available
                        if hostname := l2tpTransport.GetPeerHostname(); hostname != "" {
                                tunnel["hostname"] = hostname
                        }
                }

                // Update tunnel stats
                tunnel["session_count"] = tunnel["session_count"].(int) + 1
                tunnel["bytes_in"] = tunnel["bytes_in"].(uint64) + sess.BytesIn.Load()
                tunnel["bytes_out"] = tunnel["bytes_out"].(uint64) + sess.BytesOut.Load()

                // Use earliest session start time as tunnel uptime
                sessionUptime := int(time.Since(sess.StartTime).Seconds())
                if currentUptime := tunnel["uptime_seconds"].(int); currentUptime == 0 || sessionUptime > currentUptime {
                        tunnel["uptime_seconds"] = sessionUptime
                }

                // Add VPP index if available (use first non-zero index found)
                if sess.VPPIfIndex > 0 && tunnel["vpp_index"].(int) == 0 {
                        tunnel["vpp_index"] = sess.VPPIfIndex
                }
        }

        // Convert map to slice
        tunnelList := []map[string]interface{}{}
        for _, tunnel := range tunnelMap {
                tunnelList = append(tunnelList, tunnel)
        }

        result["tunnels"] = tunnelList
        result["tunnel_count"] = len(tunnelList)
        result["session_count"] = totalSessions
        result["unique_peers"] = len(uniquePeers)

        return result
}

// getRelayData collects VPDN relay session information
func (s *Server) getRelayData() map[string]interface{} {
        result := map[string]interface{}{
                "lns_relays":     []map[string]interface{}{},
                "lns_count":      0,
                "session_count":  0,
                "total_bytes_in": uint64(0),
                "total_bytes_out": uint64(0),
                "vpdn_sessions":  0,  // Count of VPDN relay sessions
        }

        // Get LAC tunnel details from VPP
        var lacTunnels []map[string]interface{}
        tunnels, err := vpp.Get().GetLACTunnelDetails()
        if err != nil {
                L_error("Failed to get LAC tunnel details: %v", err)
        } else if tunnels != nil {
                lacTunnels = tunnels
                L_debug("Retrieved %d LAC tunnel details from VPP", len(lacTunnels))
        }

        // Get all active sessions
        activeSessions := s.sessionMgr.GetActiveSessions()

        // Group relay sessions by LNS
        lnsMap := make(map[string]map[string]interface{})
        totalBytesIn := uint64(0)
        totalBytesOut := uint64(0)
        vpdnCount := 0

        for _, sess := range activeSessions {
                // Only process sessions in relay state
                if sess.State != 5 { // SessionStateRelaying
                        continue
                }


                lnsAddr := ""
                if sess.LNSAddress != nil {
                        lnsAddr = sess.LNSAddress.String()
                }

                // Initialize LNS entry if not exists
                if _, exists := lnsMap[lnsAddr]; !exists {
                        lnsMap[lnsAddr] = map[string]interface{}{
                                "lns_address":     lnsAddr,
                                "session_count":   0,
                                "bytes_in":        uint64(0),
                                "bytes_out":       uint64(0),
                                "packets_in":      uint64(0),
                                "packets_out":     uint64(0),
                                "oldest_session":  time.Now(),
                                "sessions":        []map[string]interface{}{}, // List of session details
                                // L2TP tunnel info (populated from VPP or session data)
                                "tunnel_established": false,
                                "tunnel_id":         0,
                                "peer_tunnel_id":    0,
                                "tunnel_state":      "unknown",
                        }
                }

                lns := lnsMap[lnsAddr]

                // Update LNS stats
                lns["session_count"] = lns["session_count"].(int) + 1
                lns["bytes_in"] = lns["bytes_in"].(uint64) + sess.BytesIn.Load()
                lns["bytes_out"] = lns["bytes_out"].(uint64) + sess.BytesOut.Load()
                lns["packets_in"] = lns["packets_in"].(uint64) + sess.PacketsIn.Load()
                lns["packets_out"] = lns["packets_out"].(uint64) + sess.PacketsOut.Load()

                // Track oldest session (earliest start time)
                if sess.StartTime.Before(lns["oldest_session"].(time.Time)) {
                        lns["oldest_session"] = sess.StartTime
                }

                // Add session details
                // Use the authenticated username (already known from local auth)
                sessionUsername := sess.Username
                if sessionUsername == "" && sess.RelayAuthUsername != "" {
                        // Fallback to extracted username if available
                        sessionUsername = sess.RelayAuthUsername
                }

                sessionInfo := map[string]interface{}{
                        "session_id":      sess.Key.SessionId,
                        "username":        sessionUsername,
                        "client_ip":       "",
                        "client_mac":      fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x",
                                sess.Key.ClientMAC[0], sess.Key.ClientMAC[1], sess.Key.ClientMAC[2],
                                sess.Key.ClientMAC[3], sess.Key.ClientMAC[4], sess.Key.ClientMAC[5]),
                        "l2tp_session_id": sess.L2tpLocalSessionID,
                        "l2tp_tunnel_id":  sess.L2tpLocalTunnelID,
                        "ipcp_state":      sess.RelayIPCPState,
                        "ipv6cp_state":    sess.RelayIPv6CPState,
                        "duration":        time.Since(sess.StartTime).Round(time.Second).String(),
                }

                if sess.RelayExtractedIP != nil && !sess.RelayExtractedIP.IsUnspecified() {
                        sessionInfo["client_ip"] = sess.RelayExtractedIP.String()
                }

                sessions := lns["sessions"].([]map[string]interface{})
                lns["sessions"] = append(sessions, sessionInfo)

                // Update totals
                totalBytesIn += sess.BytesIn.Load()
                totalBytesOut += sess.BytesOut.Load()
        }

        // Merge LAC tunnel details from VPP
        for _, tunnel := range lacTunnels {
                lnsAddr := tunnel["peer_address"].(string)
                if lns, exists := lnsMap[lnsAddr]; exists {
                        // Update tunnel status from VPP
                        lns["tunnel_established"] = tunnel["established"].(bool)
                        lns["tunnel_id"] = tunnel["local_tunnel_id"]
                        lns["peer_tunnel_id"] = tunnel["remote_tunnel_id"]
                        lns["tunnel_state"] = tunnel["state"].(string)
                        lns["tunnel_index"] = tunnel["tunnel_index"]

                        // Add hostname if available
                        if hostname, ok := tunnel["peer_hostname"].(string); ok && hostname != "" {
                                lns["peer_hostname"] = hostname
                        }

                        // Add LAC tunnel statistics from VPP
                        if packetsTx, ok := tunnel["packets_tx"].(uint64); ok {
                                lns["lac_packets_tx"] = packetsTx
                        }
                        if packetsRx, ok := tunnel["packets_rx"].(uint64); ok {
                                lns["lac_packets_rx"] = packetsRx
                        }
                        if packetsDropped, ok := tunnel["packets_dropped"].(uint64); ok {
                                lns["lac_packets_dropped"] = packetsDropped
                        }

                        // Update session count from VPP if available
                        if vppSessionCount, ok := tunnel["session_count"].(uint32); ok {
                                // Use the max of our count and VPP's count
                                ourCount := lns["session_count"].(int)
                                if int(vppSessionCount) > ourCount {
                                        L_debug("LNS %s: VPP reports %d sessions, we have %d", lnsAddr, vppSessionCount, ourCount)
                                        lns["session_count"] = int(vppSessionCount)
                                }
                        }
                } else {
                        // VPP knows about a tunnel we don't have sessions for yet
                        // This can happen during tunnel establishment
                        L_debug("LAC tunnel to %s found in VPP but no relay sessions yet", lnsAddr)
                        lnsMap[lnsAddr] = map[string]interface{}{
                                "lns_address":        lnsAddr,
                                "session_count":      0,
                                "bytes_in":          uint64(0),
                                "bytes_out":         uint64(0),
                                "packets_in":        uint64(0),
                                "packets_out":       uint64(0),
                                "sessions":          []map[string]interface{}{},
                                "tunnel_established": tunnel["established"].(bool),
                                "tunnel_id":         tunnel["local_tunnel_id"],
                                "peer_tunnel_id":    tunnel["remote_tunnel_id"],
                                "tunnel_state":      tunnel["state"].(string),
                                "tunnel_index":      tunnel["tunnel_index"],
                                "avg_uptime_seconds": 0,
                                "avg_uptime":        "0s",
                        }

                        if hostname, ok := tunnel["peer_hostname"].(string); ok && hostname != "" {
                                lnsMap[lnsAddr]["peer_hostname"] = hostname
                        }
                }
        }

        // Convert map to slice and calculate average uptime
        lnsList := []map[string]interface{}{}
        for _, lns := range lnsMap {
                // Calculate average session uptime
                if sessionCount := lns["session_count"].(int); sessionCount > 0 {
                        // Check if we have oldest_session (won't exist for VPP-only tunnels)
                        if oldestSessionRaw, ok := lns["oldest_session"]; ok {
                                oldestSession := oldestSessionRaw.(time.Time)
                                lns["avg_uptime_seconds"] = int(time.Since(oldestSession).Seconds())
                                lns["avg_uptime"] = time.Since(oldestSession).Round(time.Second).String()
                        } else if lns["avg_uptime_seconds"] == nil {
                                // No uptime info available
                                lns["avg_uptime_seconds"] = 0
                                lns["avg_uptime"] = "0s"
                        }
                } else {
                        lns["avg_uptime_seconds"] = 0
                        lns["avg_uptime"] = "0s"
                }
                delete(lns, "oldest_session") // Remove internal field

                lnsList = append(lnsList, lns)
        }

        // Sort by session count (descending)
        sort.Slice(lnsList, func(i, j int) bool {
                return lnsList[i]["session_count"].(int) > lnsList[j]["session_count"].(int)
        })

        result["lns_relays"] = lnsList
        result["lns_count"] = len(lnsList)
        result["session_count"] = vpdnCount // Total VPDN relay sessions
        result["total_bytes_in"] = totalBytesIn
        result["total_bytes_out"] = totalBytesOut
        result["vpdn_sessions"] = vpdnCount

        // Calculate total session count
        totalSessions := 0
        for _, lns := range lnsList {
                totalSessions += lns["session_count"].(int)
        }
        result["session_count"] = totalSessions

        return result
}

// handleAPIVLANStats returns VLAN discovery statistics as JSON
func (s *Server) handleAPIVLANStats(w http.ResponseWriter, r *http.Request) {
        // Only allow GET requests
        if r.Method != http.MethodGet {
                http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
                return
        }

        // Get VLAN manager stats
        vlanMgr := vlan.GetManager()
        if vlanMgr == nil {
                // VLAN manager not initialized (dynamic VLANs not enabled)
                stats := map[string]interface{}{
                        "enabled": false,
                        "message": "Dynamic VLAN discovery is not enabled",
                }
                w.Header().Set("Content-Type", "application/json")
                json.NewEncoder(w).Encode(stats)
                return
        }

        // Get basic statistics from VLAN manager
        basicStats := vlanMgr.GetVLANStatistics()

        // Get enhanced session statistics per VLAN
        vlanSessionStats := s.sessionMgr.GetVLANStatistics()

        // Combine both statistics
        combinedStats := map[string]interface{}{
                "enabled":     true,
                "basic_stats": basicStats,
                "session_stats": vlanSessionStats,
        }

        // Set JSON content type
        w.Header().Set("Content-Type", "application/json")

        // Encode and send response
        if err := json.NewEncoder(w).Encode(combinedStats); err != nil {
                L_error("Failed to encode VLAN stats: %v", err)
                http.Error(w, "Internal server error", http.StatusInternalServerError)
        }
}

// handleAPIMonitorSession handles monitor session requests (supports both GET and POST)
func (s *Server) handleAPIMonitorSession(w http.ResponseWriter, r *http.Request) {
        // Support both GET and POST methods
        if r.Method != http.MethodGet && r.Method != http.MethodPost {
                http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
                return
        }

        // Parse request parameters
        var req struct {
                SwIfIndex *uint32 `json:"sw_if_index,omitempty"`
                SessionID *uint16 `json:"session_id,omitempty"`
                Enable    bool    `json:"enable"`
                Timeout   *uint32 `json:"timeout,omitempty"` // Optional, nil means use default
        }

        switch r.Method {
        case http.MethodGet:
                // Parse query parameters for GET request
                query := r.URL.Query()

                // Parse sw_if_index if provided
                if swIfStr := query.Get("sw_if_index"); swIfStr != "" {
                        var swIf uint32
                        if _, err := fmt.Sscanf(swIfStr, "%d", &swIf); err == nil {
                                req.SwIfIndex = &swIf
                        } else {
                                http.Error(w, "Invalid sw_if_index format", http.StatusBadRequest)
                                return
                        }
                }

                // Parse session_id if provided
                if sidStr := query.Get("session_id"); sidStr != "" {
                        var sid uint16
                        if _, err := fmt.Sscanf(sidStr, "%d", &sid); err == nil {
                                req.SessionID = &sid
                        } else {
                                http.Error(w, "Invalid session_id format", http.StatusBadRequest)
                                return
                        }
                }

                // Parse enable flag (default: true)
                enableStr := query.Get("enable")
                if enableStr == "" || enableStr == "true" || enableStr == "1" {
                        req.Enable = true
                } else if enableStr == "false" || enableStr == "0" {
                        req.Enable = false
                } else {
                        http.Error(w, "Invalid enable value (use true/false or 1/0)", http.StatusBadRequest)
                        return
                }

                // Parse timeout (default: 120 seconds)
                if timeoutStr := query.Get("timeout"); timeoutStr != "" {
                        var timeout uint32
                        if _, err := fmt.Sscanf(timeoutStr, "%d", &timeout); err == nil {
                                req.Timeout = &timeout
                        } else {
                                http.Error(w, "Invalid timeout format", http.StatusBadRequest)
                                return
                        }
                }

        case http.MethodPost:
                // Parse JSON body for POST request
                if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
                        http.Error(w, "Invalid JSON request body", http.StatusBadRequest)
                        return
                }
                defer r.Body.Close()
        }

        // Determine actual timeout value
        var actualTimeout uint32
        if req.Timeout == nil {
                // No timeout specified, use default
                actualTimeout = 120 // 2 minutes default
        } else {
                // Use specified timeout (0 means infinite)
                actualTimeout = *req.Timeout
        }

        // Determine identifier type and value
        var identifierType uint8
        var identifier uint32
        var monitorTarget string

        if req.SwIfIndex != nil {
                // Monitor by sw_if_index (preferred)
                identifierType = 0
                identifier = *req.SwIfIndex
                monitorTarget = fmt.Sprintf("sw_if_index %d", identifier)
        } else if req.SessionID != nil {
                // Monitor by session ID
                identifierType = 1
                identifier = uint32(*req.SessionID)
                monitorTarget = fmt.Sprintf("session_id %d", identifier)
        } else {
                http.Error(w, "Either sw_if_index or session_id must be specified", http.StatusBadRequest)
                return
        }

        // Find the session to update its debug state and determine transport type
        var session *sessions.Session
        var sessionKey sessions.SessionKey
        found := false

        // Find the session by sw_if_index or session_id
        if req.SwIfIndex != nil {
                // Find by VPP interface index
                allSessions := s.sessionMgr.GetActiveSessions()
                for _, sess := range allSessions {
                        if sess.VPPIfIndex == *req.SwIfIndex {
                                session = sess
                                sessionKey = sess.Key
                                found = true
                                break
                        }
                }
        } else if req.SessionID != nil {
                // Find by session ID
                key, ok := s.sessionMgr.FindSessionKeyByID(*req.SessionID)
                if ok {
                        session, found = s.sessionMgr.Get(key)
                        sessionKey = key
                }
        }

        // Call appropriate VPP API based on transport type
        var tapName string
        var tapIndex uint32
        var err error

        if found && session != nil {
                // Determine transport type and call appropriate monitor function
                switch session.Key.Transport {
                case 0: // PPPoE (TransportPPPoE)
                        tapName, tapIndex, err = vpp.Get().MonitorPPPoENGSession(identifierType, identifier, req.Enable, actualTimeout)
                case 1: // L2TP (TransportL2TP)
                        tapName, tapIndex, err = vpp.Get().MonitorL2TPSession(identifierType, identifier, req.Enable, actualTimeout)
                default:
                        err = fmt.Errorf("unknown transport type: %d", session.Key.Transport)
                }
        } else {
                // Session not found
                err = fmt.Errorf("session not found for %s", monitorTarget)
        }
        if err != nil {
                L_error("Failed to configure monitor TAP for %s: %v", monitorTarget, err)
                w.Header().Set("Content-Type", "application/json")
                w.WriteHeader(http.StatusInternalServerError)
                json.NewEncoder(w).Encode(map[string]interface{}{
                        "success": false,
                        "error":   err.Error(),
                })
                return
        }

        // Update session debug state if we found the session
        if found && session != nil {
                // Get client IP from request for tracking who enabled debug
                clientIP := r.RemoteAddr
                if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
                        clientIP = forwarded
                }

                session.SetMonitorState(req.Enable, actualTimeout, clientIP, tapName, tapIndex)
                L_debug("Updated monitor state for session %d: enabled=%v, timeout=%d, by=%s, tap=%s",
                        sessionKey.SessionId, req.Enable, actualTimeout, clientIP, tapName)
        }

        // Log success
        action := "enabled"
        if !req.Enable {
                action = "disabled"
        }
        timeoutMsg := fmt.Sprintf("%d seconds", actualTimeout)
        if actualTimeout == 0 {
                timeoutMsg = "infinite"
        }
        L_info("Monitor TAP %s for %s (timeout: %s)", action, monitorTarget, timeoutMsg)

        // Return success response
        response := map[string]interface{}{
                "success": true,
                "message": fmt.Sprintf("Monitor TAP %s for %s", action, monitorTarget),
                "timeout": actualTimeout,
        }

        // Include TAP name when enabling
        if req.Enable && tapName != "" {
                response["tap_name"] = tapName
                response["tap_index"] = tapIndex
        }

        w.Header().Set("Content-Type", "application/json")
        json.NewEncoder(w).Encode(response)
}

// getRadiusStats retrieves RADIUS statistics from the auth manager
func getRadiusStats(authMgr interface{}) interface{} {
        // Default empty stats if RADIUS is not enabled
        emptyStats := map[string]interface{}{
                "enabled": false,
                "message": "RADIUS not configured",
        }

        // Type assert to get the actual auth.Manager
        mgr, ok := authMgr.(*auth.Manager)
        if !ok {
                return emptyStats
        }

        // Get the RADIUS client using the getter method
        client := mgr.GetRADIUSClient()
        if client == nil {
                return emptyStats
        }

        // Get metrics from the client
        statsMap := client.GetMetrics()

        // Convert to a map with enabled flag
        return map[string]interface{}{
                "enabled":           true,
                "total_requests":    statsMap.TotalRequests,
                "total_accepts":     statsMap.TotalAccepts,
                "total_rejects":     statsMap.TotalRejects,
                "total_timeouts":    statsMap.TotalTimeouts,
                "total_queue_full":  statsMap.TotalQueueFull,
                "total_errors":      statsMap.TotalErrors,
                "acct_starts":       statsMap.AcctStarts,
                "acct_stops":        statsMap.AcctStops,
                "acct_interims":     statsMap.AcctInterims,
                "acct_timeouts":     statsMap.AcctTimeouts,
                "acct_errors":       statsMap.AcctErrors,
                "outstanding":       statsMap.Outstanding,
                "queue_depth":       statsMap.QueueDepth,
                "queue_capacity":    statsMap.QueueCapacity,
                "last_response_ms":  statsMap.LastResponseMs,
                "min_response_ms":   statsMap.MinResponseMs,
                "max_response_ms":   statsMap.MaxResponseMs,
                "avg_response_ms":   statsMap.AvgResponseMs,
                "success_rate":      statsMap.SuccessRate,
                "primary_healthy":   statsMap.PrimaryHealthy,
                "secondary_healthy": statsMap.SecondaryHealthy,
        }
}

// handleAPIPoolsSLAAC returns IPv6 SLAAC pool statistics
func (s *Server) handleAPIPoolsSLAAC(w http.ResponseWriter, r *http.Request) {
        // Check for brief parameter
        brief := r.URL.Query().Get("brief") == "1"

        // Get SLAAC pool manager using the global getter (same pattern as PD pool)
        poolMgr := ipv6.GetSLAACPoolManager()
        if poolMgr == nil {
                http.Error(w, "SLAAC pool manager not initialized", http.StatusServiceUnavailable)
                return
        }

        // Get all pools
        pools := poolMgr.GetAllPools()

        var poolInfos []PoolInfo

        for name, pool := range pools {
                stats := pool.GetStats()

                info := PoolInfo{
                        Name:             name,
                        Type:             "ipv6_slaac",
                        BasePrefix:       stats.BasePrefix,
                        TotalCapacity:    int64(stats.TotalPrefixes),
                        Allocated:        int64(stats.AllocatedPrefixes),
                        Available:        int64(stats.AvailablePrefixes),
                        UsagePercent:     stats.UsagePercent,
                        PoolConfig: map[string]interface{}{
                                "prefix_size": 64, // SLAAC always uses /64
                        },
                }

                // Add allocations only if not brief mode
                if !brief {
                        allocations := pool.GetAllocations()
                        for _, alloc := range allocations {
                                info.Allocations = append(info.Allocations, PoolAllocation{
                                        Prefix:      alloc.Prefix.String(),
                                        Username:    alloc.Username,
                                        SessionID:   uint16(alloc.SessionID),
                                        AllocatedAt: alloc.AllocatedAt,
                                        ExpiresAt:   alloc.ExpiresAt,
                                        Source:      "slaac",
                                })
                        }
                }

                poolInfos = append(poolInfos, info)
        }

        // If no pools, return empty array rather than nil
        if poolInfos == nil {
                poolInfos = []PoolInfo{}
        }

        // Send response
        w.Header().Set("Content-Type", "application/json")
        w.Header().Set("Access-Control-Allow-Origin", "*")
        if err := json.NewEncoder(w).Encode(poolInfos); err != nil {
                L_error("Failed to encode SLAAC pool response: %v", err)
        }
}

// LoggingRequest represents a request to enable/disable session logging
type LoggingRequest struct {
        Target   string `json:"target"`    // Session ID or username/pattern
        LogLevel string `json:"log_level"` // "ERROR", "WARN", "INFO", "DEBUG", "TRACE"
        Duration int    `json:"duration"`  // Duration in minutes (0 = 24 hours)
}

// LoggingResponse represents the response for logging operations
type LoggingResponse struct {
        Status           string   `json:"status"`
        Target           string   `json:"target"`
        LogLevel         string   `json:"log_level,omitempty"`
        EnabledSessions  int      `json:"enabled_sessions,omitempty"`
        DisabledSessions int      `json:"disabled_sessions,omitempty"`
        ExistingSessions []uint16 `json:"existing_sessions,omitempty"`
        ExpiresAt        string   `json:"expires_at,omitempty"`
        Error            string   `json:"error,omitempty"`
}

// handleAPILoggingEnable handles requests to enable session logging
func (s *Server) handleAPILoggingEnable(w http.ResponseWriter, r *http.Request) {
        if r.Method != http.MethodPost {
                http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
                return
        }

        var req LoggingRequest
        if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
                http.Error(w, fmt.Sprintf("Invalid request: %v", err), http.StatusBadRequest)
                return
        }

        // Validate request
        if req.Target == "" {
                http.Error(w, "Target is required", http.StatusBadRequest)
                return
        }

        L_debug("Logging enable request: target='%s', level='%s', duration=%d minutes",
                req.Target, req.LogLevel, req.Duration)

        if req.LogLevel == "" {
                req.LogLevel = "INFO"
        }

        // Default duration is 24 hours if not specified
        duration := time.Duration(req.Duration) * time.Minute
        if duration == 0 {
                duration = 24 * time.Hour
        }

        // Get session manager
        mgr := sessions.GetManager()
        if mgr == nil {
                http.Error(w, "Session manager not available", http.StatusInternalServerError)
                return
        }

        // Enable logging
        enabledCount, err := mgr.EnableLoggingForTarget(req.Target, req.LogLevel, duration)
        if err != nil {
                resp := LoggingResponse{
                        Status: "error",
                        Target: req.Target,
                        Error:  err.Error(),
                }
                w.Header().Set("Content-Type", "application/json")
                w.WriteHeader(http.StatusInternalServerError)
                json.NewEncoder(w).Encode(resp)
                return
        }

        resp := LoggingResponse{
                Status:          "success",
                Target:          req.Target,
                LogLevel:        req.LogLevel,
                EnabledSessions: enabledCount,
                ExpiresAt:       time.Now().Add(duration).Format(time.RFC3339),
        }

        L_info("Logging enabled for target '%s': %d sessions enabled",
                req.Target, enabledCount)

        w.Header().Set("Content-Type", "application/json")
        w.Header().Set("Access-Control-Allow-Origin", "*")
        json.NewEncoder(w).Encode(resp)
}

// handleAPILoggingDisable handles requests to disable session logging
func (s *Server) handleAPILoggingDisable(w http.ResponseWriter, r *http.Request) {
        if r.Method != http.MethodPost {
                http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
                return
        }

        var req struct {
                Target string `json:"target"`
        }
        if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
                http.Error(w, fmt.Sprintf("Invalid request: %v", err), http.StatusBadRequest)
                return
        }

        if req.Target == "" {
                http.Error(w, "Target is required", http.StatusBadRequest)
                return
        }

        // Get session manager
        mgr := sessions.GetManager()
        if mgr == nil {
                http.Error(w, "Session manager not available", http.StatusInternalServerError)
                return
        }

        // Disable logging
        disabledCount := mgr.DisableLoggingForTarget(req.Target)

        resp := LoggingResponse{
                Status:           "success",
                Target:           req.Target,
                DisabledSessions: disabledCount,
        }

        w.Header().Set("Content-Type", "application/json")
        w.Header().Set("Access-Control-Allow-Origin", "*")
        json.NewEncoder(w).Encode(resp)
}

// handleAPILoggingList handles requests to list active session logging
func (s *Server) handleAPILoggingList(w http.ResponseWriter, r *http.Request) {
        if r.Method != http.MethodGet {
                http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
                return
        }

        // Get complete logging status (sessions and patterns)
        loggingStatus := GetLoggingStatus()

        w.Header().Set("Content-Type", "application/json")
        w.Header().Set("Access-Control-Allow-Origin", "*")
        json.NewEncoder(w).Encode(loggingStatus)
}
