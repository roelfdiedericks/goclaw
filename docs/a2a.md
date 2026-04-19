---
title: "A2A Networking"
description: "Configure and use GoClaw Agent2Agent networking over libp2p"
section: "Advanced"
weight: 35
---

# A2A Networking

GoClaw can talk to other GoClaw nodes through its A2A subsystem. In the current release, the active transport is `libp2p`.

In plain terms, A2A currently gives you **remote agent task submission** between GoClaw nodes. It is not yet a shared live chat session where two agents stay in one mirrored conversation.

This lets you:

- discover peers through rendezvous and bootstrap peers
- inspect peer state and retained remote tasks
- exchange pairing information
- ping peers before sending work
- submit, resume, or cancel remote tasks

## What A2A Actually Does

Today, the main A2A workflow is:

1. one GoClaw node discovers or trusts another node
2. you choose a peer by alias or peer ID
3. you send that peer a task or prompt
4. the remote GoClaw executes it and returns task updates or the final result

So when people say "chat to a remote agent", the current product meaning is usually:

- use `/a2a submit <peer> <message>`
- wait for the remote node to complete the task
- read the returned result

That is closer to "send work to a remote GoClaw" than "open a shared bi-directional chat room".

## How To Talk To A Remote Agent

The current manual workflow is:

1. enable A2A on both nodes
2. add the remote node as a trusted peer
3. test connectivity with `/a2a ping <peer>`
4. send work with `/a2a submit <peer> <message>`

Example:

```text
/a2a ping wsl
/a2a submit wsl Summarize the latest logs and tell me if anything looks wrong.
```

Other useful follow-up commands:

- `/a2a tasks active`
- `/a2a resume <peer> <task-id>`
- `/a2a cancel <peer> <task-id>`

## Set Up A Peer In The Web UI

Use the live Web UI, not the standalone setup editor.

Open the **A2A Peers** section. You will see:

- **Trusted A2A Peers** — your saved local trust records
- **Local Pairing Payload** — this node's `peerId` and currently advertised addresses
- **Ping Result** — a quick live connectivity check for configured peers

### Recommended pairing flow

On the remote GoClaw node:

1. Open the live Web UI.
2. Go to **A2A Peers**.
3. Copy the values from **Local Pairing Payload**:
   - `Peer ID`
   - advertised addresses

On your local GoClaw node:

1. Open the live Web UI.
2. Go to **A2A Peers**.
3. Click **Add Peer**.
4. Fill in the form:
   - **Type**: `libp2p`
   - **Alias**: a friendly name like `wsl`, `vps`, or `office`
   - **Peer ID**: paste the remote node's peer ID
   - **Multiaddrs**: paste the remote node's advertised addresses, one per line
   - **Local User**: select the local user this trusted remote peer should map to
   - **Enabled**: leave on unless you want to keep the record without using it
   - **Notes**: optional
5. Save the peer.
6. Use the built-in Ping action or run `/a2a ping <alias>`.

### What `Local User` means

`Local User` is important for authorization.

It tells GoClaw which local user identity a trusted remote peer maps to on this node. In other words, if the remote peer submits work into this node, that work runs under the permissions of the selected local user.

If you want two nodes to trust each other both ways, repeat this setup on both nodes.

## How It Behaves

GoClaw is intentionally relay-first when a direct path is not ready yet.

- If relay connectivity is available, GoClaw uses it immediately for the current request.
- If a peer is currently `relay-only` but GoClaw knows plausible direct addresses for that peer, GoClaw can launch a short background direct dial attempt for future traffic.
- That background direct-upgrade attempt does not delay the live `ping`, `submit`, or other outbound request already in progress.

This means you may see a relay path first and a direct path later. That is normal.

## Minimal Configuration

```json
{
  "a2a": {
    "enabled": true,
    "defaultTransport": "libp2p",
    "libp2p": {
      "enabled": true,
      "listenAddrs": [
        "/ip4/0.0.0.0/tcp/0",
        "/ip4/0.0.0.0/udp/0/quic-v1"
      ],
      "discovery": {
        "rendezvousEnabled": true,
        "bootstrapSeedTXT": "p2p_boot.goclaw.org"
      },
      "relay": {
        "enableClient": true,
        "enableAutoRelay": true,
        "enableHolePunch": true,
        "enableBackgroundDirectUpgrade": true,
        "directUpgradeTimeoutSeconds": 3,
        "directUpgradeCooldownSeconds": 30
      }
    }
  }
}
```

| Field | Default | Description |
|-------|---------|-------------|
| `a2a.enabled` | `false` | Enable the A2A subsystem |
| `a2a.defaultTransport` | `"libp2p"` | Active A2A transport |
| `a2a.libp2p.enabled` | `false` | Enable the libp2p transport |
| `a2a.libp2p.listenAddrs` | local TCP + QUIC ephemeral ports | Host listen addresses |
| `a2a.libp2p.announceAddrs` | `[]` | Optional explicit advertised addresses |
| `a2a.libp2p.discovery.bootstrapPeers` | `[]` | Explicit bootstrap multiaddrs |
| `a2a.libp2p.discovery.bootstrapSeedTXT` | `p2p_boot.goclaw.org` | DNS TXT fallback when `bootstrapPeers` is empty |
| `a2a.libp2p.discovery.rendezvousEnabled` | `true` | Enable GoClaw rendezvous registration and lookup |
| `a2a.libp2p.relay.enableClient` | `true` | Allow relayed connectivity |
| `a2a.libp2p.relay.enableServer` | `false` | Offer relay service from this node |
| `a2a.libp2p.relay.enableAutoRelay` | `true` | Use bootstrap peers as relay candidates |
| `a2a.libp2p.relay.enableHolePunch` | `true` | Allow libp2p hole punching |
| `a2a.libp2p.relay.enableBackgroundDirectUpgrade` | `true` | Try a short background direct dial after relay-backed outbound traffic when direct candidates are known |
| `a2a.libp2p.relay.directUpgradeTimeoutSeconds` | `3` | Timeout for one background direct dial attempt |
| `a2a.libp2p.relay.directUpgradeCooldownSeconds` | `30` | Minimum time between background direct-upgrade attempts for the same peer |

## Manual Usage

Use the `/a2a` slash command from any text channel:

```text
/a2a status
/a2a peers connected
/a2a tasks active
/a2a pair
/a2a ping wsl
/a2a submit wsl hello from goclaw
/a2a resume wsl remote-20260407T002849577468142
/a2a cancel wsl remote-20260407T002849577468142
```

Common patterns:

- `/a2a status` to confirm transport mode, readiness, addresses, and peer counts
- `/a2a peers all` to confirm the peer record and current runtime state
- `/a2a peers connected` to see currently connected peers
- `/a2a pair` to get the local pairing payload
- `/a2a ping <peer>` before sending work
- `/a2a submit <peer> <message>` to send work to another GoClaw node

If `/a2a submit` succeeds, that is the current user-facing way to "talk to a remote agent".

## Troubleshooting

### I added a peer, but remote work is still not authorized

Check these first:

- the peer record is **Enabled**
- the `Peer ID` matches the remote node exactly
- the saved multiaddrs are valid and current
- `Local User` is set to a real local user on this node

### I still see relay paths

That can be expected. Relay is the working fallback path.

Background direct upgrade is:

- best-effort
- bounded by timeout
- cooldown-limited
- only useful for later traffic if it succeeds

If direct upgrade never happens, check:

- whether the peer has usable non-relay addresses
- whether hole punching is enabled
- whether the nodes are behind NAT setups that allow direct UDP traffic
- whether your infra/bootstrap information is correct

### Does background direct upgrade change rendezvous advertising?

No. This feature only improves runtime connection behavior. It does not change how rendezvous entries are advertised by itself.

## See Also

- [Configuration](configuration.md) — Main `goclaw.json` reference
- [Channel Commands](commands.md) — `/a2a` manual command reference
- [A2A Tool](tools/a2a.md) — Agent-facing A2A tool
- [Roles](roles.md) — Owner-only tool access
