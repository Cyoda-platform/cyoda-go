package registry

import (
	"encoding/binary"

	"github.com/hashicorp/memberlist"

	spi "github.com/cyoda-platform/cyoda-go-spi"
)

// Gossip satisfies spi.ClusterBroadcaster by multiplexing topics over the
// same memberlist instance it uses for membership. Every broadcast is sent
// on a single memberlist "user message" channel; the message is prefixed
// with a uvarint-length topic so the receiver's gossipDelegate can route
// it to the right handler. Semantics match the spi.ClusterBroadcaster
// contract: fire-and-forget, best-effort, no ordering or persistence.
var _ spi.ClusterBroadcaster = (*Gossip)(nil)

// Broadcast enqueues payload for fan-out delivery to every reachable node
// subscribed to topic. Returns immediately; delivery is asynchronous.
func (g *Gossip) Broadcast(topic string, payload []byte) {
	g.delegate.queue.QueueBroadcast(&topicBroadcast{msg: encodeTopicMsg(topic, payload)})
}

// Subscribe registers handler for every broadcast received on topic. Multiple
// subscribers for the same topic are supported; handlers run on the
// memberlist receive goroutine (serialized per-message), so they must not
// block indefinitely.
func (g *Gossip) Subscribe(topic string, handler func(payload []byte)) {
	g.delegate.subscribe(topic, handler)
}

// topicBroadcast implements memberlist.Broadcast. Invalidates returns false
// because topic messages are semantically additive (clock updates, cache
// invalidations, etc.) — no instance of this type supersedes another.
type topicBroadcast struct{ msg []byte }

func (t *topicBroadcast) Invalidates(memberlist.Broadcast) bool { return false }
func (t *topicBroadcast) Message() []byte                       { return t.msg }
func (t *topicBroadcast) Finished()                             {}

// encodeTopicMsg prefixes payload with [uvarint(len(topic))][topic bytes]
// so the receiver can recover the topic without a separate envelope type.
func encodeTopicMsg(topic string, payload []byte) []byte {
	var lenBuf [binary.MaxVarintLen32]byte
	n := binary.PutUvarint(lenBuf[:], uint64(len(topic)))
	out := make([]byte, 0, n+len(topic)+len(payload))
	out = append(out, lenBuf[:n]...)
	out = append(out, topic...)
	out = append(out, payload...)
	return out
}

// decodeTopicMsg reverses encodeTopicMsg. Returns ok=false on any malformed
// input; callers should drop such messages (they cannot be safely dispatched).
func decodeTopicMsg(msg []byte) (topic string, payload []byte, ok bool) {
	topicLen, n := binary.Uvarint(msg)
	if n <= 0 {
		return "", nil, false
	}
	end := n + int(topicLen)
	if end > len(msg) {
		return "", nil, false
	}
	return string(msg[n:end]), msg[end:], true
}
