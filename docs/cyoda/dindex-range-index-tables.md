# Dynamic Entity Range Index Tables (`DINDEX_*`)

These three table families implement the **range query index** for dynamic entities stored in Cassandra.
They allow the platform to answer questions like _"find all entities of type X where field Y is between value A and B"_ — something Cassandra cannot do natively without a full scan.

---

## Background: The Problem

Cassandra partitions data by a hash of the partition key and can efficiently retrieve rows within a single partition using clustering columns. It cannot, however, do cross-partition range scans on arbitrary fields. To support range queries on entity fields, the platform maintains a purpose-built index stored in these tables, updated synchronously whenever an entity is written.

Every indexed field has its value range divided into fixed-size **buckets** (called _periods_). For example, with the default date period of 86 400 000 ms (one day), all dates within the same calendar day map to the same bucket. This bucketing bounds Cassandra partition sizes.

---

## Table 1 — `DINDEX_RMIDX_PARTITIONS` (the partitions registry)

```
PRIMARY KEY ( (entity_ref, shard, field_type), field_path, index_id, index_json )
```

**Purpose:** A lightweight registry that records _which_ indexed fields (and associated composite index configurations) have ever had data written for a given entity type, shard, and field data-type.

**How it is used:**

- Written once (upserted) whenever a new index entry is created for a previously unseen `(entity_ref, shard, field_path, index_id, index_json)` combination.
- Read during **physical deletion** (`IndexRecordsDeleterWithValues`) and **index clearing** (`BaseEntityCqlRangeQueryDao.clearIndex`) to enumerate which `RMIDX`/`RIDX` partitions need to be cleaned up without doing a full table scan.
- The `field_type` column (a `tinyint` enum value from `RangeType`) identifies which typed pair of tables (`_DATE_`, `_LONG_`, `_STRING_`, etc.) holds the data, so the correct typed table is targeted.

In short: **"For entity type T on shard S, here are all the field paths and index configs that have range-index data."**

---

## Table 2 — `DINDEX_<type>_RMIDX` (Range Meta-Index, the periods table)

Example: `DINDEX_DATE_RMIDX`, `DINDEX_LONG_RMIDX`, `DINDEX_STRING_RMIDX`, …

```
PRIMARY KEY ((entity_ref, shard, field_path, index_id, index_json), period_val)
```

**Purpose:** For a given indexed field (and optional composite index config), records the **set of period-bucket values** that have at least one entity stored in the corresponding `RIDX` table.

The `period_val` is computed by rounding the field's actual value down to its bucket boundary (e.g. `value - (value % period)`). Default bucket sizes are defined in `RangePeriod`:

| Type       | Default bucket size                  |
|------------|--------------------------------------|
| DATE / UUID | 1 day (86 400 000 ms)               |
| LONG        | 1 000 000 (milliseconds, typically) |
| INT / FLOAT / DOUBLE | 10 (units)               |
| STRING      | first 2 characters                  |

**How it is used:**

- Written (upserted) alongside each `RIDX` row when an entity is indexed.
- Read at **query time** to find which partition keys exist in `RIDX` before issuing range queries. The system scans only the RMIDX rows whose `period_val` overlaps the requested range, then queries the corresponding RIDX partitions. This avoids hitting empty Cassandra partitions.
- Read during **physical deletion and index clearing** to enumerate all period partitions to delete.

In short: **"For indexed field F on entity type T, these bucket-rounded values have at least one entry in the RIDX."**

---

## Table 3 — `DINDEX_<type>_RIDX` (Range Index, the actual reverse index)

Example: `DINDEX_DATE_RIDX`, `DINDEX_LONG_RIDX`, `DINDEX_STRING_RIDX`, …

```
PRIMARY KEY (
  (entity_ref, shard, field_path, period_val, index_id, index_json),
  value, entity_id, create_time, collection_keys, in_out_marker
)
CLUSTERING ORDER BY (value ASC, entity_id ASC, create_time ASC, …)
```

**Purpose:** The actual inverted index. Each Cassandra partition holds all entities whose indexed field value falls within one period bucket, sorted by the real field value. Given a value range `[A, B]`, a slice query on the clustering column `value` returns the matching `entity_id`s.

Extra clustering columns:
- **`entity_id`** — the entity's primary key.
- **`create_time`** (timeuuid) — the transaction timestamp, used to filter to a specific point-in-time view (only the latest version before a given timestamp is considered valid).
- **`collection_keys`** — when the indexed field is inside a collection (list/set/map), this encodes the position/key within the collection so distinct collection elements produce distinct rows.
- **`in_out_marker`** — distinguishes entries written for values _entering_ the index versus _leaving_ it; used in the consistency / rollback protocol.

**How it is used:**

- Written (inserted) when an entity is created or updated and its indexed field value changes.
- Deleted when an entity is updated (old value removed) or deleted entirely.
- Queried at **range query time**: for each period bucket identified via RMIDX, the DAO executes a CQL slice query `WHERE value >= ? AND value <= ?` within the partition, collects the `entity_id`s, and merges results across buckets and shards.

In short: **"Given entity type T, field F, and period bucket P, here are all entity IDs whose field value lies within that bucket, sorted by value."**

---

## How the Three Tables Work Together

```
Write path (entity saved):
  1. Compute period_val = round_down(field_value, period)
  2. INSERT into DINDEX_<type>_RIDX  (the actual index row)
  3. UPSERT into DINDEX_<type>_RMIDX (record that this period bucket exists)
  4. UPSERT into DINDEX_RMIDX_PARTITIONS (record that this field/index has data)

Query path (range query on a field):
  1. DINDEX_RMIDX_PARTITIONS  → (optional; used during cleanup, not query)
  2. DINDEX_<type>_RMIDX      → find all period_val buckets overlapping [from, to]
  3. DINDEX_<type>_RIDX        → for each bucket, slice by value range → collect entity_ids
```

### Typed variants

The `<type>` segment matches the Java/Cassandra type of the indexed field:
`DATE`, `DECIMAL`, `DOUBLE`, `FLOAT`, `INT`, `LOCAL_DATE`, `LOCAL_DATE_TIME`,
`LOCAL_TIME`, `LONG`, `STRING`, `UUID`, `VARINT`, `ZONED_DATE_TIME`.

Each type requires its own pair of tables because Cassandra's native ordering rules differ per CQL type, and the period-bucketing arithmetic is type-specific.

### `DINDEX_` vs `SINDEX_`

The `DINDEX_` prefix marks tables for **dynamic entities** (schema-flexible, sharded). An equivalent `SINDEX_` family exists for **static entities** (fixed-schema, no sharding, simpler partition keys).

