package entity_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	spi "github.com/cyoda-platform/cyoda-go-spi"
	"github.com/cyoda-platform/cyoda-go/internal/common"
	"github.com/cyoda-platform/cyoda-go/internal/domain/entity"
)

// TestGetEntity_InfrastructureErrorReturns500 verifies that non-ErrNotFound errors
// from the entity store result in a 500 Internal Server Error, not a 404 (IM-04).
func TestGetEntity_InfrastructureErrorReturns500(t *testing.T) {
	srv := newTestServer(t)
	importAndLockModel(t, srv.URL, "Infra", 1, `{"name":"test"}`)

	// Create an entity first so we have a valid ID
	resp := doCreateEntity(t, srv.URL, "JSON", "Infra", 1, `{"name":"test"}`)
	expectStatus(t, resp, http.StatusOK)

	// Now test the service layer directly with a mock that returns infrastructure error
	handler := entity.New(
		&failingStoreFactory{err: errors.New("database connection lost")},
		nil,
		common.NewDefaultUUIDGenerator(),
		nil,
	)

	ctx := context.Background()
	uc := &spi.UserContext{
		UserID:   "test-user",
		UserName: "test",
		Tenant:   spi.Tenant{ID: "test-tenant", Name: "Test"},
		Roles:    []string{"user"},
	}
	ctx = spi.WithUserContext(ctx, uc)

	_, err := handler.GetEntity(ctx, entity.GetOneEntityInput{
		EntityID: "some-id",
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	var appErr *common.AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("expected *common.AppError, got %T", err)
	}

	// Infrastructure errors should be 500, not 404
	if appErr.Status != http.StatusInternalServerError {
		t.Errorf("expected status 500 for infrastructure error, got %d", appErr.Status)
	}
}

// TestGetEntity_NotFoundReturns404 verifies that ErrNotFound from the entity store
// results in a 404.
func TestGetEntity_NotFoundReturns404(t *testing.T) {
	handler := entity.New(
		&failingStoreFactory{err: spi.ErrNotFound},
		nil,
		common.NewDefaultUUIDGenerator(),
		nil,
	)

	ctx := context.Background()
	uc := &spi.UserContext{
		UserID:   "test-user",
		UserName: "test",
		Tenant:   spi.Tenant{ID: "test-tenant", Name: "Test"},
		Roles:    []string{"user"},
	}
	ctx = spi.WithUserContext(ctx, uc)

	_, err := handler.GetEntity(ctx, entity.GetOneEntityInput{
		EntityID: "nonexistent-id",
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	var appErr *common.AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("expected *common.AppError, got %T", err)
	}

	if appErr.Status != http.StatusNotFound {
		t.Errorf("expected status 404 for not-found error, got %d", appErr.Status)
	}
}
