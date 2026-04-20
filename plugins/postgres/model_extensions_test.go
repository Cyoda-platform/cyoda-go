package postgres_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	spi "github.com/cyoda-platform/cyoda-go-spi"
	"github.com/cyoda-platform/cyoda-go/plugins/postgres"
)

// jsonRecordingApplyFunc returns a test ApplyFunc whose output is
// always valid JSON (so it can be persisted into the payload JSONB
// column as a savepoint) and whose output reflects both the base and
// the delta, so callers can assert Apply actually ran. It embeds the
// delta bytes under a dedicated "$$applied" array on the base object —
// deep-merging is outside the scope of these plugin tests.
func jsonRecordingApplyFunc() postgres.ApplyFunc {
	return func(base []byte, delta spi.SchemaDelta) ([]byte, error) {
		var m map[string]json.RawMessage
		if len(base) == 0 {
			m = map[string]json.RawMessage{}
		} else if err := json.Unmarshal(base, &m); err != nil {
			// Base isn't an object — wrap under "$$base".
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

func setupModelExtTest(t *testing.T) *postgres.StoreFactory {
	t.Helper()
	pool := newTestPool(t)
	if err := postgres.DropSchemaForTest(pool); err != nil {
		t.Fatalf("reset schema: %v", err)
	}
	if err := postgres.Migrate(pool); err != nil {
		t.Fatalf("migration failed: %v", err)
	}
	t.Cleanup(func() { _ = postgres.DropSchemaForTest(pool) })
	return postgres.NewStoreFactory(pool)
}

func seedBaseModel(t *testing.T, ms spi.ModelStore, ctx context.Context, ref spi.ModelRef, schemaJSON string, cl spi.ChangeLevel) {
	t.Helper()
	desc := &spi.ModelDescriptor{
		Ref:         ref,
		State:       spi.ModelUnlocked,
		ChangeLevel: cl,
		Schema:      []byte(schemaJSON),
		UpdateDate:  time.Now().UTC().Truncate(time.Millisecond),
	}
	if err := ms.Save(ctx, desc); err != nil {
		t.Fatalf("Save: %v", err)
	}
}

func TestExtendSchema_AppendAndFold(t *testing.T) {
	factory := setupModelExtTest(t)
	factory.SetApplyFunc(jsonRecordingApplyFunc())

	ctx := ctxWithTenant("t1")
	ms, err := factory.ModelStore(ctx)
	if err != nil {
		t.Fatalf("ModelStore: %v", err)
	}
	ref := spi.ModelRef{EntityName: "Book", ModelVersion: "1"}
	seedBaseModel(t, ms, ctx, ref, `{"name":"title"}`, spi.ChangeLevelStructural)

	delta := spi.SchemaDelta(`[{"kind":"add_property","path":"","name":"isbn","payload":{}}]`)
	if err := ms.ExtendSchema(ctx, ref, delta); err != nil {
		t.Fatalf("ExtendSchema: %v", err)
	}

	desc, err := ms.Get(ctx, ref)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !bytes.Contains(desc.Schema, []byte("isbn")) {
		t.Errorf("expected folded schema to contain delta bytes; got %s", desc.Schema)
	}
}

func TestExtendSchema_EmptyDeltaIsNoop(t *testing.T) {
	factory := setupModelExtTest(t)
	factory.SetApplyFunc(jsonRecordingApplyFunc())

	ctx := ctxWithTenant("t1")
	ms, err := factory.ModelStore(ctx)
	if err != nil {
		t.Fatalf("ModelStore: %v", err)
	}
	ref := spi.ModelRef{EntityName: "Book", ModelVersion: "1"}
	seedBaseModel(t, ms, ctx, ref, `{"a":1}`, spi.ChangeLevelStructural)

	if err := ms.ExtendSchema(ctx, ref, nil); err != nil {
		t.Fatalf("nil delta: %v", err)
	}
	if err := ms.ExtendSchema(ctx, ref, spi.SchemaDelta{}); err != nil {
		t.Fatalf("empty delta: %v", err)
	}

	pool := factory.Pool()
	var cnt int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM model_schema_extensions WHERE tenant_id=$1 AND model_name=$2 AND model_version=$3`,
		"t1", ref.EntityName, ref.ModelVersion).Scan(&cnt); err != nil {
		t.Fatalf("count: %v", err)
	}
	if cnt != 0 {
		t.Errorf("expected 0 extension rows, got %d", cnt)
	}
}

func TestExtendSchema_SavepointEvery64(t *testing.T) {
	factory := setupModelExtTest(t)
	factory.SetApplyFunc(jsonRecordingApplyFunc())

	ctx := ctxWithTenant("t1")
	ms, err := factory.ModelStore(ctx)
	if err != nil {
		t.Fatalf("ModelStore: %v", err)
	}
	ref := spi.ModelRef{EntityName: "Book", ModelVersion: "1"}
	seedBaseModel(t, ms, ctx, ref, `{"base":1}`, spi.ChangeLevelStructural)

	d := spi.SchemaDelta(json.RawMessage(`[{"kind":"broaden_type","path":"x","payload":["NULL"]}]`))
	for i := 0; i < 64; i++ {
		if err := ms.ExtendSchema(ctx, ref, d); err != nil {
			t.Fatalf("ExtendSchema #%d: %v", i, err)
		}
	}

	pool := factory.Pool()
	var savepointCount int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM model_schema_extensions
		 WHERE tenant_id=$1 AND model_name=$2 AND model_version=$3 AND kind='savepoint'`,
		"t1", ref.EntityName, ref.ModelVersion).Scan(&savepointCount); err != nil {
		t.Fatalf("savepoint count: %v", err)
	}
	if savepointCount != 1 {
		t.Errorf("expected 1 savepoint after 64 deltas, got %d", savepointCount)
	}

	// Get must still succeed and fold everything.
	if _, err := ms.Get(ctx, ref); err != nil {
		t.Fatalf("Get after 64 deltas: %v", err)
	}
}

func TestGet_NoExtensions_ReturnsBase(t *testing.T) {
	// Intentionally no ApplyFunc — with zero deltas, fold must not error.
	factory := setupModelExtTest(t)

	ctx := ctxWithTenant("t1")
	ms, err := factory.ModelStore(ctx)
	if err != nil {
		t.Fatalf("ModelStore: %v", err)
	}
	ref := spi.ModelRef{EntityName: "Book", ModelVersion: "1"}
	seedBaseModel(t, ms, ctx, ref, `{"t":1}`, spi.ChangeLevelStructural)

	desc, err := ms.Get(ctx, ref)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !bytes.Contains(desc.Schema, []byte(`"t":1`)) {
		t.Errorf("expected base schema returned verbatim; got %s", desc.Schema)
	}
}

func TestGet_WithExtensions_ButNoApplyFunc_Errors(t *testing.T) {
	factory := setupModelExtTest(t)
	factory.SetApplyFunc(jsonRecordingApplyFunc())

	ctx := ctxWithTenant("t1")
	ms, err := factory.ModelStore(ctx)
	if err != nil {
		t.Fatalf("ModelStore: %v", err)
	}
	ref := spi.ModelRef{EntityName: "Book", ModelVersion: "1"}
	seedBaseModel(t, ms, ctx, ref, `{"base":1}`, spi.ChangeLevelStructural)
	if err := ms.ExtendSchema(ctx, ref, spi.SchemaDelta(`[{"kind":"broaden_type","path":"x","payload":["NULL"]}]`)); err != nil {
		t.Fatalf("ExtendSchema: %v", err)
	}

	// Build a SECOND factory sharing the same pool but with no ApplyFunc.
	factory2 := postgres.NewStoreFactory(factory.Pool())
	ms2, err := factory2.ModelStore(ctx)
	if err != nil {
		t.Fatalf("ModelStore 2: %v", err)
	}
	if _, err := ms2.Get(ctx, ref); err == nil {
		t.Error("expected error when ApplyFunc is not wired but deltas exist")
	}
}

func TestStoreFactory_SetApplyFuncTwicePanics(t *testing.T) {
	factory := postgres.NewStoreFactory(nil)
	factory.SetApplyFunc(jsonRecordingApplyFunc())
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on second SetApplyFunc call")
		}
	}()
	factory.SetApplyFunc(jsonRecordingApplyFunc())
}
