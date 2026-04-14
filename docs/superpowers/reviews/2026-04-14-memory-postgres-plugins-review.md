Overall assessment: This is a well-structured, clearly scoped refactoring plan. The decisions are sound, the module layout is idiomatic Go, and the document does an excellent job of making implicit conventions explicit (the Startable/Close symmetry contract, the getenv injection pattern, the operator-actionable panic message). It's evident this has been through real design thinking, not just shuffled around. That said, I have concerns at several levels — some architectural, some about gaps in the spec, and some about operational risk.

# 1. The txID → pgx.Tx registry is a concurrency and lifecycle hazard.
This is my biggest concern. D7 describes a "thread-safe registry mapping txID → pgx.Tx" inside the postgres TM. A few things worry me here:
The registry is essentially a sync.Map or mutex-guarded map that holds live database connections. Every entry pins a connection from the pgx pool for the lifetime of the transaction. The document acknowledges the pool defaults (25 max) but doesn't describe what happens when the registry grows to pool capacity. If Begin() is called and the pool is exhausted, pgxpool.Begin() blocks (or times out, depending on pool config). The TM's Begin should have explicit behavior here — either propagate the pool timeout error cleanly, or enforce its own admission control. The design should state which.
More critically, what's the cleanup strategy for orphaned entries? If a service-layer goroutine panics after Begin() but before Commit()/Rollback(), the pgx.Tx sits in the registry indefinitely, holding a connection. The architecture doc mentions a transaction TTL (60s default) and a reaper in cluster/lifecycle/, but the design document doesn't say whether the postgres TM's registry participates in that reaping. It should. If the reaper only operates at the cluster/token level and the postgres TM registry is a separate structure, you have two sources of truth about transaction liveness that can drift. I'd want the design to explicitly state: "the cluster-level TTL reaper calls TM.Rollback(txID) for expired transactions, which cleans up both the registry entry and the underlying pgx.Tx."

# 2. The StoreFactory.
TransactionManager(ctx) signature implies per-call instantiation but the design implies a singleton.
The method returns (TransactionManager, error), which syntactically looks like a factory method — "give me a TM for this context." But in the core wiring code, it's called once at startup and the result is stored on the app struct for the lifetime of the process. That's fine, but it means the ctx parameter passed at startup is meaningless for the postgres TM (which just returns its singleton). Meanwhile, a future plugin might actually want per-request TMs. The design should clarify the contract: is this "return the plugin's TM singleton" or "create a TM scoped to this context"? If it's the former, consider whether ctx is even needed in the signature, or document that plugins should ignore cancellation on this context.

# 3. Missing D-number (D8 jumps from the document, but D9 follows — there's no D8 header in the decisions section).
Looking at the numbering: D1 through D7 are in the "Design Decisions" section, then D9 and D10 appear, with D8 described later under --help rendering. This is a minor editorial issue, but in a document that will serve as the authoritative record for "why did we do X," having a decision go missing in the sequence erodes trust in the spec as a reference. Renumber or add a note.
Edit: D8 does appear, but it's placed under a different heading hierarchy than D1–D7 and D9–D10. It reads like a section description rather than a decision. Consider normalizing the format so all D-numbered items are peer-level under "Design Decisions."

# 4. The go.work commitment is under-specified for CI.
The design says go.work is committed and go.work.sum is gitignored. It also notes GOWORK=off would break things. But it doesn't specify what the CI pipeline looks like. Does CI run go test ./... from the repo root (which traverses all workspace modules)? Or does it test each module independently? The answer matters because go test ./... under a workspace doesn't guarantee that each module's go.mod is self-consistent — a module might compile only because the workspace resolves a dependency that its own go.mod doesn't declare. Before Plan 4 tags these modules for independent consumption, you should run GOWORK=off go build ./... per-module in CI as a hygiene check. The design should prescribe this.

# 5. The Querier abstraction for pool-or-tx is solid but the design doesn't address SET LOCAL for RLS.
The architecture doc describes row-level security enforced via SET LOCAL app.current_tenant at transaction start. In the postgres TM, Begin() starts a pgx.Tx and registers it. But the design doesn't say where SET LOCAL happens. If it happens in Begin(), the TM needs the tenant ID — which means it needs to extract it from the context (via spi.GetUserContext(ctx).TenantID). If it happens in each store call, it's duplicated and error-prone. This is an important implementation detail that the design should pin down, because getting it wrong means RLS silently doesn't apply and you have a multi-tenancy data leak.

# 6. The env-var rename is described as "greenfield, no migration tooling," but the e2e parity tests use these vars.
The design acknowledges this in the risks section ("postgres fixture sets CYODA_POSTGRES_URL instead of CYODA_DB_URL") but frames it as a mechanical sweep. If this project has any deployment automation, Docker Compose files, Helm charts, or CI secrets that reference CYODA_DB_*, those all break silently — the postgres plugin just won't see a URL and will fail at startup. The design should include a grep/audit step in the implementation plan to enumerate all references outside the Go source tree.

# 7. NewFactory calls createPool(context.Background(), cfg) — this should use the caller's context.
In the postgres plugin skeleton, NewFactory passes context.Background() to pool creation. During graceful startup, if the postgres instance is unreachable, this call will hang until the pgx default timeout (which may be quite long). The core wiring in app.go holds a perfectly good ctx but passes os.Getenv to NewFactory, not a context. The Plugin.NewFactory signature doesn't accept a context.Context. This is a gap in the SPI — NewFactory should take a context so that startup can be cancelled or timed out. Since the SPI is pre-1.0, now is the time to add it. If you defer this, Plan 4's cassandra plugin (which connects to a Cassandra cluster at factory creation time) will hit the same problem and you'll need to break the interface then anyway.

# 8. No health check or readiness probe integration for the plugin.
The core wiring calls Start(ctx) if the factory is Startable, but there's no Healthy() error or Ready() error method on the SPI. The postgres plugin's pool might lose connectivity after startup. Today, the application presumably discovers this when a store call fails. But for Kubernetes deployments (which the architecture doc implies via its discussion of nginx LB and stateless nodes), a readiness probe that checks pool.Ping(ctx) would prevent traffic from reaching a node whose PG connection is dead. This isn't strictly Plan 3 scope, but it's worth capturing as a future SPI addition, and the design doc's "Out of Scope / Deferred" section would be the right place.

# 9. RegisteredPlugins() return order is unspecified.
The --help rendering loops over spi.RegisteredPlugins(). If the underlying storage is a map[string]Plugin, iteration order is nondeterministic. The help output will shuffle between runs, which is mildly unprofessional and makes golden-file testing of help output impossible. The design should specify sorted-by-name return order, or document that the order is registration order (which is init() order, which is deterministic per the Go spec for a given set of imports).
10. The observability decorator wrapping is ordered correctly but fragile.
In app.go:
```go
a.transactionManager = txMgr
if cfg.OTelEnabled {
    a.transactionManager = observability.NewTracingTransactionManager(a.transactionManager)
}
```

This works, but it means the tracing TM wraps the plugin TM. If a future middleware (metrics, logging, circuit-breaking) also wraps the TM, the ordering becomes a chain of decorators whose sequence matters. Consider documenting the intended decoration order, or moving to an explicit middleware chain pattern, so Plan 4 doesn't accidentally wrap in the wrong order.

# Minor items worth fixing before merging:

The plugin struct in both skeletons is unexported and stateless, which is correct. But the postgres NewFactory closes the pool on migration failure (pool.Close()) — make sure StoreFactory.Close() is also safe to call if NewFactory never returned the factory. The current code looks correct (pool is closed before the factory is constructed), but add a comment noting the intentionality so a future refactor doesn't accidentally double-close.
The document references "session 39451fb7" and commit "a95cd24" as provenance for D1. These are useful for the team, but for a long-lived spec, consider adding a one-line summary of what those references contain so the document is self-standing even if chat session logs are lost.

# Verdict
Approve with revisions. The architecture is sound and the Go idioms are well-chosen. The critical items to address before implementation are: (1) specify the TM registry cleanup/reaper integration, (2) clarify the SET LOCAL RLS mechanism in the TM or store layer, (3) add context.Context to Plugin.NewFactory while the SPI is still pre-1.0, and (4) specify CI behavior under go.work. The remaining items are worth capturing but don't block.
