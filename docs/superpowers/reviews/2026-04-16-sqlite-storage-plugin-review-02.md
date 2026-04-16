# architectural review of the proposed SQLite Storage Plugin design

## Executive Summary
Overall, this is a highly pragmatic, well-reasoned design. Choosing a CGO-free approach via ncruces/go-sqlite3 and explicitly planning for single-node embedded use cases perfectly bridges the gap between the ephemeral memory plugin and the heavy PostgreSQL plugin. The schema choices (STRICT, WITHOUT ROWID, INTEGER timestamps) demonstrate a strong understanding of SQLite-specific optimizations.

However, there is a critical architectural flaw regarding pagination and post-filtering that will cause performance issues or incorrect results if not addressed before implementation.

## 1. Critical Flaw: The Pagination & Post-Filtering Contradiction
The design states: "If a residual filter exists, the plugin applies it in Go on the SQL result set. Pagination (LIMIT/OFFSET) is applied after post-filtering to ensure correct counts".

Yet, the Query Templates section includes LIMIT ? OFFSET ? directly in the SQL.

The Problem:
If you have a residual filter (e.g., a regex match or case-insensitive search), you cannot push LIMIT and OFFSET to SQLite.

If you apply LIMIT 10 in SQL, but your residual Go filter drops 5 of those rows, you only return 5 rows to the user, breaking the requested limit of 10.

If you remove LIMIT/OFFSET from SQL to apply it in Go, a query like WHERE tenant_id = 'A' AND (residual_filter) could force SQLite to load millions of rows into Go memory just to return the first 10 matches.

Recommendation:
If a query contains residual filters, the SQLite plugin must use an explicit iterator/streaming cursor (Rows.Next()). It should stream rows from SQLite, apply the residual filter in Go row-by-row, maintain a running count to handle OFFSET, and close the rows cursor once the requested LIMIT is reached. Never use SQL LIMIT/OFFSET if a residual filter exists, and never load the entire un-paginated result set into memory.

## 2. Architecture & Concurrency Model
App-Layer SSI vs. SQLite Locking: Mirroring the memory plugin's SSI engine to maintain behavioral parity is a sound decision. It ensures the cyoda-go application layer behaves predictably regardless of the underlying plugin.

BEGIN IMMEDIATE for Flushes: The design correctly identifies the need for BEGIN IMMEDIATE during the commit flush phase. This is crucial. In WAL mode, if you start a standard BEGIN (which is a read transaction) and later try to write, you risk SQLITE_BUSY deadlocks if another connection is doing the same. Acquiring the write lock immediately prevents this.

## 3. Schema & Storage Optimization
UUIDs and Primary Keys: Utilizing WITHOUT ROWID on tables with composite text primary keys (tenant_id, entity_id) is excellent. This prevents SQLite from creating an unnecessary hidden 64-bit integer index, saving up to 20% in storage and eliminating a B-Tree lookup step.

JSON vs. JSONB: The design mentions waiting to verify if ncruces bundles a recent enough SQLite for JSONB. Recommendation: The ncruces/go-sqlite3 library tightly tracks current SQLite releases (it currently supports SQLite 3.46+). You can and should safely use the SQLite JSONB format immediately. Note that SQLite's JSONB is not a distinct data type like Postgres; it's a binary blob serialization. You would just use BLOB or TEXT as the column type, but insert using jsonb(?) and query using json_extract(), which auto-detects the binary format.

Case-Insensitive Searching: The design punts case-insensitive ops (ieq, icontains) to Go post-filtering due to Unicode correctness. While technically correct, this will severely penalize the performance of standard string searches. Recommendation: Consider if ASCII-only case insensitivity is acceptable for your domain. If it is, you can push down ieq using SQLite's COLLATE NOCASE or LOWER().

## 4. Query Planner & Search Pushdown
Greedy Dissection: The GreedyAndPlanner logic is elegant. Splitting the AND tree into pushable vs. residual filters is exactly how modern ORMs and custom query engines bridge the gap between domain logic and dialect-specific limitations.

Point-in-Time Query Optimization: The query provided for point-in-time search uses an INNER JOIN with a MAX(version) ... GROUP BY entity_id subquery.
```sql
-- Proposed in design
SELECT entity_id, MAX(version) AS max_ver
FROM entity_versions
WHERE tenant_id = ? AND submit_time <= ?
GROUP BY entity_id
```
On large tables, this grouping requires scanning significant amounts of the entity_versions index. Since SQLite 3.25.0 supports Window Functions, consider using ROW_NUMBER() OVER (PARTITION BY entity_id ORDER BY version DESC) or a correlated subquery, which can often be better optimized by the SQLite query engine if the index on (tenant_id, entity_id, version, submit_time) is structured perfectly.

## 5. Configuration & Pragmas
The provided pragmas are mostly ideal for a high-performance WAL setup:

PRAGMA journal_mode = WAL; (Great for concurrent readers)

PRAGMA synchronous = NORMAL; (Safe in WAL mode, much faster than FULL)

Missing Recommendation: Add PRAGMA wal_autocheckpoint = 1000; (or similar). Without managing the WAL checkpoint size, heavy write operations can cause the .wal file to grow infinitely, eventually degrading read performance before a graceful shutdown occurs.
