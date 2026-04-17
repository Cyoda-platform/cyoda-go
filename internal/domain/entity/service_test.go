package entity_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	spi "github.com/cyoda-platform/cyoda-go-spi"
	"github.com/cyoda-platform/cyoda-go/internal/common"
	"github.com/cyoda-platform/cyoda-go/internal/domain/entity"
	"github.com/cyoda-platform/cyoda-go/plugins/memory"
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

// statsTestCtx returns a context with a UserContext for the given tenant.
func statsTestCtx(tenantID spi.TenantID) context.Context {
	return spi.WithUserContext(context.Background(), &spi.UserContext{
		UserID: "stats-user",
		Tenant: spi.Tenant{ID: tenantID, Name: string(tenantID)},
		Roles:  []string{"USER"},
	})
}

// TestGetStatisticsByState_UsesCountByState verifies the handler now drives
// state aggregation via EntityStore.CountByState (not GetAll-then-count) and
// honours the SPI dereference contract:
//   - nil-pointer states → no filter (all states returned)
//   - pointer to non-empty slice → only those states returned
//   - pointer to empty slice → empty result (per SPI: empty map, no storage call)
func TestGetStatisticsByState_UsesCountByState(t *testing.T) {
	factory := memory.NewStoreFactory()
	ctx := statsTestCtx("tenant-stats")
	h := entity.New(factory, nil, common.NewDefaultUUIDGenerator(), nil)

	mref := spi.ModelRef{EntityName: "stats-model", ModelVersion: "1"}

	// Register the model so GetStatisticsByState's modelStore.GetAll iteration
	// includes it.
	mstore, err := factory.ModelStore(ctx)
	if err != nil {
		t.Fatalf("ModelStore: %v", err)
	}
	if err := mstore.Save(ctx, &spi.ModelDescriptor{Ref: mref, State: spi.ModelLocked}); err != nil {
		t.Fatalf("ModelStore.Save: %v", err)
	}

	// Save 3 entities in two states: 2 NEW, 1 APPROVED.
	es, err := factory.EntityStore(ctx)
	if err != nil {
		t.Fatalf("EntityStore: %v", err)
	}
	for i, st := range []string{"NEW", "NEW", "APPROVED"} {
		_, err := es.Save(ctx, &spi.Entity{
			Meta: spi.EntityMeta{
				ID:       []string{"e1", "e2", "e3"}[i],
				TenantID: "tenant-stats",
				ModelRef: mref,
				State:    st,
			},
			Data: []byte(`{}`),
		})
		if err != nil {
			t.Fatalf("Save: %v", err)
		}
	}

	// nil-pointer filter → all states returned.
	stats, err := h.GetStatisticsByState(ctx, nil)
	if err != nil {
		t.Fatalf("GetStatisticsByState(nil): %v", err)
	}
	got := flattenStatsByState(stats)
	want := map[string]int64{"NEW": 2, "APPROVED": 1}
	if len(got) != len(want) {
		t.Fatalf("nil-filter: got %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("nil-filter: state %q got %d, want %d", k, got[k], v)
		}
	}

	// Pointer-to-non-empty: filter to APPROVED only.
	filter := []string{"APPROVED"}
	stats, err = h.GetStatisticsByState(ctx, &filter)
	if err != nil {
		t.Fatalf("GetStatisticsByState(&['APPROVED']): %v", err)
	}
	if len(stats) != 1 {
		t.Fatalf("approved-filter: expected 1 row, got %d (%v)", len(stats), stats)
	}
	if stats[0].State != "APPROVED" || stats[0].Count != 1 {
		t.Errorf("approved-filter: got %+v", stats[0])
	}

	// Pointer-to-empty-slice: per SPI, empty map → no rows.
	emptyFilter := []string{}
	stats, err = h.GetStatisticsByState(ctx, &emptyFilter)
	if err != nil {
		t.Fatalf("GetStatisticsByState(&[]): %v", err)
	}
	if len(stats) != 0 {
		t.Errorf("empty-filter: expected 0 rows, got %d (%v)", len(stats), stats)
	}
}

// TestGetStatisticsByStateForModel_UsesCountByState mirrors the above for the
// per-model variant.
func TestGetStatisticsByStateForModel_UsesCountByState(t *testing.T) {
	factory := memory.NewStoreFactory()
	ctx := statsTestCtx("tenant-stats-m")
	h := entity.New(factory, nil, common.NewDefaultUUIDGenerator(), nil)

	mref := spi.ModelRef{EntityName: "model-m", ModelVersion: "1"}

	es, err := factory.EntityStore(ctx)
	if err != nil {
		t.Fatalf("EntityStore: %v", err)
	}
	for i, st := range []string{"NEW", "NEW", "APPROVED", "REJECTED"} {
		_, err := es.Save(ctx, &spi.Entity{
			Meta: spi.EntityMeta{
				ID:       []string{"e1", "e2", "e3", "e4"}[i],
				TenantID: "tenant-stats-m",
				ModelRef: mref,
				State:    st,
			},
			Data: []byte(`{}`),
		})
		if err != nil {
			t.Fatalf("Save: %v", err)
		}
	}

	// nil filter → all three states.
	stats, err := h.GetStatisticsByStateForModel(ctx, "model-m", "1", nil)
	if err != nil {
		t.Fatalf("GetStatisticsByStateForModel(nil): %v", err)
	}
	got := flattenStatsByState(stats)
	want := map[string]int64{"NEW": 2, "APPROVED": 1, "REJECTED": 1}
	if len(got) != len(want) {
		t.Fatalf("nil-filter: got %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("nil-filter: state %q got %d, want %d", k, got[k], v)
		}
	}

	// Filter to two states.
	filter := []string{"NEW", "REJECTED"}
	stats, err = h.GetStatisticsByStateForModel(ctx, "model-m", "1", &filter)
	if err != nil {
		t.Fatalf("GetStatisticsByStateForModel(&['NEW','REJECTED']): %v", err)
	}
	got = flattenStatsByState(stats)
	want = map[string]int64{"NEW": 2, "REJECTED": 1}
	if len(got) != len(want) {
		t.Fatalf("filtered: got %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("filtered: state %q got %d, want %d", k, got[k], v)
		}
	}
	// Confirm APPROVED is NOT in the result.
	if _, ok := got["APPROVED"]; ok {
		t.Errorf("filtered: APPROVED must not appear in filtered result, got %v", got)
	}

	// Empty (non-nil) filter → no rows.
	emptyFilter := []string{}
	stats, err = h.GetStatisticsByStateForModel(ctx, "model-m", "1", &emptyFilter)
	if err != nil {
		t.Fatalf("GetStatisticsByStateForModel(&[]): %v", err)
	}
	if len(stats) != 0 {
		t.Errorf("empty-filter: expected 0 rows, got %d (%v)", len(stats), stats)
	}
}

func flattenStatsByState(stats []entity.EntityStatByState) map[string]int64 {
	out := make(map[string]int64, len(stats))
	for _, s := range stats {
		out[s.State] = s.Count
	}
	return out
}
