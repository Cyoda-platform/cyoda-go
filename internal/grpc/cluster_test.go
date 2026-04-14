package grpc

import (
	"context"
	"encoding/json"
	"testing"

	cepb "github.com/cyoda-platform/cyoda-go/api/grpc/cloudevents"
	"github.com/cyoda-platform/cyoda-go/internal/common"
)

func TestClusterService_ListEmpty(t *testing.T) {
	registry := NewMemberRegistry()
	svc := NewClusterService(registry)

	data, err := svc.ListCalculationMembers(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var items []memberInfo
	if err := json.Unmarshal(data, &items); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected empty list, got %d items", len(items))
	}
}

func TestClusterService_ListWithMember(t *testing.T) {
	registry := NewMemberRegistry()
	registry.Register(common.TenantID("tenant-1"), []string{"python"}, func(ce *cepb.CloudEvent) error { return nil })

	svc := NewClusterService(registry)
	data, err := svc.ListCalculationMembers(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var items []memberInfo
	if err := json.Unmarshal(data, &items); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 member, got %d", len(items))
	}
	if items[0].TenantID != "tenant-1" {
		t.Errorf("expected tenantId=tenant-1, got %s", items[0].TenantID)
	}
	if len(items[0].Tags) != 1 || items[0].Tags[0] != "python" {
		t.Errorf("expected tags=[python], got %v", items[0].Tags)
	}
	if items[0].Status != "online" {
		t.Errorf("expected status=online, got %s", items[0].Status)
	}
}

func TestClusterService_GetMember(t *testing.T) {
	registry := NewMemberRegistry()
	memberID := registry.Register(common.TenantID("tenant-2"), []string{"java"}, func(ce *cepb.CloudEvent) error { return nil })

	svc := NewClusterService(registry)
	data, err := svc.GetCalculationMember(context.Background(), memberID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var info memberInfo
	if err := json.Unmarshal(data, &info); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if info.MemberID != memberID {
		t.Errorf("expected memberId=%s, got %s", memberID, info.MemberID)
	}
	if info.TenantID != "tenant-2" {
		t.Errorf("expected tenantId=tenant-2, got %s", info.TenantID)
	}
	if len(info.Tags) != 1 || info.Tags[0] != "java" {
		t.Errorf("expected tags=[java], got %v", info.Tags)
	}
}

func TestClusterService_GetMemberNotFound(t *testing.T) {
	registry := NewMemberRegistry()
	svc := NewClusterService(registry)

	_, err := svc.GetCalculationMember(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent member")
	}
}

func TestClusterService_Summary(t *testing.T) {
	registry := NewMemberRegistry()
	noop := func(ce *cepb.CloudEvent) error { return nil }
	registry.Register(common.TenantID("tenant-a"), []string{"python"}, noop)
	registry.Register(common.TenantID("tenant-a"), []string{"java"}, noop)
	registry.Register(common.TenantID("tenant-b"), []string{"python"}, noop)

	svc := NewClusterService(registry)
	data, err := svc.GetSummary(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var summary map[string]any
	if err := json.Unmarshal(data, &summary); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	total, ok := summary["totalMembers"].(float64)
	if !ok || int(total) != 3 {
		t.Errorf("expected totalMembers=3, got %v", summary["totalMembers"])
	}
	online, ok := summary["onlineMembers"].(float64)
	if !ok || int(online) != 3 {
		t.Errorf("expected onlineMembers=3, got %v", summary["onlineMembers"])
	}

	byTenant, ok := summary["byTenant"].(map[string]any)
	if !ok {
		t.Fatalf("expected byTenant to be a map, got %T", summary["byTenant"])
	}
	if tenantA, ok := byTenant["tenant-a"].(float64); !ok || int(tenantA) != 2 {
		t.Errorf("expected byTenant[tenant-a]=2, got %v", byTenant["tenant-a"])
	}
	if tenantB, ok := byTenant["tenant-b"].(float64); !ok || int(tenantB) != 1 {
		t.Errorf("expected byTenant[tenant-b]=1, got %v", byTenant["tenant-b"])
	}
}
