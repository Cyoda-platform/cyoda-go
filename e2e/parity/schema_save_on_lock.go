package parity

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/cyoda-platform/cyoda-go/e2e/parity/client"
)

// RunSchemaExtensionSavepointOnLockFoldEquivalence asserts B-I2/B-I3:
// a lock-triggered savepoint does not change the observable fold.
// The backend's fold across the lock boundary must equal the in-memory
// oracle's serial-replay result — the oracle has no concept of
// savepoints, proving the persistence optimization is invisible.
//
// Scenario: import seed (state=UNLOCKED, schema={field_0}) → Lock
// (triggers savepoint per B-I3 in the sqlite/postgres plugins) →
// SetChangeLevel=STRUCTURAL → two extension-widening CreateEntity
// calls (schema widens to {field_0, field_1, field_2}).
//
// The resulting SIMPLE_VIEW bytes must match a serial oracle replay
// of [seedBody, ext1Body, ext2Body]. Any divergence indicates the
// savepoint-on-lock affected observable state.
func RunSchemaExtensionSavepointOnLockFoldEquivalence(t *testing.T, fixture BackendFixture) {
	tenant := fixture.NewTenant(t)
	c := client.NewClient(fixture.BaseURL(), tenant.Token)

	const modelName = "b-i2-lock-fold"
	const modelVersion = 1

	seedBody := map[string]string{"field_0": "seed"}
	seedRaw, _ := json.Marshal(seedBody)
	if err := c.ImportModel(t, modelName, modelVersion, string(seedRaw)); err != nil {
		t.Fatalf("ImportModel: %v", err)
	}
	// Lock triggers the save-on-lock savepoint (B-I3). Subsequent
	// CreateEntity extensions fold on top of that savepoint.
	if err := c.LockModel(t, modelName, modelVersion); err != nil {
		t.Fatalf("LockModel: %v", err)
	}
	if err := c.SetChangeLevel(t, modelName, modelVersion, "STRUCTURAL"); err != nil {
		t.Fatalf("SetChangeLevel STRUCTURAL: %v", err)
	}

	ext1Body := map[string]string{"field_0": "e1", "field_1": "e1"}
	ext1Raw, _ := json.Marshal(ext1Body)
	if _, err := c.CreateEntity(t, modelName, modelVersion, string(ext1Raw)); err != nil {
		t.Fatalf("CreateEntity ext1: %v", err)
	}

	ext2Body := map[string]string{"field_0": "e2", "field_1": "e2", "field_2": "e2"}
	ext2Raw, _ := json.Marshal(ext2Body)
	if _, err := c.CreateEntity(t, modelName, modelVersion, string(ext2Raw)); err != nil {
		t.Fatalf("CreateEntity ext2: %v", err)
	}

	got, err := c.ExportModel(t, "SIMPLE_VIEW", modelName, modelVersion)
	if err != nil {
		t.Fatalf("ExportModel: %v", err)
	}

	// Oracle replays the three bodies in order with ChangeLevelStructural.
	// Model is locked when Export runs → currentState "LOCKED".
	expected, err := expectedSimpleViewFromBodies(
		[]map[string]string{seedBody, ext1Body, ext2Body},
		"LOCKED",
	)
	if err != nil {
		t.Fatalf("oracle: %v", err)
	}
	if !bytes.Equal(got, expected) {
		t.Errorf("%s: fold across lock != serial replay — savepoint-on-lock affected observable bytes\n  got:      %s\n  expected: %s",
			t.Name(), string(got), string(expected))
	}
}
