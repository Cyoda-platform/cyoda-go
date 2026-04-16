# Design Review: Postgres SI + Row-Granular First-Committer-Wins

## Overall Assessment
This is a well-written, carefully reasoned design. The core approach — downgrading from SERIALIZABLE to REPEATABLE READ and implementing application-level first-committer-wins via commit-time version validation — is sound and well-matched to the stated goal of behavioral parity with the Cassandra plugin. The motivation is clearly articulated, the scope is appropriately bounded, and the non-goals are honest about deferred work.
That said, I have a few concerns ranging from a potential correctness gap to operational considerations.

## 1. Correctness: FOR SHARE under RR and the 40001 claim
The design states:

Under RR, if that row was modified by a concurrent committed tx after our snapshot, postgres raises ERROR: could not serialize access due to concurrent update (SQLSTATE 40001) automatically.

This is correct per the Postgres docs. Under Repeatable Read, SELECT ... FOR SHARE will block if the target row has an uncommitted concurrent modification, and will raise 40001 if that concurrent transaction commits a modification before our lock attempt resolves. This is the standard RR write-conflict detection behavior applied to locking reads. So the mechanism the design relies on is valid.
However, there's a subtlety worth noting: the 40001 from FOR SHARE fires only when a concurrent transaction has modified the row. If a concurrent transaction merely read the same row (even with FOR SHARE), there's no conflict — FOR SHARE locks are compatible with each other. This is exactly the desired behavior for first-committer-wins, so it's correct here, but it's worth calling out in implementation comments so future readers understand why FOR SHARE (not FOR UPDATE) was chosen.

## 2. Potential race: validate-then-commit window under FOR SHARE
The design uses FOR SHARE rather than FOR UPDATE. This means the validate-then-commit window is protected against concurrent writes to the same rows, but a third transaction could still acquire its own FOR SHARE lock on the same rows concurrently.
Consider: Tx A and Tx B both read entity X at version 5, both write it, and both enter the commit phase simultaneously. Both issue SELECT ... FOR SHARE. Since FOR SHARE is compatible with FOR SHARE, both succeed, both see version 5, both validate successfully, and both call pgxTx.Commit(). At the Commit point, the actual UPDATE statements were already executed earlier in the transaction under RR, so the second committer's UPDATE would trigger Postgres's own RR concurrent-update detection — but only if both transactions wrote to the same row.
This scenario is actually handled correctly because: (a) the writes already happened during the tx, meaning Postgres holds row-level exclusive locks from the UPDATE/INSERT statements themselves, and (b) under RR, a second transaction trying to update a row already modified by a concurrent in-progress transaction will block on the row lock, not on the FOR SHARE lock.
So the FOR SHARE really serves as a read-set validator, and write-write conflicts on the same row are caught by Postgres's own tuple-level locking from the earlier UPDATE statements. This is fine, but the design should explicitly state this dual-mechanism argument — that write-set conflicts are caught by Postgres's inherent row locks from the DML, while FOR SHARE catches read-set staleness. Right now the document somewhat blurs these two mechanisms together.

## 3. Insert conflict detection gap
The design says:

Write-set: expected pre-write version must equal current; version 0 (new insert) requires the row to not yet exist in current.

For inserts, writeSet[id] = 0 means "this entity didn't exist when I wrote it." At commit time, the validation checks that the entity ID is absent from the SELECT ... FOR SHARE results. But there's a subtle issue: if two transactions concurrently insert the same entity ID, the SELECT ... FOR SHARE at commit time will find the row only if the other transaction has already committed. If both are in-flight:

Tx A inserts entity X → row exists with a tuple lock
Tx B inserts entity X → this would already fail at INSERT time with a unique constraint violation (assuming (tenant_id, id) is unique), not at commit-time validation

So the design is correct in practice if the entities table has a unique constraint on (tenant_id, id), which it presumably does. But this should be explicitly stated as a precondition, since the commit-time SELECT ... FOR SHARE path alone wouldn't catch concurrent inserts of the same ID if both are still uncommitted.

## 4. Deleted rows in the read-set
The design correctly handles the case where a concurrent committer deletes a row that's in our read-set:

Missing current (deleted by concurrent committer) → conflict.

But this depends on deletes being "soft deletes" (the row still exists with a bumped version) or on the delete being committed before our SELECT ... FOR SHARE runs. If deletes are hard deletes (DELETE FROM entities WHERE ...), a committed hard delete would cause the row to be absent from the FOR SHARE results. The design mentions "soft delete bumps version" in the store-layer table, which is good — but it's worth confirming that all entity deletion paths go through soft delete. If any path does a hard DELETE, the FOR SHARE approach degrades: the row simply vanishes from the result set, which the read-set validator would catch as "missing = conflict," so it's still correct but the error message/path differs.

## 5. Performance: single batched query is good, but watch cardinality
The commit-time validation issues a single SELECT id, version FROM entities WHERE tenant_id = $1 AND id = ANY($2) FOR SHARE. This is a good design — one round-trip, batch processing. A few operational notes:

Large read/write sets: If a transaction touches hundreds or thousands of entities, the ANY($2) array could get large. Postgres handles ANY(array) well up to a few thousand elements, but beyond that, query planning can degrade. Consider documenting an upper-bound expectation or, for defense, chunking the validation if len(ids) > N.
Index usage: The query should hit an index on (tenant_id, id). If the primary key or a unique index covers this, the FOR SHARE scan is efficient. Worth confirming this in the implementation PR.
Lock acquisition ordering: When many concurrent transactions validate at the same time, FOR SHARE locks are acquired in whatever order the index scan returns rows. Since FOR SHARE is compatible with FOR SHARE, this doesn't cause deadlocks by itself. But if a transaction also holds row-exclusive locks from prior writes, and another transaction's FOR SHARE needs to wait on those, there's potential for deadlock if the lock acquisition order differs between transactions. Postgres will detect this and raise 40P01, which classifyError already maps to ErrConflict — so the behavior is correct, but deadlock-induced aborts could become a performance concern under high contention. Sorting ids before issuing the query would make lock acquisition deterministic and eliminate this class of deadlock.


## 6. Savepoint semantics: intentional divergence from Cassandra
The design notes:

This diverges from cassandra's "clear read-set on savepoint" semantic — this divergence is intentional because postgres tracks row visibility natively and the read-set in this plugin is purely commit-validation bookkeeping.

This is a reasonable decision, but it means the plugins are no longer perfectly behavioral-parity on savepoints. The conformance test suite (spitest) should include a savepoint-specific concurrency test that documents the expected behavior difference, or the spitest framework should allow plugin-specific expected results for savepoint edge cases. Otherwise a future contributor might see this divergence and treat it as a bug.

## 7. GetAsAt and GetVersionHistory not tracked — correct, but document the threat model
The decision not to track GetAsAt (point-in-time reads) and GetVersionHistory makes sense: these are reads of immutable historical data that don't participate in first-committer-wins. However, if application logic reads a historical version via GetAsAt and then makes a decision that leads to a write in the same transaction, that decision is based on data that isn't validated at commit time. This is acceptable under the stated semantic (only live-row reads are tracked), but it's worth adding a one-line comment in the code to prevent a future developer from "fixing" this by adding tracking.

## 8. Minor: cleanupTx on error paths
The commit code calls tm.cleanupTx(txID) followed by pgxTx.Rollback(context.Background()) on error paths. The order seems fine, but if cleanupTx removes the txState entry before Rollback completes, and Rollback itself triggers any callback that consults txState, there could be an issue. This is likely not a real problem given the architecture, but it's worth confirming that Rollback doesn't reference txState anywhere.
Also, using context.Background() for the rollback (rather than the original ctx) is correct — you don't want a cancelled context to prevent cleanup. Good.

## 9. Test strategy is strong
The test list is comprehensive. A few additions I'd suggest:

TestCommit_DeletedEntityConflict: Tx A reads entity X; concurrent Tx B deletes X (soft delete); A's commit detects the version bump from the delete.
TestCommit_LargeReadSet: Stress test with a transaction reading 500+ entities to verify the batched query performs acceptably.
TestCommit_SavepointRollbackThenConflict: A transaction reads entity X, takes a savepoint, reads entity Y, rolls back to savepoint (dropping Y from the read-set), then a concurrent tx modifies Y. The commit should succeed because Y is no longer in the validated read-set.


## Summary
The design is solid. The core mechanism correctly leverages Postgres RR's built-in concurrent-update detection via FOR SHARE to implement row-granular first-committer-wins, and the explicit version validation catches the cases that Postgres's native checks wouldn't (e.g., read-set staleness without a write attempt on that row). The main items I'd want addressed before or during implementation:

Explicitly document the dual mechanism (Postgres row locks for write-write conflicts, FOR SHARE + version check for read-set validation) so reviewers and future maintainers understand the full safety argument.
Add a precondition note about the (tenant_id, id) unique constraint being required for insert-conflict correctness.
Sort ids before the validation query to ensure deterministic lock acquisition order and prevent deadlocks under contention.
Document the savepoint divergence from Cassandra in the conformance test suite, not just in this design doc.
Consider the additional test cases listed above.

None of these are blockers — the design is ready for implementation planning as stated. These are refinements that would strengthen the implementation and its long-term maintainability.
