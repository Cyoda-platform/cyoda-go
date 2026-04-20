package sqlite_test

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
	"time"

	spi "github.com/cyoda-platform/cyoda-go-spi"
	"github.com/cyoda-platform/cyoda-go/plugins/sqlite"
)

func recordingApplyFunc() sqlite.ApplyFunc {
	return func(base []byte, delta spi.SchemaDelta) ([]byte, error) {
		// Return valid JSON that carries both base and delta so tests can
		// assert Apply was invoked. Store {"base": <base>, "delta": <delta>}.
		// This isn't semantically meaningful; it's just observable.
		if len(base) == 0 {
			return []byte(`{"base":null,"delta":` + string(delta) + `}`), nil
		}
		return []byte(`{"base":` + string(base) + `,"delta":` + string(delta) + `}`), nil
	}
}

func extTestCtx(tenant string) context.Context {
	uc := &spi.UserContext{
		UserID: "test-user",
		Tenant: spi.Tenant{ID: spi.TenantID(tenant)},
	}
	return spi.WithUserContext(context.Background(), uc)
}

func setupSQLiteExt(t *testing.T) *sqlite.StoreFactory {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	f, err := sqlite.NewStoreFactoryForTest(context.Background(), dbPath, sqlite.WithApplyFunc(recordingApplyFunc()))
	if err != nil {
		t.Fatalf("NewStoreFactoryForTest: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

func TestSQLite_ExtendSchema_AppliesInPlace(t *testing.T) {
	f := setupSQLiteExt(t)
	ctx := extTestCtx("t1")
	ms, err := f.ModelStore(ctx)
	if err != nil {
		t.Fatalf("ModelStore: %v", err)
	}
	ref := spi.ModelRef{EntityName: "Book", ModelVersion: "1"}

	// Seed.
	if err := ms.Save(ctx, &spi.ModelDescriptor{
		Ref: ref, State: spi.ModelUnlocked, ChangeLevel: spi.ChangeLevelStructural,
		Schema: []byte(`{"initial":true}`), UpdateDate: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := ms.Lock(ctx, ref); err != nil {
		t.Fatalf("Lock: %v", err)
	}

	delta := spi.SchemaDelta(`[{"kind":"broaden_type","path":"x","payload":["NULL"]}]`)
	if err := ms.ExtendSchema(ctx, ref, delta); err != nil {
		t.Fatalf("ExtendSchema: %v", err)
	}

	got, err := ms.Get(ctx, ref)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !bytes.Contains(got.Schema, []byte(`"delta"`)) || !bytes.Contains(got.Schema, []byte(`"initial"`)) {
		t.Errorf("expected applied schema to contain both initial and delta, got %s", got.Schema)
	}
}

func TestSQLite_ExtendSchema_EmptyDeltaIsNoop(t *testing.T) {
	f := setupSQLiteExt(t)
	ctx := extTestCtx("t1")
	ms, _ := f.ModelStore(ctx)
	ref := spi.ModelRef{EntityName: "Book", ModelVersion: "1"}
	_ = ms.Save(ctx, &spi.ModelDescriptor{
		Ref: ref, State: spi.ModelUnlocked, ChangeLevel: spi.ChangeLevelStructural,
		Schema: []byte(`{"s":1}`), UpdateDate: time.Now().UTC(),
	})

	if err := ms.ExtendSchema(ctx, ref, nil); err != nil {
		t.Fatalf("nil delta: %v", err)
	}
	if err := ms.ExtendSchema(ctx, ref, spi.SchemaDelta{}); err != nil {
		t.Fatalf("empty delta: %v", err)
	}
	got, _ := ms.Get(ctx, ref)
	if !bytes.Equal(got.Schema, []byte(`{"s":1}`)) {
		t.Errorf("expected schema unchanged, got %s", got.Schema)
	}
}

func TestSQLite_ExtendSchema_MissingApplyFunc_Errors(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	f, err := sqlite.NewStoreFactoryForTest(context.Background(), dbPath) // no WithApplyFunc
	if err != nil {
		t.Fatalf("NewStoreFactoryForTest: %v", err)
	}
	defer f.Close()
	ctx := extTestCtx("t1")
	ms, _ := f.ModelStore(ctx)
	ref := spi.ModelRef{EntityName: "Book", ModelVersion: "1"}
	_ = ms.Save(ctx, &spi.ModelDescriptor{
		Ref: ref, State: spi.ModelUnlocked, ChangeLevel: spi.ChangeLevelStructural,
		Schema: []byte(`{}`), UpdateDate: time.Now().UTC(),
	})
	err = ms.ExtendSchema(ctx, ref, spi.SchemaDelta(`[{"kind":"broaden_type","path":"x","payload":["NULL"]}]`))
	if err == nil {
		t.Fatal("expected error when ApplyFunc not wired")
	}
}

func TestSQLite_ExtendSchema_ModelNotFound(t *testing.T) {
	f := setupSQLiteExt(t)
	ctx := extTestCtx("t1")
	ms, _ := f.ModelStore(ctx)
	ref := spi.ModelRef{EntityName: "Missing", ModelVersion: "1"}
	err := ms.ExtendSchema(ctx, ref, spi.SchemaDelta(`[{"kind":"broaden_type","path":"x","payload":["NULL"]}]`))
	if err == nil {
		t.Fatal("expected error on missing model")
	}
}
