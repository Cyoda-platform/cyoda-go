# Review: SQLite Storage Plugin Design

## Overall Assessment
The design is coherent, well-scoped, and fits cleanly into the plugin pattern already established by the memory and postgres backends. The decision table does a good job of calling out alternatives instead of presenting choices as inevitable. Mirroring the memory plugin's application-layer SSI — rather than trying to lean on SQLite's native locking — is the right call: you already have a working SSI engine with tested semantics, and SQLite's single-writer lock gives you zero write concurrency at the engine level anyway, so there's nothing to gain from re-platforming the transaction model. The choice to use the SQLite plugin as the template for the PostgreSQL search-pushdown work (issue #37) is also sensible; SQLite gives you a clean venue to iterate on the query-planner + residual-filter split without a container round-trip in the test loop.
The sections below flag the things that would benefit from more clarity before the first PR lands. They're roughly sorted by how much they matter.
Substantive issues worth resolving before implementation

## 1. Commit serialization is implied but not stated. 
The transaction lifecycle table says "validate in Go, then BEGIN IMMEDIATE, then append to committedLog." For SSI to be correct, steps 2–4 (validate → flush → append to committedLog) must be under a single serialization point — otherwise two commits can both validate against an outdated log, both acquire the write lock sequentially, and the second one misses a conflict with the first. Please make the commit mutex explicit in the design, and spell out ordering: validate → BEGIN IMMEDIATE → apply rows → COMMIT → append to committedLog → release mutex. The committedLog append must happen after SQLite commit succeeds, otherwise a failed flush leaves the log ahead of persisted state.

## 2. Snapshot-read semantics need the "latest version ≤ snapshot" treatment. 
The "Read (in tx)" row says SELECT FROM entity_versions WHERE submit_time <= ?, but entity_versions contains the full version chain. You need MAX(version) GROUP BY entity_id (or an equivalent window/subquery), same as the point-in-time search template further down. Worth unifying these two code paths — they're the same query shape.

## 3. Read-your-writes + search pushdown is the unresolved hard case. 
For in-transaction reads, a correct implementation has to merge (a) the SQL pushdown result against the snapshot with (b) the in-memory buffer (inserts, updates, deletes of the current tx). With pushdown, a row that no longer matches the filter because of a buffered update, or a newly-buffered row that does match, must be handled. The doc doesn't address this. Options:

Apply pushdown for read-only / cross-tx searches only; fall back to no-pushdown for in-tx searches until buffer is empty. Simplest, correct, some perf loss.
Read all candidates (no LIMIT in SQL), apply buffer overlay in Go, then the residual filter, then paginate. Correct but expensive.
Force buffer flush before search. Breaks SSI semantics subtly (writes become visible to other txs pre-commit) — avoid.

Whichever you pick, call it out explicitly. This is the kind of thing the SSI conformance suite should have a test for.

## 4. LIKE predicates need escape handling. contains, starts_with, ends_with use LIKE '%' || ? || '%' etc. 
Without escaping %, _, and \ in the bound value, user input like 50% becomes a wildcard. Either preprocess the value (strings.ReplaceAll(v, "%", %) etc.) and add ESCAPE '\' to the SQL, or push these to instr(col, ?) > 0 / substr(col, 1, length(?)) = ? instead. Add fuzz cases to the planner tests.

## 5. NULL / 3VL mismatch between SQL and Go filter semantics. 
json_extract(data, '$.missing') != 'x' evaluates to NULL, which is filtered out by WHERE. But the memory plugin's Go filter likely treats missing-field as "not equal to 'x'" → true. Same issue for gt/lt/gte/lte. If you want parity, wrap with (json_extract(...) IS NOT NULL AND json_extract(...) != ?) or push the "is missing → treated as X" convention explicitly. This is a parity-test minefield; please add a conformance case per operator × (present / missing / null literal) and make sure results match memory.

## 6. JSON type coercion under STRICT. 
STRICT tables enforce column-level affinity, but json_extract returns whatever type the JSON value is. Comparisons like json_extract(data, '$.n') = ? with ? bound as Go int64 against a JSON 1.0 will return false under SQLite's type rules. Decide and document how filter values are normalized (e.g., always bind numbers as REAL; or strip trailing .0 in JSON on write). Also a good fuzz target.

## 7. Submit-time and snapshot-time monotonicity under clock skew. 
With the commit mutex from issue #1 in place, submit_time collisions between concurrent commits can't happen — the critical section serializes timestamp capture, and realistic commit latency puts consecutive time.Now().UnixMicro() values many µs apart. The real risk is non-monotonic wall clocks: NTP steps, VM pause/migrate, and leap-second smearing can all move time.Now() backwards between two commits, which breaks SSI's "committed after my snapshot" check. Fix is a one-liner inside the commit section:

```go
submitTime := max(clock.Now().UnixMicro(), lastSubmitTime + 1)
```

…where lastSubmitTime is kept on the txmanager struct (no atomics needed — the commit mutex already guards it). On startup, seed from SELECT MAX(submit_time) FROM entity_versions.
The same issue applies to snapshotTime captured at Begin(), which deliberately runs outside the commit mutex to allow concurrent begins. Two transactions can begin at the same µs and get identical snapshot times; that's harmless for reads (they see the same committed state) but it forces a decision about whether committedLog entries with submit_time == my_snapshot_time are visible to the reader or not. Either apply the same max(now, last+1) pattern to begin-time capture under a lightweight counter, or fix the convention as strict inequality (submit_time < snapshot_time is "in my snapshot") and document it. The transaction lifecycle table should state which.
Neither fix changes the schema — submit_time INTEGER (Unix microseconds) stays as specified. The change is purely in how the plugin sources timestamps from the clock.

## 8. submit_times table has no documented retention policy. 
The idx_submit_times_ttl index hints at one, but the design doesn't describe when rows are pruned. Every commit writes a row; over a year of a busy deployment this table grows without bound. Either specify the retention mechanism (background sweeper, retention window, foreign coupling to entity_versions GC) or drop the "ttl" from the index name.

## 9. Pagination correctness when residual filter is present. 

The design states: "If a residual filter exists, the plugin applies it in Go on the SQL result set. Pagination (LIMIT/OFFSET) is applied after post-filtering to ensure correct counts".

Yet, the Query Templates section includes LIMIT ? OFFSET ? directly in the SQL.

The Problem:
If you have a residual filter (e.g., a regex match or case-insensitive search), you cannot push LIMIT and OFFSET to SQLite.

If you apply LIMIT 10 in SQL, but your residual Go filter drops 5 of those rows, you only return 5 rows to the user, breaking the requested limit of 10.

If you remove LIMIT/OFFSET from SQL to apply it in Go, a query like WHERE tenant_id = 'A' AND (residual_filter) could force SQLite to load millions of rows into Go memory just to return the first 10 matches.

Add a scan budget (e.g., CYODA_SQLITE_SEARCH_SCAN_LIMIT, default 100k rows examined) so a pathologically selective residual filter can't turn one request into a full table scan. Return a distinguishable error when the budget is exhausted so callers can tighten their filter.

Update the design's two query templates explicitly — one template with LIMIT ? OFFSET ? for the residual-free path, one without for the streaming path. Don't leave it as conditional pagination inside a single template.

## 10. SearchOptions has no ordering. 
Templates hardcode ORDER BY entity_id. If the domain search API exposes ordering (by updated_at, by a data field, etc.), adding it later is a breaking SPI change. Either include OrderBy []OrderSpec now, or document that stable entity_id ordering is the permanent contract. The former is cheap now, expensive later.

## 11. WAL file size management. 
The pragma list sets journal_mode = WAL and synchronous = NORMAL correctly, but doesn't address on-disk WAL file growth. SQLite's default wal_autocheckpoint = 1000 already moves committed pages from the WAL back into the main database file, so pages don't accumulate indefinitely — that part is handled. What isn't handled is the .wal file shrinking on disk: auto-checkpoint resets the write position but doesn't truncate the file, so under bursty write load the WAL can balloon to hundreds of MB and stay there. Two additions close this:
```sql
PRAGMA journal_size_limit = 67108864;  -- cap .wal at 64 MB on idle
```

…plus a background goroutine that issues PRAGMA wal_checkpoint(TRUNCATE) periodically (every few minutes, or after N commits — whichever the plugin author prefers). The pragma caps idle size; the periodic truncate reclaims space promptly after write bursts.
There's a related hazard worth a sentence in the design: a long-running reader transaction holds the WAL snapshot open and prevents the checkpointer from advancing past it, which can cause WAL growth regardless of any pragma. The plugin should bound read-transaction lifetime (a timeout, or forced rollback on shutdown) so a forgotten Begin() without a matching Commit()/Rollback() doesn't silently pin the WAL.

## 12. TEXT vs JSONB
The design mentions waiting to verify if ncruces bundles a recent enough SQLite for JSONB. Recommendation: The ncruces/go-sqlite3 library tightly tracks current SQLite releases (it currently supports SQLite 3.46+). You can and should safely use the SQLite JSONB format immediately. Note that SQLite's JSONB is not a distinct data type like Postgres; it's a binary blob serialization. You would just use BLOB or TEXT as the column type, but insert using jsonb(?) and query using json_extract(), which auto-detects the binary format.

Schema: Store JSON columns as BLOB rather than TEXT. (The column type is mostly advisory under STRICT when the values are binary, but BLOB is the honest declaration.)
Writes: All JSON inserts/upserts route through jsonb(?) in the prepared statements.
Reads: All read queries that return the blob to application code wrap with json(data). json_extract() in pushdown stays unchanged (auto-detects format).
Startup: Assert sqlite_version() >= 3.45.0 on connection; fail with a clear error message otherwise.
Ops: Ship a *_readable view alongside each JSON-bearing table in the initial migration, so CLI inspection is SELECT * FROM entities_readable rather than binary garbage.

## Smaller concerns
**SPI stability of Filter.Value any and FieldSource int**. any makes Filter hard to round-trip over a wire or cache. Fine for a Go-only SPI today, but if Filter ever crosses an RPC boundary (e.g., a push-down-aware distributed backend) you'll want a tagged union. And type FieldSource int with iota constants is fragile — a reorder silently corrupts persisted filters. FilterOp is already string-based; make FieldSource string-based too for consistency and forward-compat.

**Error mapping**. The design names spi.ErrConflict. What about SQLITE_BUSY after busy_timeout expires, SQLITE_FULL, SQLITE_CORRUPT, SQLITE_READONLY? The plugin should map these to stable SPI errors with retry semantics documented.

**Migration locking**. CYODA_SQLITE_AUTO_MIGRATE=true with two processes starting against the same file will race golang-migrate. Single-node is the stated use case, but people put sidecars next to things. A flock on the DB file (or a dedicated lock file) around migrate is cheap insurance.

**WITHOUT ROWID update cost**. For entities (high-UPSERT table with large data TEXT), WITHOUT ROWID makes every update rewrite the full row in the clustered leaf. For entity_versions (append-only), it's unambiguously a win. Worth benchmarking entities with and without — you may find the current-state table is better off with rowid, while version history keeps WITHOUT ROWID.

**TEXT → JSONB migration is not transparent**. SQLite 3.45+ introduced separate jsonb_extract(). Switching storage format requires both a migration and changing every query. Worth pinning the ncruces/go-sqlite3 embedded SQLite version explicitly, asserting it at startup with a clear error message, and deferring JSONB to a follow-up issue rather than hinting at transparent migration.

**Non-transactional writes**. "Direct INSERT/UPSERT under SQLite transaction, no SSI tracking" — for which code paths? Bootstrap? Stats? Audit? List them. Anything that touches tables also read by transactional reads needs a documented isolation story.

**VACUUM / auto_vacuum**. Not mentioned. With heavy entity_versions churn plus retention, free pages accumulate. PRAGMA auto_vacuum=INCREMENTAL at DB creation time plus a periodic PRAGMA incremental_vacuum(N) in a background goroutine is a small addition with real benefit. auto_vacuum mode must be set before the first table is created, so this is a day-one decision.

**Driver performance note**. ncruces/go-sqlite3 (WASM) is typically ~2–3x slower than mattn/go-sqlite3 (CGO). The non-CGO tradeoff is worth making, but the design document is the right place to cite the rough cost and say "we accept this for deploy simplicity and the sqlite-vec roadmap."
Testing strategy — additions worth making
The four-layer test plan (planner unit / conformance / parity / searcher integration) is solid. Consider adding:

**A property/fuzz suite over the planner**: generate random Filter trees and assert that SQL(filter) + residual(filter) applied to a shared dataset matches Go-only(filter). This catches the LIKE-escaping and 3VL issues above.
A crash-recovery test: start, write, SIGKILL, restart, verify persisted state matches the last successful commit and no partial write is visible.

**A concurrency stress test** (N goroutines, random reads/writes, half conflicting) that verifies conflict rate, throughput, and absence of lost writes. Use race detector.

## Questions for the author

- Is there a single commit-phase mutex in the memory plugin, or is serialization derived from something else (atomic counters, channel)? The SQLite plugin should mirror whatever pattern's already proven.
- How does the memory plugin handle read-your-writes today when a search is issued inside a tx with buffered writes? Whatever it does is the target for parity.
- Is Filter's anti-corruption layer meant to stay Go-only forever, or is there a plausible future where it serializes (for remote plans, caching, etc.)? That answers the any / FieldSource int concerns.
- What's the retention policy for entity_versions and submit_times — unbounded, time-windowed, or tied to a per-model retention config? The SQLite schema is downstream of that decision.
- Should ieq / icontains / etc. be defined at the SPI level as ASCII-only, Unicode-correct, or locale-dependent? Current design defaults to Go post-filtering to preserve Unicode correctness, which forecloses SQL pushdown for these operators. If the SPI contract allows ASCII-only semantics, COLLATE NOCASE + LOWER() becomes pushable. Requires a decision from the domain layer owners before the plugin can optimize this path.
