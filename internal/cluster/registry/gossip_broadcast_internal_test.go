package registry

import (
	"testing"
)

// TestEncodeTopicMsg_OversizedPayload_ReturnsNil asserts that encoding a
// gossip message whose capacity would overflow the caller's size bound
// returns nil rather than risking an overflow in the make([]byte, 0, N)
// capacity argument (CWE-190).
//
// The guard applies at the practical limit (MaxTopicMsgSize). Callers
// MUST treat nil as "drop" — Broadcast already does so. The test uses
// a payload of MaxTopicMsgSize bytes so the total would exceed the cap
// once the topic and uvarint are prepended.
func TestEncodeTopicMsg_OversizedPayload_ReturnsNil(t *testing.T) {
	topic := "oversized"
	payload := make([]byte, MaxTopicMsgSize)
	msg := encodeTopicMsg(topic, payload)
	if msg != nil {
		t.Errorf("encodeTopicMsg with %d-byte payload returned non-nil (len=%d); want nil for oversize drop", len(payload), len(msg))
	}
}

// TestEncodeTopicMsg_NormalSize_RoundTrips asserts the happy path is
// undisturbed by the size guard.
func TestEncodeTopicMsg_NormalSize_RoundTrips(t *testing.T) {
	topic := "hello"
	payload := []byte("world")
	msg := encodeTopicMsg(topic, payload)
	if msg == nil {
		t.Fatal("encodeTopicMsg returned nil for normal-size input")
	}
	gotTopic, gotPayload, ok := decodeTopicMsg(msg)
	if !ok {
		t.Fatal("decodeTopicMsg failed on valid encoded message")
	}
	if gotTopic != topic {
		t.Errorf("topic = %q, want %q", gotTopic, topic)
	}
	if string(gotPayload) != string(payload) {
		t.Errorf("payload = %q, want %q", gotPayload, payload)
	}
}
