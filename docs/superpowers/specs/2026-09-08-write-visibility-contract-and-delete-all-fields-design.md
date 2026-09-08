# Write-visibility contract (`waitForConsistencyAfter` retired) and gRPC delete-all's inert fields

**Issue:** #501 (follow-up to #379) · **Milestone:** v0.8.4 · **Date:** 2026-09-08

## Problem

Two families of advertised-but-inert contract surface remain after #379:

1. **`waitForConsistencyAfter`** — a boolean query parameter declared on 7 entity write
   operations in `api/openapi.yaml` (`create`, `createCollection`, `updateCollection`,
   `updateSingle`, `updateSingleWithLoopback`, `patchSingle`, `patchSingleWithLoopback`),
   `default: false`, bound into the generated params structs, read by nothing. The help topic
   says "parsed but currently has no behavioural effect". No gRPC event carries an equivalent.
2. **gRPC `EntityDeleteAllRequest`** — `pageSize` (schema default 10), `pointInTime` and
   `verbose` (schema default false) are decoded and never used; the response's required
   `entityIds` is hard-coded to an empty list on both the batched and the single-transaction
   path.

Tracing the second family found the same defect on the HTTP door: `DELETE
/entity/{entityName}/{modelVersion}` with an empty body and no `transactionSize` takes a
whole-model fast path that ignores `pointInTime` (it deletes entities created after the
instant — a data-loss bug) and returns an empty `ids` list beside a non-zero count when
`verbose=true`.

Acceptance from the issue: each field is honored with real semantics, spec'd and tested, or
removed from the contract with a cloud-parity record; no silently-inert parameter remains.

## Settled (do not re-litigate)

- **The contract statement**, confirmed by the project owner: *a successful write response
  means the write is visible to subsequent reads on every node.* Unconditional; no opt-in.
- **Why Cloud has the flag and cyoda-go does not need it.** On Cyoda Cloud a new transaction
  reads at the consistency time captured when it was created, and concurrent transactions
  can hold that time below the first transaction's submit time; `waitForConsistencyAfter`
  polls after commit until the consistency time has passed the write, so that a *second*
  transaction can read what the first wrote. That is a mechanism for satisfying the contract
  above, and in cyoda-go it belongs inside the storage backend, not in the API.
- **In-tree backends meet the contract by construction.** memory, sqlite and postgres return
  from `Commit` only after the write is visible to point reads, listings, searches and
  point-in-time reads; nothing runs asynchronously after commit; there is no entity cache in
  front of the store (only a model-descriptor cache with bounded refresh); plain HTTP reads
  open no transaction and read the shared store. Multi-node postgres shares one store and
  keeps no node-local read state.
- **The commercial Cassandra plugin does not yet meet it** in three ways (chunked post-commit
  rollout visible to concurrent readers; commit success reported on a materialisation
  failure; a new transaction's snapshot can sit below an already-acknowledged commit on
  another node). Filed as Cyoda/cyoda-go-cassandra#97 with the fix direction. Out of scope
  here; the parity record states the obligations every backend must meet.
- v0.8.4 is a PATCH; backward compatibility is not a design constraint. Contract removals are
  declared under `### Breaking`.
- cyoda-go defines the contract; Cloud aligns (Gate 7).

## Design decisions

**D1 — Retire `waitForConsistencyAfter`; state the contract.** The guarantee the flag asks for
is unconditional in cyoda-go. A flag that toggles nothing cannot be honored, only advertised,
and advertising it is the fictional contract surface the issue forbids. Therefore:

- Remove the parameter from the 7 operations in `api/openapi.yaml`; regenerate
  `api/generated.go`. No gRPC schema changes (no such field exists).
- A request that still carries the parameter — with any value, including a non-boolean —
  is accepted and the parameter is ignored. Unknown query parameters are not validated on
  the production HTTP path (oapi-codegen binds by name; the only OpenAPI validator in the
  tree is the e2e response validator). Note the one observable change: a malformed value
  (`?waitForConsistencyAfter=maybe`) was `400 BAD_REQUEST` and is now ignored.
- New `docs/cloud-parity/write-visibility-contract.md` states the contract, the three
  obligations a backend must meet (commit returns after every read path can see the write;
  no post-commit rollout is observable by any reader; a new transaction's snapshot is never
  below an acknowledged commit), the multi-node basis for the in-tree backends (one shared
  store, no node-local read state), and the alignment consequence for Cloud: its HTTP write
  path must satisfy the contract without a client opt-in — the wait becomes unconditional
  — or the divergence is recorded on the Cloud side.
- `oasdiff` classifies a removed query parameter as a **warning**
  (`request-parameter-removed`), and the CI gate fails only on errors. Verified against the
  CI-pinned v1.21.0 and the local build: `7 changes: 0 error, 7 warning`. No ignore-list
  entries are added — an entry that never matches would itself be fiction.

Rejected: keeping the parameter as a documented always-true no-op "for client portability".
It keeps the fictional surface, and portability is already served by the tolerant reader.

**D2 — One routing rule for the delete fast path, on both doors.** In
`DeleteEntitiesConditional`, the whole-model `DeleteAllEntities` fast path is taken only when
the request needs nothing per entity: no condition, no `transactionSize`, no `pointInTime`,
and `verbose=false`. A request that carries `pointInTime` or `verbose=true` goes through the
existing single-transaction enumerate-then-delete path with a nil condition (the zero-value
selection plan already selects every entity). Semantics are exactly those of the conditional
path today:

- `pointInTime` selects the entities that existed at that instant in committed state (SPI
  `IterateOptions.PointInTime`, committed-only, the ambient transaction ignored — the same
  carve-out `docs/cloud-parity/tx-aware-search.md` records) and deletes their current rows.
  An id that is already gone is recorded in `deleteResult.idToError`. A future instant selects
  the current committed state. **Behavioural note for joined requests:** a compute-node
  callback that joined the transaction, created entities, and then issues an unconditional
  delete *with* `pointInTime` no longer deletes its own buffered entities (they did not exist
  in committed state at any instant); without `pointInTime` the fast path still does. This
  is the conditional path's existing behaviour extended to the unconditional form, recorded
  in the parity file and pinned by a memory-backend unit test.
- `verbose` lists every id the delete **attempted** — the matched set. Ids whose delete
  failed appear both in the list and in `idToError`. This is what the conditional and
  batched paths already return and what Cloud returns; three shipped texts say "deleted" or
  "removed" ids and are corrected (OpenAPI `verbose` description, the JSON-schema
  `entityIds` description, the help topic line that also claims delete-all never lists ids).
- The gRPC handler passes `req.PointInTime` and `req.Verbose` through on both its paths and
  fills `entityIds` from `DeleteResult.IDs` (the batched path's streamed and one-pass
  branches both already collect ids when verbose). `numDeleted` stays the removed count.
- Ordering, error classes and the cycle guard are unchanged: the model-not-found check runs
  inside the transaction on both routes; `DELETE_NOT_CONVERGED` lives only in the
  `transactionSize` streamed branch, unreachable from a non-batched request.

**D3 — Remove `pageSize` from `EntityDeleteAllRequest`.** There is nothing for it to mean:
selection is streamed, and Cloud ignores the field too (its `transactionSize` drives both its
read page and its batch). Remove it from `docs/cyoda/schema/entity/EntityDeleteAllRequest.json`
and regenerate `api/grpc/events/types.go` (the field and its default injection disappear —
compile-breaking for Go importers constructing the struct, accepted pre-1.0 as for #379's
`TransactionSize`). A client still sending it is tolerated: the generated unmarshaller ignores
unknown fields. `verbose`'s `default: false` stays — the generated unmarshaller applies it
and D2 honors it, so it is not fictional. Recorded in
`docs/cloud-parity/transaction-control-params.md` next to the `TransactionSize` schema change.

**D4 — No new error codes.** `pointInTime`'s OpenAPI description on `deleteEntities` is
reworded from "defaults to the consistency time of the system" to "absent means the current
committed state" — there is no consistency time under this contract. The same "consistency
time" wording on every other point-in-time parameter (entity reads, listings, search,
statistics, history) and in the analytics help topic is reworded to "absent means the current
committed state" — description-only, one definition of an absent instant across the spec.

## Error / status table

| Endpoint | Status / code | When |
|---|---|---|
| 7 HTTP write ops | unchanged | `waitForConsistencyAfter` present with any value → ignored; normal outcome |
| `DELETE /entity/{name}/{ver}`, empty body, `pointInTime` | 200 | as-at selection; already-gone ids in `idToError`; future instant = current state |
| same, `verbose=true` | 200 | `ids` = attempted ids |
| same, model unknown | 404 `MODEL_NOT_FOUND` | inside the transaction, as today |
| same, invalid `pointInTime` | 400 `BAD_REQUEST` | existing binding error |
| same, commit conflict | 409 `CONFLICT` (retryable) | existing single-tx path |
| gRPC `EntityDeleteAllRequest` + `pointInTime` / `verbose` | success envelope | as-at selection; `entityIds` populated when verbose |
| same, model unknown | `CLIENT_ERROR` `MODEL_NOT_FOUND: …` | existing envelope class |
| same, malformed `pointInTime` | gRPC `InvalidArgument` "invalid payload" | decode failure, existing class |
| same, `pageSize` sent (removed) | success envelope | tolerated, ignored |

## Coverage matrix

**U** unit · **E** running-backend e2e (`internal/e2e`) · **P** cross-backend parity
(`e2e/parity`, registered in `registry.go`) · **G** gRPC (`internal/grpc`, envelope asserted).

| Scenario | U | E | P | G |
|---|---|---|---|---|
| Flag retired: all 7 ops accept `?waitForConsistencyAfter=true`, `=false` and `=maybe`; immediate GET sees the write | — | ✔ one test, 7 ops × 3 values | existing suites (every scenario is write-then-read) | n/a |
| Contract, multi-node: write on node A, read on node B (get, list, search) sees it | — | ✔ `TestMultiNode` (postgres, two nodes) | — | — |
| Unconditional delete honors `pointInTime`: entities created after T survive; already-gone id in `idToError` | ✔ routing rule | ✔ | ✔ `EntityDeleteAllPointInTime` | ✔ |
| Unconditional delete honors `verbose`: `ids`/`entityIds` = attempted set, counts unchanged | ✔ | ✔ | ✔ `EntityDeleteAllVerbose` | ✔ (single and batched paths) |
| Future `pointInTime` = current state | — | ✔ sub-case | — | — |
| Joined request + `pointInTime` does not delete same-tx buffered entities | ✔ memory backend | — | — | — |
| Fast path still taken when nothing per-entity is requested | ✔ | existing | existing | existing |
| 404 model unknown on the new route | — | ✔ | — | ✔ |
| 409 commit conflict on the new route | waiver: same single-tx path as conditional delete, covered by its existing conflict test | | | |
| Malformed `pointInTime` → 400 / `InvalidArgument` | — | ✔ | — | ✔ |
| `pageSize` sent by an old client → tolerated | ✔ decode | — | — | ✔ |
| oasdiff gate passes unchanged (warning class) | CI | | | |

Nothing concurrency-shaped is placed in the parity suite.

## Documentation and parity obligations (Gate 4 / Gate 7)

- `api/openapi.yaml`: remove the 7 declarations; reword `deleteEntities` `pointInTime` and
  `verbose` descriptions. `go generate ./api`.
- `docs/cyoda/schema/entity/EntityDeleteAllRequest.json`: drop `pageSize`;
  `EntityDeleteAllResponse.json`: `entityIds` description → attempted ids. Regenerate via
  `scripts/generate-events.sh`.
- `cmd/cyoda/help/content/crud.md`: remove the 7 flag lines; rewrite the `verbose` line of
  `deleteEntities` (attempted ids; delete-all lists them too); state `pointInTime` on the
  unconditional form.
- `docs/cloud-parity/write-visibility-contract.md` (new) + README row;
  `transaction-control-params.md` gains the `pageSize` removal;
  `entity-patch.md` drops the parameter name from its "accepted params" sentence.
- `e2e/externalapi/scenarios/00-endpoints.yaml`: delete the `waitForConsistencyAfter`
  vocabulary entry (nothing consumes `query_params`; the recon and scenario URLs that still
  send the flag stay as they are — they are Cloud-facing and cyoda-go ignores it).
- `CHANGELOG.md`: `### Breaking` — `waitForConsistencyAfter` removed from the 7 ops (any
  value now ignored, including malformed); `pageSize` removed from `EntityDeleteAllRequest`
  (generated Go type changes). `### Fixed` — unconditional delete honors `pointInTime` and
  `verbose` on both doors; `entityIds` populated on gRPC delete-all.
- `COMPATIBILITY.md`: untouched (no SPI change, no release/chart/pin change).
- No issue numbers in shipped source, comments, help, OpenAPI or schemas.

## Out of scope

- Cassandra plugin obligations (Cyoda/cyoda-go-cassandra#97).
- Cloud's one-response-per-chunk streaming on gRPC delete-all versus cyoda-go's single
  response (pre-existing shape difference, not part of #501).
- Scheduled transitions firing later by design; that is workflow timing, not visibility.
