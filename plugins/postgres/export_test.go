package postgres

// DropSchemaForTest exposes dropSchema (the unexported implementation) to
// _test.go files in this package and any external test packages that import
// "github.com/cyoda-platform/cyoda-go/plugins/postgres". The export_test.go
// idiom keeps the symbol invisible to non-test compilation: the file is
// compiled only when `go test` is building the package, so production binaries
// never see DropSchemaForTest.
//
// Use this in test helpers and conformance fixtures. Never call it from
// production code.
var DropSchemaForTest = dropSchema

// MigrateDownForTest exposes migrateDown to test files via the export_test.go
// idiom. Use only in tests; never in production code.
var MigrateDownForTest = migrateDown

// ClassifyErrorForTest exposes classifyError to allow unit-testing of the
// serialization/deadlock classification logic without requiring a live database.
var ClassifyErrorForTest = classifyError
