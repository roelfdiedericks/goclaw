package libp2p

import (
	"bufio"
	"context"
	"encoding/json"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

func (r *Runtime) handleRPCStream(stream network.Stream) {
	defer stream.Close()
	remotePeer := stream.Conn().RemotePeer().String()
	L_info("a2a libp2p: inbound rpc stream opened", "peerID", remotePeer, "protocol", stream.Protocol())

	var first rpcEnvelope
	if err := json.NewDecoder(stream).Decode(&first); err != nil {
		L_warn("a2a libp2p: rpc decode failed", "peerID", remotePeer, "error", err)
		_ = json.NewEncoder(stream).Encode(rpcEnvelope{Kind: "error", Error: err.Error()})
		return
	}
	L_debug("a2a libp2p: inbound rpc envelope received", "peerID", remotePeer, "kind", first.Kind, "taskID", first.TaskID)
	switch first.Kind {
	case "submit":
		if r.callbacks.OnInboundSubmit == nil {
			L_warn("a2a libp2p: inbound submit rejected", "peerID", remotePeer, "taskID", first.TaskID, "error", "inbound submit not configured")
			_ = json.NewEncoder(stream).Encode(rpcEnvelope{Kind: "error", TaskID: first.TaskID, Error: "inbound submit not configured"})
			return
		}
		L_info("a2a libp2p: inbound submit received", "peerID", remotePeer, "taskID", first.TaskID, "inputLength", len(first.Input))
		updates, err := r.callbacks.OnInboundSubmit(context.Background(), remotePeer, first.TaskID, first.Input)
		if err != nil {
			L_warn("a2a libp2p: inbound submit rejected", "peerID", remotePeer, "taskID", first.TaskID, "error", err)
			_ = json.NewEncoder(stream).Encode(rpcEnvelope{Kind: "error", TaskID: first.TaskID, Error: err.Error()})
			return
		}
		L_trace("a2a libp2p: streaming inbound submit updates", "peerID", remotePeer, "taskID", first.TaskID)
		r.streamTaskUpdates(stream, updates)
	case "resume":
		if r.callbacks.OnInboundResume == nil {
			L_warn("a2a libp2p: inbound resume rejected", "peerID", remotePeer, "taskID", first.TaskID, "error", "inbound resume not configured")
			_ = json.NewEncoder(stream).Encode(rpcEnvelope{Kind: "error", TaskID: first.TaskID, Error: "inbound resume not configured"})
			return
		}
		L_info("a2a libp2p: inbound resume received", "peerID", remotePeer, "taskID", first.TaskID)
		updates, err := r.callbacks.OnInboundResume(remotePeer, first.TaskID)
		if err != nil {
			L_warn("a2a libp2p: inbound resume rejected", "peerID", remotePeer, "taskID", first.TaskID, "error", err)
			_ = json.NewEncoder(stream).Encode(rpcEnvelope{Kind: "error", TaskID: first.TaskID, Error: err.Error()})
			return
		}
		L_trace("a2a libp2p: streaming inbound resume updates", "peerID", remotePeer, "taskID", first.TaskID)
		r.streamTaskUpdates(stream, updates)
	case "cancel":
		env := rpcEnvelope{Kind: "cancelled", TaskID: first.TaskID}
		L_info("a2a libp2p: inbound cancel received", "peerID", remotePeer, "taskID", first.TaskID)
		if r.callbacks.OnInboundCancel != nil {
			if err := r.callbacks.OnInboundCancel(remotePeer, first.TaskID); err != nil {
				L_warn("a2a libp2p: inbound cancel rejected", "peerID", remotePeer, "taskID", first.TaskID, "error", err)
				env.Kind = "error"
				env.Error = err.Error()
			}
		}
		_ = json.NewEncoder(stream).Encode(env)
	case "ping":
		L_info("a2a libp2p: inbound rpc ping received", "peerID", remotePeer)
		_ = json.NewEncoder(stream).Encode(rpcEnvelope{Kind: "pong", Message: "pong"})
	default:
		L_warn("a2a libp2p: unknown rpc kind", "peerID", remotePeer, "kind", first.Kind, "taskID", first.TaskID)
		_ = json.NewEncoder(stream).Encode(rpcEnvelope{Kind: "error", Error: "unknown rpc kind"})
	}
	L_debug("a2a libp2p: inbound rpc stream closed", "peerID", remotePeer, "kind", first.Kind, "taskID", first.TaskID)
}

func (r *Runtime) streamTaskUpdates(stream network.Stream, updates <-chan TaskUpdate) {
	remotePeer := stream.Conn().RemotePeer().String()
	writer := bufio.NewWriter(stream)
	enc := json.NewEncoder(writer)
	var lastTaskID string
	for update := range updates {
		if update.UpdatedAt.IsZero() {
			update.UpdatedAt = time.Now()
		}
		lastTaskID = update.TaskID
		L_trace("a2a libp2p: streaming task update", "peerID", remotePeer, "taskID", update.TaskID, "state", update.State)
		if err := enc.Encode(rpcEnvelope{Kind: "update", TaskID: update.TaskID, Update: &update}); err != nil {
			L_warn("a2a libp2p: encode task update failed", "peerID", remotePeer, "taskID", update.TaskID, "error", err)
			return
		}
		if err := writer.Flush(); err != nil {
			L_warn("a2a libp2p: flush task update failed", "peerID", remotePeer, "taskID", update.TaskID, "error", err)
			return
		}
	}
	L_trace("a2a libp2p: task update stream ended", "peerID", remotePeer, "taskID", lastTaskID)
}
