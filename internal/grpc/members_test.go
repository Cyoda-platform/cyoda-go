package grpc

import (
	"testing"
	"time"

	spi "github.com/cyoda-platform/cyoda-go-spi"
	cepb "github.com/cyoda-platform/cyoda-go/api/grpc/cloudevents"
)

func noopSend(_ *cepb.CloudEvent) error { return nil }

func TestMemberRegistry_RegisterAndList(t *testing.T) {
	reg := NewMemberRegistry()
	tenant := spi.TenantID("tenant-1")
	tags := []string{"python", "default"}

	id := reg.Register(tenant, tags, noopSend)

	members := reg.List()
	if len(members) != 1 {
		t.Fatalf("expected 1 member, got %d", len(members))
	}
	m := members[0]
	if m.ID != id {
		t.Errorf("expected ID %s, got %s", id, m.ID)
	}
	if m.TenantID != tenant {
		t.Errorf("expected tenant %s, got %s", tenant, m.TenantID)
	}
	if len(m.Tags) != 2 || m.Tags[0] != "python" || m.Tags[1] != "default" {
		t.Errorf("unexpected tags: %v", m.Tags)
	}
	if m.ConnectedAt.IsZero() {
		t.Error("ConnectedAt should not be zero")
	}
}

func TestMemberRegistry_RegisterAndUnregister(t *testing.T) {
	reg := NewMemberRegistry()
	id := reg.Register("tenant-1", []string{"a"}, noopSend)

	reg.Unregister(id)

	if len(reg.List()) != 0 {
		t.Fatal("expected 0 members after unregister")
	}
}

func TestMemberRegistry_FindByTags_MatchingTag(t *testing.T) {
	reg := NewMemberRegistry()
	reg.Register("tenant-1", []string{"python", "ml"}, noopSend)

	m := reg.FindByTags("tenant-1", "ml")
	if m == nil {
		t.Fatal("expected to find member with matching tag")
	}
}

func TestMemberRegistry_FindByTags_NoMatchingTag(t *testing.T) {
	reg := NewMemberRegistry()
	reg.Register("tenant-1", []string{"python", "ml"}, noopSend)

	m := reg.FindByTags("tenant-1", "java")
	if m != nil {
		t.Fatal("expected nil when no tag matches")
	}
}

func TestMemberRegistry_FindByTags_EmptyRequired(t *testing.T) {
	reg := NewMemberRegistry()
	reg.Register("tenant-1", []string{"python"}, noopSend)

	m := reg.FindByTags("tenant-1", "")
	if m == nil {
		t.Fatal("expected to find any member when required tags are empty")
	}
}

func TestMemberRegistry_FindByTags_WrongTenant(t *testing.T) {
	reg := NewMemberRegistry()
	reg.Register("tenant-1", []string{"python"}, noopSend)

	m := reg.FindByTags("tenant-2", "python")
	if m != nil {
		t.Fatal("expected nil for wrong tenant")
	}
}

func TestMember_TrackAndCompleteRequest(t *testing.T) {
	reg := NewMemberRegistry()
	id := reg.Register("tenant-1", []string{"a"}, noopSend)
	m := reg.Get(id)

	ch := m.TrackRequest("req-1")

	go func() {
		m.CompleteRequest("req-1", &ProcessingResponse{
			Success: true,
			Payload: []byte(`{"result":"ok"}`),
		})
	}()

	select {
	case resp := <-ch:
		if resp == nil {
			t.Fatal("expected non-nil response")
		}
		if !resp.Success {
			t.Error("expected success=true")
		}
		if string(resp.Payload) != `{"result":"ok"}` {
			t.Errorf("unexpected payload: %s", resp.Payload)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for response")
	}
}

func TestMemberRegistry_UnregisterFailsPending(t *testing.T) {
	reg := NewMemberRegistry()
	id := reg.Register("tenant-1", []string{"a"}, noopSend)
	m := reg.Get(id)

	ch := m.TrackRequest("req-1")

	reg.Unregister(id)

	select {
	case resp := <-ch:
		if resp == nil {
			t.Fatal("expected non-nil error response")
		}
		if resp.Success {
			t.Error("expected success=false for failed pending")
		}
		if resp.Error == "" {
			t.Error("expected non-empty error message")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for error response on pending channel")
	}
}

func TestMemberRegistry_GetExisting(t *testing.T) {
	reg := NewMemberRegistry()
	id := reg.Register("tenant-1", []string{"a"}, noopSend)

	m := reg.Get(id)
	if m == nil {
		t.Fatal("expected non-nil member")
	}
	if m.ID != id {
		t.Errorf("expected ID %s, got %s", id, m.ID)
	}
}

func TestMemberRegistry_GetNonExistent(t *testing.T) {
	reg := NewMemberRegistry()

	m := reg.Get("does-not-exist")
	if m != nil {
		t.Fatal("expected nil for non-existent member")
	}
}
