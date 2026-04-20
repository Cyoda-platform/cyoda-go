package memory_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	spi "github.com/cyoda-platform/cyoda-go-spi"
	"github.com/cyoda-platform/cyoda-go/plugins/memory"
)

func recordingApplyFunc() memory.ApplyFunc {
	return func(base []byte, delta spi.SchemaDelta) ([]byte, error) {
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

func TestMemory_ExtendSchema_AppliesInPlace(t *testing.T) {
	f := memory.NewStoreFactory(memory.WithApplyFunc(recordingApplyFunc()))
	defer f.Close()
	ctx := extTestCtx("t1")
	ms, _ := f.ModelStore(ctx)
	ref := spi.ModelRef{EntityName: "Book", ModelVersion: "1"}

	_ = ms.Save(ctx, &spi.ModelDescriptor{
		Ref: ref, State: spi.ModelUnlocked, ChangeLevel: spi.ChangeLevelStructural,
		Schema: []byte(`{"initial":true}`), UpdateDate: time.Now().UTC(),
	})
	_ = ms.Lock(ctx, ref)

	delta := spi.SchemaDelta(`[{"kind":"broaden_type","path":"x","payload":["NULL"]}]`)
	if err := ms.ExtendSchema(ctx, ref, delta); err != nil {
		t.Fatalf("ExtendSchema: %v", err)
	}

	got, _ := ms.Get(ctx, ref)
	if !bytes.Contains(got.Schema, []byte(`"delta"`)) || !bytes.Contains(got.Schema, []byte(`"initial"`)) {
		t.Errorf("expected applied schema, got %s", got.Schema)
	}
}

func TestMemory_ExtendSchema_EmptyDeltaIsNoop(t *testing.T) {
	f := memory.NewStoreFactory(memory.WithApplyFunc(recordingApplyFunc()))
	defer f.Close()
	ctx := extTestCtx("t1")
	ms, _ := f.ModelStore(ctx)
	ref := spi.ModelRef{EntityName: "Book", ModelVersion: "1"}
	_ = ms.Save(ctx, &spi.ModelDescriptor{
		Ref: ref, State: spi.ModelUnlocked, ChangeLevel: spi.ChangeLevelStructural,
		Schema: []byte(`{"s":1}`), UpdateDate: time.Now().UTC(),
	})
	if err := ms.ExtendSchema(ctx, ref, nil); err != nil {
		t.Fatalf("nil: %v", err)
	}
	got, _ := ms.Get(ctx, ref)
	if !bytes.Equal(got.Schema, []byte(`{"s":1}`)) {
		t.Errorf("expected unchanged, got %s", got.Schema)
	}
}

func TestMemory_ExtendSchema_MissingApplyFunc_Errors(t *testing.T) {
	f := memory.NewStoreFactory() // no WithApplyFunc
	defer f.Close()
	ctx := extTestCtx("t1")
	ms, _ := f.ModelStore(ctx)
	ref := spi.ModelRef{EntityName: "Book", ModelVersion: "1"}
	_ = ms.Save(ctx, &spi.ModelDescriptor{
		Ref: ref, State: spi.ModelUnlocked, ChangeLevel: spi.ChangeLevelStructural,
		Schema: []byte(`{}`), UpdateDate: time.Now().UTC(),
	})
	err := ms.ExtendSchema(ctx, ref, spi.SchemaDelta(`[{"kind":"broaden_type","path":"x","payload":["NULL"]}]`))
	if err == nil {
		t.Fatal("expected error when ApplyFunc not wired")
	}
}

func TestMemory_ExtendSchema_ModelNotFound(t *testing.T) {
	f := memory.NewStoreFactory(memory.WithApplyFunc(recordingApplyFunc()))
	defer f.Close()
	ctx := extTestCtx("t1")
	ms, _ := f.ModelStore(ctx)
	ref := spi.ModelRef{EntityName: "Missing", ModelVersion: "1"}
	err := ms.ExtendSchema(ctx, ref, spi.SchemaDelta(`[{"kind":"broaden_type","path":"x","payload":["NULL"]}]`))
	if err == nil {
		t.Fatal("expected error")
	}
}
