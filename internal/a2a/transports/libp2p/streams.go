package libp2p

import (
	"bufio"
	"context"
	"encoding/json"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
)

func (r *Runtime) handleRPCStream(stream network.Stream) {
	defer stream.Close()

	var first rpcEnvelope
	if err := json.NewDecoder(stream).Decode(&first); err != nil {
		_ = json.NewEncoder(stream).Encode(rpcEnvelope{Kind: "error", Error: err.Error()})
		return
	}
	remotePeer := stream.Conn().RemotePeer().String()
	switch first.Kind {
	case "submit":
		if r.callbacks.OnInboundSubmit == nil {
			_ = json.NewEncoder(stream).Encode(rpcEnvelope{Kind: "error", TaskID: first.TaskID, Error: "inbound submit not configured"})
			return
		}
		updates, err := r.callbacks.OnInboundSubmit(context.Background(), remotePeer, first.TaskID, first.Input)
		if err != nil {
			_ = json.NewEncoder(stream).Encode(rpcEnvelope{Kind: "error", TaskID: first.TaskID, Error: err.Error()})
			return
		}
		r.streamTaskUpdates(stream, updates)
	case "resume":
		if r.callbacks.OnInboundResume == nil {
			_ = json.NewEncoder(stream).Encode(rpcEnvelope{Kind: "error", TaskID: first.TaskID, Error: "inbound resume not configured"})
			return
		}
		updates, err := r.callbacks.OnInboundResume(remotePeer, first.TaskID)
		if err != nil {
			_ = json.NewEncoder(stream).Encode(rpcEnvelope{Kind: "error", TaskID: first.TaskID, Error: err.Error()})
			return
		}
		r.streamTaskUpdates(stream, updates)
	case "cancel":
		env := rpcEnvelope{Kind: "cancelled", TaskID: first.TaskID}
		if r.callbacks.OnInboundCancel != nil {
			if err := r.callbacks.OnInboundCancel(remotePeer, first.TaskID); err != nil {
				env.Kind = "error"
				env.Error = err.Error()
			}
		}
		_ = json.NewEncoder(stream).Encode(env)
	case "ping":
		_ = json.NewEncoder(stream).Encode(rpcEnvelope{Kind: "pong", Message: "pong"})
	default:
		_ = json.NewEncoder(stream).Encode(rpcEnvelope{Kind: "error", Error: "unknown rpc kind"})
	}
}

func (r *Runtime) streamTaskUpdates(stream network.Stream, updates <-chan TaskUpdate) {
	writer := bufio.NewWriter(stream)
	enc := json.NewEncoder(writer)
	for update := range updates {
		if update.UpdatedAt.IsZero() {
			update.UpdatedAt = time.Now()
		}
		if err := enc.Encode(rpcEnvelope{Kind: "update", TaskID: update.TaskID, Update: &update}); err != nil {
			return
		}
		if err := writer.Flush(); err != nil {
			return
		}
	}
}
