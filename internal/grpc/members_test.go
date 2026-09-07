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

	registered := reg.Register("m-1", tenant, tags, noopSend, nil)

	members := reg.List()
	if len(members) != 1 {
		t.Fatalf("expected 1 member, got %d", len(members))
	}
	m := members[0]
	if m.ID != registered.ID {
		t.Errorf("expected ID %s, got %s", registered.ID, m.ID)
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
	m := reg.Register("m-1", "tenant-1", []string{"a"}, noopSend, nil)

	reg.Unregister(m.ID)

	if len(reg.List()) != 0 {
		t.Fatal("expected 0 members after unregister")
	}
}

func TestMemberRegistry_FindByTags_MatchingTag(t *testing.T) {
	reg := NewMemberRegistry()
	reg.Register("m-1", "tenant-1", []string{"python", "ml"}, noopSend, nil)

	m := reg.FindByTags("tenant-1", "ml")
	if m == nil {
		t.Fatal("expected to find member with matching tag")
	}
}

func TestMemberRegistry_FindByTags_NoMatchingTag(t *testing.T) {
	reg := NewMemberRegistry()
	reg.Register("m-1", "tenant-1", []string{"python", "ml"}, noopSend, nil)

	m := reg.FindByTags("tenant-1", "java")
	if m != nil {
		t.Fatal("expected nil when no tag matches")
	}
}

func TestMemberRegistry_FindByTags_EmptyRequired(t *testing.T) {
	reg := NewMemberRegistry()
	reg.Register("m-1", "tenant-1", []string{"python"}, noopSend, nil)

	m := reg.FindByTags("tenant-1", "")
	if m == nil {
		t.Fatal("expected to find any member when required tags are empty")
	}
}

func TestMemberRegistry_FindByTags_WrongTenant(t *testing.T) {
	reg := NewMemberRegistry()
	reg.Register("m-1", "tenant-1", []string{"python"}, noopSend, nil)

	m := reg.FindByTags("tenant-2", "python")
	if m != nil {
		t.Fatal("expected nil for wrong tenant")
	}
}

func TestMember_TrackAndCompleteRequest(t *testing.T) {
	reg := NewMemberRegistry()
	m := reg.Register("m-1", "tenant-1", []string{"a"}, noopSend, nil)

	ch, err := m.TrackRequest("req-1")
	if err != nil {
		t.Fatalf("TrackRequest: %v", err)
	}

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
	m := reg.Register("m-1", "tenant-1", []string{"a"}, noopSend, nil)

	ch, err := m.TrackRequest("req-1")
	if err != nil {
		t.Fatalf("TrackRequest: %v", err)
	}

	reg.Unregister(m.ID)

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
	registered := reg.Register("m-1", "tenant-1", []string{"a"}, noopSend, nil)

	m := reg.Get(registered.ID)
	if m == nil {
		t.Fatal("expected non-nil member")
	}
	if m.ID != registered.ID {
		t.Errorf("expected ID %s, got %s", registered.ID, m.ID)
	}
}

func TestMemberRegistry_GetNonExistent(t *testing.T) {
	reg := NewMemberRegistry()

	m := reg.Get("does-not-exist")
	if m != nil {
		t.Fatal("expected nil for non-existent member")
	}
}
