package postgres

// debugMode toggles dev-time operator-contract assertions that would
// otherwise only log a warning in production. Wired from
// cmd/cyoda/main.go based on CYODA_DEBUG env var (future task G1).
// Until G1 wires it, debugMode stays false and production behaviour
// is used in tests by default.
var debugMode = false

// SetDebugMode toggles the dev-time operator-contract assertion flag.
// Public so cmd/cyoda/main.go can switch it at startup and so tests
// can opt-in to the stricter path.
func SetDebugMode(on bool) { debugMode = on }

// buildIsDev reports whether this build is in dev mode — i.e.
// operator-contract assertions are fatal rather than warn-only.
func buildIsDev() bool { return debugMode }
