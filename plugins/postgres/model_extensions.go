package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	spi "github.com/cyoda-platform/cyoda-go-spi"
)

// lastSavepointSeq returns the seq of the most-recent savepoint row
// for ref, or 0 if no savepoint rows exist. Used by ExtendSchema to
// drive the savepoint trigger without a separate round-trip at
// extension time.
func (s *modelStore) lastSavepointSeq(ctx context.Context, ref spi.ModelRef) (int64, error) {
	var seq int64
	err := s.q.QueryRow(ctx, `
		SELECT seq FROM model_schema_extensions
		WHERE tenant_id = $1 AND model_name = $2 AND model_version = $3 AND kind = 'savepoint'
		ORDER BY seq DESC LIMIT 1`,
		string(s.tenantID), ref.EntityName, ref.ModelVersion).Scan(&seq)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return 0, nil
	case err != nil:
		return 0, fmt.Errorf("lastSavepointSeq: %w", err)
	default:
		return seq, nil
	}
}

// foldLocked returns the fully-folded schema for ref. It starts from
// the most recent savepoint row (if any), else from the caller-supplied
// baseSchema (models.doc.schema), and applies every subsequent delta
// row in seq order via the injected ApplyFunc.
//
// When no extensions exist yet, returns baseSchema verbatim. ApplyFunc
// is only required when at least one delta must be applied — this lets
// models with zero deltas read cleanly on factories where the apply
// function has not been wired yet.
//
// Note: the "Locked" suffix reflects the plan's terminology (fold under
// the caller's tx/read-view), not a mutex — there is no in-memory
// synchronization in this method.
func (s *modelStore) foldLocked(ctx context.Context, ref spi.ModelRef, baseSchema []byte) ([]byte, error) {
	var savepointSeq int64
	var savepointPayload []byte
	err := s.q.QueryRow(ctx, `
		SELECT seq, payload FROM model_schema_extensions
		WHERE tenant_id = $1 AND model_name = $2 AND model_version = $3 AND kind = 'savepoint'
		ORDER BY seq DESC LIMIT 1`,
		string(s.tenantID), ref.EntityName, ref.ModelVersion).Scan(&savepointSeq, &savepointPayload)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		savepointSeq = 0
		savepointPayload = nil
	case err != nil:
		return nil, fmt.Errorf("savepoint lookup: %w", err)
	}

	current := savepointPayload
	if current == nil {
		current = baseSchema
	}

	rows, err := s.q.Query(ctx, `
		SELECT payload FROM model_schema_extensions
		WHERE tenant_id = $1 AND model_name = $2 AND model_version = $3
		  AND kind = 'delta' AND seq > $4
		ORDER BY seq ASC`,
		string(s.tenantID), ref.EntityName, ref.ModelVersion, savepointSeq)
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
			if s.applyFunc == nil {
				return nil, fmt.Errorf("model has pending schema deltas but ApplyFunc is not wired on the factory — see cmd/cyoda/main.go")
			}
			first = false
		}
		current, err = s.applyFunc(current, spi.SchemaDelta(deltaBytes))
		if err != nil {
			return nil, fmt.Errorf("apply delta: %w", err)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("delta iteration: %w", err)
	}
	return current, nil
}
