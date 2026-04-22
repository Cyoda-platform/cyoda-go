package sqlite

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"testing"
	"time"

	spi "github.com/cyoda-platform/cyoda-go-spi"
)

// setUnionApplyFunc is an associative-commutative-idempotent apply used
// by Sub-project B tests. The "schema" representation is a JSON array of
// unique string tokens, an ExtendSchema delta is a JSON-encoded string
// token (e.g. `"d00"`), and fold = sorted union.
//
// Copied verbatim from plugins/postgres/model_extensions_internal_test.go
// so the sqlite fold tests remain self-contained.
func setUnionApplyFunc(base []byte, delta spi.SchemaDelta) ([]byte, error) {
	m := map[string]struct{}{}
	if len(base) > 0 {
		var existing []string
		if err := json.Unmarshal(base, &existing); err != nil {
			return nil, fmt.Errorf("setUnionApplyFunc: decode base %q: %w", base, err)
		}
		for _, tok := range existing {
			m[tok] = struct{}{}
		}
	}
	var tok string
	if err := json.Unmarshal([]byte(delta), &tok); err != nil {
		return nil, fmt.Errorf("setUnionApplyFunc: decode delta %q: %w", delta, err)
	}
	m[tok] = struct{}{}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return json.Marshal(keys)
}

// sqliteFixture is the internal-package fixture for modelStore tests
// that need access to unexported methods (foldLocked, lastSavepointSeq,
// etc.) and to the concrete *modelStore value. Mirrors pgFixture in
// plugins/postgres/model_extensions_internal_test.go.
//
// Tests interact with the underlying *sql.DB via fx.store.db.ExecContext
// (the sqlite-correct form) rather than the pgx-style Exec(ctx, ...)
// used in the postgres fixture.
type sqliteFixture struct {
	factory  *StoreFactory
	store    *modelStore
	ctx      context.Context
	tenantID spi.TenantID
}

func newSQLiteFixture(t *testing.T) *sqliteFixture {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	f, err := NewStoreFactoryForTest(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("NewStoreFactoryForTest: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })
	f.SetApplyFunc(fixtureApplyFunc())

	tenantID := spi.TenantID("t1")
	uc := &spi.UserContext{
		UserID:   "test-user",
		UserName: "Test User",
		Tenant:   spi.Tenant{ID: tenantID, Name: string(tenantID)},
		Roles:    []string{"USER"},
	}
	ctx := spi.WithUserContext(context.Background(), uc)

	ms, err := f.ModelStore(ctx)
	if err != nil {
		t.Fatalf("ModelStore: %v", err)
	}
	store, ok := ms.(*modelStore)
	if !ok {
		t.Fatalf("ModelStore did not return *modelStore; got %T", ms)
	}
	return &sqliteFixture{
		factory:  f,
		store:    store,
		ctx:      ctx,
		tenantID: tenantID,
	}
}

// SaveModel seeds the base model row for ref with the given base schema.
func (fx *sqliteFixture) SaveModel(t *testing.T, ref spi.ModelRef, base []byte) {
	t.Helper()
	desc := &spi.ModelDescriptor{
		Ref:         ref,
		State:       spi.ModelUnlocked,
		ChangeLevel: spi.ChangeLevelStructural,
		Schema:      base,
		UpdateDate:  time.Now().UTC().Truncate(time.Millisecond),
	}
	if err := fx.store.Save(fx.ctx, desc); err != nil {
		t.Fatalf("Save: %v", err)
	}
}

// fixtureApplyFunc is the default recording apply function for fixtures.
// It nests each delta under `$$applied` so observers can detect whether
// apply was invoked. Mirrors plugins/postgres's fixture helper.
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

// TestSQLite_foldLocked_NoDeltas_ReturnsBase — sanity check for the
// new fold path. If no extension rows exist, fold returns the base
// schema verbatim (applyFunc not required).
func TestSQLite_foldLocked_NoDeltas_ReturnsBase(t *testing.T) {
	fx := newSQLiteFixture(t)
	ref := spi.ModelRef{EntityName: "E", ModelVersion: "1"}
	fx.SaveModel(t, ref, []byte(`{"base":true}`))

	got, err := fx.store.foldLocked(fx.ctx, ref, []byte(`{"base":true}`))
	if err != nil {
		t.Fatalf("foldLocked: %v", err)
	}
	if !bytes.Equal(got, []byte(`{"base":true}`)) {
		t.Errorf("foldLocked (no deltas) = %q, want base %q", got, `{"base":true}`)
	}
}

// TestSQLite_foldLocked_MultipleDeltas_AppliesInOrder — fold returns
// the forward-applied accumulation of delta payloads in seq order.
func TestSQLite_foldLocked_MultipleDeltas_AppliesInOrder(t *testing.T) {
	fx := newSQLiteFixture(t)
	fx.store.applyFunc = setUnionApplyFunc
	ref := spi.ModelRef{EntityName: "E", ModelVersion: "1"}
	fx.SaveModel(t, ref, []byte{})
	// Insert three delta rows directly (bypassing ExtendSchema to isolate the fold test).
	for i, d := range []string{`"d01"`, `"d02"`, `"d03"`} {
		if _, err := fx.store.db.ExecContext(fx.ctx,
			`INSERT INTO model_schema_extensions (tenant_id, model_name, model_version, seq, kind, payload, tx_id)
			 VALUES (?, ?, ?, ?, 'delta', ?, '')`,
			string(fx.tenantID), ref.EntityName, ref.ModelVersion, i+1, []byte(d)); err != nil {
			t.Fatalf("insert delta %d: %v", i, err)
		}
	}

	got, err := fx.store.foldLocked(fx.ctx, ref, []byte{})
	if err != nil {
		t.Fatalf("foldLocked: %v", err)
	}
	expected, _ := setUnionApplyFunc([]byte{}, spi.SchemaDelta(`"d01"`))
	expected, _ = setUnionApplyFunc(expected, spi.SchemaDelta(`"d02"`))
	expected, _ = setUnionApplyFunc(expected, spi.SchemaDelta(`"d03"`))
	if !bytes.Equal(got, expected) {
		t.Errorf("foldLocked = %q, want %q", got, expected)
	}
}
