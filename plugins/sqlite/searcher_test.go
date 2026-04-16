package sqlite_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	spi "github.com/cyoda-platform/cyoda-go-spi"
	"github.com/cyoda-platform/cyoda-go/plugins/sqlite"
)

// testCtx returns a context with a UserContext for the given tenant.
func testCtx(tenantID string) context.Context {
	return spi.WithUserContext(context.Background(), &spi.UserContext{
		UserID:   "test-user",
		UserName: "Test User",
		Tenant: spi.Tenant{
			ID:   spi.TenantID(tenantID),
			Name: "Test Tenant",
		},
		Roles: []string{"ROLE_USER"},
	})
}

// setupSearcherTest creates a StoreFactory and saves test entities.
func setupSearcherTest(t *testing.T) (*sqlite.StoreFactory, context.Context) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "search_test.db")

	factory, err := sqlite.NewStoreFactoryForTest(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("create factory: %v", err)
	}
	t.Cleanup(func() { factory.Close() })

	ctx := testCtx("tenant-1")
	ref := spi.ModelRef{EntityName: "person", ModelVersion: "1"}

	store, err := factory.EntityStore(ctx)
	if err != nil {
		t.Fatalf("EntityStore: %v", err)
	}

	entities := []struct {
		id   string
		data string
	}{
		{"e1", `{"name":"Alice","age":30,"city":"Berlin"}`},
		{"e2", `{"name":"Bob","age":25,"city":"Munich"}`},
		{"e3", `{"name":"Charlie","age":35,"city":"Berlin"}`},
		{"e4", `{"name":"Diana","age":28,"city":"Hamburg"}`},
		{"e5", `{"name":"Eve","age":40,"city":"Munich"}`},
	}

	for _, e := range entities {
		_, err := store.Save(ctx, &spi.Entity{
			Meta: spi.EntityMeta{
				ID:       e.id,
				ModelRef: ref,
				State:    "NEW",
			},
			Data: []byte(e.data),
		})
		if err != nil {
			t.Fatalf("Save %s: %v", e.id, err)
		}
	}

	return factory, ctx
}

func TestSearcher_EqFilter(t *testing.T) {
	factory, ctx := setupSearcherTest(t)

	store, _ := factory.EntityStore(ctx)
	searcher, ok := store.(spi.Searcher)
	if !ok {
		t.Fatal("entityStore does not implement spi.Searcher")
	}

	results, err := searcher.Search(ctx, spi.Filter{
		Op:     spi.FilterEq,
		Path:   "city",
		Source: spi.SourceData,
		Value:  "Berlin",
	}, spi.SearchOptions{
		ModelName:    "person",
		ModelVersion: "1",
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results for city=Berlin, got %d", len(results))
	}
}

func TestSearcher_GtFilter(t *testing.T) {
	factory, ctx := setupSearcherTest(t)

	store, _ := factory.EntityStore(ctx)
	searcher := store.(spi.Searcher)

	results, err := searcher.Search(ctx, spi.Filter{
		Op:     spi.FilterGt,
		Path:   "age",
		Source: spi.SourceData,
		Value:  float64(30),
	}, spi.SearchOptions{
		ModelName:    "person",
		ModelVersion: "1",
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	// Charlie(35) and Eve(40) have age > 30.
	if len(results) != 2 {
		t.Fatalf("expected 2 results for age>30, got %d", len(results))
	}
}

func TestSearcher_ContainsFilter(t *testing.T) {
	factory, ctx := setupSearcherTest(t)

	store, _ := factory.EntityStore(ctx)
	searcher := store.(spi.Searcher)

	results, err := searcher.Search(ctx, spi.Filter{
		Op:     spi.FilterContains,
		Path:   "name",
		Source: spi.SourceData,
		Value:  "li",
	}, spi.SearchOptions{
		ModelName:    "person",
		ModelVersion: "1",
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	// Alice and Charlie both contain "li".
	if len(results) != 2 {
		t.Fatalf("expected 2 results containing 'li', got %d", len(results))
	}
}

func TestSearcher_ANDFilter(t *testing.T) {
	factory, ctx := setupSearcherTest(t)

	store, _ := factory.EntityStore(ctx)
	searcher := store.(spi.Searcher)

	results, err := searcher.Search(ctx, spi.Filter{
		Op: spi.FilterAnd,
		Children: []spi.Filter{
			{Op: spi.FilterEq, Path: "city", Source: spi.SourceData, Value: "Berlin"},
			{Op: spi.FilterGt, Path: "age", Source: spi.SourceData, Value: float64(31)},
		},
	}, spi.SearchOptions{
		ModelName:    "person",
		ModelVersion: "1",
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	// Only Charlie: Berlin, age 35.
	if len(results) != 1 {
		t.Fatalf("expected 1 result for Berlin AND age>31, got %d", len(results))
	}
	if results[0].Meta.ID != "e3" {
		t.Errorf("expected e3, got %s", results[0].Meta.ID)
	}
}

func TestSearcher_ORFilter(t *testing.T) {
	factory, ctx := setupSearcherTest(t)

	store, _ := factory.EntityStore(ctx)
	searcher := store.(spi.Searcher)

	results, err := searcher.Search(ctx, spi.Filter{
		Op: spi.FilterOr,
		Children: []spi.Filter{
			{Op: spi.FilterEq, Path: "city", Source: spi.SourceData, Value: "Hamburg"},
			{Op: spi.FilterEq, Path: "city", Source: spi.SourceData, Value: "Munich"},
		},
	}, spi.SearchOptions{
		ModelName:    "person",
		ModelVersion: "1",
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	// Bob(Munich), Diana(Hamburg), Eve(Munich).
	if len(results) != 3 {
		t.Fatalf("expected 3 results for Hamburg OR Munich, got %d", len(results))
	}
}

func TestSearcher_PostFilterRegex(t *testing.T) {
	factory, ctx := setupSearcherTest(t)

	store, _ := factory.EntityStore(ctx)
	searcher := store.(spi.Searcher)

	// Regex is not pushable, should post-filter.
	results, err := searcher.Search(ctx, spi.Filter{
		Op:     spi.FilterMatchesRegex,
		Path:   "name",
		Source: spi.SourceData,
		Value:  "^[A-C]",
	}, spi.SearchOptions{
		ModelName:    "person",
		ModelVersion: "1",
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	// Alice, Bob, Charlie start with A/B/C.
	if len(results) != 3 {
		t.Fatalf("expected 3 results for regex ^[A-C], got %d", len(results))
	}
}

func TestSearcher_MixedPushAndPostFilter(t *testing.T) {
	factory, ctx := setupSearcherTest(t)

	store, _ := factory.EntityStore(ctx)
	searcher := store.(spi.Searcher)

	// AND with pushable eq(city) and non-pushable regex(name).
	results, err := searcher.Search(ctx, spi.Filter{
		Op: spi.FilterAnd,
		Children: []spi.Filter{
			{Op: spi.FilterEq, Path: "city", Source: spi.SourceData, Value: "Berlin"},
			{Op: spi.FilterMatchesRegex, Path: "name", Source: spi.SourceData, Value: "^A"},
		},
	}, spi.SearchOptions{
		ModelName:    "person",
		ModelVersion: "1",
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	// Alice: Berlin, starts with A. Charlie: Berlin, starts with C (no match).
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Meta.ID != "e1" {
		t.Errorf("expected e1 (Alice), got %s", results[0].Meta.ID)
	}
}

func TestSearcher_Pagination_NoPushdown(t *testing.T) {
	factory, ctx := setupSearcherTest(t)

	store, _ := factory.EntityStore(ctx)
	searcher := store.(spi.Searcher)

	// Use a pushable filter that matches all.
	filter := spi.Filter{
		Op:     spi.FilterNotNull,
		Path:   "name",
		Source: spi.SourceData,
	}

	// Get all (5 entities).
	all, err := searcher.Search(ctx, filter, spi.SearchOptions{
		ModelName:    "person",
		ModelVersion: "1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 5 {
		t.Fatalf("expected 5 results, got %d", len(all))
	}

	// Limit 2.
	page, err := searcher.Search(ctx, filter, spi.SearchOptions{
		ModelName:    "person",
		ModelVersion: "1",
		Limit:        2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 2 {
		t.Fatalf("expected 2 results with limit=2, got %d", len(page))
	}

	// Offset 3, Limit 10.
	tail, err := searcher.Search(ctx, filter, spi.SearchOptions{
		ModelName:    "person",
		ModelVersion: "1",
		Limit:        10,
		Offset:       3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(tail) != 2 {
		t.Fatalf("expected 2 results with offset=3, got %d", len(tail))
	}
}

func TestSearcher_Pagination_WithPostFilter(t *testing.T) {
	factory, ctx := setupSearcherTest(t)

	store, _ := factory.EntityStore(ctx)
	searcher := store.(spi.Searcher)

	// Non-pushable filter matching all.
	filter := spi.Filter{
		Op:     spi.FilterMatchesRegex,
		Path:   "name",
		Source: spi.SourceData,
		Value:  ".*",
	}

	// Get all.
	all, err := searcher.Search(ctx, filter, spi.SearchOptions{
		ModelName:    "person",
		ModelVersion: "1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 5 {
		t.Fatalf("expected 5 results, got %d", len(all))
	}

	// Limit 2.
	page, err := searcher.Search(ctx, filter, spi.SearchOptions{
		ModelName:    "person",
		ModelVersion: "1",
		Limit:        2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 2 {
		t.Fatalf("expected 2 results with limit=2, got %d", len(page))
	}

	// Offset 3.
	tail, err := searcher.Search(ctx, filter, spi.SearchOptions{
		ModelName:    "person",
		ModelVersion: "1",
		Limit:        10,
		Offset:       3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(tail) != 2 {
		t.Fatalf("expected 2 results with offset=3, got %d", len(tail))
	}
}

func TestSearcher_ScanBudgetExhausted(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "budget_test.db")

	// Create factory with a very low scan limit.
	factory, err := sqlite.NewStoreFactoryForTestWithScanLimit(context.Background(), dbPath, 3)
	if err != nil {
		t.Fatalf("create factory: %v", err)
	}
	defer factory.Close()

	ctx := testCtx("tenant-1")
	ref := spi.ModelRef{EntityName: "item", ModelVersion: "1"}
	store, _ := factory.EntityStore(ctx)

	// Save 10 entities.
	for i := 0; i < 10; i++ {
		_, err := store.Save(ctx, &spi.Entity{
			Meta: spi.EntityMeta{
				ID:       fmt.Sprintf("e%d", i),
				ModelRef: ref,
				State:    "NEW",
			},
			Data: []byte(fmt.Sprintf(`{"val":%d}`, i)),
		})
		if err != nil {
			t.Fatalf("Save: %v", err)
		}
	}

	searcher := store.(spi.Searcher)

	// Use a non-pushable filter to force post-filtering (triggering scan budget).
	_, err = searcher.Search(ctx, spi.Filter{
		Op:     spi.FilterMatchesRegex,
		Path:   "val",
		Source: spi.SourceData,
		Value:  ".*",
	}, spi.SearchOptions{
		ModelName:    "item",
		ModelVersion: "1",
	})

	if err == nil {
		t.Fatal("expected ErrScanBudgetExhausted, got nil")
	}
	if !errors.Is(err, sqlite.ErrScanBudgetExhausted) {
		t.Fatalf("expected ErrScanBudgetExhausted, got: %v", err)
	}
}

func TestSearcher_TenantIsolation(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "tenant_test.db")

	factory, err := sqlite.NewStoreFactoryForTest(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("create factory: %v", err)
	}
	defer factory.Close()

	ctxA := testCtx("tenant-A")
	ctxB := testCtx("tenant-B")
	ref := spi.ModelRef{EntityName: "person", ModelVersion: "1"}

	storeA, _ := factory.EntityStore(ctxA)
	storeB, _ := factory.EntityStore(ctxB)

	_, _ = storeA.Save(ctxA, &spi.Entity{
		Meta: spi.EntityMeta{ID: "e1", ModelRef: ref, State: "NEW"},
		Data: []byte(`{"name":"Alice"}`),
	})

	_, _ = storeB.Save(ctxB, &spi.Entity{
		Meta: spi.EntityMeta{ID: "e2", ModelRef: ref, State: "NEW"},
		Data: []byte(`{"name":"Bob"}`),
	})

	searcherA := storeA.(spi.Searcher)
	searcherB := storeB.(spi.Searcher)

	filter := spi.Filter{Op: spi.FilterNotNull, Path: "name", Source: spi.SourceData}
	opts := spi.SearchOptions{ModelName: "person", ModelVersion: "1"}

	resultsA, err := searcherA.Search(ctxA, filter, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(resultsA) != 1 || resultsA[0].Meta.ID != "e1" {
		t.Errorf("tenant A should see e1 only, got %d results", len(resultsA))
	}

	resultsB, err := searcherB.Search(ctxB, filter, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(resultsB) != 1 || resultsB[0].Meta.ID != "e2" {
		t.Errorf("tenant B should see e2 only, got %d results", len(resultsB))
	}
}
