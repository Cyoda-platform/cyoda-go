package e2e_test

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// TestModelSchemaExtensions_ConcurrentUpdatesNoConflict is the regression
// test for the hot-row serialization bug this effort fixes.
//
// Before the fix: with ChangeLevel set on a model, concurrent entity updates
// whose payloads each imply (at most) an additive schema extension all call
// validateOrExtend → modelStore.Save(desc), which updates the single models
// row under REPEATABLE READ. First committer wins; every other commit fails
// with SQLSTATE 40001 surfaced as a CONFLICT 409.
//
// After the fix: validateOrExtend computes a SchemaDelta via schema.Diff
// and calls modelStore.ExtendSchema, which inserts per-tx rows into
// model_schema_extensions — no hot-row contention. All concurrent writers
// commit.
func TestModelSchemaExtensions_ConcurrentUpdatesNoConflict(t *testing.T) {
	const modelName = "e2e-schema-ext-concurrent"
	const version = 1
	const N = 8

	// 1. Bring the model up: import, lock, STRUCTURAL.
	importModelE2E(t, modelName, version)
	lockModelE2E(t, modelName, version)
	setChangeLevelE2E(t, modelName, version, "STRUCTURAL")

	// 2. Fire N concurrent entity creates. Each payload introduces a
	// distinct new property (field_i) so every write genuinely triggers
	// an ExtendSchema path. Under the old Save-based path, N-1 of these
	// would fail with 40001/CONFLICT.
	var (
		wg        sync.WaitGroup
		conflicts atomic.Int32
		other     atomic.Int32
		okCount   atomic.Int32
	)
	for i := 0; i < N; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			payload := fmt.Sprintf(
				`{"name":"Test-%d","amount":%d,"status":"new","field_%d":"val_%d"}`,
				i, i*10, i, i,
			)
			path := fmt.Sprintf("/api/entity/JSON/%s/%d", modelName, version)
			resp := doAuth(t, http.MethodPost, path, payload)
			body := readBody(t, resp)
			switch resp.StatusCode {
			case http.StatusOK:
				okCount.Add(1)
			case http.StatusConflict:
				conflicts.Add(1)
				t.Logf("goroutine %d got 409 CONFLICT: %s", i, body)
			default:
				other.Add(1)
				t.Logf("goroutine %d got %d: %s", i, resp.StatusCode, body)
			}
		}()
	}
	wg.Wait()

	if conflicts.Load() != 0 {
		t.Errorf("regression: %d of %d concurrent entity updates returned CONFLICT (409). ExtendSchema must eliminate hot-row contention.",
			conflicts.Load(), N)
	}
	if other.Load() != 0 {
		t.Errorf("%d of %d concurrent entity updates returned neither 200 nor 409 (unexpected status).",
			other.Load(), N)
	}
	if okCount.Load() != int32(N) {
		t.Errorf("expected all %d concurrent creates to succeed, got %d", N, okCount.Load())
	}

	// 3. After the storm, the folded schema must reflect every new field.
	schema := exportModelE2E(t, modelName, version)
	raw := fmt.Sprintf("%v", schema)
	for i := 0; i < N; i++ {
		want := fmt.Sprintf("field_%d", i)
		if !strings.Contains(raw, want) {
			t.Errorf("folded schema missing %q after concurrent extensions: %s", want, raw)
		}
	}
}
