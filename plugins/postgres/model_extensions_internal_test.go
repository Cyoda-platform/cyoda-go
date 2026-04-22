package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	spi "github.com/cyoda-platform/cyoda-go-spi"
)

// pgFixture is the internal-package fixture for modelStore tests that
// need access to unexported methods (lastSavepointSeq, etc.) and to the
// concrete *modelStore value. Construction mirrors setupModelExtTest in
// the external test file but exposes the concrete types.
type pgFixture struct {
	factory  *StoreFactory
	pool     *pgxpool.Pool
	db       *pgxpool.Pool // alias of pool for test-readability per plan
	store    *modelStore
	ctx      context.Context
	tenantID spi.TenantID
}

func newPGFixture(t *testing.T) *pgFixture {
	t.Helper()
	dbURL := os.Getenv("CYODA_TEST_DB_URL")
	if dbURL == "" {
		t.Skip("CYODA_TEST_DB_URL not set — skipping PostgreSQL test")
	}
	poolCfg, err := pgxpool.ParseConfig(dbURL)
	if err != nil {
		t.Fatalf("parse pool config: %v", err)
	}
	poolCfg.MaxConns = 5
	poolCfg.MinConns = 0
	poolCfg.MaxConnIdleTime = 60 * time.Second
	poolCfg.HealthCheckPeriod = 24 * time.Hour

	pool, err := pgxpool.NewWithConfig(context.Background(), poolCfg)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}
	t.Cleanup(pool.Close)

	if err := dropSchema(pool); err != nil {
		t.Fatalf("reset schema: %v", err)
	}
	if err := Migrate(pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = dropSchema(pool) })

	factory := NewStoreFactory(pool)
	factory.SetApplyFunc(fixtureApplyFunc())

	tenantID := spi.TenantID("t1")
	uc := &spi.UserContext{
		UserID:   "test-user",
		UserName: "Test User",
		Tenant:   spi.Tenant{ID: tenantID, Name: string(tenantID)},
		Roles:    []string{"USER"},
	}
	ctx := spi.WithUserContext(context.Background(), uc)

	ms, err := factory.ModelStore(ctx)
	if err != nil {
		t.Fatalf("ModelStore: %v", err)
	}
	store, ok := ms.(*modelStore)
	if !ok {
		t.Fatalf("ModelStore did not return *modelStore; got %T", ms)
	}
	return &pgFixture{
		factory:  factory,
		pool:     pool,
		db:       pool,
		store:    store,
		ctx:      ctx,
		tenantID: tenantID,
	}
}

// SaveModel seeds the base model row for ref with the given base schema.
func (f *pgFixture) SaveModel(t *testing.T, ref spi.ModelRef, base []byte) {
	t.Helper()
	desc := &spi.ModelDescriptor{
		Ref:         ref,
		State:       spi.ModelUnlocked,
		ChangeLevel: spi.ChangeLevelStructural,
		Schema:      base,
		UpdateDate:  time.Now().UTC().Truncate(time.Millisecond),
	}
	if err := f.store.Save(f.ctx, desc); err != nil {
		t.Fatalf("Save: %v", err)
	}
}

// fixtureApplyFunc is the same recording apply function used by the
// external test file; duplicated here because it's needed for the
// internal-package fixture too.
func fixtureApplyFunc() ApplyFunc {
	return func(base []byte, delta spi.SchemaDelta) ([]byte, error) {
		var m map[string]json.RawMessage
		if len(base) == 0 {
			m = map[string]json.RawMessage{}
		} else if err := json.Unmarshal(base, &m); err != nil {
			m = map[string]json.RawMessage{"$$base": json.RawMessage(base)}
		}
		var applied []json.RawMessage
		if raw, ok := m["$$applied"]; ok {
			if err := json.Unmarshal(raw, &applied); err != nil {
				return nil, err
			}
		}
		applied = append(applied, json.RawMessage(delta))
		encoded, err := json.Marshal(applied)
		if err != nil {
			return nil, err
		}
		m["$$applied"] = encoded
		return json.Marshal(m)
	}
}

// TestLastSavepointSeq_NoSavepoint — new helper function
// lastSavepointSeq(ctx, ref) (int64, error) returns the seq of
// the most-recent savepoint row for ref, or 0 if none exists.
// Task 10 refactor uses this to drive the savepoint trigger.
func TestLastSavepointSeq_NoSavepoint(t *testing.T) {
	fx := newPGFixture(t)
	ref := spi.ModelRef{EntityName: "E", ModelVersion: "1"}
	fx.SaveModel(t, ref, []byte(`{"base":true}`))

	seq, err := fx.store.lastSavepointSeq(fx.ctx, ref)
	if err != nil {
		t.Fatalf("lastSavepointSeq: %v", err)
	}
	if seq != 0 {
		t.Errorf("lastSavepointSeq on empty log = %d, want 0", seq)
	}
}

func TestLastSavepointSeq_ReturnsMostRecent(t *testing.T) {
	fx := newPGFixture(t)
	ref := spi.ModelRef{EntityName: "E", ModelVersion: "1"}
	fx.SaveModel(t, ref, []byte(`{"base":true}`))
	// Drive enough deltas to trigger two savepoints (default interval=64;
	// savepoint rows themselves consume a seq from the shared BIGSERIAL,
	// so hardcoding expected seq numbers is fragile — compare against a
	// direct query of the most-recent savepoint seq instead).
	for i := 0; i < 128; i++ {
		if err := fx.store.ExtendSchema(fx.ctx, ref, spi.SchemaDelta(fmt.Sprintf(`[{"kind":"broaden_type","path":"d%d","payload":["NULL"]}]`, i))); err != nil {
			t.Fatalf("ExtendSchema %d: %v", i, err)
		}
	}

	var want int64
	if err := fx.db.QueryRow(fx.ctx, `
		SELECT seq FROM model_schema_extensions
		WHERE tenant_id=$1 AND model_name=$2 AND model_version=$3 AND kind='savepoint'
		ORDER BY seq DESC LIMIT 1`,
		string(fx.tenantID), ref.EntityName, ref.ModelVersion).Scan(&want); err != nil {
		t.Fatalf("direct SP query: %v", err)
	}
	if want == 0 {
		t.Fatalf("expected at least one savepoint row after 128 deltas (interval=64); saw none")
	}

	got, err := fx.store.lastSavepointSeq(fx.ctx, ref)
	if err != nil {
		t.Fatalf("lastSavepointSeq: %v", err)
	}
	if got != want {
		t.Errorf("lastSavepointSeq = %d, want %d (most-recent SP row per direct query)", got, want)
	}

	// Sanity: two savepoint rows after 128 deltas at interval=64.
	var spCount int
	if err := fx.db.QueryRow(fx.ctx, `
		SELECT COUNT(*) FROM model_schema_extensions
		WHERE tenant_id=$1 AND model_name=$2 AND model_version=$3 AND kind='savepoint'`,
		string(fx.tenantID), ref.EntityName, ref.ModelVersion).Scan(&spCount); err != nil {
		t.Fatalf("SP count: %v", err)
	}
	if spCount != 2 {
		t.Errorf("savepoint count after 128 deltas (interval=64) = %d, want 2", spCount)
	}
}
