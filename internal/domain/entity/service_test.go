package entity_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
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

// decodeJSONResponseUseNumber decodes an HTTP response body using
// json.Decoder.UseNumber() so numeric leaves arrive as json.Number and
// the test can assert exact literal preservation. Mirrors the
// production helper entity.decodeJSONPreservingNumbers (unexported).
// Keep the two in sync when changing numeric-decode policy.
func decodeJSONResponseUseNumber(t *testing.T, body []byte, v any) {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(v); err != nil {
		t.Fatalf("failed to decode response with UseNumber: %v", err)
	}
}

// TestCreateEntity_PreservesLargeIntPrecision verifies that a JSON entity
// payload containing an integer larger than 2^53 round-trips through the
// CreateEntity path without precision loss. Bare json.Unmarshal would
// decode such an int into a float64 and round it; the precision-preserving
// path must keep the literal exactly.
func TestCreateEntity_PreservesLargeIntPrecision(t *testing.T) {
	srv := newTestServer(t)
	// Updated for A.1: seed the schema with a 2^53+1 literal so the id
	// leaf classifies as LONG. Post-A.1 the classifier no longer widens
	// LONG values into an INTEGER schema; the sample fixture must advertise
	// the LONG family upfront for this precision test to reach the service
	// layer at all.
	importAndLockModel(t, srv.URL, "PrecisionCreate", 1, `{"id":9007199254740993,"name":"x"}`)

	// 9007199254740993 == 2^53 + 1, the smallest positive integer that is
	// not exactly representable as a float64.
	const bigIDLiteral = "9007199254740993"
	payload := `{"id":` + bigIDLiteral + `,"name":"big"}`

	entityID := createEntityAndGetID(t, srv.URL, "PrecisionCreate", 1, payload)

	resp := doGetEntity(t, srv.URL, entityID)
	expectStatus(t, resp, http.StatusOK)
	body := readBody(t, resp)

	var envelope map[string]any
	decodeJSONResponseUseNumber(t, body, &envelope)

	data, ok := envelope["data"].(map[string]any)
	if !ok {
		t.Fatalf("expected data to be an object, got %T", envelope["data"])
	}
	idVal := data["id"]
	num, ok := idVal.(json.Number)
	if !ok {
		t.Fatalf("expected id to decode as json.Number, got %T (value=%v)", idVal, idVal)
	}
	if string(num) != bigIDLiteral {
		t.Fatalf("precision lost: expected id=%q, got %q", bigIDLiteral, string(num))
	}
	gotInt, err := num.Int64()
	if err != nil {
		t.Fatalf("Int64() failed on preserved literal: %v", err)
	}
	if gotInt != int64(9007199254740993) {
		t.Fatalf("expected int64=9007199254740993, got %d", gotInt)
	}
}

// TestUpdateEntity_PreservesLargeIntPrecision verifies the same precision
// preservation through the UpdateEntity HTTP path (service.go :781).
func TestUpdateEntity_PreservesLargeIntPrecision(t *testing.T) {
	srv := newTestServer(t)
	// Updated for A.1: seed with a LONG-family literal so the schema
	// accepts the >2^53 update payload. See TestCreateEntity_PreservesLargeIntPrecision.
	importAndLockModel(t, srv.URL, "PrecisionUpdate", 1, `{"id":9007199254740993,"name":"x"}`)

	// Create with a small id, then update with a >2^53 id.
	entityID := createEntityAndGetID(t, srv.URL, "PrecisionUpdate", 1, `{"id":1,"name":"orig"}`)

	const bigIDLiteral = "9007199254740993"
	updateBody := `{"id":` + bigIDLiteral + `,"name":"big"}`
	resp := doUpdateEntity(t, srv.URL, "JSON", entityID, "UPDATE", updateBody)
	expectStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	resp = doGetEntity(t, srv.URL, entityID)
	expectStatus(t, resp, http.StatusOK)
	body := readBody(t, resp)

	var envelope map[string]any
	decodeJSONResponseUseNumber(t, body, &envelope)

	data, ok := envelope["data"].(map[string]any)
	if !ok {
		t.Fatalf("expected data to be an object, got %T", envelope["data"])
	}
	idVal := data["id"]
	num, ok := idVal.(json.Number)
	if !ok {
		t.Fatalf("expected id to decode as json.Number, got %T (value=%v)", idVal, idVal)
	}
	if string(num) != bigIDLiteral {
		t.Fatalf("precision lost on update: expected id=%q, got %q", bigIDLiteral, string(num))
	}
}

// TestCollectionCreate_PreservesLargeIntPrecision verifies that the
// dedicated collection-create path (POST /entity/{format} with an array
// of {model, payload} items) preserves integer literals >2^53 exactly.
// Spec Section 6.4: this exercises the CreateEntityCollection JSON-array
// parsing path (service.go decodeJSONPreservingNumbers call), which is
// distinct from the single-create path covered by
// TestCreateEntity_PreservesLargeIntPrecision.
func TestCollectionCreate_PreservesLargeIntPrecision(t *testing.T) {
	srv := newTestServer(t)
	// Updated for A.1: seed with a LONG-family literal so the schema
	// accepts the >2^53 payload. See TestCreateEntity_PreservesLargeIntPrecision.
	importAndLockModel(t, srv.URL, "PrecisionCollection", 1, `{"id":9007199254740993,"name":"x"}`)

	// 9007199254740993 == 2^53 + 1, the smallest positive integer that is
	// not exactly representable as a float64. The first item carries the
	// precision-witness id; the second item is a plain small-int control.
	const bigIDLiteral = "9007199254740993"
	body := `[
		{"model":{"name":"PrecisionCollection","version":1},"payload":"{\"id\":` + bigIDLiteral + `,\"name\":\"big\"}"},
		{"model":{"name":"PrecisionCollection","version":1},"payload":"{\"id\":2,\"name\":\"small\"}"}
	]`

	resp, err := http.Post(srv.URL+"/entity/JSON", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("create collection request failed: %v", err)
	}
	expectStatus(t, resp, http.StatusOK)
	respBody := readBody(t, resp)

	var results []map[string]any
	if err := json.Unmarshal(respBody, &results); err != nil {
		t.Fatalf("failed to parse create collection response: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result envelope, got %d", len(results))
	}
	entityIDs, ok := results[0]["entityIds"].([]any)
	if !ok || len(entityIDs) != 2 {
		t.Fatalf("expected 2 entity IDs, got %v", results[0]["entityIds"])
	}
	bigEntityID, ok := entityIDs[0].(string)
	if !ok || bigEntityID == "" {
		t.Fatalf("expected non-empty entityId for big-id item, got %v", entityIDs[0])
	}

	// Read back the first item and assert exact literal preservation.
	getResp := doGetEntity(t, srv.URL, bigEntityID)
	expectStatus(t, getResp, http.StatusOK)
	getBody := readBody(t, getResp)

	var envelope map[string]any
	decodeJSONResponseUseNumber(t, getBody, &envelope)

	data, ok := envelope["data"].(map[string]any)
	if !ok {
		t.Fatalf("expected data to be an object, got %T", envelope["data"])
	}
	idVal := data["id"]
	num, ok := idVal.(json.Number)
	if !ok {
		t.Fatalf("expected id to decode as json.Number, got %T (value=%v)", idVal, idVal)
	}
	if string(num) != bigIDLiteral {
		t.Fatalf("precision lost in collection-create: expected id=%q, got %q", bigIDLiteral, string(num))
	}
	gotInt, err := num.Int64()
	if err != nil {
		t.Fatalf("Int64() failed on preserved literal: %v", err)
	}
	if gotInt != int64(9007199254740993) {
		t.Fatalf("expected int64=9007199254740993, got %d", gotInt)
	}
}

// TestDeleteAllEntities_EmptyModel_ReturnsZeroCount verifies that
// DeleteAllEntities on a model with no entities returns a success result
// with TotalCount=0 rather than a 404. Idempotent delete-before-recreate
// smoke flows in multi-node clusters depend on this behavior.
func TestDeleteAllEntities_EmptyModel_ReturnsZeroCount(t *testing.T) {
	factory := memory.NewStoreFactory()
	ctx := statsTestCtx("tenant-delete-empty")

	txMgr, err := factory.TransactionManager(ctx)
	if err != nil {
		t.Fatalf("TransactionManager: %v", err)
	}
	h := entity.New(factory, txMgr, common.NewDefaultUUIDGenerator(), nil)

	// Register a LOCKED model with zero entities.
	mref := spi.ModelRef{EntityName: "EmptyModel", ModelVersion: "1"}
	mstore, err := factory.ModelStore(ctx)
	if err != nil {
		t.Fatalf("ModelStore: %v", err)
	}
	if err := mstore.Save(ctx, &spi.ModelDescriptor{Ref: mref, State: spi.ModelLocked}); err != nil {
		t.Fatalf("ModelStore.Save: %v", err)
	}

	result, err := h.DeleteAllEntities(ctx, "EmptyModel", "1")
	if err != nil {
		t.Fatalf("DeleteAllEntities on empty model: expected no error, got %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.TotalCount != 0 {
		t.Errorf("expected TotalCount=0, got %d", result.TotalCount)
	}
	if result.ModelID == "" {
		t.Error("expected ModelID to be populated")
	}
	if result.EntityModelID == "" {
		t.Error("expected EntityModelID to be populated")
	}
}
