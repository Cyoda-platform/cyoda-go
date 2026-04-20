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

// TestMemory_ExtendSchema_MultiDeltaFold asserts three sequential ExtendSchema
// calls all appear in the folded schema on Get. The recordingApplyFunc nests
// each delta inside the next, so all three delta payloads remain byte-findable.
func TestMemory_ExtendSchema_MultiDeltaFold(t *testing.T) {
	f := memory.NewStoreFactory(memory.WithApplyFunc(recordingApplyFunc()))
	defer f.Close()
	ctx := extTestCtx("t1")
	ms, _ := f.ModelStore(ctx)
	ref := spi.ModelRef{EntityName: "Book", ModelVersion: "1"}

	if err := ms.Save(ctx, &spi.ModelDescriptor{
		Ref: ref, State: spi.ModelUnlocked, ChangeLevel: spi.ChangeLevelStructural,
		Schema: []byte(`{"base":1}`), UpdateDate: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := ms.Lock(ctx, ref); err != nil {
		t.Fatalf("Lock: %v", err)
	}

	deltas := []spi.SchemaDelta{
		spi.SchemaDelta(`[{"kind":"broaden_type","path":"x","payload":["NULL"]}]`),
		spi.SchemaDelta(`[{"kind":"broaden_type","path":"y","payload":["STRING"]}]`),
		spi.SchemaDelta(`[{"kind":"broaden_type","path":"z","payload":["BOOLEAN"]}]`),
	}
	for i, d := range deltas {
		if err := ms.ExtendSchema(ctx, ref, d); err != nil {
			t.Fatalf("delta %d: %v", i, err)
		}
	}

	desc, err := ms.Get(ctx, ref)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	for _, marker := range []string{"NULL", "STRING", "BOOLEAN"} {
		if !bytes.Contains(desc.Schema, []byte(marker)) {
			t.Errorf("expected %q in folded schema, got %s", marker, desc.Schema)
		}
	}
}

// TestMemory_ExtendSchema_CrossTenantIsolation asserts extending one tenant's
// model never affects another tenant's same-ref model.
func TestMemory_ExtendSchema_CrossTenantIsolation(t *testing.T) {
	f := memory.NewStoreFactory(memory.WithApplyFunc(recordingApplyFunc()))
	defer f.Close()
	ctxA := extTestCtx("tenantA")
	ctxB := extTestCtx("tenantB")

	msA, _ := f.ModelStore(ctxA)
	msB, _ := f.ModelStore(ctxB)

	ref := spi.ModelRef{EntityName: "Shared", ModelVersion: "1"}
	if err := msA.Save(ctxA, &spi.ModelDescriptor{
		Ref: ref, State: spi.ModelUnlocked, ChangeLevel: spi.ChangeLevelStructural,
		Schema: []byte(`{"t":"A"}`), UpdateDate: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("Save A: %v", err)
	}
	if err := msA.Lock(ctxA, ref); err != nil {
		t.Fatalf("Lock A: %v", err)
	}
	if err := msB.Save(ctxB, &spi.ModelDescriptor{
		Ref: ref, State: spi.ModelUnlocked, ChangeLevel: spi.ChangeLevelStructural,
		Schema: []byte(`{"t":"B"}`), UpdateDate: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("Save B: %v", err)
	}
	if err := msB.Lock(ctxB, ref); err != nil {
		t.Fatalf("Lock B: %v", err)
	}

	if err := msA.ExtendSchema(ctxA, ref, spi.SchemaDelta(`[{"kind":"broaden_type","path":"x","payload":["A_DELTA"]}]`)); err != nil {
		t.Fatalf("ExtendSchema A: %v", err)
	}

	descA, err := msA.Get(ctxA, ref)
	if err != nil {
		t.Fatalf("Get A: %v", err)
	}
	descB, err := msB.Get(ctxB, ref)
	if err != nil {
		t.Fatalf("Get B: %v", err)
	}

	if !bytes.Contains(descA.Schema, []byte("A_DELTA")) {
		t.Errorf("tenant A: expected A_DELTA, got %s", descA.Schema)
	}
	if bytes.Contains(descB.Schema, []byte("A_DELTA")) {
		t.Errorf("tenant isolation broken: tenant B sees A's delta: %s", descB.Schema)
	}
	if !bytes.Contains(descA.Schema, []byte(`"t":"A"`)) {
		t.Errorf("tenant A lost base: %s", descA.Schema)
	}
	if !bytes.Contains(descB.Schema, []byte(`"t":"B"`)) {
		t.Errorf("tenant B lost base: %s", descB.Schema)
	}
}
