package parity

import "testing"

// Total parity scenarios: 34 (Phase 1 smoke + Phase 4a CRUD/persistence +
// Phase 4b workflow/compute + distributed-safety contracts).
//
// Unmigrated internal/e2e/ tests (40 remaining): entity lifecycle,
// model extension, transaction stress tests, workflow failure paths,
// workflow loopback/cascade-depth/multi-processor, search string operators,
// message batch delete, workflow overwrite/export-empty. These continue
// to run as postgres-only tests and will be migrated to the parity suite
// in a follow-up effort.

// NamedTest is a single parity scenario plus the name under which it
// shows up in subtest output.
type NamedTest struct {
	Name string
	Fn   func(t *testing.T, fixture BackendFixture)
}

// allTests is the canonical list of parity scenarios. Per-backend
// wrappers iterate this list and run every entry against their fixture.
//
// Adding a scenario: add one new entry here AND create the corresponding
// Run* function in a topical file (e.g. entity.go, workflow_proc.go).
// Every backend wrapper picks the new entry up automatically — there is
// no per-backend wiring to forget.
var allTests = []NamedTest{
	// Phase 1 — smoke test
	{"SmokeTest", RunSmokeTest},

	// Phase 4a — model lifecycle (Task 4a.1)
	{"ModelImportAndExport", RunModelImportAndExport},
	{"ModelLockAndUnlock", RunModelLockAndUnlock},
	{"ModelListModels", RunModelListModels},
	{"ModelDelete", RunModelDelete},
	{"WorkflowImportExport", RunWorkflowImportExport},

	// Phase 4a — entity CRUD (Task 4a.2)
	{"EntityCreateAndGet", RunEntityCreateAndGet},
	{"EntityDelete", RunEntityDelete},
	{"EntityListByModel", RunEntityListByModel},

	// Phase 4a — bi-temporal (Task 4a.3)
	{"TemporalPointInTimeRetrieval", RunTemporalPointInTimeRetrieval},
	{"TemporalGetAsAtPopulatesFullMeta", RunTemporalGetAsAtPopulatesFullMeta},

	// Phase 4a — audit (Task 4a.4)
	{"AuditEntityHistory", RunAuditEntityHistory},
	{"AuditWorkflowEvents", RunAuditWorkflowEvents},
	{"AuditPostTxIdMatchesWorkflowFinished", RunAuditPostTxIdMatchesWorkflowFinished},

	// Phase 4a — tenant isolation (Task 4a.5)
	{"TenantIsolationEntities", RunTenantIsolationEntities},
	{"TenantIsolationModels", RunTenantIsolationModels},

	// Phase 4a — messaging (Task 4a.6)
	{"MessageCreateAndGet", RunMessageCreateAndGet},
	{"MessageDelete", RunMessageDelete},
	{"MessageLargePayload", RunMessageLargePayload},

	// Phase 4a — schema symmetry (Task 4a.7)
	{"DeepSchemaSymmetry", RunDeepSchemaSymmetry},

	// Phase 4a — empty tenant + search consistency (Task 4a.8)
	{"EmptyTenantOperations", RunEmptyTenantOperations},
	{"SearchIndexImmediateConsistency", RunSearchIndexImmediateConsistency},

	// Phase 4b — workflow + processors + criteria (Tasks 4b.2-5)
	{"WorkflowProcessorChainOnCreation", RunWorkflowProcessorChainOnCreation},
	{"WorkflowCriteriaMatch", RunWorkflowCriteriaMatch},
	{"WorkflowCriteriaNoMatch", RunWorkflowCriteriaNoMatch},
	{"WorkflowMultiStateCascade", RunWorkflowMultiStateCascade},
	{"WorkflowManualTransition", RunWorkflowManualTransition},

	// Phase 4b — search scenarios (Task 4b.6-8)
	{"SearchSimpleCondition", RunSearchSimpleCondition},
	{"SearchLifecycleCondition", RunSearchLifecycleCondition},
	{"SearchGroupCondition", RunSearchGroupCondition},
	{"SearchNoMatches", RunSearchNoMatches},
	{"SearchAfterUpdate", RunSearchAfterUpdate},

	// Phase 4b — workflow selection (Task 4b.7)
	{"WorkflowCriteriaSelectingWorkflow", RunWorkflowCriteriaSelectingWorkflow},

	// Phase 4b — distributed-safety contracts (Tasks 4b.9-10)
	{"ConcurrentConflictingUpdate", RunConcurrentConflictingUpdate},
	{"ConcurrentTransitionsDifferentEntities", RunConcurrentTransitionsDifferentEntities},

	// A.1 — numeric classifier parity (HTTP round-trip)
	{"NumericClassification18DigitDecimal", RunNumericClassification18DigitDecimal},
	{"NumericClassification20DigitDecimal", RunNumericClassification20DigitDecimal},
	{"NumericClassificationLargeInteger", RunNumericClassificationLargeInteger},
	{"NumericClassificationIntegerSchemaAcceptsInteger", RunNumericClassificationIntegerSchemaAcceptsInteger},
	{"NumericClassificationIntegerSchemaRejectsDecimal", RunNumericClassificationIntegerSchemaRejectsDecimal},

	// Schema extensions — sequential fold across requests
	{"SchemaExtensionsSequentialFoldAcrossRequests", RunSchemaExtensionsSequentialFoldAcrossRequests},
	{"SchemaExtensionCrossBackendByteIdentity", RunSchemaExtensionCrossBackendByteIdentity},
	{"SchemaExtensionAtomicRejection", RunSchemaExtensionAtomicRejection},
	{"SchemaExtensionConcurrentConvergence", RunSchemaExtensionConcurrentConvergence},
	{"SchemaExtensionSavepointOnLockFoldEquivalence", RunSchemaExtensionSavepointOnLockFoldEquivalence},
	{"SchemaExtensionLocalCacheInvalidationOnCommit", RunSchemaExtensionLocalCacheInvalidationOnCommit},
	{"SchemaExtensionByteIdentityProperty", RunSchemaExtensionByteIdentityProperty},
}

// AllTests returns the canonical list of parity scenarios in registration
// order. The returned slice is a defensive copy — callers may iterate or
// filter it freely without affecting subsequent calls.
func AllTests() []NamedTest {
	out := make([]NamedTest, len(allTests))
	copy(out, allTests)
	return out
}
