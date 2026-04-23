package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	spi "github.com/cyoda-platform/cyoda-go-spi"
)

// foldLocked returns the fully-folded schema for ref. Starts from the
// most-recent savepoint payload (if any), else from the caller-supplied
// baseSchema (models.doc.schema), and applies every subsequent delta
// row in seq order via the injected ApplyFunc.
//
// When no extensions exist, returns baseSchema verbatim. ApplyFunc is
// only required when at least one delta must be applied.
//
// Mirrors postgres's foldLocked; dialect differences are minor (? vs
// $1 placeholders, and the sqlite extension table orders rows by the
// explicit seq column rather than a pg sequence).
func (s *modelStore) foldLocked(ctx context.Context, ref spi.ModelRef, baseSchema []byte) ([]byte, error) {
	return foldLockedOn(ctx, s.db, s.tenantID, s.applyFunc, ref, baseSchema)
}

// lastSavepointSeq returns the seq of the most-recent savepoint row
// for ref, or 0 if none exists. Task 17's savepoint-trigger logic
// consults this to decide whether (newSeq - lastSavepointSeq) crosses
// the configured interval.
func (s *modelStore) lastSavepointSeq(ctx context.Context, ref spi.ModelRef) (int64, error) {
	return lastSavepointSeqOn(ctx, s.db, s.tenantID, ref)
}

// lastSavepointSeqInTx is the tx-scoped variant of lastSavepointSeq —
// reads from the given tx's snapshot so the savepoint trigger sees
// rows written earlier in the same transaction.
func (s *modelStore) lastSavepointSeqInTx(ctx context.Context, tx *sql.Tx, ref spi.ModelRef) (int64, error) {
	return lastSavepointSeqOn(ctx, tx, s.tenantID, ref)
}

// foldLockedInTx is the tx-scoped variant of foldLocked. Identical in
// behaviour but reads all rows via the given tx so read-your-writes is
// honoured within an open transaction.
func (s *modelStore) foldLockedInTx(ctx context.Context, tx *sql.Tx, ref spi.ModelRef, baseSchema []byte) ([]byte, error) {
	return foldLockedOn(ctx, tx, s.tenantID, s.applyFunc, ref, baseSchema)
}

// sqliteQuerier is the intersection of *sql.DB and *sql.Tx needed by
// the fold/savepoint helpers. Lets foldLockedOn and lastSavepointSeqOn
// serve both the outer (connection-pool) and inner (tx) execution
// scopes without code duplication.
type sqliteQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func lastSavepointSeqOn(ctx context.Context, q sqliteQuerier, tenantID spi.TenantID, ref spi.ModelRef) (int64, error) {
	var seq int64
	err := q.QueryRowContext(ctx, `
		SELECT seq FROM model_schema_extensions
		WHERE tenant_id = ? AND model_name = ? AND model_version = ? AND kind = 'savepoint'
		ORDER BY seq DESC LIMIT 1`,
		string(tenantID), ref.EntityName, ref.ModelVersion).Scan(&seq)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return 0, nil
	case err != nil:
		return 0, fmt.Errorf("lastSavepointSeq: %w", err)
	default:
		return seq, nil
	}
}

func foldLockedOn(ctx context.Context, q sqliteQuerier, tenantID spi.TenantID, applyFunc ApplyFunc, ref spi.ModelRef, baseSchema []byte) ([]byte, error) {
	var savepointSeq int64
	var savepointPayload []byte
	err := q.QueryRowContext(ctx, `
		SELECT seq, payload FROM model_schema_extensions
		WHERE tenant_id = ? AND model_name = ? AND model_version = ? AND kind = 'savepoint'
		ORDER BY seq DESC LIMIT 1`,
		string(tenantID), ref.EntityName, ref.ModelVersion).Scan(&savepointSeq, &savepointPayload)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		savepointSeq = 0
		savepointPayload = nil
	case err != nil:
		return nil, fmt.Errorf("savepoint lookup: %w", err)
	}

	current := savepointPayload
	if current == nil {
		current = baseSchema
	}

	rows, err := q.QueryContext(ctx, `
		SELECT payload FROM model_schema_extensions
		WHERE tenant_id = ? AND model_name = ? AND model_version = ? AND kind = 'delta' AND seq > ?
		ORDER BY seq ASC`,
		string(tenantID), ref.EntityName, ref.ModelVersion, savepointSeq)
	if err != nil {
		return nil, fmt.Errorf("delta scan: %w", err)
	}
	defer rows.Close()

	first := true
	for rows.Next() {
		var deltaBytes []byte
		if err := rows.Scan(&deltaBytes); err != nil {
			return nil, fmt.Errorf("scan delta: %w", err)
		}
		if first {
			if applyFunc == nil {
				return nil, fmt.Errorf("model has pending schema deltas but ApplyFunc is not wired on the factory — see cmd/cyoda/main.go")
			}
			first = false
		}
		current, err = applyFunc(current, spi.SchemaDelta(deltaBytes))
		if err != nil {
			return nil, fmt.Errorf("apply delta: %w", err)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("delta iteration: %w", err)
	}
	return current, nil
}
