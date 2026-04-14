package common_test

import (
	"context"
	"testing"

	"github.com/cyoda-platform/cyoda-go/internal/common"
)

func TestTenantIDIsNamedType(t *testing.T) {
	var tid common.TenantID = "test-tenant"
	if tid == "" {
		t.Fatal("expected non-empty tenant ID")
	}
}

func TestSystemTenantIDConstant(t *testing.T) {
	if common.SystemTenantID == "" {
		t.Fatal("expected SystemTenantID to be defined")
	}
	if common.SystemTenantID != "SYSTEM" {
		t.Errorf("expected SYSTEM, got %s", common.SystemTenantID)
	}
}

func TestUserContextCarriesTenant(t *testing.T) {
	tenant := common.Tenant{ID: "tenant-A", Name: "Tenant A"}
	uc := &common.UserContext{
		UserID: "user-1",
		Tenant: tenant,
		Roles:  []string{"USER"},
	}
	ctx := common.WithUserContext(context.Background(), uc)
	got := common.MustGetUserContext(ctx)
	if got.Tenant.ID != "tenant-A" {
		t.Errorf("expected tenant-A, got %s", got.Tenant.ID)
	}
	if got.Tenant.Name != "Tenant A" {
		t.Errorf("expected Tenant A, got %s", got.Tenant.Name)
	}
}
