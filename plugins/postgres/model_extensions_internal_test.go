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
	cfg      config
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
		cfg:      store.cfg,
	}
}

// reopenWithInterval rebuilds the modelStore on the same database with
// cfg.SchemaSavepointInterval overridden — simulates operator reconfig.
func (f *pgFixture) reopenWithInterval(t *testing.T, interval int) {
	t.Helper()
	newCfg := f.cfg
	newCfg.SchemaSavepointInterval = interval
	f.cfg = newCfg
	f.factory = newStoreFactoryWithConfig(f.pool, newCfg)
	f.factory.SetApplyFunc(fixtureApplyFunc())
	ms, err := f.factory.ModelStore(f.ctx)
	if err != nil {
		t.Fatalf("reopen ModelStore: %v", err)
	}
	store, ok := ms.(*modelStore)
	if !ok {
		t.Fatalf("reopen: ModelStore did not return *modelStore; got %T", ms)
	}
	f.store = store
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

// TestExtendSchema_SavepointTriggerRespectsIntervalChange — B-I4.
// Start with interval 64, commit past the first savepoint. Change
// interval to 128, commit more deltas. The next savepoint only fires
// once (newSeq - lastSavepointSeq) >= 128 — i.e., 128 deltas past the
// most recent savepoint, not at an arbitrary global-seq multiple.
//
// NOTE: seq values are not equal to delta-call counts because the
// shared BIGSERIAL is also consumed by savepoint rows themselves.
// Assertions here are framed in terms of "did a new savepoint land or
// not" rather than exact seq arithmetic, but we still cross-check the
// last-savepoint seq against a direct query.
func TestExtendSchema_SavepointTriggerRespectsIntervalChange(t *testing.T) {
	fx := newPGFixture(t)
	ref := spi.ModelRef{EntityName: "E", ModelVersion: "1"}
	fx.SaveModel(t, ref, []byte(`{"base":true}`))

	extend := func(tag string) {
		t.Helper()
		if err := fx.store.ExtendSchema(fx.ctx, ref,
			spi.SchemaDelta(fmt.Sprintf(`[{"kind":"broaden_type","path":"%s","payload":["NULL"]}]`, tag))); err != nil {
			t.Fatalf("ExtendSchema %s: %v", tag, err)
		}
	}
	spCount := func() int {
		t.Helper()
		var n int
		if err := fx.db.QueryRow(fx.ctx, `
			SELECT COUNT(*) FROM model_schema_extensions
			WHERE tenant_id=$1 AND model_name=$2 AND model_version=$3 AND kind='savepoint'`,
			string(fx.tenantID), ref.EntityName, ref.ModelVersion).Scan(&n); err != nil {
			t.Fatalf("SP count: %v", err)
		}
		return n
	}

	// Interval = 64 (default). 64 deltas → exactly one savepoint fires at
	// newSeq=64 (lastSP was 0, 64-0 >= 64).
	for i := 0; i < 64; i++ {
		extend(fmt.Sprintf("d%d", i))
	}
	if got := spCount(); got != 1 {
		t.Fatalf("after 64 deltas with interval=64, savepoint count = %d, want 1", got)
	}
	lastSPBefore, err := fx.store.lastSavepointSeq(fx.ctx, ref)
	if err != nil {
		t.Fatalf("lastSavepointSeq: %v", err)
	}
	if lastSPBefore == 0 {
		t.Fatalf("expected a savepoint row after 64 deltas; lastSavepointSeq = 0")
	}

	// Reconfigure to interval=128. Commit 127 more deltas.
	// Under the new "since last savepoint" rule, no new savepoint fires
	// until (newSeq - lastSPBefore) >= 128. A delta-call doesn't increment
	// newSeq by 1 reliably (the savepoint row above consumed one seq), but
	// after 64 deltas + 1 savepoint, the next delta's newSeq is ~66. We
	// need newSeq >= lastSPBefore + 128 to see a new savepoint. 127 more
	// deltas cannot reach that distance: the max achievable newSeq is
	// (lastSPBefore + 127 deltas) which is lastSPBefore + 127 < lastSPBefore + 128.
	fx.reopenWithInterval(t, 128)
	for i := 0; i < 127; i++ {
		extend(fmt.Sprintf("d64-%d", i))
	}
	if got := spCount(); got != 1 {
		t.Errorf("interval=128, 127 deltas since last SP: savepoint count = %d, want still 1", got)
	}

	// One more delta — crosses the interval boundary.
	// After 128 deltas since the last savepoint, newSeq - lastSPBefore >= 128
	// is satisfied (exactly =128 after accounting for how many extra seqs
	// intervening savepoints consumed — here none since no new SP has
	// fired yet post-reconfig).
	extend("d128-trigger")
	if got := spCount(); got != 2 {
		t.Errorf("after 128 deltas since last SP with interval=128: savepoint count = %d, want 2", got)
	}
	lastSPAfter, err := fx.store.lastSavepointSeq(fx.ctx, ref)
	if err != nil {
		t.Fatalf("lastSavepointSeq: %v", err)
	}
	if lastSPAfter <= lastSPBefore {
		t.Errorf("expected lastSavepointSeq to advance: before=%d after=%d", lastSPBefore, lastSPAfter)
	}
	// The new savepoint must satisfy the trigger rule: it fires when the
	// triggering delta's newSeq is at least lastSPBefore + interval(128).
	// The SP row itself takes the next seq after that delta, so:
	//   lastSPAfter == triggering_delta_seq + 1, where triggering_delta_seq >= lastSPBefore + 128.
	if lastSPAfter < lastSPBefore+128 {
		t.Errorf("new savepoint seq %d < lastSPBefore(%d)+128; trigger fired too early", lastSPAfter, lastSPBefore)
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
