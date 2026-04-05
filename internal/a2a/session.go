package a2a

const TransportIDLibp2p = "libp2p"

func SessionKeyForTask(transportID, peerID, taskID string) string {
	return "a2a:" + transportID + ":" + peerID + ":" + taskID
}
